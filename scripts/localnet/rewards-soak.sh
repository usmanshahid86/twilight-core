#!/usr/bin/env bash
set -uo pipefail

# x/rewards soak harness.
#
# Runs the four-node localnet for SOAK_DURATION seconds with a short rewards
# epoch, continuously asserting determinism + accounting at every epoch boundary,
# and periodically exercising claims, an authority param update, an emergency
# pause/resume, and a node restart. Emits soak.log + soak-summary.md under $NET.
#
# Same harness, two durations: short+supervised locally; long+unattended on cloud.
# See docs/research/x-rewards-soak-harness-design.md.

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
BIN="${BIN:-$ROOT/build/twilightd}"
NET="${TWILIGHT_LOCALNET_HOME:-/tmp/twilight-rewards-soak}"
CHAIN_ID="${CHAIN_ID:-twilight-rewards-soak-1}"
NODE_COUNT=4

SOAK_DURATION="${SOAK_DURATION:-240}"
EPOCH_LENGTH="${EPOCH_LENGTH:-4}"
SUBSIDY="${SUBSIDY:-416190}"
PREMINE="${PREMINE:-on}"
CLAIM_EVERY_N="${CLAIM_EVERY_N:-3}"
CHAOS="${CHAOS:-on}"
PAUSE_CYCLE="${PAUSE_CYCLE:-on}"
PARAM_DRILL="${PARAM_DRILL:-on}"
HALT_TIMEOUT="${HALT_TIMEOUT:-30}"
RESUME="${RESUME:-off}"   # on = continue the chain already in $NET (no wipe/init)
KEYRING=(--keyring-backend test)

EPOCH_EMISSION=$((EPOCH_LENGTH * SUBSIDY))
PER_SLOT=$((EPOCH_EMISSION / NODE_COUNT))
GENESIS_FUNDED=1000000000000
export BIN NET CHAIN_ID TWILIGHT_LOCALNET_HOME="$NET"

LOG="$NET/soak.log"
SUMMARY="$NET/soak-summary.md"
FAILURES=0
HALTED=0
CLAIMS=0
CLAIMED_TOTAL=0
LAST_CLAIMED_EPOCH=0

need() { command -v "$1" >/dev/null 2>&1 || { echo "missing required command: $1" >&2; exit 2; }; }
need curl; need jq

rpc_url()  { echo "tcp://127.0.0.1:$((26657 + $1 * 100))"; }
http_url() { echo "http://127.0.0.1:$((26657 + $1 * 100))"; }
ts() { date -u +"%Y-%m-%dT%H:%M:%SZ"; }
human() { awk -v b="${1:-0}" 'BEGIN{u="B";s=b;if(s>=1024){s/=1024;u="KB"}if(s>=1024){s/=1024;u="MB"}if(s>=1024){s/=1024;u="GB"}printf "%.2f %s",s,u}'; }
log() { echo "[$(ts)] $*" | tee -a "$LOG"; }
record_fail() { FAILURES=$((FAILURES + 1)); log "FAIL: $*"; }

latest_height() {
  curl -fsS "$(http_url "$1")/status" 2>/dev/null \
    | jq -r '.result.sync_info.latest_block_height | tonumber' 2>/dev/null || echo 0
}
rq() { "$BIN" rewards-query "$@" --node "$(rpc_url 0)" --output json 2>/dev/null; }
bank_balance() {
  "$BIN" query bank balances "$1" --node "$(rpc_url 0)" --output json 2>/dev/null \
    | jq -r '[.balances[]? | select(.denom=="utwlt") | .amount] | first // "0"'
}
current_epoch() { rq epoch-info | jq -r '.state.current_epoch | tonumber' 2>/dev/null || echo 0; }
cumulative()    { rq cumulative-emitted | jq -r '.cumulative_emitted' 2>/dev/null || echo 0; }
module_balance(){ rq module-balances | jq -r '.rewards_balance' 2>/dev/null || echo 0; }
# claimed flag for (slot 1, epoch) from the proven rewards query.
# Is slot 1's reward for epoch $1 claimed? Use the TARGETED `claimable` query
# (ClaimableRewards: single epoch range, no pagination, returns only UNCLAIMED
# records) — never the paginated `slot-rewards` list, whose default --limit page
# is ascending by epoch and so DROPS the just-finalized epoch once the chain has
# more than a page of records (the cause of the false "not marked claimed" FAILs).
# Empty claimable set for [$1,$1] => claimed; a present record => not yet claimed.
# (Slot 1 is always active here with a positive reward, so a missing record can
# only mean claimed, not a zero-amount skip.)
slot_claimed()  { rq claimable 1 "$1" "$1" | jq -e '(.rewards | length) == 0' >/dev/null 2>&1 && echo true || echo false; }
# The claim record can still read one block behind the tx's /tx commit, so retry
# briefly for the expected state before asserting (cf. agree_at_retry).
slot_claimed_is() { # want_bool epoch  -- true within ~10s, else last-seen value
  local want="$1" epoch="$2" got i
  for i in 1 2 3 4 5 6 7 8 9 10; do
    got="$(slot_claimed "$epoch")"; [[ "$got" == "$want" ]] && { echo "$got"; return; }
    sleep 1
  done
  echo "$got"
}

