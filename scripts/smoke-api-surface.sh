#!/usr/bin/env bash
# Smoke test for the Twilight custom-module API surface (REST gRPC-gateway + gRPC).
#
# Verifies that x/rewards and x/coreslot query routes are SERVED over REST (1317) —
# i.e. they do NOT return 501 Not Implemented (which is what an unwired gateway
# returns). A custom route is considered wired if it returns anything other than 501
# or a connection failure; 200 is ideal, 400/404 are acceptable for missing
# params/data. Generic node/bank routes must return 200. Standard cosmos modules
# Twilight intentionally omits (staking/gov/mint/distribution) are reported but never
# fail the run.
#
# Usage:
#   ./scripts/smoke-api-surface.sh
#   BASE_REST=http://16.192.99.123:1317 BASE_GRPC=16.192.99.123:9090 ./scripts/smoke-api-surface.sh
set -uo pipefail

BASE_REST="${BASE_REST:-http://localhost:1317}"
BASE_GRPC="${BASE_GRPC:-localhost:9090}"

pass=0; fail=0
code() { curl -s -o /dev/null -w '%{http_code}' --max-time 8 "$1" 2>/dev/null || echo 000; }

# A custom-module route is wired unless it returns 501 (unregistered) or 000 (unreachable).
check_custom() {
  local path="$1" c; c="$(code "$BASE_REST$path")"
  if [[ "$c" == "501" || "$c" == "000" ]]; then
    printf '  FAIL  %-52s -> %s\n' "$path" "$c"; fail=$((fail+1))
  else
    printf '  ok    %-52s -> %s\n' "$path" "$c"; pass=$((pass+1))
  fi
}

# Generic routes must be 200.
check_generic() {
  local path="$1" c; c="$(code "$BASE_REST$path")"
  if [[ "$c" == "200" ]]; then
    printf '  ok    %-52s -> %s\n' "$path" "$c"; pass=$((pass+1))
  else
    printf '  FAIL  %-52s -> %s\n' "$path" "$c"; fail=$((fail+1))
  fi
}

# Informational only — never fails the run.
check_absent() {
  local path="$1" c; c="$(code "$BASE_REST$path")"
  printf '  info  %-52s -> %s (expected 501; intentionally absent)\n' "$path" "$c"
}

echo "== Twilight API surface smoke =="
echo "REST=$BASE_REST  GRPC=$BASE_GRPC"
echo
echo "-- x/rewards REST --"
check_custom /twilight/rewards/v1/params
check_custom /twilight/rewards/v1/epoch-info
check_custom /twilight/rewards/v1/next-halving
check_custom /twilight/rewards/v1/cumulative-emitted
check_custom /twilight/rewards/v1/supply-schedule
check_custom /twilight/rewards/v1/module-balances
check_custom /twilight/rewards/v1/current-epoch/active-blocks
check_custom /twilight/rewards/v1/epochs/1
check_custom /twilight/rewards/v1/slots/1/rewards
check_custom "/twilight/rewards/v1/slots/1/claimable?start_epoch=1&end_epoch=1"

echo
echo "-- x/coreslot REST --"
check_custom /twilight/coreslot/v1/params
check_custom /twilight/coreslot/v1/slots
check_custom /twilight/coreslot/v1/active-slots
check_custom /twilight/coreslot/v1/slots/1
check_custom /twilight/coreslot/v1/slots/1/reward-weight
check_custom /twilight/coreslot/v1/pending-key-rotations
check_custom /twilight/coreslot/v1/last-applied-validators

echo
echo "-- generic chain REST (must be 200) --"
check_generic /cosmos/base/tendermint/v1beta1/node_info
check_generic /cosmos/bank/v1beta1/supply

echo
echo "-- standard modules Twilight intentionally omits (never fails) --"
check_absent /cosmos/staking/v1beta1/pool
check_absent /cosmos/mint/v1beta1/inflation
check_absent /cosmos/gov/v1/proposals
check_absent /cosmos/distribution/v1beta1/params

echo
echo "-- gRPC reflection (optional; needs grpcurl) --"
if command -v grpcurl >/dev/null 2>&1; then
  if grpcurl -plaintext "$BASE_GRPC" list 2>/dev/null | grep -q 'twilight.rewards.v1.Query'; then
    echo "  ok    twilight.rewards.v1.Query listed"; pass=$((pass+1))
  else
    echo "  FAIL  twilight.rewards.v1.Query not listed"; fail=$((fail+1))
  fi
  if grpcurl -plaintext "$BASE_GRPC" list 2>/dev/null | grep -q 'twilight.coreslot.v1.Query'; then
    echo "  ok    twilight.coreslot.v1.Query listed"; pass=$((pass+1))
  else
    echo "  FAIL  twilight.coreslot.v1.Query not listed"; fail=$((fail+1))
  fi
else
  echo "  skip  grpcurl not installed"
fi

echo
echo "== result: pass=$pass fail=$fail =="
[[ "$fail" -eq 0 ]] && { echo "PASS"; exit 0; } || { echo "FAIL"; exit 1; }
