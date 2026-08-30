#!/usr/bin/env bash
#
# check-genesis.sh — verify a genesis file against the launch decisions before it
# is distributed to operators.
#
# This exists because most of what matters in genesis has NO tooling behind it.
# `twilightd init` writes defaults, `coreslot-genesis` writes authorities and
# slots, and everything else — every rewards parameter, every mining settlement
# parameter, every coreslot parameter, and block.max_gas — is a hand edit of the
# JSON. A hand edit is exactly the thing that needs checking back.
#
# THREE LAYERS, and they are deliberately separate:
#
#   1. NATIVE      the chain's own validators, which are authoritative
#   2. INVARIANT   rules that are true of any correct fresh genesis
#   3. DECISION    the values THIS launch chose, supplied by the caller
#
# A decision cannot be inferred from the file — checking a file against itself
# proves nothing. Required decisions must be supplied or this aborts. It never
# guesses, and it never treats "the default was left in place" as a decision.
#
# Usage:
#   GC_CHAIN_ID=twilight-testnet-1 GC_ACTIVE_SLOTS=2 \
#   GC_MAX_GAS=<ratified> GC_MIN_ACTIVE_SLOTS=2 \
#   GC_DISTRIBUTION_METHOD=DISTRIBUTION_METHOD_UNIFORM_ACTIVE_BLOCKS \
#     scripts/check-genesis.sh path/to/genesis.json [--bin build/twilightd]
#
set -euo pipefail

