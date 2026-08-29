#!/usr/bin/env bash
set -uo pipefail

# Block-gas load calibration  (issue #160, TW-004 of #147)
#
# This is a MEASUREMENT RIG, not a test. It has no pass/fail verdict and it does
# not choose a value. Its output is a CSV and a summary; the decision about what
# `block.max_gas` should be is a human one, taken against this data and recorded
# somewhere durable.
#
# That distinction is the point. #147 says the ceiling "must be derived from a load
# test", and a script that printed PASS would be asserting the derivation had
# already happened.
#
# # What it measures, and why gas alone is not the answer
#
# Per block: gas_wanted, gas_used, block time delta, transaction count, block size,
# and PERMANENT STATE GROWTH — accounts added, and application-DB growth where the
# filesystem is reachable.
#
# The last one is why this is not simply a gas benchmark. Gas here is metered but
# unpaid, and it prices execution, not storage. TW-006 measured ~16k permanent
# accounts per 1 MB of MsgMultiSend, and observed that "gas does not limit this
# under MaxGas = -1; bytes bind". A ceiling chosen only from block time can
# therefore be perfectly defensible and still admit a block that adds tens of
# megabytes of non-deletable account state. Both axes are recorded so the chosen
# number can be checked against both.
#
# # The worst case, deliberately unconstrained
#
# The load is maximum message throughput to FRESH recipients, with no assumption
# about the rest of the anti-spam cluster. TW-005's ante work and TW-006's funding
# rule and MultiSend output cap all reduce what an attacker can offer, so a ceiling
# derived without them stays valid as they land. Derived WITH them, it would have to
# be re-derived every time one moved.
#
# # Two things that would otherwise be measured by accident
#
# 1. Sender accounts are funded BEFORE the measurement window opens. A bank
#    SendRestriction requiring a minimum first funding (PR #159) would otherwise be
#    the thing under measurement, not block gas.
#
# 2. Unordered transactions are NOT enabled in this app: `authmodulev1.Module`
#    leaves EnableUnorderedTransactions unset, so the ante rejects any sequence but
#    the account's committed next one. One account therefore cannot pipeline. The
#    ramp is over the number of concurrently-submitting ACCOUNTS for that reason,
#    not over transactions per account — a rig that ramped the latter would measure
#    a wall of sequence-mismatch rejections and call it saturation.
#
# # Targeting
#
# Everything is configurable because the run that matters will not happen on a
# laptop: binary path, RPC endpoints, chain id, funding key and keyring all come
# from the environment. Nothing here assumes a local four-node localnet exists, and
# nothing here starts or stops a node.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
. "$ROOT/scripts/localnet/lib/blockgas.sh"

# ---- configuration ------------------------------------------------------------

CAL_BIN="${CAL_BIN:-$ROOT/build/twilightd}"
# Comma-separated RPC base URLs. The first is where transactions are broadcast and
# where blocks are read; the rest are only used to confirm the network agrees on a
# height, so a saturated node is not mistaken for a saturated chain.
CAL_NODES="${CAL_NODES:-http://127.0.0.1:26657,http://127.0.0.1:26757,http://127.0.0.1:26857,http://127.0.0.1:26957}"
CAL_CHAIN_ID="${CAL_CHAIN_ID:-twilight-localnet-1}"
CAL_HOME="${CAL_HOME:-/tmp/twilight-localnet/node0}"
CAL_FUNDER="${CAL_FUNDER:-operator0}"
CAL_KEYRING_BACKEND="${CAL_KEYRING_BACKEND:-test}"

# The ramp. Each step is a number of sender accounts submitting concurrently, which
# is the offered transaction count per wave. Raise these for a serious run: the
# defaults are sized so a first pass finishes in a few minutes, not so they saturate
# production hardware.
CAL_STEPS="${CAL_STEPS:-16,32,64,128,256}"
CAL_WAVES_PER_STEP="${CAL_WAVES_PER_STEP:-3}"
CAL_PARALLEL="${CAL_PARALLEL:-16}"

# Transaction mix. `send` is one output; `multisend` is CAL_MULTISEND_OUTPUTS
# outputs, which is the shape TW-006 measured and the one that converts bytes into
# permanent accounts fastest. `mixed` alternates by sender index.
CAL_MIX="${CAL_MIX:-mixed}"
CAL_MULTISEND_OUTPUTS="${CAL_MULTISEND_OUTPUTS:-8}"

