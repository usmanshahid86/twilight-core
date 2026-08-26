#!/usr/bin/env bash
# Negative tests for the upgrade drill's predicates.
#
# The drill's job is to fail when the chain misbehaves. That is only worth anything
# if it also fails when its own readers are fed something invalid, and the way those
# readers break is quiet: a query that exits zero and prints nothing, a response
# shape nobody anticipated, a value that arrives empty and gets treated as a number.
# Every one of those has shipped in this drill at least once.
#
# Three review rounds found four such defects while the previous suite stayed green,
# because it only covered two functions. So the rule here is that anything which can
# decide PASS gets tested: parsers AND the invariant predicates built on them. A
# strict predicate that is no longer called is caught by the drill's assertion
# multiset, not by this file — the two mechanisms cover different halves.
#
# Everything runs against the shipped functions, sourced from the drill itself. No
# chain, no localnet, about a second.
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

UPGRADE_DRILL_SOURCE_ONLY=1
# shellcheck source=/dev/null
source "$ROOT/scripts/localnet/upgrade-drill.sh"

# Parallel indexed arrays rather than an associative one: this has to run under the
# bash 3.2 that ships with macOS, where `declare -A` does not exist.
GROUP_KEYS=()
GROUP_COUNTS=()
GROUP_IDX=-1
PASSED=0; FAILED=0
group() { # <key> <label>
  GROUP_IDX=$(( GROUP_IDX + 1 ))
  GROUP_KEYS[$GROUP_IDX]="$1"
  GROUP_COUNTS[$GROUP_IDX]=0
  echo; echo "=== $2 ==="
}
check() { # <name> <expected> <actual>
  GROUP_COUNTS[$GROUP_IDX]=$(( GROUP_COUNTS[GROUP_IDX] + 1 ))
  if [[ "$2" == "$3" ]]; then printf '  ok    %-52s %s\n' "$1" "$2"; PASSED=$((PASSED+1))
  else printf '  FAIL  %-52s expected=%s actual=%s\n' "$1" "$2" "$3" >&2; FAILED=$((FAILED+1)); fi
}
# rc-and-output in one string, so a helper that returns 0 with no output cannot be
# mistaken for one that failed.
rcout() { local o rc; o="$("$@" 2>/dev/null)"; rc=$?; printf '%s:%s' "$rc" "$o"; }
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT

# ---------------------------------------------------------------------------
group b5-classify "B5 — plan_classify: only a verified canonical shape may mean 'no plan'"
# Absence arrives as a SUCCESSFUL empty response (x/upgrade's CurrentPlan returns
# QueryCurrentPlanResponse{} with a nil error), so the exit-zero path is the one that
# has to discriminate. Cases 7 and 8 are the two canonical encodings of that empty
# response; the drill records the real bytes and asserts they are one of them.
check "zero bytes"                  "1:"                 "$(rcout plan_classify '')"
check "truncated JSON"              "1:"                 "$(rcout plan_classify '{"plan":{"name":')"
check "not JSON at all"             "1:"                 "$(rcout plan_classify 'not json')"
check "array"                       "1:"                 "$(rcout plan_classify '[]')"
check "string scalar"               "1:"                 "$(rcout plan_classify '"drill-v2"')"
check "numeric scalar"              "1:"                 "$(rcout plan_classify '123')"
check "canonical empty object"      "0:none"             "$(rcout plan_classify '{}')"
check "canonical null plan"         "0:none"             "$(rcout plan_classify '{"plan":null}')"
check "unknown object"              "1:"                 "$(rcout plan_classify '{"unexpected":1}')"
check "plan-less object with name"  "1:"                 "$(rcout plan_classify '{"name":"drill-v2","height":"540"}')"
check "plan is an empty object"     "1:"                 "$(rcout plan_classify '{"plan":{}}')"
check "plan is an array"            "1:"                 "$(rcout plan_classify '{"plan":[]}')"
check "empty name"                  "1:"                 "$(rcout plan_classify '{"plan":{"name":""}}')"
check "null name"                   "1:"                 "$(rcout plan_classify '{"plan":{"name":null}}')"
check "numeric name"                "1:"                 "$(rcout plan_classify '{"plan":{"name":123}}')"
check "boolean name"                "1:"                 "$(rcout plan_classify '{"plan":{"name":true}}')"
check "object name"                 "1:"                 "$(rcout plan_classify '{"plan":{"name":{"a":1}}}')"
check "extra envelope key"          "1:"                 "$(rcout plan_classify '{"plan":{"name":"drill-v2"},"extra":1}')"
check "null plan, extra key"        "1:"                 "$(rcout plan_classify '{"plan":null,"extra":1}')"
check "valid pending"               "0:pending:drill-v2" "$(rcout plan_classify '{"plan":{"name":"drill-v2"}}')"
check "valid pending, full body"    "0:pending:drill-v2" \
  "$(rcout plan_classify '{"plan":{"name":"drill-v2","height":"540","info":"x","upgraded_client_state":null}}')"