# Minimum latest height across all nodes — a height every node has surely
# committed, so a cross-node hash compare there can't false-positive on a peer
# that is merely a block or two behind under load.
min_height_all() {
  local m="" node h
  for node in 0 1 2 3; do
    h="$(latest_height "$node")"
    { [[ -z "$m" ]] || (( h < m )); } && m=$h
  done
  echo "${m:-0}"
}
wait_all_height() {
  local target="$1" deadline=$((SECONDS + 90)) node
  while ((SECONDS < deadline)); do
    local ready=1
    for node in 0 1 2 3; do (($(latest_height "$node") < target)) && { ready=0; break; }; done
    ((ready == 1)) && return 0
    sleep 1
  done
  return 1
}
agree_at() {
  local h="$1"
  if MIN_HEIGHT="$h" "$ROOT/scripts/localnet/agree.sh" >>"$LOG" 2>&1; then return 0; fi
  return 1
}
# Block until a node reports sync_info.catching_up == false (fast-sync done).
wait_synced() {
  local node="$1" deadline=$((SECONDS + 60))
  while ((SECONDS < deadline)); do
    [[ "$(curl -fsS "$(http_url "$node")/status" 2>/dev/null | jq -r '.result.sync_info.catching_up' 2>/dev/null)" == "false" ]] && return 0
    sleep 1
  done
  return 1
}
# Retry the cross-node agree check a few times — right after a restart the
# rejoining node can still report catching_up briefly even with a matching hash.
agree_at_retry() {
  local h="$1" tries="${2:-5}" i
  for ((i = 1; i <= tries; i++)); do
    agree_at "$h" && return 0
    sleep 3
  done
  return 1
}

write_summary() {
  local verdict="$1" epoch="$2" cum="$3" modbal="$4" dbk="$5"
  cat >"$SUMMARY" <<EOF
# x/rewards Soak — running summary

| Field | Value |
|---|---|
| Updated | $(ts) |
| Verdict (so far) | **$verdict** |
| Chain id | $CHAIN_ID |
| Nodes | $NODE_COUNT |
| Premine | $PREMINE |
| Epoch length | $EPOCH_LENGTH blocks |
| Epoch emission | ${EPOCH_EMISSION}utwlt |
| Per-slot reward | ${PER_SLOT}utwlt |
| Current epoch | $epoch |
| Epochs finalized | $((epoch > 0 ? epoch - 1 : 0)) |
| Cumulative emitted | ${cum}utwlt |
| Module balance | ${modbal}utwlt |
| Claims executed | $CLAIMS |
| Claimed total | ${CLAIMED_TOTAL}utwlt |
| Assertion failures | $FAILURES |
| Halt detected | $((HALTED == 1 ? 1 : 0)) |
| node0 data size | ${dbk} KB |
| Duration target | ${SOAK_DURATION}s |
EOF
}

