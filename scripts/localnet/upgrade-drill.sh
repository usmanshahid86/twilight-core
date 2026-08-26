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
#
# # Fail-closed
#
# Every assertion here must be able to FAIL. That sounds obvious and was not: an
# earlier version validated numeric reads inside a command substitution, where
# `exit` kills only the subshell, so a failed read left an empty string that bash
# arithmetic treated as zero and every clock assertion passed vacuously. The
# helpers below validate in the PARENT shell and assign with printf -v, and every
# critical read is checked by its caller. Evidence is derived from assertion
# outcomes rather than written on entry to a phase.

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
# Set before sourcing: drill-common derives the evidence directory from DRILL at
# source time, so exporting it afterwards would file this run's evidence under the
# default name.
export DRILL="upgrade"
. "$ROOT/scripts/localnet/lib/drill-common.sh"
# drill-common enables `set -e`; this drill accounts for its own failures and must
# not abort halfway through, leaving a localnet running and no evidence.
set +e

NODE_COUNT=4
export NODE_COUNT

BIN_A="$ROOT/build/twilightd"
BIN_B="$ROOT/build/twilightd-upgradedrill"
UPGRADE_NAME="drill-v2"
# Long enough that "no clock advanced" is a claim about wall time, not a
# scheduling accident. Measured, not assumed — see phase 9.
HALT_WAIT_SECONDS="${HALT_WAIT_SECONDS:-12}"
EXPECTED_VALIDATORS=4
EXPECTED_POWER=1
# Fast blocks; the protocol epoch length is NOT shortened.
export TWILIGHT_LOCALNET_TIMEOUT_COMMIT="${UPGRADE_DRILL_TIMEOUT_COMMIT:-200ms}"

FAILURES=0
fail() { echo "  FAIL: $*" >&2; FAILURES=$((FAILURES + 1)); }
ok()   { echo "  ok: $*"; }
note() { echo "  note: $*"; }
die()  { echo "  ABORT: $*" >&2; finish 2; }

need curl; need jq

# ---- fail-closed primitives -------------------------------------------------
#
# read_required_uint VAR cmd [args...]
#
# Runs the command in THIS shell, checks its exit status, requires non-empty
# strictly-unsigned-integer output, and assigns with printf -v so the value lands
# in the caller's scope. Returns non-zero on any failure, having already recorded
# one. Callers must check the return value — a critical read that fails must stop
# the assertions that depend on it rather than feed them an empty operand.
read_required_uint() {
  local __var="$1"; shift
  local __out __rc
  __out="$("$@" 2>/dev/null)"; __rc=$?
  if (( __rc != 0 )); then fail "$__var: reader exited $__rc ($*)"; return 1; fi
  if [[ -z "$__out" ]]; then fail "$__var: reader produced no output ($*)"; return 1; fi
  if [[ ! "$__out" =~ ^[0-9]+$ ]]; then fail "$__var: '$__out' is not an unsigned integer ($*)"; return 1; fi
  printf -v "$__var" '%s' "$__out"
  return 0
}

# read_required_str VAR cmd [args...] — same contract for non-numeric values.
read_required_str() {
  local __var="$1"; shift
  local __out __rc
  __out="$("$@" 2>/dev/null)"; __rc=$?
  if (( __rc != 0 )); then fail "$__var: reader exited $__rc ($*)"; return 1; fi
  if [[ -z "$__out" ]]; then fail "$__var: reader produced no output ($*)"; return 1; fi
  printf -v "$__var" '%s' "$__out"
  return 0
}

# ---- readers (each is a single pipeline whose status propagates) ------------
sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'
  else shasum -a 256 "$1" | awk '{print $1}'; fi
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
app_height() { rpc_get "$1" /abci_info | jq -er '.result.response.last_block_height'; }
store_height() { rpc_get "$1" /status | jq -er '.result.sync_info.latest_block_height'; }
clock_at() { "$BIN_B" mining-query settlement-clock --node "$(rpc_url "$1")" --height "$2" --output json | jq -er '.settlement_clock'; }
active_slot_count() { "$BIN_B" coreslot-query active --node "$(rpc_url "$1")" --output json | jq -er '(.slots // []) | length'; }
validator_count() { rpc_get "$1" /validators | jq -er '.result.total'; }
min_validator_power() { rpc_get "$1" /validators | jq -er '[.result.validators[].voting_power | tonumber] | min'; }
max_validator_power() { rpc_get "$1" /validators | jq -er '[.result.validators[].voting_power | tonumber] | max'; }
krd_at() { "$BIN_B" coreslot-query params --node "$(rpc_url "$1")" --output json | jq -er '.params.key_rotation_delay_blocks'; }
hash_at() { rpc_get "$1" "/block?height=$2" | jq -er ".result.block.header.$3"; }
q_node() { local n="$1"; shift; "$BIN_B" "$@" --node "$(rpc_url "$n")" --output json 2>/dev/null; }
q_epoch_len() { "$BIN_B" rewards-query params --node "$(rpc_url "$1")" --output json | jq -er '.params.epoch_length_blocks'; }
jq_field() { jq -er "$2" <<<"$1"; }
mb_field() { "$BIN_B" rewards-query module-balances --node "$(rpc_url "$1")" --output json | jq -er "$2"; }
mb_field_at() { "$BIN_B" rewards-query module-balances --node "$(rpc_url "$1")" --height "$2" --output json | jq -er "$3"; }
settle_field_at() { "$BIN_B" mining-query settlement "$2" "$3" --node "$(rpc_url "$1")" --height "$4" --output json | jq -er "$5"; }
plan_name() { "$BIN_B" query upgrade plan --node "$(rpc_url "$1")" --output json | jq -er '.plan.name // .name'; }
plan_height() { "$BIN_B" query upgrade plan --node "$(rpc_url "$1")" --output json | jq -er '.plan.height // .height'; }
version_map_canon() {
  "$BIN_B" query upgrade module-versions --node "$(rpc_url "$1")" --output json \
    | jq -er '[.module_versions[] | "\(.name):\(.version // 0)"] | sort | join(",")'
}

