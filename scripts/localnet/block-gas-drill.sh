#!/usr/bin/env bash
set -uo pipefail

# Block-gas ceiling drill  (issue #160, TW-004 of #147)
#
# TW-004 is that `block.max_gas` is -1, so nothing but `max_bytes` bounds a block.
# Its fix is a genesis carrying a finite value. This is the regression that keeps
# that fix once it is made: it launches a localnet whose genesis carries a finite
# ceiling, floods it, and asserts the four things that have to be simultaneously
# true for a ceiling to be worth anything.
#
#   1. the ceiling is FINITE in the params consensus is actually enforcing
#   2. under flood, no block exceeds it
#   3. the excess lands in LATER blocks — deferred, not dropped
#   4. the chain keeps producing blocks throughout
#
# The fourth is not decoration, and it is the reason this is one drill rather than
# three. A halted chain satisfies the first three perfectly: its params are finite,
# none of its blocks exceeds anything, and no transaction was dropped because none
# was processed. A ceiling proven without liveness is a proof about a stopped chain.
#
# # The value here is NOT a proposal
#
# BLOCK_GAS_MAX below is a drill constant chosen so the ceiling binds inside a short
# run. It is not a candidate for production and nothing should read it as one — the
# production value comes from load-calibration.sh, measured on real hardware, and
# has to be ratified. What this drill fixes is the MECHANISM, and the mechanism is
# indifferent to the number.
#
# # Installed through genesis, because there is no other way
#
# x/consensus is wired with the CoreSlot authority, which is `coreslot-authority` —
# a module address with no private key. x/upgrade is reachable anyway because
# x/coreslot proxies it (MsgScheduleUpgrade); x/consensus has no such proxy, so
# MsgUpdateParams can never be signed and block params are reachable only through
# genesis or an upgrade handler. This drill therefore patches the genesis document,
# which is exactly the path #147 names for the fix.
#
# # Two things that would otherwise be measured by accident
#
#   - sender accounts are funded BEFORE the flood, so a minimum-first-funding rule
#     (PR #159) is not what the flood measures
#   - flood transfers go BETWEEN existing senders, so no account is created on the
#     flood path and the gas per transaction stays the thing under test

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# Set before sourcing: drill-common derives its evidence directory from DRILL at
# source time.
export DRILL="block-gas"
. "$ROOT/scripts/localnet/lib/drill-common.sh"
. "$ROOT/scripts/localnet/lib/drill-assert.sh"
. "$ROOT/scripts/localnet/lib/blockgas.sh"
# drill-common enables `set -e`. This drill accounts for its own failures: a failed
# assertion has to reach the verdict file, not end the run before it.
set +e

DRILL_NAME="block-gas drill"

# ---- the contract, pinned ---------------------------------------------------------

NODE_COUNT=4
export NODE_COUNT
BIN="$ROOT/build/twilightd"

# A drill constant, not a proposal. See the header.
readonly BLOCK_GAS_MAX=2000000
readonly FLOOD_TX_GAS=200000
# floor(2000000 / 200000). Both CometBFT's mempool reaping and the SDK's default
# proposal handler admit a transaction while the running total is <= max_gas, so
# the tenth fits exactly and the eleventh does not.
readonly PER_BLOCK_CAP=10
# Six times the per-block cap. The arithmetic is the point: 60 transactions that
# are all included, at no more than 10 per block, CANNOT fit in fewer than 6 blocks.
# That makes "the excess was deferred" a consequence of the ceiling rather than a
# claim about how fast the burst happened to be submitted.
readonly FLOOD_SENDERS=60
readonly MIN_SPAN_BLOCKS=6
readonly FUND_BATCH=10
readonly FUND_PER_SENDER=500000000
readonly FLOOD_AMOUNT=1
readonly BROADCAST_PARALLEL=20
# The shipped genesis default, which this drill replaces. Recorded rather than
# asserted: the day TW-004 is actually closed the default becomes finite, and that
# must not turn this drill red.
readonly STOCK_MAX_BYTES=22020096

