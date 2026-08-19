#!/usr/bin/env bash
set -euo pipefail

# The definitive POC1 money-movement proof.
#
# This is the run that shows the deployed chain paying real participants: an
# isolated four-node localnet, real signed transactions from a node, real bank
# balances, across more than one epoch.
#
#   ACTIVE CoreSlots participate
#     -> rewards finalizes the epoch
#     -> SlotEntitlements are created
#     -> x/mining materializes a Settlement per nonzero entitlement
#     -> MsgSubmitSettlementChunk pays participants  (real balance increase)
#     -> MsgFinalizeSettlement pays the operator remainder to the IMMUTABLE
#        payout snapshot, and the settlement becomes terminal
#     -> participant total + remainder == entitlement, exactly
#
# It differs from rewards-smoke.sh deliberately, and the two must stay distinct:
# that script proves an epoch closes and creates the right obligations, this one
# proves the obligations get paid. A single script doing both would stop isolating
# which half had failed.
#
# Refusals are asserted from the DELIVERED code and from unchanged settlement
# state, never from the broadcast response. A `--broadcast-mode sync` reply reports
# CheckTx only, so a message refused during execution still answers code 0 at
# broadcast; a test trusting that number would pass against a chain that had
# stopped enforcing every rule below.

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
BIN="${BIN:-$ROOT/build/twilightd}"
NET="${TWILIGHT_LOCALNET_HOME:-/tmp/twilight-settlement-localnet}"
CHAIN_ID="${CHAIN_ID:-twilight-settlement-localnet-1}"
NODE_COUNT=4

# The ratified interval is [360, 720] and genesis refuses anything outside it, so
# these localnets run a fast block time rather than a short epoch.
EPOCH_LENGTH="${SETTLEMENT_EPOCH_LENGTH:-360}"
SUBSIDY=416190
EXPECTED_EMISSION=$((EPOCH_LENGTH * SUBSIDY))
EXPECTED_PER_SLOT=$((EXPECTED_EMISSION / NODE_COUNT))
GENESIS_FUNDED_BALANCE=1000000000000

# Three participant lines, each above the immutable 10,000 floor.
PAY_A=11000
PAY_B=22000
PAY_C=33000
DISTRIBUTED=$((PAY_A + PAY_B + PAY_C))
EXPECTED_REMAINDER=$((EXPECTED_PER_SLOT - DISTRIBUTED))

KEYRING=(--keyring-backend test)
EPOCH_WAIT_SECONDS=$(( 120 + EPOCH_LENGTH ))

export BIN NET CHAIN_ID TWILIGHT_LOCALNET_HOME="$NET"

need() { command -v "$1" >/dev/null 2>&1 || { echo "missing required command: $1" >&2; exit 2; }; }
need curl
need jq

rpc_url() { echo "tcp://127.0.0.1:$((26657 + $1 * 100))"; }
http_url() { echo "http://127.0.0.1:$((26657 + $1 * 100))"; }
latest_height() {
  curl -fsS "$(http_url "$1")/status" 2>/dev/null | jq -r '.result.sync_info.latest_block_height | tonumber' 2>/dev/null || echo 0
}
wait_all_height() {
  local target="$1" deadline=$((SECONDS + EPOCH_WAIT_SECONDS)) node
  while ((SECONDS < deadline)); do
    local ready=1
    for node in $(seq 0 $((NODE_COUNT - 1))); do
      if (($(latest_height "$node") < target)); then ready=0; break; fi
    done
    ((ready == 1)) && return 0
    sleep 1
  done
  echo "timed out waiting for all nodes to reach height $target" >&2
  return 1
}

mq() { "$BIN" mining-query "$@" --node "$(rpc_url 0)" --output json 2>/dev/null; }
rq() { "$BIN" rewards-query "$@" --node "$(rpc_url 0)" --output json 2>/dev/null; }
balance() {
  "$BIN" query bank balances "$1" --node "$(rpc_url 0)" --output json 2>/dev/null \
    | jq -r '[.balances[]? | select(.denom == "utwlt") | .amount] | first // "0"'
}