# plan_state <node> — "none" | "pending:<name>" | non-zero exit.
#
# An empty result must never be read as "no plan": a transport failure, a CLI
# error or malformed JSON all produce empty output, and treating that as absence
# would report a consumed plan on a node nobody could reach. The canonical
# no-plan answer on this CLI is an error naming it, so it is recognised
# explicitly and everything else fails.
plan_state() {
  local out rc nm
  out="$("$BIN_B" query upgrade plan --node "$(rpc_url "$1")" --output json 2>&1)"; rc=$?
  if (( rc == 0 )); then
    nm="$(jq -r '.plan.name // .name // empty' <<<"$out" 2>/dev/null)"
    [[ -n "$nm" ]] && echo "pending:$nm" || echo "none"
    return 0
  fi
  grep -qiE "no upgrade (scheduled|plan)|upgrade plan not found|no plan" <<<"$out" && { echo "none"; return 0; }
  return 1
}

# app_height_after_offset <node> <log-offset> — the committed application height
# reported by THIS start, when RPC never came up.
#
# A node that refuses an upgrade during replay dies before binding RPC, so there
# is no endpoint to ask. CometBFT logs the height at the ABCI handshake, and
# reading only past the recorded offset means the answer comes from this start
# rather than from an earlier one still sitting in the append-only log.
app_height_after_offset() {
  local n="$1" off="$2" v
  if v="$(app_height "$n" 2>/dev/null)" && [[ "$v" =~ ^[0-9]+$ ]]; then echo "$v"; return 0; fi
  v="$(tail -n +$((off + 1)) "$NET/logs/node$n.log" 2>/dev/null | grep -o 'appHeight=[0-9]\+' | tail -1 | cut -d= -f2)"
  [[ "$v" =~ ^[0-9]+$ ]] || return 1
  echo "$v"
}

# node_exe_sha <node> — SHA-256 of the executable the recorded pid is running.
# A shell variable saying which binary was requested is not evidence that it is
# the binary running; this reads the process.
node_exe_sha() {
  local pidfile="$NET/node$1.pid" pid exe
  [[ -f "$pidfile" ]] || return 1
  pid="$(cat "$pidfile" 2>/dev/null)"; [[ "$pid" =~ ^[0-9]+$ ]] || return 1
  exe="$(ps -o command= -p "$pid" 2>/dev/null | awk '{print $1}')"
  [[ -n "$exe" && -f "$exe" ]] || return 1
  sha256_of "$exe"
}

# ---- evidence: derived from outcomes, never written on entry to a phase -----
SUMMARY_ROWS=0
ASSERT_ROWS=0
record_phase() { # <phase> <result> <detail>
  printf '%s,%s,%s\n' "$1" "$2" "${3//,/;}" >>"$SUMMARY" || die "could not write a summary row"
  SUMMARY_ROWS=$((SUMMARY_ROWS + 1))
}
PHASE_BASE=0
phase_begin() { PHASE_BASE=$FAILURES; }
phase_end() { # <phase> <detail> — result derived from failures raised IN this phase
  local r=PASS
  (( FAILURES > PHASE_BASE )) && r=FAIL
  record_phase "$1" "$r" "$2"
}
record_assert() { # <node|-> <assertion> <expected> <observed> <result>
  jq -nc --arg n "$1" --arg a "$2" --arg e "$3" --arg o "$4" --arg r "$5" \
    '{node:$n, assertion:$a, expected:$e, observed:$o, result:$r}' >>"$UPGRADE_LOG" \
    || die "could not write assertion evidence for $2"
  ASSERT_ROWS=$((ASSERT_ROWS + 1))
}

# expect <assertion> <expected> <observed> [node] — assert AND record together,
# so evidence cannot claim a result the check did not produce.
expect() {
  local a="$1" e="$2" o="$3" n="${4:--}"
  if [[ "$e" == "$o" ]]; then
    ok "$a ($o)"; record_assert "$n" "$a" "$e" "$o" PASS; return 0
  fi
  fail "$a: expected '$e', observed '$o'"; record_assert "$n" "$a" "$e" "$o" FAIL; return 1
}

# finish <forced-fail?> — single exit point. Verifies the evidence it is about to
# stand behind actually exists before declaring anything.
finish() {
  local forced="${1:-}" verdict="PASS" f
  (( FAILURES > 0 )) && verdict="FAIL"
  [[ -n "$forced" ]] && verdict="FAIL"
  if [[ -n "${EVID_DIR:-}" && -d "$EVID_DIR" ]]; then
    for f in binaries.json upgrade.jsonl economics.jsonl summary.csv; do
      [[ -s "$EVID_DIR/$f" ]] || { echo "  FAIL: mandatory evidence $f is missing or empty" >&2; verdict="FAIL"; }
    done
    (( ASSERT_ROWS >= 20 )) || { echo "  FAIL: only $ASSERT_ROWS assertions recorded; the run did not reach its checks" >&2; verdict="FAIL"; }
    printf '%s\n' "$verdict" >"$EVID_DIR/verdict.txt"
    record_phase "final" "$verdict" "failures=$FAILURES assertions=$ASSERT_ROWS" 2>/dev/null || true
  fi
  echo
  if [[ "$verdict" == "PASS" ]]; then
    echo "upgrade drill: PASS"; exit 0
  fi
  echo "upgrade drill: FAIL (failures=$FAILURES)" >&2; exit 1
}

