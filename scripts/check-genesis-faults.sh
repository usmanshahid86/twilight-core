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

# Strip colour, take the text after PASS/FAIL, drop the " = value" tail that eq()
# appends, so a label matches the same string a `want` greps for.
record_labels() {
  sed -e 's/\x1b\[[0-9;]*m//g' "$WORK/out" 2>/dev/null \
    | awk '/^  (PASS|FAIL)  /{ sub(/^  (PASS|FAIL)  /,""); sub(/ = .*$/,""); print }' \
    >>"$LABELS" || true
}

run_checker() { # run_checker <genesis> [extra env assignments...] -> writes $WORK/out, returns exit code
  local g="$1"; shift
  local rc=0
  env GC_CHAIN_ID="$CHAIN_ID" GC_ACTIVE_SLOTS=2 "$@" \
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
  if grep -q "FAIL.*${want}" "$WORK/out"; then
    pass "$label"
  else
    fail "$label" "checker failed, but not on '${want}' — the intended check may be dead"
  fi
}

echo
echo "==> traps"
mutate "unlimited block gas is caught" \
  '.consensus.params.block.max_gas="-1"' "block.max_gas is finite"
mutate "a wrong-but-finite max_gas is caught" \
  '.consensus.params.block.max_gas="40000000"' "block.max_gas"
mutate "a display denom in a bank amount is caught" \
  '.app_state.bank.supply=[{denom:"twlt",amount:"1"}]' "no display denom in any bank amount"
mutate "a treasury share with no address is caught" \
  '.app_state.rewards.params.emission_treasury_share_bps="100"
   | .app_state.rewards.reward_config_versions[0].emission_treasury_share_bps="100"' \
  "treasury address present for a non-zero share" GC_EMISSION_TREASURY_SHARE_BPS=100
mutate "min_active_slots above the active count is caught" \
  '.app_state.coreslot.params.min_active_slots="3"' "active slot count within" GC_MIN_ACTIVE_SLOTS=3

echo
echo "==> mirror consistency (params vs the canonical version that governs)"
mutate "subsidy drift between params and version 1" \
  '.app_state.rewards.params.initial_block_subsidy="500000"' "initial_block_subsidy mirrors" \
  GC_INITIAL_BLOCK_SUBSIDY=500000
mutate "treasury-address drift" \
  '.app_state.rewards.reward_config_versions[0].treasury_address="twilight1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq"' \
  "treasury_address mirrors"
mutate "epoch-length drift" \
  '.app_state.rewards.params.epoch_length_blocks="400"' "epoch_length_blocks mirrors" \
  GC_EPOCH_LENGTH_BLOCKS=400
mutate "epoch anchor not at initial_height" \
  '.app_state.rewards.epoch_config_versions[0].effective_start_height="7"' \
  "epoch anchor starts at initial_height"

echo
echo "==> immutable bounds"
mutate "epoch length below the floor" \
  '.app_state.rewards.params.epoch_length_blocks="100"
   | .app_state.rewards.epoch_config_versions[0].epoch_length_blocks="100"' \
  "epoch_length_blocks within" GC_EPOCH_LENGTH_BLOCKS=100
mutate "treasury share above the ceiling" \
  '.app_state.rewards.params.emission_treasury_share_bps="6000"
   | .app_state.rewards.reward_config_versions[0].emission_treasury_share_bps="6000"' \
  "emission_treasury_share_bps <= 5000" GC_EMISSION_TREASURY_SHARE_BPS=6000
mutate "recipients per chunk above the ceiling" \
  '.app_state.mining.settlement_params_versions[0].max_recipients_per_chunk="33"' \
  "max_recipients_per_chunk within"
mutate "chunks per settlement above the ceiling" \
  '.app_state.mining.settlement_params_versions[0].max_chunks_per_settlement="5"' \
  "max_chunks_per_settlement within"
mutate "payout floor below the ratified minimum" \
  '.app_state.mining.settlement_params_versions[0].min_recipient_payout_amount="9999"' \
  "min_recipient_payout_amount >= 10000"
mutate "max_active_slots above the immutable ceiling" \
  '.app_state.coreslot.params.max_active_slots="101"' "max_active_slots <= 100"

