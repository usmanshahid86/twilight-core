#!/usr/bin/env bash
set -uo pipefail

# The operational half of #130: a real two-step authority handover on a running
# chain, for BOTH operational roles.
#
# The keeper tests already establish the mechanism. What they cannot establish is
# that the handover works through the signing path an operator actually uses —
# where the authority is a key in a keyring, the message is broadcast, and
# "unauthorized" arrives as a transaction code rather than a returned error.
#
# What this proves, and why each step is here:
#
#   nomination is INERT — the incumbent keeps acting, and the nominee cannot act.
#     If a nomination silently moved the role, a mistaken one would be exactly as
#     terminal as the single-step rotation this replaced.
#   only the NOMINEE completes it — the proof that the destination key exists.
#     This is the whole point: a wrong-but-valid address can never sign.
#   cancellation and replacement INVALIDATE a nominee — a nomination is
#     correctable while pending, which is what makes a typo survivable.
#   after acceptance the roles have genuinely SWAPPED — the successor acts, and
#     the former holder does not. Proving only the first half would pass even if
#     the old holder had kept the capability.
#   UpdateParams cannot rotate anything — the bulk path is closed, which is what
#     stops an unrelated max_active_slots edit from ending governance.
#
# Both roles run, because emergency_authority has the same shape, the same
# consequences, and its own signer. A mechanism proven for one is not proven for
# the other.
#
# # Fail-closed
#
# Every assertion must be able to fail. Authority checks are the trap here: a
# transaction rejected for the WRONG reason — bad sequence, missing account,
# malformed argument — looks identical to one rejected for being unauthorized if
# only "did it fail" is asserted. So authorized actions assert code 0 and
# unauthorized ones assert the specific unauthorized code, never merely non-zero.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
. "$ROOT/scripts/localnet/lib/drill-common.sh"
. "$ROOT/scripts/localnet/lib/drill-assert.sh"

NODE_COUNT="${NODE_COUNT:-4}"
RUN_ID="${RUN_ID:-$(date +%Y%m%d-%H%M%S)}"
DRILL_EVID_DIR="$ROOT/build/localnet/evidence/$RUN_ID/authority-rotation"

# A reused RUN_ID must not be able to append to an earlier run's evidence, or to
# leave its PASS verdict standing after a later run terminates early. Refuse
# rather than delete: evidence from a previous run is the record of that run, and
# silently removing it would destroy the thing a re-reader came for.
if [[ -e "$DRILL_EVID_DIR" ]]; then
  echo "refusing to run: $DRILL_EVID_DIR already exists; use a fresh RUN_ID" >&2
  exit 2
fi
drill_assert_init "$DRILL_EVID_DIR" || { echo "could not initialise evidence" >&2; exit 2; }

# The codespace-2 error registered as ErrUnauthorized. Asserted by value, not as
# "non-zero": a stale sequence also fails, and would otherwise be indistinguishable
# from a correct refusal.
CODE_UNAUTHORIZED=2
# ErrInvalidParams, raised by the UpdateParams authority lockout.
CODE_INVALID_PARAMS=10
# ErrNoPendingNomination, raised when accepting a nomination that was cancelled,
# replaced or never made. Distinct from unauthorized on purpose: "there is
# nothing to accept" and "you are not the nominee" are different failures.
CODE_NO_PENDING=22

# Basenames, not paths: the gate joins each entry onto DRILL_EVID_DIR itself.
DRILL_MANDATORY_FILES=(binaries.json rotation.json assertions.jsonl summary.csv)
DRILL_VERDICT_GATES=("primary=PASS" "emergency=PASS" "lockout=PASS")
# Derived from a calibration run, not estimated. A run that ends early still
# writes a verdict, so without these a truncated run reports PASS on whatever it
# managed to reach.
DRILL_EXPECTED_ASSERTIONS=42
# Counted where the gate checks it, which is BEFORE the summary's own final row
# is appended — so this is the number of phase_end calls, not the row count.
DRILL_EXPECTED_PHASES=5

# ---- helpers ---------------------------------------------------------------

# submit_as broadcasts a coreslot subcommand signed by an arbitrary key in node0's
# keyring, which submit_authority/submit_emergency cannot express — they are
# pinned to operator0 and operator1, and this drill needs successors that are
# neither.
submit_as() { # <key> <coreslot-subcommand...>
  _submit "$1" "$(home_for_key "$1")" "${@:2}"
}

# fund creates an on-chain account for a nominee. A key with no account cannot
# sign at all, so without this an acceptance would fail for "account not found"
# and be indistinguishable from a correct refusal.
fund() { # <address>
  "$BIN" tx bank send "$(key_addr operator0)" "$1" 1000000utwlt \
    --from operator0 $KEYRING --home "$(node_home 0)" \
    --chain-id "$CHAIN_ID" --node "$(rpc_url 0)" \
    --gas 300000 --fees 0utwlt --broadcast-mode sync --output json -y >/dev/null 2>&1
  sleep 3
}

