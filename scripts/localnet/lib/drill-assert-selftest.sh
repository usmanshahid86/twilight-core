#!/usr/bin/env bash
# Self/negative tests for the drill assertion library.
#
# The library exists to make a drill fail when something is wrong. That only means
# something if it also fails when fed bad input, and the ways it can break are
# quiet: a query that exits zero and prints nothing, a value that arrives empty and
# becomes a number, a PASS printed over evidence nobody wrote.
#
# Every case here corresponds to a defect that shipped at least once in the
# upgrade drill these primitives came from. They run against the real library,
# sourced, in about a second and with no chain.
set -uo pipefail
LIB="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/drill-assert.sh"
# shellcheck source=/dev/null
source "$LIB"

# Parallel indexed arrays, not associative: macOS ships bash 3.2, which has no
# `declare -A`.
GROUP_KEYS=(); GROUP_COUNTS=(); GROUP_IDX=-1
PASSED=0; FAILED=0
group() { GROUP_IDX=$((GROUP_IDX+1)); GROUP_KEYS[$GROUP_IDX]="$1"; GROUP_COUNTS[$GROUP_IDX]=0; echo; echo "=== $2 ==="; }
check() {
  GROUP_COUNTS[$GROUP_IDX]=$(( GROUP_COUNTS[GROUP_IDX] + 1 ))
  if [[ "$2" == "$3" ]]; then printf '  ok    %-48s %s\n' "$1" "$2"; PASSED=$((PASSED+1))
  else printf '  FAIL  %-48s expected=%s actual=%s\n' "$1" "$2" "$3" >&2; FAILED=$((FAILED+1)); fi
}
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT

# A complete, well-formed evidence directory. Each test mutates one thing.
mk_evidence() { # <dir> [assert-rows...]
  local d="$1"; shift
  mkdir -p "$d"
  echo x >"$d/mandatory-one.json"
  echo x >"$d/mandatory-two.json"
  echo "phase,result,detail" >"$d/summary.csv"
  : >"$d/assertions.jsonl"
  local r
  for r in "$@"; do echo "$r" >>"$d/assertions.jsonl"; done
}

# run_finalize runs finalize_verdict in a subshell (it exits) with a fully
# specified contract, and reports the process result alongside what landed on disk.
run_finalize() { # <dir> <phases-recorded> <phases-expected> <asserts> <asserts-expected> <multiset>
  local d="$1" rc
  ( DRILL_EVID_DIR="$d"
    DRILL_ASSERT_LOG="$d/assertions.jsonl"
    DRILL_SUMMARY="$d/summary.csv"
    DRILL_MANDATORY_FILES=(mandatory-one.json mandatory-two.json)
    DRILL_VERDICT_LINES=("probe=yes")
    DRILL_NAME="selftest"
    FAILURES=0; SUMMARY_ROWS="$2"; ASSERT_ROWS="$4"
    DRILL_EXPECTED_PHASES="$3"; DRILL_EXPECTED_ASSERTIONS="$5"; DRILL_EXPECTED_MULTISET="$6"
    finalize_verdict >/dev/null 2>&1 ); rc=$?
  printf '%s:%s' "$rc" "$(grep '^overall=' "$d/verdict.txt" 2>/dev/null | cut -d= -f2)"
}

A1='{"assertion":"alpha","node":"0"}'
A2='{"assertion":"alpha","node":"1"}'
MS_OK='alpha|0:1,alpha|1:1'

# ---------------------------------------------------------------------------
group reads "validated reads — an unusable read may never become a number"
FAILURES=0
r_empty()  { printf ''; }
r_bad()    { printf 'not-a-number'; }
r_err()    { return 3; }
r_neg()    { printf -- '-1'; }
r_float()  { printf '1.5'; }
r_ok()     { printf '811'; }
r_space()  { printf '  '; }

