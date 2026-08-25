#!/usr/bin/env bash
set -uo pipefail

# Three Slots, three epochs, and every settlement case the profile can reach.
#
# join-and-settle.sh proves the happy path once: a Slot joins, earns, and its
# operator settles alone. This drill is the matrix around it — membership moving
# in BOTH directions, across MULTIPLE epoch boundaries, with settlements
# outstanding while the set changes underneath them.
#
# The scenario, in order:
#
#   epoch 1   Slot 1 from genesis; Slots 2 and 3 join mid-epoch.
#             Slot 2's operator is funded. Slot 3's deliberately is NOT.
#   epoch 2   Slot 2 settles its epoch-1 entitlement across two chunks and
#             finalizes it early. Slot 3's operator, holding nothing, cannot.
#             Slot 3 is then inactivated mid-epoch, leaving a settlement open
#             behind it.
#   epoch 3   Slot 3 is absent and earns nothing. The epoch-1 deadlines pass,
#             and an unrelated operator finalizes what is left — including
#             Slot 3's, which pays the chain's own money to an operator that
#             never had any.
#
# Every refusal here is asserted by the message the chain returned, and every
# negative case is constructed so the rule under test is the only thing wrong with
# the transaction. A refusal that passes for the wrong reason is worse than no test
# at all: it reports a rule as enforced that was never reached.
#
# The question this was built to answer
#
# A Slot admitted after genesis has an address nobody has funded. It cannot sign,
# so it cannot pay participants and cannot finalize early either — both need an
# account. Its entitlement is real but sits in escrow. Only at the deadline does
# the settlement open to everyone, and the finalization then pays 100% of it to
# the operator's immutable payout snapshot, with participants receiving nothing
# for that epoch.
#
# So the chain does fund a new operator out of its own earnings, without anyone's
# help. What pre-funding buys is not the money — it is the ability to DISTRIBUTE
# in the first epochs rather than forfeit them. Section 9 proves both halves.

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
. "$ROOT/scripts/localnet/lib/drill-common.sh"

# drill-common turns on `set -e`, and this drill deliberately queries state that
# does not exist — an absent entitlement, a settlement that was never materialized.
# Those queries fail, and under `-e` (with pipefail) a failing query inside a
# command substitution kills the run mid-section, silently discarding every
# assertion that had not executed yet. The drill counts its own failures and exits
# non-zero at the end, so `-e` buys nothing here and costs the rest of the results.
set +e

# Slow enough for a joining node to outpace production (see validator-growth.sh),
# fast enough that three epochs stay under fifteen minutes.
export TWILIGHT_LOCALNET_TIMEOUT_COMMIT="${MATRIX_TIMEOUT_COMMIT:-500ms}"
NODE_COUNT=3
export NODE_COUNT   # so the exit trap stops every node it started

EPOCH_LENGTH=360
SUBSIDY=416190
EPOCH_EMISSION=$((EPOCH_LENGTH * SUBSIDY))
# HardMaxChunksPerSettlement. A chunk at this index is one past the last legal one.
HARD_MAX_CHUNKS=4
FAILURES=0
fail() { echo "  FAIL: $*" >&2; FAILURES=$((FAILURES + 1)); }
ok()   { echo "  ok: $*"; }
note() { echo "  note: $*"; }

need curl; need jq

if pgrep -x twilightd >/dev/null 2>&1; then
  echo "a twilightd process is already running; stop it first" >&2; exit 2
fi
if pgrep -f "make drills" >/dev/null 2>&1; then
  echo "a drill run is already in progress; wait for it" >&2; exit 2
fi
trap '"$ROOT/scripts/localnet/stop.sh" >/dev/null 2>&1 || true; pkill -f "twilightd start" >/dev/null 2>&1 || true' EXIT

