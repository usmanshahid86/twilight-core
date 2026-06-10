#!/usr/bin/env bash
set -euo pipefail

# Drill 1 + 5: CLI-driven live lifecycle e2e on the four-node localnet, with a
# script-level single-emitter / validator-update provenance guard.
#
# Inspired by twilight-core-slot-experiments scripts/scenario1.sh::collect_evidence,
# but it drives the chain with REAL `nyksd coreslot ...` transactions (no in-app
# CORESLOT_SCENARIO1_PLAN hook, no experiment chain code).
#
# After every action it asserts four-node app/validators/next-validators hash
# agreement (scripts/localnet/agree.sh) and records evidence under
# build/localnet/evidence/<run-id>/lifecycle/.
#
# Provenance guard: for each action it checks the CometBFT num_val_updates at the
# action's effective block against the expected count (0 for no-op blocks, 1 for
# a single set membership change, 2 for an active key rotation). Since staking is
# omitted, any validator update is necessarily a CoreSlot update — there is no
# other emitter.

export DRILL="lifecycle"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
# shellcheck source=scripts/localnet/lib/drill-common.sh
source "$ROOT/scripts/localnet/lib/drill-common.sh"

FAILS=0
note_fail() { FAILS=$((FAILS + 1)); }

trap 'teardown_localnet' EXIT

# run_action <label> <expect_code> <expect_vu|-> <eff_offset> <submit_fn> -- <subcmd...>
run_action() {
  local label="$1" expect_code="$2" expect_vu="$3" eff_off="$4" submit="$5"
  shift 5
  [[ "${1:-}" == "--" ]] && shift
  local hb result="PASS" nvu="n/a"
  hb="$(latest_height 0)"

  "$submit" "$@"
  local code="$LAST_TXCODE" hash="$LAST_TXHASH"

  if [[ "$expect_code" == "0" && "$code" != "0" ]]; then
    result="FAIL(code=$code)"; note_fail
  elif [[ "$expect_code" != "0" && "$code" == "0" ]]; then
    result="FAIL(expected-reject)"; note_fail
  fi

  local incH effH
  incH="$(tx_height "$hash")"
  if [[ -n "$incH" ]]; then
    effH=$((incH + eff_off))
    wait_all_height $((effH + 1)) || true
    if [[ "$expect_vu" != "-" ]]; then
      if assert_num_val_updates_exact_at "$label" "$effH" "$expect_vu"; then
        nvu="$LAST_NUM_VAL_UPDATES"
      else
        nvu="FAIL"; result="$result;FAIL(num_val_updates want $expect_vu)"; note_fail
      fi
    fi
  elif [[ "$expect_code" == "0" ]]; then
    result="$result;FAIL(missing-inclusion-height)"; note_fail
  fi

  local agree="PASS"
  if ! agree_nodes "" "$label"; then agree="FAIL"; result="$result;AGREE_FAIL"; note_fail; fi

  record_row "$label" "$hb" "$hash" "$code" "$(latest_height 0)" "$(active_count)" "${nvu:-n/a}" "$agree" "$result"
}

main() {
  setup_localnet
  init_evidence
  wait_all_height "${MIN_HEIGHT:-4}"

  echo "== authority=$(authority_addr)  emergency=$(emergency_addr) =="
  echo "== initial active set =="
  local initial_h initial_agree="PASS" initial_result="PASS" initial_vu="FAIL"
  initial_h="$(latest_height 0)"
  if assert_num_val_updates_exact_at "00-initial" "$initial_h" 0; then initial_vu="$LAST_NUM_VAL_UPDATES"; else initial_result="FAIL(provenance)"; note_fail; fi
  if agree_nodes "" "00-initial"; then echo "  initial 4-node agreement: PASS"; else echo "  initial 4-node agreement: FAIL"; initial_agree="FAIL"; initial_result="$initial_result;AGREE_FAIL"; note_fail; fi
  record_row "00-initial" "$initial_h" "-" "-" "$(latest_height 0)" "$(active_count)" "$initial_vu" "$initial_agree" "$initial_result"

  # Provenance baseline: a quiet block (no lifecycle tx) must have zero updates.
  local quietH; quietH="$(latest_height 0)"
  wait_all_height $((quietH + 1)) || true
  if ! assert_num_val_updates_exact_at "00-quiet-block" "$quietH" 0; then
    echo "  quiet-block provenance FAIL at height $quietH" >&2
    note_fail
  fi

  # New slot operator + consensus key (operator field needs no on-chain account).
  "$BIN" keys add newop5 $KEYRING --home "$(node_home 0)" >/dev/null 2>&1 || true
  local newop; newop="$("$BIN" keys show newop5 -a $KEYRING --home "$(node_home 0)")"
  local k5; k5="$("$ROOT/scripts/localnet/gen-consensus-key.sh" slot5key | cut -f1)"
  local sid; sid="$(next_slot_id)" # expected new slot id (5)

  #  step           label                 code vu  eff submit            args
  run_action "01-register-slot$sid"   0 0 0 submit_authority -- register "$newop" "$newop" "$k5" "core5"
  run_action "02-activate-slot$sid"   0 1 0 submit_authority -- activate "$sid"
  run_action "03-inactivate-slot4"    0 1 0 submit_authority -- inactivate 4 "maintenance"
  run_action "04-reactivate-slot4"    0 1 0 submit_authority -- activate 4
  run_action "05-suspend-slot$sid"    0 1 0 submit_emergency -- suspend "$sid" "evidence" "incident-001"
  run_action "06-reactivate-slot$sid" 0 1 0 submit_authority -- activate "$sid"
  run_action "07-inactivate-slot$sid" 0 1 0 submit_authority -- inactivate "$sid" "decommission"
  # remove of a NON-active slot has no validator-set effect -> 0 updates expected
  run_action "08-remove-slot$sid"     0 0 0 submit_authority -- remove "$sid" "decommission"
  # active key rotation: delayed by KeyRotationDelayBlocks(=1); old@0 + new@power
  local k1; k1="$("$ROOT/scripts/localnet/gen-consensus-key.sh" slot1rot | cut -f1)"
  run_action "09-rotate-slot1-key"    0 2 1 submit_authority -- rotate-key 1 "$k1"

  echo ""
  echo "== final active set =="
  q active 2>/dev/null | jq -r '.slots[] | "  slot \(.slot_id) \(.status) power=\(.consensus_power) op=\(.operator_address)"' 2>/dev/null || true
  echo "evidence: $SUMMARY"

  if ((FAILS > 0)); then
    echo "lifecycle-e2e: FAIL ($FAILS check failures)" >&2
    return 1
  fi
  echo "lifecycle-e2e: PASS"
}

main "$@"
