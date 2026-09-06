#!/usr/bin/env bash
#
# check-genesis-faults.sh — prove that check-genesis.sh actually checks.
#
# A verifier that stops verifying does not go quiet, it goes GREEN. That is the
# worst failure mode available to it, and it is not hypothetical: three separate
# bugs in the first draft of check-genesis.sh each disabled real checks while the
# script still reported success.
#
#   - `jq -e` sets exit status 1 when the OUTPUT VALUE is false, so every
#     boolean-false field read as missing.
#   - Slot status was matched as CORE_SLOT_STATUS_ACTIVE; the enum is
#     SLOT_STATUS_ACTIVE, so a correct genesis counted ZERO active slots.
#   - The consensus path was written `.consensus_params.block`; it is
#     `.consensus.params.block`, so the max_gas check read nothing at all.
#
# Every one of those fails toward a confident wrong answer. So each check gets a
# fault that must make it fire.
#
# THE CONTRACT IS AN EXACT SET COMPARISON over stable check IDs:
#
#     declared  ==  exercised  ==  targeted
#
# Prose is matched nowhere. Substring matching on labels previously let one
# check's pattern be satisfied by ANOTHER check's output — three snapshot mirror
# checks looked covered while no fault targeted them, and exact ids surfaced that
# the moment they were introduced.
#
# `declared` is a STATIC list in check-genesis.sh rather than something derived
# from a run, because a coverage mechanism built only from what executed cannot
# distinguish "no fault triggers this" from "this can no longer fire at all", and
# the second is the dangerous one.
#
# THE COVERAGE CLAIM IS ENFORCED, NOT ASSERTED. This file used to say every check
# had a fault case while 29 of them did not, and nine live checks could be deleted
# outright with it still reporting "every check fires on its own fault". The list
# of checks is now DERIVED from what the checker actually printed across every run
# below, and a check with no matching fault case fails this suite.
#
# THE ASSERTION IS SPECIFIC, DELIBERATELY. It is not enough that the checker
# exited non-zero — a mutation that broke something unrelated would satisfy that
# while the intended check stayed dead. Each case names the check it must see
# fail, and a non-zero exit without that check named is itself a failure.
#
# The base genesis is BUILT WITH THE REAL BINARY rather than committed as a
# fixture. A fixture drifts from the schema the chain actually emits, and a
# faults suite testing a stale shape is the same silent-green problem one level
# up.
#
# No chain is started. This needs only the binary, jq, and a temp directory.
#
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHECKER="$ROOT/scripts/check-genesis.sh"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
# Built FRESH into the work directory unless a binary is named explicitly, the way
# check-cli-surface.sh does. Reusing build/twilightd would let a stale local build
# decide what the baseline genesis looks like, so the suite would be validating
# yesterday's schema while reporting on today's — the same silent-staleness this
# script exists to prevent, one level up.
BIN="${BIN:-}"

CASES=0; FAILED=0
pass() { CASES=$((CASES+1)); printf '  \033[32mok\033[0m    %s\n' "$1"; }
fail() { CASES=$((CASES+1)); FAILED=$((FAILED+1)); printf '  \033[31mBAD\033[0m   %s\n' "$1"; [[ -n "${2:-}" ]] && printf '        %s\n' "$2"; }
abort() { printf '\033[31m%s\033[0m\n' "$1" >&2; exit 2; }

command -v jq >/dev/null || abort "jq is required"
[[ -f "$CHECKER" ]] || abort "checker not found: $CHECKER"
if [[ -n "$BIN" ]]; then
  [[ -x "$BIN" ]] || abort "BIN is set but not executable: $BIN"
else
  BIN="$WORK/twilightd"
  echo "==> building a fresh binary"
  (cd "$ROOT" && go build -o "$BIN" ./cmd/twilightd) || abort "could not build the binary"
fi

CHAIN_ID="twilight-faults-1"

# ---- a genesis that must be COMPLETE and VALID ------------------------------------------
#
# Every mutation below is measured against this. If the baseline itself did not
# pass, each mutation would "fail" for a reason that has nothing to do with the
# check it claims to prove, and the whole suite would be theatre.
echo "==> building the baseline genesis with the real binary"
HOME_DIR="$WORK/home"
"$BIN" init faults-probe --chain-id "$CHAIN_ID" --home "$HOME_DIR" >/dev/null 2>&1 \
  || abort "twilightd init failed"

