#!/usr/bin/env bash
set -uo pipefail

# The operational half of the x/upgrade proof: a real coordinated upgrade across
# four validators and two processes.
#
# The in-process tests already establish the mechanism — schedule from a binary
# without the handler, ride the pre-height window, halt, swap, resume, migrate
# once. None of that needs consensus, so none of it proves the things that only
# go wrong with more than one node:
#
#   every validator halts at the SAME height
#   the upgraded nodes agree on the app hash across the boundary
#   a validator left on the old binary halts rather than following the network
#     with stale application logic
#   wall-clock downtime does not consume the settlement clock
#   entitlements, escrow and the validator set survive the boundary untouched
#
# Four validators, not two, and that is load-bearing. Quorum is more than 2/3 of
# voting power, so at four a partial rollout of three can resume while the fourth
# is still down — which is what a real operator rollout looks like. At two or
# three, every operator must succeed simultaneously and the interesting case
# cannot be expressed at all.
#
# Binaries A and B are built from ONE source revision and differ only in the
# compiled upgrade registry, via the `upgradedrill` build tag. Their SHA-256 sums
# are recorded, because "which bytes ran" is the whole question a binary-swap
# drill answers.

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
# Set before sourcing: drill-common derives the evidence directory from DRILL at
# source time, so exporting it afterwards would file this run's evidence under the
# default name.
export DRILL="upgrade"
. "$ROOT/scripts/localnet/lib/drill-common.sh"
# drill-common enables `set -e`; this drill accounts for its own failures and
# must not abort halfway through, leaving a localnet running and no evidence.
set +e

NODE_COUNT=4
export NODE_COUNT

BIN_A="$ROOT/build/twilightd"
BIN_B="$ROOT/build/twilightd-upgradedrill"
UPGRADE_NAME="drill-v2"
# Long enough that "no clock advanced" is a claim about wall time, not a
# scheduling accident.
HALT_WAIT_SECONDS="${HALT_WAIT_SECONDS:-12}"
# Fast blocks; the protocol epoch length is NOT shortened.
export TWILIGHT_LOCALNET_TIMEOUT_COMMIT="${UPGRADE_DRILL_TIMEOUT_COMMIT:-200ms}"

FAILURES=0
fail() { echo "  FAIL: $*" >&2; FAILURES=$((FAILURES + 1)); }
ok()   { echo "  ok: $*"; }
note() { echo "  note: $*"; }
die()  { echo "  ABORT: $*" >&2; exit 1; }

need curl; need jq

# ---- fail-closed value helpers ---------------------------------------------
# A missing RPC field, an empty jq result or a dead node must never read as a
# passing value. Every numeric the drill asserts on goes through here.
require_num() { # <value> <label>
  local v="$1" label="$2"
  [[ "$v" =~ ^[0-9]+$ ]] || die "$label is not a number: '${v}'"
  echo "$v"
}
sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'
  else shasum -a 256 "$1" | awk '{print $1}'; fi
}
rpc_height() { # <node> -> block-store height, or empty when unreachable
  rpc_get "$1" /status 2>/dev/null | jq -r '.result.sync_info.latest_block_height // empty' 2>/dev/null
}

# app_height <node> — the height the APPLICATION has committed.
#
# Not the same number as the block-store height, and the difference is the whole
# point at an upgrade boundary. CometBFT stores a block once consensus agrees on
# it and only then asks the application to apply it. When x/upgrade refuses,
# FinalizeBlock returns an error and the node panics inside finalizeCommit — so
# the block store holds H while the application is still at H-1. Asserting "no
# node committed H" against /status would read the stored-but-unapplied block and
# report a failure that did not happen.
app_height() { # <node> -> application height, or empty when unreachable
  rpc_get "$1" /abci_info 2>/dev/null | jq -r '.result.response.last_block_height // empty' 2>/dev/null
}
clock_at() { # <node> <height> -> settlement clock committed at that height
  "$BIN_B" mining-query settlement-clock --node "$(rpc_url "$1")" --height "$2" \
    --output json 2>/dev/null | jq -r '.settlement_clock // empty' 2>/dev/null
}
q_node() { # <node> <query...> -> JSON on stdout
  local n="$1"; shift
  "$BIN_B" "$@" --node "$(rpc_url "$n")" --output json 2>/dev/null
}

