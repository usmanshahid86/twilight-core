#!/usr/bin/env bash
set -uo pipefail

# Block-gas ceiling drill  (issue #160, TW-004 of #147)
#
# TW-004 is that `block.max_gas` is -1, so nothing but `max_bytes` bounds a block.
# Its fix is a genesis carrying a finite value. This is the regression that keeps
# that fix once it is made.
#
# # What a ceiling proof has to establish, and why each half is load-bearing
#
#   1. the ceiling is FINITE in the params consensus is actually enforcing
#   2. the offered backlog EXCEEDED one block's gas capacity, and at least one
#      block was decided BY the ceiling rather than by a shortage of transactions
#   3. every flood transaction was admitted, included AND executed successfully,
#      with the excess landing in later blocks
#   4. the chain kept producing throughout, without stalling and resuming
#
# The second and fourth are the ones a weaker drill omits, and both omissions are
# false PASSes:
#
#   - a slow harness that only ever offers ten transactions at a time satisfies
#     "no block exceeded the ceiling" and "the work spanned six blocks" perfectly,
#     while proving nothing about gas being the binding constraint. So backlog depth
#     is sampled throughout submission and a block that could not have taken one
#     more transaction is required.
#   - a halted chain satisfies every ceiling assertion: its params are finite, none
#     of its blocks exceeds anything, and nothing was dropped because nothing was
#     processed. So progress is required DURING the flood window, measured from
#     block timestamps, not merely before and after it.
#
# Inclusion is also not delivery. A transaction can pass CheckTx, occupy a block and
# fail during execution; counting it as work completed is a false measurement in the
# optimistic direction, so admitted, included and delivered are counted separately
# and the verdict requires the third.
#
# # The value here is NOT a proposal
#
# BLOCK_GAS_MAX below is a drill constant chosen so the ceiling binds inside a short
# run. It is not a candidate for production. The production value comes from
# load-calibration.sh on representative hardware and has to be ratified; what this
# drill fixes is the MECHANISM, which is indifferent to the number.
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
#   - sender accounts are funded BEFORE the flood, so the minimum-first-funding
#     rule does not become the thing under test
#   - flood transfers go BETWEEN existing senders, so nothing on the flood path
#     creates an account and the gas per transaction stays the thing under test

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
# proposal handler admit a transaction while the running total is <= max_gas, so the
# tenth fits exactly and the eleventh does not.
readonly PER_BLOCK_CAP=10
# Six times the per-block cap. The arithmetic is deliberate: 60 transactions that are
# all included, at no more than 10 per block, CANNOT fit in fewer than 6 blocks.
readonly FLOOD_SENDERS=60
readonly MIN_SPAN_BLOCKS=6
readonly FUND_BATCH=10
readonly FUND_PER_SENDER=500000000
readonly FLOOD_AMOUNT=1
readonly BROADCAST_PARALLEL=20
readonly STOCK_MAX_BYTES=22020096

# Slower blocks than the localnet default, so a pre-signed burst is comfortably
# inside one block interval and the mempool genuinely backs up. Unlike the previous
# version of this drill, that is not merely desirable — the saturation gate requires
# it, and a run where it does not happen FAILS rather than passing quietly.
export TWILIGHT_LOCALNET_TIMEOUT_COMMIT="${BLOCK_GAS_TIMEOUT_COMMIT:-3s}"
# The allowed inter-block gap inside the flood window, as a multiple of the
# configured commit cadence. Deliberately generous: its job is to separate "slow
# under stress" from "stalled and later resumed", not to police jitter.
readonly LIVENESS_FACTOR="${BLOCK_GAS_LIVENESS_FACTOR:-4}"
readonly BACKLOG_SAMPLE_SECONDS="${BLOCK_GAS_BACKLOG_SAMPLE_SECONDS:-0.15}"