# The decoded-byte order the chain requires of chunk recipients. bech32 text order
# is NOT the same order, so it is derived from the address bytes themselves.
addr_hex() { "$BIN" debug addr "$1" 2>/dev/null | awk '/hex/ { print $3 }'; }

wait_tx_code() {
  local hash="$1" deadline=$((SECONDS + 60)) result
  while ((SECONDS < deadline)); do
    result="$(curl -fsS "$(http_url 0)/tx?hash=0x$hash" 2>/dev/null || true)"
    if [[ -n "$result" ]] && jq -e '.result.tx_result' >/dev/null 2>&1 <<<"$result"; then
      jq -r '.result.tx_result.code // 0' <<<"$result"
      return 0
    fi
    sleep 1
  done
  echo "not_included"
}

# submit runs a transaction and prints the code that actually decided it: the
# broadcast code when the mempool refused it, the DELIVERED code otherwise.
submit() {
  local out code hash
  out="$("$@" 2>&1)" || true
  code="$(jq -r '.code // empty' <<<"$out" 2>/dev/null || true)"
  if [[ -z "$code" ]]; then
    # A broadcast that did not even answer in the expected shape is a broken
    # harness, not a refusal. Failing here matters: every refusal below asserts
    # "code != 0", and a non-numeric marker would satisfy that vacuously — the
    # test would report a rule as enforced without the chain having enforced it.
    echo "broadcast produced no code: $out" >&2
    exit 1
  fi
  if [[ "$code" != "0" ]]; then
    echo "$code"
    return 0
  fi
  hash="$(jq -r '.txhash' <<<"$out")"
  code="$(wait_tx_code "$hash")"
  # Same reasoning: a transaction that never made it into a block tells us
  # nothing about what the chain would have done with it.
  if [[ "$code" == "not_included" ]]; then
    echo "transaction $hash was never included in a block" >&2
    exit 1
  fi
  echo "$code"
}

chunk_tx() { # <signer-name> <node-home-index> <chunk-index> <payout-json>...
  local signer="$1" home="$2" index="$3"; shift 3
  local args=()
  local payout
  for payout in "$@"; do args+=(--payouts "$payout"); done
  "$BIN" tx mining submit-settlement-chunk \
    --slot-id 1 --epoch 1 --chunk-index "$index" "${args[@]}" \
    --from "$signer" "${KEYRING[@]}" --home "$NET/node$home" \
    --chain-id "$CHAIN_ID" --node "$(rpc_url 0)" --gas 600000 --yes --output json
}

settlement_state() { mq settlement 1 1; }
assert_settlement_unchanged() { # <label> <expected-next-index> <expected-released>
  local label="$1" want_index="$2" want_released="$3" state
  state="$(settlement_state)"
  [[ "$(jq -r '.settlement.next_chunk_index' <<<"$state")" == "$want_index" ]] \
    || { echo "$label changed the chunk cursor" >&2; exit 1; }
  [[ "$(jq -r '.released_amount' <<<"$state")" == "$want_released" ]] \
    || { echo "$label released value" >&2; exit 1; }
}

# Refuse to run beside another localnet.
#
# Every localnet script here binds the same ports, so a node left over from an
# interrupted run keeps answering on :26657 while this one initializes its own
# chain. The transactions then land somewhere this script is not looking, and the
# result is not a failure — it is a PASS or a failure that describes a different
# chain. That is worth aborting for: a money proof whose isolation is unverified
# proves nothing about the money.
if pgrep -x twilightd >/dev/null 2>&1; then
  echo "a twilightd process is already running; stop it before running this proof" >&2
  pgrep -fl twilightd >&2 || true
  exit 2
fi

"$ROOT/scripts/localnet/init.sh"

