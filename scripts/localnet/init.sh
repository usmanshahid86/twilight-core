#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
BIN="${BIN:-$ROOT/build/twilightd}"
NET="${TWILIGHT_LOCALNET_HOME:-/tmp/twilight-localnet}"
CHAIN_ID="${CHAIN_ID:-twilight-localnet-1}"

mkdir -p "$ROOT/build"
GOCACHE="${GOCACHE:-/tmp/twilight-go-build}" go build -o "$BIN" "$ROOT/cmd/twilightd"
rm -rf "$NET"
mkdir -p "$NET"

for i in 0 1 2 3; do
  home="$NET/node$i"
  "$BIN" init "node$i" --chain-id "$CHAIN_ID" --home "$home" >/dev/null
  "$BIN" keys add "operator$i" --keyring-backend test --home "$home" --output json >"$NET/operator$i.json"
done

genesis_home="$NET/node0"
authority="$(sed -n 's/.*"address":"\([^"]*\)".*/\1/p' "$NET/operator0.json")"
emergency="$(sed -n 's/.*"address":"\([^"]*\)".*/\1/p' "$NET/operator1.json")"
"$BIN" coreslot-genesis set-authorities "$authority" "$emergency" --home "$genesis_home"
"$BIN" add-genesis-account "$authority" 1000000000000utwlt --home "$genesis_home"
# Fund the emergency authority too so MsgSuspendCoreSlot (signed by the emergency
# key) has an on-chain account to sign with. Fees are zero (min-gas 0utwlt); the
# balance only needs to exist for account-number/sequence resolution.
"$BIN" add-genesis-account "$emergency" 1000000000000utwlt --home "$genesis_home"

for i in 0 1 2 3; do
  operator="$(sed -n 's/.*"address":"\([^"]*\)".*/\1/p' "$NET/operator$i.json")"
  pubkey="$(sed -n 's/.*"value": "\([^"]*\)".*/\1/p' "$NET/node$i/config/priv_validator_key.json" | head -n 1)"
  # operator = payout = settlement for the localnet; the settlement address is
  # mandatory protocol state and has no default.
  "$BIN" coreslot-genesis add "$operator" "$operator" "$operator" "$pubkey" "node$i" --home "$genesis_home"
done
"$BIN" coreslot-genesis validate --home "$genesis_home"

peer_ids=()
for i in 0 1 2 3; do
  id="$("$BIN" tendermint show-node-id --home "$NET/node$i")"
  port=$((26656 + i * 100))
  peer_ids+=("${id}@127.0.0.1:${port}")
done

for i in 0 1 2 3; do
  home="$NET/node$i"
  if [[ "$i" -ne 0 ]]; then
    cp "$genesis_home/config/genesis.json" "$home/config/genesis.json"
  fi
  rpc=$((26657 + i * 100))
  p2p=$((26656 + i * 100))
  grpc=$((9090 + i * 100))
  peers=""
  for j in 0 1 2 3; do
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
    "$home/config/config.toml"
  sed -i.bak -e "s#address = \"localhost:9090\"#address = \"localhost:${grpc}\"#" "$home/config/app.toml"
  rm -f "$home/config/"*.bak
done

echo "Initialized four-node localnet at $NET"