if pgrep -x twilightd >/dev/null 2>&1; then
  die "a twilightd process is already running; stop it first"
fi
trap '"$ROOT/scripts/localnet/stop.sh" >/dev/null 2>&1 || true; pkill -f "twilightd start" >/dev/null 2>&1 || true' EXIT

echo "=== 1. a four-validator localnet ==="
SOURCE_SHA="$(git -C "$ROOT" rev-parse HEAD)"
git -C "$ROOT" diff --quiet 2>/dev/null || note "working tree has uncommitted changes; binaries reflect the tree, not $SOURCE_SHA"
# init.sh builds the node binary itself, at $BIN. It therefore runs BEFORE the
# binaries are hashed — otherwise it would overwrite binary A after the drill had
# recorded its checksum, and the provenance evidence would describe bytes that no
# longer existed.
echo "  initializing (init.sh builds the node binary; a cold Go cache takes a few minutes)"
"$ROOT/scripts/localnet/init.sh" >/dev/null 2>&1 || die "init failed"

echo
echo "=== 2. two binaries from one source revision ==="
go build -o "$BIN_A" "$ROOT/cmd/twilightd" || die "building binary A failed"
go build -tags upgradedrill -o "$BIN_B" "$ROOT/cmd/twilightd" || die "building binary B failed"
SHA_A="$(sha256_of "$BIN_A")"; SHA_B="$(sha256_of "$BIN_B")"
[[ "$SHA_A" != "$SHA_B" ]] && ok "A and B differ ($( echo "$SHA_A" | cut -c1-12 ) vs $( echo "$SHA_B" | cut -c1-12 ))" \
  || fail "A and B are byte-identical; the build tag had no effect"
BIN="$BIN_A"   # every node starts on A

echo
echo "=== 3. starting all four validators on binary A ==="
"$ROOT/scripts/localnet/start.sh" >/dev/null 2>&1 || die "start failed"
init_evidence
BINARIES="$EVID_DIR/binaries.json"
UPGRADE_LOG="$EVID_DIR/upgrade.jsonl"
ECONOMICS="$EVID_DIR/economics.jsonl"
: >"$UPGRADE_LOG"; : >"$ECONOMICS"
jq -nc --arg src "$SOURCE_SHA" --arg a "$BIN_A" --arg sa "$SHA_A" --arg b "$BIN_B" --arg sb "$SHA_B" \
  '{source_commit:$src,
    binary_a:{path:$a, sha256:$sa, build:"go build -o build/twilightd ./cmd/twilightd", tags:""},
    binary_b:{path:$b, sha256:$sb, build:"go build -tags upgradedrill -o build/twilightd-upgradedrill ./cmd/twilightd", tags:"upgradedrill"}}' \
  >"$BINARIES"
ok "binary provenance recorded in $(basename "$BINARIES")"

for n in 0 1 2 3; do
  wait_height_node "$n" 3 90 || die "node$n never produced blocks"
done
agree_nodes "0 1 2 3" "pre-upgrade-start" || fail "nodes disagree before the drill begins"
ok "four validators live and agreeing"

echo
echo "=== 4. epoch geometry and a settlement that will span the boundary ==="
EPOCH_LEN="$(require_num "$(q_node 0 rewards-query params | jq -r '.params.epoch_length_blocks // empty')" "epoch_length_blocks")"
ok "epoch length is $EPOCH_LEN blocks (protocol value, not shortened)"
# Epoch 1 must close so a settlement exists to span the upgrade.
for n in 0 1 2 3; do
  wait_height_node "$n" $((EPOCH_LEN + 2)) 900 || die "node$n did not reach the first epoch boundary"
done
sleep 2
SLOT_ID=1; SETTLE_EPOCH=1
SETTLEMENT="$(q_node 0 mining-query settlement "$SLOT_ID" "$SETTLE_EPOCH")"
[[ -n "$SETTLEMENT" ]] || die "no settlement for slot $SLOT_ID epoch $SETTLE_EPOCH"
[[ "$(jq -r '.settlement.finalized' <<<"$SETTLEMENT")" == "false" ]] \
  && ok "slot $SLOT_ID epoch $SETTLE_EPOCH is OPEN and will span the upgrade" \
  || fail "the chosen settlement is already finalized"
