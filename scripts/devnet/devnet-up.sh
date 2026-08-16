#!/usr/bin/env bash
set -euo pipefail

# Single-validator, joinable Twilight devnet.
#
# Brings up ONE CoreSlot validator with p2p + RPC bound publicly so a team can
# peer full nodes into it. This is NOT the soak harness (that is a localhost
# validation tool); this is a persistent, externally reachable network.
#
# Required env:
#   PUBLIC_IP   the instance's public IP or DNS (advertised to peers)
# Optional env (devnet defaults):
#   CHAIN_ID=twilight-devnet-1  HOME_DIR=~/.twilight-devnet
#   MONIKER=twilight-devnet-validator  EPOCH_LENGTH=60 (~5 min epochs at 5s blocks)
#   RPC_PORT=26657  P2P_PORT=26656  FAUCET_BALANCE=1000000000000
#
# Re-running wipes HOME_DIR and regenerates a FRESH genesis (the upgrade/reboot
# path — team re-joins). To restart without losing state, just restart the node;
# do not re-run this script.

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
BIN="${BIN:-$ROOT/build/twilightd}"
PUBLIC_IP="${PUBLIC_IP:?set PUBLIC_IP to the instance public IP/DNS}"
CHAIN_ID="${CHAIN_ID:-twilight-devnet-1}"
HOME_DIR="${HOME_DIR:-$HOME/.twilight-devnet}"
MONIKER="${MONIKER:-twilight-devnet-validator}"
EPOCH_LENGTH="${EPOCH_LENGTH:-60}"
RPC_PORT="${RPC_PORT:-26657}"
P2P_PORT="${P2P_PORT:-26656}"
FAUCET_BALANCE="${FAUCET_BALANCE:-1000000000000}"
KEYRING=(--keyring-backend test)

command -v jq >/dev/null || { echo "need jq" >&2; exit 2; }

if [[ ! -x "$BIN" ]]; then
  mkdir -p "$ROOT/build"
  GOCACHE="${GOCACHE:-$HOME/.cache/go-build}" go build -o "$BIN" "$ROOT/cmd/twilightd"
fi

rm -rf "$HOME_DIR"
"$BIN" init "$MONIKER" --chain-id "$CHAIN_ID" --home "$HOME_DIR" >/dev/null

# Validator/authority + emergency keys (test keyring; devnet only).
"$BIN" keys add validator "${KEYRING[@]}" --home "$HOME_DIR" --output json >"$HOME_DIR/validator.json"
"$BIN" keys add emergency "${KEYRING[@]}" --home "$HOME_DIR" --output json >"$HOME_DIR/emergency.json"
authority="$(jq -r '.address' "$HOME_DIR/validator.json")"
emergency="$(jq -r '.address' "$HOME_DIR/emergency.json")"

# Authorities + funded admin/faucet accounts (gasless; balance only needs to exist
# so the authority can sign management txs and seed team accounts).
"$BIN" coreslot-genesis set-authorities "$authority" "$emergency" --home "$HOME_DIR"
"$BIN" add-genesis-account "$authority" "${FAUCET_BALANCE}utwlt" --home "$HOME_DIR"
"$BIN" add-genesis-account "$emergency" "${FAUCET_BALANCE}utwlt" --home "$HOME_DIR"

# Register the single CoreSlot validator (operator = payout = settlement = authority).
pubkey="$(sed -n 's/.*"value": "\([^"]*\)".*/\1/p' "$HOME_DIR/config/priv_validator_key.json" | head -n 1)"
"$BIN" coreslot-genesis add "$authority" "$authority" "$authority" "$pubkey" "$MONIKER" --home "$HOME_DIR"
"$BIN" coreslot-genesis validate --home "$HOME_DIR"

# Devnet-friendly epoch length so rewards are visible in minutes.
g="$HOME_DIR/config/genesis.json"; tmp="$g.tmp"
jq --arg e "$EPOCH_LENGTH" '
  .app_state.rewards.params.epoch_length_blocks = $e
  | .app_state.rewards.current_epoch_config.epoch_length_blocks = $e
' "$g" >"$tmp" && mv "$tmp" "$g"

# Expose: RPC on 0.0.0.0, advertise public p2p address, CORS for web tools,
# gasless. Single seed node => no persistent_peers, pex stays on for discovery.
cfg="$HOME_DIR/config/config.toml"
sed -i.bak \
  -e "s#laddr = \"tcp://127.0.0.1:26657\"#laddr = \"tcp://0.0.0.0:${RPC_PORT}\"#" \
  -e "s#laddr = \"tcp://0.0.0.0:26656\"#laddr = \"tcp://0.0.0.0:${P2P_PORT}\"#" \
  -e "s#external_address = \"\"#external_address = \"${PUBLIC_IP}:${P2P_PORT}\"#" \
  -e "s#cors_allowed_origins = \[\]#cors_allowed_origins = [\"*\"]#" \
  -e "s#allow_duplicate_ip = false#allow_duplicate_ip = true#" \
  "$cfg"
sed -i.bak -e "s#minimum-gas-prices = \"\"#minimum-gas-prices = \"0utwlt\"#" "$HOME_DIR/config/app.toml"
rm -f "$HOME_DIR/config/"*.bak

node_id="$("$BIN" tendermint show-node-id --home "$HOME_DIR")"
peer="${node_id}@${PUBLIC_IP}:${P2P_PORT}"
cp "$g" "$HOME_DIR/devnet-genesis.json"

cat >"$HOME_DIR/JOIN.md" <<EOF
# Join the Twilight devnet ($CHAIN_ID)

- RPC:     http://${PUBLIC_IP}:${RPC_PORT}    (status: /status, genesis: /genesis)
- peer:    ${peer}
- genesis: fetch from RPC \`/genesis\` or copy devnet-genesis.json

## Run a full node
twilightd init <your-moniker> --chain-id ${CHAIN_ID}
curl -s http://${PUBLIC_IP}:${RPC_PORT}/genesis | jq '.result.genesis' > ~/.twilightd/config/genesis.json
sed -i 's#^persistent_peers =.*#persistent_peers = "${peer}"#' ~/.twilightd/config/config.toml
twilightd start --minimum-gas-prices 0utwlt
EOF

echo "=== devnet initialized ==="
echo "chain-id : $CHAIN_ID"
echo "peer     : $peer"
echo "RPC      : http://${PUBLIC_IP}:${RPC_PORT}"
echo "home     : $HOME_DIR"
echo "join doc : $HOME_DIR/JOIN.md"
echo
echo "start it (systemd recommended for a long-lived devnet; see devnet/README.md):"
echo "  $BIN start --home $HOME_DIR --minimum-gas-prices 0utwlt"