unset V; read_required_uint V r_empty && rc=0 || rc=1; check "empty rejected"          "1:unset" "$rc:${V-unset}"
unset V; read_required_uint V r_bad   && rc=0 || rc=1; check "non-numeric rejected"    "1:unset" "$rc:${V-unset}"
unset V; read_required_uint V r_err   && rc=0 || rc=1; check "exit code honored"       "1:unset" "$rc:${V-unset}"
unset V; read_required_uint V r_neg   && rc=0 || rc=1; check "negative rejected"       "1:unset" "$rc:${V-unset}"
unset V; read_required_uint V r_float && rc=0 || rc=1; check "float rejected"          "1:unset" "$rc:${V-unset}"
unset V; read_required_uint V r_space && rc=0 || rc=1; check "whitespace rejected"     "1:unset" "$rc:${V-unset}"
unset V; read_required_uint V r_ok    && rc=0 || rc=1; check "valid uint accepted"     "0:811"   "$rc:${V-unset}"
unset V; read_required_str  V r_empty && rc=0 || rc=1; check "str: empty rejected"     "1:unset" "$rc:${V-unset}"
unset V; read_required_str  V r_err   && rc=0 || rc=1; check "str: exit code honored"  "1:unset" "$rc:${V-unset}"
unset V; read_required_str  V r_bad   && rc=0 || rc=1; check "str: non-empty accepted" "0:not-a-number" "$rc:${V-unset}"
check "each rejected read raised one failure" "8" "$FAILURES"

# The destination staying UNSET is the point: a caller that ignores the return
# value must trip over an unbound variable, not proceed with a silent zero.
unset V; read_required_uint V r_empty >/dev/null 2>&1
check "destination left unset, not zeroed" "unset" "${V-unset}"

# ---------------------------------------------------------------------------
group accounting "assertion accounting"
D="$TMP/acct"; mk_evidence "$D"
DRILL_EVID_DIR="$D"; DRILL_ASSERT_LOG="$D/assertions.jsonl"; DRILL_SUMMARY="$D/summary.csv"
FAILURES=0; ASSERT_ROWS=0; SUMMARY_ROWS=0
expect "match" "a" "a" 0 >/dev/null
check "a matching expect raises no failure" "0" "$FAILURES"
check "a matching expect records a row"     "1" "$ASSERT_ROWS"
expect "mismatch" "a" "b" 1 >/dev/null 2>&1
check "a mismatching expect raises one"     "1" "$FAILURES"
check "a mismatching expect still records"  "2" "$ASSERT_ROWS"
check "the failed row is recorded as FAIL"  "FAIL" "$(jq -r 'select(.assertion=="mismatch")|.result' "$DRILL_ASSERT_LOG")"
phase_begin; expect "in-phase" "a" "b" 2 >/dev/null 2>&1; phase_end "p" "d"
check "a phase with a failure records FAIL" "FAIL" "$(grep '^p,' "$DRILL_SUMMARY" | cut -d, -f2)"
phase_begin; expect "clean" "a" "a" 3 >/dev/null; phase_end "q" "d"
check "a later clean phase records PASS"    "PASS" "$(grep '^q,' "$DRILL_SUMMARY" | cut -d, -f2)"

# ---------------------------------------------------------------------------
group multiset "the (assertion,node) multiset"
D="$TMP/ms"; mk_evidence "$D" "$A1" "$A2"
check "correct fan-out passes"    "0:PASS" "$(run_finalize "$D" 1 1 2 2 "$MS_OK")"
D="$TMP/ms2"; mk_evidence "$D" "$A1" "$A2"
check "a wrong count fails"       "1:FAIL" "$(run_finalize "$D" 1 1 2 2 'alpha|0:2,alpha|1:1')"
D="$TMP/ms3"; mk_evidence "$D" "$A1" "$A2"
check "a wrong name fails"        "1:FAIL" "$(run_finalize "$D" 1 1 2 2 'beta|0:1,beta|1:1')"
# The defect a name-only key cannot see: one repetition lost, another duplicated.
D="$TMP/ms4"; mk_evidence "$D" "$A1" "$A1"
check "same total, wrong nodes, fails" "1:FAIL" "$(run_finalize "$D" 1 1 2 2 "$MS_OK")"
check "name-only view could not see it" "alpha:2" \
  "$(jq -r '.assertion' "$TMP/ms4/assertions.jsonl" | LC_ALL=C sort | uniq -c | awk '{printf "%s:%s",$2,$1}')"