echo
echo "==> fresh-genesis invariants"
mutate "a chain that has already emitted" \
  '.app_state.rewards.state.cumulative_emitted="1"' "rewards.state.cumulative_emitted"
mutate "a non-empty epoch schedule" \
  '.app_state.rewards.scheduled_epoch_configs=[{effective_epoch:"5",epoch_length_blocks:"720"}]' \
  "rewards.scheduled_epoch_configs is empty"
mutate "a second settlement params version" \
  '.app_state.mining.settlement_params_versions += [.app_state.mining.settlement_params_versions[0]]' \
  "mining.settlement_params_versions count"
mutate "rewards already paused at genesis" \
  '.app_state.rewards.pause_state.current_paused=true' "rewards.pause_state.current_paused"

echo
echo "==> slot status and decisions"
mutate "an inactive slot reduces the active count" \
  '.app_state.coreslot.slots[0].status="SLOT_STATUS_INACTIVE"' "active slot count within"
mutate "an unrecognised slot status is refused, not ignored" \
  '.app_state.coreslot.slots[0].status="SLOT_STATUS_SOMETHING_NEW"' \
  "every slot status is a known SlotStatus value"
mutate "the wrong chain-id" '.chain_id="twilight-wrong-1"' "chain_id"
mutate "self-registration enabled" \
  '.app_state.coreslot.params.allow_self_registration=true' "allow_self_registration"
mutate "one key holding both authority roles" \
  '.app_state.coreslot.params.emergency_authority=.app_state.coreslot.params.authority' \
  "authority and emergency_authority are distinct"
mutate "a changed native denom" \
  '.app_state.rewards.params.native_denom="uother"' "rewards native_denom"

echo
echo "==> fresh-genesis invariants, one fault each"
mutate "an epoch other than the first" \
  '.app_state.rewards.state.current_epoch="2"' "rewards.state.current_epoch"
mutate "a carried remainder at genesis" \
  '.app_state.rewards.state.carry_forward_remainder="1"' "rewards.state.carry_forward_remainder"
mutate "reward-enabled blocks already accrued" \
  '.app_state.rewards.open_reward_enabled_blocks="5"' "rewards.open_reward_enabled_blocks"
mutate "outstanding entitlement liability at genesis" \
  '.app_state.rewards.outstanding_entitlement_liability="1"' "rewards.outstanding_entitlement_liability"
mutate "a params update already queued" \
  '.app_state.rewards.has_pending_params=true' "rewards.has_pending_params"
mutate "a pause already scheduled" \
  '.app_state.rewards.pause_state.has_pending=true' "rewards.pause_state.has_pending"
mutate "a second epoch config version" \
  '.app_state.rewards.epoch_config_versions += [.app_state.rewards.epoch_config_versions[0]]' \
  "rewards.epoch_config_versions count"
mutate "a second reward config version" \
  '.app_state.rewards.reward_config_versions += [.app_state.rewards.reward_config_versions[0]]' \
  "rewards.reward_config_versions count"
mutate "a non-empty reward schedule" \
  '.app_state.rewards.scheduled_reward_configs=[{effective_epoch:"5"}]' \
  "rewards.scheduled_reward_configs is empty"
mutate "a non-empty settlement schedule" \
  '.app_state.mining.scheduled_settlement_params=[{effective_epoch:"5"}]' \
  "mining.scheduled_settlement_params is empty"
mutate "a non-empty distribution-mode schedule" \
  '.app_state.mining.scheduled_distribution_modes=[{effective_epoch:"5"}]' \
  "mining.scheduled_distribution_modes is empty"
mutate "a non-empty selection-params schedule" \
  '.app_state.mining.scheduled_selection_params=[{effective_epoch:"5"}]' \
  "mining.scheduled_selection_params is empty"
mutate "a finalized epoch at genesis" \
  '.app_state.rewards.finalized_epochs=[{epoch_number:"1"}]' "rewards.finalized_epochs is empty"
mutate "an entitlement at genesis" \
  '.app_state.rewards.slot_entitlements=[{slot_id:"1"}]' "rewards.slot_entitlements is empty"