# init.sh creates operator$i in node$i's OWN keyring, not in a shared one, so a
# key's home is not interchangeable. Successors created by this drill live in
# node0's keyring, which is where it signs from.
home_for_key() { # <key>
  case "$1" in
    operator*) node_home "${1#operator}" ;;
    *)         node_home 0 ;;
  esac
}

key_addr() { "$BIN" keys show "$1" -a $KEYRING --home "$(home_for_key "$1")" 2>/dev/null; }

new_key() { # <name> -> address, funded
  "$BIN" keys add "$1" $KEYRING --home "$(node_home 0)" --output json >/dev/null 2>&1
  local a; a="$(key_addr "$1")"
  fund "$a"
  echo "$a"
}

# params_doc emits a Params document `coreslot update-params` can actually read.
#
# The query renders every numeric field as a JSON STRING (proto3 JSON), while the
# Go struct carries plain int64/uint64 with no `,string` tag — so feeding the
# query's own output straight back in fails with "cannot unmarshal string into Go
# struct field Params.slot_voting_power of type int64". The obvious operator
# workflow (query params, edit one field, submit) is therefore broken; that is a
# CLI defect independent of this drill, tracked separately.
#
# Numeric-looking strings are converted by trying tonumber and keeping the
# original on failure, rather than by naming the numeric fields: an enumerated
# list would silently rot the next time a field is added to Params.
params_doc() { # <jq filter applied to the params object>
  # `catch .` would bind `.` to the ERROR MESSAGE, not the original value, which
  # silently replaced every bech32 address with a parser diagnostic. Bind the input
  # first so a failed conversion keeps what it was given.
  q params | jq "$1" | jq 'with_entries(.value |= (if type == "string" then (. as $v | try ($v|tonumber) catch $v) else . end))'
}

params_authority() { q params 2>/dev/null | jq -r '.params.authority // "?"'; }
params_emergency() { q params 2>/dev/null | jq -r '.params.emergency_authority // "?"'; }
params_max_slots() { q params 2>/dev/null | jq -r '.params.max_active_slots // "?"'; }

# authority_action performs a real authority-gated write and returns its tx code.
# update-params is used because it is observable and reversible: the drill can
# assert the value actually changed, which "the transaction succeeded" does not.
authority_action() { # <signing-key> <max_active_slots>
  # Split declarations deliberately: bash 3.2, which is what macOS ships, does not
  # let a later assignment in the same `local` reference an earlier one, and under
  # set -u that reads as "unbound variable" rather than as an empty string.
  local key="$1"
  local want="$2"
  local f="$DRILL_EVID_DIR/params-$want.json"
  params_doc ".params | .max_active_slots = \"$want\"" >"$f"
  submit_as "$key" update-params "$f"
  echo "$LAST_TXCODE"
}

# emergency_action exercises the emergency role specifically: pausing rewards is
# gated on emergency_authority, not on the primary authority, so it proves the
# emergency role moved rather than the primary one.
emergency_action() { # <signing-key> <pause|resume>
  LAST_TXHASH=""; LAST_TXCODE=""
  local out
  out="$("$BIN" rewards "$2" --from "$1" $KEYRING --home "$(home_for_key "$1")" \
    --chain-id "$CHAIN_ID" --node "$(rpc_url 0)" \
    --gas 400000 --fees 0utwlt --broadcast-mode sync --output json -y 2>/dev/null)" || true
  LAST_TXHASH="$(jq -r '.txhash // ""' <<<"$out" 2>/dev/null || echo "")"
  if [[ -z "$LAST_TXHASH" ]]; then echo "broadcast_error"; return 0; fi
  local check; check="$(jq -r '.code // empty' <<<"$out" 2>/dev/null || echo "")"
  if [[ -n "$check" && "$check" != "0" ]]; then echo "$check"; return 0; fi
  _wait_tx_code "$LAST_TXHASH"
}

rewards_paused() {
  # The generated tree, deliberately. `rewards-query pause-state` is one of the
  # three custom commands that fall through the dispatch switch and have never
  # returned data (#136); using it here would read "?" forever and every pause
  # assertion would be vacuous.
  #
  # Tested with `== true` rather than `// false`: proto3 JSON omits false, so an
  # unpaused chain returns no current_paused field at all, and jq's `//` treats a
  # literal false as absent too. An explicit equality test gives the same answer
  # for "absent" and "present and false" without relying on that coincidence.
  "$BIN" query rewards rewards-pause-state --node "$(rpc_url 0)" --output json 2>/dev/null \
    | jq -r '(.pause_state.current_paused == true) | tostring' 2>/dev/null || echo "?"
}