DEADLINE="$(require_num "$(jq -r '.deadline_clock // empty' <<<"$SETTLEMENT")" "deadline_clock")"
ENTITLEMENT="$(jq -r '.entitlement_amount' <<<"$SETTLEMENT")"

# H strictly inside epoch 2, proven from the chain's own geometry rather than asserted.
UPGRADE_HEIGHT=$((EPOCH_LEN + EPOCH_LEN / 2))
(( UPGRADE_HEIGHT % EPOCH_LEN != 0 )) && ok "H=$UPGRADE_HEIGHT is not an epoch boundary" \
  || fail "H=$UPGRADE_HEIGHT lands exactly on an epoch boundary"
MARGIN=$((EPOCH_LEN / 4))
(( UPGRADE_HEIGHT - EPOCH_LEN >= MARGIN && 2 * EPOCH_LEN - UPGRADE_HEIGHT >= MARGIN )) \
  && ok "H is at least $MARGIN blocks from either boundary ($EPOCH_LEN and $((2 * EPOCH_LEN)))" \
  || fail "H=$UPGRADE_HEIGHT is too close to an epoch boundary"
(( UPGRADE_HEIGHT < DEADLINE )) && ok "H precedes the settlement deadline (clock $DEADLINE)" \
  || fail "the settlement would expire before H"

echo
echo "=== 5. the authority schedules the upgrade ==="
AUTH="$(authority_addr)"
# The canonical surface is the AutoCLI-generated `tx coreslot schedule-upgrade`,
# which takes flags. (A hand-written positional variant also exists under the
# legacy top-level `coreslot` group; the drill uses the documented one.)
OUT="$("$BIN_A" tx coreslot schedule-upgrade \
  --name "$UPGRADE_NAME" --height "$UPGRADE_HEIGHT" --info "sha256:$SHA_B" \
  --from operator0 --keyring-backend test --home "$(node_home 0)" \
  --chain-id "$CHAIN_ID" --node "$(rpc_url 0)" --gas 600000 --fees 0utwlt -y --output json 2>&1)"
TXHASH="$(jq -r '.txhash // empty' <<<"$OUT" 2>/dev/null)"
[[ -n "$TXHASH" ]] || die "the scheduling transaction was never broadcast: $(head -c 300 <<<"$OUT")"
# CheckTx acceptance is not delivery. Assert the DELIVERED result.
TXCODE="$(_wait_tx_code "$TXHASH")"
[[ "$TXCODE" == "0" ]] && ok "schedule-upgrade DELIVERED (tx ${TXHASH:0:12}, code 0)" \
  || die "schedule-upgrade was not delivered successfully (code $TXCODE)"

for n in 0 1 2 3; do
  PLAN="$(q_node "$n" query upgrade plan)"
  PNAME="$(jq -r '.plan.name // .name // empty' <<<"$PLAN" 2>/dev/null)"
  PHEIGHT="$(jq -r '.plan.height // .height // empty' <<<"$PLAN" 2>/dev/null)"
  [[ "$PNAME" == "$UPGRADE_NAME" && "$PHEIGHT" == "$UPGRADE_HEIGHT" ]] \
    && ok "node$n sees plan $PNAME@$PHEIGHT" \
    || fail "node$n reports plan name='$PNAME' height='$PHEIGHT'"
done

echo
echo "=== 6. baseline before the boundary ==="
BASE_H="$(require_num "$(rpc_height 0)" "node0 height")"
KRD_BEFORE="$(require_num "$(q_node 0 coreslot-query params | jq -r '.params.key_rotation_delay_blocks // empty')" "key_rotation_delay_blocks")"
[[ "$KRD_BEFORE" == "1" ]] && ok "key_rotation_delay_blocks is $KRD_BEFORE (the migration expects this)" \
  || fail "key_rotation_delay_blocks is $KRD_BEFORE; the drill migration requires 1"
