#!/usr/bin/env bash
set -euo pipefail

# Drill 3 + 4: quorum continue / halt / resume, and offline-validator-no-auto-
# power-change. Four equal-power nodes (slots 1-4).
#
# Inspired by scenario1.sh::assert_no_progress and its quorum sequence.
#
# Sequence:
#   1. confirm four nodes live and agreeing
#   2. stop one validator (node3)  -> 3/4 (75%) > 2/3: chain continues
#   3. confirm live nodes {0,1,2} advance and agree
#   4. confirm offline node3's slot is still ACTIVE (no auto jail/slash/power change)
#   5. stop a second validator (node1) -> 2/4 (50%) < 2/3: chain halts safely
#   6. assert no progress on the live nodes {0,2} over a window (no fork)
#   7. restart node1 and node3 -> chain resumes from last finalized height
#   8. confirm four-node app/validators/next-validators hash agreement
#
# There is NO x/slashing: offline validators must not auto-lose power or jail.

export DRILL="quorum"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
# shellcheck source=scripts/localnet/lib/drill-common.sh
source "$ROOT/scripts/localnet/lib/drill-common.sh"

FAILS=0
note_fail() { FAILS=$((FAILS + 1)); }
trap 'teardown_localnet' EXIT

# assert live nodes do not advance over a window (expected during a safe halt)
assert_no_progress() {
  local window="${1:-15}"; shift
  local n before after
  # plain indexed array (node indices are small integers); avoids bash-4 `-A`.
  local h0=()
  for n in "$@"; do h0[$n]="$(latest_height "$n")"; done
  sleep "$window"
  local ok=1
  for n in "$@"; do
    before="${h0[$n]}"; after="$(latest_height "$n")"
    if ((after > before)); then echo "  node$n advanced $before -> $after during expected halt (FAIL)" >&2; ok=0; fi
  done
  ((ok == 1))
}

main() {
  setup_localnet
  init_evidence
  wait_all_height "${MIN_HEIGHT:-5}"

  # 1) initial agreement
  local initial_h r="PASS" initial_vu="FAIL"
  initial_h="$(latest_height 0)"
  if ! agree_nodes "" "00-initial-4node"; then r="FAIL"; note_fail; fi
  if assert_num_val_updates_exact_at "00-initial-4node" "$initial_h" 0; then initial_vu="$LAST_NUM_VAL_UPDATES"; else note_fail; fi
  record_row "00-initial-4node" "$initial_h" "-" "-" "$(latest_height 0)" "$(active_count)" "$initial_vu" "$r" "$r"

  # 2) stop node3 -> 3/4 continue
  echo "== stopping node3 (3/4 online, expect continue) =="
  local h_before; h_before="$(latest_height 0)"
  stop_node 3
  local cont_result="PASS"
  if wait_online_height $((h_before + 3)) 0 1 2; then echo "  3/4 nodes continued producing blocks: PASS"; else echo "  3/4 did NOT continue: FAIL" >&2; cont_result="FAIL(no-progress)"; note_fail; fi
  local agree3="PASS" cont_vu="FAIL"
  if ! agree_nodes "0 1 2" "01-stop-node3"; then agree3="FAIL"; note_fail; cont_result="$cont_result;AGREE_FAIL"; fi
  if assert_num_val_updates_exact_at "01-stop-node3" $((h_before + 1)) 0 0 1 2; then cont_vu="$LAST_NUM_VAL_UPDATES"; else cont_result="$cont_result;PROVENANCE_FAIL"; note_fail; fi
  record_row "01-stop-node3" "$h_before" "-" "-" "$(latest_height 0)" "$(active_count)" "$cont_vu" "$agree3" "$cont_result"

  # 4) offline-validator-no-auto-power-change: node3's slot4 must stay ACTIVE.
  local st pw off_result="PASS" off_agree="PASS"
  st="$(slot_status 4)"; pw="$(slot_power 4)"
  if [[ "$st" == "SLOT_STATUS_ACTIVE" && "$pw" == "1" ]]; then
    echo "  offline node3 slot4 still ACTIVE power=1 (no auto jail/slash): PASS"
  else
    echo "  offline slot4 changed unexpectedly: status=$st power=$pw (FAIL)" >&2; off_result="FAIL"; note_fail
  fi
  if ! agree_nodes "0 1 2" "02-offline-no-auto-change"; then off_agree="FAIL"; off_result="$off_result;AGREE_FAIL"; note_fail; fi
  record_row "02-offline-no-auto-change" "$(latest_height 0)" "-" "-" "$(latest_height 0)" "$(active_count)" "-" "$off_agree" "$off_result" "4" "$st" "$pw"

  # 5) stop node1 -> 2/4 (50%) < 2/3: safe halt
  echo "== stopping node1 (2/4 online, expect safe halt) =="
  local halt_h; halt_h="$(latest_height 0)"
  stop_node 1
  sleep 6 # let any in-flight round settle
  local halt_result="PASS"
  if assert_no_progress 15 0 2; then echo "  chain halted safely (no progress, no fork) on nodes {0,2}: PASS"; else echo "  chain did NOT halt as expected: FAIL" >&2; halt_result="FAIL(progressed)"; note_fail; fi
  local halt_agree="PASS" halt_vu="FAIL"
  if ! agree_nodes "0 2" "03-stop-node1-halt"; then halt_agree="FAIL"; halt_result="$halt_result;AGREE_FAIL"; note_fail; fi
  if assert_num_val_updates_exact_at "03-stop-node1-halt" "$halt_h" 0 0 2; then halt_vu="$LAST_NUM_VAL_UPDATES"; else halt_result="$halt_result;PROVENANCE_FAIL"; note_fail; fi
  record_row "03-stop-node1-halt" "$halt_h" "-" "-" "$(latest_height 0)" "$(active_count)" "$halt_vu" "$halt_agree" "$halt_result"

  # 7) restart node1 and node3 -> resume
  echo "== restarting node1 and node3 (expect resume) =="
  local resume_from; resume_from="$(latest_height 0)"
  start_node 1
  start_node 3
  local resume_result="PASS"
  if wait_all_height $((resume_from + 3)) ; then echo "  chain resumed; all nodes advanced: PASS"; else echo "  chain did NOT resume: FAIL" >&2; resume_result="FAIL(no-resume)"; note_fail; fi

  # 8) final four-node agreement
  local agree4="PASS" resume_vu="FAIL"
  if ! agree_nodes "" "04-resume-4node"; then agree4="FAIL"; note_fail; resume_result="$resume_result;AGREE_FAIL"; fi
  if assert_num_val_updates_exact_at "04-resume-4node" $((resume_from + 1)) 0; then resume_vu="$LAST_NUM_VAL_UPDATES"; else resume_result="$resume_result;PROVENANCE_FAIL"; note_fail; fi
  record_row "04-resume-4node" "$resume_from" "-" "-" "$(latest_height 0)" "$(active_count)" "$resume_vu" "$agree4" "$resume_result"

  echo "evidence: $SUMMARY"
  if ((FAILS > 0)); then echo "quorum-drill: FAIL ($FAILS failures)" >&2; return 1; fi
  echo "quorum-drill: PASS"
}

main "$@"