# Slower blocks than the localnet default, so the burst lands inside roughly one
# block interval and the mempool genuinely backs up. None of the assertions depend
# on that happening — the span bound above is arithmetic — but a run where it does
# not happen says less.
export TWILIGHT_LOCALNET_TIMEOUT_COMMIT="${BLOCK_GAS_TIMEOUT_COMMIT:-3s}"

RUN_ID="${RUN_ID:-$(date -u +%Y%m%d-%H%M%S)-$$}"
export RUN_ID
DRILL_EVID_DIR="$ROOT/build/localnet/evidence/$RUN_ID/block-gas"
# Throwaway sender keyring, kept OUT of the evidence directory: evidence is what a
# reader inspects afterwards, and sixty test keys are working state, not proof.
WORK="$ROOT/build/localnet/block-gas-$RUN_ID"
KEYRING_DIR="$WORK/keyring"

DRILL_MANDATORY_FILES=(
  genesis-block-params.json consensus-params.json flood.jsonl blocks.csv
  assertions.jsonl summary.csv
)
DRILL_VERDICT_GATES=(
  "ceiling=FINITE" "bound=HELD" "inclusion=DEFERRED_NOT_DROPPED" "liveness=SUSTAINED"
)

# The proof contract. A count alone is a floor — it lets one node's assertion vanish
# while another is duplicated in its place — so the multiset is keyed by
# (assertion, node). Four for anything proven per validator: the genesis document
# each node loaded, the params each node enforces, progress during and after the
# flood, and agreement on a flooded block. One each for the aggregates, every one of
# which is paired with a cardinality check so that "nothing was measured" cannot
# satisfy "nothing exceeded the ceiling".
DRILL_EXPECTED_PHASES=6
DRILL_EXPECTED_ASSERTIONS=33
DRILL_EXPECTED_MULTISET="advanced_after_flood|0:1,advanced_after_flood|1:1,advanced_after_flood|2:1,advanced_after_flood|3:1,advanced_during_flood|0:1,advanced_during_flood|1:1,advanced_during_flood|2:1,advanced_during_flood|3:1,flood_accepted|-:1,flood_agree_app_hash|0:1,flood_agree_app_hash|1:1,flood_agree_app_hash|2:1,flood_agree_app_hash|3:1,flood_blocks_read|-:1,flood_included|-:1,flood_peak_block_gas_characterization|-:1,funding_txs_delivered|-:1,genesis_max_bytes_unchanged|-:1,genesis_max_gas|0:1,genesis_max_gas|1:1,genesis_max_gas|2:1,genesis_max_gas|3:1,inclusion_spans_minimum_blocks|-:1,live_max_gas_is_finite|-:1,live_max_gas|0:1,live_max_gas|1:1,live_max_gas|2:1,live_max_gas|3:1,max_txs_in_a_block_within_cap|-:1,no_block_over_gas_used|-:1,no_block_over_gas_wanted|-:1,senders_have_balance|-:1,senders_resolvable|-:1"

# ---- failure handling ----------------------------------------------------------------
#
# abort() is for setup failures that make the run impossible. It is not a substitute
# for a failed assertion: it finalizes first, so an aborted run still leaves a
# machine-readable verdict rather than a directory a reader cannot tell from a run
# that never started.
FINALIZED=0
finalize_once() {
  (( FINALIZED )) && return 0
  FINALIZED=1
  finalize_verdict "${1:-}"
}
abort() {
  echo "block-gas drill: $*" >&2
  if [[ -n "${DRILL_ASSERT_LOG:-}" ]]; then finalize_once forced; fi
  exit 2
}
cleanup() {
  local rc=$?
  teardown_localnet
  if [[ -n "${DRILL_ASSERT_LOG:-}" ]]; then
    if (( rc != 0 )); then finalize_once forced; else finalize_once; fi
  fi
}