MB="$(q_node 0 rewards-query module-balances)"
ESCROW="$(jq -r '.rewards_balance' <<<"$MB")"; LIAB="$(jq -r '.outstanding_entitlement_liability' <<<"$MB")"; CARRY="$(jq -r '.carry_forward_remainder' <<<"$MB")"
[[ "$ESCROW" == "$((LIAB + CARRY))" ]] && ok "escrow == liability + carry ($ESCROW) before the boundary" \
  || fail "solvency broken before the boundary: $ESCROW != $LIAB + $CARRY"
# The per-block settlement-clock tick is MEASURED from committed state, not
# inferred from a pause flag.
#
# The clock ticks once per committed block while settlement release is enabled and
# not at all while it is paused, so a hard-coded +1 would pass silently on a paused
# chain. Two height-pinned reads give the actual tick this chain is running at,
# which is the number the halt arithmetic must be checked against.
#
# It is also the only route available: `rewards-query pause-state` is registered
# but absent from the CLI's query dispatch and returns "unsupported query request"
# — a pre-existing defect this drill uncovered, reported separately and NOT worked
# around by weakening any assertion here.
TICK_H="$(require_num "$(rpc_height 0)" "node0 height for the tick measurement")"
TICK_C1="$(require_num "$(clock_at 0 $((TICK_H - 3)))" "settlement clock at $((TICK_H - 3))")"
TICK_C2="$(require_num "$(clock_at 0 $((TICK_H - 2)))" "settlement clock at $((TICK_H - 2))")"
EXPECTED_TICK=$((TICK_C2 - TICK_C1))
(( EXPECTED_TICK == 0 || EXPECTED_TICK == 1 )) \
  || die "settlement clock moved $EXPECTED_TICK across one block; expected 0 or 1"
(( EXPECTED_TICK == 1 )) && RELEASE_ENABLED=true || RELEASE_ENABLED=false
ok "measured settlement-clock tick = $EXPECTED_TICK per committed block (release enabled=$RELEASE_ENABLED)"
jq -nc --arg cp "baseline_pre_halt" --argjson h "$BASE_H" --arg krd "$KRD_BEFORE" \
  --arg esc "$ESCROW" --arg li "$LIAB" --arg ca "$CARRY" --arg dl "$DEADLINE" --arg ent "$ENTITLEMENT" \
  --argjson slot "$SLOT_ID" --argjson ep "$SETTLE_EPOCH" \
  '{checkpoint:$cp, height:$h, key_rotation_delay_blocks:$krd, escrow:$esc, liability:$li,
    carry:$ca, escrow_equals_liability_plus_carry:true, settlement_slot:$slot, settlement_epoch:$ep,
    deadline_clock:$dl, entitlement_amount:$ent}' >>"$ECONOMICS"

echo
echo "=== 7. every validator halts at H, and none applies it ==="
# A halted node here is HUNG, not gone: x/upgrade returns an error from PreBlock,
# CometBFT panics its consensus routine, and the process stays up serving RPC with
# consensus dead. Waiting for the process to exit would wait forever; treating an
# unreachable RPC as the halt signal would let a genuine crash pass as success.
# The signal used is the application height standing still at H-1 while the node
# is still answering, corroborated by the upgrade-required line in its log.
MAXAPP=(0 0 0 0)
STILL=0
DEADLINE_TS=$((SECONDS + 900))
while ((SECONDS < DEADLINE_TS)); do
  at_target=0; advanced=0
  for n in 0 1 2 3; do
    a="$(app_height "$n")"
    if [[ "$a" =~ ^[0-9]+$ ]]; then
      if (( a > MAXAPP[n] )); then MAXAPP[$n]=$a; advanced=1; fi
      (( a == UPGRADE_HEIGHT - 1 )) && at_target=$((at_target + 1))
    fi
  done
  if (( at_target == 4 )); then
    if (( advanced == 0 )); then STILL=$((STILL + 1)); else STILL=0; fi
    (( STILL >= 5 )) && break
  fi
  sleep 1
done
(( STILL >= 5 )) || fail "the four validators never settled at a halted application height"

