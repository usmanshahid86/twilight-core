#!/usr/bin/env bash
set -euo pipefail

NET="${TWILIGHT_LOCALNET_HOME:-/tmp/twilight-localnet}"
NODE_COUNT="${NODE_COUNT:-4}"
for i in $(seq 0 $((NODE_COUNT - 1))); do
  if [[ -f "$NET/node$i.pid" ]]; then
    kill "$(cat "$NET/node$i.pid")" 2>/dev/null || true
    rm -f "$NET/node$i.pid"
  fi
done