# ---- run -------------------------------------------------------------------

echo "==> starting a $NODE_COUNT-node localnet"
# `twilightd init` writes its genesis summary to STDERR, so both streams are
# captured to a file rather than dropped: dropping stderr would also hide a real
# setup failure, and leaving it on stdout buries the drill's own output.
NODE_COUNT="$NODE_COUNT" "$ROOT/scripts/localnet/init.sh" >"$DRILL_EVID_DIR/setup.log" 2>&1
NODE_COUNT="$NODE_COUNT" "$ROOT/scripts/localnet/start.sh" >>"$DRILL_EVID_DIR/setup.log" 2>&1
trap '"$ROOT/scripts/localnet/stop.sh" >/dev/null 2>&1 || true' EXIT
wait_all_height 3

# The binary under test, recorded by hash: "which bytes ran" is the first thing
# a re-reader of this evidence needs.
jq -n --arg commit "$(git -C "$ROOT" rev-parse HEAD)" \
      --arg sha "$(shasum -a 256 "$BIN" | awk '{print $1}')" \
      '{source_commit: $commit, twilightd_sha256: $sha}' >"$DRILL_EVID_DIR/binaries.json"

AUTH0="$(key_addr operator0)"
EMER0="$(key_addr operator1)"
echo "==> incumbent primary   $AUTH0"
echo "==> incumbent emergency $EMER0"

echo "==> creating and funding successors"
NEW_PRIMARY="$(new_key rotate-primary)"
DISPLACED="$(new_key rotate-displaced)"
NEW_EMERGENCY="$(new_key rotate-emergency)"

# ---- phase 1: nomination is inert ------------------------------------------

phase_begin
expect "params_authority_is_incumbent" "$AUTH0" "$(params_authority)"

submit_authority nominate-authority primary "$DISPLACED"
expect "nominate_primary_accepted" "0" "$LAST_TXCODE"
expect "authority_unchanged_after_nomination" "$AUTH0" "$(params_authority)"

# The incumbent still acts. If a nomination had moved the role, this would fail
# and the mechanism would be no safer than the one it replaced.
expect "incumbent_still_acts" "0" "$(authority_action operator0 90)"
expect "incumbent_action_took_effect" "90" "$(params_max_slots)"

# And the PENDING NOMINEE cannot act yet — rotate-displaced, which is the key
# nominated above, not some other stranger. Signing this with a third party would
# prove only that a stranger cannot act, which was never in doubt and is not the
# property under test: the claim is that being nominated confers nothing.
#
# Asserted against the SPECIFIC unauthorized code, because a nominee with a bad
# sequence would also fail.
expect "nominee_cannot_act_yet" "$CODE_UNAUTHORIZED" "$(authority_action rotate-displaced 91)"
expect "nominee_action_had_no_effect" "90" "$(params_max_slots)"
phase_end "nomination-inert" "nomination records a successor without moving the role"

# ---- phase 2: replacement and cancellation invalidate a nominee -------------

phase_begin
submit_authority nominate-authority primary "$NEW_PRIMARY"
expect "replacement_nomination_accepted" "0" "$LAST_TXCODE"

# The displaced nominee is now a stranger to the pending record.
submit_as rotate-displaced accept-authority primary
expect "displaced_nominee_cannot_accept" "$CODE_UNAUTHORIZED" "$LAST_TXCODE"
expect "authority_unchanged_after_displaced_accept" "$AUTH0" "$(params_authority)"

# Cancel, then prove the live nominee cannot accept either.
submit_authority cancel-authority-nomination primary
expect "cancel_accepted" "0" "$LAST_TXCODE"
submit_as rotate-primary accept-authority primary
expect "accept_after_cancel_refused" "$CODE_NO_PENDING" "$LAST_TXCODE"
expect "authority_unchanged_after_cancel" "$AUTH0" "$(params_authority)"

# Re-nominate for the handover the next phase completes.
submit_authority nominate-authority primary "$NEW_PRIMARY"
expect "renomination_accepted" "0" "$LAST_TXCODE"
phase_end "nomination-correctable" "a pending nomination can be replaced and withdrawn"

# ---- phase 3: the primary handover -----------------------------------------

phase_begin
submit_as rotate-primary accept-authority primary
expect "nominee_accepts" "0" "$LAST_TXCODE"
expect "authority_is_successor" "$NEW_PRIMARY" "$(params_authority)"
expect "emergency_untouched_by_primary_rotation" "$EMER0" "$(params_emergency)"

# The successor acts...
expect "successor_acts" "0" "$(authority_action rotate-primary 92)"
expect "successor_action_took_effect" "92" "$(params_max_slots)"