mutate "a settlement at genesis" \
  '.app_state.mining.settlements=[{slot_id:"1"}]' "mining.settlements is empty"

echo
echo "==> the remaining immutable bounds"
mutate "a zero settlement window" \
  '.app_state.mining.settlement_params_versions[0].settlement_window_epochs="0"' \
  "settlement_window_epochs >= 1"
mutate "a selection cooldown below the floor" \
  '.app_state.coreslot.params.selection_policy_update_cooldown_blocks="100"' \
  "selection_policy_update_cooldown_blocks >= 360"

echo
echo "==> the third copy: current_epoch_config"
mutate "snapshot distribution_method drift" \
  '.app_state.rewards.current_epoch_config.distribution_method="DISTRIBUTION_METHOD_SNAPSHOT_UNIFORM"' \
  "current_epoch_config.distribution_method mirrors params"
mutate "snapshot treasury-share drift" \
  '.app_state.rewards.current_epoch_config.emission_treasury_share_bps="100"' \
  "current_epoch_config.emission_treasury_share_bps mirrors params"
mutate "snapshot fee_denom drift" \
  '.app_state.rewards.current_epoch_config.fee_denom="uother"' \
  "current_epoch_config.fee_denom mirrors params"
mutate "snapshot fee-treasury-share drift" \
  '.app_state.rewards.current_epoch_config.fee_treasury_share_bps="100"' \
  "current_epoch_config.fee_treasury_share_bps mirrors params"
mutate "snapshot halving_mode drift" \
  '.app_state.rewards.current_epoch_config.halving_mode="HALVING_MODE_UNSPECIFIED"' \
  "current_epoch_config.halving_mode mirrors params"
mutate "snapshot remainder_policy drift" \
  '.app_state.rewards.current_epoch_config.remainder_policy="REMAINDER_POLICY_BURN"' \
  "current_epoch_config.remainder_policy mirrors params"
mutate "treasury-share drift against the canonical version" \
  '.app_state.rewards.reward_config_versions[0].emission_treasury_share_bps="100"' \
  "emission_treasury_share_bps mirrors"

echo
echo "==> the remaining launch decisions"
mutate "a different active slot count" \
  '.app_state.coreslot.slots += [.app_state.coreslot.slots[0] | .slot_id="3"]' "active slots"
mutate "a min_active_slots other than the one decided" \
  '.app_state.coreslot.params.min_active_slots="1"' "min_active_slots"
mutate "an epoch length other than the one decided" \
  '.app_state.rewards.params.epoch_length_blocks="720"
   | .app_state.rewards.epoch_config_versions[0].epoch_length_blocks="720"
   | .app_state.rewards.current_epoch_config.epoch_length_blocks="720"' "epoch_length_blocks"
mutate "a different max supply" \
  '.app_state.rewards.params.max_supply="42000000000000"' "max_supply"
mutate "a subsidy other than the one decided" \
  '.app_state.rewards.params.initial_block_subsidy="500000"
   | .app_state.rewards.reward_config_versions[0].initial_block_subsidy="500000"
   | .app_state.rewards.current_epoch_config.initial_block_subsidy="500000"' "initial_block_subsidy"
mutate "a distribution method other than the one decided" \
  '.app_state.rewards.params.distribution_method="DISTRIBUTION_METHOD_WEIGHTED_ACTIVE_BLOCKS"
   | .app_state.rewards.current_epoch_config.distribution_method="DISTRIBUTION_METHOD_WEIGHTED_ACTIVE_BLOCKS"' \
  "distribution_method"
mutate "a treasury share other than the one decided" \
  '.app_state.rewards.params.emission_treasury_share_bps="200"
   | .app_state.rewards.reward_config_versions[0].emission_treasury_share_bps="200"
   | .app_state.rewards.current_epoch_config.emission_treasury_share_bps="200"' \
  "emission_treasury_share_bps"
mutate "a treasury address other than the one decided" \
  '.app_state.rewards.params.treasury_address="twilight1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq"
   | .app_state.rewards.reward_config_versions[0].treasury_address="twilight1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq"
   | .app_state.rewards.current_epoch_config.treasury_address="twilight1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq"' \
  "treasury_address"
