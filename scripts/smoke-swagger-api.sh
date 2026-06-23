#!/usr/bin/env bash
# Smoke test for the Twilight Swagger/OpenAPI surface (API-3).
#
# Verifies that, when api.swagger=true, the app serves the Swagger UI and the merged
# OpenAPI spec, that the spec is valid JSON covering both the custom twilight modules
# and the enabled generic Cosmos modules, and that the REST routes from API-0..2 still
# work. Standard modules Twilight does not run (staking/gov/mint/distribution) are not
# required and never fail the run.
#
# Usage:
#   ./scripts/smoke-swagger-api.sh
#   BASE_REST=http://16.192.99.123:1317 ./scripts/smoke-swagger-api.sh
set -uo pipefail

BASE_REST="${BASE_REST:-http://localhost:1317}"
SPEC_URL="$BASE_REST/swagger/twilight.swagger.json"
pass=0; fail=0

ok()   { printf '  ok    %s\n' "$1"; pass=$((pass+1)); }
bad()  { printf '  FAIL  %s\n' "$1"; fail=$((fail+1)); }
# curl -w already emits 000 on a connection failure; do not append another 000.
code() { local c; c="$(curl -s -o /dev/null -w '%{http_code}' --max-time 8 "$1" 2>/dev/null)"; echo "${c:-000}"; }

echo "== Twilight Swagger smoke =="
echo "REST=$BASE_REST"
echo

# Preflight: requires a RUNNING node with api.enable=true AND api.swagger=true.
if [[ "$(code "$BASE_REST/cosmos/base/tendermint/v1beta1/node_info")" == "000" ]]; then
  echo "ERROR: no REST API reachable at $BASE_REST (connection failed)." >&2
  echo "This smoke runs against a LIVE node with [api] enable=true and swagger=true." >&2
  echo "Start a localnet (see scripts/smoke-api-surface.sh preflight) and also set" >&2
  echo "  sed -i.bak '/^\\[api\\]/,/^\\[/ s/^swagger = false/swagger = true/' node0/config/app.toml" >&2
  echo "or point BASE_REST at a running node, e.g. BASE_REST=http://16.192.99.123:1317" >&2
  exit 2
fi

# 1. Swagger UI served
c="$(code "$BASE_REST/swagger/")"
[[ "$c" == "200" ]] && ok "/swagger/ -> 200" || bad "/swagger/ -> $c (expected 200)"

# 2. Spec served + valid JSON
c="$(code "$SPEC_URL")"
[[ "$c" == "200" ]] && ok "/swagger/twilight.swagger.json -> 200" || bad "/swagger/twilight.swagger.json -> $c"
spec="$(curl -s --max-time 8 "$SPEC_URL" 2>/dev/null)"
if jq -e . >/dev/null 2>&1 <<<"$spec"; then ok "spec is valid JSON"; else bad "spec is not valid JSON"; fi

# 3. Spec coverage
nrewards="$(jq -r '.paths|keys[]' <<<"$spec" 2>/dev/null | grep -c '/twilight/rewards/')"
ncoreslot="$(jq -r '.paths|keys[]' <<<"$spec" 2>/dev/null | grep -c '/twilight/coreslot/')"
ncosmos="$(jq -r '.paths|keys[]' <<<"$spec" 2>/dev/null | grep -c '/cosmos/')"
[[ "${nrewards:-0}" -ge 1 ]] && ok "spec has x/rewards routes ($nrewards)" || bad "spec missing x/rewards routes"
[[ "${ncoreslot:-0}" -ge 1 ]] && ok "spec has x/coreslot routes ($ncoreslot)" || bad "spec missing x/coreslot routes"
[[ "${ncosmos:-0}" -ge 1 ]] && ok "spec has generic /cosmos routes ($ncosmos)" || bad "spec missing generic /cosmos routes (full-app merge)"

# 4. Representative custom REST routes still 200
echo
for p in /twilight/rewards/v1/params /twilight/coreslot/v1/params /twilight/coreslot/v1/active-slots; do
  c="$(code "$BASE_REST$p")"
  [[ "$c" == "200" ]] && ok "$p -> 200" || bad "$p -> $c (expected 200)"
done

echo
echo "== result: pass=$pass fail=$fail =="
[[ "$fail" -eq 0 ]] && { echo "PASS"; exit 0; } || { echo "FAIL"; exit 1; }
