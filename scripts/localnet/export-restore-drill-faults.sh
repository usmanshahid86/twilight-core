#!/usr/bin/env bash
# Negative tests for the export/restore drill's outcome classification.
#
# The drill's three answers are only worth anything if each one can be wrong. The
# dangerous paths are the ones a passing run never exercises:
#
#   - every restored process is gone, but for the WRONG reason. "It died" is not
#     evidence of a designed refusal, and classifying it as one would report a
#     crash as correct behaviour.
#   - the restored chain runs, but on state that silently lost something. That is
#     worse than a refusal, because nobody finds out.
#   - a component verdict derived from a proxy: an artifact that exists is not one
#     that carried the right state, and a node at height 1 has not joined.
#
# These run against the shipped classifiers, sourced from the drill, with explicit
# inputs and no chain.
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

EXPORT_DRILL_SOURCE_ONLY=1
# shellcheck source=/dev/null
source "$ROOT/scripts/localnet/export-restore-drill.sh"

GROUP_KEYS=(); GROUP_COUNTS=(); GROUP_IDX=-1
PASSED=0; FAILED=0
group() { GROUP_IDX=$((GROUP_IDX+1)); GROUP_KEYS[$GROUP_IDX]="$1"; GROUP_COUNTS[$GROUP_IDX]=0; echo; echo "=== $2 ==="; }
check() {
  GROUP_COUNTS[$GROUP_IDX]=$(( GROUP_COUNTS[GROUP_IDX] + 1 ))
  if [[ "$2" == "$3" ]]; then printf '  ok    %-52s %s\n' "$1" "$2"; PASSED=$((PASSED+1))
  else printf '  FAIL  %-52s expected=%s actual=%s\n' "$1" "$2" "$3" >&2; FAILED=$((FAILED+1)); fi
}
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT

# The real refusal, as a restored node records it. Slot number and height vary per
# run and are deliberately not pinned.
REAL_REFUSAL='panic: initialize coreslot genesis: active slot 1 must have activated and activation-effective heights equal to the initial height 815: invalid core slot genesis [cosmossdk.io/errors@v1.0.2/errors.go:155]'

# ---------------------------------------------------------------------------
group refusal "refusal_class — only the identified rule counts as designed"
mklog() { printf '%s\n' "$2" >"$TMP/$1.log"; echo "$TMP/$1.log"; }
check "the real CoreSlot refusal"      "coreslot-fresh-genesis" "$(refusal_class "$(mklog real "$REAL_REFUSAL")")"
check "different slot and height"      "coreslot-fresh-genesis" \
  "$(refusal_class "$(mklog alt 'panic: initialize coreslot genesis: active slot 7 must have activated and activation-effective heights equal to the initial height 99999: invalid core slot genesis')")"
check "an unrelated panic"             "other" "$(refusal_class "$(mklog unrel 'panic: runtime error: index out of range [3]')")"
check "a bind failure"                 "other" "$(refusal_class "$(mklog bind 'ERR failed to listen: address already in use')")"
check "a rewards refusal is not this"  "other" \
  "$(refusal_class "$(mklog rew 'panic: initialize rewards genesis: epoch anchor must be version 1 effective at epoch 1')")"
check "half the message is not enough" "other" \
  "$(refusal_class "$(mklog half 'panic: initialize coreslot genesis: something else entirely')")"
check "a clean log"                    "none"  "$(refusal_class "$(mklog clean 'INF committed state height=5')")"
check "a missing log"                  "none"  "$(refusal_class "$TMP/absent.log")"