# ---------------------------------------------------------------------------
group completeness "missing phases, missing assertions, missing evidence"
D="$TMP/c1"; mk_evidence "$D" "$A1" "$A2"
check "a short phase count fails"     "1:FAIL" "$(run_finalize "$D" 4 5 2 2 "$MS_OK")"
D="$TMP/c2"; mk_evidence "$D" "$A1" "$A2"
check "an extra phase fails"          "1:FAIL" "$(run_finalize "$D" 6 5 2 2 "$MS_OK")"
D="$TMP/c3"; mk_evidence "$D" "$A1"
check "a missing assertion fails"     "1:FAIL" "$(run_finalize "$D" 1 1 1 2 "$MS_OK")"
D="$TMP/c4"; mk_evidence "$D" "$A1" "$A2"; rm "$D/mandatory-two.json"
check "missing evidence fails"        "1:FAIL" "$(run_finalize "$D" 1 1 2 2 "$MS_OK")"
D="$TMP/c5"; mk_evidence "$D" "$A1" "$A2"; : >"$D/mandatory-two.json"
check "empty evidence fails"          "1:FAIL" "$(run_finalize "$D" 1 1 2 2 "$MS_OK")"
check "no evidence directory fails"   "1:" "$(run_finalize "$TMP/absent" 1 1 2 2 "$MS_OK")"

# ---------------------------------------------------------------------------
group writes "evidence-write failure cannot leave a usable PASS"
D="$TMP/w1"; mk_evidence "$D" "$A1" "$A2"; chmod 400 "$D/summary.csv"
check "unwritable summary fails"          "1:FAIL" "$(run_finalize "$D" 1 1 2 2 "$MS_OK")"
chmod 600 "$D/summary.csv"
# The trap this closes: exiting FAIL while the file on disk still reads PASS.
D="$TMP/w2"; mk_evidence "$D" "$A1" "$A2"; chmod 400 "$D/summary.csv"
run_finalize "$D" 1 1 2 2 "$MS_OK" >/dev/null
check "no stale PASS left on disk" "absent" \
  "$(grep -q '^overall=PASS$' "$D/verdict.txt" 2>/dev/null && echo present || echo absent)"
chmod 600 "$D/summary.csv"

# ---------------------------------------------------------------------------
group verdict "the verdict exposes every outcome, not just one word"
D="$TMP/v1"; mk_evidence "$D" "$A1" "$A2"
run_finalize "$D" 1 1 2 2 "$MS_OK" >/dev/null
check "extra outcome lines are written" "probe=yes" "$(grep '^probe=' "$D/verdict.txt")"
check "overall is written separately"   "overall=PASS" "$(grep '^overall=' "$D/verdict.txt")"

# ---------------------------------------------------------------------------
group gates "component verdicts must be able to fail the run"
# Writing per-component outcomes into verdict.txt without consulting them let a
# run exit zero carrying "join=FAIL overall=PASS" — worse than not reporting
# them, because it looks like a checked claim.
run_gated() { # <dir> <gates-csv> <lines-csv> -> "<rc>:<persisted overall>"
  local d="$1" rc
  ( DRILL_EVID_DIR="$d"; DRILL_ASSERT_LOG="$d/assertions.jsonl"; DRILL_SUMMARY="$d/summary.csv"
    DRILL_MANDATORY_FILES=(mandatory-one.json mandatory-two.json)
    DRILL_NAME="selftest"; FAILURES=0; SUMMARY_ROWS=1; ASSERT_ROWS=2
    DRILL_EXPECTED_PHASES=1; DRILL_EXPECTED_ASSERTIONS=2; DRILL_EXPECTED_MULTISET="$MS_OK"
    IFS=',' read -r -a DRILL_VERDICT_GATES <<<"$2"
    IFS=',' read -r -a DRILL_VERDICT_LINES <<<"$3"
    [[ -z "$2" ]] && DRILL_VERDICT_GATES=()
    [[ -z "$3" ]] && DRILL_VERDICT_LINES=()
    finalize_verdict >/dev/null 2>&1 ); rc=$?
  printf '%s:%s' "$rc" "$(grep '^overall=' "$d/verdict.txt" 2>/dev/null | cut -d= -f2)"
}
GATES='export=PASS,join=PASS,restore=SUPPORTED|REFUSED_AS_DESIGNED'
mkg() { mk_evidence "$TMP/$1" "$A1" "$A2"; echo "$TMP/$1"; }

