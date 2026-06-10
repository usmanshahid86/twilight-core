#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
BIN="${BIN:-$ROOT/build/nyksd}"
NET="${NYKS_LOCALNET_HOME:-/tmp/nyks-localnet}"
mkdir -p "$NET/logs"

for i in 0 1 2 3; do
  # --log_no_color keeps node logs ANSI-free so the drills can reliably scrape
  # CometBFT's per-block num_val_updates for the provenance guard.
  "$BIN" start --home "$NET/node$i" --minimum-gas-prices 0unyks --log_no_color >"$NET/logs/node$i.log" 2>&1 &
  echo "$!" >"$NET/node$i.pid"
done
echo "Started localnet; logs are in $NET/logs"