# ---- helpers --------------------------------------------------------------------------

genesis_block_field() { # <node> <max_gas|max_bytes>
  local f="$(node_home "$1")/config/genesis.json"
  [[ -s "$f" ]] || return 1
  jq -er --arg k "$2" '
      .consensus.params.block
    | if type != "object" then error("no block params") else . end
    | if has($k) | not then error("missing key") else .[$k] end
    | tostring
  ' <"$f" 2>/dev/null || return 1
}

# The live ceiling as the APPLICATION reports it, not as CometBFT echoes it.
#
# Both are worth reading and they are not the same claim: x/consensus holds the
# params the state machine was initialised with and would enforce after an update,
# while CometBFT reports what it is reaping against. This drill asserts on the
# application's copy and records CometBFT's alongside it, because a divergence
# between them is exactly the kind of thing a ceiling regression should surface.
app_max_gas() { # <node>
  "$BIN" query consensus params --node "$(rpc_url "$1")" --output json 2>/dev/null \
    | jq -er '.params.block.max_gas | tostring' 2>/dev/null || return 1
}

submit_and_wait() { # <argv...> -> the delivered code, or a reason
  local out hash code i res
  out="$("$@" 2>/dev/null)"
  hash="$(jq -r '.txhash // ""' <<<"$out" 2>/dev/null)"
  code="$(jq -r '.code // empty' <<<"$out" 2>/dev/null)"
  [[ -n "$hash" ]] || { echo "broadcast_error"; return 0; }
  if [[ -n "$code" && "$code" != "0" ]]; then echo "$code"; return 0; fi
  for (( i = 0; i < 90; i++ )); do
    res="$(rpc_get 0 "/tx?hash=0x$hash" 2>/dev/null)" || res=""
    if [[ -n "$res" ]] && jq -e '.result.tx_result' >/dev/null 2>&1 <<<"$res"; then
      jq -r '.result.tx_result.code // 0' <<<"$res"; return 0
    fi
    sleep 1
  done
  echo "not_included"
}

# The fault suite sources this file to exercise the REAL helpers rather than a second,
# more permissive copy written in test shell. Everything above is definitions and
# constants with no side effects; everything below touches the machine.
[[ "${BLOCK_GAS_DRILL_SOURCE_ONLY:-0}" == "1" ]] && return 0

# ---- 0. preflight -----------------------------------------------------------------------

[[ -e "$DRILL_EVID_DIR" ]] && { echo "block-gas drill: $DRILL_EVID_DIR exists; use a fresh RUN_ID" >&2; exit 2; }
command -v jq >/dev/null 2>&1 || { echo "block-gas drill: jq is required" >&2; exit 2; }
command -v curl >/dev/null 2>&1 || { echo "block-gas drill: curl is required" >&2; exit 2; }
# Fails closed when it cannot inspect, so a machine without lsof refuses rather than
# reporting every port free.
require_free_ports || { echo "block-gas drill: refusing to run" >&2; exit 2; }

drill_assert_init "$DRILL_EVID_DIR" || { echo "block-gas drill: could not initialise evidence" >&2; exit 2; }
mkdir -p "$KEYRING_DIR" || { echo "block-gas drill: could not create $KEYRING_DIR" >&2; exit 2; }
trap cleanup EXIT

# ---- 1. a genesis that carries a finite ceiling -------------------------------------------

echo "==> initialising a $NODE_COUNT-node localnet with block.max_gas = $BLOCK_GAS_MAX"
phase_begin
"$ROOT/scripts/localnet/init.sh" >"$DRILL_EVID_DIR/setup.log" 2>&1 || abort "init failed; see setup.log"

# The shipped default, recorded before it is replaced. Not asserted: closing TW-004
# makes it finite, and that must not fail this drill.
SHIPPED_DEFAULT="$(genesis_block_field 0 max_gas)" || abort "the generated genesis has no consensus.params.block.max_gas"