# Submit a tx and block until it is included, echoing the result code:
#   "0"           = DeliverTx success
#   "<n>"         = DeliverTx failure (the message handler ran in the block and
#                  rejected it — this app runs handlers at DeliverTx, not CheckTx)
#   "rejected:<c>"= CheckTx rejected (ante/sequence) — never entered a block
#   "not_included"= accepted but never mined within the deadline
# Blocking until inclusion also serializes same-signer txs so account sequences
# advance correctly between calls.
submit_and_wait() { # signer_node from_key  -- rest are tx args
  local node="$1" from="$2"; shift 2
  local out ccode hash
  out="$("$BIN" "$@" --from "$from" "${KEYRING[@]}" --home "$NET/node$node" \
        --chain-id "$CHAIN_ID" --node "$(rpc_url 0)" --gas 600000 --fees 0utwlt \
        --broadcast-mode sync --output json -y 2>/dev/null || true)"
  ccode="$(jq -r '.code // 1' <<<"$out" 2>/dev/null || echo 1)"
  hash="$(jq -r '.txhash // ""' <<<"$out" 2>/dev/null || echo "")"
  if [[ "$ccode" != "0" || -z "$hash" ]]; then echo "rejected:$ccode"; return; fi
  wait_tx_code "$hash"
}
wait_tx_code() {
  local hash="$1" deadline=$((SECONDS + 30)) result
  while ((SECONDS < deadline)); do
    result="$(curl -fsS "$(http_url 0)/tx?hash=0x$hash" 2>/dev/null || true)"
    if [[ -n "$result" ]] && jq -e '.result.tx_result' >/dev/null 2>&1 <<<"$result"; then
      jq -r '.result.tx_result.code // 0' <<<"$result"; return 0
    fi
    sleep 1
  done
  echo "not_included"
}

# ---------------------------------------------------------------------------
# Setup
# ---------------------------------------------------------------------------
if [[ "$RESUME" != "on" ]]; then
  rm -rf "$NET"
  "$ROOT/scripts/localnet/init.sh"
  mkdir -p "$NET/logs"

  for node in 0 1 2 3; do
    genesis="$NET/node$node/config/genesis.json"; tmp="$genesis.tmp"
    jq --arg e "$EPOCH_LENGTH" '
      .app_state.rewards.params.epoch_length_blocks = $e
      | .app_state.rewards.current_epoch_config.epoch_length_blocks = $e
    ' "$genesis" >"$tmp" && mv "$tmp" "$genesis"
    if [[ "$PREMINE" == "off" ]]; then
      jq '.app_state.bank.balances = [] | .app_state.bank.supply = []' "$genesis" >"$tmp" && mv "$tmp" "$genesis"
    fi
  done
  : >"$LOG"
else
  [[ -f "$NET/operator0.json" && -d "$NET/node0/data" ]] || { echo "RESUME=on but no existing chain at $NET" >&2; exit 2; }
  mkdir -p "$NET/logs"
fi

operator0="$(jq -r '.address' "$NET/operator0.json")"  # slot 1 payout + authority
operator1="$(jq -r '.address' "$NET/operator1.json")"  # claim signer + emergency authority

[[ "$RESUME" == "on" ]] && phase="resume" || phase="start"
log "soak $phase: duration=${SOAK_DURATION}s epoch_length=$EPOCH_LENGTH premine=$PREMINE emission/epoch=$EPOCH_EMISSION per_slot=$PER_SLOT"
"$ROOT/scripts/localnet/start.sh"
trap '"$ROOT/scripts/localnet/stop.sh" >/dev/null 2>&1 || true' EXIT

# ---------------------------------------------------------------------------
# Baseline
# ---------------------------------------------------------------------------
START=$SECONDS
did_pause=0; did_chaos=0; did_param=0

if [[ "$RESUME" != "on" ]]; then
  if ! wait_all_height 2; then record_fail "nodes did not reach height 2"; HALTED=1; fi
  agree_at 2 || record_fail "no cross-node agreement at height 2"
  [[ "$(module_balance)" == "0" ]] || record_fail "module balance nonzero before any finalization"
else
  # Continue an existing chain: wait for the nodes to come back up, re-derive the
  # already-claimed total from the coverage identity (module == cumulative - claimed),
  # baseline the loop at the current epoch, and skip the one-shot drills (already
  # proven in the prior run) so this is a pure continuous endurance pass.
  cur_h="$(latest_height 0)"
  if ! wait_all_height "$cur_h"; then record_fail "resumed nodes did not reach height $cur_h"; HALTED=1; fi
  agree_at_retry "$(min_height_all)" || record_fail "resumed nodes do not agree on app hash"
  base_cum="$(cumulative)"; base_mod="$(module_balance)"
  CLAIMED_TOTAL=$((base_cum - base_mod))
  LAST_CLAIMED_EPOCH="$(current_epoch)"
  did_pause=1; did_chaos=1; did_param=2
  log "resumed at epoch $(current_epoch) height $cur_h: cumulative=$base_cum module=$base_mod derived_claimed_total=$CLAIMED_TOTAL (one-shot drills skipped)"