# ---------------------------------------------------------------------------
group b5-fetch "B5 — plan_state: the fetch layer, and that it still delegates"
# Injection is at the binary, so the shipped plan_state runs unmodified. If it ever
# stopped calling plan_classify — or grew its own classification back — the shape
# cases below would diverge from the parser's.
STUB="$TMP/twilightd-stub"
cat >"$STUB" <<'STUBEOF'
#!/usr/bin/env bash
printf '%s' "$FIXTURE_BODY"
exit "${FIXTURE_RC:-0}"
STUBEOF
chmod +x "$STUB"
BIN_B="$STUB"
rpc_url() { echo "http://127.0.0.1:1"; }
export FIXTURE_BODY FIXTURE_RC
fetch() { FIXTURE_BODY="$1"; FIXTURE_RC="$2"; rcout plan_state 0; }

check "non-zero exit with error text" "1:"                 "$(fetch 'no upgrade scheduled' 1)"
check "non-zero exit, silent"         "1:"                 "$(fetch '' 1)"
check "exit 0, zero bytes"            "1:"                 "$(fetch '' 0)"
check "delegates: canonical absence"  "0:none"             "$(fetch '{"plan":null}' 0)"
check "delegates: pending plan"       "0:pending:drill-v2" "$(fetch '{"plan":{"name":"drill-v2"}}' 0)"
check "delegates: unknown object"     "1:"                 "$(fetch '{"unexpected":1}' 0)"

# ---------------------------------------------------------------------------
group b1-reads "B1 — an unusable read may never become a number"
FAILURES=0; ASSERT_ROWS=0; SUMMARY_ROWS=0
UPGRADE_LOG="$TMP/u.jsonl"; SUMMARY="$TMP/s.csv"
r_empty()  { printf ''; }
r_bad()    { printf 'not-a-number'; }
r_err()    { return 3; }
r_ok()     { printf '540'; }
r_signed() { printf -- '-1'; }
r_float()  { printf '1.5'; }

unset V; read_required_uint V r_empty  && rc=0 || rc=1; check "empty output rejected"      "1:unset" "$rc:${V-unset}"
unset V; read_required_uint V r_bad    && rc=0 || rc=1; check "non-numeric rejected"       "1:unset" "$rc:${V-unset}"
unset V; read_required_uint V r_err    && rc=0 || rc=1; check "reader exit code honored"   "1:unset" "$rc:${V-unset}"
unset V; read_required_uint V r_signed && rc=0 || rc=1; check "negative rejected"          "1:unset" "$rc:${V-unset}"
unset V; read_required_uint V r_float  && rc=0 || rc=1; check "float rejected"             "1:unset" "$rc:${V-unset}"
unset V; read_required_uint V r_ok     && rc=0 || rc=1; check "valid unsigned accepted"    "0:540"   "$rc:${V-unset}"
unset V; read_required_str  V r_empty  && rc=0 || rc=1; check "str: empty rejected"        "1:unset" "$rc:${V-unset}"
check "every rejected read raised a failure" "6" "$FAILURES"

