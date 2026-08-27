#!/usr/bin/env bash
# Fail-closed assertion and evidence primitives for localnet drills.
#
# These are the primitives from the x/upgrade operational drill (#131/#137), which
# took three independent review rounds to get right. Every rule below is here
# because a drill once reported PASS without it:
#
#   - a read that validates inside a command substitution cannot abort the caller;
#     `exit` there kills only the subshell, and the parent continues with an empty
#     string that bash arithmetic then reads as zero
#   - an assertion floor lets checks vanish silently; only an exact multiset sees it
#   - a multiset keyed on the assertion name alone cannot see one repetition moving
#     to another, so the key includes the node
#   - a PASS printed over incomplete evidence asserts something nobody can check
#   - a verdict file written without checking the write can disagree with the
#     verdict the process actually returned
#
# The upgrade drill still carries its own copies and is deliberately untouched:
# it is frozen with two normative runs and two independent reviews against it.
# Migrating it onto this library is separate follow-up work.
#
# Consumers set the DRILL_* globals below, call drill_assert_init, then use the
# primitives and end with finalize_verdict.
#
# Sorting is locale-pinned. The multiset constant is generated under one collation
# and compared under another otherwise, and a correct run would fail on a machine
# with a different LC_COLLATE.

# ---- state the consumer configures ------------------------------------------
DRILL_EVID_DIR="${DRILL_EVID_DIR:-}"       # evidence directory
DRILL_ASSERT_LOG="${DRILL_ASSERT_LOG:-}"   # jsonl, one row per assertion
DRILL_SUMMARY="${DRILL_SUMMARY:-}"         # csv, one row per phase
DRILL_EXPECTED_PHASES="${DRILL_EXPECTED_PHASES:-0}"
DRILL_EXPECTED_ASSERTIONS="${DRILL_EXPECTED_ASSERTIONS:-0}"
DRILL_EXPECTED_MULTISET="${DRILL_EXPECTED_MULTISET:-}"
DRILL_MANDATORY_FILES=()                   # evidence that must exist and be non-empty
DRILL_VERDICT_LINES=()                     # extra "key=value" lines for verdict.txt
DRILL_VERDICT_GATES=()                     # "key=allowed|allowed" — see verdict_gates_ok

# ---- counters ---------------------------------------------------------------
FAILURES=0
ASSERT_ROWS=0
SUMMARY_ROWS=0
PHASE_BASE=0

drill_assert_init() { # <evidence-dir> [assert-log-name] [summary-name]
  DRILL_EVID_DIR="$1"
  DRILL_ASSERT_LOG="$DRILL_EVID_DIR/${2:-assertions.jsonl}"
  DRILL_SUMMARY="$DRILL_EVID_DIR/${3:-summary.csv}"
  mkdir -p "$DRILL_EVID_DIR" || return 1
  : >"$DRILL_ASSERT_LOG" || return 1
  echo "phase,result,detail" >"$DRILL_SUMMARY" || return 1
  FAILURES=0; ASSERT_ROWS=0; SUMMARY_ROWS=0; PHASE_BASE=0
}

# ---- reporting --------------------------------------------------------------
ok()   { echo "  ok: $*"; }
note() { echo "  note: $*"; }
fail() { echo "  FAIL: $*" >&2; FAILURES=$((FAILURES + 1)); }
die()  { echo "  ABORT: $*" >&2; finalize_verdict forced; }

# ---- validated reads --------------------------------------------------------
#
# read_required_uint VAR cmd [args...]
#
# Runs the command in THIS shell, checks its exit status, requires non-empty
# strictly-unsigned output, and assigns via printf -v so the value lands in the
# caller's scope. Returns non-zero having already recorded a failure, and leaves
# the destination UNSET so a caller that ignores the return value trips over an
# unbound variable rather than proceeding with a silent zero.
read_required_uint() {
  local __var="$1"; shift
  local __out __rc
  __out="$("$@" 2>/dev/null)"; __rc=$?
  if (( __rc != 0 )); then fail "$__var: reader exited $__rc ($*)"; return 1; fi
  if [[ -z "$__out" ]]; then fail "$__var: reader produced no output ($*)"; return 1; fi
  if [[ ! "$__out" =~ ^[0-9]+$ ]]; then fail "$__var: '$__out' is not an unsigned integer ($*)"; return 1; fi
  printf -v "$__var" '%s' "$__out"
  return 0
}

read_required_str() {
  local __var="$1"; shift
  local __out __rc
  __out="$("$@" 2>/dev/null)"; __rc=$?
  if (( __rc != 0 )); then fail "$__var: reader exited $__rc ($*)"; return 1; fi
  if [[ -z "$__out" ]]; then fail "$__var: reader produced no output ($*)"; return 1; fi
  printf -v "$__var" '%s' "$__out"
  return 0
}

