#!/usr/bin/env bash
# Shared primitives for the block-gas tooling (issue #160). Sourced, not executed.
#
# Two consumers use these: load-calibration.sh, which measures offered load against
# block gas and permanent state growth, and block-gas-drill.sh, which asserts the
# ceiling holds. Both make claims about what a block CONTAINED, so every reader here
# either produces a validated value or fails.
#
# The rule that shapes all of it: a block with no transactions has a gas total of
# zero, and an unreadable block has no gas total at all. Those must never render the
# same way. A `// 0` fallback would collapse them, and the collapse is silent in the
# direction that matters — "the ceiling was never exceeded" is exactly what you get
# from a window where nothing could be read.

# ---- transport ----------------------------------------------------------------
#
# Indirected through one function so the fault suite can drive every parser from
# fixtures without a chain. The shipped implementation is a plain HTTP GET.
blockgas_get() { # <base-url> <path>
  curl -fsS --max-time "${BLOCKGAS_HTTP_TIMEOUT:-10}" "$1$2" 2>/dev/null
}

# ---- per-block readers ----------------------------------------------------------

# block_gas <base-url> <height> <gas_wanted|gas_used> — the summed gas of every
# transaction in one block.
#
# The requested height is checked against the height the node answered with. A node
# that has pruned, or one that silently clamps an out-of-range request to its head,
# would otherwise return a real block that is not the one under measurement — and
# every gas figure downstream would describe the wrong height.
block_gas() {
  local base="$1" height="$2" field="$3" body
  case "$field" in gas_wanted|gas_used) ;; *) return 1 ;; esac
  [[ "$height" =~ ^[0-9]+$ ]] || return 1
  body="$(blockgas_get "$base" "/block_results?height=$height")" || return 1
  [[ -n "$body" ]] || return 1
  jq -er --arg h "$height" --arg f "$field" '
      if type != "object" then error("not an object") else . end
    | if has("result") | not then error("no result") else .result end
    | if (.height | tostring) != $h then error("height mismatch") else . end
    | if has("txs_results") | not then error("no txs_results") else .txs_results end
    | (. // [])
    | if type != "array" then error("txs_results is not an array") else . end
    | [ .[] | .[$f]
        | if . == null then error("missing gas field")
          elif type == "string" then (if test("^-?[0-9]+$") then tonumber else error("gas not an integer") end)
          elif type == "number" then .
          else error("gas is the wrong type") end ]
    | add // 0
  ' <<<"$body" 2>/dev/null || return 1
}