# ---------------------------------------------------------------------------
group b2-refusal "B2 — a refusal must come from THIS start, not the append-only log"
LOG="$TMP/node3.log"
{
  echo "old line"
  echo 'UPGRADE "drill-v2" NEEDED at height: 540'
  echo "more old"
} >"$LOG"
OFF="$(wc -l <"$LOG" | tr -d ' ')"
check "old refusals do not count as new"  "0:0"   "$(rcout refusals_after_offset "$LOG" "$OFF" drill-v2 540)"
echo 'UPGRADE "drill-v2" NEEDED at height: 540' >>"$LOG"
check "a new refusal after the offset"    "0:1"   "$(rcout refusals_after_offset "$LOG" "$OFF" drill-v2 540)"
check "whole file counts both"            "0:2"   "$(rcout refusals_after_offset "$LOG" 0 drill-v2 540)"
check "a different name does not match"   "0:0"   "$(rcout refusals_after_offset "$LOG" 0 other-name 540)"
check "a different height does not match" "0:0"   "$(rcout refusals_after_offset "$LOG" 0 drill-v2 541)"
check "a non-numeric offset is rejected"  "1:"    "$(rcout refusals_after_offset "$LOG" abc drill-v2 540)"
check "a missing log is zero, not error"  "0:0"   "$(rcout refusals_after_offset "$TMP/none.log" 0 drill-v2 540)"
NET="$TMP"; mkdir -p "$TMP/logs"; cp "$LOG" "$TMP/logs/node3.log"
echo 'INFO handshake appHeight=539 module=consensus' >>"$TMP/logs/node3.log"
app_height() { return 1; }   # RPC unavailable, as it is for a node that died in replay
check "height read from this start's log" "0:539" "$(rcout app_height_after_offset 3 0)"

# ---------------------------------------------------------------------------
group b3-economics "B3 — the books must be identical, not merely solvent"
check "canon rejects too few values"  "1:"  "$(rcout economics_canon 1 2 3 4 5 6)"
check "canon rejects a non-number"    "1:"  "$(rcout economics_canon 1 2 3 4 5 6 x)"
check "canon rejects an empty value"  "1:"  "$(rcout economics_canon 1 2 3 4 5 6 '')"
check "canon joins seven values"      "0:1080:10:0:10:149828400:149828400:0" \
  "$(rcout economics_canon 1080 10 0 10 149828400 149828400 0)"
GOOD="1080:10:0:10:149828400:149828400:0"
check "identical books are preserved" "0:" "$(rcout economics_preserved "$GOOD" "$GOOD")"
# Each stable value moved on its own must fail: a total that still balances is
# exactly the case a solvency check cannot see.
for i in 1 2 3 4 5 6 7; do
  ALT="$(awk -v n="$i" 'BEGIN{split("1080:10:0:10:149828400:149828400:0",a,":");a[n]=a[n]+1;
        printf "%s:%s:%s:%s:%s:%s:%s",a[1],a[2],a[3],a[4],a[5],a[6],a[7]}')"
  check "field $i moved alone is caught" "1:" "$(rcout economics_preserved "$GOOD" "$ALT")"
done
check "malformed canon is not preserved" "1:" "$(rcout economics_preserved "$GOOD" "1:2:3")"
check "solvency holds when balanced"     "0:" "$(rcout solvency_holds 100 60 40)"
check "solvency fails when short"        "1:" "$(rcout solvency_holds 100 60 41)"
check "solvency rejects a non-number"    "1:" "$(rcout solvency_holds 100 60 x)"