check "all components satisfied"        "0:PASS" "$(run_gated "$(mkg g1)"  "$GATES" 'export=PASS,restore=REFUSED_AS_DESIGNED,join=PASS')"
check "the other allowed restore value" "0:PASS" "$(run_gated "$(mkg g2)"  "$GATES" 'export=PASS,restore=SUPPORTED,join=PASS')"
# The exact reported defect.
check "join=FAIL forces overall=FAIL"   "1:FAIL" "$(run_gated "$(mkg g3)"  "$GATES" 'export=PASS,restore=REFUSED_AS_DESIGNED,join=FAIL')"
check "export=FAIL forces overall=FAIL" "1:FAIL" "$(run_gated "$(mkg g4)"  "$GATES" 'export=FAIL,restore=REFUSED_AS_DESIGNED,join=PASS')"
check "restore=DEFECT forces FAIL"      "1:FAIL" "$(run_gated "$(mkg g5)"  "$GATES" 'export=PASS,restore=DEFECT,join=PASS')"
check "a value outside the set"         "1:FAIL" "$(run_gated "$(mkg g6)"  "$GATES" 'export=MAYBE,restore=SUPPORTED,join=PASS')"
# Absence is not satisfaction.
check "a gated component never emitted" "1:FAIL" "$(run_gated "$(mkg g7)"  "$GATES" 'export=PASS,restore=SUPPORTED')"
check "no component lines at all"       "1:FAIL" "$(run_gated "$(mkg g8)"  "$GATES" '')"
# A malformed gate must be fatal, never silently permissive.
check "a gate with no ="                "1:FAIL" "$(run_gated "$(mkg g9)"  'exportPASS' 'export=PASS')"
check "a gate with an empty key"        "1:FAIL" "$(run_gated "$(mkg g10)" '=PASS' 'export=PASS')"
check "a gate with an empty value"      "1:FAIL" "$(run_gated "$(mkg g11)" 'export=' 'export=PASS')"
# A component reported twice is ambiguous.
check "a duplicate component key"       "1:FAIL" "$(run_gated "$(mkg g12)" "$GATES" 'export=PASS,export=FAIL,restore=SUPPORTED,join=PASS')"
# Backward compatibility: no gates preserves the previous behaviour.
check "no gates, components ignored"    "0:PASS" "$(run_gated "$(mkg g13)" '' 'export=FAIL,join=FAIL')"
# A prefix of an allowed value is not that value.
check "a prefix is not a match"         "1:FAIL" "$(run_gated "$(mkg g14)" 'restore=SUPPORTED' 'restore=SUPP')"
check "a superstring is not a match"    "1:FAIL" "$(run_gated "$(mkg g15)" 'restore=SUPPORTED' 'restore=SUPPORTEDX')"