# ---- cleanup scoped to THIS drill's recorded processes ----------------------
# Never a broad pkill: another twilightd on the machine is not this drill's to
# kill, and a stale pid file must not authorize killing whatever reused the pid.
# The predicate must recognise BOTH binaries. Binary B is installed as
# `twilightd-upgradedrill`, so a check anchored on the literal string
# "twilightd start" does not match it — and a cleanup that cannot match half the
# drill's own processes leaves one holding its port, which the next run then
# fails to bind while a stale node answers in its place. Match on the home
# directory and on the executable living in this repository's build directory.
node_process_matches() { # <pid> <node-index>
  local cmd; cmd="$(ps -o command= -p "$1" 2>/dev/null)" || return 1
  [[ "$cmd" == "$ROOT/build/"* ]] || return 1
  [[ "$cmd" == *"start --home $(node_home "$2")"* ]] || return 1
  return 0
}
kill_recorded_node() {
  local i="$1" pidfile="$NET/node$1.pid" pid
  [[ -f "$pidfile" ]] || return 0
  pid="$(cat "$pidfile" 2>/dev/null)"
  if [[ "$pid" =~ ^[0-9]+$ ]] && node_process_matches "$pid" "$i"; then
    kill "$pid" 2>/dev/null || true
  fi
  rm -f "$pidfile"
}
cleanup_drill() { local i; for i in 0 1 2 3; do kill_recorded_node "$i"; done; }

# Isolation is checked by what is serving THIS localnet, not by process name.
# `pgrep -x twilightd` misses binary B entirely, and a leftover node answering on
# a port this run needs is exactly the contamination that makes a drill report a
# height it did not produce.
LEFTOVER="$(pgrep -f "$ROOT/build/twilightd.* start --home $NET/" 2>/dev/null | tr '\n' ' ')"
if [[ -n "${LEFTOVER// /}" ]]; then
  echo "  ABORT: processes are already serving $NET (pids: $LEFTOVER); stop them first" >&2; exit 2
fi
trap cleanup_drill EXIT

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
[[ -n "$SHA_A" && -n "$SHA_B" ]] || die "could not hash the binaries"
BIN="$BIN_A"   # every node starts on A

echo
echo "=== 3. starting all four validators on binary A ==="
"$ROOT/scripts/localnet/start.sh" >/dev/null 2>&1 || die "start failed"
init_evidence
BINARIES="$EVID_DIR/binaries.json"
UPGRADE_LOG="$EVID_DIR/upgrade.jsonl"
ECONOMICS="$EVID_DIR/economics.jsonl"
SUMMARY="$EVID_DIR/summary.csv"
: >"$UPGRADE_LOG"; : >"$ECONOMICS"
printf 'phase,result,detail\n' >"$SUMMARY"
jq -nc --arg src "$SOURCE_SHA" --arg a "$BIN_A" --arg sa "$SHA_A" --arg b "$BIN_B" --arg sb "$SHA_B" \
  '{source_commit:$src,
    binary_a:{path:$a, sha256:$sa, build:"go build -o build/twilightd ./cmd/twilightd", tags:""},
    binary_b:{path:$b, sha256:$sb, build:"go build -tags upgradedrill -o build/twilightd-upgradedrill ./cmd/twilightd", tags:"upgradedrill"}}' \
  >"$BINARIES" || die "could not write binary provenance"
expect "binaries_differ" "differ" "$([[ "$SHA_A" != "$SHA_B" ]] && echo differ || echo identical)"
phase_end "preflight" "A=${SHA_A:0:16} B=${SHA_B:0:16}"

for n in 0 1 2 3; do
  wait_height_node "$n" 3 90 || die "node$n never produced blocks"
done
agree_nodes "0 1 2 3" "pre-upgrade-start" || fail "nodes disagree before the drill begins"

echo
phase_begin
echo "=== 4. topology: four validators, equal power ==="
# Asserted, not observed in passing. Three of four is a quorum only because the
# powers are equal and there are exactly four; a drill that silently ran on three
# validators, or on an unequal set, would prove something else entirely.
TOPO_OK=1
read_required_uint SLOTS_ACTIVE active_slot_count 0 || TOPO_OK=0
read_required_uint VAL_COUNT validator_count 0 || TOPO_OK=0
read_required_uint VAL_MIN min_validator_power 0 || TOPO_OK=0
read_required_uint VAL_MAX max_validator_power 0 || TOPO_OK=0
if (( TOPO_OK == 1 )); then
  expect "coreslot_active_slots" "$EXPECTED_VALIDATORS" "$SLOTS_ACTIVE"
  expect "cometbft_validators" "$EXPECTED_VALIDATORS" "$VAL_COUNT"
  expect "validator_power_min" "$EXPECTED_POWER" "$VAL_MIN"
  expect "validator_power_max" "$EXPECTED_POWER" "$VAL_MAX"
  ok "3 of $VAL_COUNT is $(( 3 * 100 / VAL_COUNT ))% of voting power, strictly above 2/3"