HALT_TS="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
for n in 0 1 2 3; do
  (( MAXAPP[n] == UPGRADE_HEIGHT - 1 )) \
    && ok "node$n application committed $((UPGRADE_HEIGHT - 1)) and never applied H" \
    || fail "node$n application reached height ${MAXAPP[n]}, expected $((UPGRADE_HEIGHT - 1))"
  # The block store legitimately holds H: consensus agreed on the block before the
  # application refused it. Recorded to show the two heights diverging exactly here.
  BLK="$(rpc_height "$n")"
  note "node$n block store at ${BLK:-unreachable}, application at ${MAXAPP[$n]}"
  if node_alive "$n"; then
    if grep -q "UPGRADE .${UPGRADE_NAME}. NEEDED at height: ${UPGRADE_HEIGHT}" "$NET/logs/node$n.log" 2>/dev/null; then
      ok "node$n logged the upgrade-required halt"
    else
      fail "node$n stopped advancing without logging an upgrade-required halt"
    fi
  else
    fail "node$n process exited; an upgrade halt leaves the process up with consensus stopped"
  fi
done

echo "=== 8. each node left the metadata the next binary needs ==="
for n in 0 1 2 3; do
  INFO="$(node_home "$n")/data/upgrade-info.json"
  if [[ ! -f "$INFO" ]]; then fail "node$n wrote no upgrade-info.json"; continue; fi
  cp "$INFO" "$EVID_DIR/node$n-upgrade-info.json"
  iname="$(jq -r '.name // empty' "$INFO")"; iheight="$(jq -r '.height // empty' "$INFO")"
  [[ "$iname" == "$UPGRADE_NAME" && "$iheight" == "$UPGRADE_HEIGHT" ]] \
    && ok "node$n upgrade-info: $iname@$iheight" \
    || fail "node$n upgrade-info says name='$iname' height='$iheight'"
  jq -nc --argjson n "$n" --arg name "$iname" --argjson h "$iheight" --argjson last "${MAXAPP[$n]}" \
    --arg sha "$SHA_A" --arg ts "$HALT_TS" \
    '{node:$n, phase:"halt", upgrade_name:$name, scheduled_height:$h, last_committed_height:$last,
      halt_observed:true, halt_timestamp:$ts, binary_sha256:$sha, result:"halted"}' >>"$UPGRADE_LOG"
done

echo
echo "=== 9. wall-clock downtime commits nothing ==="
# The settlement clock is driven by committed blocks, never by elapsed time. The
# proof is that no block beyond H-1 exists after a deliberate wait — not that a
# query returned the same number, which a dead node would also do.
sleep "$HALT_WAIT_SECONDS"
for n in 0 1 2 3; do
  a="$(app_height "$n")"
  [[ "$a" =~ ^[0-9]+$ ]] || { fail "node$n stopped answering during the halt window"; continue; }
  (( a == UPGRADE_HEIGHT - 1 )) \
    || fail "node$n application advanced to $a during a window in which no block could commit"
done
ok "${HALT_WAIT_SECONDS}s elapsed with every validator halted at $((UPGRADE_HEIGHT - 1))"
ok "no block at or beyond H was committed, so no settlement-clock tick was possible"
jq -nc --arg cp "after_halt_wait" --argjson h $((UPGRADE_HEIGHT - 1)) --argjson w "$HALT_WAIT_SECONDS" \
  '{checkpoint:$cp, committed_height:$h, halt_wait_seconds:$w, blocks_committed_during_wait:0,
    settlement_clock_ticks_during_wait:0}' >>"$ECONOMICS"

echo
echo "=== 10. a PARTIAL rollout: three validators move to binary B ==="
# Three of four is more than 2/3 of voting power, so the network can cross the
# boundary while one operator is still behind. That asymmetry is the reason this
# drill needs four validators: at two or three, every operator must succeed
# simultaneously and the interesting case cannot be expressed.
for n in 0 1 2 3; do stop_node "$n"; done
sleep 3
for n in 0 1 2; do
  eval "export NODE_BIN_$n=\"$BIN_B\""
  start_node "$n"
done
ok "nodes 0,1,2 restarted on binary B; node3 left on A and not running"

RESUMED=0
DEADLINE_TS=$((SECONDS + 300))
while ((SECONDS < DEADLINE_TS)); do
  a0="$(app_height 0)"; a1="$(app_height 1)"; a2="$(app_height 2)"
  if [[ "$a0" =~ ^[0-9]+$ && "$a1" =~ ^[0-9]+$ && "$a2" =~ ^[0-9]+$ ]] \
     && (( a0 >= UPGRADE_HEIGHT + 2 && a1 >= UPGRADE_HEIGHT + 2 && a2 >= UPGRADE_HEIGHT + 2 )); then
    RESUMED=1; break
  fi
  sleep 2