fi

# ---------------------------------------------------------------------------
# Soak loop
# ---------------------------------------------------------------------------
prev_epoch="$(current_epoch)"
last_height="$(latest_height 0)"
last_progress=$SECONDS
# Baselines for the disk-growth rate in the final report.
INIT_EPOCH="$prev_epoch"
INIT_DB_KB="$(du -sk "$NET/node0/data" 2>/dev/null | awk '{print $1}')"; INIT_DB_KB="${INIT_DB_KB:-0}"
INIT_TS=$SECONDS

while (( SECONDS - START < SOAK_DURATION )); do
  iter_t=$SECONDS
  sleep 2
  # If our own 2s sleep elapsed far more wall-clock than asked, the whole process
  # was frozen (host sleep / heavy throttle) — every node froze together, so this
  # is NOT a chain halt. Don't penalize it: reset the progress window, re-baseline
  # height, and extend the soak so frozen time doesn't eat the intended runtime.
  if (( SECONDS - iter_t > HALT_TIMEOUT )); then
    frozen=$((SECONDS - iter_t))
    log "NOTE: host suspended ~${frozen}s (sleep/throttle) — not a chain halt; resetting progress window and extending soak by ${frozen}s"
    last_progress=$SECONDS; last_height="$(latest_height 0)"; SOAK_DURATION=$((SOAK_DURATION + frozen))
    continue
  fi
  h="$(latest_height 0)"
  if (( h > last_height )); then last_height=$h; last_progress=$SECONDS; fi
  if (( SECONDS - last_progress > HALT_TIMEOUT )); then
    HALTED=1; log "CRITICAL: no height progress for ${HALT_TIMEOUT}s (height stuck at $h) — possible fail-closed halt"; break
  fi

  epoch="$(current_epoch)"
  (( epoch <= prev_epoch )) && continue
  # Epoch advanced: epoch-1 just finalized. (handle multi-advance by looping)
  while (( prev_epoch < epoch )); do
    finalized=$prev_epoch                      # the epoch that just closed
    prev_epoch=$((prev_epoch + 1))
    (( finalized < 1 )) && continue

    # Compare hashes at the min height every node has committed, with retry, so a
    # peer that is 1-2 blocks behind under load is not misread as a hash fork.
    boundary_h="$(min_height_all)"
    agree_at_retry "$boundary_h" || record_fail "app-hash divergence at epoch $finalized (height $boundary_h)"

    cum="$(cumulative)"
    expected_cum=$((finalized * EPOCH_EMISSION))
    [[ "$cum" == "$expected_cum" ]] || record_fail "cumulative=$cum != expected $expected_cum after epoch $finalized"

    modbal="$(module_balance)"
    expected_mod=$((expected_cum - CLAIMED_TOTAL))
    [[ "$modbal" == "$expected_mod" ]] || record_fail "module balance=$modbal != expected $expected_mod (cumulative-claimed) after epoch $finalized"

    dbk="$(du -sk "$NET/node0/data" 2>/dev/null | awk '{print $1}')"
    log "epoch $finalized finalized: cumulative=$cum module=$modbal db=${dbk}KB"

    # --- periodic claim of the just-finalized epoch ---
    # Verified via proven rewards queries: the claim record flips claimed=true, a
    # double claim fails, and the next epoch's coverage invariant confirms exactly
    # PER_SLOT left the module account (the live `query bank balances` path is not
    # used — it was never validated and races block production).
    if (( finalized % CLAIM_EVERY_N == 0 )) && (( finalized > LAST_CLAIMED_EPOCH )); then
      [[ "$(slot_claimed_is false "$finalized")" == "false" ]] || record_fail "epoch $finalized claim record not unclaimed before claim"
      ccode="$(submit_and_wait 1 operator1 rewards claim 1 "$finalized" "$finalized")"
      if [[ "$ccode" == "0" ]]; then
        [[ "$(slot_claimed_is true "$finalized")" == "true" ]] || record_fail "epoch $finalized not marked claimed after successful claim"
        dcode="$(submit_and_wait 1 operator1 rewards claim 1 "$finalized" "$finalized")"
        [[ "$dcode" != "0" ]] || record_fail "double claim of epoch $finalized succeeded"
        CLAIMS=$((CLAIMS + 1)); CLAIMED_TOTAL=$((CLAIMED_TOTAL + PER_SLOT)); LAST_CLAIMED_EPOCH=$finalized
        log "claim epoch $finalized: record claimed, double-claim rejected (code $dcode), coverage tracked"
      else
        record_fail "claim epoch $finalized not successful (code $ccode)"
      fi
    fi

    # --- one emergency pause/resume cycle ---
    # submit_and_wait blocks until each tx is in a block, so the params query
    # reflects committed state and same-signer (operator1) sequences don't collide.
    # The paused claim is expected to FAIL at DeliverTx (handlers run in the block,
    # not at CheckTx), so a non-zero code there is the success condition.
    if [[ "$PAUSE_CYCLE" == "on" ]] && (( did_pause == 0 )) && (( finalized >= 2 )); then
      did_pause=1
      pcode="$(submit_and_wait 1 operator1 rewards pause --claims)"
      [[ "$pcode" == "0" ]] || record_fail "pause tx failed (code $pcode)"
      enabled="$(rq params | jq -r '.params.claims_enabled')"
      [[ "$enabled" == "false" ]] || record_fail "pause --claims did not disable claims (claims_enabled=$enabled)"
      bcd="$(submit_and_wait 1 operator1 rewards claim 1 1 1)"
      [[ "$bcd" != "0" ]] || record_fail "claim succeeded while claims paused"
      rcode="$(submit_and_wait 1 operator1 rewards resume --claims)"
      [[ "$rcode" == "0" ]] || record_fail "resume tx failed (code $rcode)"
      enabled="$(rq params | jq -r '.params.claims_enabled')"
      [[ "$enabled" == "true" ]] || record_fail "resume --claims did not re-enable claims (claims_enabled=$enabled)"
      log "pause/resume cycle OK (emergency authority; paused-claim rejected code $bcd)"
    fi

    # --- one authority param update + pending activation ---
    if [[ "$PARAM_DRILL" == "on" ]] && (( did_param == 0 )) && (( finalized >= 3 )); then
      did_param=1
      rq params | jq '.params | .max_claim_epochs_per_tx = "50"' >"$NET/params.json"
      ucode="$(submit_and_wait 0 operator0 rewards update-params "$NET/params.json")"
      [[ "$ucode" == "0" ]] || record_fail "update-params failed (code $ucode)"
      pend="$(rq epoch-info | jq -r '.has_pending_params')"
      [[ "$pend" == "true" ]] || record_fail "update-params did not queue (has_pending_params=$pend)"
      log "update-params queued (authority); awaiting boundary activation"
    elif [[ "$PARAM_DRILL" == "on" ]] && (( did_param == 1 )); then
      mce="$(rq params | jq -r '.params.max_claim_epochs_per_tx')"
      if [[ "$mce" == "50" ]]; then
        pend="$(rq epoch-info | jq -r '.has_pending_params')"
        [[ "$pend" == "false" ]] || record_fail "pending params not cleared after activation"
        log "pending params activated at boundary (max_claim_epochs_per_tx=50)"
        did_param=2
      fi
    fi

    # --- one chaos node restart ---
    if [[ "$CHAOS" == "on" ]] && (( did_chaos == 0 )) && (( finalized >= 4 )); then
      did_chaos=1
      pid="$(cat "$NET/node3.pid" 2>/dev/null)"
      log "chaos: killing node3 (pid $pid)"
      [[ -n "$pid" ]] && kill "$pid" 2>/dev/null
      sleep 4
      "$BIN" start --home "$NET/node3" --minimum-gas-prices 0utwlt --log_no_color >"$NET/logs/node3.log" 2>&1 &
      echo "$!" >"$NET/node3.pid"
      log "chaos: restarted node3; waiting for catch-up"
      wait_synced 3 || log "chaos: node3 still reports catching_up after wait (continuing to agree check)"
      cur="$(latest_height 0)"
      if wait_all_height "$cur"; then
        if agree_at_retry "$cur"; then
          log "chaos: node3 caught up and agrees at height $cur"
        else
          record_fail "node3 did not re-agree after restart"
        fi
      else
        record_fail "node3 did not catch up after restart"
      fi
    fi

    write_summary "$([[ $FAILURES -eq 0 && $HALTED -eq 0 ]] && echo RUNNING-CLEAN || echo PROBLEMS)" "$epoch" "$cum" "$modbal" "${dbk:-0}"
  done