# ---------------------------------------------------------------------------
group b4-validators "B4 — the validator set and the topology the quorum argument needs"
BLK='{"result":{"block":{"header":{"validators_hash":"AA11","next_validators_hash":"BB22"}}}}'
check "hashes parsed"                 "0:AA11:BB22" "$(rcout validator_hashes_from_json "$BLK")"
check "missing validators_hash"       "1:" "$(rcout validator_hashes_from_json '{"result":{"block":{"header":{"next_validators_hash":"BB22"}}}}')"
check "missing next_validators_hash"  "1:" "$(rcout validator_hashes_from_json '{"result":{"block":{"header":{"validators_hash":"AA11"}}}}')"
check "empty validators_hash"         "1:" "$(rcout validator_hashes_from_json '{"result":{"block":{"header":{"validators_hash":"","next_validators_hash":"BB22"}}}}')"
check "no header at all"              "1:" "$(rcout validator_hashes_from_json '{"result":{}}')"
check "identical hashes are stable"   "0:" "$(rcout validator_hash_stable "AA11:BB22" "AA11:BB22")"
check "validators_hash moved"         "1:" "$(rcout validator_hash_stable "AA11:BB22" "CC33:BB22")"
check "next_validators_hash moved"    "1:" "$(rcout validator_hash_stable "AA11:BB22" "AA11:CC33")"
check "malformed pair is not stable"  "1:" "$(rcout validator_hash_stable "AA11" "AA11")"
V4='{"result":{"total":"4","validators":[{"voting_power":"1"},{"voting_power":"1"},{"voting_power":"1"},{"voting_power":"1"}]}}'
check "topology parsed"               "0:4:1:1" "$(rcout topology_from_json "$V4")"
check "total disagreeing with list"   "1:" "$(rcout topology_from_json '{"result":{"total":"5","validators":[{"voting_power":"1"}]}}')"
check "no validators"                 "1:" "$(rcout topology_from_json '{"result":{"total":"0","validators":[]}}')"
check "unequal power is visible"      "0:4:1:7" \
  "$(rcout topology_from_json '{"result":{"total":"4","validators":[{"voting_power":"1"},{"voting_power":"1"},{"voting_power":"1"},{"voting_power":"7"}]}}')"
check "required topology accepted"    "0:" "$(rcout topology_valid 4 "4:1:1")"
check "three validators rejected"     "1:" "$(rcout topology_valid 4 "3:1:1")"
check "unequal power rejected"        "1:" "$(rcout topology_valid 4 "4:1:7")"
check "slot count disagreeing"        "1:" "$(rcout topology_valid 3 "4:1:1")"

# ---------------------------------------------------------------------------
group b6-upgrade-info "B6 — the halt receipt is a precondition, not a convenience"
INFO="$TMP/upgrade-info.json"
printf '{"name":"drill-v2","height":540}' >"$INFO"
check "valid receipt"          "0:" "$(rcout upgrade_info_valid "$INFO" drill-v2 540)"
check "wrong name"             "1:" "$(rcout upgrade_info_valid "$INFO" other 540)"
check "wrong height"           "1:" "$(rcout upgrade_info_valid "$INFO" drill-v2 541)"
check "absent file"            "1:" "$(rcout upgrade_info_valid "$TMP/nope.json" drill-v2 540)"
: >"$TMP/empty.json"
check "empty file"             "1:" "$(rcout upgrade_info_valid "$TMP/empty.json" drill-v2 540)"
printf '{"name":"drill-v2",' >"$TMP/bad.json"
check "malformed JSON"         "1:" "$(rcout upgrade_info_valid "$TMP/bad.json" drill-v2 540)"
printf '{"height":540}' >"$TMP/noname.json"
check "no name field"          "1:" "$(rcout upgrade_info_valid "$TMP/noname.json" drill-v2 540)"
printf '{"name":"drill-v2","height":null}' >"$TMP/nullh.json"
check "null height"            "1:" "$(rcout upgrade_info_valid "$TMP/nullh.json" drill-v2 540)"

# ---------------------------------------------------------------------------
group s3-version-map "S3 — a version must be observed, never synthesized"
# Settled by a recorded response, not assumed: autocli omits zero values, so runtime
# arrives with no version field while every other module carries an explicit one.
# Omission renders as its own token so that it can never be mistaken for a zero, and
# the pinned map is what turns that distinction into a failure.
VM='{"module_versions":[{"name":"auth","version":"5"},{"name":"runtime","version":"0"}]}'
mapof() { version_map_from_json "$1"; }
# The real response, exactly as RUN 9 recorded it: runtime carries no version field.
REAL='{"module_versions":[{"name":"auth","version":"5"},{"name":"bank","version":"4"},{"name":"consensus","version":"1"},{"name":"coreslot","version":"1"},{"name":"mining","version":"1"},{"name":"rewards","version":"1"},{"name":"runtime"},{"name":"upgrade","version":"2"}]}'
check "the recorded response parses"      "0:$EXPECTED_VERSION_MAP" "$(rcout mapof "$REAL")"
check "runtime omitted is the pinned map" "match" \
  "$([[ "$(mapof "$REAL")" == "$EXPECTED_VERSION_MAP" ]] && echo match || echo differs)"