else
  fail "topology could not be read; the quorum argument is unproven"
fi
phase_end "topology" "slots=$SLOTS_ACTIVE validators=$VAL_COUNT power=$VAL_MIN..$VAL_MAX"

echo
phase_begin
echo "=== 5. epoch geometry and a settlement that will span the boundary ==="
read_required_uint EPOCH_LEN q_epoch_len 0 || die "the epoch length could not be read"
ok "epoch length is $EPOCH_LEN blocks (protocol value, not shortened)"
for n in 0 1 2 3; do
  wait_height_node "$n" $((EPOCH_LEN + 2)) 900 || die "node$n did not reach the first epoch boundary"
done
sleep 2
SLOT_ID=1; SETTLE_EPOCH=1
SETTLEMENT="$(q_node 0 mining-query settlement "$SLOT_ID" "$SETTLE_EPOCH")"
[[ -n "$SETTLEMENT" ]] || die "no settlement for slot $SLOT_ID epoch $SETTLE_EPOCH"
expect "settlement_open_before_upgrade" "false" "$(jq -r '.settlement.finalized' <<<"$SETTLEMENT")"
read_required_uint DEADLINE jq_field "$SETTLEMENT" .deadline_clock || die "the settlement deadline could not be read"

UPGRADE_HEIGHT=$((EPOCH_LEN + EPOCH_LEN / 2))
MARGIN=$((EPOCH_LEN / 4))
if (( UPGRADE_HEIGHT % EPOCH_LEN != 0 )); then ok "H=$UPGRADE_HEIGHT is not an epoch boundary"; record_assert - "H_not_epoch_boundary" "not a multiple of $EPOCH_LEN" "$UPGRADE_HEIGHT" PASS
else fail "H=$UPGRADE_HEIGHT lands on an epoch boundary"; record_assert - "H_not_epoch_boundary" "not a multiple of $EPOCH_LEN" "$UPGRADE_HEIGHT" FAIL; fi
if (( UPGRADE_HEIGHT - EPOCH_LEN >= MARGIN && 2 * EPOCH_LEN - UPGRADE_HEIGHT >= MARGIN )); then
  ok "H is at least $MARGIN blocks from either boundary ($EPOCH_LEN and $((2 * EPOCH_LEN)))"
  record_assert - "H_margin_from_boundaries" ">=$MARGIN" "$((UPGRADE_HEIGHT - EPOCH_LEN))" PASS
else fail "H=$UPGRADE_HEIGHT is too close to an epoch boundary"; record_assert - "H_margin_from_boundaries" ">=$MARGIN" "$((UPGRADE_HEIGHT - EPOCH_LEN))" FAIL; fi
if (( UPGRADE_HEIGHT < DEADLINE )); then ok "H precedes the settlement deadline (clock $DEADLINE)"
  record_assert - "H_before_settlement_deadline" "<$DEADLINE" "$UPGRADE_HEIGHT" PASS
else fail "the settlement would expire before H"; record_assert - "H_before_settlement_deadline" "<$DEADLINE" "$UPGRADE_HEIGHT" FAIL; fi
phase_end "geometry" "epoch=$EPOCH_LEN H=$UPGRADE_HEIGHT deadline=$DEADLINE"

echo
phase_begin
echo "=== 6. the authority schedules the upgrade ==="
OUT="$("$BIN_A" tx coreslot schedule-upgrade \
  --name "$UPGRADE_NAME" --height "$UPGRADE_HEIGHT" --info "sha256:$SHA_B" \
  --from operator0 --keyring-backend test --home "$(node_home 0)" \
  --chain-id "$CHAIN_ID" --node "$(rpc_url 0)" --gas 600000 --fees 0utwlt -y --output json 2>&1)"
TXHASH="$(jq -r '.txhash // empty' <<<"$OUT" 2>/dev/null)"
[[ -n "$TXHASH" ]] || die "the scheduling transaction was never broadcast: $(head -c 300 <<<"$OUT")"
# CheckTx acceptance is not delivery. Assert the DELIVERED result.
TXCODE="$(_wait_tx_code "$TXHASH")"
expect "schedule_tx_delivered" "0" "$TXCODE" || die "schedule-upgrade was not delivered successfully"
for n in 0 1 2 3; do
  read_required_str PNAME plan_name "$n" || { fail "node$n: the pending plan could not be read"; continue; }
  read_required_uint PHEIGHT plan_height "$n" || { fail "node$n: the plan height could not be read"; continue; }
  expect "pending_plan_name" "$UPGRADE_NAME" "$PNAME" "$n"
  expect "pending_plan_height" "$UPGRADE_HEIGHT" "$PHEIGHT" "$n"
done
phase_end "schedule" "tx=$TXHASH name=$UPGRADE_NAME H=$UPGRADE_HEIGHT"

echo
phase_begin
echo "=== 7. baseline, and the per-block clock tick MEASURED from the chain ==="
read_required_uint BASE_H app_height 0 || die "the baseline height could not be read"
read_required_uint KRD_BEFORE krd_at 0 || die "key_rotation_delay_blocks could not be read"
expect "migration_precondition_value" "1" "$KRD_BEFORE"
read_required_str ESCROW mb_field 0 .rewards_balance || die "escrow could not be read"
read_required_str LIAB mb_field 0 .outstanding_entitlement_liability || die "liability could not be read"
read_required_str CARRY mb_field 0 .carry_forward_remainder || die "carry could not be read"
expect "solvency_before_boundary" "$ESCROW" "$((LIAB + CARRY))"