done

# ---------------------------------------------------------------------------
# Final metrics + invariant summary + disk-growth + state export
# ---------------------------------------------------------------------------
# Stop nodes (unlock the DB) and export canonical state — the single consistent
# end-of-run snapshot. final-export.json is an audit artifact + import fixture.
# Sourcing every final number from the export (not live queries taken before the
# stop) removes the 1-2 block race between the last read and the actual halt.
"$ROOT/scripts/localnet/stop.sh" >/dev/null 2>&1 || true
sleep 3
EXPORT="$NET/final-export.json"
if "$BIN" export --home "$NET/node0" >"$EXPORT" 2>>"$LOG"; then
  log "exported final app state -> $EXPORT ($(human "$(wc -c <"$EXPORT")"))"
else
  log "WARN: final state export failed"; : >"$EXPORT"
fi
final_db="$(du -sk "$NET/node0/data" 2>/dev/null | awk '{print $1}')"; final_db="${final_db:-0}"

# Census from the export (verified gogoproto-JSON paths; pagination-proof). claimed
# is omitted when false (proto3), so select(.claimed!=true) captures unclaimed.
premine_amt=$([[ "$PREMINE" == "on" ]] && echo $(( 2 * GENESIS_FUNDED )) || echo 0)
ex_cum=0; ex_carry=0; ex_epoch=0; total_supply=0
rec_count=0; claimed_count=0; claimed_amount=0; unclaimed_total=0
if [[ -s "$EXPORT" ]] && jq -e . "$EXPORT" >/dev/null 2>&1; then
  ex_cum=$(jq -r '.app_state.rewards.state.cumulative_emitted // "0"' "$EXPORT")
  ex_carry=$(jq -r '.app_state.rewards.state.carry_forward_remainder // "0"' "$EXPORT")
  ex_epoch=$(jq -r '.app_state.rewards.state.current_epoch // "0"' "$EXPORT")
  total_supply=$(jq -r '[.app_state.bank.supply[]? | select(.denom=="utwlt") | .amount] | first // "0"' "$EXPORT")
  rec_count=$(jq '[.app_state.rewards.claim_records[]?] | length' "$EXPORT")
  claimed_count=$(jq '[.app_state.rewards.claim_records[]? | select(.claimed==true)] | length' "$EXPORT")
  claimed_amount=$(jq '[.app_state.rewards.claim_records[]? | select(.claimed==true) | .amount | tonumber] | add // 0' "$EXPORT")
  unclaimed_total=$(jq '[.app_state.rewards.claim_records[]? | select(.claimed!=true) | .amount | tonumber] | add // 0' "$EXPORT")
  for v in ex_cum ex_carry ex_epoch total_supply rec_count claimed_count claimed_amount unclaimed_total; do
    [[ "${!v}" =~ ^[0-9]+$ ]] || printf -v "$v" 0
  done
