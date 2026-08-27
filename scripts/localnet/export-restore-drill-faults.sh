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
check "progresses, participation lost"   "DEFECT"              "$(classify_restore_outcome 4 4 0 5 agree match lost)"
check "progresses, participation n/a"    "DEFECT"              "$(classify_restore_outcome 4 4 0 5 agree match unreadable)"
check "progresses, participation unread" "DEFECT"              "$(classify_restore_outcome 4 4 0 5 agree match unreadable)"
check "progresses, hashes disagree"      "DEFECT"              "$(classify_restore_outcome 4 4 0 5 disagree match preserved)"
check "progresses, state mismatch"       "DEFECT"              "$(classify_restore_outcome 4 4 0 5 agree mismatch preserved)"
check "alive but no progress"            "DEFECT"              "$(classify_restore_outcome 4 4 0 0 agree match preserved)"
check "alive, progress below the floor"  "DEFECT"              "$(classify_restore_outcome 4 4 0 2 agree match preserved)"
check "4 of 4, everything holds"         "SUPPORTED"           "$(classify_restore_outcome 4 4 0 3 agree match preserved)"
check "restored RPC disagreement"        "DEFECT"              "$(classify_restore_outcome 4 4 0 5 disagree match present)"
check "restored nodes unreachable"       "DEFECT"              "$(classify_restore_outcome 4 4 0 5 unreachable match preserved)"
check "restored nodes still catching up" "DEFECT"              "$(classify_restore_outcome 4 4 0 5 catching_up match preserved)"
check "no common restored height"        "DEFECT"              "$(classify_restore_outcome 4 4 0 5 no_common_height match preserved)"
# A mixed network is never supported: one validator accepting the document while
# another rejects it is a determinism failure, not a degraded success.
check "2 of 4 alive, all else good"      "DEFECT"              "$(classify_restore_outcome 4 2 2 5 agree match preserved)"
check "3 of 4 alive, all else good"      "DEFECT"              "$(classify_restore_outcome 4 3 0 5 agree match preserved)"
check "3 alive + 1 designed refusal"     "DEFECT"              "$(classify_restore_outcome 4 3 1 5 agree match preserved)"
check "all alive but one also refused"   "DEFECT"              "$(classify_restore_outcome 4 4 1 5 agree match preserved)"
# Unevaluated inputs must not be silently accepted as satisfied.
check "n/a agreement is not agreement"   "DEFECT"              "$(classify_restore_outcome 4 4 0 5 n/a match preserved)"
check "n/a state is not a match"         "DEFECT"              "$(classify_restore_outcome 4 4 0 5 agree n/a preserved)"

check "non-numeric input is a defect"    "DEFECT"              "$(classify_restore_outcome 4 '' 0 5 agree match preserved)"
check "zero nodes is a defect"           "DEFECT"              "$(classify_restore_outcome 0 0 0 0 n/a n/a n/a)"
# B4: a height that could not be read is not a height, and must never become 0.
check "progress n/a with a live network"  "DEFECT"             "$(classify_restore_outcome 4 4 0 n/a agree match preserved)"
check "progress empty"                    "DEFECT"             "$(classify_restore_outcome 4 4 0 '' agree match preserved)"
check "progress non-numeric"              "DEFECT"             "$(classify_restore_outcome 4 4 0 unreadable agree match preserved)"
check "progress negative"                 "DEFECT"             "$(classify_restore_outcome 4 4 0 -4 agree match preserved)"
# n/a progress is fine on the all-dead designed-refusal branch: nothing was running.
check "progress n/a on the refusal branch" "REFUSED_AS_DESIGNED" "$(classify_restore_outcome 4 0 4 n/a n/a n/a n/a)"