CAL_TX_GAS="${CAL_TX_GAS:-200000}"
CAL_TX_GAS_MULTISEND="${CAL_TX_GAS_MULTISEND:-}"
# At or above the 10,000 utwlt first-funding floor proposed for TW-006, so the rig
# measures the same thing whether or not that rule is in place. An attacker willing
# to pay the floor is the case that matters anyway.
CAL_SEND_AMOUNT="${CAL_SEND_AMOUNT:-10000}"
CAL_FUND_PER_SENDER="${CAL_FUND_PER_SENDER:-500000000}"
CAL_FUND_BATCH="${CAL_FUND_BATCH:-10}"

CAL_TARGET_BLOCK_MS="${CAL_TARGET_BLOCK_MS:-2000}"
CAL_SETTLE_BLOCKS="${CAL_SETTLE_BLOCKS:-4}"
CAL_MAX_SECONDS="${CAL_MAX_SECONDS:-2400}"

# Account-count sampling is one height-pinned query per block. It is the reliable
# permanent-growth signal, and it is also the slowest part of collection, so it can
# be turned off — at the cost of the axis this rig exists to keep in view.
CAL_ACCOUNT_SAMPLING="${CAL_ACCOUNT_SAMPLING:-1}"
# Optional path to a node home on THIS machine. Without it the application-DB
# column is recorded as unavailable rather than as zero.
CAL_NODE_HOME="${CAL_NODE_HOME:-}"

RUN_ID="${RUN_ID:-$(date -u +%Y%m%d-%H%M%S)-$$}"
CAL_OUT="${CAL_OUT:-$ROOT/build/localnet/calibration/$RUN_ID}"
# Fresh recipients are derived from this salt so a run is reproducible by passing
# the salt back. Defaulting it to a run-scoped value keeps two runs against the same
# network from reusing addresses, which would silently stop the account-growth
# column from growing.
CAL_RECIPIENT_SALT="${CAL_RECIPIENT_SALT:-$(( ($(date -u +%s) % 1000000) + 1 ))}"

[[ -n "$CAL_TX_GAS_MULTISEND" ]] || CAL_TX_GAS_MULTISEND=$(( CAL_TX_GAS + CAL_MULTISEND_OUTPUTS * 40000 ))

# ---- reporting ----------------------------------------------------------------

say()  { echo "$*"; }
note() { echo "  note: $*"; }
warn() { echo "  warn: $*" >&2; }
abort() { echo "calibration: $*" >&2; exit 2; }

# ---- node handles --------------------------------------------------------------

