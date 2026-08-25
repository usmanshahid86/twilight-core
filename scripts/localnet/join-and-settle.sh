#!/usr/bin/env bash
set -uo pipefail

# The operational playbook, end to end: a chain starts with one Slot, a second
# Slot joins it, both earn, and each operator settles its OWN entitlement alone.
#
#   one Slot producing
#     -> a second node syncs and is admitted
#     -> its operator account is funded            (the step people forget)
#     -> the epoch closes and BOTH Slots earn
#     -> consensus materializes a Settlement for each
#     -> each operator pays its own participants, from its own node, with its own
#        credential — and cannot touch the other's
#     -> each finalizes, remainder to its own immutable payout snapshot
#
# Two properties this exists to show that no other drill reaches.
#
# A Slot that joins mid-epoch earns LESS than one present from genesis, because
# allocation is by active-block participation. The difference is arithmetic, not
# policy, and the epoch's whole emission still has to be accounted for — nothing
# is created or lost by a validator arriving late.
#
# And settlement authority is per-Slot. Two operators run side by side on the same
# chain, each able to release its own entitlement in full and neither able to
# reach the other's, without any coordination between them.

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
. "$ROOT/scripts/localnet/lib/drill-common.sh"

# Slow enough that a joining node can outpace block production: a single
# validator commits several blocks a second with no consensus round to wait for,
# and a syncing node replays fewer. See validator-growth.sh.
# The second node joins within the first few blocks, so it has almost nothing to
# replay and does not need the slow pace validator-growth requires. 500ms keeps
# the epoch to a few minutes while leaving the joiner ample margin.
export TWILIGHT_LOCALNET_TIMEOUT_COMMIT="${JOIN_TIMEOUT_COMMIT:-500ms}"
# drill-common's wait_all_height iterates NODE_COUNT, which defaults to four.
# This chain has two.
NODE_COUNT=2

EPOCH_LENGTH=360
SUBSIDY=416190
EPOCH_EMISSION=$((EPOCH_LENGTH * SUBSIDY))
PAY_A=25000
PAY_B=35000
FAILURES=0
fail() { echo "  FAIL: $*" >&2; FAILURES=$((FAILURES + 1)); }
ok()   { echo "  ok: $*"; }
note() { echo "  note: $*"; }

need curl; need jq

if pgrep -x twilightd >/dev/null 2>&1; then
  echo "a twilightd process is already running; stop it first" >&2; exit 2
fi
trap '"$ROOT/scripts/localnet/stop.sh" >/dev/null 2>&1 || true; pkill -f "twilightd start" >/dev/null 2>&1 || true' EXIT

mq() { "$BIN" mining-query "$@" --node "$(rpc_url 0)" --output json 2>/dev/null; }
rq() { "$BIN" rewards-query "$@" --node "$(rpc_url 0)" --output json 2>/dev/null; }
balance() {
  "$BIN" query bank balances "$1" --node "$(rpc_url 0)" --output json 2>/dev/null \
    | jq -r '[.balances[]? | select(.denom=="utwlt") | .amount] | first // "0"'
}
addr_of() { sed -n 's/.*"address":"\([^"]*\)".*/\1/p' "$NET/operator$1.json" | head -1; }
wait_rpc() { local d=$((SECONDS + 45)); while ((SECONDS < d)); do [[ -n "$(latest_height "$1")" ]] && return 0; sleep 1; done; return 1; }
wait_producing() {
  local o="$1" s n d; s="$(latest_height "$o")"; [[ -z "$s" ]] && return 1
  d=$((SECONDS + 30))
  while ((SECONDS < d)); do n="$(latest_height "$o")"; [[ -n "$n" ]] && (( n - s >= 2 )) && return 0; sleep 1; done
  return 1
}

