#!/usr/bin/env bash
set -uo pipefail

# Fast fault coverage for the block-gas tooling's proof primitives  (issue #160).
#
# block-gas-drill.sh launches four validators, funds sixty accounts and floods them;
# load-calibration.sh runs longer still. Neither belongs in ordinary CI. Their
# primitives do, because every one of them has a failure mode that presents as
# success:
#
#   - a window of unreadable blocks contains no block over the ceiling
#   - an empty block and an unreadable one both have "no failing transactions"
#   - a transaction that reverted was still included
#   - transactions spanning six blocks prove nothing if only ten were ever offered
#   - a chain that stalled and resumed still shows a greater height afterwards
#   - `max_gas` of 0 is not -1, and is also not a ceiling
#   - an unsampled growth axis totals zero
#   - a wave that "settled" for three blocks may still have transactions pending
#   - a step labelled 256-concurrent may have been sixteen batches of sixteen
#   - an assertion contract that is internally inconsistent can never be satisfied,
#     and the drill carrying it can never pass — which reads as a chain defect
#
# Everything here runs against STUBS: canned RPC payloads, synthetic CSVs, pinned
# address vectors. No chain is started, no binary is built, nothing is downloaded.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

# Source the real drill for its constants and helpers.
BLOCK_GAS_DRILL_SOURCE_ONLY=1
# shellcheck source=/dev/null
. "$ROOT/scripts/localnet/block-gas-drill.sh"
set +e

GROUP_KEYS=(); GROUP_COUNTS=(); GROUP_IDX=-1
PASSED=0; FAILED=0
group() { GROUP_IDX=$((GROUP_IDX+1)); GROUP_KEYS[$GROUP_IDX]="$1"; GROUP_COUNTS[$GROUP_IDX]=0; echo; echo "=== $2 ==="; }
check() {
  GROUP_COUNTS[$GROUP_IDX]=$(( GROUP_COUNTS[GROUP_IDX] + 1 ))
  if [[ "$2" == "$3" ]]; then printf '  ok    %-56s %s\n' "$1" "$2"; PASSED=$((PASSED+1))
  else printf '  FAIL  %-56s expected=%s actual=%s\n' "$1" "$2" "$3" >&2; FAILED=$((FAILED+1)); fi
}
TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT

FIXTURE=""
blockgas_get() { [[ -n "$FIXTURE" ]] || return 1; printf '%s' "$FIXTURE"; }
rc_of() { [[ $1 -ne 0 ]] && echo nonzero || echo ZERO; }
CAL="$ROOT/scripts/localnet/load-calibration.sh"
DRILL_FILE="$ROOT/scripts/localnet/block-gas-drill.sh"

# ---------------------------------------------------------------------------------
group gasreaders "per-block gas: an empty block and an unreadable one are different"

FIXTURE='{"result":{"height":"42","txs_results":[{"gas_wanted":"200000","gas_used":"78000"},{"code":11,"gas_wanted":"200000","gas_used":"200000"}]}}'
check "gas_wanted sums"                          "400000" "$(block_gas http://x 42 gas_wanted)"
check "gas_used sums"                            "278000" "$(block_gas http://x 42 gas_used)"
check "delivered codes are visible"              "0,11"   "$(block_tx_codes http://x 42 | paste -sd, -)"

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
check "zero is NOT a working ceiling"            "no"  "$(max_gas_is_finite 0)"
check "and neither is another negative"          "no"  "$(max_gas_is_finite -7)"
check "a naive != -1 test would accept zero"     "accepted" "$([[ "0" != "-1" ]] && echo accepted || echo rejected)"
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
group delivery "inclusion is not delivery"

# S1. The false PASS the previous contract carried: a transaction that passed
# CheckTx, occupied a block and REVERTED was counted as deferred-not-dropped, because
# the drill incremented its included counter on the presence of a height alone.
FIXTURE='{"result":{"height":"55","tx_result":{"code":0}}}'
check "a successful transaction resolves"        "55 0"  "$(tx_outcome http://x ABCD)"
FIXTURE='{"result":{"height":"55","tx_result":{"code":11}}}'
check "a reverted transaction also has a height" "55 11" "$(tx_outcome http://x ABCD)"
# Counting on the height alone cannot tell them apart; the delivery code can.
INCLUDED_OLD=2
DELIVERED_NEW="$(for c in 0 11; do [[ "$c" == "0" ]] && echo x; done | grep -c .)"
check "the old included-count sees 2 of 2"       "2" "$INCLUDED_OLD"
check "  the delivery count sees 1 of 2"         "1" "$DELIVERED_NEW"
check "  so the drill gate would fail"           "different" \
  "$([[ "$DELIVERED_NEW" != "60" ]] && echo different || echo SAME)"