# ---- assertions and phases --------------------------------------------------
record_assert() { # <node|-> <assertion> <expected> <observed> <result>
  jq -nc --arg n "$1" --arg a "$2" --arg e "$3" --arg o "$4" --arg r "$5" \
    '{node:$n, assertion:$a, expected:$e, observed:$o, result:$r}' >>"$DRILL_ASSERT_LOG" \
    || die "could not write assertion evidence for $2"
  ASSERT_ROWS=$((ASSERT_ROWS + 1))
}

expect() { # <assertion> <expected> <observed> [node]
  local a="$1" e="$2" o="$3" n="${4:--}"
  if [[ "$e" == "$o" ]]; then
    ok "$a ($o)"; record_assert "$n" "$a" "$e" "$o" PASS; return 0
  fi
  fail "$a: expected '$e', observed '$o'"; record_assert "$n" "$a" "$e" "$o" FAIL; return 1
}

record_phase() { # <phase> <result> <detail>
  printf '%s,%s,%s\n' "$1" "$2" "${3//,/;}" >>"$DRILL_SUMMARY" || die "could not write a summary row"
  SUMMARY_ROWS=$((SUMMARY_ROWS + 1))
}

phase_begin() { PHASE_BASE=$FAILURES; }

phase_end() { # <phase> <detail> — result derived from failures raised IN this phase
  local r=PASS
  (( FAILURES > PHASE_BASE )) && r=FAIL
  record_phase "$1" "$r" "$2"
}

# ---- the observed assertion multiset ----------------------------------------
#
# Keyed on (assertion, node). The name alone is not enough: an assertion that
# legitimately repeats across a node fan-out can lose one repetition and gain
# another elsewhere while the per-name total is unchanged.
assert_multiset_observed() {
  jq -r '"\(.assertion)|\(.node)"' "$DRILL_ASSERT_LOG" 2>/dev/null \
    | LC_ALL=C sort | uniq -c \
    | awk '{printf "%s:%s\n", $2, $1}' \
    | LC_ALL=C sort | paste -sd, - 2>/dev/null
}