# settle_chunk <node-index> <key> <slot> <epoch> <chunk> <recipient> <amount>
# Each operator submits from its OWN node home with its OWN key — no shared
# signer, which is the point of the exercise.
settle_chunk() {
  local n="$1" key="$2" slot="$3" epoch="$4" idx="$5" recip="$6" amt="$7" out
  out="$("$BIN" tx mining submit-settlement-chunk \
      --slot-id "$slot" --epoch "$epoch" --chunk-index "$idx" \
      --payouts "{\"recipient\":\"$recip\",\"amount\":\"$amt\"}" \
      --from "$key" --keyring-backend test --home "$(node_home "$n")" \
      --chain-id "$CHAIN_ID" --node "$(rpc_url 0)" --gas 600000 --yes --output json 2>&1)"
  local code hash
  code="$(jq -r '.code // empty' <<<"$out" 2>/dev/null || true)"
  [[ -z "$code" ]] && { echo "broadcast_error"; return 0; }
  [[ "$code" != "0" ]] && { echo "$code"; return 0; }
  hash="$(jq -r '.txhash' <<<"$out")"
  _wait_tx_code "$hash"
}

echo "=== 1. a chain with a single Slot ==="
NODE_COUNT=1 "$ROOT/scripts/localnet/init.sh" >/dev/null 2>&1 || { echo "init failed" >&2; exit 1; }
NODE_COUNT=1 "$ROOT/scripts/localnet/start.sh" >/dev/null 2>&1 || { echo "start failed" >&2; exit 1; }
wait_rpc 0 || { echo "node0 never answered" >&2; exit 1; }
wait_producing 0 || { echo "the single Slot must produce" >&2; exit 1; }
ok "Slot 1 producing alone"

echo
echo "=== 2. a second Slot joins ==="
"$BIN" init node1 --chain-id "$CHAIN_ID" --home "$(node_home 1)" >/dev/null 2>&1
"$BIN" keys add operator1 --keyring-backend test --home "$(node_home 1)" --output json >"$NET/operator1.json" 2>/dev/null
cp "$(node_home 0)/config/genesis.json" "$(node_home 1)/config/genesis.json"
id0="$("$BIN" tendermint show-node-id --home "$(node_home 0)")"
sed -i.bak \
  -e "s#laddr = \"tcp://127.0.0.1:26657\"#laddr = \"tcp://127.0.0.1:26757\"#" \
  -e "s#laddr = \"tcp://0.0.0.0:26656\"#laddr = \"tcp://0.0.0.0:26756\"#" \
  -e "s#persistent_peers = \"\"#persistent_peers = \"${id0}@127.0.0.1:26656\"#" \
  -e "s#pex = true#pex = false#" -e "s#allow_duplicate_ip = false#allow_duplicate_ip = true#" \
  -e "s#^timeout_commit = .*#timeout_commit = \"${TWILIGHT_LOCALNET_TIMEOUT_COMMIT}\"#" \
  "$(node_home 1)/config/config.toml"
sed -i.bak -e "s#address = \"localhost:9090\"#address = \"localhost:9190\"#" "$(node_home 1)/config/app.toml"
rm -f "$(node_home 1)/config/"*.bak
start_node 1
wait_rpc 1 || fail "node1 RPC never answered"

synced=0; d=$((SECONDS + 90))
while ((SECONDS < d)); do
  if [[ "$(rpc_get 1 /status | jq -r '.result.sync_info.catching_up' 2>/dev/null)" == "false" ]]; then
    h1="$(latest_height 1)"; h0="$(latest_height 0)"
    [[ -n "$h1" && -n "$h0" ]] && (( h0 - h1 <= 2 )) && { synced=1; break; }
  fi
  sleep 1
done
(( synced == 1 )) && ok "node1 caught up before admission" || fail "node1 never caught up"