# ...and the former holder does not. Asserting only the first half would pass
# even if the old authority had kept the capability.
expect "former_authority_cannot_act" "$CODE_UNAUTHORIZED" "$(authority_action operator0 93)"
expect "former_authority_action_had_no_effect" "92" "$(params_max_slots)"
phase_end "primary-rotated" "the primary role moved and the former holder lost it"

# ---- phase 4: the emergency handover ----------------------------------------

phase_begin
expect "emergency_incumbent_controls_pause" "0" "$(emergency_action operator1 pause)"
sleep 3
expect "rewards_are_paused" "true" "$(rewards_paused)"
expect "emergency_incumbent_controls_resume" "0" "$(emergency_action operator1 resume)"
sleep 3

submit_emergency nominate-authority emergency "$NEW_EMERGENCY"
expect "nominate_emergency_accepted" "0" "$LAST_TXCODE"
expect "emergency_unchanged_after_nomination" "$EMER0" "$(params_emergency)"
expect "emergency_nominee_cannot_pause_yet" "$CODE_UNAUTHORIZED" "$(emergency_action rotate-emergency pause)"

submit_as rotate-emergency accept-authority emergency
expect "emergency_nominee_accepts" "0" "$LAST_TXCODE"
expect "emergency_is_successor" "$NEW_EMERGENCY" "$(params_emergency)"
expect "primary_untouched_by_emergency_rotation" "$NEW_PRIMARY" "$(params_authority)"

expect "emergency_successor_controls_pause" "0" "$(emergency_action rotate-emergency pause)"
sleep 3
expect "rewards_paused_by_successor" "true" "$(rewards_paused)"
expect "former_emergency_cannot_resume" "$CODE_UNAUTHORIZED" "$(emergency_action operator1 resume)"
expect "emergency_successor_controls_resume" "0" "$(emergency_action rotate-emergency resume)"
sleep 3
expect "rewards_resumed_by_successor" "false" "$(rewards_paused)"
phase_end "emergency-rotated" "the emergency role moved independently of the primary"

# ---- phase 5: the bulk path cannot rotate -----------------------------------

phase_begin
# The failure this whole issue exists to prevent: an ordinary parameter edit that
# also carries an authority field.
f="$DRILL_EVID_DIR/params-hijack.json"
params_doc ".params | .authority = \"$AUTH0\" | .max_active_slots = \"95\"" >"$f"
submit_as rotate-primary update-params "$f"
expect "update_params_cannot_change_authority" "$CODE_INVALID_PARAMS" "$LAST_TXCODE"
expect "authority_survived_bulk_update" "$NEW_PRIMARY" "$(params_authority)"
expect "bulk_update_had_no_partial_effect" "92" "$(params_max_slots)"

f="$DRILL_EVID_DIR/params-hijack-emergency.json"
params_doc ".params | .emergency_authority = \"$EMER0\" | .max_active_slots = \"95\"" >"$f"
submit_as rotate-primary update-params "$f"
expect "update_params_cannot_change_emergency" "$CODE_INVALID_PARAMS" "$LAST_TXCODE"
expect "emergency_survived_bulk_update" "$NEW_EMERGENCY" "$(params_emergency)"

# An unrelated edit still works, so the lockout is narrow rather than a freeze.
expect "unrelated_param_update_still_works" "0" "$(authority_action rotate-primary 94)"
expect "unrelated_update_took_effect" "94" "$(params_max_slots)"
phase_end "bulk-path-closed" "UpdateParams edits parameters and cannot rotate a role"

# ---- evidence ---------------------------------------------------------------

jq -n \
  --arg incumbent_primary "$AUTH0" --arg successor_primary "$NEW_PRIMARY" \
  --arg incumbent_emergency "$EMER0" --arg successor_emergency "$NEW_EMERGENCY" \
  --arg displaced "$DISPLACED" \
  --arg final_authority "$(params_authority)" --arg final_emergency "$(params_emergency)" \
  '{primary: {incumbent: $incumbent_primary, successor: $successor_primary, final: $final_authority},
    emergency: {incumbent: $incumbent_emergency, successor: $successor_emergency, final: $final_emergency},
    displaced_nominee: $displaced}' >"$DRILL_EVID_DIR/rotation.json"

DRILL_VERDICT_LINES=(
  "primary=$([[ "$(params_authority)" == "$NEW_PRIMARY" ]] && echo PASS || echo FAIL)"
  "emergency=$([[ "$(params_emergency)" == "$NEW_EMERGENCY" ]] && echo PASS || echo FAIL)"
  "lockout=$([[ "$(params_max_slots)" == "94" ]] && echo PASS || echo FAIL)"
)
finalize_verdict