# `|| true` is load-bearing: a query for state that does not exist is a normal
# outcome in this drill, not an error, and must yield empty output rather than a
# failed pipeline.
mq() { "$BIN" mining-query "$@" --node "$(rpc_url 0)" --output json 2>/dev/null || true; }
rq() { "$BIN" rewards-query "$@" --node "$(rpc_url 0)" --output json 2>/dev/null || true; }
# Always prints a number. An empty string here would be substituted straight into
# an arithmetic context and break the sum rather than fail the assertion.
balance() {
  local v
  v="$("$BIN" query bank balances "$1" --node "$(rpc_url 0)" --output json 2>/dev/null \
    | jq -r '[.balances[]? | select(.denom=="utwlt") | .amount] | first // "0"' 2>/dev/null || true)"
  echo "${v:-0}"
}
addr_of() { sed -n 's/.*"address":"\([^"]*\)".*/\1/p' "$NET/operator$1.json" | head -1; }
pubkey_of() { sed -n 's/.*"value": "\([^"]*\)".*/\1/p' "$(node_home "$1")/config/priv_validator_key.json" | head -1; }
# A query for state that does not exist fails and prints nothing, and jq given
# empty input produces no output at all — so `// "0"` inside the filter never
# runs. The default has to be applied to the SUBSTITUTION, not inside jq, or an
# absent entitlement reads as "" and every comparison against it is wrong.
entitlement() {
  local v; v="$(rq entitlement "$1" "$2" | jq -r '.entitlement.entitlement_amount // "0"' 2>/dev/null || true)"
  echo "${v:-0}"
}
settlement_slot() { # prints the slot id of a settlement, or "none" if there is none
  local v; v="$(mq settlement "$1" "$2" | jq -r '.settlement.slot_id // "none"' 2>/dev/null || true)"
  echo "${v:-none}"
}
carry() {
  local v; v="$(rq module-balances | jq -r '.carry_forward_remainder // "0"' 2>/dev/null || true)"
  echo "${v:-0}"
}

# latest_height reports 0 for a node that is not answering, never an empty
# string, so both of these test the VALUE. A guard written against emptiness
# returns on the first failed poll and reports a dead node as a healthy one.
wait_rpc() { local d=$((SECONDS + 60)); while ((SECONDS < d)); do (( $(latest_height "$1") > 0 )) && return 0; sleep 1; done; return 1; }
wait_producing() {
  local o="$1" s n d; s="$(latest_height "$o")"; (( s == 0 )) && return 1
  d=$((SECONDS + 45))
  while ((SECONDS < d)); do n="$(latest_height "$o")"; (( n - s >= 2 )) && return 0; sleep 1; done
  return 1
}
# Every epoch boundary needs its own generous deadline: 360 blocks at 500ms is six
# minutes, and drill-common's default is sized for a handful of blocks.
wait_epoch_end() {
  # Two statements, not one: bash expands every word of a `local` command before
  # it assigns any of them, so a target computed from `e` in the same statement
  # is expanded while `e` is still unset — fatal under `set -u`.
  local e="$1" target n
  target=$((e * EPOCH_LENGTH + 2))
  for n in $(seq 0 $((NODE_COUNT - 1))); do
    wait_height_node "$n" "$target" 900 || { fail "node$n never reached the end of epoch $e"; return 1; }
  done
  sleep 3
}

# Refusals are asserted by REASON, not by a non-zero code.
#
# A bare `code != 0` check passes for any refusal at all — a below-floor amount, a
# chunk index out of order, a malformed flag — so it can report a rule as enforced
# that the chain never reached. Every negative case below therefore names the
# message it expects, and every negative case is built so that the rule under test
# is the ONLY thing wrong with the transaction.
LAST_CODE=""; LAST_LOG=""

_delivered() { # <hash> -> sets LAST_CODE / LAST_LOG from the DELIVERED result
  local hash="$1" i res
  for ((i = 0; i < 60; i++)); do
    res="$(rpc_get 0 "/tx?hash=0x$hash" 2>/dev/null || true)"
    if [[ -n "$res" ]] && jq -e '.result.tx_result' >/dev/null 2>&1 <<<"$res"; then
      LAST_CODE="$(jq -r '.result.tx_result.code // 0' <<<"$res")"
      LAST_LOG="$(jq -r '.result.tx_result.log // ""' <<<"$res")"
      return 0
    fi
    sleep 1
  done
  LAST_CODE="not_included"; LAST_LOG="the transaction was never included"
}