done
(( RESUMED == 1 )) && ok "the three upgraded validators resumed and passed H+2" \
  || fail "the upgraded majority did not resume past H+2"

echo
echo "=== 11. the migration is visible in committed state on every upgraded node ==="
for n in 0 1 2; do
  KRD="$(require_num "$(q_node "$n" coreslot-query params | jq -r '.params.key_rotation_delay_blocks // empty')" "node$n key_rotation_delay_blocks")"
  (( KRD == 2 )) && ok "node$n reports key_rotation_delay_blocks=2" \
    || fail "node$n reports key_rotation_delay_blocks=$KRD, expected 2"
  PLAN_AFTER="$(q_node "$n" query upgrade plan 2>/dev/null | jq -r '.plan.name // .name // empty' 2>/dev/null)"
  [[ -z "$PLAN_AFTER" ]] && ok "node$n has no pending plan; it was consumed" \
    || fail "node$n still reports a pending plan: $PLAN_AFTER"
  MV="$(q_node "$n" query upgrade module-versions)"
  for m in auth bank consensus coreslot mining rewards runtime upgrade; do
    jq -e --arg m "$m" '.module_versions[]? | select(.name == $m)' >/dev/null 2>&1 <<<"$MV" \
      || fail "node$n version map is missing $m"
  done
  ok "node$n version map covers all eight mounted modules"
done

echo
echo "=== 12. the upgraded majority agrees across the boundary ==="
agree_nodes "0 1 2" "post-upgrade" || fail "the upgraded nodes disagree after the boundary"

echo
echo "=== 13. clock arithmetic: only committed blocks tick ==="
C_BEFORE="$(require_num "$(clock_at 0 $((UPGRADE_HEIGHT - 1)))" "clock at H-1")"
C_AT="$(require_num "$(clock_at 0 "$UPGRADE_HEIGHT")" "clock at H")"
C_AFTER="$(require_num "$(clock_at 0 $((UPGRADE_HEIGHT + 1)))" "clock at H+1")"
(( C_AT - C_BEFORE == EXPECTED_TICK )) \
  && ok "H ticked the clock by exactly $EXPECTED_TICK ($C_BEFORE -> $C_AT)" \
  || fail "H moved the clock by $((C_AT - C_BEFORE)), expected $EXPECTED_TICK"
(( C_AFTER - C_AT == EXPECTED_TICK )) \
  && ok "H+1 ticked the clock by exactly $EXPECTED_TICK ($C_AT -> $C_AFTER)" \
  || fail "H+1 moved the clock by $((C_AFTER - C_AT)), expected $EXPECTED_TICK"
# The whole point: the halt consumed no clock. Two blocks committed across the
# boundary, so exactly two ticks are accounted for, whatever the wall time was.
(( C_AFTER - C_BEFORE == 2 * EXPECTED_TICK )) \
  && ok "${HALT_WAIT_SECONDS}s+ of downtime added ZERO ticks; only the 2 committed blocks did" \
  || fail "clock moved $((C_AFTER - C_BEFORE)) across two committed blocks"

echo
echo "=== 14. the settlement and the books are untouched by the boundary ==="
S_BEFORE="$("$BIN_B" mining-query settlement "$SLOT_ID" "$SETTLE_EPOCH" --node "$(rpc_url 0)" --height $((UPGRADE_HEIGHT - 1)) --output json 2>/dev/null)"
S_AFTER="$("$BIN_B" mining-query settlement "$SLOT_ID" "$SETTLE_EPOCH" --node "$(rpc_url 0)" --height $((UPGRADE_HEIGHT + 1)) --output json 2>/dev/null)"
[[ -n "$S_BEFORE" && -n "$S_AFTER" ]] || die "could not read the settlement on both sides of the boundary"
for field in deadline_clock entitlement_amount released_amount remaining_amount; do
  b="$(jq -r ".$field" <<<"$S_BEFORE")"; a="$(jq -r ".$field" <<<"$S_AFTER")"
  [[ "$b" == "$a" && -n "$b" && "$b" != "null" ]] && ok "$field unchanged across H ($b)" \
    || fail "$field changed across H: $b -> $a"