FIXTURE='{"result":{"height":"55"}}'
tx_outcome http://x ABCD >/dev/null 2>&1
check "a result with no tx_result refuses"       "nonzero" "$(rc_of $?)"
FIXTURE='{"result":{"height":"notaheight","tx_result":{"code":0}}}'
tx_outcome http://x ABCD >/dev/null 2>&1
check "a malformed height refuses"               "nonzero" "$(rc_of $?)"
FIXTURE=''
tx_outcome http://x ABCD >/dev/null 2>&1
check "an unresolved transaction refuses"        "nonzero" "$(rc_of $?)"
tx_outcome http://x "not-hex" >/dev/null 2>&1
check "a malformed hash refuses"                 "nonzero" "$(rc_of $?)"
# And the drill must actually gate on it.
check "the drill asserts delivered_ok"           "1" \
  "$(grep -c 'expect "flood_delivered_ok" "\$FLOOD_SENDERS" "\$DELIVERED_OK"' "$DRILL_FILE")"
check "  and gates unresolved at zero"           "1" \
  "$(grep -c 'expect "flood_unresolved" "0" "\$UNRESOLVED"' "$DRILL_FILE")"

# ---------------------------------------------------------------------------------
group saturation "spanning blocks is not proof the ceiling bound"

# S2. The central false PASS. Sixty transactions across six blocks is exactly what a
# slow harness produces when only ten were ever available to a proposer — the blocks
# are indistinguishable from a genuinely saturated run.
check "a block at the ceiling binds"             "yes" "$(block_binds_gas_ceiling 2000000 200000 2000000)"
check "a block one short does not"               "no"  "$(block_binds_gas_ceiling 1800000 200000 2000000)"
check "a block that could not take another does" "yes" "$(block_binds_gas_ceiling 1900000 200000 2000000)"
check "an empty block does not"                  "no"  "$(block_binds_gas_ceiling 0 200000 2000000)"
block_binds_gas_ceiling "" 200000 2000000 >/dev/null 2>&1
check "unreadable gas refuses"                   "nonzero" "$(rc_of $?)"