# tx <node> <key> <args...> — a mining transaction from one operator's own home
# with its own credential. Sets LAST_CODE and LAST_LOG; prints the code.
tx() {
  local n="$1" key="$2"; shift 2
  local out code hash
  LAST_CODE=""; LAST_LOG=""
  out="$("$BIN" tx mining "$@" \
      --from "$key" --keyring-backend test --home "$(node_home "$n")" \
      --chain-id "$CHAIN_ID" --node "$(rpc_url 0)" --gas 600000 --yes --output json 2>&1)"
  code="$(jq -r '.code // empty' <<<"$out" 2>/dev/null || true)"
  if [[ -z "$code" ]]; then
    # Not a transaction at all: no account to sign against, or a client refusal.
    LAST_CODE="unsigned"; LAST_LOG="$(tr '\n' ' ' <<<"$out" | cut -c1-300)"
    echo "unsigned"; return 0
  fi
  if [[ "$code" != "0" ]]; then
    LAST_CODE="$code"; LAST_LOG="$(jq -r '.raw_log // ""' <<<"$out")"
    echo "$code"; return 0
  fi
  hash="$(jq -r '.txhash' <<<"$out")"
  _delivered "$hash"
  echo "$LAST_CODE"
}

# refused <expected-substring> <description> — assert the LAST tx was refused FOR
# THE STATED REASON.
refused() {
  local want="$1" what="$2"
  if [[ "$LAST_CODE" == "0" ]]; then fail "$what — the chain ACCEPTED it"; return; fi
  if [[ "$LAST_LOG" == *"$want"* ]]; then ok "$what (code $LAST_CODE)"
  else fail "$what — refused for a DIFFERENT reason (code $LAST_CODE): $LAST_LOG"; fi
}
accepted() { # <description>
  [[ "$LAST_CODE" == "0" ]] && ok "$1" || fail "$1 — refused (code $LAST_CODE): $LAST_LOG"
}

chunk() { # <node> <key> <slot> <epoch> <index> <payout-json...>
  local n="$1" key="$2" slot="$3" epoch="$4" idx="$5"; shift 5
  local args=(); local p; for p in "$@"; do args+=(--payouts "$p"); done
  tx "$n" "$key" submit-settlement-chunk --slot-id "$slot" --epoch "$epoch" \
     --chunk-index "$idx" "${args[@]}"
}
finalize() { tx "$1" "$2" finalize-settlement --slot-id "$3" --epoch "$4"; }

# Chunk payout lines must be strictly ascending by DECODED ADDRESS BYTES, which is
# a different order from the bech32 text. Sorting on the text would produce a
# chunk the chain refuses, and the refusal would look like a bug in the drill.
addr_hex() { "$BIN" debug addr "$1" 2>/dev/null | sed -n 's/^Address (hex): //p'; }
pays() { # <addr:amount>... -> payout JSON, ascending by address bytes
  local p a
  for p in "$@"; do a="${p%%:*}"; echo "$(addr_hex "$a") $p"; done | sort | while read -r _ p; do
    printf '{"recipient":"%s","amount":"%s"}\n' "${p%%:*}" "${p##*:}"
  done
}
pays_desc() { # the same lines in the order the chain must refuse
  local p a
  for p in "$@"; do a="${p%%:*}"; echo "$(addr_hex "$a") $p"; done | sort -r | while read -r _ p; do
    printf '{"recipient":"%s","amount":"%s"}\n' "${p%%:*}" "${p##*:}"
  done
}

