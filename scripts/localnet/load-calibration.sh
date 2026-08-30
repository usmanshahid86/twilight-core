#!/usr/bin/env bash
set -uo pipefail

# Block-gas load calibration  (issue #160, TW-004 of #147)
#
# A MEASUREMENT INSTRUMENT. It produces two separate answers, and conflating them
# is the mistake this whole design exists to prevent:
#
#   measurement_valid   was the experiment itself trustworthy?
#   candidate_max_gas   a data-derived estimate, when it was
#
# The second is never emitted from a run that failed the first. Neither is a TW-004
# verdict: a candidate is an input to human ratification, not a ratified parameter.
# Nothing here chooses what the chain ships.
#
# # What it measures, and why gas alone is not the answer
#
# Per block: gas_wanted, gas_used, block interval, transaction count, block size, and
# PERMANENT STATE GROWTH — accounts added, and application-DB growth where the
# filesystem is reachable.
#
# The last is why this is not a gas benchmark. Gas here is metered but unpaid, and it
# prices execution, not storage. TW-006 measured roughly 16k permanent accounts per
# 1 MB of MsgMultiSend and observed that "gas does not limit this under MaxGas = -1;
# bytes bind". A ceiling chosen only from block time can be entirely defensible and
# still admit a block that adds tens of megabytes of non-deletable account state.
#
# When the account axis is unavailable, that is reported as unavailable. It is never
# reported as zero growth — zero is the optimistic reading, and an instrument must
# not invent the reassuring answer for data it does not have.
#
# # The worst case, deliberately unconstrained
#
# Maximum message throughput to FRESH recipients, with no assumption about the rest
# of the anti-spam cluster. TW-005's ante work and TW-006's funding rule and
# MultiSend output cap all REDUCE what an attacker can offer, so a ceiling derived
# without them stays valid as they land.
#
# # Five things that would otherwise be measured by accident
#
# 1. Sender accounts are funded BEFORE the measurement window, so the bank
#    SendRestriction's minimum first funding is not the thing under measurement.
#
# 2. Unordered transactions are NOT enabled in this app: `authmodulev1.Module` leaves
#    EnableUnorderedTransactions unset, so the ante accepts only an account's
#    committed next sequence. One account cannot pipeline. The ramp is therefore over
#    the number of concurrently submitting ACCOUNTS, and a rig that ramped
#    transactions per account would measure a wall of self-inflicted sequence
#    rejections and call it saturation.
#
# 3. A wave is not finished when a fixed number of blocks have passed. A transaction
#    can be in a node's CheckTx state while the canonical account query still returns
#    the old committed sequence, so the next wave would sign stale and reject itself.
#    Every wave is DRAINED to terminal state, and every sender's committed sequence is
#    verified to have advanced, before the next wave starts.
#
# 4. Transactions are signed BEFORE the timing window and submitted as bytes. A
#    harness that spawns the binary inside its own burst measures process startup and
#    signing latency and reports it as chain admission.
#
# 5. Recipient addresses come from a wide per-run namespace whose freshness is
#    VERIFIED against the chain, not assumed. A short cycling salt repeats within
#    days; a repeat against a persistent network silently reuses accounts that
#    already exist and flattens the growth column while everything else looks healthy.
#
# # Targeting
#
# Everything is configurable because the run that matters will not happen on a
# laptop. Nothing here assumes a local four-node localnet, and nothing starts or
# stops a node.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
. "$ROOT/scripts/localnet/lib/blockgas.sh"

# ---- configuration ------------------------------------------------------------

CAL_BIN="${CAL_BIN:-$ROOT/build/twilightd}"
# Comma-separated RPC base URLs. The first is where transactions are broadcast and
# blocks are read; all of them are checked for agreement, because a saturated RPC
# node must not be mistaken for a saturated chain.
CAL_NODES="${CAL_NODES:-http://127.0.0.1:26657,http://127.0.0.1:26757,http://127.0.0.1:26857,http://127.0.0.1:26957}"
CAL_CHAIN_ID="${CAL_CHAIN_ID:-twilight-localnet-1}"
CAL_HOME="${CAL_HOME:-/tmp/twilight-localnet/node0}"
CAL_FUNDER="${CAL_FUNDER:-operator0}"
CAL_KEYRING_BACKEND="${CAL_KEYRING_BACKEND:-test}"

# The ramp. Each step is a number of sender accounts submitting one transaction each
# per wave, which is the offered transaction count per wave. Raise these for a
# representative run: the defaults finish in minutes and are a smoke ramp, not a
# calibration of production hardware.
CAL_STEPS="${CAL_STEPS:-16,32,64,128,256}"
CAL_WAVES_PER_STEP="${CAL_WAVES_PER_STEP:-3}"
# Bounded broadcaster. The ACTUAL achieved concurrency and submission rate are
# measured and reported; a step is never labelled with a concurrency the harness did
# not deliver.
CAL_PARALLEL="${CAL_PARALLEL:-32}"

CAL_MIX="${CAL_MIX:-mixed}"
CAL_MULTISEND_OUTPUTS="${CAL_MULTISEND_OUTPUTS:-8}"
CAL_TX_GAS="${CAL_TX_GAS:-200000}"
CAL_TX_GAS_MULTISEND="${CAL_TX_GAS_MULTISEND:-}"
# At or above the minimum-first-funding floor, so the rig measures the same thing
# whether or not that rule is in place. An attacker willing to pay the floor is the
# case that matters anyway.
CAL_SEND_AMOUNT="${CAL_SEND_AMOUNT:-10000}"
CAL_FUND_PER_SENDER="${CAL_FUND_PER_SENDER:-500000000}"
CAL_FUND_BATCH="${CAL_FUND_BATCH:-10}"

# The service target the knee is defined against.
CAL_TARGET_BLOCK_MS="${CAL_TARGET_BLOCK_MS:-2000}"
# The minimum number of USABLE BLOCK INTERVALS a step must contribute before its p95
# may inform the knee.
#
# Intervals, not blocks, and the distinction is load-bearing: a block can be ACTIVE
# and supply no interval — it is the first block of a range, or its timestamp did not
# parse. Checking block cardinality would let a step hold twenty active blocks and one
# usable interval while satisfying a minimum of twenty, and the p95 would then be a
# one-sample statistic. The cardinality gated here is exactly the one the p95 is
# computed from.
#
# A p95 over one or two observations is arithmetically defined and is not a tail
# measurement. The default is deliberately high enough that the shipped smoke ramp
# will NOT earn a candidate: a representative calibration has to be sized for it, and
# a smoke that wants to exercise the mechanics must lower this EXPLICITLY. The
# effective value is recorded in the manifest and result so a reviewer can see which
# regime a result came from.
CAL_MIN_ACTIVE_INTERVALS_PER_STEP="${CAL_MIN_ACTIVE_INTERVALS_PER_STEP:-20}"
# Quiet blocks AFTER a wave has fully drained. These are recorded as QUIET and are
# excluded from the load response; they are not, and must never be, the evidence that
# the wave finished.
CAL_QUIET_BLOCKS="${CAL_QUIET_BLOCKS:-2}"
CAL_DRAIN_TIMEOUT_S="${CAL_DRAIN_TIMEOUT_S:-240}"
CAL_MAX_SECONDS="${CAL_MAX_SECONDS:-3600}"

# Account-count sampling is one height-pinned query per active block. It is the
# permanent-growth signal, and disabling it does not make growth zero — it makes the
# state-growth axis UNAVAILABLE, and the candidate is downgraded accordingly.
CAL_ACCOUNT_SAMPLING="${CAL_ACCOUNT_SAMPLING:-1}"
# Optional path to a node home on THIS machine, for application-DB corroboration.
CAL_NODE_HOME="${CAL_NODE_HOME:-}"

# Policy inputs. The tool does not invent limits: absent these, it reports the
# candidate as performance-derived only and says the state-growth guard is
# unratified.
CAL_MAX_ACCOUNTS_PER_BLOCK="${CAL_MAX_ACCOUNTS_PER_BLOCK:-}"
CAL_MAX_APPDB_KB_PER_BLOCK="${CAL_MAX_APPDB_KB_PER_BLOCK:-}"
# The heaviest LEGITIMATE block's gas requirement (#107). A ceiling below it would
# break the protocol's own work, so a candidate cannot be called shippable without
# this comparison.
CAL_LEGITIMATE_GAS_FLOOR="${CAL_LEGITIMATE_GAS_FLOOR:-}"
# Conservative reduction applied to the estimated knee, in basis points.
CAL_GAS_SAFETY_BPS="${CAL_GAS_SAFETY_BPS:-2000}"
# How far endpoints may diverge in height before the run is not measuring one chain.
CAL_ENDPOINT_LAG_BLOCKS="${CAL_ENDPOINT_LAG_BLOCKS:-3}"

RUN_ID="${RUN_ID:-$(date -u +%Y%m%d-%H%M%S)-$$}"
CAL_OUT="${CAL_OUT:-$ROOT/build/localnet/calibration/$RUN_ID}"
# The recipient namespace seed. Wide and drawn per run, recorded in the manifest so a
# run is reproducible by passing it back.
CAL_RECIPIENT_NONCE="${CAL_RECIPIENT_NONCE:-}"

[[ -n "$CAL_TX_GAS_MULTISEND" ]] || CAL_TX_GAS_MULTISEND=$(( CAL_TX_GAS + CAL_MULTISEND_OUTPUTS * 40000 ))

# ---- reporting ----------------------------------------------------------------

say()  { echo "$*"; }
note() { echo "  note: $*"; }
warn() { echo "  warn: $*" >&2; }
abort() { echo "calibration: $*" >&2; exit 2; }

# Validity findings accumulate here. A run is valid only if this stays empty.
INVALID_REASONS=""
invalidate() { INVALID_REASONS="${INVALID_REASONS}${INVALID_REASONS:+; }$1"; warn "measurement invalid: $1"; }

# ---- node handles --------------------------------------------------------------