else
  record_fail "final state export missing/invalid — could not verify end-state invariants"
fi
finalized_count=$(( ex_epoch > 0 ? ex_epoch - 1 : 0 ))
module_bal=$(( ex_cum - claimed_amount ))   # what the module account holds = unclaimed + carry

# Cross-check invariants (all export-internal — one snapshot, race-free).
if [[ -s "$EXPORT" ]]; then
  expected_supply=$(( ex_cum + premine_amt ))
  [[ "$total_supply" == "$expected_supply" ]] || record_fail "total supply $total_supply != cumulative+premine $expected_supply"
  (( rec_count == finalized_count * NODE_COUNT )) || record_fail "claim records $rec_count != finalized*$NODE_COUNT $(( finalized_count * NODE_COUNT ))"
  (( claimed_amount + unclaimed_total + ex_carry == ex_cum )) || record_fail "claimed($claimed_amount)+unclaimed($unclaimed_total)+carry($ex_carry) != cumulative($ex_cum)"
fi

# Disk-growth rate + linear projections (state is never pruned).
epochs_session=$(( ex_epoch - INIT_EPOCH )); (( epochs_session < 1 )) && epochs_session=1
elapsed=$(( SECONDS - INIT_TS )); (( elapsed < 1 )) && elapsed=1
grow_kb=$(( final_db - INIT_DB_KB )); (( grow_kb < 0 )) && grow_kb=0
bytes_per_epoch=$(( grow_kb * 1024 / epochs_session ))
epochs_per_hour=$(( epochs_session * 3600 / elapsed ))
proj_24h=$(( bytes_per_epoch * epochs_per_hour * 24 ))
proj_48h=$(( bytes_per_epoch * epochs_per_hour * 48 ))
proj_7d=$(( bytes_per_epoch * epochs_per_hour * 168 ))

