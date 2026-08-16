#!/usr/bin/env bash
set -euo pipefail

# Phase 10 release-readiness proof for x/rewards.
#
# This creates an isolated four-node localnet with a short rewards epoch, proves
# finalization and query behavior, submits a real claim transaction, checks
# cross-node hashes after both state transitions, and exports state for exact
# bank/supply assertions. Production defaults and the normal localnet smoke are
# unchanged.

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
BIN="${BIN:-$ROOT/build/twilightd}"
NET="${TWILIGHT_LOCALNET_HOME:-/tmp/twilight-rewards-localnet}"
CHAIN_ID="${CHAIN_ID:-twilight-rewards-localnet-1}"
NODE_COUNT=4
EPOCH_LENGTH="${REWARDS_EPOCH_LENGTH:-10}"
SUBSIDY=416190
EXPECTED_EMISSION=$((EPOCH_LENGTH * SUBSIDY))
EXPECTED_PER_SLOT=$((EXPECTED_EMISSION / NODE_COUNT))
EXPECTED_MODULE_AFTER_CLAIM=$((EXPECTED_EMISSION - EXPECTED_PER_SLOT))
GENESIS_FUNDED_BALANCE=1000000000000
EXPECTED_PAYOUT_AFTER_CLAIM=$((GENESIS_FUNDED_BALANCE + EXPECTED_PER_SLOT))
EXPECTED_SUPPLY_AFTER_FINALIZE=$((2 * GENESIS_FUNDED_BALANCE + EXPECTED_EMISSION))
KEYRING=(--keyring-backend test)

export BIN NET CHAIN_ID TWILIGHT_LOCALNET_HOME="$NET"

need() { command -v "$1" >/dev/null 2>&1 || { echo "missing required command: $1" >&2; exit 2; }; }
need curl
need jq

rpc_url() { echo "tcp://127.0.0.1:$((26657 + $1 * 100))"; }
http_url() { echo "http://127.0.0.1:$((26657 + $1 * 100))"; }
latest_height() {
  curl -fsS "$(http_url "$1")/status" 2>/dev/null | jq -r '.result.sync_info.latest_block_height | tonumber' 2>/dev/null || echo 0
}
wait_all_height() {
  local target="$1" deadline=$((SECONDS + 90)) node
  while ((SECONDS < deadline)); do
    local ready=1
    for node in 0 1 2 3; do
      if (($(latest_height "$node") < target)); then ready=0; break; fi
    done
    ((ready == 1)) && return 0
    sleep 1
  done
  echo "timed out waiting for all nodes to reach height $target" >&2
  return 1
}
wait_current_epoch() {
  local target="$1" deadline=$((SECONDS + 90)) current
  while ((SECONDS < deadline)); do
    current="$(rq epoch-info 2>/dev/null | jq -r '.state.current_epoch | tonumber' 2>/dev/null || echo 0)"
    ((current >= target)) && return 0
    sleep 1
  done
  echo "timed out waiting for rewards epoch $target" >&2
  return 1
}
rq() { "$BIN" rewards-query "$@" --node "$(rpc_url 0)" --output json 2>/dev/null; }
balance_from_export() {
  local address="$1" file="$2"
  jq -r --arg addr "$address" '
    [.app_state.bank.balances[]? | select(.address == $addr) | .coins[]? | select(.denom == "utwlt") | .amount]
    | first // "0"
  ' "$file"
}
wait_tx_code() {
  local hash="$1" deadline=$((SECONDS + 60)) result
  while ((SECONDS < deadline)); do
    result="$(curl -fsS "$(http_url 0)/tx?hash=0x$hash" 2>/dev/null || true)"
    if [[ -n "$result" ]] && jq -e '.result.tx_result' >/dev/null 2>&1 <<<"$result"; then
      jq -r '.result.tx_result.code // 0' <<<"$result"
      return 0
    fi
    sleep 1
  done
  echo "not_included"
}
submit_claim() {
  local out hash code
  out="$("$BIN" rewards claim 1 1 1 \
    --from operator1 "${KEYRING[@]}" --home "$NET/node1" \
    --chain-id "$CHAIN_ID" --node "$(rpc_url 0)" \
    --gas 600000 --fees 0utwlt --broadcast-mode sync --output json -y 2>/dev/null || true)"
  hash="$(jq -r '.txhash // ""' <<<"$out")"
  [[ -n "$hash" ]] || { echo "claim broadcast failed: $out" >&2; return 1; }
  code="$(wait_tx_code "$hash")"
  [[ "$code" == "0" ]] || { echo "claim DeliverTx failed: hash=$hash code=$code" >&2; return 1; }
  echo "$hash"
}
submit_double_claim_expect_failure() {
  local out hash code
  out="$("$BIN" rewards claim 1 1 1 \
    --from operator1 "${KEYRING[@]}" --home "$NET/node1" \
    --chain-id "$CHAIN_ID" --node "$(rpc_url 0)" \
    --gas 600000 --fees 0utwlt --broadcast-mode sync --output json -y 2>/dev/null || true)"
  hash="$(jq -r '.txhash // ""' <<<"$out")"
  if [[ -z "$hash" ]]; then
    return 0
  fi
  code="$(wait_tx_code "$hash")"
  [[ "$code" != "0" && "$code" != "not_included" ]]
}