# ---------------------------------------------------------------------------
group grammar "gate grammar — malformed configuration is fatal, never permissive"
# The gate parser existed to make component outcomes able to fail a run, and was
# itself fail-open on malformed input: an empty gate element was skipped, and a
# leading or doubled separator produced an empty alternative that matched an
# empty component value. Grammar is now validated in full before any matching:
#
#     nonempty-key "=" nonempty-value ( "|" nonempty-value )*
#
# Every malformed case is checked twice — the shipped predicate, and the
# persisted overall= the finalizer leaves behind.
pred() { # <gate-csv> <line-csv> -> 0|1
  ( IFS=','; read -r -a DRILL_VERDICT_GATES <<<"$1"; read -r -a DRILL_VERDICT_LINES <<<"$2"
    [[ -z "$1" ]] && DRILL_VERDICT_GATES=(); [[ -z "$2" ]] && DRILL_VERDICT_LINES=()
    verdict_gates_ok >/dev/null 2>&1 ); echo $?
}
G2='restore=SUPPORTED|REFUSED_AS_DESIGNED'

# --- malformed: leading, trailing, doubled, and a bare separator ---
check "leading | — predicate"        "1"      "$(pred 'restore=|SUPPORTED' 'restore=')"
check "leading | — finalizer"        "1:FAIL" "$(run_gated "$(mkg h1)" 'restore=|SUPPORTED' 'restore=')"
check "trailing | — predicate"       "1"      "$(pred 'restore=SUPPORTED|' 'restore=SUPPORTED')"
check "trailing | — finalizer"       "1:FAIL" "$(run_gated "$(mkg h2)" 'restore=SUPPORTED|' 'restore=SUPPORTED')"
check "doubled || — predicate"       "1"      "$(pred 'restore=SUPPORTED||REFUSED_AS_DESIGNED' 'restore=')"
check "doubled || — finalizer"       "1:FAIL" "$(run_gated "$(mkg h3)" 'restore=SUPPORTED||REFUSED_AS_DESIGNED' 'restore=')"
check "a bare | as the value — pred" "1"      "$(pred 'restore=|' 'restore=')"
check "a bare | as the value — fin"  "1:FAIL" "$(run_gated "$(mkg h4)" 'restore=|' 'restore=')"

# --- malformed: empty gate elements ---
check "a single empty gate — pred"   "1"      "$(pred ' ' 'restore=SUPPORTED')"
check "a single empty gate — fin"    "1:FAIL" "$(run_gated "$(mkg h5)" ' ' 'restore=SUPPORTED')"
check "an empty among valid — pred"  "1"      "$(pred "export=PASS, ,$G2" 'export=PASS,restore=SUPPORTED')"
check "an empty among valid — fin"   "1:FAIL" "$(run_gated "$(mkg h6)" "export=PASS, ,$G2" 'export=PASS,restore=SUPPORTED')"

# --- an empty component value cannot match a valid allowed list ---
check "empty value, single — pred"   "1"      "$(pred 'restore=SUPPORTED' 'restore=')"
check "empty value, single — fin"    "1:FAIL" "$(run_gated "$(mkg h7)" 'restore=SUPPORTED' 'restore=')"
check "empty value, two-value — pred" "1"     "$(pred "$G2" 'restore=')"
check "empty value, two-value — fin"  "1:FAIL" "$(run_gated "$(mkg h8)" "$G2" 'restore=')"

# --- well-formed gates still accept what they should ---
check "single value — predicate"     "0"      "$(pred 'restore=SUPPORTED' 'restore=SUPPORTED')"
check "single value — finalizer"     "0:PASS" "$(run_gated "$(mkg h9)" 'restore=SUPPORTED' 'restore=SUPPORTED')"
check "two-value, first — predicate" "0"      "$(pred "$G2" 'restore=SUPPORTED')"
check "two-value, first — finalizer" "0:PASS" "$(run_gated "$(mkg h10)" "$G2" 'restore=SUPPORTED')"
check "two-value, second — pred"     "0"      "$(pred "$G2" 'restore=REFUSED_AS_DESIGNED')"
check "two-value, second — finalizer" "0:PASS" "$(run_gated "$(mkg h11)" "$G2" 'restore=REFUSED_AS_DESIGNED')"

