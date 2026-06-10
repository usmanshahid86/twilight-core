#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
MIN_HEIGHT="${MIN_HEIGHT:-3}"

"$ROOT/scripts/localnet/init.sh"
"$ROOT/scripts/localnet/start.sh"
trap '"$ROOT/scripts/localnet/stop.sh"' EXIT

# Wait until EVERY node (not just node0) has progressed past MIN_HEIGHT.
deadline=$((SECONDS + 60))
ready=0
while ((SECONDS < deadline)); do
  ok=1
  for i in 0 1 2 3; do
    port=$((26657 + i * 100))
    # Tolerate not-yet-listening nodes during startup (curl exit 7) without
    # aborting under `set -e`.
    status="$(curl -sf "http://127.0.0.1:${port}/status" 2>/dev/null || true)"
    h="$(sed -n 's/.*"latest_block_height":"\([0-9]*\)".*/\1/p' <<<"$status" | head -1)"
    if [[ -z "$h" ]] || ((h < MIN_HEIGHT)); then
      ok=0
      break
    fi
  done
  if ((ok == 1)); then
    ready=1
    break
  fi
  sleep 1
done

if ((ready == 0)); then
  echo "Localnet did not reach height $MIN_HEIGHT on all nodes" >&2
  exit 1
fi

# The real smoke assertion: cross-node app-hash / validators-hash /
# next-validators-hash agreement (exits nonzero on any divergence).
MIN_HEIGHT="$MIN_HEIGHT" "$ROOT/scripts/localnet/agree.sh"