# Test-only profile on this isolated localnet, matching rewards-smoke.sh: the
# EpochConfigVersion is the sole epoch-geometry authority and the two deprecated
# mirrors must agree with it.
for node in $(seq 0 $((NODE_COUNT - 1))); do
  genesis="$NET/node$node/config/genesis.json"
  tmp="$genesis.tmp"
  jq --arg epoch "$EPOCH_LENGTH" '
    .app_state.rewards.params.epoch_length_blocks = $epoch
    | .app_state.rewards.current_epoch_config.epoch_length_blocks = $epoch
    | .app_state.rewards.epoch_config_versions[0].epoch_length_blocks = $epoch
  ' "$genesis" >"$tmp"
  mv "$tmp" "$genesis"
done

"$ROOT/scripts/localnet/start.sh"
trap '"$ROOT/scripts/localnet/stop.sh" || true' EXIT

# --- 1. an epoch closes and consensus materializes the settlement --------------
wait_all_height $((EPOCH_LENGTH + 1))

state="$(settlement_state)"
[[ "$(jq -r '.settlement.settlement_mode' <<<"$state")" == "SETTLEMENT_MODE_TRUSTED_AS" ]]
[[ "$(jq -r '.settlement.next_chunk_index' <<<"$state")" == "0" ]]
[[ "$(jq -r '.settlement.finalized' <<<"$state")" == "false" ]]
[[ "$(jq -r '.entitlement_amount' <<<"$state")" == "$EXPECTED_PER_SLOT" ]]
[[ "$(jq -r '.released_amount' <<<"$state")" == "0" ]]
[[ "$(jq -r '.participant_distribution_ceiling' <<<"$state")" == "$EXPECTED_PER_SLOT" ]]
[[ "$(jq -r '.permissionless_finalization_now' <<<"$state")" == "false" ]]
payout_address="$(jq -r '.payout_address' <<<"$state")"
payout_before="$(balance "$payout_address")"
echo "settlement (slot 1, epoch 1) materialized: entitlement=$EXPECTED_PER_SLOT"

# --- 2. a real multi-recipient chunk pays real participants --------------------
for name in participant_a participant_b participant_c; do
  "$BIN" keys add "$name" "${KEYRING[@]}" --home "$NET/node0" --output json >"$NET/$name.json" 2>/dev/null
done
# Ordered by decoded address bytes, which is what the chain enforces.
mapping="$(for name in participant_a participant_b participant_c; do
  addr="$(jq -r '.address' "$NET/$name.json")"
  echo "$(addr_hex "$addr") $addr"
done | sort)"
recipient_a="$(sed -n '1p' <<<"$mapping" | awk '{print $2}')"
recipient_b="$(sed -n '2p' <<<"$mapping" | awk '{print $2}')"
recipient_c="$(sed -n '3p' <<<"$mapping" | awk '{print $2}')"

code="$(submit chunk_tx operator0 0 0 \
  "{\"recipient\":\"$recipient_a\",\"amount\":\"$PAY_A\"}" \
  "{\"recipient\":\"$recipient_b\",\"amount\":\"$PAY_B\"}" \
  "{\"recipient\":\"$recipient_c\",\"amount\":\"$PAY_C\"}")"
[[ "$code" == "0" ]] || {
  echo "the settlement chunk was refused (code $code)" >&2
  echo "  signer (operator0)      : $(jq -r '.address' "$NET/operator0.json")" >&2
  echo "  slot 1 settlement addr  : $("$BIN" coreslot-query slot 1 --node "$(rpc_url 0)" --output json 2>/dev/null | jq -r '.slot.settlement_address')" >&2
  echo "  slot 1 status           : $("$BIN" coreslot-query slot 1 --node "$(rpc_url 0)" --output json 2>/dev/null | jq -r '.slot.status')" >&2
  echo "  settlement state        : $(settlement_state | jq -c '{mode:.settlement.settlement_mode,next:.settlement.next_chunk_index,clock:.current_settlement_clock,deadline:.deadline_clock}')" >&2
  exit 1
}

# Real money, in real accounts.
[[ "$(balance "$recipient_a")" == "$PAY_A" ]]
[[ "$(balance "$recipient_b")" == "$PAY_B" ]]
[[ "$(balance "$recipient_c")" == "$PAY_C" ]]