RUN_ID="${RUN_ID:-$(date -u +%Y%m%d-%H%M%S)-$$}"
export RUN_ID
DRILL_EVID_DIR="$ROOT/build/localnet/evidence/$RUN_ID/block-gas"
# Throwaway working state, kept OUT of the evidence directory: evidence is what a
# reader inspects afterwards, and sixty test keys and their signed payloads are
# working state, not proof.
WORK="$ROOT/build/localnet/block-gas-$RUN_ID"
KEYRING_DIR="$WORK/keyring"
TXDIR="$WORK/tx"

DRILL_MANDATORY_FILES=(
  genesis-block-params.json consensus-params.json flood.jsonl blocks.csv
  backlog.csv gaps.csv assertions.jsonl summary.csv
)
DRILL_VERDICT_GATES=(
  "ceiling=FINITE" "bound=HELD" "saturation=BINDING"
  "inclusion=DELIVERED_AND_DEFERRED" "liveness=SUSTAINED"
)

# The proof contract. A count alone is a floor — it lets one node's assertion vanish
# while another is duplicated in its place — so the multiset is keyed by
# (assertion, node). Four for anything proven per validator; one each for the
# aggregates, every one of which is paired with a cardinality check so that
# "nothing was measured" cannot satisfy "nothing exceeded the ceiling".
DRILL_EXPECTED_PHASES=7
DRILL_EXPECTED_ASSERTIONS=38
DRILL_EXPECTED_MULTISET="advanced_after_flood|0:1,advanced_after_flood|1:1,advanced_after_flood|2:1,advanced_after_flood|3:1,advanced_during_flood|0:1,advanced_during_flood|1:1,advanced_during_flood|2:1,advanced_during_flood|3:1,binding_blocks_observed|-:1,comet_max_gas_is_finite|-:1,flood_accepted|-:1,flood_agree_app_hash|0:1,flood_agree_app_hash|1:1,flood_agree_app_hash|2:1,flood_agree_app_hash|3:1,flood_blocks_read|-:1,flood_delivered_ok|-:1,flood_included|-:1,flood_unresolved|-:1,flood_window_all_blocks_timed|-:1,flood_window_max_gap_within_bound|-:1,funding_txs_delivered|-:1,genesis_max_bytes_unchanged|-:1,genesis_max_gas|0:1,genesis_max_gas|1:1,genesis_max_gas|2:1,genesis_max_gas|3:1,inclusion_spans_minimum_blocks|-:1,live_app_max_gas|0:1,live_app_max_gas|1:1,live_app_max_gas|2:1,live_app_max_gas|3:1,max_txs_in_a_block_within_cap|-:1,no_block_over_gas_used|-:1,no_block_over_gas_wanted|-:1,peak_backlog_exceeds_block_capacity|-:1,senders_have_balance|-:1,senders_resolvable|-:1"

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
BACKLOG_PID=""
stop_backlog_sampler() {
  [[ -n "$BACKLOG_PID" ]] && kill "$BACKLOG_PID" 2>/dev/null
  BACKLOG_PID=""
}
cleanup() {
  local rc=$?
  stop_backlog_sampler
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
# Both are read, and they are not the same claim: x/consensus holds the params the
# state machine was initialised with, while CometBFT reports what it is reaping
# against. A divergence between them is exactly what a ceiling regression should
# surface, so neither reading substitutes for the other.
app_max_gas() { # <node>
  "$BIN" query consensus params --node "$(rpc_url "$1")" --output json 2>/dev/null \
    | jq -er '.params.block.max_gas | tostring' 2>/dev/null || return 1
}

submit_and_wait() { # <argv...> -> the DELIVERED code, or a reason it never arrived
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

COMMIT_MS="$(commit_timeout_ms "$TWILIGHT_LOCALNET_TIMEOUT_COMMIT")" \
  || { echo "block-gas drill: unusable commit timeout '$TWILIGHT_LOCALNET_TIMEOUT_COMMIT'" >&2; exit 2; }
MAX_GAP_MS=$(( COMMIT_MS * LIVENESS_FACTOR ))

drill_assert_init "$DRILL_EVID_DIR" || { echo "block-gas drill: could not initialise evidence" >&2; exit 2; }
mkdir -p "$KEYRING_DIR" "$TXDIR" || { echo "block-gas drill: could not create $WORK" >&2; exit 2; }
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
    expect "live_app_max_gas" "$BLOCK_GAS_MAX" "$LMG" "$n"
  else fail "node$n: could not read the live consensus params" "$n"; fi
done

# The TW-004 predicate itself, stated once and explicitly, against what CometBFT is
# reaping with. -1 is the unlimited sentinel, but zero and negatives are equally not
# a ceiling, and a check that only compared against -1 would accept them.
if read_required_str CMG live_max_gas "$(http_url 0)"; then
  expect "comet_max_gas_is_finite" "yes" "$(max_gas_is_finite "$CMG")"
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
  #
  # The per-recipient amount is far above the minimum-first-funding floor, so the
  # bank SendRestriction admits these account-creating outputs and the flood that
  # follows measures gas rather than that rule.
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

echo "==> pre-signing $FLOOD_SENDERS transactions at $FLOOD_TX_GAS gas each"
phase_begin

# Signed BEFORE the timing window. A harness that spawns the binary inside its own
# burst measures process startup and signing latency and reports it as chain
# behaviour — and, worse here, spreads submission over enough wall-clock time that no
# backlog ever forms and the saturation gate below could never be met honestly.
: >"$WORK/signed.txt"
for (( i = 0; i < FLOOD_SENDERS; i++ )); do
  to=$(( (i + 1) % FLOOD_SENDERS ))
  u="$TXDIR/tx$i.json"
  # Recipients are other senders, all of which already exist, so nothing on the flood
  # path creates an account and the gas per transaction stays the thing under test.
  "$BIN" tx bank send "${SENDER_ADDR[$i]}" "${SENDER_ADDR[$to]}" "${FLOOD_AMOUNT}utwlt" \
    --generate-only --chain-id "$CHAIN_ID" --gas "$FLOOD_TX_GAS" --fees 0utwlt \
    --output json >"$u" 2>/dev/null || abort "could not build the unsigned transaction for sender $i"
  # Sequence 0 for every sender: each was created by the funding transfer and has
  # never signed. Unordered transactions are not enabled in this app, so one account
  # could not pipeline anyway — the concurrency here is across ACCOUNTS.
  sign_and_encode_into SIGNED "$BIN" "$KEYRING_DIR" test "$CHAIN_ID" "${SENDER_NAME[$i]}" \
    "${SENDER_ACC[$i]}" 0 "$u" || abort "could not sign the transaction for sender $i"
  echo "$SIGNED" >>"$WORK/signed.txt"
done

SIGNED_COUNT="$(grep -c . "$WORK/signed.txt" 2>/dev/null || echo 0)"
(( SIGNED_COUNT == FLOOD_SENDERS )) || abort "$SIGNED_COUNT of $FLOOD_SENDERS transactions were signed"

# The liveness marks, taken as late as possible so the window they cover is the
# flood itself.
PRE_HEIGHT=()
for (( n = 0; n < NODE_COUNT; n++ )); do
  if read_required_uint PH app_height "$n"; then PRE_HEIGHT[$n]="$PH"
  else abort "could not mark node$n's height before the flood"; fi
done

# The backlog observer. A single sample taken after submission is not evidence: by
# then a block may already have drained it, and the peak that mattered is gone. This
# samples throughout and keeps every reading, so the PEAK is what the gate uses.
BACKLOG_CSV="$DRILL_EVID_DIR/backlog.csv"
echo "unix_ms,n_txs" >"$BACKLOG_CSV"
PRIMARY_HTTP="$(http_url 0)"
(
  while :; do
    d="$(mempool_depth "$PRIMARY_HTTP" 2>/dev/null)" || d=""
    [[ "$d" =~ ^[0-9]+$ ]] && echo "$(date -u +%s)000,$d" >>"$BACKLOG_CSV"
    sleep "$BACKLOG_SAMPLE_SECONDS"
  done
) &
BACKLOG_PID=$!
# Disowned so the shell does not print a job-control "Terminated" line into the
# drill's own output when the sampler is killed. The evidence stream is read by
# people; a stray signal notice in the middle of the assertions is noise that looks
# like a fault.
disown "$BACKLOG_PID" 2>/dev/null || true

echo "==> broadcasting the pre-signed burst"
FLOOD_LOG="$DRILL_EVID_DIR/flood.jsonl"
: >"$FLOOD_LOG"
# The broadcasters are waited on BY PID, never with a bare `wait`.
#
# A bare `wait` waits for every background job of this shell, and the backlog sampler
# above is one of them — and it never exits. A live run hung here for exactly that
# reason: the burst completed, the chain drained it at ten per block, and the drill
# sat waiting on a sampler that had no intention of finishing.
BPIDS=()
idx=0
while IFS= read -r b64; do
  [[ -n "$b64" ]] || continue
  (
    # Short lines appended with >> are written atomically, which is what keeps sixty
    # concurrent writers from interleaving mid-record.
    if res="$(broadcast_signed "$PRIMARY_HTTP" "$b64")"; then
      set -- $res
      jq -nc --arg i "$idx" --arg c "$1" --arg h "${2:-}" \
        '{sender:($i|tonumber), check_code:($c|tonumber), txhash:$h}' >>"$FLOOD_LOG"
    else
      echo "{\"sender\":$idx,\"check_code\":-1,\"txhash\":\"\"}" >>"$FLOOD_LOG"
    fi
  ) &
  BPIDS+=($!)
  idx=$(( idx + 1 ))
  if (( ${#BPIDS[@]} >= BROADCAST_PARALLEL )); then wait "${BPIDS[@]}"; BPIDS=(); fi
done <"$WORK/signed.txt"
(( ${#BPIDS[@]} > 0 )) && wait "${BPIDS[@]}"
BPIDS=()

ACCEPTED="$(jq -s '[.[] | select(.check_code == 0 and .txhash != "")] | length' "$FLOOD_LOG" 2>/dev/null)"
[[ "$ACCEPTED" =~ ^[0-9]+$ ]] || ACCEPTED=0
expect "flood_accepted" "$FLOOD_SENDERS" "$ACCEPTED"

# Full lifecycle accounting. Admitted, included and DELIVERED are three different
# facts, and only the third is work the chain actually completed. A transaction that
# passes CheckTx, occupies a block and fails during execution consumed block space
# and produced nothing — counting it as included-and-therefore-fine is a false PASS
# in the optimistic direction.
INCLUDED=0; DELIVERED_OK=0; UNRESOLVED=0
HEIGHTS_FILE="$DRILL_EVID_DIR/inclusion-heights.txt"
: >"$HEIGHTS_FILE"
LIFECYCLE="$DRILL_EVID_DIR/inclusion.jsonl"
: >"$LIFECYCLE"
DEADLINE=$(( SECONDS + 300 ))
while IFS= read -r hash; do
  [[ -n "$hash" ]] || continue
  h=""; dcode=""; status="ACCEPTED_PENDING"
  while (( SECONDS < DEADLINE )); do
    if out="$(tx_outcome "$PRIMARY_HTTP" "$hash")"; then
      set -- $out; h="$1"; dcode="$2"
      break
    fi
    sleep 1
  done
  if [[ "$h" =~ ^[0-9]+$ ]]; then
    INCLUDED=$(( INCLUDED + 1 )); echo "$h" >>"$HEIGHTS_FILE"
    if [[ "$dcode" == "0" ]]; then
      DELIVERED_OK=$(( DELIVERED_OK + 1 )); status="DELIVERED_OK"
    else
      status="DELIVERED_FAILED"
    fi
  else
    UNRESOLVED=$(( UNRESOLVED + 1 )); status="NOT_FOUND_TIMEOUT"
  fi
  jq -nc --arg t "$hash" --arg h "${h:-}" --arg c "${dcode:-}" --arg s "$status" \
    '{txhash:$t, height:$h, deliver_code:$c, status:$s}' >>"$LIFECYCLE"
done < <(jq -r 'select(.check_code == 0) | .txhash | select(. != "")' "$FLOOD_LOG" 2>/dev/null)

stop_backlog_sampler

expect "flood_included" "$FLOOD_SENDERS" "$INCLUDED"
# The gate the previous contract lacked. Inclusion was treated as success, so a
# transaction that occupied a block and reverted still counted as deferred-not-dropped.
expect "flood_delivered_ok" "$FLOOD_SENDERS" "$DELIVERED_OK"
# An unresolved transaction is a MEASUREMENT failure, never folded into either side.
expect "flood_unresolved" "0" "$UNRESOLVED"

[[ -s "$HEIGHTS_FILE" ]] || abort "no flood transaction reached a block; there is nothing to measure"
H_MIN="$(LC_ALL=C sort -n "$HEIGHTS_FILE" | head -1)"
H_MAX="$(LC_ALL=C sort -n "$HEIGHTS_FILE" | tail -1)"
DISTINCT="$(LC_ALL=C sort -nu "$HEIGHTS_FILE" | grep -c .)"
[[ "$H_MIN" =~ ^[0-9]+$ && "$H_MAX" =~ ^[0-9]+$ ]] || abort "unusable inclusion heights"

# Per-block gas across the whole inclusion window.
echo "height,num_txs,gas_wanted,gas_used,binds_ceiling" >"$DRILL_EVID_DIR/blocks.csv"
WINDOW=$(( H_MAX - H_MIN + 1 ))
BLOCKS_READ=0; OVER_WANTED=0; OVER_USED=0; MAX_TXS=0; PEAK_GAS=0; BINDING_BLOCKS=0
for (( h = H_MIN; h <= H_MAX; h++ )); do
  gw="$(block_gas "$PRIMARY_HTTP" "$h" gas_wanted)" || gw=""
  gu="$(block_gas "$PRIMARY_HTTP" "$h" gas_used)"   || gu=""
  nt="$(block_meta "$PRIMARY_HTTP" "$h" num_txs)"   || nt=""
  if [[ ! "$gw" =~ ^[0-9]+$ || ! "$gu" =~ ^[0-9]+$ || ! "$nt" =~ ^[0-9]+$ ]]; then
    printf '%s,-,-,-,-\n' "$h" >>"$DRILL_EVID_DIR/blocks.csv"
    continue
  fi
  BLOCKS_READ=$(( BLOCKS_READ + 1 ))
  (( gw > BLOCK_GAS_MAX )) && OVER_WANTED=$(( OVER_WANTED + 1 ))
  (( gu > BLOCK_GAS_MAX )) && OVER_USED=$(( OVER_USED + 1 ))
  (( nt > MAX_TXS )) && MAX_TXS="$nt"
  (( gw > PEAK_GAS )) && PEAK_GAS="$gw"
  binds="$(block_binds_gas_ceiling "$gw" "$FLOOD_TX_GAS" "$BLOCK_GAS_MAX")"
  [[ "$binds" == "yes" ]] && BINDING_BLOCKS=$(( BINDING_BLOCKS + 1 ))
  printf '%s,%s,%s,%s,%s\n' "$h" "$nt" "$gw" "$gu" "$binds" >>"$DRILL_EVID_DIR/blocks.csv"
done

# The cardinality guard. Without it the two counts below are satisfied by a window in
# which nothing could be read at all — zero blocks over the ceiling is exactly what an
# unreadable window produces.
expect "flood_blocks_read" "$WINDOW" "$BLOCKS_READ"
expect "no_block_over_gas_wanted" "0" "$OVER_WANTED"
expect "no_block_over_gas_used" "0" "$OVER_USED"
phase_end "flood" "$DELIVERED_OK delivered of $FLOOD_SENDERS across $DISTINCT blocks"

# ---- 5. the ceiling was the binding constraint ------------------------------------------------

echo "==> proving the gas ceiling bound, rather than merely not being exceeded"
phase_begin

# Backlog. Without this, a harness slow enough to offer ten transactions at a time
# satisfies every other assertion in this drill while proving nothing: the blocks
# would look identical. The peak is taken across the whole submission and drain, and
# the sampler's own coverage is proven by requiring at least one reading.
PEAK_BACKLOG="$(awk -F, 'NR > 1 && $2 ~ /^[0-9]+$/ { if ($2 + 0 > m) m = $2 + 0 } END { print m + 0 }' "$BACKLOG_CSV" 2>/dev/null)"
BACKLOG_SAMPLES="$(awk -F, 'NR > 1 && $2 ~ /^[0-9]+$/ { n++ } END { print n + 0 }' "$BACKLOG_CSV" 2>/dev/null)"
[[ "$PEAK_BACKLOG" =~ ^[0-9]+$ ]] || PEAK_BACKLOG=0
(( BACKLOG_SAMPLES > 0 )) || fail "the backlog sampler produced no readings; saturation cannot be established"
expect "peak_backlog_exceeds_block_capacity" "yes" \
  "$([[ "$PEAK_BACKLOG" -gt "$PER_BLOCK_CAP" ]] && echo yes || echo no)"

# And at least one block that the CEILING decided. The test is whether one more
# transaction of the flood's size would have exceeded max_gas: a large block does not
# show the ceiling bound, but a block that could not have taken another does.
expect "binding_blocks_observed" "yes" \
  "$([[ "$BINDING_BLOCKS" -ge 1 ]] && echo yes || echo no)"
expect "max_txs_in_a_block_within_cap" "yes" \
  "$([[ "$MAX_TXS" -le "$PER_BLOCK_CAP" ]] && echo yes || echo no)"
# The deferral claim, as arithmetic rather than as timing. 60 transactions, all
# included, at no more than 10 per block, cannot occupy fewer than 6 blocks.
expect "inclusion_spans_minimum_blocks" "yes" \
  "$([[ "$DISTINCT" -ge "$MIN_SPAN_BLOCKS" ]] && echo yes || echo no)"
phase_end "saturation" "peak backlog $PEAK_BACKLOG over cap $PER_BLOCK_CAP; $BINDING_BLOCKS binding blocks; peak gas $PEAK_GAS"

# ---- 6. liveness, inside the flood window and after it ----------------------------------------

echo "==> measuring inter-block gaps across the flood window"
phase_begin
LIVENESS_BASE=$FAILURES

# Progress measured from BLOCK TIMESTAMPS, not from two height readings taken either
# side of the flood. A chain that stalls in the middle of the window and resumes
# afterwards satisfies "height went up"; only the gap between consecutive blocks
# separates "slow under stress" from "stopped and came back".
#
# The window starts one block before the first inclusion, so a stall that happens
# exactly at the onset of load is inside what is measured.
GAP_FROM=$(( H_MIN > 1 ? H_MIN - 1 : 1 ))
# Timing goes through the SHARED observation helper, the same one the calibration rig
# uses. It refuses a malformed or calendar-invalid timestamp and any interval that is
# zero or backwards, and a refusal here does NOT count as a timed block — so
# flood_window_all_blocks_timed below fails closed on exactly those readings rather
# than quietly proving liveness over a shorter series than it claims.
echo "height,unix_ms,gap_ms" >"$DRILL_EVID_DIR/gaps.csv"
TIMED=0; EXPECTED_TIMED=$(( H_MAX - GAP_FROM + 1 )); MAX_GAP=0; PREV_MS=""
for (( h = GAP_FROM; h <= H_MAX; h++ )); do
  t="$(block_meta "$PRIMARY_HTTP" "$h" time)" || t=""
  if [[ -z "$t" ]] || ! observe_block_time OBS_MS OBS_GAP "$PREV_MS" "$t"; then
    printf '%s,-,-\n' "$h" >>"$DRILL_EVID_DIR/gaps.csv"
    continue
  fi
  TIMED=$(( TIMED + 1 ))
  if [[ -n "$OBS_GAP" ]]; then
    (( OBS_GAP > MAX_GAP )) && MAX_GAP="$OBS_GAP"
    printf '%s,%s,%s\n' "$h" "$OBS_MS" "$OBS_GAP" >>"$DRILL_EVID_DIR/gaps.csv"
  else
    printf '%s,%s,-\n' "$h" "$OBS_MS" >>"$DRILL_EVID_DIR/gaps.csv"
  fi
  PREV_MS="$OBS_MS"
done
# Paired with the bound, for the same reason as every other aggregate here: a window
# where no block could be timed has a maximum gap of zero.
expect "flood_window_all_blocks_timed" "$EXPECTED_TIMED" "$TIMED"
expect "flood_window_max_gap_within_bound" "yes" \
  "$([[ "$MAX_GAP" -le "$MAX_GAP_MS" ]] && echo yes || echo no)"

for (( n = 0; n < NODE_COUNT; n++ )); do
  if read_required_uint DH app_height "$n"; then
    expect "advanced_during_flood" "yes" \
      "$([[ "$DH" -gt "${PRE_HEIGHT[$n]}" ]] && echo yes || echo no)" "$n"
  else fail "node$n: could not read the application height after the flood" "$n"; fi
done

# A chain that produced blocks WHILE the mempool drained and then stopped has not
# survived the flood, and every ceiling assertion above would still hold.
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
phase_end "liveness" "max flood-window gap ${MAX_GAP}ms, bound ${MAX_GAP_MS}ms"

# ---- 7. agreement on a flooded block ------------------------------------------------------------

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

# Every component below is DERIVED from something that was read. A literal here would
# be a claim the verdict file makes and nothing checked — which is worse than
# omitting the component, because it reads as a checked result.
#
# `bound` in particular requires the window to have been fully read: zero blocks over
# the ceiling is also what an unreadable window produces.
DRILL_VERDICT_LINES=(
  "ceiling=$([[ "$(max_gas_is_finite "${CMG:-}" 2>/dev/null)" == "yes" ]] && echo FINITE || echo UNLIMITED)"
  "bound=$([[ "$OVER_WANTED" -eq 0 && "$OVER_USED" -eq 0 && "$BLOCKS_READ" -eq "$WINDOW" && "$MAX_TXS" -le "$PER_BLOCK_CAP" ]] && echo HELD || echo EXCEEDED)"
  "saturation=$([[ "$PEAK_BACKLOG" -gt "$PER_BLOCK_CAP" && "$BINDING_BLOCKS" -ge 1 ]] && echo BINDING || echo NOT_DEMONSTRATED)"
  "inclusion=$([[ "$DELIVERED_OK" -eq "$FLOOD_SENDERS" && "$UNRESOLVED" -eq 0 && "$DISTINCT" -ge "$MIN_SPAN_BLOCKS" ]] && echo DELIVERED_AND_DEFERRED || echo INCOMPLETE)"
  "liveness=$([[ "${LIVENESS_OK:-0}" -eq 1 ]] && echo SUSTAINED || echo INTERRUPTED)"
  "installed_max_gas=$BLOCK_GAS_MAX"
  "shipped_default_max_gas=$SHIPPED_DEFAULT"
  "peak_block_gas=$PEAK_GAS"
  "binding_blocks=$BINDING_BLOCKS"
  "peak_backlog=$PEAK_BACKLOG"
  "inclusion_blocks=$DISTINCT"
  "max_flood_gap_ms=$MAX_GAP"
  "allowed_gap_ms=$MAX_GAP_MS"
)
finalize_once