op1="$(addr_of 1)"
pub1="$(sed -n 's/.*"value": "\([^"]*\)".*/\1/p' "$(node_home 1)/config/priv_validator_key.json" | head -1)"
sid="$(next_slot_id)"
join_height="$(latest_height 0)"
submit_authority register "$op1" "$op1" "$op1" "$pub1" "node1" >/dev/null 2>&1
[[ "$LAST_TXCODE" == "0" ]] || fail "register was refused (code $LAST_TXCODE)"
submit_authority activate "$sid" >/dev/null 2>&1
[[ "$LAST_TXCODE" == "0" ]] || fail "activate was refused (code $LAST_TXCODE)"
sleep 3
[[ "$(active_count)" == "2" ]] && ok "Slot $sid admitted at height ~$join_height; 2 active" \
  || fail "active is $(active_count), expected 2"

echo
echo "=== 3. the joining operator has no account yet ==="
# A Slot admitted after genesis has an address nobody has ever paid. It has no
# account number, so it cannot sign — at any fee, and this chain is feeless. The
# step is invisible until a settlement is attempted, which is why it is asserted
# here rather than assumed.
pre_balance="$(balance "$op1")"
[[ "$pre_balance" == "0" ]] && note "Slot $sid's operator holds nothing and cannot yet sign" \
  || note "Slot $sid's operator already holds $pre_balance"
"$BIN" tx bank send "$(addr_of 0)" "$op1" 1000000utwlt \
  --from operator0 --keyring-backend test --home "$(node_home 0)" \
  --chain-id "$CHAIN_ID" --node "$(rpc_url 0)" --gas 300000 --yes --output json >/dev/null 2>&1
sleep 4
[[ "$(balance "$op1")" != "0" ]] && ok "funded, so it can sign its own transactions" \
  || fail "funding the joining operator failed; it cannot settle"

echo
echo "=== 4. the epoch closes; both Slots earn ==="
# An epoch is 360 blocks; the default 90-second deadline in wait_height_node is
# sized for a handful of blocks, so this passes its own.
for n in 0 1; do
  wait_height_node "$n" $((EPOCH_LENGTH + 2)) 600 || fail "node$n did not reach the epoch boundary"
done
sleep 3
ent1="$(rq entitlement 1 1 | jq -r '.entitlement.entitlement_amount // "0"')"
ent2="$(rq entitlement "$sid" 1 | jq -r '.entitlement.entitlement_amount // "0"')"
carry="$(rq module-balances | jq -r '.carry_forward_remainder // "0"')"
echo "  Slot 1 entitlement: $ent1"
echo "  Slot $sid entitlement: $ent2   (joined mid-epoch)"
[[ "$ent1" != "0" && "$ent2" != "0" ]] && ok "both Slots earned" || fail "an entitlement is missing"
(( ent1 > ent2 )) && ok "the Slot present from genesis earned more — allocation follows participation" \
  || fail "the late Slot earned at least as much as the one present throughout"
total=$((ent1 + ent2 + carry))
(( total == EPOCH_EMISSION )) && ok "entitlements + carry == the epoch emission ($EPOCH_EMISSION)" \
  || fail "entitlements ($ent1 + $ent2) + carry ($carry) = $total, expected $EPOCH_EMISSION"

echo
echo "=== 5. a Settlement per Slot, materialized by consensus ==="
for s in 1 "$sid"; do
  mode="$(mq settlement "$s" 1 | jq -r '.settlement.settlement_mode // "missing"')"
  [[ "$mode" == "SETTLEMENT_MODE_TRUSTED_AS" ]] && ok "Slot $s has an open Settlement" \
    || fail "Slot $s settlement mode is $mode"
done

echo
echo "=== 6. each operator settles its OWN Slot, alone ==="
rcpt1="$("$BIN" keys add p1 --keyring-backend test --home "$(node_home 0)" --output json 2>/dev/null | jq -r .address)"
rcpt2="$("$BIN" keys add p2 --keyring-backend test --home "$(node_home 1)" --output json 2>/dev/null | jq -r .address)"

code="$(settle_chunk 0 operator0 1 1 0 "$rcpt1" "$PAY_A")"
[[ "$code" == "0" ]] && ok "Slot 1's operator paid its own participant" || fail "Slot 1 chunk refused (code $code)"
code="$(settle_chunk 1 operator1 "$sid" 1 0 "$rcpt2" "$PAY_B")"
[[ "$code" == "0" ]] && ok "Slot $sid's operator paid its own participant, from its own node" \
  || fail "Slot $sid chunk refused (code $code)"
sleep 3
[[ "$(balance "$rcpt1")" == "$PAY_A" ]] && ok "participant of Slot 1 holds $PAY_A" || fail "Slot 1 participant holds $(balance "$rcpt1")"
[[ "$(balance "$rcpt2")" == "$PAY_B" ]] && ok "participant of Slot $sid holds $PAY_B" || fail "Slot $sid participant holds $(balance "$rcpt2")"

echo
echo "=== 7. neither operator can settle the other's Slot ==="
code="$(settle_chunk 0 operator0 "$sid" 1 1 "$rcpt1" "$PAY_A")"
[[ "$code" != "0" ]] && ok "Slot 1's operator cannot pay from Slot $sid (code $code)" \
  || fail "Slot 1's operator settled Slot $sid — authority is not per-Slot"
code="$(settle_chunk 1 operator1 1 1 1 "$rcpt2" "$PAY_A")"
[[ "$code" != "0" ]] && ok "Slot $sid's operator cannot pay from Slot 1 (code $code)" \
  || fail "Slot $sid's operator settled Slot 1"

echo
echo "=== 8. each finalizes its own, remainder to its own payout snapshot ==="
for pair in "0:operator0:1" "1:operator1:$sid"; do
  n="${pair%%:*}"; rest="${pair#*:}"; key="${rest%%:*}"; slot="${rest##*:}"
  payout="$(mq settlement "$slot" 1 | jq -r '.payout_address')"
  before="$(balance "$payout")"
  out="$("$BIN" tx mining finalize-settlement --slot-id "$slot" --epoch 1 \
      --from "$key" --keyring-backend test --home "$(node_home "$n")" \
      --chain-id "$CHAIN_ID" --node "$(rpc_url 0)" --gas 600000 --yes --output json 2>&1)"
  hash="$(jq -r '.txhash // ""' <<<"$out" 2>/dev/null)"
  code="$(_wait_tx_code "$hash")"
  [[ "$code" == "0" ]] || { fail "finalize for Slot $slot refused (code $code)"; continue; }
  sleep 2
  state="$(mq settlement "$slot" 1)"
  [[ "$(jq -r '.settlement.finalized' <<<"$state")" == "true" ]] && ok "Slot $slot finalized" \
    || fail "Slot $slot is not finalized"
  ent="$(jq -r '.entitlement_amount' <<<"$state")"
  rel="$(jq -r '.released_amount' <<<"$state")"
  [[ "$rel" == "$ent" ]] && ok "Slot $slot released its whole entitlement ($rel)" \
    || fail "Slot $slot released $rel of $ent"
  after="$(balance "$payout")"
  paid=$(( after - before ))
  (( paid > 0 )) && ok "Slot $slot's remainder ($paid) reached its own payout snapshot" \
    || fail "Slot $slot's remainder did not reach its payout address"
done

echo
echo "=== 9. solvency across both ==="
mb="$(rq module-balances)"
escrow="$(jq -r '.rewards_balance' <<<"$mb")"
liab="$(jq -r '.outstanding_entitlement_liability' <<<"$mb")"
carry2="$(jq -r '.carry_forward_remainder' <<<"$mb")"
[[ "$escrow" == "$((liab + carry2))" ]] && ok "escrow == liability + carry ($escrow)" \
  || fail "escrow $escrow != liability $liab + carry $carry2"

echo
echo "=============== join and settle ==============="
echo "  a Slot joined a running chain, earned a partial-epoch entitlement,"
echo "  and its operator settled it alone from its own node."
echo "  Slot 1: $ent1    Slot $sid: $ent2    carry: $carry"
echo "=============================================="
if (( FAILURES > 0 )); then echo "join and settle: FAIL ($FAILURES)" >&2; exit 1; fi
echo "join and settle: PASS"