# An explicit zero is a DIFFERENT observation from an omission, and must not satisfy
# the pinned contract just because the two mean the same thing in protobuf.
R0='{"module_versions":[{"name":"auth","version":"5"},{"name":"bank","version":"4"},{"name":"consensus","version":"1"},{"name":"coreslot","version":"1"},{"name":"mining","version":"1"},{"name":"rewards","version":"1"},{"name":"runtime","version":"0"},{"name":"upgrade","version":"2"}]}'
check "runtime explicit 0 is not omitted" "0:auth:5,bank:4,consensus:1,coreslot:1,mining:1,rewards:1,runtime:0,upgrade:2" "$(rcout mapof "$R0")"
check "runtime explicit 0 fails the map"  "differs" \
  "$([[ "$(mapof "$R0")" == "$EXPECTED_VERSION_MAP" ]] && echo match || echo differs)"
R1='{"module_versions":[{"name":"auth","version":"5"},{"name":"bank","version":"4"},{"name":"consensus","version":"1"},{"name":"coreslot","version":"1"},{"name":"mining","version":"1"},{"name":"rewards","version":"1"},{"name":"runtime","version":"1"},{"name":"upgrade","version":"2"}]}'
check "runtime version 1 fails the map"   "differs" \
  "$([[ "$(mapof "$R1")" == "$EXPECTED_VERSION_MAP" ]] && echo match || echo differs)"
# A module that SHOULD carry a version and arrives without one is the case the old
# `// 0` rendering hid; it now reads as omitted and fails the pinned map.
RA='{"module_versions":[{"name":"auth"},{"name":"bank","version":"4"},{"name":"consensus","version":"1"},{"name":"coreslot","version":"1"},{"name":"mining","version":"1"},{"name":"rewards","version":"1"},{"name":"runtime"},{"name":"upgrade","version":"2"}]}'
check "auth omitted renders as omitted"   "0:auth:omitted-zero,bank:4,consensus:1,coreslot:1,mining:1,rewards:1,runtime:omitted-zero,upgrade:2" "$(rcout mapof "$RA")"
check "auth omitted fails the map"        "differs" \
  "$([[ "$(mapof "$RA")" == "$EXPECTED_VERSION_MAP" ]] && echo match || echo differs)"
RN='{"module_versions":[{"name":"auth","version":"5"},{"name":"bank","version":"4"},{"name":"consensus","version":"1"},{"name":"coreslot","version":"1"},{"name":"mining","version":"1"},{"name":"rewards","version":"1"},{"name":"runtime","version":null},{"name":"upgrade","version":"2"}]}'
check "a null version anywhere is fatal"  "1:" "$(rcout mapof "$RN")"
check "explicit versions still parse"     "0:auth:5,runtime:0" "$(rcout mapof "$VM")"
check "integer versions accepted"         "0:auth:5,runtime:0" \
  "$(rcout mapof '{"module_versions":[{"name":"auth","version":5},{"name":"runtime","version":0}]}')"
check "null version rejected"             "1:" "$(rcout mapof '{"module_versions":[{"name":"runtime","version":null}]}')"
check "negative version rejected"         "1:" "$(rcout mapof '{"module_versions":[{"name":"runtime","version":-1}]}')"
check "float version rejected"            "1:" "$(rcout mapof '{"module_versions":[{"name":"runtime","version":1.5}]}')"
check "boolean version rejected"          "1:" "$(rcout mapof '{"module_versions":[{"name":"runtime","version":true}]}')"
check "object version rejected"           "1:" "$(rcout mapof '{"module_versions":[{"name":"runtime","version":{"a":1}}]}')"
check "non-digit string rejected"         "1:" "$(rcout mapof '{"module_versions":[{"name":"runtime","version":"0x1"}]}')"
check "empty name rejected"               "1:" "$(rcout mapof '{"module_versions":[{"name":"","version":"1"}]}')"
check "duplicate name rejected"           "1:" \
  "$(rcout mapof '{"module_versions":[{"name":"auth","version":"5"},{"name":"auth","version":"5"}]}')"