# The tick is MEASURED, never inferred. A hard-coded +1 would pass silently on a
# paused chain; and because this drill never pauses release, a measured 0 means
# the reads are broken, not that the chain is paused — so 0 is a FAILURE here,
# not an accepted alternative.
read_required_uint TICK_H app_height 0 || die "height for the tick measurement could not be read"
read_required_uint TICK_C1 clock_at 0 $((TICK_H - 3)) || die "settlement clock at $((TICK_H - 3)) could not be read"
read_required_uint TICK_C2 clock_at 0 $((TICK_H - 2)) || die "settlement clock at $((TICK_H - 2)) could not be read"
EXPECTED_TICK=$((TICK_C2 - TICK_C1))
expect "measured_clock_tick_per_block" "1" "$EXPECTED_TICK" \
  || die "the measured settlement-clock tick is $EXPECTED_TICK; this drill never pauses release, so anything but 1 means the reads are wrong"
jq -nc --arg cp "baseline_pre_halt" --argjson h "$BASE_H" --arg krd "$KRD_BEFORE" \
  --arg esc "$ESCROW" --arg li "$LIAB" --arg ca "$CARRY" --argjson dl "$DEADLINE" \
  --argjson slot "$SLOT_ID" --argjson ep "$SETTLE_EPOCH" --argjson tick "$EXPECTED_TICK" \
  '{checkpoint:$cp, height:$h, key_rotation_delay_blocks:$krd, escrow:$esc, liability:$li,
    carry:$ca, settlement_slot:$slot, settlement_epoch:$ep, deadline_clock:$dl,
    measured_tick_per_block:$tick}' >>"$ECONOMICS" || die "could not write the baseline checkpoint"
phase_end "baseline" "tick=$EXPECTED_TICK krd=$KRD_BEFORE escrow=$ESCROW"

echo
phase_begin
echo "=== 8. every validator halts at H, and none applies it ==="
# A halted node here is HUNG, not gone: x/upgrade returns an error from PreBlock,
# CometBFT panics its consensus routine, and the process stays up serving RPC with
# consensus dead. Waiting for the process to exit would wait forever; treating an
# unreachable RPC as the halt signal would let a genuine crash pass as success.
MAXAPP=(0 0 0 0); STILL=0
DEADLINE_TS=$((SECONDS + 900))
while ((SECONDS < DEADLINE_TS)); do
  at_target=0; advanced=0
  for n in 0 1 2 3; do
    if a="$(app_height "$n" 2>/dev/null)" && [[ "$a" =~ ^[0-9]+$ ]]; then
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
for n in 0 1 2 3; do
  expect "halt_app_height" "$((UPGRADE_HEIGHT - 1))" "${MAXAPP[$n]}" "$n"
  # The block store legitimately holds H: consensus agreed on the block before the
  # application refused it. Recorded to show the two heights diverging exactly here.
  if read_required_uint BLK store_height "$n"; then
    expect "halt_block_store_height" "$UPGRADE_HEIGHT" "$BLK" "$n"
  fi
  if node_alive "$n"; then
    if grep -q "UPGRADE .${UPGRADE_NAME}. NEEDED at height: ${UPGRADE_HEIGHT}" "$NET/logs/node$n.log" 2>/dev/null; then
      expect "halt_logged_upgrade_required" "logged" "logged" "$n"
    else
      expect "halt_logged_upgrade_required" "logged" "absent" "$n"
    fi
  else
    expect "halt_process_alive" "alive" "exited" "$n"
  fi
  INFO="$(node_home "$n")/data/upgrade-info.json"
  if [[ -f "$INFO" ]]; then
    cp "$INFO" "$EVID_DIR/node$n-upgrade-info.json" || die "could not preserve node$n upgrade-info"
    expect "upgrade_info_name" "$UPGRADE_NAME" "$(jq -r '.name // empty' "$INFO")" "$n"
    expect "upgrade_info_height" "$UPGRADE_HEIGHT" "$(jq -r '.height // empty' "$INFO")" "$n"
  else
    expect "upgrade_info_written" "present" "missing" "$n"
  fi
done
phase_end "halt" "all four at $((UPGRADE_HEIGHT - 1))"

echo
phase_begin
echo "=== 9. wall-clock downtime commits nothing — measured, not assumed ==="
HALT_START=$SECONDS
HALT_START_TS="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
sleep "$HALT_WAIT_SECONDS"
HALT_ELAPSED=$((SECONDS - HALT_START))
HALT_END_TS="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
if (( HALT_ELAPSED >= HALT_WAIT_SECONDS )); then
  ok "measured halt interval: ${HALT_ELAPSED}s (required >= ${HALT_WAIT_SECONDS}s)"
  record_assert - "halt_interval_measured" ">=${HALT_WAIT_SECONDS}s" "${HALT_ELAPSED}s" PASS
else
  fail "the halt interval measured ${HALT_ELAPSED}s, below the required ${HALT_WAIT_SECONDS}s"
  record_assert - "halt_interval_measured" ">=${HALT_WAIT_SECONDS}s" "${HALT_ELAPSED}s" FAIL
fi
for n in 0 1 2 3; do
  if read_required_uint AH app_height "$n"; then
    expect "app_height_after_halt_interval" "$((UPGRADE_HEIGHT - 1))" "$AH" "$n"
  else
    fail "node$n stopped answering during the measured halt interval"
  fi