# ---------------------------------------------------------------------------
group setu "failure-path variables under set -u"
# A drill runs with `set -u`. A phase_end on a failure path that expands a variable
# the failed read never set would abort the shell mid-run and lose the phase row
# that says why. Nothing in the library may expand a caller variable it did not set.
D="$TMP/u1"; mk_evidence "$D"
( set -u
  DRILL_EVID_DIR="$D"; DRILL_ASSERT_LOG="$D/assertions.jsonl"; DRILL_SUMMARY="$D/summary.csv"
  FAILURES=0; SUMMARY_ROWS=0; PHASE_BASE=0
  phase_begin; phase_end "unset-detail" "" ) >/dev/null 2>&1
check "phase_end survives an empty detail" "0" "$?"
( set -u
  DRILL_EVID_DIR="$D"; DRILL_ASSERT_LOG="$D/assertions.jsonl"; DRILL_SUMMARY="$D/summary.csv"
  FAILURES=0; ASSERT_ROWS=0
  unset MISSING
  read_required_uint MISSING r_empty ) >/dev/null 2>&1
check "a failed read under set -u returns" "1" "$?"
( set -u; DRILL_MANDATORY_FILES=(); DRILL_VERDICT_LINES=()
  DRILL_EVID_DIR="$TMP/u2"; mkdir -p "$TMP/u2"
  DRILL_ASSERT_LOG="$TMP/u2/a.jsonl"; DRILL_SUMMARY="$TMP/u2/s.csv"
  echo "phase,result,detail" >"$DRILL_SUMMARY"; : >"$DRILL_ASSERT_LOG"
  FAILURES=0; SUMMARY_ROWS=0; ASSERT_ROWS=0
  DRILL_EXPECTED_PHASES=0; DRILL_EXPECTED_ASSERTIONS=0; DRILL_EXPECTED_MULTISET=""
  finalize_verdict ) >/dev/null 2>&1
check "finalize survives empty arrays under set -u" "0" "$?"

# ---------------------------------------------------------------------------
group locale "sorting is locale-pinned"
# The multiset constant is generated under one collation and compared under
# another otherwise, and a correct run fails on a differently-configured machine.
D="$TMP/l1"; mk_evidence "$D" '{"assertion":"Zeta","node":"0"}' '{"assertion":"alpha","node":"0"}'
ORDER_C="$(LC_ALL=C bash -c 'source "'"$LIB"'"; DRILL_ASSERT_LOG="'"$D"'/assertions.jsonl"; assert_multiset_observed')"
ORDER_EN="$(LC_ALL=en_US.UTF-8 bash -c 'source "'"$LIB"'"; DRILL_ASSERT_LOG="'"$D"'/assertions.jsonl"; assert_multiset_observed' 2>/dev/null)"
check "same order under C and en_US" "same" "$([[ "$ORDER_C" == "$ORDER_EN" ]] && echo same || echo differs)"
check "C collation puts Zeta first"  "Zeta|0:1,alpha|0:1" "$ORDER_C"

# ---------------------------------------------------------------------------
echo
echo "=== per-group counts ==="
TOTAL=0
for i in $(seq 0 $GROUP_IDX); do
  printf '  %3d  %s\n' "${GROUP_COUNTS[$i]}" "${GROUP_KEYS[$i]}"
  TOTAL=$(( TOTAL + GROUP_COUNTS[i] ))
done
printf '  %3d  TOTAL\n' "$TOTAL"

# Exact, for the same reason the library pins its multiset: an approximate target
# lets a dropped case pass unnoticed.
EXPECTED_CHECKS=76
echo
if (( TOTAL != EXPECTED_CHECKS )); then
  echo "drill-assert selftest: FAIL — $TOTAL checks ran, the contract is $EXPECTED_CHECKS" >&2
  echo "  (reconcile the contract deliberately; do not fit it to the run)" >&2
  exit 1
fi
if (( FAILED > 0 )); then
  echo "drill-assert selftest: FAIL ($FAILED of $TOTAL)" >&2; exit 1
fi
echo "drill-assert selftest: PASS ($PASSED checks)"