# ---------------------------------------------------------------------------
# Final verdict
# ---------------------------------------------------------------------------
VERDICT="PASS"
(( FAILURES > 0 )) && VERDICT="FAIL"
(( HALTED == 1 )) && VERDICT="FAIL (HALT)"
write_summary "$VERDICT" "$ex_epoch" "$ex_cum" "$module_bal" "$final_db"

FINAL_REPORT="$NET/soak-final-report.md"
cat >"$FINAL_REPORT" <<EOF
# x/rewards Soak — Final Report

| Field | Value |
|---|---|
| Generated | $(ts) |
| Verdict | **$VERDICT** |
| Chain id | $CHAIN_ID |
| Premine | $PREMINE |
| Epoch length | $EPOCH_LENGTH blocks |
| Assertion failures | $FAILURES |
| Halt detected | $((HALTED == 1 ? 1 : 0)) |

## Invariant summary (from canonical export)

| Metric | Value |
|---|---|
| Finalized epochs | $finalized_count |
| Claim records | $rec_count (expected $(( finalized_count * NODE_COUNT )) = finalized × $NODE_COUNT slots) |
| Claimed records | $claimed_count |
| Claimed total | ${claimed_amount}utwlt |
| Unclaimed total | ${unclaimed_total}utwlt |
| Carry-forward remainder | ${ex_carry}utwlt |
| Module balance | ${module_bal}utwlt (= cumulative − claimed = unclaimed + carry) |
| Cumulative emitted | ${ex_cum}utwlt |
| Total supply | ${total_supply}utwlt (= cumulative + premine ${premine_amt}) |

Cross-checks (each counted as a failure above on mismatch): total supply ==
cumulative + premine; claim records == finalized × active slots;
claimed + unclaimed + carry == cumulative.

## Disk-growth rate

| Metric | Value |
|---|---|
| Initial DB (node0) | $(human $(( INIT_DB_KB * 1024 ))) |
| Final DB (node0) | $(human $(( final_db * 1024 ))) |
| Growth this run | $(human $(( grow_kb * 1024 ))) over $epochs_session epochs / ${elapsed}s |
| Bytes per finalized epoch | $(human "$bytes_per_epoch") |
| Epochs per hour (observed) | $epochs_per_hour |
| Projected 24h growth | $(human "$proj_24h") |
| Projected 48h growth | $(human "$proj_48h") |
| Projected 7d growth | $(human "$proj_7d") |

Projections are linear extrapolation of the observed rate (no pruning of
FinalizedEpochs/ClaimRecords); production epoch length differs, so scale by
(production_epoch_length / $EPOCH_LENGTH) for real cadence.

## Artifacts

- \`$LOG\`
- \`$SUMMARY\`
- \`$EXPORT\` (canonical exported app state)
EOF

log "soak end: verdict=$VERDICT epochs_finalized=$finalized_count records=$rec_count claimed=$claimed_count claimed_total=${claimed_amount} unclaimed=${unclaimed_total} carry=${ex_carry} module=${module_bal} cumulative=${ex_cum} supply=${total_supply} failures=$FAILURES halted=$HALTED"
log "disk: ${grow_kb}KB/${epochs_session}ep -> $(human "$bytes_per_epoch")/epoch; proj 24h=$(human "$proj_24h") 48h=$(human "$proj_48h") 7d=$(human "$proj_7d")"
echo
echo "==================== SOAK $VERDICT ===================="
echo "  finalized epochs : $finalized_count"
echo "  claim records    : $rec_count  (claimed $claimed_count)"
echo "  unclaimed total  : ${unclaimed_total}utwlt   module: ${module_bal}utwlt   carry: ${ex_carry}"
echo "  cumulative       : ${ex_cum}utwlt   supply: ${total_supply}utwlt"
echo "  assertion fails  : $FAILURES    halt: $HALTED"
echo "  DB node0         : $(human $(( final_db * 1024 )))   $(human "$bytes_per_epoch")/epoch"
echo "  proj growth      : 24h $(human "$proj_24h") | 48h $(human "$proj_48h") | 7d $(human "$proj_7d")"
echo "  artifacts        : $FINAL_REPORT | $EXPORT"
echo "======================================================="
[[ "$VERDICT" == "PASS" ]]