# ---------------------------------------------------------------------------
group econ "econ_canon — every persisted field is compared, including nested ones"
# B1: the first export proof compared epoch NUMBERS and required entitlements to
# merely exist. Its replacement enumerated top-level fields by hand and so still
# omitted a finalized epoch's embedded reward attribution and its snapshotted
# config — both persisted, both simply not named. Whole objects are compared now,
# and these cases prove the nested ones are actually covered.
#
# The fixture carries a representative EligibleSlotReward and EpochConfigSnapshot;
# without them the nested mutations below would prove nothing.
GOOD='{"epochs":[{"epoch_number":"1","start_height":"1","end_height":"360","minted_emission":"149828400","carry_in":"0","distributable_fees":"0","treasury_amount":"0","reward_pool":"149828400","allocated_amount":"149828400","carry_out":"0","distribution_method":"DISTRIBUTION_METHOD_UNIFORM_ACTIVE_BLOCKS","remainder_policy":"REMAINDER_POLICY_CARRY_FORWARD","cumulative_emitted_after_epoch":"149828400","reward_enabled_blocks":"360",
   "rewards":[{"slot_id":"1","operator_address":"twilight1op1","payout_address":"twilight1pay1","blocks_active":"360","reward_weight":"1","effective_weight":"1","amount":"37457100","claimed":false,"claimed_at_height":"0","epoch_number":"1"},
              {"slot_id":"2","operator_address":"twilight1op2","payout_address":"twilight1pay2","blocks_active":"360","reward_weight":"1","effective_weight":"1","amount":"37457100","claimed":false,"claimed_at_height":"0","epoch_number":"1"}],
   "config":{"snapshot_version":"1","epoch_length_blocks":"360","distribution_method":"DISTRIBUTION_METHOD_UNIFORM_ACTIVE_BLOCKS","remainder_policy":"REMAINDER_POLICY_CARRY_FORWARD","initial_block_subsidy":"416190","halving_mode":"HALVING_MODE_SUPPLY_THRESHOLD","weighted_rewards_enabled":false,"fee_collection_enabled":false,"fee_distribution_enabled":false,"fee_denom":"utwlt","fee_distribution_mode":"FEE_DISTRIBUTION_MODE_NONE","treasury_address":"","emission_treasury_share_bps":"0","fee_treasury_share_bps":"0"}}],
 "entitlements":[{"slot_id":"1","epoch":"1","total_blocks_active":"360","entitlement_amount":"37457100","released_amount":"37457100","payout_address":"twilight1aaa","reward_config_version":"1","slot_status_at_epoch_close":"SLOT_STATUS_ACTIVE","activation_sequence_at_epoch_close":"1","created_height":"360"}],
 "settlements":[{"slot_id":"1","epoch":"1","distribution_mode_version":"1","settlement_mode":"SETTLEMENT_MODE_TRUSTED_AS","settlement_params_version":"1","next_chunk_index":"1","finalized":true,"finalized_height":"367","finalization_reason":"SETTLEMENT_FINALIZATION_REASON_AUTHORIZED_EARLY"}],
 "slots":[{"slot_id":"1","status":"SLOT_STATUS_ACTIVE","consensus_power":"1","operator_address":"twilight1op1","payout_address":"twilight1pay1","settlement_address":"twilight1set1","consensus_pubkey":"cGs=","activation_sequence":"1","activated_height":"1","activation_effective_height":"1","created_height":"1","updated_height":"1","suspended_height":"0","removed_height":"0","reward_weight":"1","current_selection_policy_version":"1","last_selection_policy_update_height":"0","metadata":{"moniker":"node0"}}],
 "balances":{"liability":"262199700","carry":"0","escrow":"262199700"}}'
BASE="$(econ_canon "$GOOD")"
check "the fixture canonicalizes"        "1/1/1/1" "$(econ_counts "$BASE")"
check "the fixture carries nested rewards" "2"     "$(jq -r '.epochs[0].rewards | length' <<<"$BASE")"
check "the fixture carries a config"       "true"  "$(jq -r '(.epochs[0].config | type) == "object"' <<<"$BASE")"
mutated() { econ_canon "$(jq -c "$1" <<<"$GOOD")"; }
differs() { [[ "$(mutated "$1")" != "$BASE" ]] && echo differs || echo same; }