# ---- component verdict gates ------------------------------------------------
#
# A drill can report per-component outcomes, and those outcomes have to be able to
# fail the run. Writing them into verdict.txt without consulting them let a run
# exit zero carrying "join=FAIL overall=PASS", which is worse than not reporting
# them at all: it looks like a checked claim.
#
# A gate must satisfy this grammar, and the WHOLE grammar is checked before any
# matching happens:
#
#     nonempty-key "=" nonempty-value ( "|" nonempty-value )*
#
# Validating the grammar rather than guarding the cases that happened to be
# reported is the point. An earlier version skipped an empty gate element and
# split the allowed list without requiring the alternatives to be non-empty, so
# `restore=|SUPPORTED` produced an empty alternative that matched an empty
# component value: a malformed gate made the parser permissive, which is the
# opposite of what a gate is for.
#
# Allowed values are compared as exact literal strings. No eval, no regex, no
# pattern interpretation: a gate is data.
#
# Undefined or empty gates preserve the previous behaviour, so a drill that does
# not report components is unaffected.
#
# Returns 0 only if every gate is satisfied; prints the reason otherwise.
verdict_gates_ok() {
  local gate key allowed line lkey lval found matched seen=""
  (( ${#DRILL_VERDICT_GATES[@]:-0} > 0 )) || return 0

  # A component reported twice is ambiguous, and picking either occurrence would
  # be a guess about which one the drill meant.
  for line in "${DRILL_VERDICT_LINES[@]:-}"; do
    [[ -n "$line" && "$line" == *=* ]] || continue
    lkey="${line%%=*}"
    case " $seen " in
      *" $lkey "*) echo "  FAIL: component '$lkey' is reported more than once" >&2; return 1 ;;
    esac
    seen="$seen $lkey"
  done

  for gate in "${DRILL_VERDICT_GATES[@]:-}"; do
    # --- grammar, checked in full before anything is matched ---
    if [[ -z "$gate" ]]; then
      echo "  FAIL: empty verdict gate element" >&2
      return 1
    fi
    if [[ "$gate" != *=* ]]; then
      echo "  FAIL: malformed verdict gate '$gate' (expected key=value[|value])" >&2
      return 1
    fi
    key="${gate%%=*}"; allowed="${gate#*=}"
    if [[ -z "$key" ]]; then
      echo "  FAIL: verdict gate '$gate' has an empty key" >&2
      return 1
    fi
    if [[ -z "$allowed" ]]; then
      echo "  FAIL: verdict gate '$key' has an empty allowed list" >&2
      return 1
    fi
    # A leading, trailing or doubled separator each mean an empty alternative,
    # and an empty alternative matches an empty component value.
    if [[ "$allowed" == "|"* || "$allowed" == *"|" || "$allowed" == *"||"* ]]; then
      echo "  FAIL: verdict gate '$key' has an empty alternative in '$allowed'" >&2
      return 1
    fi
    found=""; matched=0
    for line in "${DRILL_VERDICT_LINES[@]:-}"; do
      [[ -n "$line" && "$line" == *=* ]] || continue
      lkey="${line%%=*}"; lval="${line#*=}"
      [[ "$lkey" == "$key" ]] || continue
      found=yes
      # Literal comparison against each allowed value, split on '|'.
      local rest="$allowed" one
      while [[ -n "$rest" ]]; do
        one="${rest%%|*}"
        [[ "$one" == "$lval" ]] && { matched=1; break; }
        [[ "$rest" == *"|"* ]] && rest="${rest#*|}" || rest=""
      done
      break
    done
    if [[ -z "$found" ]]; then
      echo "  FAIL: verdict gate '$key' has no reported component; absence is not satisfaction" >&2
      return 1
    fi
    if (( matched == 0 )); then
      echo "  FAIL: component '$key' is outside its allowed set ($allowed)" >&2
      return 1
    fi
  done
  return 0
}

# ---- finalization -----------------------------------------------------------
#
# Everything a printed PASS stands on is checked here, and both closing writes are
# checked too. A FAIL verdict is written unconditionally: skipping the rewrite
# after a failed write is how a run can exit FAIL while the file on disk says PASS.
finalize_verdict() { # [forced]
  local forced="${1:-}" verdict="PASS" f observed line

  (( FAILURES > 0 )) && verdict="FAIL"
  [[ -n "$forced" ]] && verdict="FAIL"

  if [[ -n "${DRILL_EVID_DIR:-}" && -d "$DRILL_EVID_DIR" ]]; then
    for f in "${DRILL_MANDATORY_FILES[@]:-}"; do
      [[ -z "$f" ]] && continue
      [[ -s "$DRILL_EVID_DIR/$f" ]] || { echo "  FAIL: mandatory evidence $f is missing or empty" >&2; verdict="FAIL"; }
    done

    if (( DRILL_EXPECTED_PHASES > 0 )) && (( SUMMARY_ROWS != DRILL_EXPECTED_PHASES )); then
      echo "  FAIL: $SUMMARY_ROWS of $DRILL_EXPECTED_PHASES phase rows recorded; the run did not complete" >&2
      verdict="FAIL"
    fi

    if [[ -n "$DRILL_EXPECTED_MULTISET" ]]; then
      observed="$(assert_multiset_observed)"
      if [[ "$observed" != "$DRILL_EXPECTED_MULTISET" ]]; then
        echo "  FAIL: the assertion multiset does not match the proof contract" >&2
        diff <(tr ',' '\n' <<<"$DRILL_EXPECTED_MULTISET") <(tr ',' '\n' <<<"$observed") >&2 || true
        verdict="FAIL"
      fi
    fi

    if (( DRILL_EXPECTED_ASSERTIONS > 0 )) && (( ASSERT_ROWS != DRILL_EXPECTED_ASSERTIONS )); then
      echo "  FAIL: $ASSERT_ROWS assertions recorded, the contract is exactly $DRILL_EXPECTED_ASSERTIONS" >&2
      verdict="FAIL"
    fi

    # Gates are evaluated BEFORE the verdict file is written, so the persisted
    # overall= can never disagree with the status this function returns.
    verdict_gates_ok || verdict="FAIL"

    # The verdict file carries every outcome the drill wants exposed, not just an
    # overall word, so a later reader cannot collapse a nuanced result into "PASS".
    {
      for line in "${DRILL_VERDICT_LINES[@]:-}"; do
        [[ -n "$line" ]] && echo "$line"
      done
      echo "overall=$verdict"
    } >"$DRILL_EVID_DIR/verdict.txt" || {
      echo "  FAIL: could not write verdict.txt" >&2; verdict="FAIL"
    }

    printf '%s,%s,%s\n' "final" "$verdict" "failures=$FAILURES assertions=$ASSERT_ROWS" >>"$DRILL_SUMMARY" || {
      echo "  FAIL: could not append the final summary row" >&2; verdict="FAIL"
    }

    if ! grep -q "^overall=$verdict$" "$DRILL_EVID_DIR/verdict.txt" 2>/dev/null; then
      {
        for line in "${DRILL_VERDICT_LINES[@]:-}"; do
          [[ -n "$line" ]] && echo "$line"
        done
        echo "overall=$verdict"
      } >"$DRILL_EVID_DIR/verdict.txt" 2>/dev/null || true
      if ! grep -q "^overall=$verdict$" "$DRILL_EVID_DIR/verdict.txt" 2>/dev/null; then
        echo "  FAIL: verdict.txt does not read back as overall=$verdict" >&2
        verdict="FAIL"
        echo "overall=FAIL" >"$DRILL_EVID_DIR/verdict.txt" 2>/dev/null || true
      fi
    fi
  else
    echo "  FAIL: no evidence directory; nothing about this run can be checked" >&2
    verdict="FAIL"
  fi

  echo
  if [[ "$verdict" == "PASS" ]]; then
    echo "${DRILL_NAME:-drill}: PASS"; exit 0
  fi
  echo "${DRILL_NAME:-drill}: FAIL (failures=$FAILURES)" >&2; exit 1
}
