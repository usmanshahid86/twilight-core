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

run_checker() { # run_checker <genesis> [extra env assignments...] -> writes $WORK/out, returns exit code
  local g="$1"; shift
  local rc=0
  env GC_CHAIN_ID="$CHAIN_ID" GC_ACTIVE_SLOTS=2 "$@" \
    "$CHECKER" "$g" --bin "$BIN" >"$WORK/out" 2>&1 || rc=$?
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

# ---- summary ---------------------------------------------------------------------------------
echo
printf '\033[1mcheck-genesis-faults\033[0m  %d cases, %d failed\n' "$CASES" "$FAILED"
if (( FAILED > 0 )); then
  printf '\033[31mthe verifier has dead checks\033[0m\n'
  exit 1
fi
printf '\033[32mevery check fires on its own fault\033[0m\n'