done
jq -nc --arg cp "after_halt_wait" --argjson h $((UPGRADE_HEIGHT - 1)) --argjson w "$HALT_ELAPSED" \
  --arg s "$HALT_START_TS" --arg e "$HALT_END_TS" \
  '{checkpoint:$cp, committed_height:$h, measured_halt_seconds:$w, started:$s, ended:$e,
    blocks_committed_during_interval:0, settlement_clock_ticks_during_interval:0}' >>"$ECONOMICS" \
  || die "could not write the downtime checkpoint"
phase_end "downtime" "elapsed=${HALT_ELAPSED}s blocks=0"

echo
phase_begin
echo "=== 10. a PARTIAL rollout: three validators move to binary B ==="
# Three of four is more than 2/3 of voting power, so the network can cross the
# boundary while one operator is still behind. That asymmetry is the reason this
# drill needs four validators.
for n in 0 1 2 3; do stop_node "$n"; done
sleep 3
for n in 0 1 2; do eval "export NODE_BIN_$n=\"$BIN_B\""; start_node "$n"; done
sleep 5
for n in 0 1 2; do
  if read_required_str EXE node_exe_sha "$n"; then expect "running_binary_is_B" "$SHA_B" "$EXE" "$n"
  else fail "node$n: could not read the executable of the running process"; fi
done
RESUMED=0; DEADLINE_TS=$((SECONDS + 300))
while ((SECONDS < DEADLINE_TS)); do
  a0="$(app_height 0 2>/dev/null)"; a1="$(app_height 1 2>/dev/null)"; a2="$(app_height 2 2>/dev/null)"
  if [[ "$a0" =~ ^[0-9]+$ && "$a1" =~ ^[0-9]+$ && "$a2" =~ ^[0-9]+$ ]] \
     && (( a0 >= UPGRADE_HEIGHT + 2 && a1 >= UPGRADE_HEIGHT + 2 && a2 >= UPGRADE_HEIGHT + 2 )); then RESUMED=1; break; fi
  sleep 2
done
expect "upgraded_majority_passed_H_plus_2" "resumed" "$([[ $RESUMED -eq 1 ]] && echo resumed || echo stalled)"
phase_end "partial_resume" "nodes 0,1,2 on B"

echo
phase_begin
echo "=== 11. the migration and the plan, read from committed state ==="
for n in 0 1 2; do
  if read_required_uint KRD krd_at "$n"; then expect "migration_applied" "2" "$KRD" "$n"; fi
  if read_required_str PSTATE plan_state "$n"; then expect "pending_plan_cleared" "none" "$PSTATE" "$n"
  else fail "node$n: the upgrade-plan query failed; absence cannot be inferred from a failed query"; fi
done
phase_end "migration" "key_rotation_delay_blocks=2"

echo
phase_begin
echo "=== 12. the upgraded majority agrees, and the validator set did not move ==="
agree_nodes "0 1 2" "post-upgrade" || fail "the upgraded nodes disagree after the boundary"
# Agreement across nodes at ONE height cannot see a migration that changed the
# validator set identically everywhere. Comparing the same hash ACROSS the
# boundary can.
VH_OK=1
read_required_str VH_PRE  hash_at 0 $((UPGRADE_HEIGHT - 1)) validators_hash      || VH_OK=0
read_required_str VH_POST hash_at 0 $((UPGRADE_HEIGHT + 1)) validators_hash      || VH_OK=0
read_required_str NH_PRE  hash_at 0 $((UPGRADE_HEIGHT - 1)) next_validators_hash || VH_OK=0
read_required_str NH_POST hash_at 0 $((UPGRADE_HEIGHT + 1)) next_validators_hash || VH_OK=0
if (( VH_OK == 1 )); then
  expect "validators_hash_stable_across_H" "$VH_PRE" "$VH_POST"
  expect "next_validators_hash_stable_across_H" "$NH_PRE" "$NH_POST"
else fail "validator hashes could not be pinned across the boundary"; fi
phase_end "validator_stability" "pre=${VH_PRE:0:16} post=${VH_POST:0:16}"

echo
phase_begin
echo "=== 13. clock arithmetic: only committed blocks tick ==="
CLK_OK=1
read_required_uint C_BEFORE clock_at 0 $((UPGRADE_HEIGHT - 1)) || CLK_OK=0
read_required_uint C_AT     clock_at 0 "$UPGRADE_HEIGHT"       || CLK_OK=0
read_required_uint C_AFTER  clock_at 0 $((UPGRADE_HEIGHT + 1)) || CLK_OK=0
if (( CLK_OK == 1 )); then
  expect "clock_tick_at_H" "$EXPECTED_TICK" "$((C_AT - C_BEFORE))"
  expect "clock_tick_at_H_plus_1" "$EXPECTED_TICK" "$((C_AFTER - C_AT))"
  expect "clock_total_across_two_blocks" "$((2 * EXPECTED_TICK))" "$((C_AFTER - C_BEFORE))"
  ok "${HALT_ELAPSED}s of measured downtime added ZERO ticks; only the 2 committed blocks did"
else fail "the settlement clock could not be read across the boundary; the downtime proof is unproven"; fi
phase_end "clock" "H-1=$C_BEFORE H=$C_AT H+1=$C_AFTER tick=$EXPECTED_TICK"

