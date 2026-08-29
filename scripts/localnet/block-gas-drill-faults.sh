#!/usr/bin/env bash
set -uo pipefail

# Fast fault coverage for the block-gas tooling's proof primitives  (issue #160).
#
# block-gas-drill.sh launches four validators, funds sixty accounts and floods them,
# and load-calibration.sh runs longer still. Neither belongs in ordinary CI. But
# their primitives are exactly the things whose silent failure turns a broken run
# green, so those run on every push.
#
# Everything here is driven from STUBS: canned RPC payloads, synthetic genesis
# documents, pinned address vectors. No chain is started and nothing is downloaded.
# What it proves is narrow and specific — that each reader REFUSES when it cannot
# see, rather than returning a value it invented.
#
# The distinction matters because every failure mode below has a plausible
# presentation as success:
#
#   - a window of unreadable blocks contains no block over the ceiling
#   - an empty block and an unreadable one both have "no failing transactions"
#   - `max_gas` of 0 is not -1, and is also not a working ceiling
#   - a wrong bech32 checksum is a well-formed address string
#   - an assertion contract that is internally inconsistent can never be satisfied,
#     and the drill that carries it can never pass — which looks like a chain bug

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

# Source the real drill for its constants and helpers. A second, more permissive
# copy written in test shell would prove nothing about the code that runs.
BLOCK_GAS_DRILL_SOURCE_ONLY=1
# shellcheck source=/dev/null
. "$ROOT/scripts/localnet/block-gas-drill.sh"
set +e

GROUP_KEYS=(); GROUP_COUNTS=(); GROUP_IDX=-1
PASSED=0; FAILED=0
group() { GROUP_IDX=$((GROUP_IDX+1)); GROUP_KEYS[$GROUP_IDX]="$1"; GROUP_COUNTS[$GROUP_IDX]=0; echo; echo "=== $2 ==="; }
check() {
  GROUP_COUNTS[$GROUP_IDX]=$(( GROUP_COUNTS[GROUP_IDX] + 1 ))
  if [[ "$2" == "$3" ]]; then printf '  ok    %-54s %s\n' "$1" "$2"; PASSED=$((PASSED+1))
  else printf '  FAIL  %-54s expected=%s actual=%s\n' "$1" "$2" "$3" >&2; FAILED=$((FAILED+1)); fi
}
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT

# The transport every reader goes through, replaced by a fixture so the PARSE is
# what is under test.
FIXTURE=""
blockgas_get() { [[ -n "$FIXTURE" ]] || return 1; printf '%s' "$FIXTURE"; }
rc_of() { [[ $1 -ne 0 ]] && echo nonzero || echo ZERO; }

# ---------------------------------------------------------------------------------
group gasreaders "per-block gas: an empty block and an unreadable one are different"

FIXTURE='{"result":{"height":"42","txs_results":[{"gas_wanted":"200000","gas_used":"78000"},{"code":11,"gas_wanted":"200000","gas_used":"200000"}]}}'
check "gas_wanted sums"                          "400000" "$(block_gas http://x 42 gas_wanted)"
check "gas_used sums"                            "278000" "$(block_gas http://x 42 gas_used)"
check "delivered codes are visible"              "0,11"   "$(block_tx_codes http://x 42 | paste -sd, -)"

# The one that matters. A block with no transactions has a gas total of zero and no
# failing transactions; an unreadable block has neither. Collapsing them makes an
# unreadable measurement window look like a perfectly behaved one.
FIXTURE='{"result":{"height":"42","txs_results":null}}'
check "an empty block totals zero"               "0"  "$(block_gas http://x 42 gas_wanted)"
block_gas http://x 42 gas_wanted >/dev/null 2>&1
check "  and succeeds"                           "0"  "$?"
EMPTY_CODES="$(block_tx_codes http://x 42)"
check "an empty block has no codes"              "0"  "$?"
check "  and the code list is empty"             ""   "$EMPTY_CODES"

FIXTURE=''
block_gas http://x 42 gas_wanted >/dev/null 2>&1
check "an unreachable node refuses"              "nonzero" "$(rc_of $?)"
block_tx_codes http://x 42 >/dev/null 2>&1
check "  and its code list refuses too"          "nonzero" "$(rc_of $?)"