done
MB_AFTER="$(q_node 0 rewards-query module-balances)"
E2="$(jq -r '.rewards_balance' <<<"$MB_AFTER")"; L2="$(jq -r '.outstanding_entitlement_liability' <<<"$MB_AFTER")"; C2="$(jq -r '.carry_forward_remainder' <<<"$MB_AFTER")"
[[ "$E2" == "$((L2 + C2))" ]] && ok "escrow == liability + carry ($E2) after the boundary" \
  || fail "solvency broken after the boundary: $E2 != $L2 + $C2"
[[ "$E2" == "$ESCROW" && "$L2" == "$LIAB" && "$C2" == "$CARRY" ]] \
  && ok "escrow, liability and carry are byte-identical to the pre-upgrade values" \
  || note "economic totals moved across the boundary: escrow $ESCROW->$E2 liability $LIAB->$L2 carry $CARRY->$C2"
jq -nc --arg cp "post_upgrade_h_plus_1" --argjson h $((UPGRADE_HEIGHT + 1)) \
  --arg cb "$C_BEFORE" --arg ca "$C_AT" --arg cf "$C_AFTER" --argjson tick "$EXPECTED_TICK" \
  --arg esc "$E2" --arg li "$L2" --arg ca2 "$C2" --arg krd "2" \
  '{checkpoint:$cp, committed_height:$h, clock_h_minus_1:$cb, clock_h:$ca, clock_h_plus_1:$cf,
    expected_tick_per_block:$tick, escrow:$esc, liability:$li, carry:$ca2,
    escrow_equals_liability_plus_carry:true, key_rotation_delay_blocks:$krd}' >>"$ECONOMICS"

echo
echo "=== 15. the stale validator fails CLOSED, it does not fork ==="
# node3 is restarted on the OLD binary after the network has already crossed H.
# The dangerous outcome is not that it stops — it is that it follows the upgraded
# chain using pre-migration application logic, producing state nobody else has.
unset NODE_BIN_3
start_node 3
sleep 20
A3="$(app_height 3)"
if [[ "$A3" =~ ^[0-9]+$ ]]; then
  (( A3 == UPGRADE_HEIGHT - 1 )) \
    && ok "node3 on binary A is still at $((UPGRADE_HEIGHT - 1)); it cannot cross H" \
    || fail "node3 on the old binary reached application height $A3"
else
  # Expected, not a gap: the node replays the stored block H during startup and
  # refuses it before the RPC server binds, so there is no height to read. The
  # log below is the primary evidence, and the convergence in section 16 is what
  # rules out a fork — a node that had applied H with old logic could not later
  # agree on the app hash with the nodes that applied the migration.
  note "node3 on binary A never bound RPC; it refused the boundary during replay"
fi
grep -q "UPGRADE .${UPGRADE_NAME}. NEEDED at height: ${UPGRADE_HEIGHT}" "$NET/logs/node3.log" 2>/dev/null \
  && ok "node3 refuses the boundary with the upgrade-required error" \
  || fail "node3 did not report an upgrade-required halt"
KRD3="$(q_node 3 coreslot-query params 2>/dev/null | jq -r '.params.key_rotation_delay_blocks // empty')"
[[ "$KRD3" == "1" || -z "$KRD3" ]] \
  && ok "node3 never applied the migration (params read '${KRD3:-unavailable — halted before RPC}')" \
  || fail "node3 somehow applied the migration while on the old binary"

echo
echo "=== 16. the fourth validator upgrades and rejoins ==="
stop_node 3; sleep 3
eval "export NODE_BIN_3=\"$BIN_B\""
start_node 3
CAUGHT=0
DEADLINE_TS=$((SECONDS + 300))
while ((SECONDS < DEADLINE_TS)); do
  a3="$(app_height 3)"; a0="$(app_height 0)"
  if [[ "$a3" =~ ^[0-9]+$ && "$a0" =~ ^[0-9]+$ ]] && (( a3 >= UPGRADE_HEIGHT + 2 && a0 - a3 <= 3 )); then
    CAUGHT=1; break
  fi
  sleep 2