for n in op1 pay1 set1 op2 pay2 set2 auth eauth; do
  "$BIN" keys add "$n" --keyring-backend test --home "$HOME_DIR" >/dev/null 2>&1 \
    || abort "could not create key $n"
done
addr() { "$BIN" keys show "$1" -a --keyring-backend test --home "$HOME_DIR" 2>/dev/null; }

VK1="$WORK/vk1"; VK2="$WORK/vk2"
"$BIN" init v1 --chain-id "$CHAIN_ID" --home "$VK1" >/dev/null 2>&1
"$BIN" init v2 --chain-id "$CHAIN_ID" --home "$VK2" >/dev/null 2>&1
PK1="$("$BIN" comet show-validator --home "$VK1" | jq -r .key)"
PK2="$("$BIN" comet show-validator --home "$VK2" | jq -r .key)"
[[ -n "$PK1" && -n "$PK2" ]] || abort "could not read consensus pubkeys"

"$BIN" coreslot-genesis set-authorities "$(addr auth)" "$(addr eauth)" --home "$HOME_DIR" >/dev/null 2>&1 \
  || abort "set-authorities failed"
"$BIN" coreslot-genesis add "$(addr op1)" "$(addr pay1)" "$(addr set1)" "$PK1" node-1 --home "$HOME_DIR" >/dev/null 2>&1 \
  || abort "adding slot 1 failed"
"$BIN" coreslot-genesis add "$(addr op2)" "$(addr pay2)" "$(addr set2)" "$PK2" node-2 --home "$HOME_DIR" >/dev/null 2>&1 \
  || abort "adding slot 2 failed"

GOOD="$WORK/genesis.good.json"
jq '.consensus.params.block.max_gas="50000000" | .app_state.coreslot.params.min_active_slots="2"' \
  "$HOME_DIR/config/genesis.json" >"$GOOD" || abort "could not finish the baseline genesis"

# Every label the checker has ever printed, across the baseline and every mutant.
# This is what the coverage assertion at the end is derived from, so a check added
# to check-genesis.sh with no fault case here is DETECTED rather than assumed.
LABELS="$WORK/labels.all"
WANTS="$WORK/wants.all"
: >"$LABELS"; : >"$WANTS"

# Collect the stable IDs the checker emitted, not its prose. Prose is reworded;
# ids are the contract.
record_labels() {
  sed -e 's/\x1b\[[0-9;]*m//g' "$WORK/out" 2>/dev/null \
    | awk '/^  (PASS|FAIL)  \[/{ sub(/^  (PASS|FAIL)  \[/,""); sub(/\].*$/,""); print }' \
    >>"$LABELS" || true
}

run_checker() { # run_checker <genesis> [extra env assignments...] -> writes $WORK/out, returns exit code
  local g="$1"; shift
  local rc=0
  env GC_CHAIN_ID="$CHAIN_ID" GC_ACTIVE_SLOTS=2 GC_MAX_GAS=50000000 GC_MIN_ACTIVE_SLOTS=2 \
      GC_DISTRIBUTION_METHOD=DISTRIBUTION_METHOD_UNIFORM_ACTIVE_BLOCKS "$@" \
    "$CHECKER" "$g" --bin "$BIN" >"$WORK/out" 2>&1 || rc=$?
  record_labels
  return $rc
}

echo
echo "==> baseline must PASS (nothing below means anything otherwise)"
if run_checker "$GOOD"; then
  pass "a complete genesis passes"
else
  printf '\033[31mBASELINE FAILED — the suite cannot run\033[0m\n'
  tail -25 "$WORK/out"
  exit 2
fi

# ---- each check gets a fault that must make IT fire --------------------------------------
#
# mutate <label> <jq-mutation> <check-name-that-must-fail> [extra env...]
mutate() {
  local label="$1" expr="$2" want="$3"; shift 3
  local g="$WORK/mutant.json" rc=0
  jq "$expr" "$GOOD" >"$g" 2>/dev/null || { fail "$label" "the mutation itself could not be applied"; return; }
  if cmp -s "$g" "$GOOD"; then
    fail "$label" "the mutation changed nothing — it would pass for the wrong reason"
    return
  fi
  printf '%s\n' "$want" >>"$WANTS"
  run_checker "$g" "$@" || rc=$?
  if (( rc == 0 )); then
    fail "$label" "checker still exited 0"
    return
  fi
  # EXACT id match, anchored. Substring matching on prose previously let a short
  # pattern be satisfied by a different check's output, so a mutation could look
  # proven while the check it named stayed dead.
  if sed -e 's/\x1b\[[0-9;]*m//g' "$WORK/out" | grep -q "FAIL  \[${want}\]"; then
    pass "$label"
  else
    fail "$label" "checker failed, but not on [${want}] — the intended check may be dead"
  fi
}