# --- nested EpochReward.rewards[] : the residual B1 gap ---
check "rewards[0].amount"             "differs" "$(differs '.epochs[0].rewards[0].amount="1"')"
check "rewards[0].blocks_active"      "differs" "$(differs '.epochs[0].rewards[0].blocks_active="1"')"
check "rewards[0].payout_address"     "differs" "$(differs '.epochs[0].rewards[0].payout_address="twilight1zzz"')"
check "rewards[0].operator_address"   "differs" "$(differs '.epochs[0].rewards[0].operator_address="twilight1zzz"')"
check "rewards[0].effective_weight"   "differs" "$(differs '.epochs[0].rewards[0].effective_weight="9"')"
check "rewards[0].claimed"            "differs" "$(differs '.epochs[0].rewards[0].claimed=true')"
check "a removed rewards[] entry"     "differs" "$(differs '.epochs[0].rewards = [.epochs[0].rewards[0]]')"
check "an added rewards[] entry"      "differs" "$(differs '.epochs[0].rewards += [.epochs[0].rewards[0] * {slot_id:"3"}]')"
check "an emptied rewards[]"          "differs" "$(differs '.epochs[0].rewards = []')"

# --- nested EpochReward.config : the other residual ---
check "config.initial_block_subsidy"  "differs" "$(differs '.epochs[0].config.initial_block_subsidy="1"')"
check "config.treasury_address"       "differs" "$(differs '.epochs[0].config.treasury_address="twilight1treasury"')"
check "config.distribution_method"    "differs" "$(differs '.epochs[0].config.distribution_method="DISTRIBUTION_METHOD_WEIGHTED"')"
check "config.remainder_policy"       "differs" "$(differs '.epochs[0].config.remainder_policy="REMAINDER_POLICY_BURN"')"
check "config.epoch_length_blocks"    "differs" "$(differs '.epochs[0].config.epoch_length_blocks="720"')"
check "config.halving_mode"           "differs" "$(differs '.epochs[0].config.halving_mode="HALVING_MODE_FIXED_INTERVAL"')"
check "config.emission_treasury_bps"  "differs" "$(differs '.epochs[0].config.emission_treasury_share_bps="500"')"
check "a removed config"              "differs" "$(differs 'del(.epochs[0].config)')"

# --- top-level epoch economics ---
check "finalized-epoch economics"     "differs" "$(differs '.epochs[0].minted_emission="1"')"
check "epoch carry_out"               "differs" "$(differs '.epochs[0].carry_out="9"')"
check "epoch reward_enabled_blocks"   "differs" "$(differs '.epochs[0].reward_enabled_blocks="359"')"
check "a missing finalized epoch"     "differs" "$(differs '.epochs=[]')"

# --- entitlements, settlements, slots, balances ---
check "entitlement amount"            "differs" "$(differs '.entitlements[0].entitlement_amount="1"')"
check "released amount"               "differs" "$(differs '.entitlements[0].released_amount="0"')"
check "payout address"                "differs" "$(differs '.entitlements[0].payout_address="twilight1zzz"')"
check "total blocks active"           "differs" "$(differs '.entitlements[0].total_blocks_active="1"')"
check "entitlement created height"    "differs" "$(differs '.entitlements[0].created_height="1"')"
check "settlement finalized height"   "differs" "$(differs '.settlements[0].finalized_height="999"')"
check "finalization reason"           "differs" "$(differs '.settlements[0].finalization_reason="DEADLINE"')"
check "next chunk index"              "differs" "$(differs '.settlements[0].next_chunk_index="0"')"
check "settlement finalized flag"     "differs" "$(differs '.settlements[0].finalized=false')"
check "settlement mode"               "differs" "$(differs '.settlements[0].settlement_mode="SETTLEMENT_MODE_OPERATOR_ONLY"')"
check "coreslot status"               "differs" "$(differs '.slots[0].status="SLOT_STATUS_SUSPENDED"')"
check "coreslot power"                "differs" "$(differs '.slots[0].consensus_power="7"')"
# Fields an earlier identity/status/power projection excluded. Leaving the value
# destinations out of an export-preservation proof was the same class of gap as
# the omitted rewards[] and config.
check "coreslot payout_address"       "differs" "$(differs '.slots[0].payout_address="twilight1zzz"')"
check "coreslot settlement_address"   "differs" "$(differs '.slots[0].settlement_address="twilight1zzz"')"
check "coreslot operator_address"     "differs" "$(differs '.slots[0].operator_address="twilight1zzz"')"
check "coreslot consensus_pubkey"     "differs" "$(differs '.slots[0].consensus_pubkey="b3RoZXI="')"
check "coreslot activation_sequence"  "differs" "$(differs '.slots[0].activation_sequence="9"')"
check "coreslot reward_weight"        "differs" "$(differs '.slots[0].reward_weight="5"')"
check "coreslot metadata.moniker"     "differs" "$(differs '.slots[0].metadata.moniker="renamed"')"
check "coreslot activated_height"     "differs" "$(differs '.slots[0].activated_height="99"')"
check "coreslot suspended_height"     "differs" "$(differs '.slots[0].suspended_height="99"')"
check "a missing coreslot"            "differs" "$(differs '.slots=[]')"
check "an extra coreslot"             "differs" "$(differs '.slots += [.slots[0] * {slot_id:"2"}]')"
check "coreslot order"                "same" \
  "$([[ "$(econ_canon "$(jq -c '.slots = [(.slots[0] * {slot_id:"2"}), .slots[0]]' <<<"$GOOD")")" \
     == "$(econ_canon "$(jq -c '.slots = [.slots[0], (.slots[0] * {slot_id:"2"})]' <<<"$GOOD")")" ]] && echo same || echo differs)"