if [[ "${1:-}" == "--list-checks" ]]; then LIST_ONLY=1; else LIST_ONLY=0; fi
GENESIS="${1:-}"
BIN=""
shift || true
while (( $# )); do
  case "$1" in
    --bin)
      [[ $# -ge 2 ]] || { echo "--bin needs a path to the twilightd binary" >&2; exit 2; }
      BIN="$2"; shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

(( LIST_ONLY )) || [[ -n "$GENESIS" ]] || { echo "usage: $0 <genesis.json> [--bin <twilightd>]" >&2; exit 2; }
(( LIST_ONLY )) || [[ -f "$GENESIS" ]] || { echo "no such genesis file: $GENESIS" >&2; exit 2; }
(( LIST_ONLY )) || command -v jq >/dev/null || { echo "jq is required" >&2; exit 2; }

# THE DECLARED CHECK SET.
#
# Every check this script can emit, listed STATICALLY. This is the contract the
# fault suite holds the script to, and it is static precisely so that a check
# which has become UNREACHABLE still appears here: a coverage mechanism derived
# only from what ran cannot tell "no fault triggers this" from "this can no
# longer fire at all", and the second is the dangerous one.
#
# Three sets must be equal, and check-genesis-faults.sh asserts it:
#
#   declared (this list)  ==  exercised (emitted across all runs)  ==  targeted (fault cases)
#
# Adding a check without adding it here is a hard error at runtime. Adding it here
# without a fault case fails the suite. Making one unreachable fails the suite.
CHECK_IDS=(
  native.validate
  fresh.current_epoch fresh.cumulative_emitted fresh.carry_forward_remainder
  fresh.open_reward_blocks fresh.entitlement_liability fresh.has_pending_params
  fresh.paused fresh.pause_pending
  fresh.epoch_versions_count fresh.reward_versions_count fresh.settlement_versions_count
  fresh.sched_epoch_empty fresh.sched_reward_empty fresh.sched_settlement_empty
  fresh.sched_distmode_empty fresh.sched_selection_empty
  fresh.finalized_epochs_empty fresh.slot_entitlements_empty fresh.settlements_empty
  mirror.epoch_length mirror.subsidy mirror.emission_share mirror.treasury_address
  mirror.epoch_anchor
  snapshot.epoch_length_blocks snapshot.initial_block_subsidy snapshot.treasury_address
  snapshot.emission_treasury_share_bps snapshot.distribution_method snapshot.halving_mode
  snapshot.remainder_policy snapshot.fee_denom snapshot.fee_treasury_share_bps
  bound.epoch_length bound.emission_share bound.recipients_per_chunk
  bound.chunks_per_settlement bound.min_payout bound.settlement_window
  bound.max_active_slots bound.selection_cooldown
  trap.treasury_address_for_share trap.slot_status_known trap.active_within_bounds
  trap.native_denom trap.fee_denom trap.display_denom_leak trap.max_gas_finite
  decision.chain_id decision.max_gas decision.active_slots decision.min_active_slots
  decision.epoch_length decision.max_supply decision.subsidy decision.distribution_method
  decision.emission_share decision.treasury_address decision.allow_self_registration
  decision.target_block_time decision.authority_shape
  decision.emergency_authority_shape decision.authorities_distinct
)

# LIST_ONLY is captured before the argument shift above; re-testing $1 here would
# read an already-consumed argument.
if (( LIST_ONLY )); then
  printf '%s\n' "${CHECK_IDS[@]}"
  exit 0
fi

# An id emitted but not declared is a bug in THIS script, not in the genesis, so
# it aborts rather than being reported as a check result.
declared_id() {
  local want="$1" have
  for have in "${CHECK_IDS[@]}"; do [[ "$have" == "$want" ]] && return 0; done
  return 1
}


# ---- decisions the caller must state ------------------------------------------------------
#
# THE RULE FOR WHAT MAY BE DEFAULTED, stated so it can be argued with:
#
#   Default only to a value the CODEBASE ITSELF establishes. Require everything
#   else.
#
# Defaulting to "you did not change what the binary ships" is verifiable from
# source, and a reader can check it. Defaulting to a number chosen in a discussion
# is this script inventing a launch decision — which is exactly what it exists to
# stop, and the failure would be silent: a run that passes because the caller
# omitted a variable reads identically to one that passes because the value was
# ratified.
#
# Required, no default, because the codebase establishes no value for them:
#
#   GC_CHAIN_ID        no default exists anywhere.
#   GC_ACTIVE_SLOTS    a genesis cannot state how many slots were INTENDED.
#   GC_MAX_GAS         `twilightd init` writes -1 (cometbft types/params.go).
#                      Any finite value is a ratification decision, and as of
#                      #160, #107 and #167 no value is ratified. This script must
#                      never be the thing that supplies one.
#   GC_MIN_ACTIVE_SLOTS  the shipped default is 1 (coreslot validation.go);
#                      anything else is a deployment choice about liveness.
#   GC_DISTRIBUTION_METHOD  the shipped default IS the likely answer, but the value
#                      is FROZEN at genesis — uniform can never become weighted
#                      without an upgrade — so accepting it must be a statement
#                      rather than a silence.
: "${GC_CHAIN_ID:?set GC_CHAIN_ID to the chain-id this launch decided on}"
: "${GC_ACTIVE_SLOTS:?set GC_ACTIVE_SLOTS to the number of slots that must be ACTIVE at genesis}"
: "${GC_MAX_GAS:?set GC_MAX_GAS to the ratified block max_gas for this launch — twilightd init writes -1 and no value is ratified in-repo}"
: "${GC_MIN_ACTIVE_SLOTS:?set GC_MIN_ACTIVE_SLOTS to the active-slot floor this launch decided on — the shipped default is 1}"
: "${GC_DISTRIBUTION_METHOD:?set GC_DISTRIBUTION_METHOD — it is frozen at genesis, so it must be stated even when the shipped default is the answer}"

# SHIPPED-DEFAULT EXPECTATIONS — not caller launch decisions.
#
# Each asserts "this genesis still carries what the binary ships", and each cites
# the constant it mirrors so the claim is checkable rather than asserted. A PASS
# here means the shipped value was not changed. It does NOT mean anyone decided
# it. Anything a deployment actually chooses belongs in the required block above.
GC_EPOCH_LENGTH_BLOCKS="${GC_EPOCH_LENGTH_BLOCKS:-360}"                                  # rewards DefaultEpochLengthBlocks
GC_MAX_SUPPLY="${GC_MAX_SUPPLY:-21000000000000}"                                         # rewards DefaultMaxSupply
GC_INITIAL_BLOCK_SUBSIDY="${GC_INITIAL_BLOCK_SUBSIDY:-416190}"                           # rewards DefaultInitialBlockSubsidy
GC_TREASURY_ADDRESS="${GC_TREASURY_ADDRESS:-}"                                           # rewards DefaultParams (empty)
GC_EMISSION_TREASURY_SHARE_BPS="${GC_EMISSION_TREASURY_SHARE_BPS:-0}"                    # rewards DefaultParams
GC_NATIVE_DENOM="${GC_NATIVE_DENOM:-utwlt}"                                              # appparams.NativeBaseDenom
# Not a genesis value. Used only to project the emission schedule, because the
# schedule depends on real block time and genesis cannot record it.
GC_BLOCK_TIME_SECONDS="${GC_BLOCK_TIME_SECONDS:-5}"

PASS=0; FAIL=0; SECTION=""

section() { SECTION="$1"; printf '\n\033[1m%s\033[0m\n' "$1"; }
# Results carry a STABLE ID. The label is prose and may be reworded; the id is the
# contract the fault suite matches on, exactly — substring matching on prose let a
# short pattern be satisfied by a different check's output.
ok() { # ok <id> <label>
  declared_id "$1" || { printf 'undeclared check id: %s\n' "$1" >&2; exit 3; }
  PASS=$((PASS+1)); printf '  \033[32mPASS\033[0m  [%s] %s\n' "$1" "$2"
  return 0
}
bad() { # bad <id> <label> [detail]
  declared_id "$1" || { printf 'undeclared check id: %s\n' "$1" >&2; exit 3; }
  FAIL=$((FAIL+1)); printf '  \033[31mFAIL\033[0m  [%s] %s\n' "$1" "$2"
  # An `x && printf` tail would return 1 whenever the detail is empty, and every
  # call site is the tail of a compound command, so `set -e` would abort the run
  # with no message at all. Explicit return.
  if [[ -n "${3:-}" ]]; then printf '        %s\n' "$3"; fi
  return 0
}
note() { printf '  \033[36mnote\033[0m  %s\n' "$1"; }

# jq read that fails closed: a missing path becomes __MISSING__ rather than an
# empty string that would silently compare equal to an empty expectation.
#
# Deliberately NOT `jq -e`. That flag sets exit status 1 when the OUTPUT VALUE is
# false or null, so a legitimate `false` — allow_self_registration, every pause
# flag — reads as missing. `//` is wrong for the same reason: it fires on false
# as well as null. So the status is used only for evaluation errors, and a JSON
# null is mapped explicitly.
j() {
  local out rc
  out="$(jq -r "$1" "$GENESIS" 2>/dev/null)"; rc=$?
  if (( rc != 0 )) || [[ "$out" == "null" ]]; then
    echo "__MISSING__"
    return 0
  fi
  printf '%s' "$out"
}

# A value that could not be read is never equal to anything and never numeric.
# [[ -ge ]] evaluates its operands ARITHMETICALLY, so a non-numeric string is
# treated as a variable name and `set -u` kills the run mid-way — silently
# skipping every check below it, including the max_gas check this exists for.
is_num() { [[ "$1" =~ ^-?[0-9]+$ ]]; }

eq() { # eq <id> <label> <expected> <actual>
  if [[ "$4" == "__MISSING__" ]]; then
    bad "$1" "$2" "not present in the genesis file (expected: $3)"
  elif [[ "$3" == "$4" ]]; then ok "$1" "$2 = $4"
  else bad "$1" "$2" "expected: $3
        actual:   $4"; fi
  return 0
}

# Both sides are read from the file, so two ABSENT values must not agree. A
# mirror check that passes because neither copy exists is not doing its job.
mirror() { # mirror <id> <label> <a> <b>
  if [[ "$3" == "__MISSING__" || "$4" == "__MISSING__" ]]; then
    bad "$1" "$2" "one or both copies are absent: '$3' vs '$4'"
  elif [[ "$3" == "$4" ]]; then ok "$1" "$2 = $4"
  else bad "$1" "$2" "the two copies disagree:
        params-side:  $3
        canonical:    $4"; fi
  return 0
}

# Compared against `true` EXPLICITLY. `jq -e` exits 0 for any output that is not
# false or null — including 0, "", [] and {} — so a bound check accidentally
# reduced to a value-producing expression would pass for every input.
truthy() { # truthy <id> <label> <jq-boolean-expression> <detail-on-fail>
  if jq -e "($3) == true" "$GENESIS" >/dev/null 2>&1; then ok "$1" "$2"
  else bad "$1" "$2" "${4:-}"; fi
  return 0
}

printf '\033[1mcheck-genesis\033[0m  %s\n' "$GENESIS"

# ---- 1. native validation ------------------------------------------------------------------
#
# The chain's own answer. Everything below is a cross-check that produces a better
# message; nothing below overrides this.
section "1. native validation (authoritative)"
if [[ -n "$BIN" ]]; then
  [[ -x "$BIN" ]] || { echo "binary not executable: $BIN" >&2; exit 2; }
  VLOG="$(mktemp "${TMPDIR:-/tmp}/check-genesis.XXXXXX")"
  trap 'rm -f "$VLOG"' EXIT
  if "$BIN" validate "$GENESIS" >"$VLOG" 2>&1; then
    ok native.validate "twilightd validate"
  else
    bad native.validate "twilightd validate" "$(tail -3 "$VLOG")"
  fi
else
  note "no --bin given; the chain's own validator was NOT run. Supply --bin build/twilightd."
fi

# ---- 2. fresh-genesis invariants ------------------------------------------------------------
#
# A fresh genesis is not merely a valid one: it must carry no history. These are the
# rules x/rewards and x/mining enforce at InitGenesis, checked here so a violation
# is named rather than surfacing as a start-up failure.
section "2. fresh-genesis invariants"
eq fresh.current_epoch "rewards.state.current_epoch"              "1" "$(j '.app_state.rewards.state.current_epoch')"
eq fresh.cumulative_emitted "rewards.state.cumulative_emitted"         "0" "$(j '.app_state.rewards.state.cumulative_emitted')"
eq fresh.carry_forward_remainder "rewards.state.carry_forward_remainder"    "0" "$(j '.app_state.rewards.state.carry_forward_remainder')"
eq fresh.open_reward_blocks "rewards.open_reward_enabled_blocks"       "0" "$(j '.app_state.rewards.open_reward_enabled_blocks')"
eq fresh.entitlement_liability "rewards.outstanding_entitlement_liability" "0" "$(j '.app_state.rewards.outstanding_entitlement_liability')"
eq fresh.has_pending_params "rewards.has_pending_params"           "false" "$(j '.app_state.rewards.has_pending_params')"
eq fresh.paused "rewards.pause_state.current_paused"   "false" "$(j '.app_state.rewards.pause_state.current_paused')"
eq fresh.pause_pending "rewards.pause_state.has_pending"      "false" "$(j '.app_state.rewards.pause_state.has_pending')"

eq fresh.epoch_versions_count "rewards.epoch_config_versions count"      "1" "$(j '.app_state.rewards.epoch_config_versions | length')"
eq fresh.reward_versions_count "rewards.reward_config_versions count"     "1" "$(j '.app_state.rewards.reward_config_versions | length')"
eq fresh.settlement_versions_count "mining.settlement_params_versions count"  "1" "$(j '.app_state.mining.settlement_params_versions | length')"

for pair in \
  "fresh.sched_epoch_empty:rewards.scheduled_epoch_configs" \
  "fresh.sched_reward_empty:rewards.scheduled_reward_configs" \
  "fresh.sched_settlement_empty:mining.scheduled_settlement_params" \
  "fresh.sched_distmode_empty:mining.scheduled_distribution_modes" \
  "fresh.sched_selection_empty:mining.scheduled_selection_params"; do
  cid="${pair%%:*}"; cpath="${pair#*:}"
  eq "$cid" "$cpath is empty" "0" "$(j ".app_state.${cpath} | length")"
done
eq fresh.finalized_epochs_empty "rewards.finalized_epochs is empty" "0" "$(j '.app_state.rewards.finalized_epochs | length')"
eq fresh.slot_entitlements_empty "rewards.slot_entitlements is empty" "0" "$(j '.app_state.rewards.slot_entitlements | length')"
eq fresh.settlements_empty "mining.settlements is empty"        "0" "$(j '.app_state.mining.settlements | length')"

# ---- 3. mirror consistency -------------------------------------------------------------------
#
# THE hand-edit trap. Three economic values and the epoch length each live in TWO
# places: the params document, and the canonical version-1 history entry that
# actually governs. Editing one and not the other is the single most likely
# mistake in a hand-cut genesis, and the version entry is the one that wins.
section "3. mirror consistency (params vs canonical version 1 vs the snapshot)"
mirror mirror.epoch_length "epoch_length_blocks mirrors" \
   "$(j '.app_state.rewards.params.epoch_length_blocks')" \
   "$(j '.app_state.rewards.epoch_config_versions[0].epoch_length_blocks')"
mirror mirror.subsidy "initial_block_subsidy mirrors" \
   "$(j '.app_state.rewards.params.initial_block_subsidy')" \
   "$(j '.app_state.rewards.reward_config_versions[0].initial_block_subsidy')"
mirror mirror.emission_share "emission_treasury_share_bps mirrors" \
   "$(j '.app_state.rewards.params.emission_treasury_share_bps')" \
   "$(j '.app_state.rewards.reward_config_versions[0].emission_treasury_share_bps')"
mirror mirror.treasury_address "treasury_address mirrors" \
   "$(j '.app_state.rewards.params.treasury_address')" \
   "$(j '.app_state.rewards.reward_config_versions[0].treasury_address')"
mirror mirror.epoch_anchor "epoch anchor starts at initial_height" \
   "$(j '.initial_height')" \
   "$(j '.app_state.rewards.epoch_config_versions[0].effective_start_height')"

# THE THIRD COPY. current_epoch_config (EpochConfigSnapshot, params.proto:78) is a
# full snapshot carrying its own epoch_length_blocks, subsidy, treasury address and
# share, distribution method, halving mode and remainder policy. An operator who
# edits the two above and stops has a genesis the chain still refuses, and the
# refusal names none of them. Every field it duplicates is checked here.
for f in epoch_length_blocks initial_block_subsidy treasury_address \
         emission_treasury_share_bps distribution_method halving_mode \
         remainder_policy fee_denom fee_treasury_share_bps; do
  mirror "snapshot.$f" "current_epoch_config.$f mirrors params" \
     "$(j ".app_state.rewards.params.${f}")" \
     "$(j ".app_state.rewards.current_epoch_config.${f}")"
done

# ---- 4. immutable bounds ---------------------------------------------------------------------
#
# Ratified in app/params/bounds.go. Genesis is the last point these can be chosen;
# afterwards they need an upgrade, so a value outside the interval must be caught
# here rather than at first block.
section "4. immutable bounds (app/params/bounds.go)"
truthy bound.epoch_length "epoch_length_blocks within [360,720]" \
  '(.app_state.rewards.params.epoch_length_blocks|tonumber) as $v | $v >= 360 and $v <= 720' \
  "found $(j '.app_state.rewards.params.epoch_length_blocks')"
truthy bound.emission_share "emission_treasury_share_bps <= 5000" \
  '(.app_state.rewards.params.emission_treasury_share_bps|tonumber) <= 5000' \
  "found $(j '.app_state.rewards.params.emission_treasury_share_bps')"
truthy bound.recipients_per_chunk "max_recipients_per_chunk within [1,32]" \
  '(.app_state.mining.settlement_params_versions[0].max_recipients_per_chunk|tonumber) as $v | $v >= 1 and $v <= 32' \
  "found $(j '.app_state.mining.settlement_params_versions[0].max_recipients_per_chunk')"
truthy bound.chunks_per_settlement "max_chunks_per_settlement within [1,4]" \
  '(.app_state.mining.settlement_params_versions[0].max_chunks_per_settlement|tonumber) as $v | $v >= 1 and $v <= 4' \
  "found $(j '.app_state.mining.settlement_params_versions[0].max_chunks_per_settlement')"
truthy bound.min_payout "min_recipient_payout_amount >= 10000" \
  '(.app_state.mining.settlement_params_versions[0].min_recipient_payout_amount|tonumber) >= 10000' \
  "found $(j '.app_state.mining.settlement_params_versions[0].min_recipient_payout_amount')"
truthy bound.settlement_window "settlement_window_epochs >= 1" \
  '(.app_state.mining.settlement_params_versions[0].settlement_window_epochs|tonumber) >= 1' \
  "found $(j '.app_state.mining.settlement_params_versions[0].settlement_window_epochs')"
truthy bound.max_active_slots "max_active_slots <= 100" \
  '(.app_state.coreslot.params.max_active_slots|tonumber) <= 100' \
  "found $(j '.app_state.coreslot.params.max_active_slots')"
truthy bound.selection_cooldown "selection_policy_update_cooldown_blocks >= 360" \
  '(.app_state.coreslot.params.selection_policy_update_cooldown_blocks|tonumber) >= 360' \
  "found $(j '.app_state.coreslot.params.selection_policy_update_cooldown_blocks')"

# ---- 5. the traps ------------------------------------------------------------------------------
section "5. traps"

# The treasury trap. An empty address is only legal while both shares are zero, and
# BOTH the share and the address are frozen after genesis. Launching at zero with no
# address means no treasury share can ever be introduced without an upgrade.
TREAS_ADDR="$(j '.app_state.rewards.params.treasury_address')"
EMIS_BPS="$(j '.app_state.rewards.params.emission_treasury_share_bps')"
FEE_BPS="$(j '.app_state.rewards.params.fee_treasury_share_bps')"
if [[ "$EMIS_BPS" != "0" || "$FEE_BPS" != "0" ]]; then
  if [[ "$TREAS_ADDR" =~ ^twilight1[0-9a-z]{38,58}$ ]]; then
    ok trap.treasury_address_for_share "treasury address present for a non-zero share"
  else
    bad trap.treasury_address_for_share "treasury address present for a non-zero share" "shares are non-zero but treasury_address is '$TREAS_ADDR'"
  fi
elif [[ -z "$TREAS_ADDR" ]]; then
  note "treasury_address is EMPTY with zero shares. Legal — but the address and the share are"
  note "both frozen at genesis, so no treasury share can be introduced later without an upgrade."
else
  # A note, not a PASS. Nothing is being verified here — the address is legal
  # either way at a zero share — and counting a status report as a passing check
  # both inflates the total and creates a "check" no fault could ever make fire.
  note "treasury_address is set ahead of a zero share, which keeps the option open."
fi

# The restart trap. Genesis requires active >= min_active_slots. UpdateParams does
# NOT check this, so a running chain can be pushed into a state its own export
# cannot restart from. Checked here because this file may itself be an export.
# Status is matched against the real enum (proto SlotStatus). An UNRECOGNISED
# status is a hard failure rather than a slot that quietly counts as inactive:
# guessing the spelling here once produced a zero active count on a genesis that
# was in fact correct, which is the most dangerous possible direction for this
# check to be wrong in.
UNKNOWN_STATUS="$(jq -r '[.app_state.coreslot.slots[]?.status
  | select(. != "SLOT_STATUS_UNSPECIFIED" and . != "SLOT_STATUS_PENDING"
       and . != "SLOT_STATUS_ACTIVE"      and . != "SLOT_STATUS_INACTIVE"
       and . != "SLOT_STATUS_SUSPENDED"   and . != "SLOT_STATUS_REMOVED")]
  | unique | join(",")' "$GENESIS" 2>/dev/null || echo "__ERR__")"
if [[ -n "$UNKNOWN_STATUS" ]]; then
  bad trap.slot_status_known "every slot status is a known SlotStatus value" "unrecognised: $UNKNOWN_STATUS"
else
  ok trap.slot_status_known "every slot status is a known SlotStatus value"
fi
ACTIVE="$(jq -r '[.app_state.coreslot.slots[]? | select(.status=="SLOT_STATUS_ACTIVE")] | length' "$GENESIS" 2>/dev/null || echo 0)"
MINA="$(j '.app_state.coreslot.params.min_active_slots')"
MAXA="$(j '.app_state.coreslot.params.max_active_slots')"
if ! is_num "$ACTIVE" || ! is_num "$MINA" || ! is_num "$MAXA"; then
  bad trap.active_within_bounds "active slot count within [min,max]" \
      "unreadable bound — active=$ACTIVE min=$MINA max=$MAXA (a missing or non-numeric params entry)"
elif (( ACTIVE >= MINA && ACTIVE <= MAXA )); then
  ok trap.active_within_bounds "active slot count within [min,max] ($ACTIVE in [$MINA,$MAXA])"
else
  bad trap.active_within_bounds "active slot count within [min,max]" "active=$ACTIVE min=$MINA max=$MAXA — this genesis will be REFUSED at start-up"
fi

# Invariant 5: utwlt is the only accounting denom; no display denom may leak into
# an amount.
eq trap.native_denom "rewards native_denom" "$GC_NATIVE_DENOM" "$(j '.app_state.rewards.params.native_denom')"
eq trap.fee_denom "rewards fee_denom"    "$GC_NATIVE_DENOM" "$(j '.app_state.rewards.params.fee_denom')"
LEAKED="$(jq -r '[.app_state.bank.supply[]?.denom, .app_state.bank.balances[]?.coins[]?.denom] | map(select(. == "twlt" or . == "TWLT")) | length' "$GENESIS" 2>/dev/null || echo 0)"
eq trap.display_denom_leak "no display denom in any bank amount" "0" "$LEAKED"

# Block gas must be finite. This is TW-004 and nothing else writes it.
MAXGAS="$(j '.consensus.params.block.max_gas')"
if [[ "$MAXGAS" == "-1" ]]; then
  bad trap.max_gas_finite "block.max_gas is finite" "max_gas is -1 (unlimited) — TW-004 is NOT addressed in this genesis"
else
  ok trap.max_gas_finite "block.max_gas is finite ($MAXGAS)"
fi

# ---- 6. launch decisions -----------------------------------------------------------------------
section "6. launch decisions (supplied, not inferred)"
eq decision.chain_id "chain_id"                    "$GC_CHAIN_ID"                    "$(j '.chain_id')"
eq decision.max_gas "block.max_gas"               "$GC_MAX_GAS"                     "$MAXGAS"
eq decision.active_slots "active slots"                "$GC_ACTIVE_SLOTS"                "$ACTIVE"
eq decision.min_active_slots "min_active_slots"            "$GC_MIN_ACTIVE_SLOTS"            "$MINA"
eq decision.epoch_length "epoch_length_blocks"         "$GC_EPOCH_LENGTH_BLOCKS"         "$(j '.app_state.rewards.params.epoch_length_blocks')"
eq decision.max_supply "max_supply"                  "$GC_MAX_SUPPLY"                  "$(j '.app_state.rewards.params.max_supply')"
eq decision.subsidy "initial_block_subsidy"       "$GC_INITIAL_BLOCK_SUBSIDY"       "$(j '.app_state.rewards.params.initial_block_subsidy')"
eq decision.distribution_method "distribution_method"         "$GC_DISTRIBUTION_METHOD"         "$(j '.app_state.rewards.params.distribution_method')"
eq decision.emission_share "emission_treasury_share_bps" "$GC_EMISSION_TREASURY_SHARE_BPS" "$EMIS_BPS"
eq decision.treasury_address "treasury_address"            "$GC_TREASURY_ADDRESS"            "$TREAS_ADDR"
eq decision.allow_self_registration "allow_self_registration"     "false"                           "$(j '.app_state.coreslot.params.allow_self_registration')"
# target_block_time_seconds drives NO computation — it is validated non-zero and
# then unused — so nothing in the chain notices when it disagrees with the pacing
# operators actually run. The node's own default is derived from the Go constant,
# not from this genesis field, so a genesis declaring 10 while nodes pace at 5
# passes every other check ever written. It is compared here because this is the
# only place the two can be brought together.
eq decision.target_block_time "target_block_time_seconds"   "$GC_BLOCK_TIME_SECONDS"          "$(j '.app_state.rewards.params.target_block_time_seconds')"

AUTH="$(j '.app_state.coreslot.params.authority')"
EAUTH="$(j '.app_state.coreslot.params.emergency_authority')"
if [[ "$AUTH" =~ ^twilight1[0-9a-z]{38,58}$ ]]; then ok decision.authority_shape "authority has a well-formed address"
else bad decision.authority_shape "authority has a well-formed address" "found '$AUTH'"; fi
if [[ "$EAUTH" =~ ^twilight1[0-9a-z]{38,58}$ ]]; then ok decision.emergency_authority_shape "emergency_authority has a well-formed address"
else bad decision.emergency_authority_shape "emergency_authority has a well-formed address" "found '$EAUTH'"; fi
if [[ "$AUTH" != "$EAUTH" ]]; then ok decision.authorities_distinct "authority and emergency_authority are distinct"
else bad decision.authorities_distinct "authority and emergency_authority are distinct" "both are $AUTH — one compromised key reaches both roles"; fi
note "address checks are SHAPE only; this script does not verify a bech32 checksum."
note "Run with --bin so the chain's own validator does."

# ---- 7. what genesis cannot record --------------------------------------------------------------
section "7. emission projection — NOT a genesis value"
SUBSIDY="$(j '.app_state.rewards.params.initial_block_subsidy')"
SUPPLY="$(j '.app_state.rewards.params.max_supply')"
EPOCHLEN="$(j '.app_state.rewards.params.epoch_length_blocks')"
# A projection, not a check — and it must never be able to abort the run. A
# missing max_supply previously reached awk as 0 and killed the script with
# "division by zero" AFTER its own FAIL was recorded, so the summary and the
# verdict were never printed and a real verification failure exited 2, the code
# this script otherwise reserves for usage errors.
if is_num "$SUBSIDY" && is_num "$SUPPLY" && (( SUPPLY > 0 )) && is_num "$GC_BLOCK_TIME_SECONDS" && (( GC_BLOCK_TIME_SECONDS > 0 )); then
  awk -v s="$SUBSIDY" -v m="$SUPPLY" -v bt="$GC_BLOCK_TIME_SECONDS" 'BEGIN {
    spy = 31557600; bpy = spy / bt; y1 = s * bpy;
    printf "  at %s-second blocks:\n", bt;
    printf "    year-one emission   %.0f utwlt  (%.2f%% of max supply)\n", y1, 100*y1/m;
    printf "    first halving       %.2f years  (at 50%% of max supply)\n", ((m/2)/s)*bt/spy;
  }'
  if is_num "$EPOCHLEN"; then
    awk -v e="$EPOCHLEN" -v bt="$GC_BLOCK_TIME_SECONDS" \
      'BEGIN { printf "    epoch length        %.1f minutes\n", e*bt/60 }'
  fi
else
  note "projection skipped: subsidy='$SUBSIDY' max_supply='$SUPPLY' block_time='$GC_BLOCK_TIME_SECONDS'"
fi
note "Block time is timeout_commit in each node's config.toml. It is NOT in genesis."

# ---- summary ---------------------------------------------------------------------------------
printf '\n\033[1msummary\033[0m  %d passed, %d failed\n' "$PASS" "$FAIL"
if (( FAIL > 0 )); then
  printf '\033[31mGENESIS NOT READY\033[0m — %d check(s) failed.\n' "$FAIL"
  exit 1
fi
printf '\033[32mall checks passed\033[0m\n'
[[ -n "$BIN" ]] || { printf '\033[33mbut the chain'"'"'s own validator was not run — re-run with --bin\033[0m\n'; exit 1; }
exit 0