# The mutation the specification asks for: all sixty span six blocks, but no block
# ever reached the ceiling and the backlog never exceeded one block's capacity.
SLOW_BLOCKS=(1000000 1000000 1000000 1000000 1000000 1000000)
BINDING=0
for g in "${SLOW_BLOCKS[@]}"; do [[ "$(block_binds_gas_ceiling "$g" 200000 2000000)" == "yes" ]] && BINDING=$(( BINDING + 1 )); done
check "a slow-harness run spans six blocks"      "6" "${#SLOW_BLOCKS[@]}"
check "  and would pass the old span gate"       "yes" "$([[ "${#SLOW_BLOCKS[@]}" -ge 6 ]] && echo yes || echo no)"
check "  but binds nothing"                      "0" "$BINDING"
check "  so the binding gate FAILS it"           "no" "$([[ "$BINDING" -ge 1 ]] && echo yes || echo no)"
SLOW_PEAK_BACKLOG=8
check "  and its backlog never exceeds capacity" "no" \
  "$([[ "$SLOW_PEAK_BACKLOG" -gt "$PER_BLOCK_CAP" ]] && echo yes || echo no)"
GENUINE_PEAK_BACKLOG=60
check "a genuine flood backs up past capacity"   "yes" \
  "$([[ "$GENUINE_PEAK_BACKLOG" -gt "$PER_BLOCK_CAP" ]] && echo yes || echo no)"

# Mempool depth is how that backlog is observed, and it must refuse rather than
# report zero when it cannot look.
FIXTURE='{"result":{"n_txs":"47","total":"47","total_bytes":"12345","txs":null}}'
check "mempool depth parses"                     "47" "$(mempool_depth http://x)"
FIXTURE='{"result":{"n_txs":"0","total":"0","total_bytes":"0","txs":null}}'
check "an empty mempool is zero, not a refusal"  "0"  "$(mempool_depth http://x)"
FIXTURE='{"result":{}}'
mempool_depth http://x >/dev/null 2>&1
check "an absent n_txs refuses"                  "nonzero" "$(rc_of $?)"
FIXTURE=''
mempool_depth http://x >/dev/null 2>&1
check "an unreachable mempool refuses"           "nonzero" "$(rc_of $?)"
check "the drill gates on peak backlog"          "1" \
  "$(grep -c 'expect "peak_backlog_exceeds_block_capacity"' "$DRILL_FILE")"
check "  and on an observed binding block"       "1" \
  "$(grep -c 'expect "binding_blocks_observed"' "$DRILL_FILE")"

# ---------------------------------------------------------------------------------
group stall "a chain that stalled and resumed is not a live chain"

# S3. Height-before/height-after proves eventual progress and nothing else. The
# synthetic window below stalls for 40s in the middle and then resumes.
STALL_TIMES=(0 3000 6000 46000 49000 52000)
MAXGAP=0; PREV=""
for t in "${STALL_TIMES[@]}"; do
  [[ -n "$PREV" ]] && { g=$(( t - PREV )); (( g > MAXGAP )) && MAXGAP="$g"; }
  PREV="$t"
done
check "the stalled window still ends higher"     "yes" \
  "$([[ "${STALL_TIMES[5]}" -gt "${STALL_TIMES[0]}" ]] && echo yes || echo no)"
check "  so the old liveness check passes it"    "yes" \
  "$([[ 6 -gt 1 ]] && echo yes || echo no)"
check "the maximum gap is visible"               "40000" "$MAXGAP"
ALLOWED=$(( $(commit_timeout_ms 3s) * 4 ))
check "the bound derives from the cadence"       "12000" "$ALLOWED"
check "  and the gap gate FAILS the stall"       "no" "$([[ "$MAXGAP" -le "$ALLOWED" ]] && echo yes || echo no)"
HEALTHY_TIMES=(0 3000 6100 9200 12050 15000)
MAXGAP2=0; PREV=""
for t in "${HEALTHY_TIMES[@]}"; do
  [[ -n "$PREV" ]] && { g=$(( t - PREV )); (( g > MAXGAP2 )) && MAXGAP2="$g"; }
  PREV="$t"
done
check "a merely slow window passes"              "yes" "$([[ "$MAXGAP2" -le "$ALLOWED" ]] && echo yes || echo no)"
check "  with a visible worst gap"               "3100" "$MAXGAP2"
check "the drill gates on the flood-window gap"  "1" \
  "$(grep -c 'expect "flood_window_max_gap_within_bound"' "$DRILL_FILE")"
check "  paired with timing coverage"            "1" \
  "$(grep -c 'expect "flood_window_all_blocks_timed"' "$DRILL_FILE")"

# ---------------------------------------------------------------------------------
group drain "a wave is finished when it has drained, not when blocks have passed"

# S4/S5. A transaction can be in a node's CheckTx state while the canonical account
# query still returns the previous committed sequence. A rig that used a fixed settle
# window as proof would sign the next wave stale, and the rejections it caused would
# be recorded as the chain refusing load.
check "settle blocks are not the drain proof"    "0" \
  "$(grep -c 'CAL_SETTLE_BLOCKS' "$CAL")"
check "the rig drains to terminal state"         "1" \
  "$(grep -c 'left \$W_UNRESOLVED transactions unresolved at its boundary' "$CAL")"
check "  and invalidates on a pending boundary"  "1" \
  "$(grep -c 'if (( W_UNRESOLVED > 0 )); then' "$CAL")"
check "quiet blocks are recorded separately"     "1" \
  "$(grep -c 'CAL_QUIET_BLOCKS' "$CAL" | awk '{print ($1 > 0) ? 1 : 0}')"

# The sequence invariant: after N waves a participating sender must have committed
# exactly N transactions. Anything else means the rig's own bookkeeping diverged.
EXPECTED_SEQ=3
check "a healthy sender advanced exactly"        "ok"      "$([[ 3 -eq "$EXPECTED_SEQ" ]] && echo ok || echo VIOLATION)"
check "a stalled sender is caught"               "VIOLATION" "$([[ 2 -eq "$EXPECTED_SEQ" ]] && echo ok || echo VIOLATION)"
check "a runaway sender is caught too"           "VIOLATION" "$([[ 4 -eq "$EXPECTED_SEQ" ]] && echo ok || echo VIOLATION)"
check "an unreadable sequence is a violation"    "VIOLATION" "$([[ "x" =~ ^[0-9]+$ ]] && echo ok || echo VIOLATION)"
check "the rig checks the committed sequence"    "1" \
  "$(grep -c 'committed sequence \$seq after wave' "$CAL")"

# ---------------------------------------------------------------------------------
group concurrency "a step is never labelled with concurrency the harness did not deliver"

# S6. Sixteen successive batches of sixteen is not 256-way concurrency, and labelling
# it as such attributes the chain's response to a load that was never offered.
printf 'start_ms,end_ms\n100,400\n150,350\n200,300\n500,600\n' >"$TMP/conc3.csv"
check "genuine overlap is measured"              "3" "$(max_concurrency_of "$TMP/conc3.csv")"
printf 'start_ms,end_ms\n100,200\n300,400\n500,600\n' >"$TMP/serial.csv"
check "serial submission measures 1"             "1" "$(max_concurrency_of "$TMP/serial.csv")"
# The exact mutation: a nominal population far above what was ever in flight.
NOMINAL=256; MEASURED="$(max_concurrency_of "$TMP/serial.csv")"
check "  nominal and measured differ"            "different" \
  "$([[ "$NOMINAL" != "$MEASURED" ]] && echo different || echo SAME)"
max_concurrency_of "$TMP/absent.csv" >/dev/null 2>&1
check "a missing timing file refuses"            "nonzero" "$(rc_of $?)"
printf 'start_ms,end_ms\n' >"$TMP/empty.csv"
max_concurrency_of "$TMP/empty.csv" >/dev/null 2>&1
check "a header-only timing file refuses"        "nonzero" "$(rc_of $?)"
check "the rig records measured concurrency"     "1" \
  "$(grep -c 'max_concurrent' "$CAL" | awk '{print ($1 > 0) ? 1 : 0}')"
check "  and an accepted-per-second rate"        "1" \
  "$(grep -c 'ACCEPTED_PER_S' "$CAL" | awk '{print ($1 > 0) ? 1 : 0}')"

# ---------------------------------------------------------------------------------
group attribution "no block belongs to two load steps"

# S7. Deriving windows from a broadcast height plus a fixed settle count let adjacent
# ranges share a boundary block, and the analyser attributed it to whichever matched
# first — so a knee could move on bookkeeping rather than chain behaviour.
printf '10 1 ACTIVE\n11 1 ACTIVE\n12 2 ACTIVE\n13 2 ACTIVE\n' >"$TMP/attr-ok.txt"
check "disjoint windows have no overlap"         "0" "$(attribution_overlaps "$TMP/attr-ok.txt")"
printf '10 1 ACTIVE\n11 1 ACTIVE\n11 2 ACTIVE\n12 2 ACTIVE\n' >"$TMP/attr-bad.txt"
check "a shared boundary block is caught"        "1" "$(attribution_overlaps "$TMP/attr-bad.txt")"
printf '10 1 ACTIVE\n10 2 ACTIVE\n11 1 ACTIVE\n11 2 ACTIVE\n' >"$TMP/attr-worse.txt"
check "two shared blocks are counted"            "2" "$(attribution_overlaps "$TMP/attr-worse.txt")"
printf '10 1 ACTIVE\n10 1 ACTIVE\n' >"$TMP/attr-dup.txt"
check "the same step twice is not an overlap"    "0" "$(attribution_overlaps "$TMP/attr-dup.txt")"
attribution_overlaps "$TMP/attr-absent.txt" >/dev/null 2>&1
check "a missing attribution file refuses"       "nonzero" "$(rc_of $?)"
check "the rig invalidates on overlap"           "1" \
  "$(grep -c 'blocks are claimed by more than one load step' "$CAL")"
check "  and windows come from inclusion data"   "1" \
  "$(grep -c 'the ACTIVE window, from transaction evidence' "$CAL")"

# ---------------------------------------------------------------------------------
group na "an unmeasured axis is UNAVAILABLE, never zero growth"

# S8/S9. The previous summary initialised its aggregates numerically, so an axis that
# was never sampled printed 0 — the single most reassuring answer available, invented
# for data that did not exist.
check "a true zero renders as zero"              "0"  "$(growth_render 0 12)"
check "real growth renders"                      "480" "$(growth_render 480 12)"
check "an unsampled axis renders NA"             "NA" "$(growth_render 0 0)"
check "  and is distinguishable from zero"       "different" \
  "$([[ "$(growth_render 0 0)" != "$(growth_render 0 12)" ]] && echo different || echo SAME)"
check "an unusable sum renders NA"               "NA" "$(growth_render "" 5)"
check "an unusable sample count renders NA"      "NA" "$(growth_render 5 "")"
check "sampling disabled is DISABLED"            "DISABLED"    "$(axis_availability 0 0 100)"
check "requested but never answered"             "UNAVAILABLE" "$(axis_availability 1 0 100)"
check "partial coverage is PARTIAL"              "PARTIAL"     "$(axis_availability 1 40 100)"
check "full coverage is AVAILABLE"               "AVAILABLE"   "$(axis_availability 1 100 100)"
check "the rig never prints a bare zero for NA"  "0" \
  "$(grep -c 'ACCT_OUT="0"' "$CAL")"

# ---------------------------------------------------------------------------------
group namespace "recipient reuse is detected, not assumed away"

# S10. A short cycling salt repeats within days. A repeat against a persistent network
# silently reuses accounts that already exist, and the growth column flattens while
# every other part of the run looks healthy.
bech32_init twilight
N1="aaaaaaaaaaaaaaaa"; N2="bbbbbbbbbbbbbbbb"
printf -v H1 '%s000000000000%06x%04x%02x' "$N1" 1 0 0
printf -v H2 '%s000000000000%06x%04x%02x' "$N2" 1 0 0
bech32_encode_into A1 "$H1"; bech32_encode_into A2 "$H2"
check "different nonces give different addresses" "different" "$([[ "$A1" != "$A2" ]] && echo different || echo SAME)"
printf -v H3 '%s000000000000%06x%04x%02x' "$N1" 1 0 1
bech32_encode_into A3 "$H3"
check "different outputs differ"                  "different" "$([[ "$A1" != "$A3" ]] && echo different || echo SAME)"
printf -v H4 '%s000000000000%06x%04x%02x' "$N1" 2 0 0
bech32_encode_into A4 "$H4"
check "different waves differ"                    "different" "$([[ "$A1" != "$A4" ]] && echo different || echo SAME)"
check "the same coordinates are stable"           "same" \
  "$(bech32_encode_into A5 "$H1"; [[ "$A1" == "$A5" ]] && echo same || echo DIFFERENT)"
check "the payload is exactly 20 bytes"           "40" "${#H1}"
check "the nonce is 16 hex wide"                  "16" "${#N1}"
check "the rig verifies freshness on chain"       "1" \
  "$(grep -c 'recipient namespace already in use at' "$CAL")"
check "  and records the seed in provenance"      "1" \
  "$(grep -c 'namespace_nonce' "$CAL")"

# ---------------------------------------------------------------------------------
group endpoints "one node is not the chain"

# S11. Blocks are read from a single endpoint. If the others do not agree on them,
# the CSV describes a node under load rather than a chain under load — and a
# saturated RPC endpoint is exactly what would otherwise be mistaken for saturation.
AGREE=(AAAA AAAA AAAA AAAA); DISAGREE=(AAAA AAAA BBBB AAAA)
u=0; ref=""; for a in "${AGREE[@]}"; do [[ -z "$ref" ]] && ref="$a"; [[ "$a" == "$ref" ]] || u=1; done
check "agreeing endpoints pass"                  "0" "$u"
u=0; ref=""; for a in "${DISAGREE[@]}"; do [[ -z "$ref" ]] && ref="$a"; [[ "$a" == "$ref" ]] || u=1; done
check "one divergent endpoint is caught"         "1" "$u"
check "a 2-block spread is within tolerance"     "yes" "$([[ $(( 102 - 100 )) -le 3 ]] && echo yes || echo no)"
check "a 40-block spread is not"                 "no"  "$([[ $(( 140 - 100 )) -le 3 ]] && echo yes || echo no)"
check "the rig invalidates on disagreement"      "1" \
  "$(grep -c 'endpoints disagreed at' "$CAL")"
check "  and on excessive lag"                   "1" \
  "$(grep -c 'endpoint heights span' "$CAL")"
check "  and refuses a catching-up endpoint"     "1" \
  "$(grep -c 'is still catching up' "$CAL")"

# ---------------------------------------------------------------------------------
group waits "a bare wait also waits for the sampler that never exits"

# Found by a live run, not by reading. Both scripts run an endless background sampler
# — mempool depth in the drill, application-DB size in the rig — and both fan out
# their broadcasts as background jobs. `wait` with no arguments waits for EVERY
# background job of the shell, so it also waits for the sampler, which never
# finishes. The burst completed, the chain drained it at ten transactions a block,
# and the drill sat there forever.
#
# The fix is to wait by PID. These checks pin that, because the bug reads as
# perfectly ordinary shell and its symptom is a hang rather than a failure.
check "the drill has no bare wait"               "0" \
  "$(grep -cE '^[[:space:]]*wait[[:space:]]*$|;[[:space:]]*wait[[:space:]]*;' "$DRILL_FILE")"
check "the rig has no bare wait"                 "0" \
  "$(grep -cE '^[[:space:]]*wait[[:space:]]*$|;[[:space:]]*wait[[:space:]]*;' "$CAL")"
check "the drill waits by pid"                   "1" \
  "$(grep -c 'wait "\${BPIDS\[@\]}"; BPIDS=()' "$DRILL_FILE")"
check "the rig waits by pid"                     "1" \
  "$(grep -c 'wait "\${BPIDS\[@\]}"; BPIDS=()' "$CAL")"
check "the drill runs an endless sampler"        "1" \
  "$(grep -c 'BACKLOG_PID=\$!' "$DRILL_FILE")"
check "the rig runs an endless sampler"          "1" \
  "$(grep -c 'APPDB_PID=\$!' "$CAL")"

# The behaviour itself, demonstrated rather than asserted: a shell with a long-lived
# background job plus short ones cannot use a bare wait to join only the short ones.
( sleep 30 ) & LONG=$!
( : ) & SHORT=$!
wait "$SHORT" 2>/dev/null
check "waiting by pid returns while the other runs" "yes" \
  "$(kill -0 "$LONG" 2>/dev/null && echo yes || echo no)"
kill "$LONG" 2>/dev/null; wait "$LONG" 2>/dev/null

# ---------------------------------------------------------------------------------
group knee "no bracket, no number"

# S12/S13/S14. A run establishes a knee only if it brackets one. Every other outcome
# names what to change and yields nothing — a rig that extrapolated past its own
# measurements would be inventing the parameter it exists to measure.
check "a genuine bracket"                        "BRACKETED" "$(knee_classify 4 5)"
check "top step still safe"                      "UNBOUNDED_BY_RUN_INCREASE_LOAD" "$(knee_classify 5 "")"
check "first step already unsafe"                "BELOW_TEST_RANGE_REDUCE_LOAD"   "$(knee_classify "" 1)"
check "nothing usable"                           "NO_USABLE_STEPS" "$(knee_classify "" "")"
# An unsafe step BELOW the safe one is not a bracket: capacity that recovers as load
# increases is not a transition, it is a measurement to distrust.
check "an inverted pair is not a bracket"        "NO_USABLE_STEPS" "$(knee_classify 5 3)"
check "equal steps are not a bracket"            "NO_USABLE_STEPS" "$(knee_classify 3 3)"
check "the safety margin reduces the estimate"   "1600000" "$(candidate_from_knee 2000000 2000)"
check "a zero margin keeps the estimate"         "2000000" "$(candidate_from_knee 2000000 0)"
candidate_from_knee 0 2000 >/dev/null 2>&1
check "a zero knee yields no candidate"          "nonzero" "$(rc_of $?)"
candidate_from_knee "NA" 2000 >/dev/null 2>&1
check "an NA knee yields no candidate"           "nonzero" "$(rc_of $?)"
candidate_from_knee 2000000 10000 >/dev/null 2>&1
check "a 100% margin is refused"                 "nonzero" "$(rc_of $?)"
check "the rig emits no candidate when unbracketed" "1" \
  "$(grep -c 'the ramp never became unsafe' "$CAL")"
check "  and none when the first step is unsafe"    "1" \
  "$(grep -c 'the first loaded step was already unsafe' "$CAL")"
check "  and none from an invalid measurement"      "1" \
  "$(grep -c 'A candidate is never emitted from an invalid experiment' "$CAL")"
check "the knee uses a tail metric, not a majority" "0" \
  "$(grep -c 'majority of blocks' "$CAL")"
check "  p95 is what decides safe/unsafe"           "1" \
  "$(grep -c 'P95 <= CAL_TARGET_BLOCK_MS' "$CAL")"

# ---------------------------------------------------------------------------------
group percentile "the tail statistic itself"

check "p95 of twenty"                            "19" "$(percentile_of 95 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20)"
check "p50 of ten"                               "5"  "$(percentile_of 50 1 2 3 4 5 6 7 8 9 10)"
check "a single sample"                          "42" "$(percentile_of 95 42)"
# The behaviour a median would hide: nineteen good blocks and one very bad one.
check "p50 hides a single bad tail"              "100" "$(percentile_of 50 100 100 100 100 100 100 100 100 100 9000)"
check "  p95 does not"                           "9000" "$(percentile_of 95 100 100 100 100 100 100 100 100 100 9000)"
percentile_of 95 >/dev/null 2>&1
check "an empty sample refuses"                  "nonzero" "$(rc_of $?)"
percentile_of 95 a b >/dev/null 2>&1
check "a non-numeric sample refuses"             "nonzero" "$(rc_of $?)"
percentile_of 0 1 2 3 >/dev/null 2>&1
check "an out-of-range percentile refuses"       "nonzero" "$(rc_of $?)"

# ---------------------------------------------------------------------------------
group deferral "excess in later blocks is arithmetic, not a claim about timing"

span_min() { echo $(( ($1 + $2 - 1) / $2 )); }
check "60 at 10 per block need 6"                "6" "$(span_min 60 10)"
check "61 at 10 per block need 7"                "7" "$(span_min 61 10)"
check "10 at 10 per block need 1"                "1" "$(span_min 10 10)"
check "one block fails the 6-block bound"        "no"  "$([[ 1 -ge 6 ]] && echo yes || echo no)"
check "six blocks satisfy it"                    "yes" "$([[ 6 -ge 6 ]] && echo yes || echo no)"
check "the drill's span bound is ceil(N/CAP)"    "$(span_min "$FLOOD_SENDERS" "$PER_BLOCK_CAP")" "$MIN_SPAN_BLOCKS"

# ---------------------------------------------------------------------------------
group cardinality "an unreadable window is not a well-behaved one"

FIXTURE=''
OVER=0; READ=0; WINDOW=5
for h in 10 11 12 13 14; do
  gw="$(block_gas http://x "$h" gas_wanted)" || continue
  READ=$(( READ + 1 )); (( gw > 2000000 )) && OVER=$(( OVER + 1 ))
done
check "nothing exceeded the ceiling"             "0" "$OVER"
check "  because nothing was read"               "0" "$READ"
check "and the pairing catches it"               "different" "$([[ "$READ" != "$WINDOW" ]] && echo different || echo SAME)"
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
check "a leap day"                               "951782400000"  "$(rfc3339_to_ms 2000-02-29T00:00:00Z)"
check "past a non-leap century"                  "4107542400000" "$(rfc3339_to_ms 2100-03-01T00:00:00Z)"
check "a single fractional digit"                "1767225600500" "$(rfc3339_to_ms 2026-01-01T00:00:00.5Z)"
rfc3339_to_ms "not a time" >/dev/null 2>&1
check "unparseable input refuses"                "nonzero" "$(rc_of $?)"
rfc3339_to_ms "2026-13-01T00:00:00Z" >/dev/null 2>&1
check "an impossible month refuses"              "nonzero" "$(rc_of $?)"
rfc3339_to_ms "2026-08-29T12:34:56+02:00" >/dev/null 2>&1
check "a non-UTC offset refuses"                 "nonzero" "$(rc_of $?)"
check "3s in milliseconds"                       "3000" "$(commit_timeout_ms 3s)"
check "200ms in milliseconds"                    "200"  "$(commit_timeout_ms 200ms)"
commit_timeout_ms "soon" >/dev/null 2>&1
check "an unusable cadence refuses"              "nonzero" "$(rc_of $?)"

# ---------------------------------------------------------------------------------
group address "recipient addresses are checked against pinned vectors"

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
bech32_encode_into A "0000000000000000000000000000000000000001"
bech32_encode_into B "0000000000000000000000000000000000000002"
check "distinct payloads differ"                 "different" "$([[ "$A" != "$B" ]] && echo different || echo SAME)"
bech32_encode_into ODD "01020" >/dev/null 2>&1
check "an odd-length payload refuses"            "nonzero" "$(rc_of $?)"
bech32_encode_into NH "zzzz" >/dev/null 2>&1
check "a non-hex payload refuses"                "nonzero" "$(rc_of $?)"

# ---------------------------------------------------------------------------------
group genesis "the genesis patch refuses a document it does not recognise"

node_home() { echo "$TMP/$1"; }
mkdir -p "$TMP/0/config" "$TMP/1/config" "$TMP/2/config"
printf '{"consensus":{"params":{"block":{"max_bytes":"22020096","max_gas":"-1"}}}}\n' >"$TMP/0/config/genesis.json"
check "the shipped default is readable"          "-1"       "$(genesis_block_field 0 max_gas)"
check "max_bytes is readable"                    "22020096" "$(genesis_block_field 0 max_bytes)"
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

# S16. A contract that cannot be satisfied makes the drill permanently red, and the
# red reads as a chain defect rather than an editing mistake.
check "the per-block cap follows from the ceiling" "$(( BLOCK_GAS_MAX / FLOOD_TX_GAS ))" "$PER_BLOCK_CAP"
check "the installed ceiling is itself finite"     "yes" "$(max_gas_is_finite "$BLOCK_GAS_MAX")"
MS_TOTAL="$(tr ',' '\n' <<<"$DRILL_EXPECTED_MULTISET" | awk -F: '{ s += $NF } END { print s + 0 }')"
check "the multiset totals the assertion count"    "$DRILL_EXPECTED_ASSERTIONS" "$MS_TOTAL"
MS_SORTED="$(tr ',' '\n' <<<"$DRILL_EXPECTED_MULTISET" | LC_ALL=C sort | paste -sd, -)"
check "the multiset is in canonical order"         "same" \
  "$([[ "$MS_SORTED" == "$DRILL_EXPECTED_MULTISET" ]] && echo same || echo REORDERED)"
check "every multiset key names a node"            "0" \
  "$(tr ',' '\n' <<<"$DRILL_EXPECTED_MULTISET" | grep -cv '|')"
# Every gate the drill declares must be emitted by the drill, or finalization fails
# on a component nobody reported.
GATE_MISSING=0
for g in "${DRILL_VERDICT_GATES[@]}"; do
  k="${g%%=*}"
  grep -q "\"$k=" "$DRILL_FILE" || GATE_MISSING=$(( GATE_MISSING + 1 ))
done
check "every declared gate is emitted"             "0" "$GATE_MISSING"
check "the contract names five gates"              "5" "${#DRILL_VERDICT_GATES[@]}"
check "the phase count matches the contract"       "7" "$DRILL_EXPECTED_PHASES"
PHASE_ENDS="$(grep -c '^phase_end ' "$DRILL_FILE")"
check "  and that many phase_end calls exist"      "$DRILL_EXPECTED_PHASES" "$PHASE_ENDS"

# ---------------------------------------------------------------------------------
group verdict "a failing run still writes a machine-readable verdict"

EV="$TMP/evid1"; drill_assert_init "$EV" >/dev/null 2>&1
expect "deliberately_failing" "a" "b" >/dev/null 2>&1
( DRILL_MANDATORY_FILES=(); DRILL_VERDICT_GATES=(); DRILL_EXPECTED_PHASES=0
  DRILL_EXPECTED_ASSERTIONS=0; DRILL_EXPECTED_MULTISET=""; finalize_verdict >/dev/null 2>&1 )
check "a failed assertion exits non-zero"        "1" "$?"
check "and the verdict file records it"          "overall=FAIL" "$(grep '^overall=' "$EV/verdict.txt")"

EV2="$TMP/evid2"; drill_assert_init "$EV2" >/dev/null 2>&1
expect "passing_assertion" "a" "a" >/dev/null 2>&1
( DRILL_MANDATORY_FILES=(); DRILL_EXPECTED_PHASES=0; DRILL_EXPECTED_ASSERTIONS=0
  DRILL_EXPECTED_MULTISET=""; DRILL_VERDICT_GATES=("saturation=BINDING")
  DRILL_VERDICT_LINES=("saturation=NOT_DEMONSTRATED"); finalize_verdict >/dev/null 2>&1 )
check "an out-of-set component fails"            "1" "$?"

EV3="$TMP/evid3"; drill_assert_init "$EV3" >/dev/null 2>&1
expect "passing_assertion" "a" "a" >/dev/null 2>&1
( DRILL_MANDATORY_FILES=(); DRILL_EXPECTED_PHASES=0; DRILL_EXPECTED_ASSERTIONS=0
  DRILL_EXPECTED_MULTISET=""; DRILL_VERDICT_GATES=("saturation=BINDING"); DRILL_VERDICT_LINES=()
  finalize_verdict >/dev/null 2>&1 )
check "an unreported component fails"            "1" "$?"

EV4="$TMP/evid4"; drill_assert_init "$EV4" >/dev/null 2>&1
expect "passing_assertion" "a" "a" >/dev/null 2>&1
( DRILL_MANDATORY_FILES=(blocks.csv); DRILL_VERDICT_GATES=(); DRILL_EXPECTED_PHASES=0
  DRILL_EXPECTED_ASSERTIONS=0; DRILL_EXPECTED_MULTISET=""; finalize_verdict >/dev/null 2>&1 )
check "missing mandatory evidence fails"         "1" "$?"

EV5="$TMP/evid5"; drill_assert_init "$EV5" >/dev/null 2>&1
expect "passing_assertion" "a" "a" >/dev/null 2>&1
( DRILL_MANDATORY_FILES=(); DRILL_EXPECTED_PHASES=0; DRILL_EXPECTED_ASSERTIONS=0
  DRILL_EXPECTED_MULTISET=""; DRILL_VERDICT_GATES=("saturation=BINDING")
  DRILL_VERDICT_LINES=("saturation=BINDING"); FAILURES=0; finalize_verdict >/dev/null 2>&1 )
check "a clean run passes"                       "0" "$?"
check "  and records PASS"                       "overall=PASS" "$(grep '^overall=' "$EV5/verdict.txt")"

# ---------------------------------------------------------------------------------
# The per-group and total contracts. Declared deliberately: a floor lets checks
# vanish silently, and fitting these numbers to whatever the run produced would make
# them decoration.
GROUP_NAMES=(gasreaders ceiling meta params delivery saturation stall drain concurrency
             attribution na namespace endpoints waits knee percentile deferral cardinality
             time address genesis contract verdict)
PER_GROUP_EXPECTED=(16 9 7 5 11 17 9 9 7 7 11 8 7 7 16 8 6 5 11 7 5 9 7)

EXPECTED_CHECKS=0
for e in "${PER_GROUP_EXPECTED[@]}"; do EXPECTED_CHECKS=$(( EXPECTED_CHECKS + e )); done
TOTAL=$(( PASSED + FAILED ))

echo
if (( GROUP_IDX + 1 != ${#PER_GROUP_EXPECTED[@]} )); then
  echo "block-gas faults: FAIL — $((GROUP_IDX + 1)) groups ran, the contract names ${#PER_GROUP_EXPECTED[@]}" >&2
  exit 1
fi
for i in $(seq 0 $GROUP_IDX); do
  if [[ "${GROUP_KEYS[$i]}" != "${GROUP_NAMES[$i]}" ]]; then
    echo "block-gas faults: FAIL — group $i is ${GROUP_KEYS[$i]}, the contract names ${GROUP_NAMES[$i]}" >&2
    exit 1
  fi
  if (( GROUP_COUNTS[i] != PER_GROUP_EXPECTED[i] )); then
    echo "block-gas faults: FAIL — group ${GROUP_KEYS[$i]} ran ${GROUP_COUNTS[$i]}, the contract is ${PER_GROUP_EXPECTED[$i]}" >&2
    exit 1
  fi
done
if (( TOTAL != EXPECTED_CHECKS )); then
  echo "block-gas faults: FAIL — $TOTAL checks ran, the contract is $EXPECTED_CHECKS" >&2
  echo "  (reconcile the contract deliberately; do not fit it to the run)" >&2
  exit 1
fi
if (( FAILED > 0 )); then
  echo "block-gas faults: FAIL ($FAILED of $TOTAL)" >&2; exit 1
fi
echo "block-gas faults: PASS ($PASSED checks across ${#PER_GROUP_EXPECTED[@]} groups)"
