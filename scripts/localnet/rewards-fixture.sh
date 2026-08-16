#!/usr/bin/env bash
set -euo pipefail

# Explorer rewards fixture (Phase 7.2).
#
# Derived from rewards-smoke.sh, but targeted at producing a PERSISTENT localnet the
# twilight-core-explorer indexer can ingest — so the explorer's rewards/claims/supply
# endpoints have REAL data (today they return 200 []). Differences from the smoke:
#   1. REST (1317) is ENABLED on node0 (the indexer needs it for sampled supply/balances;
#      the smoke only exposed RPC + gRPC).
#   2. NO teardown — the chain is LEFT RUNNING (RPC 26657 + REST 1317) so the indexer can
#      ingest. Stop it later with: TWILIGHT_LOCALNET_HOME=<NET> scripts/localnet/stop.sh
#   3. Drives several finalized epochs + two real claims (one single-epoch, one multi-epoch
#      range) so the rewards pages have multiple rows.
#
# Genesis (from init.sh): each node i has a CoreSlot with operator==payout==operatorI and
# moniker "node$i"; only operator0 (authority) and operator1 (emergency) are funded, so the
# claim signer is operator1 (signer != slot-1 payout operator0 — claims are permissionless).

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
BIN="${BIN:-$ROOT/build/twilightd}"
# FORCE the fixture's own home + chain-id. Do NOT read TWILIGHT_LOCALNET_HOME / CHAIN_ID here — if the
# caller already exported those (e.g. a sourced localnet dev env, like a `localnet >` prompt), inheriting
# them makes this run against the DEFAULT localnet (twilight-localnet-1 / /tmp/twilight-localnet) with the
# default 17280-block epoch that never finalizes here. Override with the fixture-specific names if needed.
NET="${REWARDS_FIXTURE_HOME:-/tmp/twilight-rewards-fixture}"
CHAIN_ID="${REWARDS_FIXTURE_CHAIN_ID:-twilight-rewards-fixture-1}"
# The epoch length must sit inside the ratified immutable interval
# [360, 720]; genesis refuses anything outside it. These localnets therefore run
# a fast block time instead of a short epoch — block time is node-local
# configuration and is not a protocol value.
EPOCH_LENGTH="${REWARDS_EPOCH_LENGTH:-360}"
EPOCHS_TO_RUN="${EPOCHS_TO_RUN:-3}"     # let this many epochs finalize before leaving it running
CLAIM_SLOT="${CLAIM_SLOT:-1}"
CLAIM_SIGNER="${CLAIM_SIGNER:-operator1}"
CLAIM_SIGNER_HOME="${CLAIM_SIGNER_HOME:-node1}"
SUBSIDY=416190
KEYRING=(--keyring-backend test)

# init.sh + start.sh read these standard names — overwrite whatever the caller had, with our values.
export BIN
export TWILIGHT_LOCALNET_HOME="$NET"
export CHAIN_ID

need() { command -v "$1" >/dev/null 2>&1 || { echo "missing required command: $1" >&2; exit 2; }; }
need curl
need jq

# Preflight: RPC 26657 must be FREE (the fixture and any running localnet share ports). Refuse to
# clobber a running chain — stop it first.
if curl -fsS --max-time 2 http://127.0.0.1:26657/status >/dev/null 2>&1; then
  echo "ERROR: a node is already serving RPC 26657. Stop every running localnet first, then re-run:" >&2
  echo "  TWILIGHT_LOCALNET_HOME=/tmp/twilight-localnet         $ROOT/scripts/localnet/stop.sh" >&2
  echo "  TWILIGHT_LOCALNET_HOME=$NET $ROOT/scripts/localnet/stop.sh" >&2
  echo "  pkill -f 'twilightd start'   # last resort for orphaned nodes" >&2
  exit 1
fi

rpc_url() { echo "tcp://127.0.0.1:$((26657 + $1 * 100))"; }
http_url() { echo "http://127.0.0.1:$((26657 + $1 * 100))"; }
rq() { "$BIN" rewards-query "$@" --node "$(rpc_url 0)" --output json 2>/dev/null; }