echo "=== 1. a chain with a single Slot ==="
NODE_COUNT=1 "$ROOT/scripts/localnet/init.sh" >/dev/null 2>&1 || { echo "init failed" >&2; exit 1; }
NODE_COUNT=1 "$ROOT/scripts/localnet/start.sh" >/dev/null 2>&1 || { echo "start failed" >&2; exit 1; }
wait_rpc 0 || { echo "node0 never answered" >&2; exit 1; }
wait_producing 0 || { echo "the single Slot must produce" >&2; exit 1; }
ok "Slot 1 producing alone"

echo
echo "=== 2. two Slots join mid-epoch ==="
# bash 3.2 has no associative arrays, and the drill has exactly two joiners.
# Sets ADMITTED_SLOT rather than printing it: a command substitution would run
# the body in a subshell, and every fail() inside it would increment a copy of
# FAILURES that dies with the subshell. The drill would then pass while reporting
# its own failures.
ADMITTED_SLOT=""
admit() { # <node-index>
  local i="$1" op sid
  ADMITTED_SLOT=""
  join_node "$i"
  start_node "$i"
  wait_rpc "$i" || { fail "node$i RPC never answered"; return; }
  wait_synced "$i" 120 && ok "node$i caught up before admission" || fail "node$i never caught up"
  op="$(addr_of "$i")"
  sid="$(next_slot_id)"
  submit_authority register "$op" "$op" "$op" "$(pubkey_of "$i")" "node$i" >/dev/null 2>&1
  [[ "$LAST_TXCODE" == "0" ]] || fail "register for node$i refused (code $LAST_TXCODE)"
  submit_authority activate "$sid" >/dev/null 2>&1
  [[ "$LAST_TXCODE" == "0" ]] || fail "activate for Slot $sid refused (code $LAST_TXCODE)"
  sleep 3
  ok "Slot $sid admitted at height $(latest_height 0)"
  ADMITTED_SLOT="$sid"
}
admit 1; S2="$ADMITTED_SLOT"
admit 2; S3="$ADMITTED_SLOT"
[[ -n "$S2" && -n "$S3" ]] || { echo "a joiner never reached admission; nothing further is testable" >&2; exit 1; }
[[ "$(active_count)" == "3" ]] && ok "three Slots active" || fail "active is $(active_count), expected 3"

echo
echo "=== 3. one joiner is funded, the other deliberately is not ==="
"$BIN" tx bank send "$(addr_of 0)" "$(addr_of 1)" 1000000utwlt \
  --from operator0 --keyring-backend test --home "$(node_home 0)" \
  --chain-id "$CHAIN_ID" --node "$(rpc_url 0)" --gas 300000 --yes --output json >/dev/null 2>&1
sleep 4
[[ "$(balance "$(addr_of 1)")" != "0" ]] && ok "Slot $S2's operator is funded and can sign" \
  || fail "funding Slot $S2's operator failed"
[[ "$(balance "$(addr_of 2)")" == "0" ]] && ok "Slot $S3's operator holds nothing — left that way on purpose" \
  || fail "Slot $S3's operator was funded; the bootstrap case cannot be tested"

echo
echo "=== 4. epoch 1 closes ==="
wait_epoch_end 1
e1_1="$(entitlement 1 1)"; e1_2="$(entitlement "$S2" 1)"; e1_3="$(entitlement "$S3" 1)"
c1="$(carry)"
echo "  Slot 1: $e1_1    Slot $S2: $e1_2    Slot $S3: $e1_3    carry: $c1"
[[ "$e1_1" != "0" && "$e1_2" != "0" && "$e1_3" != "0" ]] && ok "all three Slots earned" \
  || fail "an epoch-1 entitlement is missing"
(( e1_1 > e1_2 && e1_1 > e1_3 )) && ok "the genesis Slot out-earned both joiners — allocation follows participation" \
  || fail "a mid-epoch joiner earned at least as much as the Slot present throughout"
t1=$((e1_1 + e1_2 + e1_3 + c1))
(( t1 == EPOCH_EMISSION )) && ok "epoch 1 conserved: $t1 == $EPOCH_EMISSION" \
  || fail "epoch 1 sums to $t1, expected $EPOCH_EMISSION"