for (( n = 0; n < NODE_COUNT; n++ )); do
  g="$(node_home "$n")/config/genesis.json"
  # The path is REQUIRED to exist before it is written. A blind assignment would
  # create the key wherever jq was pointed, and the drill would then assert
  # confidently about a field the node never reads.
  genesis_block_field "$n" max_gas >/dev/null || abort "node$n genesis has no consensus.params.block.max_gas"
  jq --arg v "$BLOCK_GAS_MAX" '.consensus.params.block.max_gas = $v' <"$g" >"$g.patched" \
    || abort "could not patch node$n genesis"
  mv "$g.patched" "$g" || abort "could not install the patched node$n genesis"
done

for (( n = 0; n < NODE_COUNT; n++ )); do
  if read_required_uint GMG genesis_block_field "$n" max_gas; then
    expect "genesis_max_gas" "$BLOCK_GAS_MAX" "$GMG" "$n"
  else fail "node$n: could not read the patched genesis max_gas" "$n"; fi
done
# Only max_gas moved. A patch that also rewrote max_bytes would change what bounds a
# block, and every later measurement would be about a different experiment.
if read_required_uint GMB genesis_block_field 0 max_bytes; then
  expect "genesis_max_bytes_unchanged" "$STOCK_MAX_BYTES" "$GMB"
else fail "could not read the genesis max_bytes"; fi

jq -n --arg mg "$BLOCK_GAS_MAX" --arg mb "$STOCK_MAX_BYTES" --arg def "$SHIPPED_DEFAULT" \
  '{installed_max_gas: $mg, max_bytes: $mb, shipped_default_max_gas: $def}' \
  >"$DRILL_EVID_DIR/genesis-block-params.json"
phase_end "genesis" "max_gas $SHIPPED_DEFAULT -> $BLOCK_GAS_MAX on $NODE_COUNT nodes"

# ---- 2. the ceiling consensus is enforcing --------------------------------------------------

echo "==> starting the network and reading the live consensus params"
phase_begin
"$ROOT/scripts/localnet/start.sh" >>"$DRILL_EVID_DIR/setup.log" 2>&1
wait_all_height 3 || abort "the network did not reach height 3"

for (( n = 0; n < NODE_COUNT; n++ )); do
  if read_required_uint LMG app_max_gas "$n"; then
    expect "live_max_gas" "$BLOCK_GAS_MAX" "$LMG" "$n"
  else fail "node$n: could not read the live consensus params" "$n"; fi
done

# The TW-004 predicate itself, stated once and explicitly. -1 is the unlimited
# sentinel, but zero and negatives are equally not a ceiling, and a check that only
# compared against -1 would accept them.
if read_required_str CMG live_max_gas "$(http_url 0)"; then
  expect "live_max_gas_is_finite" "yes" "$(max_gas_is_finite "$CMG")"
else fail "could not read the ceiling CometBFT is reaping against"; fi
rpc_get 0 /consensus_params >"$DRILL_EVID_DIR/consensus-params.json" 2>/dev/null
phase_end "params" "every node enforces a finite max_gas of $BLOCK_GAS_MAX"

# ---- 3. funded senders, before the window opens -----------------------------------------------

echo "==> provisioning and funding $FLOOD_SENDERS senders"
phase_begin
SENDER_NAME=(); SENDER_ADDR=(); SENDER_ACC=()
for (( i = 0; i < FLOOD_SENDERS; i++ )); do
  name="flood$i"
  addr="$("$BIN" keys add "$name" --keyring-backend test --home "$KEYRING_DIR" --output json 2>/dev/null \
          | jq -er '.address' 2>/dev/null)"
  [[ -n "$addr" ]] || abort "could not create sender key $name"
  SENDER_NAME[$i]="$name"; SENDER_ADDR[$i]="$addr"
done

