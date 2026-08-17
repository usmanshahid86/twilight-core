#!/usr/bin/env bash
set -euo pipefail

# Rewards epoch/entitlement localnet proof.
#
# This creates an isolated four-node localnet, proves epoch finalization, the
# canonical entitlement state it produces, query behavior, cross-node hash
# agreement, and exports state for exact supply assertions. Production defaults
# and the normal localnet smoke are unchanged.
#
# THIS IS NOT A MONEY-MOVEMENT PROOF, and it deliberately no longer claims to be.
#
# V2 finalization creates SlotEntitlements rather than claim records, and the
# only way value leaves rewards escrow is the constrained keeper API, which has
# no transaction, no CLI, and no public surface by design. There is therefore no
# public payout a localnet can submit at this stage. The definitive public
# money-moving proof arrives with Settlement, which calls that API.
#
# Repairing this script by injecting legacy claim records into genesis is
# explicitly forbidden: it would manufacture a payable obligation the chain no
# longer creates, and present the resulting transfer as evidence about a path
# production does not use.

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
BIN="${BIN:-$ROOT/build/twilightd}"
NET="${TWILIGHT_LOCALNET_HOME:-/tmp/twilight-rewards-localnet}"
CHAIN_ID="${CHAIN_ID:-twilight-rewards-localnet-1}"
NODE_COUNT=4
# The epoch length must sit inside the ratified immutable interval
# [360, 720]; genesis refuses anything outside it. These localnets therefore run
# a fast block time instead of a short epoch — block time is node-local
# configuration and is not a protocol value.
EPOCH_LENGTH="${REWARDS_EPOCH_LENGTH:-360}"
SUBSIDY=416190
EXPECTED_EMISSION=$((EPOCH_LENGTH * SUBSIDY))
EXPECTED_PER_SLOT=$((EXPECTED_EMISSION / NODE_COUNT))
# Escrow retains the whole emission: entitlements are created, and nothing
# releases them at this stage.
EXPECTED_MODULE_AFTER_FINALIZE=$EXPECTED_EMISSION
GENESIS_FUNDED_BALANCE=1000000000000
EXPECTED_SUPPLY_AFTER_FINALIZE=$((2 * GENESIS_FUNDED_BALANCE + EXPECTED_EMISSION))
KEYRING=(--keyring-backend test)

# Wait budgets scale with the epoch length. The ratified minimum epoch is 360
# blocks, so an epoch is minutes of chain time even at the fast localnet block
# rate; a fixed 90-second budget was sized for the retired 10-block epoch.
EPOCH_WAIT_SECONDS=$(( 120 + EPOCH_LENGTH ))

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
  local target="$1" deadline=$((SECONDS + EPOCH_WAIT_SECONDS)) node
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
  local target="$1" deadline=$((SECONDS + EPOCH_WAIT_SECONDS)) current
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

# After finalization: exact emission, aggregate, entitlements, queries, agreement.
wait_current_epoch 2
epoch_after="$(rq epoch-info)"
finalized="$(rq epoch-reward 1)"
entitlement="$(rq entitlement 1 1)"
epoch_entitlements="$(rq epoch-entitlements 1 --limit 10)"
module_after="$(rq module-balances)"
cumulative_after="$(rq cumulative-emitted)"
rq params >/dev/null
rq supply-schedule >/dev/null
rq current-active-blocks --limit 10 >/dev/null
rq reward-config-versions --limit 10 >/dev/null

[[ "$(jq -r '.state.current_epoch' <<<"$epoch_after")" == "2" ]]
[[ "$(jq -r '.epoch_reward.minted_emission' <<<"$finalized")" == "$EXPECTED_EMISSION" ]]
[[ "$(jq -r '.epoch_reward.allocated_amount' <<<"$finalized")" == "$EXPECTED_EMISSION" ]]
[[ "$(jq -r '.epoch_reward.carry_out' <<<"$finalized")" == "0" ]]
# The canonical obligation, and nothing released against it.
[[ "$(jq -r '.entitlement.entitlement_amount' <<<"$entitlement")" == "$EXPECTED_PER_SLOT" ]]
[[ "$(jq -r '.entitlement.released_amount' <<<"$entitlement")" == "0" ]]
[[ "$(jq -r '.entitlement.reward_config_version' <<<"$entitlement")" == "1" ]]
# One per node, ascending by slot id.
[[ "$(jq -r '(.entitlements // []) | length' <<<"$epoch_entitlements")" == "$NODE_COUNT" ]]
[[ "$(jq -r '[.entitlements[].slot_id] | map(tonumber)' -c <<<"$epoch_entitlements")" == "[1,2,3,4]" ]]
# Escrow holds the whole emission and the liability accounts for all of it.
[[ "$(jq -r '.rewards_balance' <<<"$module_after")" == "$EXPECTED_MODULE_AFTER_FINALIZE" ]]
[[ "$(jq -r '.outstanding_entitlement_liability' <<<"$module_after")" == "$EXPECTED_EMISSION" ]]
[[ "$(jq -r '.carry_forward_remainder' <<<"$module_after")" == "0" ]]
[[ "$(jq -r '.cumulative_emitted' <<<"$cumulative_after")" == "$EXPECTED_EMISSION" ]]
MIN_HEIGHT="$EPOCH_LENGTH" "$ROOT/scripts/localnet/agree.sh"

# Export exact bank state. Nothing has been released, so operator balances are
# untouched and supply equals the emission.
"$ROOT/scripts/localnet/stop.sh"
export_file="$NET/rewards-export.json"
"$BIN" export --home "$NET/node0" --output-document "$export_file" >/dev/null
payout_balance="$(balance_from_export "$operator0" "$export_file")"
signer_balance="$(balance_from_export "$operator1" "$export_file")"
supply="$(jq -r '[.app_state.bank.supply[]? | select(.denom == "utwlt") | .amount] | first // "0"' "$export_file")"
[[ "$payout_balance" == "$GENESIS_FUNDED_BALANCE" ]]
[[ "$signer_balance" == "$GENESIS_FUNDED_BALANCE" ]]
[[ "$supply" == "$EXPECTED_SUPPLY_AFTER_FINALIZE" ]]
[[ "$(jq -r '.app_state.rewards.state.cumulative_emitted' "$export_file")" == "$EXPECTED_EMISSION" ]]
[[ "$(jq -r '.app_state.rewards.finalized_epochs[0].epoch_number' "$export_file")" == "1" ]]
# The switchover, visible in exported state: an entitlement exists and no claim
# record was created for the epoch that produced it.
[[ "$(jq -r '[.app_state.rewards.slot_entitlements[]? | select(.epoch == "1")] | length' "$export_file")" == "$NODE_COUNT" ]]
[[ "$(jq -r '[.app_state.rewards.claim_records[]? | select(.epoch_number == "1")] | length' "$export_file")" == "0" ]]

echo "rewards localnet epoch/entitlement smoke: PASS"
echo "  epoch_length=$EPOCH_LENGTH minted=$EXPECTED_EMISSION per_slot=$EXPECTED_PER_SLOT"
echo "  entitlements=$NODE_COUNT escrow=$EXPECTED_MODULE_AFTER_FINALIZE total_supply=$EXPECTED_SUPPLY_AFTER_FINALIZE"
echo "  NOTE: no value was released. Release is keeper-only until Settlement."