"$ROOT/scripts/localnet/init.sh"

# Test-only profile: modify only this isolated localnet's rewards genesis.
for node in 0 1 2 3; do
  genesis="$NET/node$node/config/genesis.json"
  tmp="$genesis.tmp"
  jq --arg epoch "$EPOCH_LENGTH" '
    .app_state.rewards.params.epoch_length_blocks = $epoch
    | .app_state.rewards.current_epoch_config.epoch_length_blocks = $epoch
    # EpochConfigVersion is the sole epoch-geometry authority; the two lines above
    # are deprecated mirrors that fresh genesis requires to agree with it.
    | .app_state.rewards.epoch_config_versions[0].epoch_length_blocks = $epoch
  ' "$genesis" >"$tmp"
  mv "$tmp" "$genesis"
done

operator0="$(jq -r '.address' "$NET/operator0.json")"
operator1="$(jq -r '.address' "$NET/operator1.json")"

"$ROOT/scripts/localnet/start.sh"
trap '"$ROOT/scripts/localnet/stop.sh" || true' EXIT

# Before finalization: activity exists, epoch 1 is open, and rewards minted zero.
wait_all_height 2
epoch_before="$(rq epoch-info)"
active_before="$(rq current-active-blocks --limit 10)"
module_before="$(rq module-balances)"
cumulative_before="$(rq cumulative-emitted)"
[[ "$(jq -r '.state.current_epoch' <<<"$epoch_before")" == "1" ]]
[[ "$(jq -r '(.active_blocks // []) | length' <<<"$active_before")" == "4" ]]
[[ "$(jq -r '.rewards_balance' <<<"$module_before")" == "0" ]]
[[ "$(jq -r '.cumulative_emitted' <<<"$cumulative_before")" == "0" ]]
if rq epoch-reward 1 >/dev/null 2>&1; then
  echo "epoch 1 finalized before its configured boundary" >&2
  exit 1
fi
MIN_HEIGHT=2 "$ROOT/scripts/localnet/agree.sh"

# After finalization: exact emission, aggregate, claims, queries, and agreement.
wait_current_epoch 2
epoch_after="$(rq epoch-info)"
finalized="$(rq epoch-reward 1)"
slot_rewards="$(rq slot-rewards 1 --limit 10)"
claimable="$(rq claimable 1 1 1)"
module_after="$(rq module-balances)"
cumulative_after="$(rq cumulative-emitted)"
rq params >/dev/null
rq supply-schedule >/dev/null
rq current-active-blocks --limit 10 >/dev/null