FUND_BATCHES=0; FUND_OK=0
i=0
while (( i < FLOOD_SENDERS )); do
  batch=()
  for (( j = i; j < i + FUND_BATCH && j < FLOOD_SENDERS; j++ )); do batch+=("${SENDER_ADDR[$j]}"); done
  # Batched, not one enormous transaction: the ceiling installed above is small by
  # design, and a funding transaction that could not fit in a block would strand the
  # run before the thing under test had been exercised at all.
  code="$(submit_and_wait "$BIN" tx bank multi-send operator0 "${batch[@]}" "${FUND_PER_SENDER}utwlt" \
      --from operator0 --home "$(node_home 0)" --keyring-backend test \
      --chain-id "$CHAIN_ID" --node "$(rpc_url 0)" \
      --gas $(( 150000 + ${#batch[@]} * 60000 )) --fees 0utwlt \
      --broadcast-mode sync --output json -y)"
  FUND_BATCHES=$(( FUND_BATCHES + 1 ))
  [[ "$code" == "0" ]] && FUND_OK=$(( FUND_OK + 1 ))
  i=$(( i + FUND_BATCH ))
done
expect "funding_txs_delivered" "$FUND_BATCHES" "$FUND_OK"

# Existence and balance are separate facts and both matter. An account that resolves
# but holds nothing cannot send, and the flood would then measure a wall of
# insufficient-funds rejections rather than a gas ceiling.
RESOLVED=0; WITH_BALANCE=0
for (( i = 0; i < FLOOD_SENDERS; i++ )); do
  acc="$("$BIN" query auth account-info "${SENDER_ADDR[$i]}" --node "$(rpc_url 0)" --output json 2>/dev/null \
         | jq -er '.info.account_number | tostring' 2>/dev/null)"
  if [[ "$acc" =~ ^[0-9]+$ ]]; then SENDER_ACC[$i]="$acc"; RESOLVED=$(( RESOLVED + 1 )); else SENDER_ACC[$i]=""; fi
  # `bank balance <address> <denom>`, not `bank balances --denom`: this SDK has no
  # --denom flag on the plural form, and the singular answers {"balance":{...}}.
  bal="$("$BIN" query bank balance "${SENDER_ADDR[$i]}" utwlt --node "$(rpc_url 0)" --output json 2>/dev/null \
         | jq -er '.balance.amount | tostring' 2>/dev/null)"
  [[ "$bal" =~ ^[0-9]+$ ]] && (( bal >= FUND_PER_SENDER )) && WITH_BALANCE=$(( WITH_BALANCE + 1 ))
done
expect "senders_resolvable" "$FLOOD_SENDERS" "$RESOLVED"
expect "senders_have_balance" "$FLOOD_SENDERS" "$WITH_BALANCE"
(( RESOLVED == FLOOD_SENDERS )) || abort "senders are not all on chain; refusing to flood"
phase_end "funding" "$FLOOD_SENDERS senders funded before the measurement window"

# ---- 4. the flood ----------------------------------------------------------------------------

echo "==> flooding $FLOOD_SENDERS transactions at $FLOOD_TX_GAS gas each (cap $PER_BLOCK_CAP per block)"
phase_begin

# The liveness marks, taken as late as possible so the window they cover is the
# flood itself.
PRE_HEIGHT=()
for (( n = 0; n < NODE_COUNT; n++ )); do
  if read_required_uint PH app_height "$n"; then PRE_HEIGHT[$n]="$PH"
  else abort "could not mark node$n's height before the flood"; fi
done