check "outstanding liability"         "differs" "$(differs '.balances.liability="1"')"
check "carry"                         "differs" "$(differs '.balances.carry="1"')"
check "escrow"                        "differs" "$(differs '.balances.escrow="1"')"
check "a missing entitlement"         "differs" "$(differs '.entitlements=[]')"
check "an extra entitlement"          "differs" "$(differs '.entitlements += [.entitlements[0] * {slot_id:"2"}]')"
check "a missing settlement"          "differs" "$(differs '.settlements=[]')"
check "an extra settlement"           "differs" "$(differs '.settlements += [.settlements[0] * {slot_id:"2"}]')"

# --- ordering alone must never matter ---
check "entitlement order"             "same" \
  "$([[ "$(econ_canon "$(jq -c '.entitlements = [(.entitlements[0] * {slot_id:"2"}), .entitlements[0]]' <<<"$GOOD")")" \
     == "$(econ_canon "$(jq -c '.entitlements = [.entitlements[0], (.entitlements[0] * {slot_id:"2"})]' <<<"$GOOD")")" ]] && echo same || echo differs)"
check "settlement order"              "same" \
  "$([[ "$(econ_canon "$(jq -c '.settlements = [(.settlements[0] * {epoch:"2"}), .settlements[0]]' <<<"$GOOD")")" \
     == "$(econ_canon "$(jq -c '.settlements = [.settlements[0], (.settlements[0] * {epoch:"2"})]' <<<"$GOOD")")" ]] && echo same || echo differs)"
check "object key order"              "same" \
  "$([[ "$(econ_canon "$GOOD")" == "$(econ_canon "$(jq -c 'to_entries | reverse | from_entries' <<<"$GOOD")")" ]] && echo same || echo differs)"
check "empty input is refused"        "1" "$(econ_canon "" >/dev/null 2>&1; echo $?)"
check "malformed input is refused"    "1" "$(econ_canon '{oops' >/dev/null 2>&1; echo $?)"

# ---------------------------------------------------------------------------
group participation "preservation is answered from the artifact, not the restart"
# B3: asking a restarted chain whether it has participation can only ever report
# freshly accrued counters, which would conceal the very loss being tested for.
check "non-zero in, no field out, is lost"  "lost"       "$(participation_preservation 4 absent)"
check "non-zero in, field present"          "preserved"  "$(participation_preservation 4 present)"
check "one slot is still non-zero"          "lost"       "$(participation_preservation 1 absent)"
check "nothing to preserve"                 "n/a"        "$(participation_preservation 0 absent)"
check "unreadable capture"                  "unreadable" "$(participation_preservation '' absent)"
check "non-numeric capture"                 "unreadable" "$(participation_preservation many present)"
# The full B3 case: participation lost from the artifact, restored chain later
# generates fresh counters, and it is STILL a defect.
check "lost + fresh counters is a defect"   "DEFECT" \
  "$(classify_restore_outcome 4 4 0 9 agree match "$(participation_preservation 4 absent)")"

