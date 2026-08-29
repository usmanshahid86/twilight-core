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
