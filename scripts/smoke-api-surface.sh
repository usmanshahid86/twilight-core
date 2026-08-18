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
#   BASE_REST=http://16.192.99.123:1317 BASE_GRPC=16.192.99.123:9090 \
#     BASE_RPC=http://16.192.99.123:26657 ./scripts/smoke-api-surface.sh
set -uo pipefail

BASE_REST="${BASE_REST:-http://localhost:1317}"
BASE_GRPC="${BASE_GRPC:-localhost:9090}"
# CometBFT RPC — used only to source a real hex consensus address for the
# CoreSlotByConsensusAddress route. Optional: that check is skipped if unreachable.
BASE_RPC="${BASE_RPC:-http://localhost:26657}"

pass=0; fail=0
# Returns the HTTP status, or 000 if unreachable. curl's -w already emits 000 on a
# connection failure, so do NOT add a `|| echo 000` (that would double it to 000000).
code() { local c; c="$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 "$1" 2>/dev/null)"; echo "${c:-000}"; }
body() { curl -s --max-time 8 "$1" 2>/dev/null; }

# Preflight: these checks require a RUNNING node with the REST API enabled.
preflight() {
  local c; c="$(code "$BASE_REST/cosmos/base/tendermint/v1beta1/node_info")"
  if [[ "$c" == "000" ]]; then
    echo "ERROR: no REST API reachable at $BASE_REST (connection failed)." >&2
    echo "These smoke tests run against a LIVE node; they do not start one." >&2
    echo "Start a localnet with REST enabled, e.g.:" >&2
    echo "  export TWILIGHT_LOCALNET_HOME=/tmp/twilight-localnet" >&2
    echo "  scripts/localnet/init.sh" >&2
    echo "  sed -i.bak '/^\\[api\\]/,/^\\[/ s/^enable = false/enable = true/' \"\$TWILIGHT_LOCALNET_HOME/node0/config/app.toml\"" >&2
    echo "  scripts/localnet/start.sh   # wait for a block, then re-run this script" >&2
    echo "Or point BASE_REST at a running node, e.g. BASE_REST=http://16.192.99.123:1317" >&2
    exit 2
  fi
}

# A parameterized route exercised with a REAL fixture value must return 200.
check_200() {
  local label="$1" url="$2" c; c="$(code "$url")"
  if [[ "$c" == "200" ]]; then
    printf '  ok    %-52s -> %s\n' "$label" "$c"; pass=$((pass+1))
  else
    printf '  FAIL  %-52s -> %s\n' "$label" "$c"; fail=$((fail+1))
  fi
}

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

# A route that was deliberately retired must no longer be served. An unregistered
# pattern reaches the gateway's routing-error handler, which answers 501; anything
# else means the handler is still wired.
check_retired() {
  local path="$1" c; c="$(code "$BASE_REST$path")"
  if [[ "$c" == "501" ]]; then
    printf '  ok    %-52s -> %s (retired; no longer served)\n' "$path" "$c"; pass=$((pass+1))
  else
    printf '  FAIL  %-52s -> %s (retired route still answering)\n' "$path" "$c"; fail=$((fail+1))
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
preflight
echo "-- x/rewards REST --"
check_custom /twilight/rewards/v1/params
check_custom /twilight/rewards/v1/epoch-info
check_custom /twilight/rewards/v1/next-halving
check_custom /twilight/rewards/v1/cumulative-emitted
check_custom /twilight/rewards/v1/supply-schedule
check_custom /twilight/rewards/v1/module-balances
check_custom /twilight/rewards/v1/current-epoch/active-blocks
check_custom /twilight/rewards/v1/epochs/1
# Retired with the legacy claim path: both were claim-record-backed.
check_retired /twilight/rewards/v1/slots/1/rewards
check_retired "/twilight/rewards/v1/slots/1/claimable?start_epoch=1&end_epoch=1"

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
echo "-- x/coreslot parameterized routes (real fixtures, expect 200) --"
# CoreSlotByOperator: operator_address sourced from slot 1 over REST.
op="$(body "$BASE_REST/twilight/coreslot/v1/slots/1" | jq -r '.slot.operator_address // empty' 2>/dev/null)"
if [[ -n "$op" ]]; then
  check_200 "/twilight/coreslot/v1/operators/{operator_address}" "$BASE_REST/twilight/coreslot/v1/operators/$op"
else
  printf '  skip  CoreSlotByOperator (no slot-1 operator_address available)\n'
fi
# CoreSlotByConsensusAddress: needs a HEX consensus address (the keeper rejects bech32
# valcons). CometBFT /validators exposes it; skip gracefully if the RPC is unreachable.
hexcons="$(body "$BASE_RPC/validators" | sed -n 's/.*"address":"\([A-F0-9]\{40\}\)".*/\1/p' | head -1)"
if [[ -n "$hexcons" ]]; then
  check_200 "/twilight/coreslot/v1/consensus/{hex_cons_address}" "$BASE_REST/twilight/coreslot/v1/consensus/$hexcons"
else
  printf '  skip  CoreSlotByConsensusAddress (no hex cons address from %s/validators)\n' "$BASE_RPC"
fi
# ReservedConsensusAddress: a clean chain has no reservations (returns not-found), so a
# 200 needs a real reservation fixture. Opt in by exporting RESERVED_CONS_HEX with the
# lowercase-hex consensus address of a known reservation — e.g. seed one into a localnet
# genesis with scripts/seed-reservation.sh, which prints the value to use. Skipped
# otherwise. (Deterministic coverage of this query lives in the Go integration test
# x/coreslot/keeper/query_server_test.go.)
if [[ -n "${RESERVED_CONS_HEX:-}" ]]; then
  check_200 "/twilight/coreslot/v1/reserved-consensus-address/{hex}" "$BASE_REST/twilight/coreslot/v1/reserved-consensus-address/$RESERVED_CONS_HEX"
else
  printf '  skip  ReservedConsensusAddress (set RESERVED_CONS_HEX to a known reserved hex address)\n'
fi

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