NODES=()
IFS=',' read -r -a NODES <<<"$CAL_NODES"
(( ${#NODES[@]} > 0 )) || abort "CAL_NODES is empty"
PRIMARY="${NODES[0]}"

KEYRING_DIR="$CAL_OUT/keyring"
TXDIR="$CAL_OUT/tx"
SENDERS_FILE="$CAL_OUT/senders.csv"
BLOCKS_CSV="$CAL_OUT/blocks.csv"
WAVES_CSV="$CAL_OUT/waves.csv"
STEPS_CSV="$CAL_OUT/steps.csv"
TXLOG="$CAL_OUT/transactions.jsonl"
APPDB_CSV="$CAL_OUT/appdb-samples.csv"
MANIFEST="$CAL_OUT/manifest.json"
RESULT="$CAL_OUT/result.json"

# now_ms — wall-clock milliseconds.
#
# Submission windows are sub-second, so second resolution would round most of them to
# zero and make every derived rate meaningless. GNU date has %3N and BSD date does
# not, so the capability is probed once rather than assumed, and the fallback is
# recorded in the manifest so a reader knows what resolution the timings have.
NOW_MS_SOURCE="seconds"
if [[ "$(date -u +%s%3N 2>/dev/null)" =~ ^[0-9]{13,}$ ]]; then
  NOW_MS_SOURCE="date"
  now_ms() { date -u +%s%3N; }
elif command -v perl >/dev/null 2>&1 && [[ "$(perl -MTime::HiRes -e 'print int(Time::HiRes::time()*1000)' 2>/dev/null)" =~ ^[0-9]{13,}$ ]]; then
  NOW_MS_SOURCE="perl"
  now_ms() { perl -MTime::HiRes -e 'print int(Time::HiRes::time()*1000)'; }
else
  now_ms() { echo "$(( $(date -u +%s) * 1000 ))"; }
fi

primary_height() {
  local body
  body="$(blockgas_get "$PRIMARY" /status)" || return 1
  jq -er '.result.sync_info.latest_block_height | tostring' <<<"$body" 2>/dev/null || return 1
}

endpoint_height() { # <url>
  local body
  body="$(blockgas_get "$1" /status)" || return 1
  jq -er '.result.sync_info.latest_block_height | tostring' <<<"$body" 2>/dev/null || return 1
}
endpoint_catching_up() { # <url> -> true|false
  local body
  body="$(blockgas_get "$1" /status)" || return 1
  # `// "true"` would be wrong: jq's // treats boolean false as empty, so a healthy
  # node reporting catching_up=false would be rewritten into "true".
  jq -er 'if .result.sync_info | has("catching_up") then (.result.sync_info.catching_up | tostring) else error("absent") end' \
    <<<"$body" 2>/dev/null || return 1
}
endpoint_app_hash() { # <url> <height>
  local body
  body="$(blockgas_get "$1" "/block?height=$2")" || return 1
  jq -er --arg h "$2" '
      .result.block.header
    | if (.height | tostring) != $h then error("height mismatch") else . end
    | .app_hash | if . == null or . == "" then error("no app hash") else . end
  ' <<<"$body" 2>/dev/null || return 1
}

wait_blocks() { # <count> — advance this many blocks on the primary
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
  [[ -n "$hash" ]] || { echo "broadcast_error"; return 0; }
  if [[ -n "$code" && "$code" != "0" ]]; then echo "$code"; return 0; fi
  for (( i = 0; i < 120; i++ )); do
    if res="$(tx_outcome "$PRIMARY" "$hash")"; then set -- $res; echo "$2"; return 0; fi
    sleep 1
  done
  echo "not_included"
}

# account_exists <address> -> yes|no
account_exists() {
  "$CAL_BIN" query auth account-info "$1" --node "$PRIMARY" --output json 2>/dev/null \
    | jq -er '.info.account_number | tostring' >/dev/null 2>&1 && echo yes || echo no
}
account_sequence() { # <address>
  "$CAL_BIN" query auth account-info "$1" --node "$PRIMARY" --output json 2>/dev/null \
    | jq -er '.info.sequence | tostring' 2>/dev/null || return 1
}

# ---- 0. preflight ---------------------------------------------------------------

command -v curl >/dev/null 2>&1 || abort "curl is required"
command -v jq   >/dev/null 2>&1 || abort "jq is required"
[[ -x "$CAL_BIN" ]] || abort "CAL_BIN is not executable: $CAL_BIN"
[[ -e "$CAL_OUT" ]] && abort "$CAL_OUT exists; use a fresh RUN_ID or CAL_OUT"
mkdir -p "$CAL_OUT" "$KEYRING_DIR" "$TXDIR" || abort "could not create $CAL_OUT"

STARTED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
say "==> calibration run $RUN_ID"
say "    binary   $CAL_BIN"
say "    chain    $CAL_CHAIN_ID via $PRIMARY (${#NODES[@]} endpoints)"
say "    output   $CAL_OUT"

H_PREFLIGHT="$(primary_height)" || abort "no height from $PRIMARY; is the network reachable?"
[[ "$H_PREFLIGHT" =~ ^[0-9]+$ ]] || abort "unusable height from $PRIMARY"

STEP_LIST=()
IFS=',' read -r -a STEP_LIST <<<"$CAL_STEPS"
MAX_SENDERS=0
for s in "${STEP_LIST[@]}"; do
  [[ "$s" =~ ^[0-9]+$ ]] && (( s > 0 )) || abort "CAL_STEPS entry '$s' is not a positive integer"
  (( s > MAX_SENDERS )) && MAX_SENDERS="$s"
done
TOTAL_WAVES=$(( ${#STEP_LIST[@]} * CAL_WAVES_PER_STEP ))
(( MAX_SENDERS < 65536 )) || abort "CAL_STEPS asks for $MAX_SENDERS senders; the address scheme allows under 65536"
(( TOTAL_WAVES < 16777216 )) || abort "too many waves to address distinctly"
(( CAL_MULTISEND_OUTPUTS >= 1 && CAL_MULTISEND_OUTPUTS < 256 )) || abort "CAL_MULTISEND_OUTPUTS must be in [1, 256)"
case "$CAL_MIX" in send|multisend|mixed) ;; *) abort "CAL_MIX must be send, multisend or mixed" ;; esac
[[ "$CAL_GAS_SAFETY_BPS" =~ ^[0-9]+$ ]] && (( CAL_GAS_SAFETY_BPS < 10000 )) \
  || abort "CAL_GAS_SAFETY_BPS must be an integer in [0, 10000)"

# Every configured endpoint must be reachable, on the same chain, and CAUGHT UP.
# Reading blocks from one node while the rest of the network is elsewhere produces a
# clean CSV describing a chain nobody else is on.
for url in "${NODES[@]}"; do
  net="$(blockgas_get "$url" /status | jq -er '.result.node_info.network' 2>/dev/null)"
  [[ "$net" == "$CAL_CHAIN_ID" ]] || abort "endpoint $url serves '$net', not '$CAL_CHAIN_ID'"
  cu="$(endpoint_catching_up "$url")" || abort "endpoint $url did not report sync status"
  [[ "$cu" == "false" ]] || abort "endpoint $url is still catching up"
done

# The starting posture, recorded before anything is offered. A run against an
# already-finite ceiling is legitimate — re-testing a candidate is a normal reason to
# run this — but it must be visible in the artifact.
START_MAX_GAS="$(live_max_gas "$PRIMARY")" || abort "could not read the live consensus params"
START_MAX_BYTES="$(blockgas_get "$PRIMARY" /consensus_params | jq -er '.result.consensus_params.block.max_bytes | tostring' 2>/dev/null)" \
  || abort "could not read block.max_bytes"
START_MAX_GAS_FINITE="$(max_gas_is_finite "$START_MAX_GAS")"
say "    max_gas  $START_MAX_GAS (finite: $START_MAX_GAS_FINITE)"

# The address encoder must agree with the chain's own codec before it mints a single
# recipient. A wrong encoder yields well-formed addresses the chain refuses, and the
# run would then measure its own rejection rate.
HRP="$("$CAL_BIN" query auth bech32-prefix --node "$PRIMARY" --output json 2>/dev/null | jq -er '.bech32_prefix' 2>/dev/null)"
[[ -n "$HRP" ]] || HRP="twilight"
bech32_agrees_with_binary "$CAL_BIN" "$HRP" || abort "the local address encoder disagrees with $CAL_BIN for prefix '$HRP'"
bech32_init "$HRP" || abort "could not initialise the address encoder"

FUNDER_ADDR="$("$CAL_BIN" keys show "$CAL_FUNDER" -a --keyring-backend "$CAL_KEYRING_BACKEND" --home "$CAL_HOME" 2>/dev/null)"
[[ -n "$FUNDER_ADDR" ]] || abort "could not resolve funding key '$CAL_FUNDER' in $CAL_HOME"

[[ -n "$CAL_RECIPIENT_NONCE" ]] || CAL_RECIPIENT_NONCE="$(run_nonce)"
[[ "$CAL_RECIPIENT_NONCE" =~ ^[0-9a-f]{16}$ ]] || abort "CAL_RECIPIENT_NONCE must be 16 lowercase hex characters"
say "    ramp     ${STEP_LIST[*]} senders, $CAL_WAVES_PER_STEP waves each, mix=$CAL_MIX"
say "    nonce    $CAL_RECIPIENT_NONCE"

# ---- recipient namespace ---------------------------------------------------------
#
# 20-byte payload laid out as: nonce(8B) | zero(6B) | wave(3B) | sender(2B) | out(1B).
# The ranges are disjoint by construction and every field is bounds-checked above, so
# no two transactions in a run can address the same recipient.
recipient_into() { # <var> <wave> <sender-index> <output-index>
  local __var="$1" wave="$2" idx="$3" n="$4" hex
  printf -v hex '%s000000000000%06x%04x%02x' "$CAL_RECIPIENT_NONCE" "$wave" "$idx" "$n"
  bech32_encode_into "$__var" "$hex" || return 1
}

# Freshness is VERIFIED, not assumed. Representatives are taken from the corners and
# the middle of the namespace this run will actually use, so a nonce that collided
# with an earlier run is caught before the growth column is quietly flattened.
say "==> verifying the recipient namespace is unused"
NAMESPACE_FRESH=yes
LAST_WAVE=$TOTAL_WAVES
MID_WAVE=$(( (TOTAL_WAVES + 1) / 2 ))
for probe in "1 0 0" "1 $(( MAX_SENDERS - 1 )) 0" "$MID_WAVE 0 0" \
             "$LAST_WAVE $(( MAX_SENDERS - 1 )) $(( CAL_MULTISEND_OUTPUTS - 1 ))"; do
  set -- $probe
  recipient_into PROBE_ADDR "$1" "$2" "$3" || abort "could not derive a namespace probe address"
  if [[ "$(account_exists "$PROBE_ADDR")" == "yes" ]]; then
    NAMESPACE_FRESH=no
    invalidate "recipient namespace already in use at $PROBE_ADDR (wave $1 sender $2 out $3)"
    break
  fi
done

# ---- 1. provisioning: senders exist and are funded BEFORE measurement -------------

say "==> provisioning $MAX_SENDERS sender accounts"
: >"$SENDERS_FILE"
echo "index,name,address,account_number" >>"$SENDERS_FILE"
SENDER_ADDR=(); SENDER_NAME=(); SENDER_ACC=(); SENDER_EXPECT=()

for (( i = 0; i < MAX_SENDERS; i++ )); do
  name="cal$i"
  addr="$("$CAL_BIN" keys add "$name" --keyring-backend test --home "$KEYRING_DIR" --output json 2>/dev/null \
          | jq -er '.address' 2>/dev/null)"
  [[ -n "$addr" ]] || abort "could not create sender key $name"
  SENDER_NAME[$i]="$name"; SENDER_ADDR[$i]="$addr"; SENDER_EXPECT[$i]=0
  (( (i + 1) % 50 == 0 )) && say "    $((i + 1))/$MAX_SENDERS keys"
done

say "==> funding senders ($CAL_FUND_PER_SENDER utwlt each, in batches of $CAL_FUND_BATCH)"
FUND_BATCHES=0; FUND_FAILURES=0
i=0
while (( i < MAX_SENDERS )); do
  batch=()
  for (( j = i; j < i + CAL_FUND_BATCH && j < MAX_SENDERS; j++ )); do batch+=("${SENDER_ADDR[$j]}"); done
  # Batched rather than one enormous transaction: the network under test may ALREADY
  # have a finite ceiling — re-testing a candidate is a normal reason to run this —
  # and a funding transaction that cannot fit in a block would strand the run.
  #
  # The per-recipient amount is far above the minimum-first-funding floor, so the
  # bank SendRestriction admits these account-creating outputs.
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

# Every sender must be RESOLVABLE on chain before the window opens. If an account
# does not exist here, its first flood transaction would be creating it — and the
# minimum-first-funding rule, not block gas, would be what the run measured.
say "==> confirming every sender exists on chain"
FUNDED=0
for (( i = 0; i < MAX_SENDERS; i++ )); do
  info="$("$CAL_BIN" query auth account-info "${SENDER_ADDR[$i]}" --node "$PRIMARY" --output json 2>/dev/null)"
  accnum="$(jq -er '.info.account_number | tostring' <<<"$info" 2>/dev/null)"
  if [[ "$accnum" =~ ^[0-9]+$ ]]; then
    SENDER_ACC[$i]="$accnum"; FUNDED=$(( FUNDED + 1 ))
    printf '%s,%s,%s,%s\n' "$i" "${SENDER_NAME[$i]}" "${SENDER_ADDR[$i]}" "$accnum" >>"$SENDERS_FILE"
  else
    SENDER_ACC[$i]=""
    warn "sender $i (${SENDER_ADDR[$i]}) has no on-chain account"
  fi
done
(( FUNDED == MAX_SENDERS )) || abort "$FUNDED of $MAX_SENDERS senders are funded; refusing to measure a funding rule"
say "    $FUNDED senders funded and resolvable"

# ---- 2. the application-DB sampler ------------------------------------------------
#
# Disk size moves with compaction as well as with writes, so it corroborates the
# account count rather than replacing it. Its absence is recorded as unavailable and
# never as zero growth.
APPDB_PID=""
APPDB_AVAILABLE=no
start_appdb_sampler() {
  [[ -n "$CAL_NODE_HOME" ]] || { note "CAL_NODE_HOME unset; application-DB growth is UNAVAILABLE, not zero"; return 0; }
  local db="$CAL_NODE_HOME/data/application.db"
  [[ -d "$db" ]] || { warn "no application.db under $CAL_NODE_HOME; DB growth UNAVAILABLE"; return 0; }
  APPDB_AVAILABLE=yes
  echo "height,appdb_kb" >"$APPDB_CSV"
  (
    last=""; while :; do
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
  # Disowned so killing it does not print a job-control notice into the run's output.
  disown "$APPDB_PID" 2>/dev/null || true
}
stop_appdb_sampler() { [[ -n "$APPDB_PID" ]] && kill "$APPDB_PID" 2>/dev/null; APPDB_PID=""; }
trap 'stop_appdb_sampler' EXIT

# ---- 3. the ramp -------------------------------------------------------------------
#
# One wave is: sign everything, broadcast it, DRAIN it to terminal state, verify every
# participating sender's committed sequence advanced, then rest for a quiet period.
#
# The drain is the part a fixed settle window cannot replace. A transaction can sit in
# a node's CheckTx state while the canonical account query still returns the previous
# committed sequence; a next wave signed from that reading is stale and rejects
# itself, and the rig would then be measuring sequence contention it created rather
# than chain capacity.

: >"$TXLOG"
echo "step,senders,wave,offered,accepted,included,delivered_ok,delivered_failed,unresolved,submit_start_ms,submit_end_ms,submit_ms,max_concurrent,accepted_per_s,active_from,active_to,quiet_from,quiet_to,workload" >"$WAVES_CSV"
start_appdb_sampler

RAMP_MIN_HEIGHT=""; RAMP_MAX_HEIGHT=""
RUN_TRUNCATED=0
STEP_IDX=0
WAVE_SERIAL=0
DEADLINE=$(( SECONDS + CAL_MAX_SECONDS ))
SEQ_VIOLATIONS=0
DRAIN_FAILURES=0

for senders in "${STEP_LIST[@]}"; do
  STEP_IDX=$(( STEP_IDX + 1 ))
  WAVES_VALID_IN_STEP=0
  say "==> step $STEP_IDX: $senders senders x $CAL_WAVES_PER_STEP waves"
  for (( wave = 1; wave <= CAL_WAVES_PER_STEP; wave++ )); do
    if (( SECONDS >= DEADLINE )); then
      # A truncated run leaves a partially executed step in the data. One unsafe wave
      # out of three configured is enough to make that step look UNSAFE and complete a
      # bracket the full step might never have produced, so truncation is recorded and
      # blocks candidate emission outright.
      RUN_TRUNCATED=1
      warn "CAL_MAX_SECONDS reached; stopping the ramp after step $STEP_IDX wave $((wave - 1))"
      break 2
    fi
    WAVE_SERIAL=$(( WAVE_SERIAL + 1 ))
    WDIR="$TXDIR/w$WAVE_SERIAL"
    mkdir -p "$WDIR" || abort "could not create $WDIR"

    # --- sign, entirely outside the timing window ---
    : >"$WDIR/signed.txt"
    SIGN_OK=1
    for (( s = 0; s < senders; s++ )); do
      kind="send"
      case "$CAL_MIX" in
        multisend) kind="multisend" ;;
        mixed) (( s % 2 == 1 )) && kind="multisend" ;;
      esac
      u="$WDIR/tx$s.json"
      if [[ "$kind" == "multisend" ]]; then
        to=()
        for (( n = 0; n < CAL_MULTISEND_OUTPUTS; n++ )); do
          recipient_into ONE "$WAVE_SERIAL" "$s" "$n" || { SIGN_OK=0; break; }
          to+=("$ONE")
        done
        (( SIGN_OK )) || break
        "$CAL_BIN" tx bank multi-send "${SENDER_ADDR[$s]}" "${to[@]}" "${CAL_SEND_AMOUNT}utwlt" \
          --generate-only --chain-id "$CAL_CHAIN_ID" --gas "$CAL_TX_GAS_MULTISEND" --fees 0utwlt \
          --output json >"$u" 2>/dev/null || { SIGN_OK=0; break; }
      else
        recipient_into ONE "$WAVE_SERIAL" "$s" 0 || { SIGN_OK=0; break; }
        "$CAL_BIN" tx bank send "${SENDER_ADDR[$s]}" "$ONE" "${CAL_SEND_AMOUNT}utwlt" \
          --generate-only --chain-id "$CAL_CHAIN_ID" --gas "$CAL_TX_GAS" --fees 0utwlt \
          --output json >"$u" 2>/dev/null || { SIGN_OK=0; break; }
      fi
      # The sequence is the count of waves this sender has already had DELIVERED,
      # tracked in the parent and verified against chain state after every drain.
      sign_and_encode_into SIGNED "$CAL_BIN" "$KEYRING_DIR" test "$CAL_CHAIN_ID" \
        "${SENDER_NAME[$s]}" "${SENDER_ACC[$s]}" "${SENDER_EXPECT[$s]}" "$u" || { SIGN_OK=0; break; }
      printf '%s %s %s\n' "$s" "$kind" "$SIGNED" >>"$WDIR/signed.txt"
    done
    (( SIGN_OK )) || abort "could not pre-sign wave $WAVE_SERIAL"

    OFFERED="$(grep -c . "$WDIR/signed.txt")"
    (( OFFERED == senders )) || abort "wave $WAVE_SERIAL signed $OFFERED of $senders"

    # --- broadcast: the only thing inside the timing window ---
    PRE_WAVE_HEIGHT="$(primary_height)" || abort "lost the primary endpoint mid-ramp"
    echo "start_ms,end_ms" >"$WDIR/timing.csv"
    : >"$WDIR/broadcast.jsonl"
    SUBMIT_START="$(now_ms)"
    # Waited on BY PID, never with a bare `wait`. A bare `wait` waits for every
    # background job of this shell, and the application-DB sampler is one of them —
    # and it never exits. That hung a live run of the sibling drill until it was
    # found; the same shape would hang every wave here.
    BPIDS=()
    while read -r sidx skind sb64; do
      [[ -n "$sb64" ]] || continue
      (
        t0="$(now_ms)"
        if res="$(broadcast_signed "$PRIMARY" "$sb64")"; then
          set -- $res
          jq -nc --arg s "$sidx" --arg k "$skind" --arg c "$1" --arg h "${2:-}" \
            '{sender:($s|tonumber), kind:$k, check_code:($c|tonumber), txhash:$h}' >>"$WDIR/broadcast.jsonl"
        else
          echo "{\"sender\":$sidx,\"kind\":\"$skind\",\"check_code\":-1,\"txhash\":\"\"}" >>"$WDIR/broadcast.jsonl"
        fi
        echo "$t0,$(now_ms)" >>"$WDIR/timing.csv"
      ) &
      BPIDS+=($!)
      if (( ${#BPIDS[@]} >= CAL_PARALLEL )); then wait "${BPIDS[@]}"; BPIDS=(); fi
    done <"$WDIR/signed.txt"
    (( ${#BPIDS[@]} > 0 )) && wait "${BPIDS[@]}"
    BPIDS=()
    SUBMIT_END="$(now_ms)"
    SUBMIT_MS=$(( SUBMIT_END - SUBMIT_START ))
    (( SUBMIT_MS >= 0 )) || SUBMIT_MS=0
    MAX_CONC="$(max_concurrency_of "$WDIR/timing.csv")" || MAX_CONC=0

    ACCEPTED="$(jq -s '[.[] | select(.check_code == 0 and .txhash != "")] | length' "$WDIR/broadcast.jsonl" 2>/dev/null)"
    [[ "$ACCEPTED" =~ ^[0-9]+$ ]] || ACCEPTED=0
    if (( SUBMIT_MS > 0 )); then ACCEPTED_PER_S=$(( ACCEPTED * 1000 / SUBMIT_MS )); else ACCEPTED_PER_S="$ACCEPTED"; fi

    # --- drain to terminal state; never a fixed number of blocks ---
    W_INCLUDED=0; W_OK=0; W_FAILED=0; W_UNRESOLVED=0
    : >"$WDIR/heights.txt"
    DRAIN_DEADLINE=$(( SECONDS + CAL_DRAIN_TIMEOUT_S ))
    while IFS= read -r line; do
      hash="$(jq -r '.txhash' <<<"$line" 2>/dev/null)"
      sidx="$(jq -r '.sender' <<<"$line" 2>/dev/null)"
      kind="$(jq -r '.kind' <<<"$line" 2>/dev/null)"
      ccode="$(jq -r '.check_code' <<<"$line" 2>/dev/null)"
      if [[ "$ccode" != "0" || -z "$hash" ]]; then
        jq -nc --arg st "$STEP_IDX" --arg w "$WAVE_SERIAL" --arg s "$sidx" --arg k "$kind" \
               --arg c "$ccode" '{step:($st|tonumber),wave:($w|tonumber),sender:($s|tonumber),kind:$k,
                                  txhash:"",check_code:($c|tonumber),height:null,deliver_code:null,
                                  status:"CHECK_REJECTED"}' >>"$TXLOG"
        continue
      fi
      h=""; dcode=""; status="ACCEPTED_PENDING"
      while (( SECONDS < DRAIN_DEADLINE )); do
        if out="$(tx_outcome "$PRIMARY" "$hash")"; then set -- $out; h="$1"; dcode="$2"; break; fi
        sleep 1
      done
      if [[ "$h" =~ ^[0-9]+$ ]]; then
        W_INCLUDED=$(( W_INCLUDED + 1 )); echo "$h" >>"$WDIR/heights.txt"
        if [[ "$dcode" == "0" ]]; then W_OK=$(( W_OK + 1 )); status="DELIVERED_OK"
        else W_FAILED=$(( W_FAILED + 1 )); status="DELIVERED_FAILED"; fi
      else
        W_UNRESOLVED=$(( W_UNRESOLVED + 1 )); status="NOT_FOUND_TIMEOUT"
      fi
      jq -nc --arg st "$STEP_IDX" --arg w "$WAVE_SERIAL" --arg s "$sidx" --arg k "$kind" \
             --arg t "$hash" --arg hh "${h:-}" --arg d "${dcode:-}" --arg status "$status" \
        '{step:($st|tonumber),wave:($w|tonumber),sender:($s|tonumber),kind:$k,txhash:$t,
          check_code:0,height:(if $hh == "" then null else ($hh|tonumber) end),
          deliver_code:(if $d == "" then null else ($d|tonumber) end),status:$status}' >>"$TXLOG"
    done <"$WDIR/broadcast.jsonl"

    # An unresolved transaction at a wave boundary makes every later wave suspect:
    # the sender's sequence is unknown, and the next signature may be stale.
    if (( W_UNRESOLVED > 0 )); then
      DRAIN_FAILURES=$(( DRAIN_FAILURES + W_UNRESOLVED ))
    fi

    # The workload a wave completed must be the workload it intended, exactly.
    #
    # An execution failure is the case the sequence check below CANNOT see: a
    # transaction that reverts still advances its sender's sequence, so the run looks
    # perfectly ordered while a share of the offered work produced none of the state
    # the workload was supposed to create. That is a different, cheaper workload than
    # the one being characterised, and a knee derived through it does not describe the
    # load it claims to. A CheckTx rejection disqualifies for the mirror reason: part
    # of the intended load never entered the mempool at all.
    WAVE_WORKLOAD="$(wave_workload_valid "$OFFERED" "$ACCEPTED" "$W_INCLUDED" "$W_OK" "$W_FAILED" "$W_UNRESOLVED")"
    if [[ "$WAVE_WORKLOAD" == "OK" ]]; then
      WAVES_VALID_IN_STEP=$(( WAVES_VALID_IN_STEP + 1 ))
    else
      invalidate "wave $WAVE_SERIAL workload incomplete ($WAVE_WORKLOAD: offered $OFFERED, accepted $ACCEPTED, included $W_INCLUDED, delivered_ok $W_OK, failed $W_FAILED, unresolved $W_UNRESOLVED)"
    fi

    # --- the ACTIVE window, from transaction evidence ---
    if [[ -s "$WDIR/heights.txt" ]]; then
      ACTIVE_FROM="$(LC_ALL=C sort -n "$WDIR/heights.txt" | head -1)"
      ACTIVE_TO="$(LC_ALL=C sort -n "$WDIR/heights.txt" | tail -1)"
    else
      ACTIVE_FROM=""; ACTIVE_TO=""
    fi

    # --- sequences must have advanced exactly ---
    for (( s = 0; s < senders; s++ )); do
      SENDER_EXPECT[$s]=$(( SENDER_EXPECT[s] + 1 ))
    done
    for (( s = 0; s < senders; s++ )); do
      seq="$(account_sequence "${SENDER_ADDR[$s]}")" || seq=""
      if [[ ! "$seq" =~ ^[0-9]+$ ]]; then
        SEQ_VIOLATIONS=$(( SEQ_VIOLATIONS + 1 ))
        invalidate "sender $s sequence unreadable after wave $WAVE_SERIAL"
      elif (( seq != SENDER_EXPECT[s] )); then
        SEQ_VIOLATIONS=$(( SEQ_VIOLATIONS + 1 ))
        invalidate "sender $s committed sequence $seq after wave $WAVE_SERIAL, expected ${SENDER_EXPECT[$s]}"
        # Resynchronise so later waves are not all rejected on top of one bad wave;
        # the run is already marked invalid and the divergence is recorded.
        SENDER_EXPECT[$s]="$seq"
      fi
    done

    # --- the QUIET period, recorded separately and excluded from load response ---
    QUIET_FROM=""; QUIET_TO=""
    if (( CAL_QUIET_BLOCKS > 0 )); then
      QUIET_FROM=$(( ${ACTIVE_TO:-$PRE_WAVE_HEIGHT} + 1 ))
      QUIET_TO="$(wait_blocks "$CAL_QUIET_BLOCKS")" || abort "the chain stopped advancing after wave $WAVE_SERIAL"
    fi

    printf '%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s\n' \
      "$STEP_IDX" "$senders" "$wave" "$OFFERED" "$ACCEPTED" "$W_INCLUDED" "$W_OK" "$W_FAILED" "$W_UNRESOLVED" \
      "$SUBMIT_START" "$SUBMIT_END" "$SUBMIT_MS" "$MAX_CONC" "$ACCEPTED_PER_S" \
      "${ACTIVE_FROM:--}" "${ACTIVE_TO:--}" "${QUIET_FROM:--}" "${QUIET_TO:--}" "$WAVE_WORKLOAD" >>"$WAVES_CSV"
    say "    wave $wave: offered $OFFERED accepted $ACCEPTED delivered $W_OK in ${SUBMIT_MS}ms (max concurrent $MAX_CONC), active ${ACTIVE_FROM:--}..${ACTIVE_TO:--} [$WAVE_WORKLOAD]"

    if [[ "$ACTIVE_FROM" =~ ^[0-9]+$ ]]; then
      [[ -n "$RAMP_MIN_HEIGHT" ]] || RAMP_MIN_HEIGHT="$ACTIVE_FROM"
    fi
    [[ "${QUIET_TO:-}" =~ ^[0-9]+$ ]] && RAMP_MAX_HEIGHT="$QUIET_TO"
    [[ "$ACTIVE_TO" =~ ^[0-9]+$ ]] && { [[ -n "${RAMP_MAX_HEIGHT:-}" ]] && (( ACTIVE_TO > RAMP_MAX_HEIGHT )) && RAMP_MAX_HEIGHT="$ACTIVE_TO"; }
  done
done
stop_appdb_sampler

[[ -n "$RAMP_MIN_HEIGHT" && -n "$RAMP_MAX_HEIGHT" ]] || abort "the ramp produced no measured height range"

# ---- 4. attribution ------------------------------------------------------------------
#
# Every measured block belongs to at most ONE load step. Deriving windows from a
# broadcast height plus a fixed settle count let adjacent ranges overlap, and the
# analyser then attributed a shared block to whichever range it happened to match
# first — so a knee could move because of harness bookkeeping rather than chain
# behaviour. Windows now come from transaction evidence, and the overlap is checked
# rather than assumed away.
say "==> checking block attribution"
ATTRIB="$CAL_OUT/attribution.txt"
: >"$ATTRIB"
awk -F, 'NR > 1 && $15 ~ /^[0-9]+$/ && $16 ~ /^[0-9]+$/ {
    for (h = $15; h <= $16; h++) print h, $1, "ACTIVE"
  }' "$WAVES_CSV" | LC_ALL=C sort -k1,1n >"$ATTRIB"
OVERLAPS="$(attribution_overlaps "$ATTRIB")" || OVERLAPS=0
if (( OVERLAPS > 0 )); then
  invalidate "$OVERLAPS blocks are claimed by more than one load step"
fi
awk -F, 'NR > 1 && $17 ~ /^[0-9]+$/ && $18 ~ /^[0-9]+$/ {
    for (h = $17; h <= $18; h++) print h, $1, "QUIET"
  }' "$WAVES_CSV" | LC_ALL=C sort -k1,1n >"$CAL_OUT/attribution-quiet.txt"

step_of_height() { awk -v h="$1" '$1 == h { print $2; exit }' "$ATTRIB"; }
class_of_height() {
  local st
  st="$(awk -v h="$1" '$1 == h { print $2; exit }' "$ATTRIB")"
  if [[ -n "$st" ]]; then echo "ACTIVE"; return 0; fi
  st="$(awk -v h="$1" '$1 == h { print $2; exit }' "$CAL_OUT/attribution-quiet.txt")"
  if [[ -n "$st" ]]; then echo "QUIET"; return 0; fi
  echo "UNATTRIBUTED"
}

# ---- 5. per-block collection -----------------------------------------------------------

say "==> collecting per-block metrics for heights $RAMP_MIN_HEIGHT..$RAMP_MAX_HEIGHT"
echo "height,step,class,unix_ms,interval_ms,tx_count,block_bytes,gas_wanted,gas_used,accounts,account_delta,appdb_kb,appdb_delta_kb" >"$BLOCKS_CSV"

appdb_at() {
  [[ "$APPDB_AVAILABLE" == "yes" && -s "$APPDB_CSV" ]] || return 1
  awk -F, -v h="$1" 'NR > 1 && $1 == h { print $2; exit }' "$APPDB_CSV" 2>/dev/null
}
accounts_at() {
  (( CAL_ACCOUNT_SAMPLING == 1 )) || return 1
  "$CAL_BIN" query auth accounts --page-limit 1 --page-count-total --height "$1" \
    --node "$PRIMARY" --output json 2>/dev/null | jq -er '.pagination.total | tostring' 2>/dev/null
}

# One block before the range, so the first measured block has an interval.
COLLECT_FROM=$(( RAMP_MIN_HEIGHT > 1 ? RAMP_MIN_HEIGHT - 1 : 1 ))
PREV_MS=""; PREV_ACC=""; PREV_DB=""
UNREADABLE_BLOCKS=0; ACCOUNT_SAMPLES=0; ACCOUNT_EXPECTED=0; APPDB_SAMPLES=0
# Blocks whose timestamp was present but would not parse. Counted separately from
# wholly unreadable blocks because the failure mode is different and quieter.
TIMING_UNREADABLE=0
# Delta coverage is tracked SEPARATELY from sample coverage, and only over ACTIVE
# blocks. A policy is a statement about growth, so a block whose account count was
# read but whose delta could not be formed contributes no growth evidence at all —
# and one missing delta may be the worst-growth block.
ACCT_DELTA_SEEN=0; APPDB_DELTA_SEEN=0
# The whole per-block collection pass is ONE FUNCTION, and the fault suite drives that
# function over a synthetic block sequence rather than reading this file.
#
# Three rounds of pinning source text were each defeated by the next textual shape: a
# call-site `|| TIMING_UNREADABLE=0`, a later reset, and finally an `if false` wrapper
# that left every anchored pattern intact while the timing call never executed. You
# cannot enumerate by pattern the ways a shell script can neutralise a value. Running
# the code closes all of them at once, because a neutralised counter is then simply
# the wrong number in the test's hands.
#
# Reads and updates the calibration collection state declared just above.
collect_block_metrics "$COLLECT_FROM" "$RAMP_MAX_HEIGHT"
if (( UNREADABLE_BLOCKS > 0 )); then
  invalidate "$UNREADABLE_BLOCKS blocks in the measured range could not be read"
fi
if ! timing_integrity_ok "$TIMING_UNREADABLE"; then
  invalidate "$TIMING_UNREADABLE blocks failed timing observation (unparseable, calendar-invalid, or a zero/backwards interval); the timing distribution is not trustworthy"
fi

# Availability is tracked SEPARATELY from the numbers. An axis that was not measured
# is UNAVAILABLE, never zero — zero growth is precisely the optimistic reading this
# instrument must not invent.
ACCOUNT_AXIS="$(axis_availability "$CAL_ACCOUNT_SAMPLING" "$ACCOUNT_SAMPLES" "$ACCOUNT_EXPECTED")"
if [[ "$APPDB_AVAILABLE" != "yes" ]]; then APPDB_AXIS="UNAVAILABLE"
else APPDB_AXIS="$(axis_availability 1 "$APPDB_SAMPLES" "$ACCOUNT_EXPECTED")"; fi
ACCT_DELTA_COVERAGE="$(delta_coverage_class "$ACCT_DELTA_SEEN" "$ACCOUNT_EXPECTED")"
APPDB_DELTA_COVERAGE="$(delta_coverage_class "$APPDB_DELTA_SEEN" "$ACCOUNT_EXPECTED")"

# ---- 6. endpoint agreement ---------------------------------------------------------------
#
# The blocks were read from ONE node. If the others do not agree on them, the CSV
# describes a node rather than a chain — and a saturated RPC endpoint is exactly the
# thing that would otherwise be mistaken for a saturated network.
say "==> checking endpoint agreement"
AGREE_SAMPLES=0; AGREE_OK=0
SAMPLE_HEIGHTS="$(awk -F, 'NR > 1 && $15 ~ /^[0-9]+$/ { print $15 }' "$WAVES_CSV" | LC_ALL=C sort -nu)"
for sh in $SAMPLE_HEIGHTS; do
  ref=""; ok=1
  for url in "${NODES[@]}"; do
    ah="$(endpoint_app_hash "$url" "$sh")" || { ok=0; break; }
    if [[ -z "$ref" ]]; then ref="$ah"; elif [[ "$ah" != "$ref" ]]; then ok=0; break; fi
  done
  AGREE_SAMPLES=$(( AGREE_SAMPLES + 1 ))
  (( ok )) && AGREE_OK=$(( AGREE_OK + 1 ))
done
if (( AGREE_SAMPLES == 0 )); then
  invalidate "no height could be sampled for endpoint agreement"
elif (( AGREE_OK != AGREE_SAMPLES )); then
  invalidate "endpoints disagreed at $(( AGREE_SAMPLES - AGREE_OK )) of $AGREE_SAMPLES sampled heights"
fi
FINAL_HEIGHTS=""
LAG_OK=1
for url in "${NODES[@]}"; do
  eh="$(endpoint_height "$url")" || { LAG_OK=0; eh="unreachable"; }
  FINAL_HEIGHTS="$FINAL_HEIGHTS ${eh}"
done
if (( LAG_OK )); then
  LOW="$(printf '%s\n' $FINAL_HEIGHTS | LC_ALL=C sort -n | head -1)"
  HIGH="$(printf '%s\n' $FINAL_HEIGHTS | LC_ALL=C sort -n | tail -1)"
  if [[ "$LOW" =~ ^[0-9]+$ && "$HIGH" =~ ^[0-9]+$ ]] && (( HIGH - LOW > CAL_ENDPOINT_LAG_BLOCKS )); then
    invalidate "endpoint heights span $(( HIGH - LOW )) blocks, over the allowed $CAL_ENDPOINT_LAG_BLOCKS"
  fi
else
  invalidate "an endpoint became unreachable during the run"
fi

# ---- 7. per-step analysis --------------------------------------------------------------
#
# Aggregated over ACTIVE blocks only. Quiet and unattributed blocks are excluded: a
# drain period is not a response to load, and including it drags the distribution
# toward the idle cadence and hides the step that has begun to fail.
#
# # On gas and time
#
# The interval recorded against block H is time(H) - time(H-1): the time it took to
# produce the block whose gas is on the same row. No per-row correlation between gas
# and latency is used or claimed. The load response is taken from SUSTAINED
# STEP-LEVEL distributions — the p95 interval and the p95 gas across a step's active
# blocks — which is a statement about the step, not about any single block.
#
# The tail is the signal. A median hides exactly the behaviour that matters: a step
# where most blocks are comfortable and a tail is not has already begun to fail.
say "==> analysing per step"
# usable_intervals is appended rather than inserted: the knee reads p95_gas_wanted by
# column index, and renumbering it here would silently repoint that read.
echo "step,senders,active_blocks,offered,accepted,delivered_ok,delivered_failed,unresolved,accepted_per_s,max_concurrent,p50_interval_ms,p95_interval_ms,max_interval_ms,p95_gas_wanted,max_gas_wanted,max_gas_used,max_tx_count,account_growth,appdb_growth_kb,waves_expected,waves_valid,eligibility,class,usable_intervals" >"$STEPS_CSV"

# Per-step classification and eligibility, both recorded so the qualification can be
# audited rather than trusted. Parallel indexed arrays: bash 3.2 has no associative
# arrays.
STEP_CLASS=(); STEP_ELIG=(); ANALYSED_STEPS=0; INELIGIBLE_REASON=""
for (( sidx = 1; sidx <= STEP_IDX; sidx++ )); do
  SENDERS_OF_STEP="$(awk -F, -v s="$sidx" 'NR > 1 && $1 == s { print $2; exit }' "$WAVES_CSV")"
  [[ -n "$SENDERS_OF_STEP" ]] || continue
  INTERVALS=(); GASES=()
  ACTIVE_BLOCKS=0; MAXIV=0; MAXGW=0; MAXGU=0; MAXTX=0
  ACCT_SUM=0; ACCT_SEEN=0; DB_SUM=0; DB_SEEN=0
  while IFS=, read -r bh bstep bclass bms biv bnt bbs bgw bgu bacc bdacc bdb bddb; do
    [[ "$bstep" == "$sidx" && "$bclass" == "ACTIVE" ]] || continue
    ACTIVE_BLOCKS=$(( ACTIVE_BLOCKS + 1 ))
    # Strictly positive. The observation helper already refuses zero and backwards, so
    # this is defence in depth at the point the tail statistic is actually assembled.
    [[ "$biv" =~ ^[0-9]+$ ]] && (( biv > 0 )) && { INTERVALS+=("$biv"); (( biv > MAXIV )) && MAXIV="$biv"; }
    [[ "$bgw" =~ ^[0-9]+$ ]] && { GASES+=("$bgw"); (( bgw > MAXGW )) && MAXGW="$bgw"; }
    [[ "$bgu" =~ ^[0-9]+$ ]] && { (( bgu > MAXGU )) && MAXGU="$bgu"; }
    [[ "$bnt" =~ ^[0-9]+$ ]] && { (( bnt > MAXTX )) && MAXTX="$bnt"; }
    [[ "$bdacc" =~ ^-?[0-9]+$ ]] && { ACCT_SUM=$(( ACCT_SUM + bdacc )); ACCT_SEEN=$(( ACCT_SEEN + 1 )); }
    [[ "$bddb" =~ ^-?[0-9]+$ ]] && { DB_SUM=$(( DB_SUM + bddb )); DB_SEEN=$(( DB_SEEN + 1 )); }
  done < <(tail -n +2 "$BLOCKS_CSV")

  OFF="$(awk -F, -v s="$sidx" 'NR > 1 && $1 == s { o += $4; a += $5; ok += $7; f += $8; u += $9 } END { print o+0, a+0, ok+0, f+0, u+0 }' "$WAVES_CSV")"
  set -- $OFF; S_OFFERED="$1"; S_ACCEPTED="$2"; S_OK="$3"; S_FAILED="$4"; S_UNRES="$5"
  S_RATE="$(awk -F, -v s="$sidx" 'NR > 1 && $1 == s { r += $14; n++ } END { if (n) printf "%d", r / n; else print 0 }' "$WAVES_CSV")"
  S_CONC="$(awk -F, -v s="$sidx" 'NR > 1 && $1 == s { if ($13 + 0 > m) m = $13 + 0 } END { print m + 0 }' "$WAVES_CSV")"

  if (( ${#INTERVALS[@]} > 0 )); then
    P50="$(percentile_of 50 "${INTERVALS[@]}")"; P95="$(percentile_of 95 "${INTERVALS[@]}")"
  else P50="NA"; P95="NA"; fi
  if (( ${#GASES[@]} > 0 )); then P95GW="$(percentile_of 95 "${GASES[@]}")"; else P95GW="NA"; fi
  # NA, not zero. A step whose growth axis was never sampled has UNKNOWN growth, and
  # printing 0 would assert the most reassuring possible answer for missing data.
  ACCT_OUT="$(growth_render "$ACCT_SUM" "$ACCT_SEEN")"
  DB_OUT="$(growth_render "$DB_SUM" "$DB_SEEN")"

  # Waves that completed their FULL intended workload, from the recorded per-wave
  # verdict rather than from a count of rows.
  WAVES_OK="$(awk -F, -v s="$sidx" 'NR > 1 && $1 == s && $19 == "OK" { n++ } END { print n + 0 }' "$WAVES_CSV")"
  # The exact cardinality the p95 was computed from — not the ACTIVE block count,
  # which can exceed it whenever a block supplied no usable interval.
  USABLE_INTERVALS=${#INTERVALS[@]}
  ELIG="$(step_eligibility "$WAVES_OK" "$CAL_WAVES_PER_STEP" "$USABLE_INTERVALS" "$CAL_MIN_ACTIVE_INTERVALS_PER_STEP")"
  if [[ "$P95" =~ ^[0-9]+$ ]]; then
    ANALYSED_STEPS=$(( ANALYSED_STEPS + 1 ))
    if (( P95 <= CAL_TARGET_BLOCK_MS )); then CLASS="SAFE"; else CLASS="UNSAFE"; fi
  else
    CLASS="NO_INTERVALS"
    [[ "$ELIG" == "ELIGIBLE" ]] && ELIG="NO_INTERVALS"
  fi
  STEP_CLASS[$sidx]="$CLASS"; STEP_ELIG[$sidx]="$ELIG"
  [[ "$ELIG" == "ELIGIBLE" || -n "$INELIGIBLE_REASON" ]] || INELIGIBLE_REASON="$ELIG"

  printf '%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s\n' \
    "$sidx" "$SENDERS_OF_STEP" "$ACTIVE_BLOCKS" "$S_OFFERED" "$S_ACCEPTED" "$S_OK" "$S_FAILED" "$S_UNRES" \
    "$S_RATE" "$S_CONC" "$P50" "$P95" "$MAXIV" "$P95GW" "$MAXGW" "$MAXGU" "$MAXTX" "$ACCT_OUT" "$DB_OUT" \
    "$CAL_WAVES_PER_STEP" "$WAVES_OK" "$ELIG" "$CLASS" "$USABLE_INTERVALS" >>"$STEPS_CSV"
done
(( ANALYSED_STEPS > 0 )) || invalidate "no step produced a usable block-interval distribution"

# ---- 8. candidate derivation -------------------------------------------------------------
#
# The algorithm, stated so it can be argued with:
#
#   1. A step is SAFE when its p95 active-block interval is at or under the service
#      target, and UNSAFE otherwise.
#   2. The run establishes a knee only if it BRACKETS one: at least one safe loaded
#      step, and an unsafe step above it. Without a bracket there is no measured
#      transition, and no number is invented.
#   3. The knee estimate is the p95 gas_wanted of the LAST SAFE step. Interpolation
#      between the safe and unsafe steps is deliberately NOT attempted: the ramp is
#      geometric and gives two coarse samples around the transition, which is not
#      enough to justify a linear model. The conservative endpoint is used instead.
#   4. A configurable safety margin is applied downward. It is a policy input with a
#      default, not a hidden constant.
#
# Nothing here ratifies anything. The output is an input to review.
# Layer 2 closes here: everything that could invalidate the MEASUREMENT has run by
# this point, so qualification can consult it rather than being decided in parallel
# with it and reconciled afterwards.
MEASUREMENT_VALID_PRECHECK=YES
[[ -z "$INVALID_REASONS" ]] || MEASUREMENT_VALID_PRECHECK=NO

CANDIDATE="null"; KNEE_GAS="null"; KNEE_BRACKETED=false
STEP_SAFE=""; STEP_UNSAFE=""

# Qualification is decided in a fixed order, most disqualifying first, so a run can
# never acquire a candidate by satisfying a later test while failing an earlier one.
# The disqualification order lives in candidate_precheck, so it is exercised directly
# by the chain-free suite rather than being an untested branch chain here.
CANDIDATE_STATUS="$(candidate_precheck "$MEASUREMENT_VALID_PRECHECK" "$RUN_TRUNCATED" "$INELIGIBLE_REASON")"
if [[ "$CANDIDATE_STATUS" != "PROCEED" ]]; then
  : # blocked; the status names why
else
  # Every configured step is eligible, so the whole ordered response can be
  # classified. Passing the complete sequence is what makes a non-monotonic run
  # visible: the previous pairwise form kept only the first safe and first unsafe
  # step, so SAFE, UNSAFE, SAFE reported a clean bracket while the chain had in fact
  # recovered as load increased.
  SEQ=()
  for (( sidx = 1; sidx <= STEP_IDX; sidx++ )); do
    [[ -n "${STEP_CLASS[$sidx]:-}" ]] && SEQ+=("${STEP_CLASS[$sidx]}")
  done
  CANDIDATE_STATUS="$(knee_classify_sequence "${SEQ[@]:-}")"
  if [[ "$CANDIDATE_STATUS" == "BRACKETED" ]]; then
    # The transition indices, derived from the same sequence that classified it.
    for (( sidx = 1; sidx <= STEP_IDX; sidx++ )); do
      if [[ "${STEP_CLASS[$sidx]:-}" == "SAFE" ]]; then STEP_SAFE="$sidx"
      elif [[ "${STEP_CLASS[$sidx]:-}" == "UNSAFE" && -z "$STEP_UNSAFE" ]]; then STEP_UNSAFE="$sidx"; fi
    done
    KNEE_BRACKETED=true
    KNEE_GAS="$(awk -F, -v s="$STEP_SAFE" 'NR > 1 && $1 == s { print $14; exit }' "$STEPS_CSV")"
    if C="$(candidate_from_knee "$KNEE_GAS" "$CAL_GAS_SAFETY_BPS")"; then
      CANDIDATE="$C"
    else
      KNEE_GAS="null"; KNEE_BRACKETED=false; CANDIDATE_STATUS="NO_GAS_AT_SAFE_STEP"
    fi
  fi
fi

# The permanent-state guards, one per axis and then combined. The tool does not
# invent policy: an axis with no supplied limit stays UNRATIFIED.
#
# Each axis is qualified on DELTA coverage, not on how many blocks were sampled. A
# single missing delta may be the worst-growth block, so partial coverage can
# characterise what was observed but can never certify a maximum — it reports
# INCOMPLETE rather than passing.
#
# The observed worst delta is computed over ACTIVE blocks only, and remains NA when
# no delta was formed at all.
WORST_ACCT_PER_BLOCK="NA"; WORST_APPDB_PER_BLOCK="NA"
if (( ACCT_DELTA_SEEN > 0 )); then
  WORST_ACCT_PER_BLOCK="$(awk -F, 'NR > 1 && $3 == "ACTIVE" && $11 ~ /^-?[0-9]+$/ { if (n == 0 || $11 + 0 > m) { m = $11 + 0; n = 1 } } END { if (n) print m; else print "NA" }' "$BLOCKS_CSV")"
fi
if (( APPDB_DELTA_SEEN > 0 )); then
  WORST_APPDB_PER_BLOCK="$(awk -F, 'NR > 1 && $3 == "ACTIVE" && $13 ~ /^-?[0-9]+$/ { if (n == 0 || $13 + 0 > m) { m = $13 + 0; n = 1 } } END { if (n) print m; else print "NA" }' "$BLOCKS_CSV")"
fi
ACCOUNT_GUARD="$(growth_guard_for_axis "$CAL_MAX_ACCOUNTS_PER_BLOCK" "$ACCOUNT_AXIS" "$ACCT_DELTA_COVERAGE" "$WORST_ACCT_PER_BLOCK")"
APPDB_GUARD="$(growth_guard_for_axis "$CAL_MAX_APPDB_KB_PER_BLOCK" "$APPDB_AXIS" "$APPDB_DELTA_COVERAGE" "$WORST_APPDB_PER_BLOCK")"
STATE_GUARD="$(combine_growth_guards "$ACCOUNT_GUARD" "$APPDB_GUARD")"

# The legitimate lower bound (#107). A ceiling below the protocol's own heaviest
# legitimate block would break the chain, so a candidate cannot be called shippable
# without this comparison.
FLOOR_CLASS="NOT_SUPPLIED"
if [[ "$CAL_LEGITIMATE_GAS_FLOOR" =~ ^[0-9]+$ ]]; then
  if [[ "$CANDIDATE" =~ ^[0-9]+$ ]]; then
    if (( CANDIDATE >= CAL_LEGITIMATE_GAS_FLOOR )); then FLOOR_CLASS="COMPATIBLE"; else FLOOR_CLASS="CONFLICT"; fi
  else FLOOR_CLASS="NO_CANDIDATE"; fi
fi

MEASUREMENT_VALID="$MEASUREMENT_VALID_PRECHECK"
[[ -z "$INVALID_REASONS" ]] || MEASUREMENT_VALID=NO

# The final authority rule lives in candidate_authority, so the consequence of an
# invalid measurement — candidate, knee estimate and bracket flag all nulled together
# — is exercised directly by the chain-free suite rather than being an untested branch
# chain here.
AUTHORITY="$(candidate_authority "$MEASUREMENT_VALID" "$CANDIDATE_STATUS" "$CANDIDATE" \
             "$KNEE_GAS" "$KNEE_BRACKETED" "$FLOOR_CLASS" "$STATE_GUARD")"
set -- $AUTHORITY
CANDIDATE="$1"; KNEE_GAS="$2"; KNEE_BRACKETED="$3"; PRODUCTION_STATUS="$4"

# ---- 9. provenance ----------------------------------------------------------------------
#
# A candidate max_gas is meaningless without knowing what produced it. Unknown fields
# are rendered as null rather than guessed, so a later reviewer can tell "not
# discoverable on that host" from "not recorded".
say "==> writing provenance"
ENDED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
VERSION_LONG="$("$CAL_BIN" version --long 2>/dev/null | sed -n '1,40p')"
BIN_VERSION="$(awk -F': *' '$1=="version" {print $2}' <<<"$VERSION_LONG" | head -1)"
BIN_COMMIT="$(awk -F': *' '$1=="commit" {print $2}' <<<"$VERSION_LONG" | head -1)"
HOST_OS="$(uname -s 2>/dev/null)"; HOST_ARCH="$(uname -m 2>/dev/null)"
CPU_MODEL=""; CPU_COUNT=""; RAM_BYTES=""
case "$HOST_OS" in
  Darwin)
    CPU_MODEL="$(sysctl -n machdep.cpu.brand_string 2>/dev/null)"
    CPU_COUNT="$(sysctl -n hw.ncpu 2>/dev/null)"
    RAM_BYTES="$(sysctl -n hw.memsize 2>/dev/null)" ;;
  Linux)
    CPU_MODEL="$(awk -F': ' '/model name/ { print $2; exit }' /proc/cpuinfo 2>/dev/null)"
    CPU_COUNT="$(getconf _NPROCESSORS_ONLN 2>/dev/null)"
    RAM_BYTES="$(awk '/MemTotal/ { print $2 * 1024; exit }' /proc/meminfo 2>/dev/null)" ;;
esac
# Unknown fields are carried as the sentinel string "null" through --arg and turned
# into a real JSON null by nul() below. Rendering them as the literal STRING "null"
# would make an undiscoverable field indistinguishable from a field whose value
# happens to be that text, and a later reviewer could not tell "this host does not
# report a CPU model" from "the CPU model is null".
jqs() { if [[ -n "${1:-}" ]]; then printf '%s' "$1"; else printf 'null'; fi; }

jq -n \
  --arg run_id "$RUN_ID" --arg started "$STARTED_AT" --arg ended "$ENDED_AT" \
  --arg version "$(jqs "$BIN_VERSION")" --arg commit "$(jqs "$BIN_COMMIT")" --arg bin "$CAL_BIN" \
  --arg chain "$CAL_CHAIN_ID" --arg nodes "$CAL_NODES" \
  --arg smg "$START_MAX_GAS" --arg smb "$START_MAX_BYTES" \
  --arg steps "$CAL_STEPS" --arg waves "$CAL_WAVES_PER_STEP" --arg parallel "$CAL_PARALLEL" \
  --arg mix "$CAL_MIX" --arg mso "$CAL_MULTISEND_OUTPUTS" \
  --arg txgas "$CAL_TX_GAS" --arg txgasms "$CAL_TX_GAS_MULTISEND" --arg amt "$CAL_SEND_AMOUNT" \
  --arg fund "$CAL_FUND_PER_SENDER" --arg fb "$CAL_FUND_BATCH" \
  --arg target "$CAL_TARGET_BLOCK_MS" --arg quiet "$CAL_QUIET_BLOCKS" --arg drain "$CAL_DRAIN_TIMEOUT_S" \
  --arg minblocks "$CAL_MIN_ACTIVE_INTERVALS_PER_STEP" --arg trunc "$RUN_TRUNCATED" \
  --arg maxsec "$CAL_MAX_SECONDS" --arg lag "$CAL_ENDPOINT_LAG_BLOCKS" --arg bps "$CAL_GAS_SAFETY_BPS" \
  --arg accs "$CAL_ACCOUNT_SAMPLING" --arg nodehome "$(jqs "$CAL_NODE_HOME")" \
  --arg acctaxis "$ACCOUNT_AXIS" --arg dbaxis "$APPDB_AXIS" \
  --arg nonce "$CAL_RECIPIENT_NONCE" --arg fresh "$NAMESPACE_FRESH" \
  --arg maxsenders "$MAX_SENDERS" --arg clock "$NOW_MS_SOURCE" \
  --arg os "$(jqs "$HOST_OS")" --arg arch "$(jqs "$HOST_ARCH")" \
  --arg cpu "$(jqs "$CPU_MODEL")" --arg cpun "$(jqs "$CPU_COUNT")" --arg ram "$(jqs "$RAM_BYTES")" \
  --arg accpolicy "$(jqs "$CAL_MAX_ACCOUNTS_PER_BLOCK")" --arg dbpolicy "$(jqs "$CAL_MAX_APPDB_KB_PER_BLOCK")" \
  --arg floor "$(jqs "$CAL_LEGITIMATE_GAS_FLOOR")" \
  --arg hmin "$RAMP_MIN_HEIGHT" --arg hmax "$RAMP_MAX_HEIGHT" \
  'def nul: if . == "null" then null else . end;
   {
     run_id: $run_id,
     started_utc: $started, ended_utc: $ended,
     binary: { path: $bin, version: ($version | nul), commit: ($commit | nul) },
     network: { chain_id: $chain, endpoints: ($nodes | split(",")),
                start_consensus_params: { max_gas: $smg, max_bytes: $smb },
                measured_heights: { from: ($hmin|tonumber), to: ($hmax|tonumber) } },
     load: { steps: $steps, waves_per_step: ($waves|tonumber), broadcast_parallel: ($parallel|tonumber),
             max_senders: ($maxsenders|tonumber), mix: $mix, multisend_outputs: ($mso|tonumber),
             tx_gas: ($txgas|tonumber), tx_gas_multisend: ($txgasms|tonumber),
             send_amount: ($amt|tonumber), fund_per_sender: ($fund|tonumber), fund_batch: ($fb|tonumber) },
     policy: { target_block_ms: ($target|tonumber), quiet_blocks: ($quiet|tonumber),
               drain_timeout_s: ($drain|tonumber), max_seconds: ($maxsec|tonumber),
               endpoint_lag_blocks: ($lag|tonumber), gas_safety_bps: ($bps|tonumber),
               min_active_intervals_per_step: ($minblocks|tonumber),
               max_accounts_per_block: ($accpolicy | nul), max_appdb_kb_per_block: ($dbpolicy | nul),
               legitimate_gas_floor: ($floor | nul) },
     sampling: { account_sampling_requested: ($accs|tonumber), account_axis: $acctaxis,
                 node_home: ($nodehome | nul), appdb_axis: $dbaxis, clock_source: $clock },
     recipients: { namespace_nonce: $nonce, namespace_verified_fresh: $fresh },
     run: { truncated_by_deadline: ($trunc == "1") },
     host: { os: ($os | nul), arch: ($arch | nul), cpu_model: ($cpu | nul),
             cpu_count: ($cpun | nul), ram_bytes: ($ram | nul) }
   }' >"$MANIFEST" || abort "could not write $MANIFEST"

# ---- 10. the machine-readable result ---------------------------------------------------------

jq -n \
  --arg valid "$MEASUREMENT_VALID" --arg reasons "$INVALID_REASONS" \
  --argjson bracketed "$KNEE_BRACKETED" \
  --arg safe "$(jqs "$STEP_SAFE")" --arg unsafe "$(jqs "$STEP_UNSAFE")" \
  --arg target "$CAL_TARGET_BLOCK_MS" --arg knee "$KNEE_GAS" --arg cand "$CANDIDATE" \
  --arg bps "$CAL_GAS_SAFETY_BPS" --arg cstatus "$CANDIDATE_STATUS" \
  --arg guard "$STATE_GUARD" --arg worst "$WORST_ACCT_PER_BLOCK" \
  --arg aguard "$ACCOUNT_GUARD" --arg dguard "$APPDB_GUARD" --arg dworst "$WORST_APPDB_PER_BLOCK" \
  --arg acov "$ACCT_DELTA_COVERAGE" --arg dcov "$APPDB_DELTA_COVERAGE" \
  --arg acovn "$ACCT_DELTA_SEEN" --arg dcovn "$APPDB_DELTA_SEEN" --arg covd "$ACCOUNT_EXPECTED" \
  --arg minblocks "$CAL_MIN_ACTIVE_INTERVALS_PER_STEP" --arg trunc "$RUN_TRUNCATED" \
  --arg acctaxis "$ACCOUNT_AXIS" --arg dbaxis "$APPDB_AXIS" \
  --arg floor "$(jqs "$CAL_LEGITIMATE_GAS_FLOOR")" --arg fclass "$FLOOR_CLASS" \
  --arg pstatus "$PRODUCTION_STATUS" --arg run_id "$RUN_ID" \
  '{
     run_id: $run_id,
     measurement_valid: ($valid == "YES"),
     invalid_reasons: (if $reasons == "" then [] else ($reasons | split("; ")) end),
     knee_bracketed: $bracketed,
     safe_step: (if $safe == "null" then null else ($safe|tonumber) end),
     unsafe_step: (if $unsafe == "null" then null else ($unsafe|tonumber) end),
     target_block_ms: ($target|tonumber),
     estimated_knee_gas: (if $knee == "null" then null else ($knee|tonumber) end),
     safety_margin_bps: ($bps|tonumber),
     performance_candidate_max_gas: (if $cand == "null" then null else ($cand|tonumber) end),
     candidate_status: $cstatus,
     interpolation: "NOT_ATTEMPTED_GEOMETRIC_RAMP_TOO_COARSE",
     run_truncated_by_deadline: ($trunc == "1"),
     min_active_intervals_per_step: ($minblocks|tonumber),
     state_growth: {
       account_guard: $aguard,
       appdb_guard: $dguard,
       combined_guard: $guard,
       account_axis: $acctaxis,
       appdb_axis: $dbaxis,
       account_delta_coverage: { class: $acov, seen: ($acovn|tonumber), expected: ($covd|tonumber) },
       appdb_delta_coverage:   { class: $dcov, seen: ($dcovn|tonumber), expected: ($covd|tonumber) },
       worst_accounts_per_active_block: (if $worst == "NA" then null else ($worst|tonumber) end),
       worst_appdb_kb_per_active_block: (if $dworst == "NA" then null else ($dworst|tonumber) end)
     },
     legitimate_gas_floor: (if $floor == "null" then null else ($floor|tonumber) end),
     legitimate_floor_comparison: $fclass,
     production_candidate_status: $pstatus,
     note: "A candidate is an input to ratification, not a ratified parameter. It does not close TW-004."
   }' >"$RESULT" || abort "could not write $RESULT"

# ---- 11. summary --------------------------------------------------------------------------

TOTAL_OFFERED="$(jq -s 'length' "$TXLOG" 2>/dev/null || echo 0)"
TOTAL_ACCEPTED="$(jq -s '[.[] | select(.check_code == 0)] | length' "$TXLOG" 2>/dev/null || echo 0)"
TOTAL_INCLUDED="$(jq -s '[.[] | select(.height != null)] | length' "$TXLOG" 2>/dev/null || echo 0)"
TOTAL_OK="$(jq -s '[.[] | select(.status == "DELIVERED_OK")] | length' "$TXLOG" 2>/dev/null || echo 0)"
TOTAL_FAILED="$(jq -s '[.[] | select(.status == "DELIVERED_FAILED")] | length' "$TXLOG" 2>/dev/null || echo 0)"
TOTAL_REJECTED="$(jq -s '[.[] | select(.status == "CHECK_REJECTED")] | length' "$TXLOG" 2>/dev/null || echo 0)"
TOTAL_TIMEOUT="$(jq -s '[.[] | select(.status == "NOT_FOUND_TIMEOUT")] | length' "$TXLOG" 2>/dev/null || echo 0)"

say ""
say "=== calibration summary  (run $RUN_ID) ==="
say "measurement_valid: $MEASUREMENT_VALID"
[[ -n "$INVALID_REASONS" ]] && say "  reasons: $INVALID_REASONS"
say ""
say "transaction outcomes (CheckTx admission is NOT execution):"
printf '  %-22s %s\n' offered "$TOTAL_OFFERED"
printf '  %-22s %s\n' check_accepted "$TOTAL_ACCEPTED"
printf '  %-22s %s\n' check_rejected "$TOTAL_REJECTED"
printf '  %-22s %s\n' included "$TOTAL_INCLUDED"
printf '  %-22s %s\n' delivered_ok "$TOTAL_OK"
printf '  %-22s %s\n' delivered_failed "$TOTAL_FAILED"
printf '  %-22s %s\n' unresolved_timeout "$TOTAL_TIMEOUT"
say ""
say "measured heights $RAMP_MIN_HEIGHT..$RAMP_MAX_HEIGHT ($UNREADABLE_BLOCKS unreadable, $TIMING_UNREADABLE with unparseable timestamps)"
(( RUN_TRUNCATED )) && warn "the ramp was truncated by CAL_MAX_SECONDS; no candidate may be derived from it"
say "state-growth axes: accounts=$ACCOUNT_AXIS ($ACCOUNT_SAMPLES of $ACCOUNT_EXPECTED active blocks sampled), appdb=$APPDB_AXIS"
say "state-growth delta coverage: accounts=$ACCT_DELTA_COVERAGE ($ACCT_DELTA_SEEN/$ACCOUNT_EXPECTED), appdb=$APPDB_DELTA_COVERAGE ($APPDB_DELTA_SEEN/$ACCOUNT_EXPECTED)"
[[ "$ACCOUNT_AXIS" == "AVAILABLE" ]] || warn "account growth is $ACCOUNT_AXIS — the columns below read NA, which is NOT zero growth"
say "starting block.max_gas: $START_MAX_GAS (finite: $START_MAX_GAS_FINITE)"
say "endpoint agreement: $AGREE_OK of $AGREE_SAMPLES sampled heights; final heights:$FINAL_HEIGHTS"
say ""
say "per step (ACTIVE blocks only; QUIET drain blocks excluded):"
column -s, -t "$STEPS_CSV" 2>/dev/null || cat "$STEPS_CSV"
say ""
say "knee: p95 active block interval against a ${CAL_TARGET_BLOCK_MS}ms target"
say "  minimum usable block intervals per contributing step: $CAL_MIN_ACTIVE_INTERVALS_PER_STEP"
STEP_RESPONSE=""
for (( i = 1; i <= STEP_IDX; i++ )); do
  STEP_RESPONSE="$STEP_RESPONSE $i:${STEP_CLASS[$i]:-?}/${STEP_ELIG[$i]:-?}"
done
say "  step response:     $STEP_RESPONSE   (class/eligibility; intervals in steps.csv)"
say "  highest safe step:  ${STEP_SAFE:-none}"
say "  first unsafe step:  ${STEP_UNSAFE:-none}"
say "  bracketed:          $KNEE_BRACKETED"
say "  candidate status:   $CANDIDATE_STATUS"
if [[ "$CANDIDATE" =~ ^[0-9]+$ ]]; then
  say "  estimated knee gas: $KNEE_GAS  (p95 gas_wanted of the last safe step)"
  say "  safety margin:      ${CAL_GAS_SAFETY_BPS} bps"
  say "  PERFORMANCE candidate max_gas: $CANDIDATE"
else
  case "$CANDIDATE_STATUS" in
    UNBOUNDED_BY_RUN_INCREASE_LOAD)
      say "  no candidate: the ramp never became unsafe. Raise CAL_STEPS and re-run;" 
      say "  the top step is a floor on capacity, not a measurement of the knee." ;;
    BELOW_TEST_RANGE_REDUCE_LOAD)
      say "  no candidate: the first loaded step was already unsafe. Lower CAL_STEPS and re-run." ;;
    NON_MONOTONIC_RESPONSE_RETRY)
      say "  no candidate: the load response is not monotonic — a step recovered above an"
      say "  unsafe one. That is a confounded experiment, not a knee. Re-run on a quiet host." ;;
    INSUFFICIENT_ACTIVE_INTERVALS_NO_CANDIDATE)
      say "  no candidate: a contributing step supplied fewer than $CAL_MIN_ACTIVE_INTERVALS_PER_STEP usable"
      say "  block intervals, so its p95 is not a tail measurement. Note this counts"
      say "  INTERVALS, not active blocks — a block contributes none if it opens the range"
      say "  or its timestamp did not parse. Lengthen the steps, or lower"
      say "  CAL_MIN_ACTIVE_INTERVALS_PER_STEP deliberately to exercise the mechanics." ;;
    TRUNCATED_RUN_NO_COMPLETE_BRACKET)
      say "  no candidate: the ramp was cut short by CAL_MAX_SECONDS, so at least one step"
      say "  never ran its configured waves. Raise CAL_MAX_SECONDS or shorten the ramp." ;;
    INCOMPLETE_STEPS_NO_CANDIDATE)
      say "  no candidate: a step did not complete its configured waves with a full workload." ;;
    MEASUREMENT_INVALID)
      say "  no candidate: the measurement itself was not trustworthy (see reasons above)." ;;
    *) say "  no candidate: $CANDIDATE_STATUS" ;;
  esac
fi
say ""
say "state-growth guards:  account=$ACCOUNT_GUARD appdb=$APPDB_GUARD combined=$STATE_GUARD"
say "  worst per active block: accounts=$WORST_ACCT_PER_BLOCK appdb_kb=$WORST_APPDB_PER_BLOCK"
say "  policies supplied:      accounts=${CAL_MAX_ACCOUNTS_PER_BLOCK:-none} appdb_kb=${CAL_MAX_APPDB_KB_PER_BLOCK:-none}"
say "legitimate floor:     ${CAL_LEGITIMATE_GAS_FLOOR:-not supplied} -> $FLOOR_CLASS"
say "production status:    $PRODUCTION_STATUS"
say ""
say "This rig does NOT ratify block.max_gas. A candidate is an input to review, to be"
say "read together with the permanent-state columns, the heaviest legitimate block"
say "(#107), and the hardware recorded in manifest.json — which is part of the result."
say ""
say "artifacts:"
for f in "$RESULT" "$MANIFEST" "$STEPS_CSV" "$WAVES_CSV" "$BLOCKS_CSV" "$TXLOG" "$SENDERS_FILE"; do
  [[ -s "$f" ]] && say "  $f"
done
[[ -s "$APPDB_CSV" ]] && say "  $APPDB_CSV"

# The experiment's own trustworthiness decides the exit status. The candidate does
# not: "no knee in range" is a legitimate, well-formed result.
if [[ "$MEASUREMENT_VALID" != "YES" ]]; then
  say ""
  say "EXIT: the measurement was invalid; no candidate was emitted."
  exit 1
fi
exit 0