FIXTURE='not json at all'
block_gas http://x 42 gas_wanted >/dev/null 2>&1
check "a non-JSON body refuses"                  "nonzero" "$(rc_of $?)"

# A node that clamps an out-of-range height to its head answers with a REAL block
# that is not the one under measurement.
FIXTURE='{"result":{"height":"99","txs_results":[]}}'
block_gas http://x 42 gas_wanted >/dev/null 2>&1
check "a clamped height refuses"                 "nonzero" "$(rc_of $?)"

FIXTURE='{"result":{"height":"42"}}'
block_gas http://x 42 gas_wanted >/dev/null 2>&1
check "an absent txs_results refuses"            "nonzero" "$(rc_of $?)"

FIXTURE='{"result":{"height":"42","txs_results":[{"gas_wanted":"lots","gas_used":"1"}]}}'
block_gas http://x 42 gas_wanted >/dev/null 2>&1
check "non-numeric gas refuses"                  "nonzero" "$(rc_of $?)"

FIXTURE='{"result":{"height":"42","txs_results":[{"gas_used":"1"}]}}'
block_gas http://x 42 gas_wanted >/dev/null 2>&1
check "a missing gas field refuses"              "nonzero" "$(rc_of $?)"

block_gas http://x 42 gas_burnt >/dev/null 2>&1
check "an unknown field name refuses"            "nonzero" "$(rc_of $?)"
block_gas http://x notaheight gas_wanted >/dev/null 2>&1
check "a non-numeric height refuses"             "nonzero" "$(rc_of $?)"

# ---------------------------------------------------------------------------------
group ceiling "the TW-004 predicate: not-minus-one is not the same as bounded"

check "the shipped default is unbounded"         "no"  "$(max_gas_is_finite -1)"
check "a real ceiling is bounded"                "yes" "$(max_gas_is_finite 2000000)"
# The mutation a naive check misses entirely: zero is not -1, and it is also not a
# ceiling that admits any transaction at all.
check "zero is NOT a working ceiling"            "no"  "$(max_gas_is_finite 0)"
check "and neither is another negative"          "no"  "$(max_gas_is_finite -7)"
check "a naive != -1 test would accept zero"     "accepted" \
  "$([[ "0" != "-1" ]] && echo accepted || echo rejected)"
check "  while the predicate rejects it"         "no"  "$(max_gas_is_finite 0)"
max_gas_is_finite abc >/dev/null 2>&1
check "unreadable input refuses"                 "nonzero" "$(rc_of $?)"
check "  and says so rather than answering"      "unreadable" "$(max_gas_is_finite abc 2>/dev/null)"
max_gas_is_finite "" >/dev/null 2>&1
check "empty input refuses"                      "nonzero" "$(rc_of $?)"

# ---------------------------------------------------------------------------------
group meta "block metadata is pinned to exactly one height"

FIXTURE='{"result":{"block_metas":[{"block_size":"4210","num_txs":"10","header":{"height":"42","time":"2026-08-29T12:00:00.5Z"}}]}}'
check "block size"                               "4210" "$(block_meta http://x 42 block_size)"
check "transaction count"                        "10"   "$(block_meta http://x 42 num_txs)"
check "timestamp"                                "2026-08-29T12:00:00.5Z" "$(block_meta http://x 42 time)"

# /blockchain answers a RANGE. A widened or clamped answer must not be read as the
# requested block.
FIXTURE='{"result":{"block_metas":[{"block_size":"1","num_txs":"1","header":{"height":"43","time":"2026-08-29T12:00:00Z"}},{"block_size":"2","num_txs":"2","header":{"height":"42","time":"2026-08-29T12:00:00Z"}}]}}'
block_meta http://x 42 num_txs >/dev/null 2>&1
check "a widened range refuses"                  "nonzero" "$(rc_of $?)"
FIXTURE='{"result":{"block_metas":[{"block_size":"1","num_txs":"1","header":{"height":"43","time":"2026-08-29T12:00:00Z"}}]}}'
block_meta http://x 42 num_txs >/dev/null 2>&1
check "the wrong single height refuses"          "nonzero" "$(rc_of $?)"
FIXTURE='{"result":{"block_metas":[]}}'
block_meta http://x 42 num_txs >/dev/null 2>&1
check "an empty range refuses"                   "nonzero" "$(rc_of $?)"
FIXTURE='{"result":{"block_metas":[{"header":{"height":"42","time":"2026-08-29T12:00:00Z"}}]}}'
block_meta http://x 42 num_txs >/dev/null 2>&1
check "a missing field refuses"                  "nonzero" "$(rc_of $?)"