# ---------------------------------------------------------------------------
group ports "the restored agreement check queries the RESTORED network"
# The defect this closes: agree.sh derives endpoints as 26657 + i*100 from the
# ordinary localnet home, so calling it here checked a network that is stopped by
# the time the restore runs. A healthy restored continuation could never have
# satisfied it.
check "node 0 uses the restore base"   "27657" "$(restore_rpc_port 0)"
check "node 1"                         "27757" "$(restore_rpc_port 1)"
check "node 2"                         "27857" "$(restore_rpc_port 2)"
check "node 3"                         "27957" "$(restore_rpc_port 3)"
check "the base is not the ordinary one" "differs" \
  "$([[ "$RESTORE_RPC_BASE" == "26657" ]] && echo same || echo differs)"
check "no restored port collides with the localnet series" "none" \
  "$(for i in 0 1 2 3; do for j in 0 1 2 3 4; do
       [[ "$(restore_rpc_port $i)" == "$((26657 + j * 100))" ]] && echo collision; done; done | head -1 | grep -c collision | sed 's/^0$/none/')"
# With nothing listening on the restore ports the helper must report the reason
# rather than silently reading agreement from somewhere else. It ASSIGNS rather
# than prints, so a caller using $( ) would lose everything it recorded.
restore_agreement 4
check "nothing listening is not agreement" "unreachable" "$RESTORE_AGREEMENT_RESULT"
restore_agreement 0
check "zero nodes is not agreement"        "unreachable" "$RESTORE_AGREEMENT_RESULT"

# ---------------------------------------------------------------------------
group agreement "restore_agreement, every branch, against the shipped helper"
# B5: the healthy branch was dead code — a healthy node reports catching_up as a
# BOOLEAN false, and jq's // treats false as the alternate case, so `// "true"`
# turned every healthy node into "catching up". Nothing executed this branch, so
# nothing caught it. These fixtures drive the real helper by injecting its fetch.
FIX_MODE=""
restore_rpc_get() {
  local n="$1" path="$2"
  case "$FIX_MODE" in
    unreachable) return 0 ;;
    malformed)   [[ "$path" == "/status" ]] && echo '{"result":{"sync_info":{"catching_up":"false","latest_block_height":"100"}}}'; return 0 ;;
    missingfield)[[ "$path" == "/status" ]] && echo '{"result":{"sync_info":{"latest_block_height":"100"}}}'; return 0 ;;
    catchingup)  [[ "$path" == "/status" ]] && printf '{"result":{"sync_info":{"catching_up":%s,"latest_block_height":"100"}}}\n' "$([[ "$n" == "1" ]] && echo true || echo false)"; return 0 ;;
    zeroheight)  [[ "$path" == "/status" ]] && echo '{"result":{"sync_info":{"catching_up":false,"latest_block_height":"0"}}}'; return 0 ;;
  esac
  if [[ "$path" == "/status" ]]; then
    echo '{"result":{"sync_info":{"catching_up":false,"latest_block_height":"100"}}}'; return 0
  fi
  case "$FIX_MODE" in
    badblock) echo '{"result":{"block":{"header":{}}}}' ;;
    disagree) local t=AA; [[ "$n" == "2" ]] && t=ZZ
              printf '{"result":{"block":{"header":{"app_hash":"%s","validators_hash":"%s","next_validators_hash":"%s"}}}}\n' "$t" "$t" "$t" ;;
    *)        echo '{"result":{"block":{"header":{"app_hash":"AA","validators_hash":"BB","next_validators_hash":"CC"}}}}' ;;
  esac
}
ra() { FIX_MODE="$1"; restore_agreement 4; echo "$RESTORE_AGREEMENT_RESULT"; }

check "a healthy network agrees"          "agree"            "$(ra healthy)"
check "one node disagreeing"              "disagree"         "$(ra disagree)"
check "one node still catching up"        "catching_up"      "$(ra catchingup)"
check "catching_up as a string"           "unreadable"       "$(ra malformed)"
check "catching_up absent"                "unreadable"       "$(ra missingfield)"
check "nothing listening"                 "unreachable"      "$(ra unreachable)"
check "no usable common height"           "no_common_height" "$(ra zeroheight)"
check "unreadable block header"           "unreadable"       "$(ra badblock)"
check "zero nodes"                        "unreachable"      "$(FIX_MODE=healthy; restore_agreement 0; echo "$RESTORE_AGREEMENT_RESULT")"