echo
echo "==> traps"
mutate "unlimited block gas is caught" \
  '.consensus.params.block.max_gas="-1"' trap.max_gas_finite
mutate "a wrong-but-finite max_gas is caught" \
  '.consensus.params.block.max_gas="40000000"' decision.max_gas
mutate "a display denom in a bank amount is caught" \
  '.app_state.bank.supply=[{denom:"twlt",amount:"1"}]' trap.display_denom_leak
mutate "a treasury share with no address is caught" \
  '.app_state.rewards.params.emission_treasury_share_bps="100"
   | .app_state.rewards.reward_config_versions[0].emission_treasury_share_bps="100"' \
  trap.treasury_address_for_share GC_EMISSION_TREASURY_SHARE_BPS=100
mutate "min_active_slots above the active count is caught" \
  '.app_state.coreslot.params.min_active_slots="3"' trap.active_within_bounds GC_MIN_ACTIVE_SLOTS=3

echo
echo "==> mirror consistency (params vs the canonical version that governs)"
mutate "subsidy drift between params and version 1" \
  '.app_state.rewards.params.initial_block_subsidy="500000"' mirror.subsidy \
  GC_INITIAL_BLOCK_SUBSIDY=500000
mutate "treasury-address drift" \
  '.app_state.rewards.reward_config_versions[0].treasury_address="twilight1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq"' \
  mirror.treasury_address
mutate "epoch-length drift" \
  '.app_state.rewards.params.epoch_length_blocks="400"' mirror.epoch_length \
  GC_EPOCH_LENGTH_BLOCKS=400
mutate "epoch anchor not at initial_height" \
  '.app_state.rewards.epoch_config_versions[0].effective_start_height="7"' \
  mirror.epoch_anchor

echo
echo "==> immutable bounds"
mutate "epoch length below the floor" \
  '.app_state.rewards.params.epoch_length_blocks="100"
   | .app_state.rewards.epoch_config_versions[0].epoch_length_blocks="100"' \
  bound.epoch_length GC_EPOCH_LENGTH_BLOCKS=100
mutate "treasury share above the ceiling" \
  '.app_state.rewards.params.emission_treasury_share_bps="6000"
   | .app_state.rewards.reward_config_versions[0].emission_treasury_share_bps="6000"' \
  bound.emission_share GC_EMISSION_TREASURY_SHARE_BPS=6000
mutate "recipients per chunk above the ceiling" \
  '.app_state.mining.settlement_params_versions[0].max_recipients_per_chunk="33"' \
  bound.recipients_per_chunk
mutate "chunks per settlement above the ceiling" \
  '.app_state.mining.settlement_params_versions[0].max_chunks_per_settlement="5"' \
  bound.chunks_per_settlement
mutate "payout floor below the ratified minimum" \
  '.app_state.mining.settlement_params_versions[0].min_recipient_payout_amount="9999"' \
  bound.min_payout
mutate "max_active_slots above the immutable ceiling" \
  '.app_state.coreslot.params.max_active_slots="101"' bound.max_active_slots

echo
echo "==> fresh-genesis invariants"
mutate "a chain that has already emitted" \
  '.app_state.rewards.state.cumulative_emitted="1"' fresh.cumulative_emitted
mutate "a non-empty epoch schedule" \
  '.app_state.rewards.scheduled_epoch_configs=[{effective_epoch:"5",epoch_length_blocks:"720"}]' \
  fresh.sched_epoch_empty
mutate "a second settlement params version" \
  '.app_state.mining.settlement_params_versions += [.app_state.mining.settlement_params_versions[0]]' \
  fresh.settlement_versions_count
mutate "rewards already paused at genesis" \
  '.app_state.rewards.pause_state.current_paused=true' fresh.paused

echo
echo "==> slot status and decisions"
mutate "an inactive slot reduces the active count" \
  '.app_state.coreslot.slots[0].status="SLOT_STATUS_INACTIVE"' trap.active_within_bounds