echo
echo "=== 5. a settlement paid across every chunk it is allowed ==="
# Six recipients so the settlement can use all four chunks and still have a spare
# address for the case that must be refused after the last one.
recipient() { "$BIN" keys add "$1" --keyring-backend test --home "$(node_home 1)" --output json 2>/dev/null | jq -r .address; }
r1="$(recipient r1)"; r2="$(recipient r2)"; r3="$(recipient r3)"
r4="$(recipient r4)"; r5="$(recipient r5)"; r6="$(recipient r6)"
MAX_CHUNKS="$(mq settlement-params-version 1 | jq -r '.version.max_chunks_per_settlement')"
FLOOR="$(mq settlement-params-version 1 | jq -r '.version.min_recipient_payout_amount')"
ok "the chain permits $MAX_CHUNKS chunks per settlement, with a $FLOOR floor per line"

chunk 1 operator1 "$S2" 1 0 $(pays "$r1:25000" "$r2:31000") >/dev/null
accepted "chunk 0 paid two participants in one transaction"
chunk 1 operator1 "$S2" 1 1 $(pays "$r3:17000") >/dev/null
accepted "chunk 1 paid a third"

# Each refusal below is a valid chunk in every respect but one.
chunk 1 operator1 "$S2" 1 2 $(pays_desc "$r4:25000" "$r5:25000") >/dev/null
refused "strictly ascending by address bytes" "payout lines out of address-byte order are refused"
chunk 1 operator1 "$S2" 1 2 $(pays "$r4:$((FLOOR - 1))") >/dev/null
refused "below the minimum participant payout" "a line one below the floor is refused"

chunk 1 operator1 "$S2" 1 2 $(pays "$r4:25000") >/dev/null
accepted "chunk 2 accepted after both refusals — a refused chunk consumes no index"
chunk 1 operator1 "$S2" 1 3 $(pays "$r5:25000") >/dev/null
accepted "chunk 3, the last one permitted"
chunk 1 operator1 "$S2" 1 "$MAX_CHUNKS" $(pays "$r6:25000") >/dev/null
refused "has used them all" "the chunk after the last permitted one is refused by the cap"

sleep 3
PAID_OUT=$((25000 + 31000 + 17000 + 25000 + 25000))
got=$(( $(balance "$r1") + $(balance "$r2") + $(balance "$r3") + $(balance "$r4") + $(balance "$r5") ))
(( got == PAID_OUT )) && ok "five participants hold exactly the $PAID_OUT the chunks named" \
  || fail "participants hold $got, expected $PAID_OUT"
[[ "$(balance "$r6")" == "0" ]] && ok "the refused chunk paid nobody" || fail "a refused chunk moved money"
[[ "$(mq settlement "$S2" 1 | jq -r '.released_amount')" == "$PAID_OUT" ]] \
  && ok "the settlement's released total tracks every chunk" \
  || fail "released is $(mq settlement "$S2" 1 | jq -r '.released_amount'), expected $PAID_OUT"

echo
echo "=== 6. settlement authority is per-Slot, in every direction ==="
# Above the floor and at each settlement's own next index, so authority is the
# only thing these transactions get wrong.
chunk 1 operator1 1 1 0 $(pays "$r1:25000") >/dev/null
refused "may submit its chunks" "Slot $S2's operator cannot pay from Slot 1"
chunk 0 operator0 "$S3" 1 0 $(pays "$r1:25000") >/dev/null
refused "may submit its chunks" "Slot 1's operator cannot pay from Slot $S3"

echo
echo "=== 7. the unfunded operator cannot act at all ==="
# Not a policy refusal: with no account there is no sequence to sign against, so
# the transaction never becomes a transaction. It is invisible until attempted.
chunk 2 operator2 "$S3" 1 0 $(pays "$r1:25000") >/dev/null
[[ "$LAST_CODE" != "0" ]] && ok "Slot $S3's operator cannot submit a chunk ($LAST_CODE)" \
  || fail "the unfunded operator submitted a chunk"