# The evidence must survive the call. It did not when the helper was invoked in
# command substitution: every global it set was discarded with the subshell.
FIX_MODE=healthy; restore_agreement 4
check "the common height survives the call" "100" "$RESTORE_AGREEMENT_HEIGHT"
check "per-node rows survive the call"      "4"   "$(tr ';' '\n' <<<"$RESTORE_AGREEMENT_ROWS" | grep -c '^node')"
check "rows name the common height"         "4"   "$(tr ';' '\n' <<<"$RESTORE_AGREEMENT_ROWS" | grep -c '@100=')"
FIX_MODE=badblock; restore_agreement 4
check "a failed run records no height"      "0"   "$RESTORE_AGREEMENT_HEIGHT"
unset -f restore_rpc_get

# ---------------------------------------------------------------------------
group verdicts "component verdicts are complete sub-proofs, not proxies"
# The specific future this prevents: a node reaches height 1, never synchronizes,
# and the run still reports join=PASS.
#          empty owns  ident synced   after agree
check "the complete chain of custody"   "PASS" "$(join_outcome true  true  true  synced  true  agree)"
check "synced but disagreeing"          "FAIL" "$(join_outcome true  true  true  synced  true  disagree)"
check "agreeing but never synced"       "FAIL" "$(join_outcome true  true  true  stalled true  agree)"
check "not started from empty state"    "FAIL" "$(join_outcome false true  true  synced  true  agree)"
# B6: the process never started — a bind failure — while something already on the
# join port answers with a healthy status and matching hashes.
check "impersonator: no process of ours" "FAIL" "$(join_outcome true  false true  synced  false agree)"
check "impersonator: foreign node id"    "FAIL" "$(join_outcome true  true  false synced  true  agree)"
check "our process died during sync"     "FAIL" "$(join_outcome true  true  true  synced  false agree)"
check "nothing established"              "FAIL" "$(join_outcome false false false stalled false disagree)"

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

# Derived from the test inventory below, per group, so a dropped case names the
# group it went missing from rather than just changing a total.
EXPECTED_REFUSAL=8
EXPECTED_ECON=60
EXPECTED_CLASSIFY=28
EXPECTED_PARTICIPATION=7
EXPECTED_PORTS=8
EXPECTED_AGREEMENT=13
EXPECTED_VERDICTS=13
EXPECTED_ENDTOEND=4
EXPECTED_CHECKS=$(( EXPECTED_REFUSAL + EXPECTED_ECON + EXPECTED_CLASSIFY + EXPECTED_PARTICIPATION \
                  + EXPECTED_PORTS + EXPECTED_AGREEMENT + EXPECTED_VERDICTS + EXPECTED_ENDTOEND ))
echo
PER_GROUP_EXPECTED=("$EXPECTED_REFUSAL" "$EXPECTED_CLASSIFY" "$EXPECTED_ECON" "$EXPECTED_PARTICIPATION" \
                    "$EXPECTED_PORTS" "$EXPECTED_AGREEMENT" "$EXPECTED_VERDICTS" "$EXPECTED_ENDTOEND")
for i in $(seq 0 $GROUP_IDX); do
  if (( GROUP_COUNTS[i] != PER_GROUP_EXPECTED[i] )); then
    echo "export/restore negative tests: FAIL — group ${GROUP_KEYS[$i]} ran ${GROUP_COUNTS[$i]}, the contract is ${PER_GROUP_EXPECTED[$i]}" >&2
    exit 1
  fi
done
if (( TOTAL != EXPECTED_CHECKS )); then
  echo "export/restore negative tests: FAIL — $TOTAL checks ran, the contract is $EXPECTED_CHECKS" >&2
  echo "  (reconcile the contract deliberately; do not fit it to the run)" >&2
  exit 1
fi
if (( FAILED > 0 )); then
  echo "export/restore negative tests: FAIL ($FAILED of $TOTAL)" >&2; exit 1
fi
echo "export/restore negative tests: PASS ($PASSED checks)"