FLOOD_LOG="$DRILL_EVID_DIR/flood.jsonl"
: >"$FLOOD_LOG"
# One transaction per sender, fired concurrently. Not many from one sender:
# unordered transactions are not enabled in this app, so the ante accepts only an
# account's committed next sequence and a pipelined sender would be rejected rather
# than queued. Concurrency across ACCOUNTS is the only way to offer more than the
# chain can include.
#
# Recipients are other senders, all of which already exist, so nothing on the flood
# path creates an account and the gas per transaction stays the thing under test.
inflight=0
for (( i = 0; i < FLOOD_SENDERS; i++ )); do
  to=$(( (i + 1) % FLOOD_SENDERS ))
  (
    out="$("$BIN" tx bank send "${SENDER_NAME[$i]}" "${SENDER_ADDR[$to]}" "${FLOOD_AMOUNT}utwlt" \
        --from "${SENDER_NAME[$i]}" --home "$KEYRING_DIR" --keyring-backend test \
        --chain-id "$CHAIN_ID" --node "$(rpc_url 0)" \
        --gas "$FLOOD_TX_GAS" --fees 0utwlt \
        -a "${SENDER_ACC[$i]}" -s 0 \
        --broadcast-mode sync --output json -y 2>/dev/null)"
    # Short lines appended with >> are written atomically, which is what keeps
    # sixty concurrent writers from interleaving mid-record.
    jq -c --arg i "$i" '{sender:($i|tonumber), txhash:(.txhash // ""), check_code:(.code // -1)}' \
      <<<"$out" 2>/dev/null >>"$FLOOD_LOG" \
      || echo "{\"sender\":$i,\"txhash\":\"\",\"check_code\":-1}" >>"$FLOOD_LOG"
  ) &
  inflight=$(( inflight + 1 ))
  if (( inflight >= BROADCAST_PARALLEL )); then wait; inflight=0; fi
done
wait

ACCEPTED="$(jq -s '[.[] | select(.check_code == 0 and .txhash != "")] | length' "$FLOOD_LOG" 2>/dev/null)"
[[ "$ACCEPTED" =~ ^[0-9]+$ ]] || ACCEPTED=0
expect "flood_accepted" "$FLOOD_SENDERS" "$ACCEPTED"

# Inclusion, per transaction. "Included, not dropped" is the whole of assertion 3,
# and the only way to establish it is to find every hash in a block.
INCLUDED=0
HEIGHTS_FILE="$DRILL_EVID_DIR/inclusion-heights.txt"
: >"$HEIGHTS_FILE"
INCLUDED_LOG="$DRILL_EVID_DIR/inclusion.jsonl"
: >"$INCLUDED_LOG"
DEADLINE=$(( SECONDS + 240 ))
while IFS= read -r hash; do
  [[ -n "$hash" ]] || continue
  h=""; dcode=""
  while (( SECONDS < DEADLINE )); do
    res="$(rpc_get 0 "/tx?hash=0x$hash" 2>/dev/null)" || res=""
    if [[ -n "$res" ]] && jq -e '.result.tx_result' >/dev/null 2>&1 <<<"$res"; then
      h="$(jq -r '.result.height // ""' <<<"$res")"
      dcode="$(jq -r '.result.tx_result.code // 0' <<<"$res")"
      break
    fi
    sleep 1
  done
  if [[ "$h" =~ ^[0-9]+$ ]]; then
    INCLUDED=$(( INCLUDED + 1 )); echo "$h" >>"$HEIGHTS_FILE"
  fi
  jq -nc --arg t "$hash" --arg h "${h:-}" --arg c "${dcode:-}" \
    '{txhash:$t, height:$h, deliver_code:$c}' >>"$INCLUDED_LOG"
done < <(jq -r '.txhash | select(. != "")' "$FLOOD_LOG" 2>/dev/null)
expect "flood_included" "$FLOOD_SENDERS" "$INCLUDED"

if [[ ! -s "$HEIGHTS_FILE" ]]; then
  abort "no flood transaction reached a block; there is nothing to measure"
fi
H_MIN="$(LC_ALL=C sort -n "$HEIGHTS_FILE" | head -1)"
H_MAX="$(LC_ALL=C sort -n "$HEIGHTS_FILE" | tail -1)"
DISTINCT="$(LC_ALL=C sort -nu "$HEIGHTS_FILE" | grep -c .)"
[[ "$H_MIN" =~ ^[0-9]+$ && "$H_MAX" =~ ^[0-9]+$ ]] || abort "unusable inclusion heights"