latest_height() {
  curl -fsS "$(http_url "$1")/status" 2>/dev/null | jq -r '.result.sync_info.latest_block_height | tonumber' 2>/dev/null || echo 0
}
wait_all_height() {
  local target="$1" deadline=$((SECONDS + 120)) node
  while ((SECONDS < deadline)); do
    local ready=1
    for node in 0 1 2 3; do (($(latest_height "$node") < target)) && { ready=0; break; }; done
    ((ready == 1)) && return 0
    sleep 1
  done
  echo "timed out waiting for all nodes to reach height $target" >&2; return 1
}
wait_current_epoch() {
  local target="$1" deadline=$((SECONDS + 180)) current
  while ((SECONDS < deadline)); do
    current="$(rq epoch-info 2>/dev/null | jq -r '.state.current_epoch | tonumber' 2>/dev/null || echo 0)"
    ((current >= target)) && return 0
    sleep 1
  done
  echo "timed out waiting for rewards epoch $target" >&2; return 1
}
wait_tx_code() {
  local hash="$1" deadline=$((SECONDS + 60)) result
  while ((SECONDS < deadline)); do
    result="$(curl -fsS "$(http_url 0)/tx?hash=0x$hash" 2>/dev/null || true)"
    if [[ -n "$result" ]] && jq -e '.result.tx_result' >/dev/null 2>&1 <<<"$result"; then
      jq -r '.result.tx_result.code // 0' <<<"$result"; return 0
    fi
    sleep 1
  done
  echo "not_included"
}
# claim <slotId> <startEpoch> <endEpoch> -> prints the tx hash on success
claim() {
  local slot="$1" start="$2" end="$3" out hash code
  out="$("$BIN" rewards claim "$slot" "$start" "$end" \
    --from "$CLAIM_SIGNER" "${KEYRING[@]}" --home "$NET/$CLAIM_SIGNER_HOME" \
    --chain-id "$CHAIN_ID" --node "$(rpc_url 0)" \
    --gas 600000 --fees 0utwlt --broadcast-mode sync --output json -y 2>/dev/null || true)"
  hash="$(jq -r '.txhash // ""' <<<"$out")"
  [[ -n "$hash" ]] || { echo "claim broadcast failed (slot $slot epochs $start-$end): $out" >&2; return 1; }
  code="$(wait_tx_code "$hash")"
  [[ "$code" == "0" ]] || { echo "claim DeliverTx failed: hash=$hash code=$code" >&2; return 1; }
  echo "$hash"
}

# --- 1. init the four-node localnet (builds twilightd) ---
"$ROOT/scripts/localnet/init.sh"

# --- 2. short rewards epoch in every node's genesis (test-only profile) ---
for node in 0 1 2 3; do
  genesis="$NET/node$node/config/genesis.json"; tmp="$genesis.tmp"
  jq --arg epoch "$EPOCH_LENGTH" '
    .app_state.rewards.params.epoch_length_blocks = $epoch
    | .app_state.rewards.current_epoch_config.epoch_length_blocks = $epoch
    # EpochConfigVersion is the sole epoch-geometry authority; the two lines above
    # are deprecated mirrors that fresh genesis requires to agree with it.
    | .app_state.rewards.epoch_config_versions[0].epoch_length_blocks = $epoch
  ' "$genesis" >"$tmp" && mv "$tmp" "$genesis"
done

# --- 3. enable REST (+ swagger) on node0 ONLY (indexer reads node0; ports 26657/1317) ---
api_toml="$NET/node0/config/app.toml"
sed -i.bak -E '/^\[api\]/,/^\[/ s/^enable = false/enable = true/' "$api_toml"
sed -i.bak -E '/^\[api\]/,/^\[/ s/^swagger = false/swagger = true/' "$api_toml"
rm -f "$api_toml.bak"

# --- 4. start the chain and LEAVE IT RUNNING (no teardown trap) ---
"$ROOT/scripts/localnet/start.sh"

# --- 5. drive finalization + claims ---
wait_all_height 2
[[ "$(rq epoch-info | jq -r '.state.current_epoch')" == "1" ]] || { echo "epoch 1 not open" >&2; exit 1; }

# epoch 1 finalizes after EPOCH_LENGTH blocks -> current_epoch advances to 2
wait_current_epoch 2
claim1="$(claim "$CLAIM_SLOT" 1 1)"
echo "claimed slot $CLAIM_SLOT epoch 1 -> $claim1"

# let more epochs finalize, then claim the 2..EPOCHS_TO_RUN range (a multi-epoch claim)
wait_current_epoch $((EPOCHS_TO_RUN + 1))
claim2="$(claim "$CLAIM_SLOT" 2 "$EPOCHS_TO_RUN")"
echo "claimed slot $CLAIM_SLOT epochs 2-$EPOCHS_TO_RUN -> $claim2"

final_height="$(latest_height 0)"
emission=$((EPOCH_LENGTH * SUBSIDY))

cat <<EOF

================ rewards fixture READY (chain left running) ================
chain-id : $CHAIN_ID
home     : $NET
RPC      : http://127.0.0.1:26657      (COMET_RPC_URL)
REST     : http://127.0.0.1:1317       (REST_URL)
height   : ~$final_height   epochs finalized: $((EPOCHS_TO_RUN))   emission/epoch: $emission utwlt
claims   : slot $CLAIM_SLOT epoch 1 = $claim1
           slot $CLAIM_SLOT epochs 2-$EPOCHS_TO_RUN = $claim2

Ingest into the explorer (separate DB, path B):
  cd <twilight-core-explorer>
  export COMET_RPC_URL=http://127.0.0.1:26657 REST_URL=http://127.0.0.1:1317
  export DATABASE_URL=postgresql://twilight:twilight@localhost:5432/twilight_explorer_rewards?schema=public
  npm run db:deploy
  START_HEIGHT=1 END_HEIGHT=$final_height npm --prefix apps/indexer run start
  # projections: coreslot-semantic -> (genesis seed) -> liveness -> rewards -> rewards-snapshot -> balance-snapshot
  npm --prefix apps/indexer run project:rewards
  npm --prefix apps/indexer run project:rewards-snapshot
  npm --prefix apps/indexer run project:balance-snapshot

Stop the fixture when done:
  TWILIGHT_LOCALNET_HOME=$NET $ROOT/scripts/localnet/stop.sh
===========================================================================
EOF