mutate "a declared block time that nothing else enforces" \
  '.app_state.rewards.params.target_block_time_seconds="10"' "target_block_time_seconds"
mutate "a changed fee denom" \
  '.app_state.rewards.params.fee_denom="uother"
   | .app_state.rewards.current_epoch_config.fee_denom="uother"' "rewards fee_denom"
mutate "a malformed authority address" \
  '.app_state.coreslot.params.authority="not-an-address"' "authority has a well-formed address"
mutate "a malformed emergency authority address" \
  '.app_state.coreslot.params.emergency_authority="not-an-address"' \
  "emergency_authority has a well-formed address"
mutate "something only the chain itself rejects" \
  '.app_state.coreslot.slots[0].slot_id="0"' "twilightd validate"

# ---- the checker must refuse to guess ------------------------------------------------------
#
# A decision it invented is a decision nobody made, so an unset required input has
# to abort rather than default.
echo
echo "==> required decisions must abort, not default"
for var in GC_CHAIN_ID GC_ACTIVE_SLOTS; do
  rc=0
  if [[ "$var" == "GC_CHAIN_ID" ]]; then
    env -u GC_CHAIN_ID GC_ACTIVE_SLOTS=2 "$CHECKER" "$GOOD" --bin "$BIN" >"$WORK/out" 2>&1 || rc=$?
  else
    env -u GC_ACTIVE_SLOTS GC_CHAIN_ID="$CHAIN_ID" "$CHECKER" "$GOOD" --bin "$BIN" >"$WORK/out" 2>&1 || rc=$?
  fi
  if (( rc != 0 )); then pass "unset $var aborts"
  else fail "unset $var aborts" "checker ran anyway and exited 0"; fi
done

# Running without the chain's own validator must not be reported as a clean pass.
rc=0
env GC_CHAIN_ID="$CHAIN_ID" GC_ACTIVE_SLOTS=2 "$CHECKER" "$GOOD" >"$WORK/out" 2>&1 || rc=$?
if (( rc != 0 )); then pass "omitting --bin is not a clean pass"
else fail "omitting --bin is not a clean pass" "checker exited 0 without running twilightd validate"; fi

# ---- coverage: every check must have a fault case ---------------------------------------------
#
# The header of this file claims each check gets a fault that makes it fire. That
# claim was previously false — 29 of 53 checks had no case, and nine live checks
# could be deleted outright with this suite still reporting "every check fires on
# its own fault", which is precisely the silent-green failure the checker exists to
# prevent, one level up.
#
# So the claim is now ENFORCED rather than asserted. The check list is derived from
# what the checker actually printed across every run above, not from a hand-kept
# list that would drift the moment someone adds a check.
echo
echo "==> every check must have a fault case"
UNCOVERED=0
while IFS= read -r label; do
  [[ -n "$label" ]] || continue
  covered=0
  while IFS= read -r want; do
    [[ -n "$want" ]] || continue
    case "$label" in *"$want"*) covered=1; break ;; esac
  done <"$WANTS"
  if (( covered == 0 )); then
    UNCOVERED=$((UNCOVERED+1))
    printf '  \033[31mBAD\033[0m   no fault case exercises: %s\n' "$label"
  fi
done < <(LC_ALL=C sort -u "$LABELS")
if (( UNCOVERED == 0 )); then
  pass "all $(LC_ALL=C sort -u "$LABELS" | grep -c . ) checks have a fault case"
else
  CASES=$((CASES+1)); FAILED=$((FAILED+1))
  printf '  \033[31mBAD\033[0m   %d check(s) have no fault case\n' "$UNCOVERED"
fi

# ---- summary ---------------------------------------------------------------------------------
echo
printf '\033[1mcheck-genesis-faults\033[0m  %d cases, %d failed\n' "$CASES" "$FAILED"
if (( FAILED > 0 )); then
  printf '\033[31mthe verifier has dead checks\033[0m\n'
  exit 1
fi
printf '\033[32mevery check fires on its own fault\033[0m\n'
