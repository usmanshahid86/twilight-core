#!/usr/bin/env bash
set -euo pipefail

# Generate a fresh CometBFT ed25519 consensus key for CoreSlot use.
#
# There is no bespoke key tool; CometBFT writes a brand-new priv_validator_key.json
# whenever a node home is initialized. We `twilightd init` into a throwaway home and
# harvest ONLY its priv_validator_key.json (the genesis/config it also writes are
# discarded). This is the same approach used to add or rotate slot keys safely —
# we never hand-edit private key material.
#
# Usage:
#   gen-consensus-key.sh <name>
# Output (two tab-separated fields on stdout):
#   <base64-ed25519-pubkey>\t<path-to-priv_validator_key.json>
#
# The base64 pubkey is the value `twilightd coreslot register/rotate-key` expects.
# The priv_validator_key.json path is what you copy into a node's config/ dir to
# make that node sign with the new key (used by the restart-after-rotation drill).

NAME="${1:?usage: gen-consensus-key.sh <name>}"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
BIN="${BIN:-$ROOT/build/twilightd}"
NET="${TWILIGHT_LOCALNET_HOME:-/tmp/twilight-localnet}"
CHAIN_ID="${CHAIN_ID:-twilight-localnet-1}"

OUT_DIR="$NET/keys/$NAME"
rm -rf "$OUT_DIR"
mkdir -p "$OUT_DIR"

"$BIN" init "$NAME" --chain-id "$CHAIN_ID" --home "$OUT_DIR" >/dev/null 2>&1

KEY_FILE="$OUT_DIR/config/priv_validator_key.json"
PUBKEY_B64="$(sed -n 's/.*"value": "\([^"]*\)".*/\1/p' "$KEY_FILE" | head -n 1)"

printf '%s\t%s\n' "$PUBKEY_B64" "$KEY_FILE"