check "empty list rejected"               "1:" "$(rcout mapof '{"module_versions":[]}')"
check "no module_versions key"            "1:" "$(rcout mapof '{}')"
check "sorted regardless of order"        "0:auth:5,runtime:0" \
  "$(rcout mapof '{"module_versions":[{"name":"runtime","version":"0"},{"name":"auth","version":"5"}]}')"
check "a dropped module changes the map"  "differs" \
  "$([[ "$(mapof "$VM")" == "auth:5" ]] && echo same || echo differs)"

# ---------------------------------------------------------------------------
group identity "identity — only this drill's own binaries, at this drill's own home"
NET="$TMP"
fake_ps() { FAKE_PS="$1"; }
ps() { printf '%s\n' "$FAKE_PS"; }
idcheck() { FAKE_PS="$1"; node_process_matches 4242 0 && echo match || echo rejected; }
H="$(node_home 0)"
check "binary A at the right home"    "match"    "$(idcheck "$BIN_A start --home $H --log_no_color")"
check "binary B at the right home"    "match"    "$(idcheck "$BIN_B start --home $H --log_no_color")"
check "--home=value form"             "match"    "$(idcheck "$BIN_A start --home=$H")"
check "a different node's home"       "rejected" "$(idcheck "$BIN_A start --home $(node_home 1)")"
check "a home that is a prefix"       "rejected" "$(idcheck "$BIN_A start --home ${H}0")"
check "an executable outside build/"  "rejected" "$(idcheck "/usr/bin/twilightd start --home $H")"
check "another executable in build/"  "rejected" "$(idcheck "$ROOT/build/unrelated-tool start --home $H")"
check "no --home at all"              "rejected" "$(idcheck "$BIN_A start")"
check "no process"                    "rejected" "$(idcheck "")"
unset -f ps

# ---------------------------------------------------------------------------
group s1-evidence "S1 — a printed PASS must stand on complete evidence"
# finish() exits, so each case runs in a subshell with its own evidence directory.
mk_evidence() { # <dir>
  mkdir -p "$1"
  for f in binaries.json upgrade.jsonl economics.jsonl summary.csv hashes.jsonl \
           plan-response-pending.json plan-response-cleared.json version-map-response.json \
           node0-upgrade-info.json node1-upgrade-info.json node2-upgrade-info.json \
           node3-upgrade-info.json node0-stale-upgrade-info-before-restart.json; do
    echo x >"$1/$f"
  done
  : >"$1/upgrade.jsonl"
  printf '{"assertion":"only_one","node":"-"}\n' >"$1/upgrade.jsonl"
}
run_finish() { # <dir> <multiset> <total> <rows-recorded> [<phases-expected>]
  local d="$1" rc
  ( EVID_DIR="$d"; SUMMARY="$d/summary.csv"; UPGRADE_LOG="$d/upgrade.jsonl"
    FAILURES=0; ASSERT_ROWS="$3"; SUMMARY_ROWS="$4"
    EXPECTED_ASSERT_MULTISET="$2"; EXPECTED_ASSERTIONS="$3"; EXPECTED_PHASES="${5:-$4}"
    finish >/dev/null 2>&1 ); rc=$?
  printf '%s:%s' "$rc" "$(cat "$d/verdict.txt" 2>/dev/null)"
}
D="$TMP/e1"; mk_evidence "$D"
check "complete evidence passes"    "0:PASS" "$(run_finish "$D" "only_one|-:1" 1 15)"
D="$TMP/e2"; mk_evidence "$D"; rm "$D/hashes.jsonl"
check "a missing file fails"        "1:FAIL" "$(run_finish "$D" "only_one|-:1" 1 15)"
D="$TMP/e3"; mk_evidence "$D"; rm "$D/plan-response-cleared.json"
check "missing plan evidence fails" "1:FAIL" "$(run_finish "$D" "only_one|-:1" 1 15)"
D="$TMP/e4"; mk_evidence "$D"
check "a wrong multiset fails"      "1:FAIL" "$(run_finish "$D" "only_one|-:4" 1 15)"
D="$TMP/e5"; mk_evidence "$D"
check "a redistributed multiset fails" "1:FAIL" "$(run_finish "$D" "other_name|-:1" 1 15)"
D="$TMP/e6"; mk_evidence "$D"
check "a short phase count fails"   "1:FAIL" "$(run_finish "$D" "only_one|-:1" 1 14 15)"
# A failed closing write must never leave PASS behind on disk.
D="$TMP/e7"; mk_evidence "$D"; chmod 400 "$D/summary.csv"
check "unwritable summary leaves FAIL" "1:FAIL" "$(run_finish "$D" "only_one|-:1" 1 15)"
chmod 600 "$D/summary.csv"