# ---------------------------------------------------------------------------
group classify "classify_restore_outcome — the branches a passing run never takes"
# nodes alive refused progress agree state participation
check "all dead, correct refusal"        "REFUSED_AS_DESIGNED" "$(classify_restore_outcome 4 0 4 0 n/a n/a n/a)"
check "all dead, unrelated panic"        "DEFECT"              "$(classify_restore_outcome 4 0 0 0 n/a n/a n/a)"
check "all dead, only some refused"      "DEFECT"              "$(classify_restore_outcome 4 0 3 0 n/a n/a n/a)"
check "progresses, participation absent" "DEFECT"              "$(classify_restore_outcome 4 4 0 5 agree match absent)"
check "progresses, hashes disagree"      "DEFECT"              "$(classify_restore_outcome 4 4 0 5 disagree match present)"
check "progresses, state mismatch"       "DEFECT"              "$(classify_restore_outcome 4 4 0 5 agree mismatch present)"
check "alive but no progress"            "DEFECT"              "$(classify_restore_outcome 4 4 0 0 agree match present)"
check "alive, progress below the floor"  "DEFECT"              "$(classify_restore_outcome 4 4 0 2 agree match present)"
check "everything holds"                 "SUPPORTED"           "$(classify_restore_outcome 4 4 0 3 agree match present)"
check "partially alive, all else good"   "SUPPORTED"           "$(classify_restore_outcome 4 2 2 5 agree match present)"
# Unevaluated inputs must not be silently accepted as satisfied.
check "n/a agreement is not agreement"   "DEFECT"              "$(classify_restore_outcome 4 4 0 5 n/a match present)"
check "n/a state is not a match"         "DEFECT"              "$(classify_restore_outcome 4 4 0 5 agree n/a present)"
check "n/a participation is not present" "DEFECT"              "$(classify_restore_outcome 4 4 0 5 agree match n/a)"
check "non-numeric input is a defect"    "DEFECT"              "$(classify_restore_outcome 4 '' 0 5 agree match present)"
check "zero nodes is a defect"           "DEFECT"              "$(classify_restore_outcome 0 0 0 0 n/a n/a n/a)"

# ---------------------------------------------------------------------------
group verdicts "component verdicts are complete sub-proofs, not proxies"
# The specific future this prevents: a node reaches height 1, never synchronizes,
# and the run still reports join=PASS.
check "join needs empty + sync + agree" "PASS" "$(join_outcome true synced agree)"
check "synced but disagreeing"          "FAIL" "$(join_outcome true synced disagree)"
check "agreeing but never synced"       "FAIL" "$(join_outcome true stalled agree)"
check "not started from empty state"    "FAIL" "$(join_outcome false synced agree)"
check "nothing established"             "FAIL" "$(join_outcome false stalled disagree)"

# An artifact that exists is not an artifact that carried the right state.
check "export needs all three"          "PASS" "$(export_outcome true true true)"
check "artifact only"                   "FAIL" "$(export_outcome true false false)"
check "artifact and height, bad content" "FAIL" "$(export_outcome true true false)"
check "content ok, height underived"    "FAIL" "$(export_outcome true false true)"
check "no artifact"                     "FAIL" "$(export_outcome false true true)"

# ---------------------------------------------------------------------------
group endtoend "the recorded outcome follows from the recorded inputs"
# The shape the real runs produce, driven through the shipped classifier from the
# per-node classes rather than asserted separately.
for i in 0 1 2 3; do printf '%s\n' "$REAL_REFUSAL" >"$TMP/n$i.log"; done
R=0
for i in 0 1 2 3; do [[ "$(refusal_class "$TMP/n$i.log")" == "coreslot-fresh-genesis" ]] && R=$((R+1)); done
check "four real refusals classify as 4" "4" "$R"
check "and the outcome is the designed one" "REFUSED_AS_DESIGNED" "$(classify_restore_outcome 4 0 "$R" 0 n/a n/a n/a)"
# One node dying differently is enough to disqualify the whole outcome.
printf '%s\n' 'panic: runtime error: index out of range [3]' >"$TMP/n2.log"
R=0
for i in 0 1 2 3; do [[ "$(refusal_class "$TMP/n$i.log")" == "coreslot-fresh-genesis" ]] && R=$((R+1)); done
check "one unrelated death drops it to 3" "3" "$R"
check "and that is a defect"              "DEFECT" "$(classify_restore_outcome 4 0 "$R" 0 n/a n/a n/a)"

# ---------------------------------------------------------------------------
echo
echo "=== per-group counts ==="
TOTAL=0
for i in $(seq 0 $GROUP_IDX); do
  printf '  %3d  %s\n' "${GROUP_COUNTS[$i]}" "${GROUP_KEYS[$i]}"
  TOTAL=$(( TOTAL + GROUP_COUNTS[i] ))
done
printf '  %3d  TOTAL\n' "$TOTAL"

EXPECTED_CHECKS=37
echo
if (( TOTAL != EXPECTED_CHECKS )); then
  echo "export/restore negative tests: FAIL — $TOTAL checks ran, the contract is $EXPECTED_CHECKS" >&2
  echo "  (reconcile the contract deliberately; do not fit it to the run)" >&2
  exit 1
fi
if (( FAILED > 0 )); then
  echo "export/restore negative tests: FAIL ($FAILED of $TOTAL)" >&2; exit 1
fi
echo "export/restore negative tests: PASS ($PASSED checks)"