finalize 2 operator2 "$S3" 1 >/dev/null
[[ "$LAST_CODE" != "0" ]] && ok "nor finalize its own settlement early ($LAST_CODE)" \
  || fail "the unfunded operator finalized early"
[[ "$(mq settlement "$S3" 1 | jq -r '.settlement.finalized')" == "false" ]] \
  && ok "Slot $S3's entitlement is real but stranded in escrow" || fail "Slot $S3's settlement changed state"

echo
echo "=== 8. early finalization, and terminal really is terminal ==="
payout2="$(mq settlement "$S2" 1 | jq -r '.payout_address')"
before="$(balance "$payout2")"
finalize 1 operator1 "$S2" 1 >/dev/null
accepted "Slot $S2's own signer finalized early, with the participant window still open"
sleep 3
st="$(mq settlement "$S2" 1)"
[[ "$(jq -r '.settlement.finalized' <<<"$st")" == "true" ]] && ok "Slot $S2 settlement is finalized" \
  || fail "Slot $S2 is not finalized"
# The arm, not merely the outcome. A finalization that succeeded down the wrong
# branch would look identical from the balances alone.
reason="$(jq -r '.settlement.finalization_reason' <<<"$st")"
[[ "$reason" == "SETTLEMENT_FINALIZATION_REASON_AUTHORIZED_EARLY" ]] \
  && ok "recorded as AUTHORIZED_EARLY — the signer's own authority, not the deadline" \
  || fail "finalization reason is $reason, expected AUTHORIZED_EARLY"
ent2="$(jq -r '.entitlement_amount' <<<"$st")"
[[ "$(jq -r '.released_amount' <<<"$st")" == "$ent2" ]] \
  && ok "its whole entitlement is released" || fail "released $(jq -r '.released_amount' <<<"$st") of $ent2"
after="$(balance "$payout2")"
remainder=$((after - before))
(( remainder == ent2 - PAID_OUT )) \
  && ok "participants ($PAID_OUT) + remainder ($remainder) == the entitlement ($ent2), exactly" \
  || fail "participants $PAID_OUT + remainder $remainder != entitlement $ent2"
[[ "$(jq -r '.permissionless_finalization_now' <<<"$st")" == "false" ]] \
  && ok "a finalized settlement advertises no further finalization" \
  || fail "a finalized settlement still advertises permissionless finalization"
chunk 1 operator1 "$S2" 1 "$MAX_CHUNKS" $(pays "$r6:25000") >/dev/null
[[ "$LAST_CODE" != "0" ]] && ok "no chunk survives finalization (code $LAST_CODE)" \
  || fail "a chunk was accepted after finalization"
finalize 1 operator1 "$S2" 1 >/dev/null
[[ "$LAST_CODE" != "0" ]] && ok "nor a second finalization (code $LAST_CODE)" \
  || fail "the settlement finalized twice"

echo
echo "=== 9. a Slot leaves with a settlement still open behind it ==="
leave_height="$(latest_height 0)"
submit_authority inactivate "$S3" "matrix-drill departure" >/dev/null 2>&1
[[ "$LAST_TXCODE" == "0" ]] || fail "inactivate for Slot $S3 refused (code $LAST_TXCODE)"
sleep 4
(( leave_height > EPOCH_LENGTH && leave_height < 2 * EPOCH_LENGTH )) \
  && ok "Slot $S3 left at height $leave_height, inside epoch 2" \
  || fail "Slot $S3 left at height $leave_height, outside epoch 2"
[[ "$(active_count)" == "2" ]] && ok "two Slots active" || fail "active is $(active_count), expected 2"
wait_producing 0 || fail "the chain stopped producing after the departure"
st="$(mq settlement "$S3" 1)"
[[ "$(jq -r '.settlement.settlement_mode' <<<"$st")" == "SETTLEMENT_MODE_TRUSTED_AS" ]] \
  && ok "its epoch-1 settlement survives its Slot leaving the set" \
  || fail "Slot $S3's settlement did not survive removal: $(jq -r '.settlement.settlement_mode' <<<"$st")"
