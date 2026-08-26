#!/usr/bin/env bash
# Negative tests for the upgrade drill's readers.
#
# The drill's job is to fail when the chain misbehaves. That is only worth
# anything if it also fails when its own readers are fed something invalid, and
# the way those readers break is quiet: a query that exits zero and prints
# nothing, a truncated JSON body, a value that arrives empty and gets treated as
# a number. Each of those once passed here.
#
# These run in about a second and need no chain. They source the drill itself, so
# what is under test is the shipping function and not a copy of it.
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

UPGRADE_DRILL_SOURCE_ONLY=1
# shellcheck source=/dev/null
source "$ROOT/scripts/localnet/upgrade-drill.sh"

PASS=0; FAIL=0
check() { # <name> <expected> <actual>
  if [[ "$2" == "$3" ]]; then printf '  ok    %-46s %s\n' "$1" "$2"; PASS=$((PASS+1))
  else printf '  FAIL  %-46s expected=%s actual=%s\n' "$1" "$2" "$3" >&2; FAIL=$((FAIL+1)); fi
}

echo "=== plan_state: only a well-formed response may mean 'no plan' ==="
# The canonical no-plan answer is a SUCCESSFUL empty response, so the exit-zero
# path is the one that has to discriminate.
#
# The injection point is the binary, not the function: BIN_B is pointed at a stub
# that prints a fixture and exits with a chosen code, so the shipped plan_state
# runs unmodified. Restating its body here would only test the restatement.
STUB="$(mktemp -d)/twilightd-stub"
cat >"$STUB" <<'STUBEOF'
#!/usr/bin/env bash
printf '%s' "$FIXTURE_BODY"
exit "${FIXTURE_RC:-0}"
STUBEOF
chmod +x "$STUB"
BIN_B="$STUB"
rpc_url() { echo "http://127.0.0.1:1"; }
export FIXTURE_BODY FIXTURE_RC

probe() { # <body> <rc> -> "<rc>:<stdout>"
  local out rc
  FIXTURE_BODY="$1"; FIXTURE_RC="$2"
  out="$(plan_state 0 2>/dev/null)"; rc=$?
  printf '%s:%s' "$rc" "$out"
}

check "exit 0, zero bytes"        "1:"                 "$(probe ''                                          0)"
check "exit 0, truncated JSON"    "1:"                 "$(probe '{"plan":{"name":'                          0)"
check "exit 0, not an object"     "1:"                 "$(probe '[]'                                        0)"
check "exit 0, plan with no name" "1:"                 "$(probe '{"plan":{"height":"540"}}'                 0)"
check "exit 0, empty plan name"   "1:"                 "$(probe '{"plan":{"name":"","height":"540"}}'       0)"
check "exit 0, not JSON"          "1:"                 "$(probe 'not json at all'                           0)"
check "transport failure"         "1:"                 "$(probe ''                                          1)"
check "error text, non-zero exit" "1:"                 "$(probe 'no upgrade scheduled'                       1)"
check "canonical empty response"  "0:none"             "$(probe '{}'                                        0)"
check "explicit null plan"        "0:none"             "$(probe '{"plan":null}'                             0)"
check "pending plan"              "0:pending:drill-v2" "$(probe '{"plan":{"name":"drill-v2","height":"540"}}' 0)"

echo
echo "=== read_required_uint: an unusable read may never become a number ==="
FAILURES=0; ASSERT_ROWS=0
UPGRADE_LOG="$(mktemp)"; SUMMARY="$(mktemp)"
r_empty()  { printf ''; }
r_bad()    { printf 'not-a-number'; }
r_err()    { return 3; }
r_ok()     { printf '540'; }
r_signed() { printf -- '-1'; }

unset V; read_required_uint V r_empty  && rc=0 || rc=1; check "empty output rejected"   "1:unset" "$rc:${V-unset}"
unset V; read_required_uint V r_bad    && rc=0 || rc=1; check "non-numeric rejected"    "1:unset" "$rc:${V-unset}"
unset V; read_required_uint V r_err    && rc=0 || rc=1; check "reader exit code honored" "1:unset" "$rc:${V-unset}"
unset V; read_required_uint V r_signed && rc=0 || rc=1; check "negative rejected"       "1:unset" "$rc:${V-unset}"
unset V; read_required_uint V r_ok     && rc=0 || rc=1; check "valid unsigned accepted" "0:540"   "$rc:${V-unset}"
check "each rejected read recorded a failure" "4" "$FAILURES"

echo
echo "=== version map: agreement between nodes is not correctness ==="
# Four nodes running the same wrong binary agree with each other perfectly.
check "a shared wrong version is not the expected map" "differs" \
  "$([[ "auth:5,bank:4,consensus:1,coreslot:1,mining:1,rewards:1,runtime:0,upgrade:1" == "$EXPECTED_VERSION_MAP" ]] && echo same || echo differs)"
check "a shared missing module is not the expected map" "differs" \
  "$([[ "auth:5,bank:4,consensus:1,coreslot:1,mining:1,rewards:1,runtime:0" == "$EXPECTED_VERSION_MAP" ]] && echo same || echo differs)"
check "a shared extra module is not the expected map" "differs" \
  "$([[ "auth:5,bank:4,consensus:1,coreslot:1,gov:1,mining:1,rewards:1,runtime:0,upgrade:2" == "$EXPECTED_VERSION_MAP" ]] && echo same || echo differs)"

rm -f "$UPGRADE_LOG" "$SUMMARY"; rm -rf "$(dirname "$STUB")"
echo
if (( FAIL > 0 )); then echo "upgrade drill negative tests: FAIL ($FAIL of $((PASS+FAIL)))" >&2; exit 1; fi
echo "upgrade drill negative tests: PASS ($PASS checks)"