# The deferral claim, as arithmetic rather than as timing. 60 transactions, all
# included, at no more than 10 per block, cannot occupy fewer than 6 blocks — so a
# run where the burst happened to be slow still proves the same thing.
expect "inclusion_spans_minimum_blocks" "yes" \
  "$([[ "$DISTINCT" -ge "$MIN_SPAN_BLOCKS" ]] && echo yes || echo no)"

# Per-block gas across the whole inclusion window.
echo "height,num_txs,gas_wanted,gas_used" >"$DRILL_EVID_DIR/blocks.csv"
WINDOW=$(( H_MAX - H_MIN + 1 ))
BLOCKS_READ=0; OVER_WANTED=0; OVER_USED=0; MAX_TXS=0; PEAK_GAS=0
PRIMARY_HTTP="$(http_url 0)"
for (( h = H_MIN; h <= H_MAX; h++ )); do
  gw="$(block_gas "$PRIMARY_HTTP" "$h" gas_wanted)" || gw=""
  gu="$(block_gas "$PRIMARY_HTTP" "$h" gas_used)"   || gu=""
  nt="$(block_meta "$PRIMARY_HTTP" "$h" num_txs)"   || nt=""
  if [[ ! "$gw" =~ ^[0-9]+$ || ! "$gu" =~ ^[0-9]+$ || ! "$nt" =~ ^[0-9]+$ ]]; then
    printf '%s,-,-,-\n' "$h" >>"$DRILL_EVID_DIR/blocks.csv"
    continue
  fi
  BLOCKS_READ=$(( BLOCKS_READ + 1 ))
  (( gw > BLOCK_GAS_MAX )) && OVER_WANTED=$(( OVER_WANTED + 1 ))
  (( gu > BLOCK_GAS_MAX )) && OVER_USED=$(( OVER_USED + 1 ))
  (( nt > MAX_TXS )) && MAX_TXS="$nt"
  (( gw > PEAK_GAS )) && PEAK_GAS="$gw"
  printf '%s,%s,%s,%s\n' "$h" "$nt" "$gw" "$gu" >>"$DRILL_EVID_DIR/blocks.csv"
done

# The cardinality guard. Without it the two counts below are satisfied by a window
# in which nothing could be read at all — zero blocks over the ceiling is exactly
# what an unreadable window produces.
expect "flood_blocks_read" "$WINDOW" "$BLOCKS_READ"
expect "no_block_over_gas_wanted" "0" "$OVER_WANTED"
expect "no_block_over_gas_used" "0" "$OVER_USED"
expect "max_txs_in_a_block_within_cap" "yes" \
  "$([[ "$MAX_TXS" -le "$PER_BLOCK_CAP" ]] && echo yes || echo no)"

# Characterization, deliberately not a gate. Whether a block reached the ceiling
# depends on how much of the burst was in the mempool when a proposal was built,
# which is a property of the machine rather than of the chain. It is recorded
# because a run in which no block came close says much less than one in which the
# ceiling visibly bound.
record_assert "-" "flood_peak_block_gas_characterization" "observed" \
  "peak=$PEAK_GAS of $BLOCK_GAS_MAX in $DISTINCT blocks (max $MAX_TXS txs)" PASS
phase_end "flood" "$INCLUDED included across $DISTINCT blocks, peak $PEAK_GAS/$BLOCK_GAS_MAX"

# ---- 5. liveness -----------------------------------------------------------------------------

echo "==> confirming the chain kept producing"
phase_begin
LIVENESS_BASE=$FAILURES
for (( n = 0; n < NODE_COUNT; n++ )); do
  if read_required_uint DH app_height "$n"; then
    expect "advanced_during_flood" "yes" \
      "$([[ "$DH" -gt "${PRE_HEIGHT[$n]}" ]] && echo yes || echo no)" "$n"
  else fail "node$n: could not read the application height after the flood" "$n"; fi