[[ "$(jq -r '.entitlement_amount' <<<"$st")" == "$e1_3" ]] \
  && ok "and still owes the full $e1_3 it earned" || fail "the owed amount changed on departure"

echo
echo "=== 10. epoch 2 closes ==="
wait_epoch_end 2
e2_1="$(entitlement 1 2)"; e2_2="$(entitlement "$S2" 2)"; e2_3="$(entitlement "$S3" 2)"
c2="$(carry)"
echo "  Slot 1: $e2_1    Slot $S2: $e2_2    Slot $S3: $e2_3    carry: $c2"
[[ "$e2_1" == "$e2_2" ]] && ok "the two Slots active all epoch earned identically" \
  || fail "equal participation paid differently: $e2_1 vs $e2_2"
(( e2_3 > 0 && e2_3 < e2_1 )) && ok "the departing Slot earned a partial share ($e2_3)" \
  || fail "the departing Slot earned $e2_3, expected a partial share"
t2=$((e2_1 + e2_2 + e2_3 + c2 - c1))
(( t2 == EPOCH_EMISSION )) && ok "epoch 2 conserved: $t2 == $EPOCH_EMISSION" \
  || fail "epoch 2 sums to $t2, expected $EPOCH_EMISSION"

echo
echo "=== 11. epoch 3 — an absent Slot earns nothing ==="
wait_epoch_end 3
e3_1="$(entitlement 1 3)"; e3_2="$(entitlement "$S2" 3)"; e3_3="$(entitlement "$S3" 3)"
c3="$(carry)"
echo "  Slot 1: $e3_1    Slot $S2: $e3_2    Slot $S3: $e3_3    carry: $c3"
[[ "$e3_3" == "0" ]] && ok "Slot $S3 earned nothing while inactive" || fail "an inactive Slot earned $e3_3"
[[ "$(settlement_slot "$S3" 3)" == "none" ]] \
  && ok "and no settlement was materialized for it" || fail "a settlement exists for an inactive Slot"
[[ "$e3_1" == "$e3_2" ]] && ok "the two remaining Slots split the epoch evenly" \
  || fail "unequal split: $e3_1 vs $e3_2"
t3=$((e3_1 + e3_2 + c3 - c2))
(( t3 == EPOCH_EMISSION )) && ok "epoch 3 conserved: $t3 == $EPOCH_EMISSION" \
  || fail "epoch 3 sums to $t3, expected $EPOCH_EMISSION"

echo
echo "=== 12. the deadline opens what no signer could ==="
# Slot 1 never settled its epoch-1 entitlement and Slot 3's operator never could.
# Both were authorized-signer-only while the participant window was open. Waiting
# is the only thing that changes that, so it is what the drill does.
for s in 1 "$S3"; do
  d=$((SECONDS + 420)); opened=0
  while ((SECONDS < d)); do
    [[ "$(mq settlement "$s" 1 | jq -r '.permissionless_finalization_now')" == "true" ]] && { opened=1; break; }
    sleep 5
  done
  (( opened == 1 )) && ok "Slot $s's epoch-1 settlement is now open to anyone" \
    || { fail "Slot $s's settlement never opened; clock $(mq settlement "$s" 1 | jq -r '.current_settlement_clock') of $(mq settlement "$s" 1 | jq -r '.deadline_clock')"; continue; }
done

echo
echo "=== 12b. past the deadline, no chunk is admitted ==="
# Slot 1's settlement is untouched, so index 0 is its next index, and operator0 IS
# its settlement address. The closed participant window is the only thing wrong.
chunk 0 operator0 1 1 0 $(pays "$r1:25000") >/dev/null
refused "the participant window for slot" "a chunk past the deadline is refused"
[[ "$(mq settlement 1 1 | jq -r '.released_amount')" == "0" ]] \
  && ok "and released nothing — the refusal left the settlement untouched" \
  || fail "a refused chunk changed released_amount"