mutate "an unrecognised slot status is refused, not ignored" \
  '.app_state.coreslot.slots[0].status="SLOT_STATUS_SOMETHING_NEW"' \
  trap.slot_status_known
mutate "the wrong chain-id" '.chain_id="twilight-wrong-1"' decision.chain_id
mutate "self-registration enabled" \
  '.app_state.coreslot.params.allow_self_registration=true' decision.allow_self_registration
mutate "one key holding both authority roles" \
  '.app_state.coreslot.params.emergency_authority=.app_state.coreslot.params.authority' \
  decision.authorities_distinct
mutate "a changed native denom" \
  '.app_state.rewards.params.native_denom="uother"' trap.native_denom

echo
echo "==> fresh-genesis invariants, one fault each"
mutate "an epoch other than the first" \
  '.app_state.rewards.state.current_epoch="2"' fresh.current_epoch
mutate "a carried remainder at genesis" \
  '.app_state.rewards.state.carry_forward_remainder="1"' fresh.carry_forward_remainder
mutate "reward-enabled blocks already accrued" \
  '.app_state.rewards.open_reward_enabled_blocks="5"' fresh.open_reward_blocks
mutate "outstanding entitlement liability at genesis" \
  '.app_state.rewards.outstanding_entitlement_liability="1"' fresh.entitlement_liability
mutate "a params update already queued" \
  '.app_state.rewards.has_pending_params=true' fresh.has_pending_params
mutate "a pause already scheduled" \
  '.app_state.rewards.pause_state.has_pending=true' fresh.pause_pending
mutate "a second epoch config version" \
  '.app_state.rewards.epoch_config_versions += [.app_state.rewards.epoch_config_versions[0]]' \
  fresh.epoch_versions_count
mutate "a second reward config version" \
  '.app_state.rewards.reward_config_versions += [.app_state.rewards.reward_config_versions[0]]' \
  fresh.reward_versions_count
mutate "a non-empty reward schedule" \
  '.app_state.rewards.scheduled_reward_configs=[{effective_epoch:"5"}]' \
  fresh.sched_reward_empty
mutate "a non-empty settlement schedule" \
  '.app_state.mining.scheduled_settlement_params=[{effective_epoch:"5"}]' \
  fresh.sched_settlement_empty
mutate "a non-empty distribution-mode schedule" \
  '.app_state.mining.scheduled_distribution_modes=[{effective_epoch:"5"}]' \
  fresh.sched_distmode_empty
mutate "a non-empty selection-params schedule" \
  '.app_state.mining.scheduled_selection_params=[{effective_epoch:"5"}]' \
  fresh.sched_selection_empty
mutate "a finalized epoch at genesis" \
  '.app_state.rewards.finalized_epochs=[{epoch_number:"1"}]' fresh.finalized_epochs_empty
mutate "an entitlement at genesis" \
  '.app_state.rewards.slot_entitlements=[{slot_id:"1"}]' fresh.slot_entitlements_empty
mutate "a settlement at genesis" \
  '.app_state.mining.settlements=[{slot_id:"1"}]' fresh.settlements_empty

echo
echo "==> the remaining immutable bounds"
mutate "a zero settlement window" \
  '.app_state.mining.settlement_params_versions[0].settlement_window_epochs="0"' \
  bound.settlement_window
mutate "a selection cooldown below the floor" \
  '.app_state.coreslot.params.selection_policy_update_cooldown_blocks="100"' \
  bound.selection_cooldown

echo
echo "==> the third copy: current_epoch_config"
# These three were previously masked: under substring matching, the params-vs-version
# mirror's pattern also matched the snapshot label, so they looked covered while no
# fault targeted them. Exact ids surfaced it.
mutate "snapshot epoch_length drift" \
  '.app_state.rewards.current_epoch_config.epoch_length_blocks="720"' \
  snapshot.epoch_length_blocks
mutate "snapshot subsidy drift" \
  '.app_state.rewards.current_epoch_config.initial_block_subsidy="500000"' \
  snapshot.initial_block_subsidy
mutate "snapshot treasury-address drift" \
  '.app_state.rewards.current_epoch_config.treasury_address="twilight1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq"' \
  snapshot.treasury_address
mutate "snapshot distribution_method drift" \
  '.app_state.rewards.current_epoch_config.distribution_method="DISTRIBUTION_METHOD_SNAPSHOT_UNIFORM"' \
  snapshot.distribution_method
