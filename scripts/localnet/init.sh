#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
BIN="${BIN:-$ROOT/build/twilightd}"
NET="${TWILIGHT_LOCALNET_HOME:-/tmp/twilight-localnet}"
CHAIN_ID="${CHAIN_ID:-twilight-localnet-1}"
# Node count is a knob so a study can build a set of a different size; every
# existing caller leaves it alone and gets the same four-node localnet as before.
# Ports are derived from the index, so sets stay non-overlapping only ONE AT A
# TIME — these scripts have always assumed a single localnet per machine.
NODE_COUNT="${NODE_COUNT:-4}"

mkdir -p "$ROOT/build"
GOCACHE="${GOCACHE:-/tmp/twilight-go-build}" go build -o "$BIN" "$ROOT/cmd/twilightd"
rm -rf "$NET"
mkdir -p "$NET"

for i in $(seq 0 $((NODE_COUNT - 1))); do
  home="$NET/node$i"
  "$BIN" init "node$i" --chain-id "$CHAIN_ID" --home "$home" >/dev/null
  "$BIN" keys add "operator$i" --keyring-backend test --home "$home" --output json >"$NET/operator$i.json"
done

genesis_home="$NET/node0"
authority="$(sed -n 's/.*"address":"\([^"]*\)".*/\1/p' "$NET/operator0.json")"
# A single-node set has no operator1 to be the emergency authority. Reusing
# operator0 keeps the roles distinct in the protocol while the key is shared,
# which is the only option when one operator is all there is.
if [[ -f "$NET/operator1.json" ]]; then
  emergency="$(sed -n 's/.*"address":"\([^"]*\)".*/\1/p' "$NET/operator1.json")"
else
  emergency="$authority"
fi
"$BIN" coreslot-genesis set-authorities "$authority" "$emergency" --home "$genesis_home"
"$BIN" add-genesis-account "$authority" 1000000000000utwlt --home "$genesis_home"
# Fund the emergency authority too so MsgSuspendCoreSlot (signed by the emergency
# key) has an on-chain account to sign with. Fees are zero (min-gas 0utwlt); the
# balance only needs to exist for account-number/sequence resolution.
if [[ "$emergency" != "$authority" ]]; then
  "$BIN" add-genesis-account "$emergency" 1000000000000utwlt --home "$genesis_home"
fi

for i in $(seq 0 $((NODE_COUNT - 1))); do
  operator="$(sed -n 's/.*"address":"\([^"]*\)".*/\1/p' "$NET/operator$i.json")"
  pubkey="$(sed -n 's/.*"value": "\([^"]*\)".*/\1/p' "$NET/node$i/config/priv_validator_key.json" | head -n 1)"
  # operator = payout = settlement for the localnet; the settlement address is
  # mandatory protocol state and has no default.
  "$BIN" coreslot-genesis add "$operator" "$operator" "$operator" "$pubkey" "node$i" --home "$genesis_home"
done
"$BIN" coreslot-genesis validate --home "$genesis_home"

peer_ids=()
for i in $(seq 0 $((NODE_COUNT - 1))); do
  id="$("$BIN" tendermint show-node-id --home "$NET/node$i")"
  port=$((26656 + i * 100))
  peer_ids+=("${id}@127.0.0.1:${port}")
done

for i in $(seq 0 $((NODE_COUNT - 1))); do
  home="$NET/node$i"
  if [[ "$i" -ne 0 ]]; then
    cp "$genesis_home/config/genesis.json" "$home/config/genesis.json"
  fi
  rpc=$((26657 + i * 100))
  p2p=$((26656 + i * 100))
  grpc=$((9090 + i * 100))
  peers=""
  for j in $(seq 0 $((NODE_COUNT - 1))); do
    if [[ "$j" -ne "$i" ]]; then
      peers="${peers}${peers:+,}${peer_ids[$j]}"
    fi
  done
  sed -i.bak \
    -e "s#laddr = \"tcp://127.0.0.1:26657\"#laddr = \"tcp://127.0.0.1:${rpc}\"#" \
    -e "s#laddr = \"tcp://0.0.0.0:26656\"#laddr = \"tcp://0.0.0.0:${p2p}\"#" \
    -e "s#persistent_peers = \"\"#persistent_peers = \"${peers}\"#" \
    -e "s#pex = true#pex = false#" \
    -e "s#allow_duplicate_ip = false#allow_duplicate_ip = true#" \
    -e "s#^timeout_commit = .*#timeout_commit = \"${TWILIGHT_LOCALNET_TIMEOUT_COMMIT:-200ms}\"#" \
    "$home/config/config.toml"
  sed -i.bak -e "s#address = \"localhost:9090\"#address = \"localhost:${grpc}\"#" "$home/config/app.toml"
  rm -f "$home/config/"*.bak
done

echo "Initialized ${NODE_COUNT}-node localnet at $NET"
