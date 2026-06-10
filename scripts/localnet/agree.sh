#!/usr/bin/env bash
set -euo pipefail

# Cross-node hash-agreement check for the twilight localnet.
#
# Ported and adapted from the Twilight experiment harness
# (twilight-core-slot-experiments scripts/devnet.sh::cmd_status and
# ::latest_provenance_value). It replaces the old node0-only height poll in
# smoke.sh: instead it queries every node and proves they agree on the app hash,
# validators hash, and next-validators hash at a common height.
#
# Why these three hashes are compared:
#   - app_hash:             proves identical application state. The catastrophic
#                           failure class is two nodes committing the same height
#                           with different app hashes (a silent fork of state).
#   - validators_hash:      the active validator set in effect AT height H.
#   - next_validators_hash: the validator set that takes effect at H+1.
#
# One-block validator-hash lag: a Core-Slot validator-set change applied in
# EndBlock at height H is reflected in next_validators_hash at H and in
# validators_hash at H+1. Comparing the SAME height H across nodes is lag-safe —
# every correctly configured node computes the identical value for that H, so a
# mismatch is a real divergence, not the lag.
#
# Staking is omitted from twilight-core, so there is no staking_updates_count to
# scrape (it is structurally zero). We optionally show CometBFT's latest
# num_val_updates from each node's log as non-fatal information.

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
NET="${TWILIGHT_LOCALNET_HOME:-/tmp/twilight-localnet}"
NODE_COUNT="${NODE_COUNT:-4}"
MIN_HEIGHT="${MIN_HEIGHT:-3}"
DRILL="${DRILL:-localnet}"
RUN_ID="${RUN_ID:-adhoc}"
AGREE_ACTION="${AGREE_ACTION:-checkpoint}"
AGREE_EVIDENCE_FILE="${AGREE_EVIDENCE_FILE:-}"
# AGREE_NODES (optional): space-separated node indices to check, e.g. "0 2".
# When set, agreement is asserted only over that subset of live nodes — used by
# the quorum drill to verify the surviving majority still agrees. When unset, all
# nodes 0..NODE_COUNT-1 are checked (the default smoke behavior).
NODES=()
if [[ -n "${AGREE_NODES:-}" ]]; then
  read -r -a NODES <<<"$AGREE_NODES"
else
  for ((i = 0; i < NODE_COUNT; i++)); do NODES+=("$i"); done
fi

need() { command -v "$1" >/dev/null 2>&1 || { echo "missing required command: $1" >&2; exit 2; }; }
need curl
need jq

# twilight localnet RPC port scheme (see scripts/localnet/init.sh): 26657 + i*100.
rpc_port() { echo $((26657 + $1 * 100)); }

rpc_get() { curl -fsS "http://127.0.0.1:$(rpc_port "$1")$2"; }

# Best-effort, non-fatal: the latest CometBFT num_val_updates from a node log.
# Informational only — x/coreslot is the sole emitter, so any nonzero value is a
# coreslot lifecycle change, never staking.
num_val_updates() {
  local log="$NET/logs/node$1.log"
  [[ -f "$log" ]] || { echo "-"; return 0; }
  local v
  v="$(sed $'s/\x1b\\[[0-9;]*m//g' "$log" 2>/dev/null | grep -oE 'num_val_updates=[0-9]+' | tail -1 | cut -d= -f2)"
  echo "${v:-0}"
}

errors=0
min_height=999999999
declare -a heights
declare -a node_status
declare -a apps
declare -a vals
declare -a nexts

for node in "${NODES[@]}"; do
  if ! status="$(rpc_get "$node" /status 2>/dev/null)"; then
    echo "node$node RPC unavailable" >&2
    errors=$((errors + 1))
    heights[$node]=0
    node_status[$node]="rpc_unavailable"
    continue
  fi
  h="$(jq -r '.result.sync_info.latest_block_height | tonumber' <<<"$status")"
  catching="$(jq -r '.result.sync_info.catching_up' <<<"$status")"
  heights[$node]="$h"
  node_status[$node]="ok"
  ((h < min_height)) && min_height="$h"
  if [[ "$catching" != "false" ]]; then
    echo "node$node still catching up" >&2
    errors=$((errors + 1))
  fi
  if ((h < MIN_HEIGHT)); then
    echo "node$node height $h below required $MIN_HEIGHT (not progressing)" >&2
    errors=$((errors + 1))
  fi