state="$(settlement_state)"
[[ "$(jq -r '.settlement.next_chunk_index' <<<"$state")" == "1" ]]
[[ "$(jq -r '.released_amount' <<<"$state")" == "$DISTRIBUTED" ]]
[[ "$(jq -r '.remaining_amount' <<<"$state")" == "$EXPECTED_REMAINDER" ]]
# The released amount's authority is the entitlement in x/rewards, never a
# settlement-side copy — so it is read from there too.
[[ "$(jq -r '.entitlement.released_amount' <<<"$(rq entitlement 1 1)")" == "$DISTRIBUTED" ]]
echo "chunk 0 paid $DISTRIBUTED to three participants"

# --- 3. what the chain must refuse --------------------------------------------
# Each refusal is checked twice: the delivered code, and that no state moved.
code="$(submit chunk_tx operator0 0 0 "{\"recipient\":\"$recipient_a\",\"amount\":\"$PAY_A\"}")"
[[ "$code" != "0" ]] || { echo "a replayed chunk index was accepted" >&2; exit 1; }
assert_settlement_unchanged "the replayed chunk" 1 "$DISTRIBUTED"

code="$(submit chunk_tx operator1 1 1 "{\"recipient\":\"$recipient_a\",\"amount\":\"$PAY_A\"}")"
[[ "$code" != "0" ]] || { echo "an unauthorized signer was accepted" >&2; exit 1; }
assert_settlement_unchanged "the unauthorized signer" 1 "$DISTRIBUTED"

code="$(submit chunk_tx operator0 0 1 \
  "{\"recipient\":\"$recipient_c\",\"amount\":\"$PAY_A\"}" \
  "{\"recipient\":\"$recipient_a\",\"amount\":\"$PAY_B\"}")"
[[ "$code" != "0" ]] || { echo "descending recipients were accepted" >&2; exit 1; }
assert_settlement_unchanged "the unsorted recipients" 1 "$DISTRIBUTED"
echo "replay, unauthorized signer and unsorted recipients all refused"

# --- 4. finalization pays the operator remainder ------------------------------
code="$(submit "$BIN" tx mining finalize-settlement --slot-id 1 --epoch 1 \
  --from operator0 "${KEYRING[@]}" --home "$NET/node0" \
  --chain-id "$CHAIN_ID" --node "$(rpc_url 0)" --gas 600000 --yes --output json)"
[[ "$code" == "0" ]] || { echo "finalization was refused (code $code)" >&2; exit 1; }

state="$(settlement_state)"
[[ "$(jq -r '.settlement.finalized' <<<"$state")" == "true" ]]
[[ "$(jq -r '.settlement.finalization_reason' <<<"$state")" == "SETTLEMENT_FINALIZATION_REASON_AUTHORIZED_EARLY" ]]
[[ "$(jq -r '.released_amount' <<<"$state")" == "$EXPECTED_PER_SLOT" ]]
[[ "$(jq -r '.remaining_amount' <<<"$state")" == "0" ]]

# The remainder reached the immutable payout snapshot and nowhere else.
payout_after="$(balance "$payout_address")"
observed_remainder=$((payout_after - payout_before))
[[ "$observed_remainder" == "$EXPECTED_REMAINDER" ]]

# Conservation, over MEASURED balances rather than the constants above. Adding the
# two expectations together would prove only that this script can subtract: the
# quantities that matter are what the participants and the payout snapshot were
# actually paid, and their sum has to be the entitlement the chain committed to.
observed_participants=$(( $(balance "$recipient_a") + $(balance "$recipient_b") + $(balance "$recipient_c") ))
entitlement_amount="$(jq -r '.entitlement_amount' <<<"$state")"
[[ "$((observed_participants + observed_remainder))" == "$entitlement_amount" ]]   || { echo "participants $observed_participants + remainder $observed_remainder != entitlement $entitlement_amount" >&2; exit 1; }
echo "finalized AUTHORIZED_EARLY; remainder $observed_remainder to the payout snapshot"