echo
echo "=== 13. an unrelated operator finalizes both ==="
# The same credential that section 6 refused a chunk from. Distributing another
# Slot's money is never permitted; pushing a settlement past its deadline to its
# own immutable destination is permitted to everyone.
for s in 1 "$S3"; do
  st="$(mq settlement "$s" 1)"
  payout="$(jq -r '.payout_address' <<<"$st")"
  owed="$(jq -r '.entitlement_amount' <<<"$st")"
  before="$(balance "$payout")"
  finalize 1 operator1 "$s" 1 >/dev/null
  [[ "$LAST_CODE" == "0" ]] || { fail "permissionless finalization of Slot $s refused ($LAST_CODE): $LAST_LOG"; continue; }
  sleep 3
  reason="$(mq settlement "$s" 1 | jq -r '.settlement.finalization_reason')"
  [[ "$reason" == "SETTLEMENT_FINALIZATION_REASON_PERMISSIONLESS_AFTER_DEADLINE" ]] \
    && ok "Slot $s recorded PERMISSIONLESS_AFTER_DEADLINE — the deadline arm, not a signer's" \
    || fail "Slot $s finalization reason is $reason, expected PERMISSIONLESS_AFTER_DEADLINE"
  after="$(balance "$payout")"
  moved=$((after - before))
  ok "Slot $s finalized by a signer with no authority over it; $moved reached its payout snapshot"
  [[ "$s" == "$S3" ]] && { (( moved == owed )) \
    && ok "the whole $owed went to the operator — no participant saw any of it" \
    || fail "expected the full $owed to the operator, got $moved"; }
done

echo
echo "=== 14. the chain funded an operator that never had anything ==="
bal3="$(balance "$(addr_of 2)")"
(( bal3 > 0 )) && ok "Slot $S3's operator now holds $bal3, paid entirely out of its own earnings" \
  || fail "Slot $S3's operator still holds nothing"
sink="$("$BIN" keys add sink --keyring-backend test --home "$(node_home 2)" --output json 2>/dev/null | jq -r .address)"
"$BIN" tx bank send "$(addr_of 2)" "$sink" 1000utwlt \
  --from operator2 --keyring-backend test --home "$(node_home 2)" \
  --chain-id "$CHAIN_ID" --node "$(rpc_url 0)" --gas 300000 --yes --output json >/dev/null 2>&1
sleep 4
[[ "$(balance "$sink")" == "1000" ]] && ok "and can sign — the bootstrap closed itself, two epochs late" \
  || fail "it still cannot sign"

echo
echo "=== 15. solvency across everything ==="
mb="$(rq module-balances)"
escrow="$(jq -r '.rewards_balance' <<<"$mb")"
liab="$(jq -r '.outstanding_entitlement_liability' <<<"$mb")"
carry_end="$(jq -r '.carry_forward_remainder' <<<"$mb")"
[[ "$escrow" == "$((liab + carry_end))" ]] && ok "escrow == liability + carry ($escrow)" \
  || fail "escrow $escrow != liability $liab + carry $carry_end"
grand=$((e1_1 + e1_2 + e1_3 + e2_1 + e2_2 + e2_3 + e3_1 + e3_2 + carry_end))
(( grand == 3 * EPOCH_EMISSION )) && ok "three epochs conserved end to end: $grand == $((3 * EPOCH_EMISSION))" \
  || fail "three epochs sum to $grand, expected $((3 * EPOCH_EMISSION))"

echo
echo "============ settlement lifecycle matrix ============"
echo "  epoch 1   $e1_1 / $e1_2 / $e1_3"
echo "  epoch 2   $e2_1 / $e2_2 / $e2_3   (Slot $S3 departs mid-epoch)"
echo "  epoch 3   $e3_1 / $e3_2 / -       (Slot $S3 absent)"
echo "  carry     $carry_end"
echo "====================================================="
if (( FAILURES > 0 )); then echo "settlement lifecycle matrix: FAIL ($FAILURES)" >&2; exit 1; fi
echo "settlement lifecycle matrix: PASS"
