#!/usr/bin/env bash
set -euo pipefail

NET="${NYKS_LOCALNET_HOME:-/tmp/nyks-localnet}"
for i in 0 1 2 3; do
  if [[ -f "$NET/node$i.pid" ]]; then
    kill "$(cat "$NET/node$i.pid")" 2>/dev/null || true
    rm -f "$NET/node$i.pid"
  fi
done