# A terminal settlement cannot be finalized twice.
code="$(submit "$BIN" tx mining finalize-settlement --slot-id 1 --epoch 1 \
  --from operator0 "${KEYRING[@]}" --home "$NET/node0" \
  --chain-id "$CHAIN_ID" --node "$(rpc_url 0)" --gas 600000 --yes --output json)"
[[ "$code" != "0" ]] || { echo "a terminal settlement was finalized twice" >&2; exit 1; }

# --- 5. the pipeline keeps running across the next epoch ----------------------
wait_all_height $((2 * EPOCH_LENGTH + 1))
next_state="$(mq settlement 1 2)"
[[ "$(jq -r '.settlement.epoch' <<<"$next_state")" == "2" ]]
[[ "$(jq -r '.entitlement_amount' <<<"$next_state")" == "$EXPECTED_PER_SLOT" ]]
[[ "$(jq -r '.released_amount' <<<"$next_state")" == "0" ]]
[[ "$(jq -r '.settlement.finalized' <<<"$next_state")" == "false" ]]
echo "epoch 2 closed and materialized its own settlement"

# --- 6. solvency, and every node agreeing on all of it ------------------------
module="$(rq module-balances)"
escrow="$(jq -r '.rewards_balance' <<<"$module")"
liability="$(jq -r '.outstanding_entitlement_liability' <<<"$module")"
carry="$(jq -r '.carry_forward_remainder' <<<"$module")"
[[ "$escrow" == "$((liability + carry))" ]] \
  || { echo "escrow $escrow != liability $liability + carry $carry" >&2; exit 1; }
# Exactly one entitlement has been released in full, so escrow is the two epochs'
# emission less that entitlement. Reported on failure: a bare comparison here would
# say only that the run failed, not which side moved.
expected_escrow=$((2 * EXPECTED_EMISSION - EXPECTED_PER_SLOT))
[[ "$escrow" == "$expected_escrow" ]] || {
  echo "escrow $escrow != expected $expected_escrow (delta $((escrow - expected_escrow)))" >&2
  for slot in 1 2 3 4; do
    echo "  slot $slot epoch 1 released: $(rq entitlement "$slot" 1 | jq -r '.entitlement.released_amount')" >&2
    echo "  slot $slot epoch 2 released: $(rq entitlement "$slot" 2 | jq -r '.entitlement.released_amount')" >&2
  done
  exit 1
}
[[ "$(jq -r '.cumulative_emitted' <<<"$(rq cumulative-emitted)")" == "$((2 * EXPECTED_EMISSION))" ]]

MIN_HEIGHT=$((2 * EPOCH_LENGTH)) "$ROOT/scripts/localnet/agree.sh"

# Isolation is asserted at the end too, while the nodes are still up. A node that
# appeared mid-run would have been serving these ports while this script
# transacted, and every number above would then describe an unknown mixture of two
# chains rather than this one.
running_nodes="$(pgrep -x twilightd | wc -l | tr -d ' ')"
[[ "$running_nodes" == "$NODE_COUNT" ]] || {
  echo "expected exactly $NODE_COUNT nodes for the whole run, found $running_nodes" >&2
  exit 1
}

# --- 7. exact bank state, from an export ---------------------------------------
"$ROOT/scripts/localnet/stop.sh"
export_file="$NET/settlement-export.json"
"$BIN" export --home "$NET/node0" --output-document "$export_file" >/dev/null
supply="$(jq -r '[.app_state.bank.supply[]? | select(.denom == "utwlt") | .amount] | first // "0"' "$export_file")"
[[ "$supply" == "$((2 * GENESIS_FUNDED_BALANCE + 2 * EXPECTED_EMISSION))" ]]
[[ "$(jq -r '.app_state.rewards.state.cumulative_emitted' "$export_file")" == "$((2 * EXPECTED_EMISSION))" ]]

echo "settlement localnet money-movement proof: PASS"
echo "  epoch_length=$EPOCH_LENGTH entitlement=$EXPECTED_PER_SLOT"
echo "  participants=$DISTRIBUTED operator_remainder=$EXPECTED_REMAINDER"
echo "  escrow=$escrow liability=$liability supply=$supply"