# ---------------------------------------------------------------------------------
group params "the live ceiling is read strictly"

FIXTURE='{"result":{"consensus_params":{"block":{"max_bytes":"22020096","max_gas":"2000000"}}}}'
check "a finite ceiling parses"                  "2000000" "$(live_max_gas http://x)"
FIXTURE='{"result":{"consensus_params":{"block":{"max_bytes":"22020096","max_gas":"-1"}}}}'
check "the unlimited sentinel parses"            "-1"      "$(live_max_gas http://x)"
check "  and is reported as unbounded"           "no"      "$(max_gas_is_finite "$(live_max_gas http://x)")"
FIXTURE='{"result":{"consensus_params":{"block":{"max_bytes":"22020096"}}}}'
live_max_gas http://x >/dev/null 2>&1
check "an absent max_gas refuses"                "nonzero" "$(rc_of $?)"
FIXTURE='{"result":{}}'
live_max_gas http://x >/dev/null 2>&1
check "absent params refuse"                     "nonzero" "$(rc_of $?)"

# ---------------------------------------------------------------------------------
group deferral "excess in later blocks is arithmetic, not a claim about timing"

# The drill's proof: N transactions, all included, at no more than CAP per block,
# cannot occupy fewer than ceil(N/CAP) blocks. That holds however fast the burst was
# submitted, which is why the drill does not have to assert anything about timing.
span_min() { echo $(( ($1 + $2 - 1) / $2 )); }
check "60 at 10 per block need 6"                "6" "$(span_min 60 10)"
check "61 at 10 per block need 7"                "7" "$(span_min 61 10)"
check "10 at 10 per block need 1"                "1" "$(span_min 10 10)"
# The failure the assertion exists to catch: everything landed in one block, which
# means the ceiling did not bind at all.
check "one block fails the 6-block bound"        "no" \
  "$([[ 1 -ge 6 ]] && echo yes || echo no)"
check "six blocks satisfy it"                    "yes" \
  "$([[ 6 -ge 6 ]] && echo yes || echo no)"
# And the drill's own constants have to agree with the arithmetic, or the bound it
# asserts is not the bound its flood implies.
check "the drill's span bound is ceil(N/CAP)"    "$(span_min "$FLOOD_SENDERS" "$PER_BLOCK_CAP")" "$MIN_SPAN_BLOCKS"

# ---------------------------------------------------------------------------------
group cardinality "an unreadable window is not a well-behaved one"

# Counting blocks over the ceiling, across a window nothing could be read from,
# yields zero. That is why the drill pairs the count with the number of blocks it
# actually read, and requires it to equal the window.
FIXTURE=''
OVER=0; READ=0; WINDOW=5
for h in 10 11 12 13 14; do
  gw="$(block_gas http://x "$h" gas_wanted)" || continue
  READ=$(( READ + 1 )); (( gw > 2000000 )) && OVER=$(( OVER + 1 ))
done
check "nothing exceeded the ceiling"             "0" "$OVER"
check "  because nothing was read"               "0" "$READ"
check "and the pairing catches it"               "different" \
  "$([[ "$READ" != "$WINDOW" ]] && echo different || echo SAME)"

# The same window, readable, with one block genuinely over.
OVER=0; READ=0
for h in 10 11 12 13 14; do
  if (( h == 12 )); then FIXTURE='{"result":{"height":"'"$h"'","txs_results":[{"gas_wanted":"2200000","gas_used":"10"}]}}'
  else FIXTURE='{"result":{"height":"'"$h"'","txs_results":[{"gas_wanted":"200000","gas_used":"10"}]}}'; fi
  gw="$(block_gas http://x "$h" gas_wanted)" || continue
  READ=$(( READ + 1 )); (( gw > 2000000 )) && OVER=$(( OVER + 1 ))
done
check "a readable window is fully read"          "5" "$READ"
check "  and the over-ceiling block is seen"     "1" "$OVER"

# ---------------------------------------------------------------------------------
group time "block timestamps convert without depending on the host's date"

check "the epoch"                                "0"             "$(rfc3339_to_ms 1970-01-01T00:00:00Z)"
check "a nanosecond timestamp"                   "1788006896789" "$(rfc3339_to_ms 2026-08-29T12:34:56.789012345Z)"
# A leap day and a century non-leap year: the naive every-four-years rule is wrong
# on the second, and the error is a silent one-day shift in every block delta.
check "a leap day"                               "951782400000"  "$(rfc3339_to_ms 2000-02-29T00:00:00Z)"
check "past a non-leap century"                  "4107542400000" "$(rfc3339_to_ms 2100-03-01T00:00:00Z)"
# ".5" is 500 milliseconds, not 5.
check "a single fractional digit"                "1767225600500" "$(rfc3339_to_ms 2026-01-01T00:00:00.5Z)"
rfc3339_to_ms "not a time" >/dev/null 2>&1
check "unparseable input refuses"                "nonzero" "$(rc_of $?)"
rfc3339_to_ms "2026-13-01T00:00:00Z" >/dev/null 2>&1
check "an impossible month refuses"              "nonzero" "$(rc_of $?)"
rfc3339_to_ms "2026-08-29T12:34:56+02:00" >/dev/null 2>&1
check "a non-UTC offset refuses"                 "nonzero" "$(rc_of $?)"

# ---------------------------------------------------------------------------------
group address "recipient addresses are checked against pinned vectors"

# The calibration rig mints fresh recipients in-process, at flood rate, rather than
# spawning the binary once per address. A wrong encoder would produce well-formed
# addresses the chain refuses, and the run would then be a measurement of its own
# rejection rate. These vectors are the chain's own codec output.
bech32_init twilight
declare -a VEC_HEX=(
  0102030405060708090a0b0c0d0e0f1011121314
  0000000000000000000000000000000000000000
  ffffffffffffffffffffffffffffffffffffffff
  deadbeef00112233445566778899aabbccddeeff
)
declare -a VEC_ADDR=(
  twilight1qypqxpq9qcrsszg2pvxq6rs0zqg3yyc5yayhe2
  twilight1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqgugkct
  twilight1llllllllllllllllllllllllllllllllydfelp
  twilight1m6kmamcqzy3rx3z4vemc3xd2h0xdmmhl9dvl86
)
for v in 0 1 2 3; do
  bech32_encode_into GOT "${VEC_HEX[$v]}"
  check "vector $v"                              "${VEC_ADDR[$v]}" "$GOT"
done
# Distinct payloads must give distinct addresses, or the account-growth column in a
# calibration run would flatten without anything looking wrong.
bech32_encode_into A "0000000000000000000000000000000000000001"
bech32_encode_into B "0000000000000000000000000000000000000002"
check "distinct payloads differ"                 "different" \
  "$([[ "$A" != "$B" ]] && echo different || echo SAME)"
bech32_encode_into ODD "01020" >/dev/null 2>&1
check "an odd-length payload refuses"            "nonzero" "$(rc_of $?)"
bech32_encode_into NH "zzzz" >/dev/null 2>&1
check "a non-hex payload refuses"                "nonzero" "$(rc_of $?)"

# ---------------------------------------------------------------------------------
group genesis "the genesis patch refuses a document it does not recognise"

# The drill patches consensus.params.block.max_gas. A blind jq assignment would
# CREATE that path wherever it was pointed, and the drill would then assert
# confidently about a field the node never reads — so the path is required to exist
# before it is written.
node_home() { echo "$TMP/$1"; }
mkdir -p "$TMP/0/config" "$TMP/1/config" "$TMP/2/config"
printf '{"consensus":{"params":{"block":{"max_bytes":"22020096","max_gas":"-1"}}}}\n' >"$TMP/0/config/genesis.json"
check "the shipped default is readable"          "-1"       "$(genesis_block_field 0 max_gas)"
check "max_bytes is readable"                    "22020096" "$(genesis_block_field 0 max_bytes)"
# The shape a future SDK might ship, where the block params moved.
printf '{"consensus_params":{"block":{"max_gas":"-1"}}}\n' >"$TMP/1/config/genesis.json"
genesis_block_field 1 max_gas >/dev/null 2>&1
check "a relocated path refuses"                 "nonzero" "$(rc_of $?)"
printf '{"consensus":{"params":{"block":{"max_bytes":"22020096"}}}}\n' >"$TMP/2/config/genesis.json"
genesis_block_field 2 max_gas >/dev/null 2>&1
check "a missing key refuses"                    "nonzero" "$(rc_of $?)"
genesis_block_field 9 max_gas >/dev/null 2>&1
check "an absent document refuses"               "nonzero" "$(rc_of $?)"

# ---------------------------------------------------------------------------------
group contract "the drill's pinned proof contract is internally consistent"

# A contract that cannot be satisfied makes the drill permanently red, and the red
# looks like a chain defect rather than an editing mistake. These are the two ways
# to break it while the file still reads correctly.
check "the per-block cap follows from the ceiling" "$(( BLOCK_GAS_MAX / FLOOD_TX_GAS ))" "$PER_BLOCK_CAP"
check "the installed ceiling is itself finite"     "yes" "$(max_gas_is_finite "$BLOCK_GAS_MAX")"
MS_TOTAL="$(tr ',' '\n' <<<"$DRILL_EXPECTED_MULTISET" | awk -F: '{ s += $NF } END { print s + 0 }')"
check "the multiset totals the assertion count"    "$DRILL_EXPECTED_ASSERTIONS" "$MS_TOTAL"
# The multiset is compared against output that has been through `LC_ALL=C sort`. A
# hand-edited constant in any other order can never match, whatever the run did.
MS_SORTED="$(tr ',' '\n' <<<"$DRILL_EXPECTED_MULTISET" | LC_ALL=C sort | paste -sd, -)"
check "the multiset is in canonical order"         "same" \
  "$([[ "$MS_SORTED" == "$DRILL_EXPECTED_MULTISET" ]] && echo same || echo REORDERED)"
check "every multiset key names a node"            "0" \
  "$(tr ',' '\n' <<<"$DRILL_EXPECTED_MULTISET" | grep -cv '|')"

# ---------------------------------------------------------------------------------
group verdict "a failing run still writes a machine-readable verdict"

# finalize_verdict is TERMINAL: it writes the verdict and exits. Each case therefore
# runs in a subshell, or the first would take this suite down with it.
EV="$TMP/evid1"; drill_assert_init "$EV" >/dev/null 2>&1
expect "deliberately_failing" "a" "b" >/dev/null 2>&1
# Separate statements, not a command prefix: `ARRAY=() cmd` is not a valid array
# assignment in bash and would silently leave the outer scope's values in place.
( DRILL_MANDATORY_FILES=(); DRILL_VERDICT_GATES=(); DRILL_EXPECTED_PHASES=0
  DRILL_EXPECTED_ASSERTIONS=0; DRILL_EXPECTED_MULTISET=""; finalize_verdict >/dev/null 2>&1 )
check "a failed assertion exits non-zero"        "1" "$?"
check "and the verdict file records it"          "overall=FAIL" "$(grep '^overall=' "$EV/verdict.txt")"

# A component gate whose value was never derived is the specific hazard here: the
# drill's `liveness=` line is computed from observations precisely because a literal
# would satisfy its own gate on a run where nothing was observed.
EV2="$TMP/evid2"; drill_assert_init "$EV2" >/dev/null 2>&1
expect "passing_assertion" "a" "a" >/dev/null 2>&1
( DRILL_MANDATORY_FILES=(); DRILL_EXPECTED_PHASES=0; DRILL_EXPECTED_ASSERTIONS=0
  DRILL_EXPECTED_MULTISET=""; DRILL_VERDICT_GATES=("liveness=SUSTAINED")
  DRILL_VERDICT_LINES=("liveness=INTERRUPTED"); finalize_verdict >/dev/null 2>&1 )
check "an out-of-set component fails"            "1" "$?"

EV3="$TMP/evid3"; drill_assert_init "$EV3" >/dev/null 2>&1
expect "passing_assertion" "a" "a" >/dev/null 2>&1
( DRILL_MANDATORY_FILES=(); DRILL_EXPECTED_PHASES=0; DRILL_EXPECTED_ASSERTIONS=0
  DRILL_EXPECTED_MULTISET=""; DRILL_VERDICT_GATES=("liveness=SUSTAINED"); DRILL_VERDICT_LINES=()
  finalize_verdict >/dev/null 2>&1 )
check "an unreported component fails"            "1" "$?"

# Mandatory evidence that was never written must fail even when every assertion
# passed: a run that ended early otherwise tells the truth about the checks it
# reached and nothing about the ones it did not.
EV4="$TMP/evid4"; drill_assert_init "$EV4" >/dev/null 2>&1
expect "passing_assertion" "a" "a" >/dev/null 2>&1
( DRILL_MANDATORY_FILES=(blocks.csv); DRILL_VERDICT_GATES=(); DRILL_EXPECTED_PHASES=0
  DRILL_EXPECTED_ASSERTIONS=0; DRILL_EXPECTED_MULTISET=""; finalize_verdict >/dev/null 2>&1 )
check "missing mandatory evidence fails"         "1" "$?"

# And the clean path still passes, so the suite is not simply asserting doom.
EV5="$TMP/evid5"; drill_assert_init "$EV5" >/dev/null 2>&1
expect "passing_assertion" "a" "a" >/dev/null 2>&1
( DRILL_MANDATORY_FILES=(); DRILL_EXPECTED_PHASES=0; DRILL_EXPECTED_ASSERTIONS=0
  DRILL_EXPECTED_MULTISET=""; DRILL_VERDICT_GATES=("liveness=SUSTAINED")
  DRILL_VERDICT_LINES=("liveness=SUSTAINED"); FAILURES=0; finalize_verdict >/dev/null 2>&1 )
check "a clean run passes"                       "0" "$?"
check "  and records PASS"                       "overall=PASS" "$(grep '^overall=' "$EV5/verdict.txt")"

# ---------------------------------------------------------------------------------
# The per-group and total contracts. Declared deliberately: a floor lets checks
# vanish silently, and fitting these numbers to whatever the run produced would make
# them decoration.
EXPECTED_GASREADERS=16
EXPECTED_CEILING=9
EXPECTED_META=7
EXPECTED_PARAMS=5
EXPECTED_DEFERRAL=6
EXPECTED_CARDINALITY=5
EXPECTED_TIME=8
EXPECTED_ADDRESS=7
EXPECTED_GENESIS=5
EXPECTED_CONTRACT=5
EXPECTED_VERDICT=7

PER_GROUP_EXPECTED=("$EXPECTED_GASREADERS" "$EXPECTED_CEILING" "$EXPECTED_META" "$EXPECTED_PARAMS" \
                    "$EXPECTED_DEFERRAL" "$EXPECTED_CARDINALITY" "$EXPECTED_TIME" "$EXPECTED_ADDRESS" \
                    "$EXPECTED_GENESIS" "$EXPECTED_CONTRACT" "$EXPECTED_VERDICT")
EXPECTED_CHECKS=0
for e in "${PER_GROUP_EXPECTED[@]}"; do EXPECTED_CHECKS=$(( EXPECTED_CHECKS + e )); done
TOTAL=$(( PASSED + FAILED ))

echo
for i in $(seq 0 $GROUP_IDX); do
  if (( GROUP_COUNTS[i] != PER_GROUP_EXPECTED[i] )); then
    echo "block-gas faults: FAIL — group ${GROUP_KEYS[$i]} ran ${GROUP_COUNTS[$i]}, the contract is ${PER_GROUP_EXPECTED[$i]}" >&2
    exit 1
  fi
done
if (( GROUP_IDX + 1 != ${#PER_GROUP_EXPECTED[@]} )); then
  echo "block-gas faults: FAIL — $((GROUP_IDX + 1)) groups ran, the contract names ${#PER_GROUP_EXPECTED[@]}" >&2
  exit 1
fi
if (( TOTAL != EXPECTED_CHECKS )); then
  echo "block-gas faults: FAIL — $TOTAL checks ran, the contract is $EXPECTED_CHECKS" >&2
  echo "  (reconcile the contract deliberately; do not fit it to the run)" >&2
  exit 1
fi
if (( FAILED > 0 )); then
  echo "block-gas faults: FAIL ($FAILED of $TOTAL)" >&2; exit 1
fi
echo "block-gas faults: PASS ($PASSED checks)"
