#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
BIN="${BIN:-$ROOT/build/twilightd}"
NET="${TWILIGHT_LOCALNET_HOME:-/tmp/twilight-localnet}"
NODE_COUNT="${NODE_COUNT:-4}"
mkdir -p "$NET/logs"

for i in $(seq 0 $((NODE_COUNT - 1))); do
  # --log_no_color keeps node logs ANSI-free so the drills can reliably scrape
  # CometBFT's per-block num_val_updates for the provenance guard.
  "$BIN" start --home "$NET/node$i" --minimum-gas-prices 0utwlt --log_no_color >"$NET/logs/node$i.log" 2>&1 &
  echo "$!" >"$NET/node$i.pid"
done
echo "Started localnet; logs are in $NET/logs"