mutate "snapshot treasury-share drift" \
  '.app_state.rewards.current_epoch_config.emission_treasury_share_bps="100"' \
  snapshot.emission_treasury_share_bps
mutate "snapshot fee_denom drift" \
  '.app_state.rewards.current_epoch_config.fee_denom="uother"' \
  snapshot.fee_denom
mutate "snapshot fee-treasury-share drift" \
  '.app_state.rewards.current_epoch_config.fee_treasury_share_bps="100"' \
  snapshot.fee_treasury_share_bps
mutate "snapshot halving_mode drift" \
  '.app_state.rewards.current_epoch_config.halving_mode="HALVING_MODE_UNSPECIFIED"' \
  snapshot.halving_mode
mutate "snapshot remainder_policy drift" \
  '.app_state.rewards.current_epoch_config.remainder_policy="REMAINDER_POLICY_BURN"' \
  snapshot.remainder_policy
mutate "treasury-share drift against the canonical version" \
  '.app_state.rewards.reward_config_versions[0].emission_treasury_share_bps="100"' \
  mirror.emission_share

echo
echo "==> the remaining launch decisions"
mutate "a different active slot count" \
  '.app_state.coreslot.slots += [.app_state.coreslot.slots[0] | .slot_id="3"]' decision.active_slots
mutate "a min_active_slots other than the one decided" \
  '.app_state.coreslot.params.min_active_slots="1"' decision.min_active_slots
mutate "an epoch length other than the one decided" \
  '.app_state.rewards.params.epoch_length_blocks="720"
   | .app_state.rewards.epoch_config_versions[0].epoch_length_blocks="720"
   | .app_state.rewards.current_epoch_config.epoch_length_blocks="720"' decision.epoch_length
mutate "a different max supply" \
  '.app_state.rewards.params.max_supply="42000000000000"' decision.max_supply
mutate "a subsidy other than the one decided" \
  '.app_state.rewards.params.initial_block_subsidy="500000"
   | .app_state.rewards.reward_config_versions[0].initial_block_subsidy="500000"
   | .app_state.rewards.current_epoch_config.initial_block_subsidy="500000"' decision.subsidy
mutate "a distribution method other than the one decided" \
  '.app_state.rewards.params.distribution_method="DISTRIBUTION_METHOD_WEIGHTED_ACTIVE_BLOCKS"
   | .app_state.rewards.current_epoch_config.distribution_method="DISTRIBUTION_METHOD_WEIGHTED_ACTIVE_BLOCKS"' \
  decision.distribution_method
mutate "a treasury share other than the one decided" \
  '.app_state.rewards.params.emission_treasury_share_bps="200"
   | .app_state.rewards.reward_config_versions[0].emission_treasury_share_bps="200"
   | .app_state.rewards.current_epoch_config.emission_treasury_share_bps="200"' \
  decision.emission_share
mutate "a treasury address other than the one decided" \
  '.app_state.rewards.params.treasury_address="twilight1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq"
   | .app_state.rewards.reward_config_versions[0].treasury_address="twilight1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq"
   | .app_state.rewards.current_epoch_config.treasury_address="twilight1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq"' \
  decision.treasury_address
mutate "a declared block time that nothing else enforces" \
  '.app_state.rewards.params.target_block_time_seconds="10"' decision.target_block_time
mutate "a changed fee denom" \
  '.app_state.rewards.params.fee_denom="uother"
   | .app_state.rewards.current_epoch_config.fee_denom="uother"' trap.fee_denom
mutate "a malformed authority address" \
  '.app_state.coreslot.params.authority="not-an-address"' decision.authority_shape
mutate "a malformed emergency authority address" \
  '.app_state.coreslot.params.emergency_authority="not-an-address"' \
  decision.emergency_authority_shape
mutate "something only the chain itself rejects" \
  '.app_state.coreslot.slots[0].slot_id="0"' native.validate