[[ "$(jq -r '.state.current_epoch' <<<"$epoch_after")" == "2" ]]
[[ "$(jq -r '.epoch_reward.minted_emission' <<<"$finalized")" == "$EXPECTED_EMISSION" ]]
[[ "$(jq -r '.epoch_reward.allocated_amount' <<<"$finalized")" == "$EXPECTED_EMISSION" ]]
[[ "$(jq -r '.epoch_reward.carry_out' <<<"$finalized")" == "0" ]]
[[ "$(jq -r '.rewards[0].amount' <<<"$slot_rewards")" == "$EXPECTED_PER_SLOT" ]]
[[ "$(jq -r '.rewards[0].claimed' <<<"$slot_rewards")" == "false" ]]
[[ "$(jq -r '.total_amount' <<<"$claimable")" == "$EXPECTED_PER_SLOT" ]]
[[ "$(jq -r '.rewards_balance' <<<"$module_after")" == "$EXPECTED_EMISSION" ]]
[[ "$(jq -r '.cumulative_emitted' <<<"$cumulative_after")" == "$EXPECTED_EMISSION" ]]
MIN_HEIGHT="$EPOCH_LENGTH" "$ROOT/scripts/localnet/agree.sh"

# Real network claim: signer operator1 is different from slot1 payout operator0.
claim_hash="$(submit_claim)"
wait_all_height $((EPOCH_LENGTH + 2))
slot_claimed="$(rq slot-rewards 1 --limit 10)"
claimable_after="$(rq claimable 1 1 1)"
module_claimed="$(rq module-balances)"
cumulative_claimed="$(rq cumulative-emitted)"
[[ "$(jq -r '.rewards[0].claimed' <<<"$slot_claimed")" == "true" ]]
[[ "$(jq -r '.total_amount' <<<"$claimable_after")" == "0" ]]
[[ "$(jq -r '.rewards_balance' <<<"$module_claimed")" == "$EXPECTED_MODULE_AFTER_CLAIM" ]]
[[ "$(jq -r '.cumulative_emitted' <<<"$cumulative_claimed")" == "$EXPECTED_EMISSION" ]]
submit_double_claim_expect_failure
MIN_HEIGHT=$((EPOCH_LENGTH + 2)) "$ROOT/scripts/localnet/agree.sh"

# Export exact bank state after claim. Claim moves balances but never changes supply.
"$ROOT/scripts/localnet/stop.sh"
export_file="$NET/rewards-export.json"
"$BIN" export --home "$NET/node0" --output-document "$export_file" >/dev/null
payout_balance="$(balance_from_export "$operator0" "$export_file")"
signer_balance="$(balance_from_export "$operator1" "$export_file")"
supply="$(jq -r '[.app_state.bank.supply[]? | select(.denom == "utwlt") | .amount] | first // "0"' "$export_file")"
[[ "$payout_balance" == "$EXPECTED_PAYOUT_AFTER_CLAIM" ]]
[[ "$signer_balance" == "$GENESIS_FUNDED_BALANCE" ]]
[[ "$supply" == "$EXPECTED_SUPPLY_AFTER_FINALIZE" ]]
[[ "$(jq -r '.app_state.rewards.state.cumulative_emitted' "$export_file")" == "$EXPECTED_EMISSION" ]]
[[ "$(jq -r '.app_state.rewards.finalized_epochs[0].epoch_number' "$export_file")" == "1" ]]
[[ "$(jq -r '.app_state.rewards.claim_records[] | select(.slot_id == "1" and .epoch_number == "1") | .claimed' "$export_file")" == "true" ]]

echo "rewards localnet smoke: PASS"
echo "  epoch_length=$EPOCH_LENGTH minted=$EXPECTED_EMISSION per_slot=$EXPECTED_PER_SLOT"
echo "  claim_tx=$claim_hash signer=$operator1 payout=$operator0"
echo "  post_claim_module_balance=$EXPECTED_MODULE_AFTER_CLAIM total_supply=$EXPECTED_SUPPLY_AFTER_FINALIZE"