# The concrete regression the name-only multiset could not see.
#
# migration_applied legitimately spans two phases — nodes 0-2 when the upgraded
# majority crosses the boundary, node 3 after the stale node rejoins. So a run that
# lost the node-3 check and double-counted node 0 still emits four of them, and a
# contract keyed on the name alone cannot tell that apart from the correct fan-out.
# What is lost is the proof that the stale node ever applied the migration.
D="$TMP/e8"; mk_evidence "$D"
printf '%s\n' \
  '{"assertion":"migration_applied","node":"0"}' \
  '{"assertion":"migration_applied","node":"0"}' \
  '{"assertion":"migration_applied","node":"1"}' \
  '{"assertion":"migration_applied","node":"2"}' >"$D/upgrade.jsonl"
check "name-only view cannot see the loss" "migration_applied:4" \
  "$(jq -r '.assertion' "$D/upgrade.jsonl" | sort | uniq -c | awk '{printf "%s:%s\n",$2,$1}' | paste -sd, -)"
NODE_CONTRACT="migration_applied|0:1,migration_applied|1:1,migration_applied|2:1,migration_applied|3:1"
check "node-keyed contract rejects it"     "1:FAIL" "$(run_finish "$D" "$NODE_CONTRACT" 4 15)"
D="$TMP/e9"; mk_evidence "$D"
printf '%s\n' \
  '{"assertion":"migration_applied","node":"0"}' \
  '{"assertion":"migration_applied","node":"1"}' \
  '{"assertion":"migration_applied","node":"2"}' \
  '{"assertion":"migration_applied","node":"3"}' >"$D/upgrade.jsonl"
check "the correct fan-out still passes"   "0:PASS" "$(run_finish "$D" "$NODE_CONTRACT" 4 15)"

# ---------------------------------------------------------------------------
echo
echo "=== per-group counts ==="
TOTAL=0
for i in $(seq 0 $GROUP_IDX); do
  printf '  %3d  %s\n' "${GROUP_COUNTS[$i]}" "${GROUP_KEYS[$i]}"
  TOTAL=$(( TOTAL + GROUP_COUNTS[i] ))
done
printf '  %3d  TOTAL\n' "$TOTAL"

# An exact count, for the same reason the drill pins its assertion multiset: an
# approximate target lets a silently-dropped case pass unnoticed, which is the very
# defect this file exists to catch.
EXPECTED_CHECKS=125
echo
if (( TOTAL != EXPECTED_CHECKS )); then
  echo "upgrade drill negative tests: FAIL — $TOTAL checks ran, the contract is $EXPECTED_CHECKS" >&2
  echo "  (a case was added or dropped; reconcile the contract deliberately, do not fit it to the run)" >&2
  exit 1
fi
if (( FAILED > 0 )); then
  echo "upgrade drill negative tests: FAIL ($FAILED of $TOTAL)" >&2; exit 1
fi
echo "upgrade drill negative tests: PASS ($PASSED checks)"