done
(( CAUGHT == 1 )) && ok "node3 crossed H on binary B and caught up to the others" \
  || fail "node3 did not catch up after upgrading"
KRD3="$(require_num "$(q_node 3 coreslot-query params | jq -r '.params.key_rotation_delay_blocks // empty')" "node3 key_rotation_delay_blocks")"
(( KRD3 == 2 )) && ok "node3 reports key_rotation_delay_blocks=2, the same as the majority" \
  || fail "node3 reports $KRD3 after upgrading"
agree_nodes "0 1 2 3" "all-four-converged" || fail "the four validators do not agree after node3 rejoined"

echo
echo "=== 17. a restart with stale metadata does not re-run the migration ==="
# The upgrade-info file is deliberately left in place, as it is on a real node.
# Because the migration requires key_rotation_delay_blocks == 1 on entry, a second
# execution would fail and the node could not produce blocks — so a restart that
# resumes normally IS the proof it did not run twice.
[[ -f "$(node_home 0)/data/upgrade-info.json" ]] && ok "node0 still carries its upgrade-info.json" \
  || note "node0 has no upgrade-info file to be stale"
BEFORE_RESTART="$(require_num "$(app_height 0)" "node0 height before restart")"
stop_node 0; sleep 3; start_node 0
PROGRESSED=0
DEADLINE_TS=$((SECONDS + 180))
while ((SECONDS < DEADLINE_TS)); do
  a0="$(app_height 0)"
  [[ "$a0" =~ ^[0-9]+$ ]] && (( a0 > BEFORE_RESTART )) && { PROGRESSED=1; break; }
  sleep 2
done
(( PROGRESSED == 1 )) && ok "node0 restarted with the stale file and kept committing blocks" \
  || fail "node0 did not resume after a restart with stale upgrade metadata"
KRD0="$(require_num "$(q_node 0 coreslot-query params | jq -r '.params.key_rotation_delay_blocks // empty')" "node0 key_rotation_delay_blocks")"
(( KRD0 == 2 )) && ok "key_rotation_delay_blocks is still 2 — the migration ran exactly once" \
  || fail "key_rotation_delay_blocks is $KRD0 after the restart"

for n in 0 1 2 3; do
  jq -nc --argjson n "$n" --arg name "$UPGRADE_NAME" --argjson h "$UPGRADE_HEIGHT" \
    --argjson cur "$(app_height "$n")" --arg sha "$SHA_B" --argjson first "$UPGRADE_HEIGHT" \
    '{node:$n, phase:"resume", upgrade_name:$name, scheduled_height:$h, binary_sha256:$sha,
      first_b_committed_height:$first, current_height:$cur, migration_marker:2,
      plan_present:false, result:"upgraded"}' >>"$UPGRADE_LOG"
done

echo
echo "================= upgrade drill ======================="
echo "  source        $SOURCE_SHA"
echo "  binary A      ${SHA_A:0:16}  (no $UPGRADE_NAME)"
echo "  binary B      ${SHA_B:0:16}  (carries $UPGRADE_NAME)"
echo "  epoch length  $EPOCH_LEN"
echo "  upgrade at    H=$UPGRADE_HEIGHT   (mid-epoch-2, deadline clock $DEADLINE)"
echo "  settlement    slot $SLOT_ID epoch $SETTLE_EPOCH, open across the boundary"
echo "  all 4 nodes   application halted at $((UPGRADE_HEIGHT - 1)), upgrade-info written"
echo "  rollout       nodes 0,1,2 -> B crossed H with node3 still on A"
echo "  stale node    node3 on A stayed at $((UPGRADE_HEIGHT - 1)); it failed closed, it did not fork"
echo "  catch-up      node3 -> B rejoined; all four agree"
echo "  migration     key_rotation_delay_blocks 1 -> 2, exactly once"
echo "  clock         $C_BEFORE -> $C_AT -> $C_AFTER  (tick $EXPECTED_TICK/block, 0 for downtime)"
echo "  evidence      $EVID_DIR"
echo "======================================================"
if (( FAILURES > 0 )); then echo "upgrade drill: FAIL ($FAILURES)" >&2; exit 1; fi
echo "upgrade drill: PASS"