done

# A chain that produced blocks WHILE the mempool drained and then stopped has not
# survived the flood, and every ceiling assertion above would still hold. So the
# marks are taken again after the drain and progress is required a second time.
POST_MARK=()
for (( n = 0; n < NODE_COUNT; n++ )); do
  if read_required_uint PM app_height "$n"; then POST_MARK[$n]="$PM"
  else abort "could not mark node$n's height after the flood"; fi
done
POST_MIN="$(min_uint "${POST_MARK[@]}")" || abort "could not derive a common post-flood height"
wait_all_height $(( POST_MIN + 4 )) || fail "the chain did not advance after the flood"
for (( n = 0; n < NODE_COUNT; n++ )); do
  if read_required_uint AH app_height "$n"; then
    expect "advanced_after_flood" "yes" \
      "$([[ "$AH" -gt "${POST_MARK[$n]}" ]] && echo yes || echo no)" "$n"
  else fail "node$n: could not read the application height after the drain" "$n"; fi
done
LIVENESS_OK=$([[ "$FAILURES" -eq "$LIVENESS_BASE" ]] && echo 1 || echo 0)
phase_end "liveness" "all $NODE_COUNT nodes advanced during and after the flood"

# ---- 6. agreement on a flooded block ------------------------------------------------------------

echo "==> comparing app hashes at flooded height $H_MAX"
phase_begin
# H_MAX is inside the flood window by construction, so agreement here is agreement
# about a block the ceiling actually shaped — not about some quiet height after it.
# Every node must have committed past it before the comparison, or a node that is
# merely behind would read as a node that disagrees.
FINAL=()
for (( n = 0; n < NODE_COUNT; n++ )); do
  if read_required_uint FH app_height "$n"; then FINAL+=("$FH")
  else abort "could not read node$n's height for the agreement check"; fi
done
FINAL_MIN="$(min_uint "${FINAL[@]}")" || abort "could not derive a common height"
(( FINAL_MIN > H_MAX )) || abort "not every node has committed past $H_MAX; agreement cannot be checked there"
assert_agreement "flood_agree_app_hash" "$H_MAX" app_hash 0 1 2 3
phase_end "agreement" "all $NODE_COUNT nodes agree on the app hash at $H_MAX"

# ---- verdict ---------------------------------------------------------------------------------

# Every component below is DERIVED from something that was read. A literal here
# would be a claim the verdict file makes and nothing checked — which is worse than
# omitting the component, because it reads as a checked result.
#
# `bound` in particular requires the window to have been fully read: zero blocks
# over the ceiling is also what an unreadable window produces, so BLOCKS_READ has to
# equal the window before "HELD" means anything.
DRILL_VERDICT_LINES=(
  "ceiling=$([[ "$(max_gas_is_finite "${CMG:-}" 2>/dev/null)" == "yes" ]] && echo FINITE || echo UNLIMITED)"
  "bound=$([[ "$OVER_WANTED" -eq 0 && "$OVER_USED" -eq 0 && "$BLOCKS_READ" -eq "$WINDOW" && "$MAX_TXS" -le "$PER_BLOCK_CAP" ]] && echo HELD || echo EXCEEDED)"
  "inclusion=$([[ "$INCLUDED" -eq "$FLOOD_SENDERS" && "$DISTINCT" -ge "$MIN_SPAN_BLOCKS" ]] && echo DEFERRED_NOT_DROPPED || echo INCOMPLETE)"
  "liveness=$([[ "${LIVENESS_OK:-0}" -eq 1 ]] && echo SUSTAINED || echo INTERRUPTED)"
  "installed_max_gas=$BLOCK_GAS_MAX"
  "shipped_default_max_gas=$SHIPPED_DEFAULT"
  "peak_block_gas=$PEAK_GAS"
  "inclusion_blocks=$DISTINCT"
)
finalize_once