# block_tx_codes <base-url> <height> — one delivered result code per transaction,
# newline separated.
#
# The codes are collected and joined rather than streamed. `jq -e` exits non-zero
# when it produced NO output, so a streamed version reports an empty block and an
# unreadable one identically — and a caller counting non-zero codes would then read
# an unreadable block as "no failures", which is the wrong answer in the
# safe-looking direction. Joining makes an empty block a successful empty string.
block_tx_codes() {
  local base="$1" height="$2" body
  [[ "$height" =~ ^[0-9]+$ ]] || return 1
  body="$(blockgas_get "$base" "/block_results?height=$height")" || return 1
  [[ -n "$body" ]] || return 1
  jq -er --arg h "$height" '
      if type != "object" then error("not an object") else . end
    | if has("result") | not then error("no result") else .result end
    | if (.height | tostring) != $h then error("height mismatch") else . end
    | if has("txs_results") | not then error("no txs_results") else .txs_results end
    | (. // [])
    | if type != "array" then error("txs_results is not an array") else . end
    | [ .[] | (.code // 0) | tostring ] | join("\n")
  ' <<<"$body" 2>/dev/null || return 1
}

# block_meta <base-url> <height> <block_size|num_txs|time> — a field of the block's
# metadata, taken from /blockchain so size, transaction count and timestamp all come
# from one request.
#
# /blockchain answers a RANGE, and answers it newest-first. Asking for a single
# height and then requiring exactly one entry at exactly that height is what keeps a
# clamped or widened range from being read as the requested block.
block_meta() {
  local base="$1" height="$2" field="$3" body
  case "$field" in block_size|num_txs|time) ;; *) return 1 ;; esac
  [[ "$height" =~ ^[0-9]+$ ]] || return 1
  body="$(blockgas_get "$base" "/blockchain?minHeight=$height&maxHeight=$height")" || return 1
  [[ -n "$body" ]] || return 1
  jq -er --arg h "$height" --arg f "$field" '
      if type != "object" then error("not an object") else . end
    | if has("result") | not then error("no result") else .result end
    | (.block_metas // [])
    | if type != "array" then error("block_metas is not an array") else . end
    | if length != 1 then error("expected exactly one block meta") else .[0] end
    | if (.header.height | tostring) != $h then error("height mismatch") else . end
    | if $f == "time" then .header.time
      elif $f == "num_txs" then .num_txs
      else .block_size end
    | if . == null then error("missing field") else tostring end
    | if length == 0 then error("empty field") else . end
  ' <<<"$body" 2>/dev/null || return 1
}

# live_max_gas <base-url> — block.max_gas as the CHAIN reports it at its head.
#
# Read from CometBFT rather than from a genesis file: the file says what a node was
# started with, and this says what consensus is enforcing now. For TW-004 only the
# second is the claim.
live_max_gas() {
  local body
  body="$(blockgas_get "$1" "/consensus_params")" || return 1
  [[ -n "$body" ]] || return 1
  jq -er '
      if type != "object" then error("not an object") else . end
    | if has("result") | not then error("no result") else .result end
    | .consensus_params.block.max_gas
    | if . == null then error("no max_gas") else tostring end
    | if test("^-?[0-9]+$") | not then error("max_gas is not an integer") else . end
  ' <<<"$body" 2>/dev/null || return 1
}

# max_gas_is_finite <value> — the TW-004 predicate, as a predicate.
#
# `-1` is the unlimited sentinel, but it is not the only value that fails to bound a
# block: zero and negatives are equally not a working ceiling, and a caller that only
# compared against -1 would accept them. Non-numeric input is refused rather than
# treated as either answer.
max_gas_is_finite() {
  local v="${1:-}"
  [[ "$v" =~ ^-?[0-9]+$ ]] || { echo "unreadable"; return 1; }
  if (( v > 0 )); then echo "yes"; else echo "no"; fi
}

# ---- RFC3339 -> epoch milliseconds ----------------------------------------------
#
# Block timestamps arrive as RFC3339 with nanoseconds. Converting them with `date`
# is not portable: GNU takes -d, BSD takes -j -f, and the two disagree about
# fractional seconds. This is pure arithmetic, so it behaves the same on a
# maintainer's laptop and on a Linux runner.
#
# Days are computed with the civil-calendar algorithm rather than a leap-year
# special case, because the naive version is wrong on century boundaries and the
# error is a silent one-day shift in every block-time delta.
rfc3339_to_ms() { # <timestamp> -> epoch milliseconds
  local ts="${1:-}" y m d hh mm ss frac ms era yoe doy doe days
  [[ "$ts" =~ ^([0-9]{4})-([0-9]{2})-([0-9]{2})T([0-9]{2}):([0-9]{2}):([0-9]{2})(\.[0-9]+)?Z$ ]] || return 1
  y=$((10#${BASH_REMATCH[1]})); m=$((10#${BASH_REMATCH[2]})); d=$((10#${BASH_REMATCH[3]}))
  hh=$((10#${BASH_REMATCH[4]})); mm=$((10#${BASH_REMATCH[5]})); ss=$((10#${BASH_REMATCH[6]}))
  frac="${BASH_REMATCH[7]:-}"
  (( m >= 1 && m <= 12 && d >= 1 && d <= 31 && hh < 24 && mm < 60 && ss < 61 )) || return 1
  # Milliseconds from the first three fractional digits, right-padded so ".5" is
  # 500ms and not 5ms.
  frac="${frac#.}"; frac="${frac}000"; ms=$((10#${frac:0:3}))
  (( m <= 2 )) && y=$((y - 1))
  era=$(( y / 400 ))
  yoe=$(( y - era * 400 ))
  if (( m > 2 )); then doy=$(( (153 * (m - 3) + 2) / 5 + d - 1 ))
  else doy=$(( (153 * (m + 9) + 2) / 5 + d - 1 )); fi
  doe=$(( yoe * 365 + yoe / 4 - yoe / 100 + doy ))
  days=$(( era * 146097 + doe - 719468 ))
  echo $(( (days * 86400 + hh * 3600 + mm * 60 + ss) * 1000 + ms ))
}

# ---- bech32 ---------------------------------------------------------------------
#
# The calibration rig has to mint fresh recipient addresses at flood rate, because
# permanent account growth is only observable when the recipients do not already
# exist — and that is the growth TW-006 is about. One `twilightd` invocation per
# address costs ~115ms, which would put address generation, rather than the chain,
# on the critical path of the very measurement being taken.
#
# So the encoder is here, in bash arithmetic, with no forks. That is only acceptable
# because it is checked two ways: pinned vectors in the fault suite, and a preflight
# in the rig that encodes a value and compares it against the binary's own codec
# before a single transaction is built. A wrong encoder would produce syntactically
# valid addresses the chain rejects, and the rig would then be measuring its own
# rejections — which is precisely the kind of result that looks like data.
BECH32_CHARSET="qpzry9x8gf2tvdw0s3jn54khce6mua7l"
BECH32_HRP=""
BECH32_HRP_VALS=()

bech32_init() { # <hrp>
  local hrp="${1:-}" i c n
  [[ -n "$hrp" ]] || return 1
  BECH32_HRP="$hrp"; BECH32_HRP_VALS=()
  for (( i = 0; i < ${#hrp}; i++ )); do
    c="${hrp:$i:1}"; printf -v n '%d' "'$c"; BECH32_HRP_VALS+=( $(( n >> 5 )) )
  done
  BECH32_HRP_VALS+=( 0 )
  for (( i = 0; i < ${#hrp}; i++ )); do
    c="${hrp:$i:1}"; printf -v n '%d' "'$c"; BECH32_HRP_VALS+=( $(( n & 31 )) )
  done
}

# bech32_encode_into <var> <hex> — BIP-173 encoding of a 20-byte address payload.
#
# Assigns through printf -v instead of printing, so the hot path costs no subshell.
bech32_encode_into() {
  local __var="$1" hex="$2" i n acc=0 bits=0 out="" chk=1 b v
  local -a data=()
  [[ -n "$BECH32_HRP" ]] || return 1
  [[ "$hex" =~ ^[0-9a-fA-F]+$ ]] || return 1
  (( ${#hex} % 2 == 0 )) || return 1
  for (( i = 0; i < ${#hex}; i += 2 )); do
    n=$(( 16#${hex:$i:2} ))
    acc=$(( (acc << 8) | n )); bits=$(( bits + 8 ))
    while (( bits >= 5 )); do bits=$(( bits - 5 )); data+=( $(( (acc >> bits) & 31 )) ); done
  done
  (( bits > 0 )) && data+=( $(( (acc << (5 - bits)) & 31 )) )
  # Six zero groups, not five: the checksum occupies six, and padding with five
  # yields a well-formed address with a wrong checksum — accepted by nothing and
  # obviously wrong only once a node has refused it.
  for v in "${BECH32_HRP_VALS[@]}" "${data[@]}" 0 0 0 0 0 0; do
    b=$(( chk >> 25 )); chk=$(( ((chk & 0x1ffffff) << 5) ^ v ))
    (( (b >> 0) & 1 )) && chk=$(( chk ^ 0x3b6a57b2 ))
    (( (b >> 1) & 1 )) && chk=$(( chk ^ 0x26508e6d ))
    (( (b >> 2) & 1 )) && chk=$(( chk ^ 0x1ea119fa ))
    (( (b >> 3) & 1 )) && chk=$(( chk ^ 0x3d4233dd ))
    (( (b >> 4) & 1 )) && chk=$(( chk ^ 0x2a1462b3 ))
  done
  chk=$(( chk ^ 1 ))
  for n in "${data[@]}"; do out="$out${BECH32_CHARSET:$n:1}"; done
  for (( i = 0; i < 6; i++ )); do
    n=$(( (chk >> (5 * (5 - i))) & 31 )); out="$out${BECH32_CHARSET:$n:1}"
  done
  printf -v "$__var" '%s' "${BECH32_HRP}1${out}"
}

# bech32_agrees_with_binary <binary> <hrp> — does the local encoder agree with the
# chain's own address codec?
#
# The rig calls this before it mints anything. Two payloads, not one: a single
# vector can be matched by an encoder that is wrong in a payload-dependent way, and
# the all-zero payload in particular exercises none of the carry paths.
bech32_agrees_with_binary() {
  local bin="$1" hrp="$2" hex want got
  [[ -x "$bin" ]] || return 1
  bech32_init "$hrp" || return 1
  for hex in 0102030405060708090a0b0c0d0e0f1011121314 deadbeef00112233445566778899aabbccddeeff; do
    want="$("$bin" debug addr "$hex" 2>/dev/null | awk '/^Bech32 Acc/ { print $3 }')"
    [[ -n "$want" ]] || return 1
    bech32_encode_into got "$hex" || return 1
    [[ "$want" == "$got" ]] || return 1
  done
  return 0
}

# ---- transaction lifecycle -------------------------------------------------------
#
# CheckTx is admission, not execution. A transaction can pass CheckTx, occupy a
# block, and still fail — and counting that as offered work completed is a false
# measurement in the optimistic direction. Every consumer here therefore tracks
# three separate facts: accepted, included, delivered.
#
# The statuses are closed and exhaustive, so a transaction is never simply absent
# from the accounting:
#
#   CHECK_REJECTED     refused at admission; never occupied a block
#   ACCEPTED_PENDING   admitted, not yet observed in a block
#   DELIVERED_OK       included and executed successfully
#   DELIVERED_FAILED   included and executed with a non-zero code
#   NOT_FOUND_TIMEOUT  admitted, never resolved — a MEASUREMENT failure, not a
#                      chain result, and never silently folded into either side

# blockgas_post <base-url> <json-rpc-body> — a JSON-RPC POST.
blockgas_post() {
  curl -fsS --max-time "${BLOCKGAS_HTTP_TIMEOUT:-10}" -H 'Content-Type: application/json' \
    -X POST --data "$2" "$1" 2>/dev/null
}

# broadcast_signed <base-url> <base64-tx> — submit pre-signed bytes, print
# "<check-code> <hash>".
#
# Pre-signed bytes rather than a CLI invocation: a harness that spawns the binary
# inside its own timing window measures process startup and signing, and reports it
# as chain admission latency. Signing happens before the window; this is the only
# thing inside it.
broadcast_signed() {
  local base="$1" b64="$2" body resp
  [[ -n "$b64" ]] || return 1
  body="$(jq -cn --arg t "$b64" '{jsonrpc:"2.0",id:1,method:"broadcast_tx_sync",params:{tx:$t}}')" || return 1
  resp="$(blockgas_post "$base" "$body")" || return 1
  [[ -n "$resp" ]] || return 1
  jq -er '
      if type != "object" then error("not an object") else . end
    | if has("result") | not then error("no result") else .result end
    | if has("code") | not then error("no code") else . end
    | "\(.code) \(.hash // "")"
  ' <<<"$resp" 2>/dev/null || return 1
}

# tx_outcome <base-url> <hash> — print "<height> <deliver-code>" for a committed
# transaction, or fail while it is not yet in a block.
#
# Failure here means "not resolved yet", which is why callers must impose their own
# deadline and record NOT_FOUND_TIMEOUT rather than treating an unresolved
# transaction as either a success or a rejection.
tx_outcome() {
  local base="$1" hash="$2" resp
  [[ "$hash" =~ ^[0-9A-Fa-f]+$ ]] || return 1
  resp="$(blockgas_get "$base" "/tx?hash=0x$hash")" || return 1
  [[ -n "$resp" ]] || return 1
  jq -er '
      if type != "object" then error("not an object") else . end
    | if has("result") | not then error("no result") else .result end
    | if has("tx_result") | not then error("no tx_result") else . end
    | (.height | tostring) as $h
    | if ($h | test("^[0-9]+$")) | not then error("bad height") else . end
    | "\($h) \(.tx_result.code // 0)"
  ' <<<"$resp" 2>/dev/null || return 1
}

# mempool_depth <base-url> — transactions currently held in this node's mempool.
#
# The backlog evidence a saturation proof needs. A single sample taken after
# submission is not enough: blocks may already have drained it, so a caller must
# sample THROUGHOUT submission and keep the peak.
mempool_depth() {
  local resp
  resp="$(blockgas_get "$1" /num_unconfirmed_txs)" || return 1
  [[ -n "$resp" ]] || return 1
  jq -er '
      if type != "object" then error("not an object") else . end
    | if has("result") | not then error("no result") else .result end
    | if has("n_txs") | not then error("no n_txs") else .n_txs end
    | tostring
    | if test("^[0-9]+$") | not then error("n_txs is not an unsigned integer") else . end
  ' <<<"$resp" 2>/dev/null || return 1
}

# ---- statistics -------------------------------------------------------------------
#
# A knee taken from a median hides exactly the behaviour that matters: a step where
# most blocks are comfortable and a tail is not is a step that has begun to fail. The
# tail metric is the load-response signal; the median is decoration next to it.

# percentile_of <p> <value>... — the p-th percentile by nearest-rank, on unsigned
# integers. Refuses rather than inventing a value for an empty sample, because an
# empty sample is a measurement gap and zero would read as an excellent result.
percentile_of() {
  local p="$1"; shift
  local n idx
  [[ "$p" =~ ^[0-9]+$ ]] && (( p >= 1 && p <= 100 )) || return 1
  (( $# > 0 )) || return 1
  local v
  for v in "$@"; do [[ "$v" =~ ^[0-9]+$ ]] || return 1; done
  n=$#
  # Nearest-rank: ceil(p/100 * n), clamped into [1, n].
  idx=$(( (p * n + 99) / 100 ))
  (( idx < 1 )) && idx=1
  (( idx > n )) && idx=n
  printf '%s\n' "$@" | LC_ALL=C sort -n | sed -n "${idx}p"
}

# ---- run identity ------------------------------------------------------------------

# run_nonce — a collision-resistant identifier for one run.
#
# The recipient namespace is derived from this. A short cycling salt (seconds modulo
# a million, say) repeats within days, and a repeat against a persistent network
# silently reuses recipients that already have accounts — which flattens the
# permanent-growth column while every other part of the run looks healthy. That is a
# false measurement in the reassuring direction, so the namespace is made wide enough
# that reuse is not a practical concern, and it is then VERIFIED rather than trusted.
run_nonce() {
  local raw
  if [[ -r /dev/urandom ]]; then
    raw="$(LC_ALL=C tr -dc 'a-f0-9' </dev/urandom 2>/dev/null | dd bs=1 count=16 2>/dev/null)"
  fi
  if [[ ! "$raw" =~ ^[0-9a-f]{16}$ ]]; then
    # No entropy source: fall back to something still wide, and let the caller see
    # that it was derived rather than drawn.
    printf -v raw '%08x%08x' "$(date -u +%s)" "$$"
  fi
  echo "$raw"
}

# sign_and_encode_into <var> <bin> <home> <keyring> <chain-id> <key> <accnum> <seq> <unsigned-file>
#
# Sign an unsigned transaction document offline and encode it to the base64 wire
# bytes, so the only thing left inside a timing window is an HTTP POST.
#
# Offline with an explicit account number and sequence: a signer that resolves those
# from a node opens a round trip per transaction against the very node the run is
# about to saturate, and the burst then paces itself against the congestion it was
# supposed to create.
sign_and_encode_into() {
  local __var="$1" bin="$2" home="$3" kr="$4" chain="$5" key="$6" acc="$7" seq="$8" unsigned="$9"
  local signed b64
  [[ -s "$unsigned" ]] || return 1
  [[ "$acc" =~ ^[0-9]+$ && "$seq" =~ ^[0-9]+$ ]] || return 1
  signed="${unsigned%.json}.signed.json"
  "$bin" tx sign "$unsigned" --from "$key" --keyring-backend "$kr" --home "$home" \
    --chain-id "$chain" --offline -a "$acc" -s "$seq" --output-document "$signed" >/dev/null 2>&1 || return 1
  [[ -s "$signed" ]] || return 1
  b64="$("$bin" tx encode "$signed" 2>/dev/null)" || return 1
  # Base64 only. An error message on stdout would otherwise be posted to the node as
  # a transaction and rejected, and the run would record a rejection it caused.
  [[ "$b64" =~ ^[A-Za-z0-9+/]+=*$ ]] || return 1
  printf -v "$__var" '%s' "$b64"
}

# commit_timeout_ms <duration> — a CometBFT commit timeout ("3s", "200ms") in
# milliseconds.
#
# The liveness bound is expressed as a multiple of the configured block cadence
# rather than as a bare number, so a drill run at a different cadence does not
# quietly acquire a bound that means something else.
commit_timeout_ms() {
  local d="${1:-}"
  if [[ "$d" =~ ^([0-9]+)ms$ ]]; then echo $((10#${BASH_REMATCH[1]})); return 0; fi
  if [[ "$d" =~ ^([0-9]+)s$ ]]; then echo $((10#${BASH_REMATCH[1]} * 1000)); return 0; fi
  if [[ "$d" =~ ^([0-9]+)$ ]]; then echo $((10#${BASH_REMATCH[1]} * 1000)); return 0; fi
  return 1
}

# block_binds_gas_ceiling <gas-wanted> <tx-gas> <max-gas> — did the gas ceiling, and
# not a shortage of transactions, decide the size of this block?
#
# The exact test is whether ONE MORE transaction of the flood's size would have
# exceeded the ceiling. A block merely being large does not show the ceiling bound;
# a block that could not have taken another does.
#
# Both CometBFT's mempool reaping and the SDK's proposal handler admit while the
# running total is <= max_gas, so the boundary case (a block exactly at the ceiling)
# is binding and is included here.
block_binds_gas_ceiling() {
  local gw="${1:-}" txgas="${2:-}" maxgas="${3:-}"
  [[ "$gw" =~ ^[0-9]+$ && "$txgas" =~ ^[0-9]+$ && "$maxgas" =~ ^[0-9]+$ ]] || { echo "unreadable"; return 1; }
  (( gw > 0 )) || { echo "no"; return 0; }
  if (( gw + txgas > maxgas )); then echo "yes"; else echo "no"; fi
}

# ---- calibration analysis predicates -----------------------------------------------
#
# These live here, rather than inline in the rig, so the chain-free fault suite
# exercises the code that actually runs. A second, more permissive copy written in
# test shell would prove nothing about the instrument.

# max_concurrency_of <intervals-csv> — the greatest number of broadcasters in flight
# at once, from their recorded start/end times.
#
# Measured rather than inferred from the configured bound. A step described as "256
# concurrent" that was actually delivered as sixteen successive batches of sixteen is
# a mislabelled x-axis, and the analysis would attribute the chain's response to a
# load that was never offered.
#
# At equal timestamps a start is counted before an end, which yields the maximum
# consistent with the data rather than an optimistic minimum.
max_concurrency_of() {
  local f="$1" rows out
  [[ -s "$f" ]] || return 1
  # A header-only file is not "zero broadcasters in flight" — it is no measurement at
  # all, and reporting 0 would put a fabricated concurrency into the step table. The
  # row count is checked before the sweep, so the two cannot render alike.
  rows="$(awk -F, 'NR > 1 && $1 ~ /^[0-9]+$/ && $2 ~ /^[0-9]+$/ { n++ } END { print n + 0 }' "$f")"
  [[ "$rows" =~ ^[0-9]+$ ]] && (( rows > 0 )) || return 1
  out="$(awk -F, 'NR > 1 && $1 ~ /^[0-9]+$/ && $2 ~ /^[0-9]+$/ { print $1 " 1"; print $2 " -1" }' "$f" \
    | LC_ALL=C sort -k1,1n -k2,2nr \
    | awk '{ c += $2; if (c > m) m = c } END { print m + 0 }')"
  [[ "$out" =~ ^[0-9]+$ ]] && (( out > 0 )) || return 1
  echo "$out"
}

# attribution_overlaps <attribution-file> — how many heights are claimed by more than
# one load step.
#
# The file is "<height> <step> ACTIVE", sorted by height. Any height appearing under
# two different steps means a block would be counted in two load responses, and the
# knee could then move because of harness bookkeeping rather than chain behaviour.
attribution_overlaps() {
  local f="$1"
  [[ -f "$f" ]] || return 1
  awk '{ if ($1 == prevh && $2 != prevs) c++; prevh = $1; prevs = $2 } END { print c + 0 }' "$f"
}

# knee_classify <safe-step> <unsafe-step> — what the run established about the knee.
#
# A run only establishes a knee if it BRACKETS one. Every other outcome names what to
# change and yields no number: a rig that extrapolated past its own measurements
# would be inventing the parameter it was built to measure.
knee_classify() {
  local safe="${1:-}" unsafe="${2:-}"
  if [[ -n "$safe" && -n "$unsafe" ]]; then
    [[ "$safe" =~ ^[0-9]+$ && "$unsafe" =~ ^[0-9]+$ ]] || { echo "NO_USABLE_STEPS"; return 0; }
    if (( unsafe > safe )); then echo "BRACKETED"; else echo "NO_USABLE_STEPS"; fi
  elif [[ -n "$safe" ]]; then echo "UNBOUNDED_BY_RUN_INCREASE_LOAD"
  elif [[ -n "$unsafe" ]]; then echo "BELOW_TEST_RANGE_REDUCE_LOAD"
  else echo "NO_USABLE_STEPS"; fi
}

# candidate_from_knee <knee-gas> <safety-bps> — the safety-adjusted candidate.
#
# The margin reduces the estimate; it is a policy input with a default rather than a
# hidden constant. Refuses on unusable input instead of producing a number.
candidate_from_knee() {
  local knee="${1:-}" bps="${2:-}"
  [[ "$knee" =~ ^[0-9]+$ ]] && (( knee > 0 )) || return 1
  [[ "$bps" =~ ^[0-9]+$ ]] && (( bps < 10000 )) || return 1
  echo $(( knee * (10000 - bps) / 10000 ))
}

# growth_render <sum> <samples-seen> — a growth total, or NA when nothing was sampled.
#
# The whole point: an axis that was never measured must not render as 0. Zero growth
# is the most reassuring possible reading, and it is exactly what an unsampled axis
# would produce from a numerically-initialised accumulator.
growth_render() {
  local sum="${1:-}" seen="${2:-}"
  [[ "$seen" =~ ^[0-9]+$ ]] || { echo "NA"; return 0; }
  (( seen > 0 )) || { echo "NA"; return 0; }
  [[ "$sum" =~ ^-?[0-9]+$ ]] || { echo "NA"; return 0; }
  echo "$sum"
}

# axis_availability <requested> <samples> <expected> — how an axis should be reported.
axis_availability() {
  local requested="${1:-}" samples="${2:-0}" expected="${3:-0}"
  [[ "$requested" == "1" ]] || { echo "DISABLED"; return 0; }
  [[ "$samples" =~ ^[0-9]+$ ]] || { echo "UNAVAILABLE"; return 0; }
  (( samples > 0 )) || { echo "UNAVAILABLE"; return 0; }
  [[ "$expected" =~ ^[0-9]+$ ]] || { echo "PARTIAL"; return 0; }
  if (( expected > 0 && samples < expected )); then echo "PARTIAL"; else echo "AVAILABLE"; fi
}