NODES=()
IFS=',' read -r -a NODES <<<"$CAL_NODES"
(( ${#NODES[@]} > 0 )) || abort "CAL_NODES is empty"
PRIMARY="${NODES[0]}"

KEYRING_DIR="$CAL_OUT/keyring"
SENDERS_FILE="$CAL_OUT/senders.csv"       # index,name,address,account_number
BLOCKS_CSV="$CAL_OUT/blocks.csv"
STEPS_CSV="$CAL_OUT/steps.csv"
BROADCAST_LOG="$CAL_OUT/broadcast.jsonl"
APPDB_CSV="$CAL_OUT/appdb-samples.csv"

primary_height() {
  local body
  body="$(blockgas_get "$PRIMARY" /status)" || return 1
  jq -er '.result.sync_info.latest_block_height | tostring' <<<"$body" 2>/dev/null || return 1
}

wait_heights() { # <count> — advance this many blocks on the primary
  local by="$1" start now deadline
  start="$(primary_height)" || return 1
  deadline=$(( SECONDS + 300 ))
  while (( SECONDS < deadline )); do
    now="$(primary_height)" || return 1
    (( now >= start + by )) && { echo "$now"; return 0; }
    sleep 1
  done
  return 1
}

# ---- 0. preflight ---------------------------------------------------------------

command -v curl >/dev/null 2>&1 || abort "curl is required"
command -v jq   >/dev/null 2>&1 || abort "jq is required"
[[ -x "$CAL_BIN" ]] || abort "CAL_BIN is not executable: $CAL_BIN"
[[ -e "$CAL_OUT" ]] && abort "$CAL_OUT exists; use a fresh RUN_ID or CAL_OUT"
mkdir -p "$CAL_OUT" "$KEYRING_DIR" || abort "could not create $CAL_OUT"

say "==> calibration run $RUN_ID"
say "    binary   $CAL_BIN"
say "    chain    $CAL_CHAIN_ID via $PRIMARY (${#NODES[@]} endpoints)"
say "    output   $CAL_OUT"

H_PREFLIGHT="$(primary_height)" || abort "no height from $PRIMARY; is the network reachable?"
[[ "$H_PREFLIGHT" =~ ^[0-9]+$ ]] || abort "unusable height from $PRIMARY"

# The starting posture, recorded before anything is offered. A run against an
# already-finite ceiling is legitimate — that is how a candidate value gets
# re-tested — but it must be visible in the artifact.
START_MAX_GAS="$(live_max_gas "$PRIMARY")" || abort "could not read the live consensus params"
START_MAX_GAS_FINITE="$(max_gas_is_finite "$START_MAX_GAS")"
say "    max_gas  $START_MAX_GAS (finite: $START_MAX_GAS_FINITE)"

# The address encoder has to agree with the chain's own codec before it mints a
# single recipient. A wrong encoder yields well-formed addresses the chain refuses,
# and the run would then be a measurement of its own rejection rate.
HRP="$("$CAL_BIN" query auth bech32-prefix --node "$PRIMARY" --output json 2>/dev/null | jq -er '.bech32_prefix' 2>/dev/null)"
[[ -n "$HRP" ]] || HRP="twilight"
bech32_agrees_with_binary "$CAL_BIN" "$HRP" \
  || abort "the local address encoder disagrees with $CAL_BIN for prefix '$HRP'"
bech32_init "$HRP" || abort "could not initialise the address encoder"

FUNDER_ADDR="$("$CAL_BIN" keys show "$CAL_FUNDER" -a --keyring-backend "$CAL_KEYRING_BACKEND" --home "$CAL_HOME" 2>/dev/null)"
[[ -n "$FUNDER_ADDR" ]] || abort "could not resolve funding key '$CAL_FUNDER' in $CAL_HOME"

STEP_LIST=()
IFS=',' read -r -a STEP_LIST <<<"$CAL_STEPS"
MAX_SENDERS=0
for s in "${STEP_LIST[@]}"; do
  [[ "$s" =~ ^[0-9]+$ ]] && (( s > 0 )) || abort "CAL_STEPS entry '$s' is not a positive integer"
  (( s > MAX_SENDERS )) && MAX_SENDERS="$s"
done
say "    ramp     ${STEP_LIST[*]} senders, $CAL_WAVES_PER_STEP waves each, mix=$CAL_MIX"

# Recipient addresses pack (salt, wave, sender, output) into disjoint bit ranges of
# one 64-bit value. Checked rather than assumed: an overflow of any field would
# silently alias two transactions onto the same recipient, and the only symptom
# would be an account-growth column that quietly stopped growing.
TOTAL_WAVES=$(( ${#STEP_LIST[@]} * CAL_WAVES_PER_STEP ))
(( CAL_RECIPIENT_SALT >= 1 && CAL_RECIPIENT_SALT < 1048576 )) \
  || abort "CAL_RECIPIENT_SALT must be in [1, 2^20)"
(( TOTAL_WAVES < 1048576 )) || abort "too many waves to address distinctly (limit 2^20)"
(( MAX_SENDERS < 4096 )) || abort "CAL_STEPS asks for $MAX_SENDERS senders; the address scheme allows under 4096"
(( CAL_MULTISEND_OUTPUTS >= 1 && CAL_MULTISEND_OUTPUTS < 256 )) \
  || abort "CAL_MULTISEND_OUTPUTS must be in [1, 256)"
case "$CAL_MIX" in send|multisend|mixed) ;; *) abort "CAL_MIX must be send, multisend or mixed" ;; esac

# Every configured endpoint must be reachable and on the same chain. Reading blocks
# from one node while the rest of the network is elsewhere would produce a clean CSV
# describing a chain nobody else is on.
for url in "${NODES[@]}"; do
  net="$(blockgas_get "$url" /status | jq -er '.result.node_info.network' 2>/dev/null)"
  [[ "$net" == "$CAL_CHAIN_ID" ]] || abort "endpoint $url serves '$net', not '$CAL_CHAIN_ID'"
done

# ---- 1. provisioning: senders exist and are funded BEFORE measurement -------------

say "==> provisioning $MAX_SENDERS sender accounts"
: >"$SENDERS_FILE"
SENDER_ADDR=(); SENDER_NAME=(); SENDER_ACCNUM=(); SENDER_SEQ=()

for (( i = 0; i < MAX_SENDERS; i++ )); do
  name="cal$i"
  addr="$("$CAL_BIN" keys add "$name" --keyring-backend test --home "$KEYRING_DIR" --output json 2>/dev/null \
          | jq -er '.address' 2>/dev/null)"
  [[ -n "$addr" ]] || abort "could not create sender key $name"
  SENDER_NAME[$i]="$name"; SENDER_ADDR[$i]="$addr"
  (( (i + 1) % 50 == 0 )) && say "    $((i + 1))/$MAX_SENDERS keys"
done

# submit_and_wait <argv...> -> the DELIVERED code, or a reason it never arrived.
#
# A sync broadcast reports CheckTx only, so a message refused during execution
# answers 0 there. Provisioning that trusted the CheckTx code would proceed to
# measure a chain where the funding never landed.
submit_and_wait() {
  local out hash code i res
  out="$("$@" 2>/dev/null)"
  hash="$(jq -r '.txhash // ""' <<<"$out" 2>/dev/null)"
  code="$(jq -r '.code // empty' <<<"$out" 2>/dev/null)"
  if [[ -z "$hash" ]]; then echo "broadcast_error"; return 0; fi
  if [[ -n "$code" && "$code" != "0" ]]; then echo "$code"; return 0; fi
  for (( i = 0; i < 60; i++ )); do
    res="$(blockgas_get "$PRIMARY" "/tx?hash=0x$hash")" || res=""
    if [[ -n "$res" ]] && jq -e '.result.tx_result' >/dev/null 2>&1 <<<"$res"; then
      jq -r '.result.tx_result.code // 0' <<<"$res"; return 0
    fi
    sleep 1
  done
  echo "not_included"
}

say "==> funding senders ($CAL_FUND_PER_SENDER utwlt each, in batches of $CAL_FUND_BATCH)"
FUND_BATCHES=0; FUND_FAILURES=0
i=0
while (( i < MAX_SENDERS )); do
  batch=()
  for (( j = i; j < i + CAL_FUND_BATCH && j < MAX_SENDERS; j++ )); do batch+=("${SENDER_ADDR[$j]}"); done
  # multi-send funds many accounts in one transaction. Batched rather than sent as
  # one enormous transaction because the network under test may ALREADY have a
  # finite ceiling — re-testing a candidate value is a normal reason to run this —
  # and a funding transaction that cannot fit in a block would strand the run
  # before it started.
  code="$(submit_and_wait "$CAL_BIN" tx bank multi-send "$CAL_FUNDER" "${batch[@]}" "${CAL_FUND_PER_SENDER}utwlt" \
      --from "$CAL_FUNDER" --home "$CAL_HOME" --keyring-backend "$CAL_KEYRING_BACKEND" \
      --chain-id "$CAL_CHAIN_ID" --node "$PRIMARY" \
      --gas $(( 150000 + ${#batch[@]} * 60000 )) --fees 0utwlt \
      --broadcast-mode sync --output json -y)"
  FUND_BATCHES=$(( FUND_BATCHES + 1 ))
  [[ "$code" == "0" ]] || { FUND_FAILURES=$(( FUND_FAILURES + 1 )); warn "funding batch at offset $i returned $code"; }
  i=$(( i + CAL_FUND_BATCH ))
done
(( FUND_FAILURES == 0 )) || abort "$FUND_FAILURES of $FUND_BATCHES funding batches did not deliver"

# Every sender must be RESOLVABLE on chain before the window opens. This is the
# guard that keeps a first-funding rule from being the thing under measurement: if
# an account does not exist here, its first flood transaction would be creating it.
say "==> confirming every sender exists on chain"
FUNDED=0
for (( i = 0; i < MAX_SENDERS; i++ )); do
  info="$("$CAL_BIN" query auth account-info "${SENDER_ADDR[$i]}" --node "$PRIMARY" --output json 2>/dev/null)"
  accnum="$(jq -er '.info.account_number | tostring' <<<"$info" 2>/dev/null)"
  if [[ "$accnum" =~ ^[0-9]+$ ]]; then
    SENDER_ACCNUM[$i]="$accnum"; SENDER_SEQ[$i]=0; FUNDED=$(( FUNDED + 1 ))
    printf '%s,%s,%s,%s\n' "$i" "${SENDER_NAME[$i]}" "${SENDER_ADDR[$i]}" "$accnum" >>"$SENDERS_FILE"
  else
    warn "sender $i (${SENDER_ADDR[$i]}) has no on-chain account"
  fi
done
(( FUNDED == MAX_SENDERS )) || abort "$FUNDED of $MAX_SENDERS senders are funded; refusing to measure a funding rule"
say "    $FUNDED senders funded and resolvable"

# ---- 2. the ramp -----------------------------------------------------------------

# recipient_into <var> <sender-index> <output-index> — a fresh, never-before-funded
# address.
#
# Derived from (salt, wave serial, sender, output) rather than from a counter. A
# counter is the obvious implementation and it is wrong here: every transaction is
# built in a BACKGROUND job, so each increment happens in a forked shell and is lost
# when that shell exits. The parent's counter would stay at zero, every concurrent
# sender would target the same address, and after the first wave nothing would be a
# fresh account at all — leaving the permanent-growth column flat and looking like
# good news.
#
# The four components are packed into disjoint bit ranges so no two transactions in
# a run can collide. WAVE_SERIAL is a parent-scope value, read by the fork rather
# than written by it.
WAVE_SERIAL=0
recipient_into() {
  local __var="$1" idx="$2" n="$3" hex
  printf -v hex '%040x' $(( (CAL_RECIPIENT_SALT << 40) + (WAVE_SERIAL << 20) + (idx << 8) + n ))
  bech32_encode_into "$__var" "$hex" || return 1
}

# The application-DB sampler. Disk size moves with compaction as well as with
# writes, so it corroborates the account count rather than replacing it — but a
# ceiling that looks fine on gas and grows the store by megabytes a block is
# exactly the outcome this rig exists to make visible.
APPDB_PID=""
start_appdb_sampler() {
  [[ -n "$CAL_NODE_HOME" ]] || { note "CAL_NODE_HOME unset; application-DB growth will be recorded as unavailable"; return 0; }
  local db="$CAL_NODE_HOME/data/application.db"
  [[ -d "$db" ]] || { warn "no application.db under $CAL_NODE_HOME; DB growth unavailable"; CAL_NODE_HOME=""; return 0; }
  echo "height,appdb_kb" >"$APPDB_CSV"
  (
    local last="" h kb
    while :; do
      h="$(primary_height 2>/dev/null)" || h=""
      if [[ "$h" =~ ^[0-9]+$ && "$h" != "$last" ]]; then
        kb="$(du -sk "$db" 2>/dev/null | awk '{print $1}')"
        [[ "$kb" =~ ^[0-9]+$ ]] && echo "$h,$kb" >>"$APPDB_CSV"
        last="$h"
      fi
      sleep 0.2
    done
  ) &
  APPDB_PID=$!
}
stop_appdb_sampler() { [[ -n "$APPDB_PID" ]] && kill "$APPDB_PID" 2>/dev/null; APPDB_PID=""; }
trap 'stop_appdb_sampler' EXIT

# broadcast_one <sender-index> — build, sign and broadcast one transaction.
#
# Account number and sequence are both passed explicitly, so the CLI never has to
# query the account. Without that, every transaction in a wave would open its own
# round trip to a node that is deliberately being saturated, and the burst would
# pace itself against the very congestion it is supposed to create.
broadcast_one() {
  local idx="$1" kind="$2" out one
  local -a to=()
  local n
  if [[ "$kind" == "multisend" ]]; then
    for (( n = 0; n < CAL_MULTISEND_OUTPUTS; n++ )); do
      recipient_into one "$idx" "$n" || return 1
      to+=("$one")
    done
    out="$("$CAL_BIN" tx bank multi-send "${SENDER_NAME[$idx]}" "${to[@]}" "${CAL_SEND_AMOUNT}utwlt" \
        --from "${SENDER_NAME[$idx]}" --home "$KEYRING_DIR" --keyring-backend test \
        --chain-id "$CAL_CHAIN_ID" --node "$PRIMARY" \
        --gas "$CAL_TX_GAS_MULTISEND" --fees 0utwlt \
        -a "${SENDER_ACCNUM[$idx]}" -s "${SENDER_SEQ[$idx]}" \
        --broadcast-mode sync --output json -y 2>/dev/null)"
  else
    recipient_into one "$idx" 0 || return 1
    out="$("$CAL_BIN" tx bank send "${SENDER_NAME[$idx]}" "$one" "${CAL_SEND_AMOUNT}utwlt" \
        --from "${SENDER_NAME[$idx]}" --home "$KEYRING_DIR" --keyring-backend test \
        --chain-id "$CAL_CHAIN_ID" --node "$PRIMARY" \
        --gas "$CAL_TX_GAS" --fees 0utwlt \
        -a "${SENDER_ACCNUM[$idx]}" -s "${SENDER_SEQ[$idx]}" \
        --broadcast-mode sync --output json -y 2>/dev/null)"
  fi
  jq -c --arg i "$idx" --arg k "$kind" \
     '{sender:($i|tonumber), kind:$k, txhash:(.txhash // ""), check_code:(.code // -1)}' \
     <<<"$out" 2>/dev/null >>"$BROADCAST_LOG" \
    || echo "{\"sender\":$idx,\"kind\":\"$kind\",\"txhash\":\"\",\"check_code\":-1}" >>"$BROADCAST_LOG"
}

: >"$BROADCAST_LOG"
echo "step,senders,wave,h_start,h_end,offered" >"$STEPS_CSV"
start_appdb_sampler

RAMP_START=""; RAMP_END=""
STEP_IDX=0
DEADLINE=$(( SECONDS + CAL_MAX_SECONDS ))

for senders in "${STEP_LIST[@]}"; do
  STEP_IDX=$(( STEP_IDX + 1 ))
  say "==> step $STEP_IDX: $senders concurrent senders x $CAL_WAVES_PER_STEP waves"
  for (( wave = 1; wave <= CAL_WAVES_PER_STEP; wave++ )); do
    if (( SECONDS >= DEADLINE )); then
      warn "CAL_MAX_SECONDS reached; stopping the ramp after step $STEP_IDX wave $((wave - 1))"
      break 2
    fi
    WAVE_SERIAL=$(( WAVE_SERIAL + 1 ))
    h_start="$(primary_height)" || abort "lost the primary endpoint mid-ramp"
    [[ -n "$RAMP_START" ]] || RAMP_START="$h_start"
    inflight=0
    for (( s = 0; s < senders; s++ )); do
      kind="send"
      case "$CAL_MIX" in
        multisend) kind="multisend" ;;
        mixed) (( s % 2 == 1 )) && kind="multisend" ;;
      esac
      broadcast_one "$s" "$kind" &
      inflight=$(( inflight + 1 ))
      if (( inflight >= CAL_PARALLEL )); then wait; inflight=0; fi
    done
    wait
    # Every sender advanced by exactly one committed transaction per wave, whether
    # or not it was accepted. Incrementing regardless would desynchronise the
    # sequence on a rejection and silently reject the rest of the run; so the
    # sequences are re-read from chain state at the end of each wave instead of
    # being assumed.
    h_end="$(wait_heights "$CAL_SETTLE_BLOCKS")" || abort "the chain stopped advancing during step $STEP_IDX"
    for (( s = 0; s < senders; s++ )); do
      seq="$("$CAL_BIN" query auth account-info "${SENDER_ADDR[$s]}" --node "$PRIMARY" --output json 2>/dev/null \
             | jq -er '.info.sequence | tostring' 2>/dev/null)"
      [[ "$seq" =~ ^[0-9]+$ ]] && SENDER_SEQ[$s]="$seq"
    done
    RAMP_END="$h_end"
    printf '%s,%s,%s,%s,%s,%s\n' "$STEP_IDX" "$senders" "$wave" "$h_start" "$h_end" "$senders" >>"$STEPS_CSV"
    say "    wave $wave: offered $senders, heights $h_start..$h_end"
  done
done
stop_appdb_sampler

[[ -n "$RAMP_START" && -n "$RAMP_END" ]] || abort "the ramp produced no measured height range"

# ---- 3. per-block collection -------------------------------------------------------

say "==> collecting per-block metrics for heights $RAMP_START..$RAMP_END"
echo "height,step,unix_ms,block_time_delta_ms,tx_count,block_bytes,gas_wanted,gas_used,accounts,account_delta,appdb_kb,appdb_delta_kb" >"$BLOCKS_CSV"

step_of_height() { # <height> -> the ramp step that height belongs to, or "-"
  awk -F, -v h="$1" 'NR > 1 && h >= $4 && h <= $5 { print $1; exit }' "$STEPS_CSV" 2>/dev/null
}
appdb_at() { # <height> -> sampled KB, or empty
  [[ -s "$APPDB_CSV" ]] || return 1
  awk -F, -v h="$1" 'NR > 1 && $1 == h { print $2; exit }' "$APPDB_CSV" 2>/dev/null
}
accounts_at() { # <height> -> total accounts at that height, or empty
  (( CAL_ACCOUNT_SAMPLING == 1 )) || return 1
  "$CAL_BIN" query auth accounts --page-limit 1 --page-count-total --height "$1" \
    --node "$PRIMARY" --output json 2>/dev/null | jq -er '.pagination.total | tostring' 2>/dev/null
}

PREV_MS=""; PREV_ACC=""; PREV_DB=""
UNREADABLE_BLOCKS=0; UNSAMPLED_ACCOUNTS=0
for (( h = RAMP_START; h <= RAMP_END; h++ )); do
  gw="$(block_gas "$PRIMARY" "$h" gas_wanted)"      || { gw=""; }
  gu="$(block_gas "$PRIMARY" "$h" gas_used)"        || { gu=""; }
  nt="$(block_meta "$PRIMARY" "$h" num_txs)"        || { nt=""; }
  bs="$(block_meta "$PRIMARY" "$h" block_size)"     || { bs=""; }
  tm="$(block_meta "$PRIMARY" "$h" time)"           || { tm=""; }
  if [[ -z "$gw" || -z "$nt" || -z "$tm" ]]; then
    UNREADABLE_BLOCKS=$(( UNREADABLE_BLOCKS + 1 ))
    printf '%s,%s,-,-,-,-,-,-,-,-,-,-\n' "$h" "$(step_of_height "$h")" >>"$BLOCKS_CSV"
    continue
  fi
  ms="$(rfc3339_to_ms "$tm")" || ms=""
  if [[ -n "$ms" && -n "$PREV_MS" ]]; then dms=$(( ms - PREV_MS )); else dms="-"; fi
  acc="$(accounts_at "$h")" || acc=""
  if [[ -n "$acc" ]]; then
    if [[ -n "$PREV_ACC" ]]; then dacc=$(( acc - PREV_ACC )); else dacc="-"; fi
    PREV_ACC="$acc"
  else
    acc="-"; dacc="-"; (( CAL_ACCOUNT_SAMPLING == 1 )) && UNSAMPLED_ACCOUNTS=$(( UNSAMPLED_ACCOUNTS + 1 ))
  fi
  db="$(appdb_at "$h")" || db=""
  if [[ -n "$db" ]]; then
    if [[ -n "$PREV_DB" ]]; then ddb=$(( db - PREV_DB )); else ddb="-"; fi
    PREV_DB="$db"
  else db="-"; ddb="-"; fi
  printf '%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s\n' \
    "$h" "$(step_of_height "$h")" "${ms:--}" "$dms" "$nt" "$bs" "$gw" "$gu" "$acc" "$dacc" "$db" "$ddb" >>"$BLOCKS_CSV"
  [[ -n "$ms" ]] && PREV_MS="$ms"
done

# ---- 4. summary --------------------------------------------------------------------
#
# Aggregates, and where block time first crosses the target. That crossing is the
# KNEE, and naming it is as far as this goes: the ceiling that gets shipped has to
# account for the heaviest legitimate block too (#107), for the state-growth column
# below, and for hardware that is not this machine.

ACCEPTED="$(jq -s '[.[] | select(.check_code == 0)] | length' "$BROADCAST_LOG" 2>/dev/null || echo 0)"
OFFERED="$(wc -l <"$BROADCAST_LOG" | tr -d ' ')"
REJECTED=$(( OFFERED - ACCEPTED ))

say ""
say "=== calibration summary  (run $RUN_ID) ==="
say "offered $OFFERED transactions, $ACCEPTED accepted at CheckTx, $REJECTED rejected"
say "measured heights $RAMP_START..$RAMP_END; $UNREADABLE_BLOCKS unreadable"
(( UNSAMPLED_ACCOUNTS > 0 )) && warn "$UNSAMPLED_ACCOUNTS blocks have no account sample (a pruning node cannot answer height-pinned queries); those rows read '-', which is NOT zero growth"
say "starting block.max_gas: $START_MAX_GAS (finite: $START_MAX_GAS_FINITE)"
# The endpoint the blocks were read from is one node. If the rest of the network is
# far behind it, the CSV describes a node under load rather than a chain under load,
# and that is a different measurement.
SPREAD=""
for url in "${NODES[@]}"; do
  eh="$(blockgas_get "$url" /status | jq -er '.result.sync_info.latest_block_height | tostring' 2>/dev/null)"
  SPREAD="$SPREAD ${eh:-unreachable}"
done
say "endpoint heights after the ramp:$SPREAD"
say ""
printf '%-5s %-8s %-10s %-10s %-12s %-9s %-9s %-9s\n' step senders med_ms max_ms max_gas_w max_txs acct_add db_kb_add
# Two files: the step table carries the offered sender count, the block table
# carries the measurements. Reading the sender count out of the block rows was not
# an option — it is not in them, and awk would have printed an empty column rather
# than complaining.
awk -F, '
  FNR == 1 { next }
  FILENAME == stepfile { senders[$1 + 0] = $2; next }
  $2 == "-" { next }
  {
    step = $2 + 0
    if ($4 != "-") { d[step] = d[step] "," $4; }
    if ($7 != "-" && $7 + 0 > mg[step]) mg[step] = $7 + 0
    if ($5 != "-" && $5 + 0 > mt[step]) mt[step] = $5 + 0
    if ($10 != "-") ad[step] += $10 + 0
    if ($12 != "-") dd[step] += $12 + 0
    seen[step] = 1
  }
  END {
    n = 0
    for (s in seen) { order[n++] = s + 0 }
    for (i = 0; i < n; i++) for (j = i + 1; j < n; j++) if (order[j] < order[i]) { t = order[i]; order[i] = order[j]; order[j] = t }
    for (i = 0; i < n; i++) {
      s = order[i]
      cnt = split(substr(d[s], 2), vals, ",")
      # Median, not mean: one slow block during a restart or a snapshot would drag
      # a mean across the target and name the wrong step as the knee.
      for (a = 1; a <= cnt; a++) for (b = a + 1; b <= cnt; b++) if (vals[b] + 0 < vals[a] + 0) { t = vals[a]; vals[a] = vals[b]; vals[b] = t }
      med = (cnt > 0) ? vals[int((cnt + 1) / 2)] + 0 : -1
      mx = (cnt > 0) ? vals[cnt] + 0 : -1
      printf "%-5s %-8s %-10s %-10s %-12s %-9s %-9s %-9s\n", \
        s, (s in senders ? senders[s] : "?"), med, mx, mg[s] + 0, mt[s] + 0, ad[s] + 0, dd[s] + 0
    }
  }
' stepfile="$STEPS_CSV" "$STEPS_CSV" "$BLOCKS_CSV"

say ""
KNEE="$(awk -F, -v target="$CAL_TARGET_BLOCK_MS" '
  NR == 1 { next }
  $2 == "-" || $4 == "-" { next }
  { step = $2; if ($4 + 0 > target) { over[step]++ } total[step]++ }
  END {
    n = 0; for (s in total) order[n++] = s + 0
    for (i = 0; i < n; i++) for (j = i + 1; j < n; j++) if (order[j] < order[i]) { t = order[i]; order[i] = order[j]; order[j] = t }
    for (i = 0; i < n; i++) { s = order[i]; if (over[s] * 2 > total[s]) { print s; exit } }
  }' "$BLOCKS_CSV")"

if [[ -n "$KNEE" ]]; then
  say "knee: block time first exceeds ${CAL_TARGET_BLOCK_MS}ms for a majority of blocks at step $KNEE"
else
  say "knee: block time never exceeded ${CAL_TARGET_BLOCK_MS}ms — the ramp did not reach saturation."
  say "      raise CAL_STEPS (more concurrent senders) and re-run; the top step is a floor, not a limit."
fi
say ""
say "This rig does not choose block.max_gas. Read the knee together with:"
say "  - the permanent state growth columns (account_delta, appdb_delta_kb): a ceiling"
say "    that is comfortable on time can still admit unbounded non-deletable state"
say "  - the heaviest LEGITIMATE block, which bounds the value from below (#107)"
say "  - the hardware this ran on, which is part of the result"
say ""
say "artifacts:"
say "  $BLOCKS_CSV"
say "  $STEPS_CSV"
say "  $SENDERS_FILE"
say "  $BROADCAST_LOG"
[[ -s "$APPDB_CSV" ]] && say "  $APPDB_CSV"
exit 0