echo
phase_begin
echo "=== 14. the settlement and the books are UNCHANGED across the boundary ==="
# Values ordinary block execution is not supposed to move must be identical, not
# merely solvent. A migration that shifted value between liability and carry would
# keep escrow == liability + carry while changing what the chain owes and to whom.
ECON_OK=1
for field in deadline_clock entitlement_amount released_amount remaining_amount; do
  if read_required_str SB settle_field_at 0 "$SLOT_ID" "$SETTLE_EPOCH" $((UPGRADE_HEIGHT - 1)) ".$field" \
     && read_required_str SA settle_field_at 0 "$SLOT_ID" "$SETTLE_EPOCH" $((UPGRADE_HEIGHT + 1)) ".$field"; then
    expect "settlement_${field}_unchanged" "$SB" "$SA"
  else fail "$field could not be read on both sides of the boundary"; ECON_OK=0; fi
done
for pair in "escrow:.rewards_balance" "liability:.outstanding_entitlement_liability" "carry:.carry_forward_remainder"; do
  label="${pair%%:*}"; path="${pair#*:}"
  if read_required_str MB_B mb_field_at 0 $((UPGRADE_HEIGHT - 1)) "$path" \
     && read_required_str MB_A mb_field_at 0 $((UPGRADE_HEIGHT + 1)) "$path"; then
    expect "${label}_unchanged_across_H" "$MB_B" "$MB_A"
    eval "POST_$label=\$MB_A"; eval "PRE_$label=\$MB_B"
  else fail "$label could not be read on both sides of the boundary"; ECON_OK=0; fi
done
if (( ECON_OK == 1 )); then
  expect "solvency_before_boundary_pinned" "$PRE_escrow" "$((PRE_liability + PRE_carry))"
  expect "solvency_after_boundary_pinned"  "$POST_escrow" "$((POST_liability + POST_carry))"
fi
jq -nc --arg cp "post_upgrade_h_plus_1" --argjson h $((UPGRADE_HEIGHT + 1)) \
  --argjson cb "$C_BEFORE" --argjson ca "$C_AT" --argjson cf "$C_AFTER" --argjson tick "$EXPECTED_TICK" \
  --arg esc "${POST_escrow:-}" --arg li "${POST_liability:-}" --arg ca2 "${POST_carry:-}" --arg krd "2" \
  '{checkpoint:$cp, committed_height:$h, clock_h_minus_1:$cb, clock_h:$ca, clock_h_plus_1:$cf,
    measured_tick_per_block:$tick, escrow:$esc, liability:$li, carry:$ca2,
    key_rotation_delay_blocks:$krd}' >>"$ECONOMICS" || die "could not write the post-upgrade checkpoint"
phase_end "economics" "escrow=${POST_escrow:-?} liability=${POST_liability:-?} carry=${POST_carry:-?}"

echo
phase_begin
echo "=== 15. the stale validator fails CLOSED, it does not fork ==="
# node3 is restarted on the OLD binary AFTER the network has crossed H. The
# dangerous outcome is not that it stops — it is that it follows the upgraded
# chain using pre-migration logic, producing state nobody else has.
#
# The log is append-only and already contains the refusal from phase 8, so a bare
# grep would find that old line and pass no matter what happened now. The offset
# is recorded first and only content written AFTER it counts.
NODE3_LOG="$NET/logs/node3.log"
LOG_OFFSET=$(wc -l <"$NODE3_LOG" 2>/dev/null | tr -d ' ' || echo 0)
# node3's pre-restart height is the value phase 8 already observed and asserted;
# it is stopped now, so there is nothing live to ask.
A3_BEFORE="${MAXAPP[3]}"
expect "stale_A_starts_from_H_minus_1" "$((UPGRADE_HEIGHT - 1))" "$A3_BEFORE" 3
unset NODE_BIN_3
# Hash the file the launcher will exec, BEFORE starting it. The process itself
# cannot be read afterwards: node3 refuses during replay and exits, so by the time
# there is anything to observe there is no process left. start_node execs exactly
# $(node_bin 3), so hashing that path establishes which bytes ran.
LAUNCH_PATH="$(node_bin 3)"
if read_required_str LAUNCH_SHA sha256_of "$LAUNCH_PATH"; then
  expect "stale_node_launches_binary_A" "$SHA_A" "$LAUNCH_SHA" 3
else fail "node3: the binary about to be launched could not be hashed"; fi
start_node 3
sleep 20
NEW_REFUSAL="$(tail -n +$((LOG_OFFSET + 1)) "$NODE3_LOG" 2>/dev/null \
  | grep -c "UPGRADE .${UPGRADE_NAME}. NEEDED at height: ${UPGRADE_HEIGHT}" || true)"
if [[ "$NEW_REFUSAL" =~ ^[0-9]+$ ]] && (( NEW_REFUSAL >= 1 )); then
  ok "stale_A_new_refusal_after_restart ($NEW_REFUSAL new refusal(s) after offset $LOG_OFFSET)"
  record_assert 3 "stale_A_new_refusal_after_restart" ">=1" "$NEW_REFUSAL" PASS
else
  fail "stale_A_new_refusal_after_restart: no NEW refusal after offset $LOG_OFFSET (found ${NEW_REFUSAL:-0})"
  record_assert 3 "stale_A_new_refusal_after_restart" ">=1" "${NEW_REFUSAL:-0}" FAIL
fi
if read_required_uint A3_AFTER app_height_after_offset 3 "$LOG_OFFSET"; then
  expect "stale_A_did_not_commit_H" "$((UPGRADE_HEIGHT - 1))" "$A3_AFTER" 3