# ---- the checker must refuse to guess ------------------------------------------------------
#
# A decision it invented is a decision nobody made, so an unset required input has
# to abort rather than default.
echo
echo "==> required decisions must abort, not default"
# All four required inputs, each omitted in turn while the other three are supplied,
# so the abort is attributable to THAT variable rather than to a generally broken
# invocation.
#
# GC_MAX_GAS matters most here. It has no shipped default — `twilightd init` writes
# -1 — so a default would be this script inventing a ratification decision that
# #160, #107 and #167 all say has not been made. A run passing because the caller
# forgot the variable is indistinguishable from one passing because the value was
# ratified, which is the exact confusion this tool exists to prevent.
for var in GC_CHAIN_ID GC_ACTIVE_SLOTS GC_MAX_GAS GC_MIN_ACTIVE_SLOTS; do
  rc=0
  env -u "$var" \
    $([[ "$var" != GC_CHAIN_ID ]]         && echo "GC_CHAIN_ID=$CHAIN_ID") \
    $([[ "$var" != GC_ACTIVE_SLOTS ]]     && echo "GC_ACTIVE_SLOTS=2") \
    $([[ "$var" != GC_MAX_GAS ]]          && echo "GC_MAX_GAS=50000000") \
    $([[ "$var" != GC_MIN_ACTIVE_SLOTS ]] && echo "GC_MIN_ACTIVE_SLOTS=2") \
    "$CHECKER" "$GOOD" --bin "$BIN" >"$WORK/out" 2>&1 || rc=$?
  if (( rc != 0 )); then pass "unset $var aborts"
  else fail "unset $var aborts" "checker ran anyway and exited 0"; fi
done

# Running without the chain's own validator must not be reported as a clean pass.
rc=0
env GC_CHAIN_ID="$CHAIN_ID" GC_ACTIVE_SLOTS=2 GC_MAX_GAS=50000000 GC_MIN_ACTIVE_SLOTS=2 \
  "$CHECKER" "$GOOD" >"$WORK/out" 2>&1 || rc=$?
if (( rc != 0 )); then pass "omitting --bin is not a clean pass"
else fail "omitting --bin is not a clean pass" "checker exited 0 without running twilightd validate"; fi

# ---- the coverage contract: declared == exercised == targeted -----------------------------
#
# Three sets, compared exactly. Each catches something the others cannot:
#
#   DECLARED    the static list in check-genesis.sh, via --list-checks.
#   EXERCISED   ids the checker actually emitted across every run above.
#   TARGETED    ids named by a fault case here.
#
#   declared \ exercised  a check that can no longer fire — UNREACHABLE. A
#                         coverage mechanism derived only from what ran cannot
#                         see this at all, which is why declared is static.
#   exercised \ declared  an id emitted but not declared (the checker also
#                         aborts on this itself).
#   declared \ targeted   a check nobody wrote a fault for.
#   targeted \ declared   a fault case naming a check that no longer exists.
echo
echo "==> coverage contract: declared == exercised == targeted"
"$CHECKER" --list-checks | LC_ALL=C sort -u >"$WORK/declared"
LC_ALL=C sort -u "$LABELS" >"$WORK/exercised"
LC_ALL=C sort -u "$WANTS"  >"$WORK/targeted"

report_diff() { # report_diff <label> <only-in-A-file> <A> <B>
  local n; n="$(grep -c . "$2" || true)"
  if [[ "$n" == "0" ]]; then return 0; fi
  printf '  \033[31mBAD\033[0m   %s (%s):\n' "$1" "$n"
  sed 's/^/          /' "$2"
  return 1
}
COVER_OK=1
comm -23 "$WORK/declared"  "$WORK/exercised" >"$WORK/d_not_e"
comm -13 "$WORK/declared"  "$WORK/exercised" >"$WORK/e_not_d"
comm -23 "$WORK/declared"  "$WORK/targeted"  >"$WORK/d_not_t"
comm -13 "$WORK/declared"  "$WORK/targeted"  >"$WORK/t_not_d"
report_diff "declared but never exercised — UNREACHABLE check" "$WORK/d_not_e" || COVER_OK=0
report_diff "emitted but not declared" "$WORK/e_not_d" || COVER_OK=0
report_diff "declared but no fault case targets it" "$WORK/d_not_t" || COVER_OK=0
report_diff "fault case targets a check that does not exist" "$WORK/t_not_d" || COVER_OK=0
if (( COVER_OK )); then
  pass "all $(grep -c . "$WORK/declared") declared checks are exercised and targeted"
else
  CASES=$((CASES+1)); FAILED=$((FAILED+1))
fi

# ---- summary ---------------------------------------------------------------------------------
echo
printf '\033[1mcheck-genesis-faults\033[0m  %d cases, %d failed\n' "$CASES" "$FAILED"
if (( FAILED > 0 )); then
  printf '\033[31mthe verifier has dead checks\033[0m\n'
  exit 1
fi
printf '\033[32mevery check fires on its own fault\033[0m\n'