done

if ((min_height == 999999999)); then min_height=0; fi

printf '%-7s %-7s %-10s %-66s %-66s %-66s %s\n' \
  node height status app_hash validators_hash next_validators_hash num_val_updates

common_app=""
common_val=""
common_next=""
for node in "${NODES[@]}"; do
  if [[ "${node_status[$node]:-}" != "ok" ]]; then
    continue
  fi
  block="$(rpc_get "$node" "/block?height=$min_height" 2>/dev/null || true)"
  if [[ -z "$block" ]]; then
    printf '%-7s %-7s %-10s %s\n' "node$node" "${heights[$node]:-0}" "noblock" "<no block at height $min_height>"
    errors=$((errors + 1))
    node_status[$node]="block_unavailable"
    continue
  fi
  app="$(jq -r '.result.block.header.app_hash' <<<"$block")"
  val="$(jq -r '.result.block.header.validators_hash' <<<"$block")"
  next="$(jq -r '.result.block.header.next_validators_hash' <<<"$block")"
  apps[$node]="$app"
  vals[$node]="$val"
  nexts[$node]="$next"
  printf '%-7s %-7s %-10s %-66s %-66s %-66s %s\n' \
    "node$node" "${heights[$node]}" "ok" "$app" "$val" "$next" "$(num_val_updates "$node")"

  if [[ -z "$common_app" ]]; then
    common_app="$app"
    common_val="$val"
    common_next="$next"
  else
    [[ "$app" == "$common_app" ]] || { echo "app-hash divergence at height $min_height (node$node)" >&2; errors=$((errors + 1)); }
    [[ "$val" == "$common_val" ]] || { echo "validators-hash divergence at height $min_height (node$node)" >&2; errors=$((errors + 1)); }
    [[ "$next" == "$common_next" ]] || { echo "next-validators-hash divergence at height $min_height (node$node)" >&2; errors=$((errors + 1)); }
  fi
done

echo "common_height=$min_height required_min_height=$MIN_HEIGHT nodes=[${NODES[*]}]"
agreement_result="PASS"
((errors == 0)) || agreement_result="FAIL"
if [[ -n "$AGREE_EVIDENCE_FILE" ]]; then
  mkdir -p "$(dirname "$AGREE_EVIDENCE_FILE")"
  for node in "${NODES[@]}"; do
    jq -cn \
      --arg drill "$DRILL" --arg run_id "$RUN_ID" --arg action "$AGREE_ACTION" \
      --arg checked_nodes "${NODES[*]}" --arg node "$node" \
      --arg common_height "$min_height" --arg latest_height "${heights[$node]:-0}" \
      --arg app_hash "${apps[$node]:-}" --arg validators_hash "${vals[$node]:-}" \
      --arg next_validators_hash "${nexts[$node]:-}" \
      --arg node_status "${node_status[$node]:-unknown}" \
      --arg agreement_result "$agreement_result" --arg timestamp "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
      '{drill:$drill,run_id:$run_id,action:$action,checked_nodes:$checked_nodes,node_index:($node|tonumber),common_comparison_height:($common_height|tonumber),node_latest_height:($latest_height|tonumber),app_hash:$app_hash,validators_hash:$validators_hash,next_validators_hash:$next_validators_hash,node_status:$node_status,agreement_result:$agreement_result,timestamp:$timestamp}' \
      >>"$AGREE_EVIDENCE_FILE"
  done
fi
if ((errors > 0)); then
  echo "localnet agreement: FAIL ($errors check failures)" >&2
  exit 1
fi
echo "localnet agreement: PASS — nodes [${NODES[*]}] agree on app/validators/next-validators hash at height $min_height"