else fail "node3's committed height after the failed start could not be read"; fi
record_phase "stale_A_negative" "$([[ $FAILURES -eq 0 ]] && echo PASS || echo FAIL)" "offset=$LOG_OFFSET new_refusals=${NEW_REFUSAL:-0}"

echo
phase_begin
echo "=== 16. the fourth validator upgrades and rejoins ==="
stop_node 3; sleep 3
eval "export NODE_BIN_3=\"$BIN_B\""
start_node 3
CAUGHT=0; DEADLINE_TS=$((SECONDS + 300))
while ((SECONDS < DEADLINE_TS)); do
  a3="$(app_height 3 2>/dev/null)"; a0="$(app_height 0 2>/dev/null)"
  if [[ "$a3" =~ ^[0-9]+$ && "$a0" =~ ^[0-9]+$ ]] && (( a3 >= UPGRADE_HEIGHT + 2 && a0 - a3 <= 3 )); then CAUGHT=1; break; fi
  sleep 2
done
expect "stale_node_caught_up_on_B" "caught_up" "$([[ $CAUGHT -eq 1 ]] && echo caught_up || echo stalled)" 3
if read_required_uint KRD3 krd_at 3; then expect "migration_applied" "2" "$KRD3" 3; fi
agree_nodes "0 1 2 3" "all-four-converged" || fail "the four validators do not agree after node3 rejoined"

# Full version maps, compared exactly across all four — names, versions, and no
# extras. Checking names alone would miss a node that disagreed on a version.
VM_REF=""
for n in 0 1 2 3; do
  if read_required_str VM version_map_canon "$n"; then
    [[ -z "$VM_REF" ]] && VM_REF="$VM"
    expect "version_map_matches_node0" "$VM_REF" "$VM" "$n"
  else fail "node$n: the module version map could not be read"; fi
done
[[ -n "$VM_REF" ]] && note "canonical version map: $VM_REF"
phase_end "node3_catchup" "version_map=${VM_REF:0:60}"

echo
phase_begin
echo "=== 17. a restart with stale metadata does not re-run the migration ==="
# The stale file is a PRECONDITION of this proof, not a convenience: a restart
# without it is an ordinary restart and proves nothing about replay. Because the
# migration requires key_rotation_delay_blocks == 1 on entry, a second execution
# would fail and the node could not produce blocks — so resuming IS the proof.
STALE="$(node_home 0)/data/upgrade-info.json"
if [[ -f "$STALE" ]]; then
  expect "stale_upgrade_info_present" "present" "present" 0
  expect "stale_upgrade_info_name" "$UPGRADE_NAME" "$(jq -r '.name // empty' "$STALE")" 0
  expect "stale_upgrade_info_height" "$UPGRADE_HEIGHT" "$(jq -r '.height // empty' "$STALE")" 0
  cp "$STALE" "$EVID_DIR/node0-stale-upgrade-info-before-restart.json" || die "could not preserve the stale metadata"
else
  expect "stale_upgrade_info_present" "present" "missing" 0
fi
if read_required_uint BEFORE_RESTART app_height 0; then
  stop_node 0; sleep 3; start_node 0
  PROGRESSED=0; DEADLINE_TS=$((SECONDS + 180))
  while ((SECONDS < DEADLINE_TS)); do
    a0="$(app_height 0 2>/dev/null)"
    [[ "$a0" =~ ^[0-9]+$ ]] && (( a0 > BEFORE_RESTART )) && { PROGRESSED=1; break; }
    sleep 2
  done
  expect "restart_with_stale_metadata_resumed" "resumed" "$([[ $PROGRESSED -eq 1 ]] && echo resumed || echo stalled)" 0
  [[ -f "$STALE" ]] && expect "stale_upgrade_info_still_present_after_restart" "present" "present" 0 \
    || expect "stale_upgrade_info_still_present_after_restart" "present" "missing" 0
  if read_required_uint KRD0 krd_at 0; then expect "migration_ran_exactly_once" "2" "$KRD0" 0; fi
else fail "node0's height before the restart could not be read"; fi
phase_end "exactly_once" "krd=${KRD0:-?}"

echo
echo "================= upgrade drill ======================="
echo "  source        $SOURCE_SHA"
echo "  binary A      ${SHA_A:0:16}  (no $UPGRADE_NAME)"
echo "  binary B      ${SHA_B:0:16}  (carries $UPGRADE_NAME)"
echo "  topology      $SLOTS_ACTIVE active slots / $VAL_COUNT validators, power $VAL_MIN"
echo "  epoch length  $EPOCH_LEN"
echo "  upgrade at    H=$UPGRADE_HEIGHT   (mid-epoch-2, deadline clock $DEADLINE)"
echo "  settlement    slot $SLOT_ID epoch $SETTLE_EPOCH, open across the boundary"
echo "  all 4 nodes   application halted at $((UPGRADE_HEIGHT - 1)), upgrade-info written"
echo "  downtime      measured ${HALT_ELAPSED}s, 0 blocks, 0 clock ticks"
echo "  rollout       nodes 0,1,2 -> B crossed H with node3 still on A"
echo "  stale node    node3 on A produced ${NEW_REFUSAL:-0} NEW refusal(s) and stayed at $((UPGRADE_HEIGHT - 1))"
echo "  catch-up      node3 -> B rejoined; all four agree"
echo "  migration     key_rotation_delay_blocks 1 -> 2, exactly once"
echo "  clock         $C_BEFORE -> $C_AT -> $C_AFTER  (measured tick $EXPECTED_TICK/block)"
echo "  evidence      $EVID_DIR"
echo "======================================================="
finish
