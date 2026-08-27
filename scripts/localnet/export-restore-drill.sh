#!/usr/bin/env bash
# Export, restore and fresh-node-join characterization  (issue #108)
#
# This drill CHARACTERIZES three operational questions. It does not implement
# continuation support and it does not fix anything it finds:
#
#   1. can a live chain that has crossed two epoch boundaries and settled real
#      value be exported, and does the document carry the state it should?
#   2. can a chain be started from that export? The answer may legitimately be
#      "no, and here is the exact refusal" — that is a result, not a failure.
#   3. can a node with no data join the running network and reach the head?
#
# The export is taken deliberately MID-EPOCH, at a height where per-slot
# participation for the open epoch is non-zero. An epoch-boundary export has no
# in-progress participation to lose, so it would pass while saying nothing about
# whether that state is exportable at all.
#
# The exported height is derived from the ARTIFACT, never from a height read taken
# before the node stops: that read races the final commit. The artifact reports
# initial_height, H_export is initial_height - 1, and every live-state query is
# then pinned to exactly that height against nodes that are still running.
#
# The restore attempt is classified, not merely recorded:
#
#   REFUSED_AS_DESIGNED  refused under an identified existing fresh-genesis rule
#   SUPPORTED            accepted, commits blocks, agrees, state matches
#   DEFECT               accepted but cannot progress, or silently lost state
#
# DEFECT is a failure of this drill. "We wrote it down" does not make an
# accepted-but-invalid continuation a legitimate characterization result.

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
# Set before sourcing: drill-common derives the evidence directory from DRILL at
# source time.
export DRILL="export-restore"
. "$ROOT/scripts/localnet/lib/drill-common.sh"
. "$ROOT/scripts/localnet/lib/drill-assert.sh"
# drill-common enables `set -e`; this drill accounts for its own failures and must
# not abort halfway through, leaving a localnet running and no evidence.
set +e

DRILL_NAME="export/restore drill"

NODE_COUNT=4
export NODE_COUNT
BIN="$ROOT/build/twilightd"
EXPECTED_VALIDATORS=4

# The ratified epoch interval is [360, 720] and genesis refuses anything outside
# it, so this localnet runs a fast block time rather than a short epoch.
EPOCH_LENGTH=360
export TWILIGHT_LOCALNET_TIMEOUT_COMMIT="${EXPORT_DRILL_TIMEOUT_COMMIT:-200ms}"

# Where in epoch 3 to export. Deliberately far from both boundaries so a
# mid-epoch export cannot be confused with a boundary one.
EXPORT_TARGET_HEIGHT=$((EPOCH_LENGTH * 2 + 90))    # 810

SLOT_ID=1
SETTLE_EPOCH=1
# Three participant lines, each above the immutable 10,000 utwlt dust floor.
PAY_A=11000
PAY_B=22000
PAY_C=33000

JOIN_NODE=4

need curl; need jq

# ---- readers ----------------------------------------------------------------
# Every reader either produces a usable value or fails; none of them may hand a
# caller an empty string that arithmetic would later read as zero.

app_height() { rpc_get "$1" /abci_info | jq -er '.result.response.last_block_height'; }
store_height() { rpc_get "$1" /status | jq -er '.result.sync_info.latest_block_height'; }
mq() { "$BIN" mining-query "$@" --node "$(rpc_url "${NODE_FOR_QUERY:-1}")" --output json 2>/dev/null; }
rq() { "$BIN" rewards-query "$@" --node "$(rpc_url "${NODE_FOR_QUERY:-1}")" --output json 2>/dev/null; }
cq() { "$BIN" coreslot-query "$@" --node "$(rpc_url "${NODE_FOR_QUERY:-1}")" --output json 2>/dev/null; }

current_epoch() { rq epoch-info | jq -er '.state.current_epoch'; }
epoch_len_live() { rq params | jq -er '.params.epoch_length_blocks'; }
open_reward_blocks_live() { rq epoch-info | jq -er '.open_reward_enabled_blocks'; }

# Count of slots reporting a non-zero active-block tally for the OPEN epoch.
# The per-slot field is `blocks_active`; the array is `active_blocks`.
active_blocks_nonzero_count() {
  rq current-active-blocks | jq -er '[(.active_blocks // [])[] | select((.blocks_active|tonumber) > 0)] | length'
}

# A height target every validator has reached, with a liveness guard so a dead
# process is reported as such instead of being blamed on a slow chain.
wait_height_live() { # <target> [timeout-seconds]
  local target="$1" deadline=$((SECONDS + ${2:-900})) n live pid h low
  while (( SECONDS < deadline )); do
    live=0
    for (( n = 0; n < NODE_COUNT; n++ )); do
      pid="$(cat "$NET/node$n.pid" 2>/dev/null)"
      [[ "$pid" =~ ^[0-9]+$ ]] || continue
      node_process_matches "$pid" "$(node_home "$n")" && live=$((live + 1))
    done
    if (( live < 3 )); then
      fail "only $live of $NODE_COUNT validators are alive; the chain cannot reach $target"
      return 1
    fi
    low=999999999
    for (( n = 0; n < NODE_COUNT; n++ )); do
      h="$(latest_height "$n" 2>/dev/null || echo 0)"
      [[ "$h" =~ ^[0-9]+$ ]] || h=0
      (( h < low )) && low="$h"
    done
    (( low >= target )) && return 0
    sleep 2
  done
  fail "the chain did not reach height $target within the window"
  return 1
}

# RPC comes up a moment after the processes do; querying before then reads an
# empty body, which jq reports as a missing field rather than as "not ready".
wait_rpc_ready() { # [timeout-seconds]
  local deadline=$((SECONDS + ${1:-120})) n ok_count
  while (( SECONDS < deadline )); do
    ok_count=0
    for (( n = 0; n < NODE_COUNT; n++ )); do
      [[ "$(latest_height "$n" 2>/dev/null || echo 0)" =~ ^[1-9][0-9]*$ ]] && ok_count=$((ok_count + 1))
    done
    (( ok_count == NODE_COUNT )) && return 0
    sleep 2
  done
  return 1
}
active_blocks_at() { # <height>
  "$BIN" rewards-query current-active-blocks --height "$1" \
    --node "$(rpc_url "${NODE_FOR_QUERY:-1}")" --output json 2>/dev/null
}

sha256_of() { shasum -a 256 "$1" 2>/dev/null | awk '{print $1}'; }

# ---- transactions -----------------------------------------------------------
mining_broadcast() { # <key> <home> <mining-subcommand...>
  local key="$1" home="$2"; shift 2
  "$BIN" tx mining "$@" \
    --from "$key" --keyring-backend test --home "$home" \
    --chain-id "$CHAIN_ID" --node "$(rpc_url 0)" \
    --gas 600000 --fees 0utwlt --broadcast-mode sync --output json -y 2>/dev/null
}
mining_submit() { # <key> <home> <mining-subcommand...>
  local key="$1" home="$2"; shift 2
  LAST_TXHASH=""; LAST_TXCODE=""
  local out checkcode
  out="$(mining_broadcast "$key" "$home" "$@")" || true
  LAST_TXHASH="$(jq -r '.txhash // ""' <<<"$out" 2>/dev/null || echo "")"
  checkcode="$(jq -r '.code // empty' <<<"$out" 2>/dev/null || echo "")"
  if [[ -z "$LAST_TXHASH" ]]; then LAST_TXCODE="broadcast_error"; return 0; fi
  if [[ -n "$checkcode" && "$checkcode" != "0" ]]; then LAST_TXCODE="$checkcode"; return 0; fi
  # The DELIVERED code is the one that decided the transaction. A sync broadcast
  # reports CheckTx only, so a message refused during execution still answers 0.
  LAST_TXCODE="$(_wait_tx_code "$LAST_TXHASH")"
  return 0
}

# The chain requires chunk recipients in DECODED-BYTE order, which is not bech32
# text order, so it is derived from the address bytes themselves.
addr_hex() { "$BIN" debug addr "$1" 2>/dev/null | awk '/hex/ { print $3 }'; }

# ---- agreement on the RESTORED network --------------------------------------
# agree.sh derives its endpoints as 26657 + i*100 and reads logs from the ordinary
# localnet home. Both are hard-coded, and both are wrong here: the restored chain
# listens on the 27657 series under its own home, and the ordinary network is
# stopped by the time this runs. Parameterising agree.sh would change behaviour
# for its existing callers, so the restored check is local to this drill.

RESTORE_RPC_BASE=27657
restore_rpc_port() { echo $(( RESTORE_RPC_BASE + $1 * 100 )); }
# Overridable so the fast suite can drive every branch of the helper below from
# fixtures. The shipped default is a real HTTP GET against the restored node.
restore_rpc_get() { curl -fsS "http://127.0.0.1:$(restore_rpc_port "$1")$2" 2>/dev/null; }

# restore_agreement <nodes> — sets, IN THE PARENT SHELL:
#     RESTORE_AGREEMENT_RESULT   agree | disagree | catching_up | unreachable
#                                | no_common_height | unreadable
#     RESTORE_AGREEMENT_HEIGHT   the common committed height, or 0
#     RESTORE_AGREEMENT_ROWS     per-node "nodeN@H=app:vals:next"
#
# It assigns rather than prints because a caller using $( ) runs it in a subshell,
# and every global it set would be discarded — which is how the recorded common
# height and per-node rows came back empty.
#
# Only "agree" may contribute to SUPPORTED. The other five are distinct reasons
# the proof was not obtained, and none of them is agreement.
RESTORE_AGREEMENT_RESULT=""
RESTORE_AGREEMENT_HEIGHT=0
RESTORE_AGREEMENT_ROWS=""
restore_agreement() {
  local total="$1" i st cu h low=999999999 blk trip first="" result="agree"
  RESTORE_AGREEMENT_RESULT=""; RESTORE_AGREEMENT_HEIGHT=0; RESTORE_AGREEMENT_ROWS=""
  if (( total <= 0 )); then RESTORE_AGREEMENT_RESULT="unreachable"; return 0; fi
  for (( i = 0; i < total; i++ )); do
    st="$(restore_rpc_get "$i" /status)"
    if [[ -z "$st" ]]; then RESTORE_AGREEMENT_RESULT="unreachable"; return 0; fi
    # `.catching_up // "true"` is wrong: jq's // treats boolean false as the
    # alternate case, so a HEALTHY node — which reports false — was read as
    # catching up, and the healthy branch could never be reached. The field must
    # be an actual boolean, and absence or the wrong type is unreadable, not
    # "assume the worst and call it catching up".
    cu="$(jq -r 'if (.result.sync_info.catching_up | type) == "boolean"
                 then (.result.sync_info.catching_up | tostring) else "malformed" end' <<<"$st" 2>/dev/null)"
    case "$cu" in
      false) : ;;
      true)  RESTORE_AGREEMENT_RESULT="catching_up"; return 0 ;;
      *)     RESTORE_AGREEMENT_RESULT="unreadable"; return 0 ;;
    esac
    h="$(jq -r '.result.sync_info.latest_block_height // ""' <<<"$st" 2>/dev/null)"
    if [[ ! "$h" =~ ^[0-9]+$ ]]; then RESTORE_AGREEMENT_RESULT="unreadable"; return 0; fi
    (( h < low )) && low="$h"
  done
  if (( low <= 0 || low >= 999999999 )); then RESTORE_AGREEMENT_RESULT="no_common_height"; return 0; fi
  RESTORE_AGREEMENT_HEIGHT="$low"
  for (( i = 0; i < total; i++ )); do
    blk="$(restore_rpc_get "$i" "/block?height=$low")"
    trip="$(jq -r 'if (.result.block.header.app_hash? // "") == "" or
                      (.result.block.header.validators_hash? // "") == "" or
                      (.result.block.header.next_validators_hash? // "") == ""
                   then "" else .result.block.header
                        | "\(.app_hash):\(.validators_hash):\(.next_validators_hash)" end' \
             <<<"${blk:-null}" 2>/dev/null)"
    if [[ -z "$trip" ]]; then RESTORE_AGREEMENT_RESULT="unreadable"; RESTORE_AGREEMENT_HEIGHT=0; return 0; fi
    RESTORE_AGREEMENT_ROWS="${RESTORE_AGREEMENT_ROWS}${RESTORE_AGREEMENT_ROWS:+;}node$i@$low=$trip"
    [[ -z "$first" ]] && first="$trip"
    [[ "$trip" == "$first" ]] || result="disagree"
  done
  RESTORE_AGREEMENT_RESULT="$result"
}

# ---- canonical economic state ------------------------------------------------
# One projection, applied to every source: the state captured live at H_export,
# the exported document, and a restored chain. Writing a separate comparison per
# source is how the export gate and the restore gate came to check different
# things; a single function cannot drift from itself.
#
# Only fields that MUST survive unchanged are projected. Anything that advances
# naturally is excluded, so a mismatch always means something was lost rather
# than something moved.
econ_canon() { # <state-json>
  local j="$1"
  [[ -n "$j" ]] || return 1
  jq -er '
    def s(x): (x // "") | tostring;
    ( [ (.epochs // [])[]
        | "epoch:\(s(.epoch_number)):\(s(.start_height)):\(s(.end_height)):\(s(.minted_emission))"
        + ":\(s(.carry_in)):\(s(.distributable_fees)):\(s(.treasury_amount)):\(s(.reward_pool))"
        + ":\(s(.allocated_amount)):\(s(.carry_out)):\(s(.distribution_method)):\(s(.remainder_policy))"
        + ":\(s(.cumulative_emitted_after_epoch)):\(s(.reward_enabled_blocks))" ]
    + [ (.entitlements // [])[]
        | "ent:\(s(.slot_id))/\(s(.epoch)):\(s(.total_blocks_active)):\(s(.entitlement_amount))"
        + ":\(s(.released_amount)):\(s(.payout_address)):\(s(.reward_config_version))"
        + ":\(s(.slot_status_at_epoch_close)):\(s(.activation_sequence_at_epoch_close)):\(s(.created_height))" ]
    + [ (.settlements // [])[]
        | "set:\(s(.slot_id))/\(s(.epoch)):\(s(.distribution_mode_version)):\(s(.settlement_mode))"
        + ":\(s(.settlement_params_version)):\(s(.next_chunk_index)):\(s(.finalized))"
        + ":\(s(.finalized_height)):\(s(.finalization_reason))" ]
    + [ (.slots // [])[] | "slot:\(s(.slot_id)):\(s(.status)):\(s(.consensus_power))" ]
    + [ "bal:\(s(.balances.liability)):\(s(.balances.carry)):\(s(.balances.escrow))" ]
    ) | sort | join("\n")
  ' <<<"$j" 2>/dev/null || return 1
}

# ---- open-epoch participation preservation ----------------------------------
# The question is whether the pre-export per-slot attribution survived the
# export/restore medium — NOT whether a restarted chain later accrues fresh
# counters. Asking the restored chain can only ever answer the second, and a
# non-zero reading there would conceal exactly the loss being tested for.
#
# So it is answered from the ARTIFACT: what went in, and whether the document
# carries a field able to hold it.
participation_preservation() { # <captured-nonzero-slots> <export-has-per-slot-field>
  local captured="$1" carried="$2"
  [[ "$captured" =~ ^[0-9]+$ ]] || { echo "unreadable"; return 0; }
  if (( captured == 0 )); then echo "n/a"; return 0; fi
  [[ "$carried" == "present" ]] && { echo "preserved"; return 0; }
  echo "lost"
}

# ---- outcome classification -------------------------------------------------
# Deterministic, and deliberately separate from the code that gathers the inputs,
# so the fast suite exercises the SHIPPED classifier rather than a copy of its
# branches. Every input is an explicit value; nothing is read from the machine.

MIN_RESTORE_PROGRESS=3

# refusal_class <logfile> — which refusal, if any, a restored node recorded.
#
# "the process died" is not evidence of a designed refusal. Only the specific
# CoreSlot fresh-genesis rejection counts: a mid-life document describes ACTIVE
# slots activated long before the height it would resume at, and that is what the
# rule exists to catch. Slot numbers and heights are left unpinned — they vary per
# run — but the semantic class is required in full.
refusal_class() {
  local f="$1" body
  [[ -f "$f" ]] || { echo "none"; return 0; }
  body="$(tr 'A-Z' 'a-z' <"$f" 2>/dev/null)"
  if [[ "$body" == *"initialize coreslot genesis"* \
     && "$body" == *"activation-effective heights equal to the initial height"* \
     && "$body" == *"invalid core slot genesis"* ]]; then
    echo "coreslot-fresh-genesis"; return 0
  fi
  if [[ "$body" == *"panic"* || "$body" == *"err"* ]]; then echo "other"; return 0; fi
  echo "none"
}

# classify_restore_outcome <nodes> <alive> <refused_as_designed> <progress>
#                          <hash_agree> <state_match> <participation_preservation>
#
#   progress             an unsigned integer, or "n/a" when no chain was running
#                        to measure. NEVER a zero invented for a read that failed.
#   hash_agree           agree | disagree | catching_up | unreachable
#                        | no_common_height | unreadable | n/a
#   state_match          match | mismatch | n/a
#   participation        preserved | lost | unreadable | n/a
#
# Progress alone does not make a continuation supported. A chain that accepts the
# document and then runs on state that silently lost something is worse than one
# that refuses, because nobody finds out.
classify_restore_outcome() {
  local nodes="$1" alive="$2" refused="$3" progress="$4"
  local agree="$5" state="$6" participation="$7"
  local v
  for v in "$nodes" "$alive" "$refused"; do
    [[ "$v" =~ ^[0-9]+$ ]] || { echo "DEFECT"; return 0; }
  done
  (( nodes > 0 )) || { echo "DEFECT"; return 0; }

  if (( alive == 0 )); then
    # Progress is irrelevant when nothing is running; only the refusal evidence
    # decides. Every node must have refused for the identified reason.
    (( refused == nodes )) && { echo "REFUSED_AS_DESIGNED"; return 0; }
    echo "DEFECT"; return 0
  fi

  # A mixed network is never a supported restore. One validator accepting the
  # document while another rejects it is a determinism or configuration failure in
  # its own right — worse than either clean outcome, because the two halves of the
  # network disagree about whether the chain exists.
  (( alive == nodes )) || { echo "DEFECT"; return 0; }
  (( refused == 0 )) || { echo "DEFECT"; return 0; }

  # A height that could not be read is not a height. Accepting "n/a" here — or a
  # zero standing in for a failed read — would let an unreadable first sample and
  # a readable second one fabricate an enormous apparent progress.
  [[ "$progress" =~ ^[0-9]+$ ]] || { echo "DEFECT"; return 0; }
  (( progress >= MIN_RESTORE_PROGRESS )) || { echo "DEFECT"; return 0; }
  [[ "$agree" == "agree" ]] || { echo "DEFECT"; return 0; }
  [[ "$state" == "match" ]] || { echo "DEFECT"; return 0; }
  # If the document was accepted as a continuation, the open epoch's per-slot
  # attribution had to come with it. At this baseline the artifact carries no
  # field able to hold it, so this branch is unreachable by construction — which
  # is the honest encoding of the finding, not a gap.
  [[ "$participation" == "preserved" ]] || { echo "DEFECT"; return 0; }
  echo "SUPPORTED"
}

# Component verdicts are complete sub-proofs, not proxies for one another. An
# artifact that exists says nothing about whether it carried the right state, and
# a node that reached height 1 has not joined.
export_outcome() { # <artifact_nonempty> <height_derived> <semantic_ok>
  [[ "$1" == "true" && "$2" == "true" && "$3" == "true" ]] && echo PASS || echo FAIL
}
# join=PASS is the whole chain of custody: an empty home, a process this drill
# launched that owns that home, an RPC whose identity is that home's node key,
# synchronization, the same process still owning it afterwards, and agreement.
# Anything less lets a service already occupying the port answer for a node that
# never started.
join_outcome() { # <started_empty> <owns_home> <rpc_identity> <synced> <owns_after> <hash_agree>
  [[ "$1" == "true" && "$2" == "true" && "$3" == "true" \
     && "$4" == "synced" && "$5" == "true" && "$6" == "agree" ]] && echo PASS || echo FAIL
}

# ---- cleanup, scoped to this drill's own processes --------------------------
# Never a broad pkill: another twilightd on the machine is not this drill's to
# kill, and a stale pid file must not authorize killing whatever reused the pid.
# Both the live localnet and the isolated restore network are matched, on the
# executable path and the exact node home.
node_process_matches() { # <pid> <home>
  local cmd home i
  cmd="$(ps -o command= -p "$1" 2>/dev/null)" || return 1
  [[ -n "$cmd" ]] || return 1
  # shellcheck disable=SC2206
  local argv=($cmd)
  [[ "${argv[0]}" == "$BIN" ]] || return 1
  for (( i = 1; i < ${#argv[@]}; i++ )); do
    if [[ "${argv[i]}" == "--home" ]]; then home="${argv[i+1]:-}"; break; fi
    if [[ "${argv[i]}" == --home=* ]]; then home="${argv[i]#--home=}"; break; fi
  done
  [[ -n "${home:-}" && "$home" == "$2" ]] || return 1
  return 0
}
kill_recorded() { # <pidfile> <home>
  local pid
  [[ -f "$1" ]] || return 0
  pid="$(cat "$1" 2>/dev/null)"
  if [[ "$pid" =~ ^[0-9]+$ ]] && node_process_matches "$pid" "$2"; then
    kill "$pid" 2>/dev/null || true
  fi
  rm -f "$1"
}
cleanup_drill() {
  local i
  for (( i = 0; i <= JOIN_NODE; i++ )); do kill_recorded "$NET/node$i.pid" "$(node_home "$i")"; done
  for (( i = 0; i < NODE_COUNT; i++ )); do kill_recorded "$RESTORE_NET/node$i.pid" "$RESTORE_NET/node$i"; done
}

RESTORE_NET="${NET}-restore"

# The negative tests source this file to exercise the real helpers. Everything
# above is definitions with no side effects; everything below touches the machine.
[[ "${EXPORT_DRILL_SOURCE_ONLY:-0}" == "1" ]] && return 0

# Isolation is checked by what is serving THESE homes, not by process name.
LEFTOVER="$(pgrep -f "$ROOT/build/twilightd.* start --home $NET/" 2>/dev/null | tr '\n' ' ')"
LEFTOVER="$LEFTOVER$(pgrep -f "$ROOT/build/twilightd.* start --home $RESTORE_NET/" 2>/dev/null | tr '\n' ' ')"
if [[ -n "${LEFTOVER// /}" ]]; then
  echo "  ABORT: processes are already serving these homes (pids: $LEFTOVER); stop them first" >&2; exit 2
fi
trap cleanup_drill EXIT

echo "=== 1. a four-validator localnet ==="
SOURCE_SHA="$(git -C "$ROOT" rev-parse HEAD)"
rm -rf "$RESTORE_NET"
echo "  initializing (init.sh builds the node binary; a cold Go cache takes a few minutes)"
"$ROOT/scripts/localnet/init.sh" >/dev/null 2>&1 || { echo "  ABORT: init failed" >&2; exit 2; }
"$ROOT/scripts/localnet/start.sh" >/dev/null 2>&1 || { echo "  ABORT: start failed" >&2; exit 2; }
wait_rpc_ready 120 || { echo "  ABORT: the validators did not begin serving RPC" >&2; exit 2; }
init_evidence
drill_assert_init "$EVID_DIR" "assertions.jsonl" "summary.csv" \
  || { echo "  ABORT: evidence could not be initialized" >&2; exit 2; }

BIN_SHA="$(sha256_of "$BIN")"
jq -nc --arg c "$SOURCE_SHA" --arg b "$BIN_SHA" --arg p "$BIN" \
  '{source_commit:$c, binary:{path:$p, sha256:$b, build:"go build -o build/twilightd ./cmd/twilightd"}}' \
  >"$EVID_DIR/binaries.json" || { echo "  ABORT: could not write provenance" >&2; exit 2; }

phase_begin
[[ -n "$BIN_SHA" ]] || fail "the node binary could not be hashed"
expect "chain_id_recorded" "$CHAIN_ID" "$CHAIN_ID"
phase_end "provenance" "sha=${BIN_SHA:0:16} chain=$CHAIN_ID"

echo
phase_begin
echo "=== 2. epoch geometry, asserted before anything depends on it ==="
if read_required_uint EPOCH_LEN epoch_len_live; then
  expect "epoch_length_is_expected" "$EPOCH_LENGTH" "$EPOCH_LEN"
else fail "the live epoch length could not be read"; fi
# The export target must sit inside epoch 3, clear of both boundaries.
expect "export_target_not_boundary" "true" \
  "$([[ $((EXPORT_TARGET_HEIGHT % EPOCH_LENGTH)) -ne 0 ]] && echo true || echo false)"
expect "export_target_in_epoch_3" "3" "$(( EXPORT_TARGET_HEIGHT / EPOCH_LENGTH + 1 ))"
phase_end "geometry" "epoch=$EPOCH_LENGTH target=$EXPORT_TARGET_HEIGHT"

echo
phase_begin
echo "=== 3. epoch 1 closes and a settlement is paid and finalized ==="
wait_height_live $((EPOCH_LENGTH + 1)) || die "the chain did not reach the first epoch boundary"
NODE_FOR_QUERY=1
SETTLEMENT="$(mq settlement "$SLOT_ID" "$SETTLE_EPOCH")"
[[ -n "$SETTLEMENT" ]] || die "no settlement materialized for slot $SLOT_ID epoch $SETTLE_EPOCH"
expect "settlement_materialized" "false" "$(jq -r '.settlement.finalized' <<<"$SETTLEMENT")"

OPERATOR0="$(sed -n 's/.*"address":"\([^"]*\)".*/\1/p' "$NET/operator0.json")"
OPERATOR1="$(sed -n 's/.*"address":"\([^"]*\)".*/\1/p' "$NET/operator1.json")"
OPERATOR2="$(sed -n 's/.*"address":"\([^"]*\)".*/\1/p' "$NET/operator2.json")"
# Recipients sorted by decoded address bytes, which is what the chain requires.
RECIPIENTS="$(
  for a in "$OPERATOR0:$PAY_A" "$OPERATOR1:$PAY_B" "$OPERATOR2:$PAY_C"; do
    printf '%s %s\n' "$(addr_hex "${a%%:*}")" "$a"
  done | LC_ALL=C sort | awk '{print $2}'
)"
PAYOUT_ARGS=()
while read -r line; do
  [[ -z "$line" ]] && continue
  PAYOUT_ARGS+=("{\"recipient\":\"${line%%:*}\",\"amount\":\"${line##*:}\"}")
done <<<"$RECIPIENTS"

mining_submit operator0 "$(node_home 0)" submit-settlement-chunk \
  --slot-id "$SLOT_ID" --epoch "$SETTLE_EPOCH" --chunk-index 0 \
  --payouts "${PAYOUT_ARGS[0]}" --payouts "${PAYOUT_ARGS[1]}" --payouts "${PAYOUT_ARGS[2]}"
expect "settlement_chunk_delivered" "0" "$LAST_TXCODE"

mining_submit operator0 "$(node_home 0)" finalize-settlement \
  --slot-id "$SLOT_ID" --epoch "$SETTLE_EPOCH"
expect "settlement_finalize_delivered" "0" "$LAST_TXCODE"

SETTLEMENT="$(mq settlement "$SLOT_ID" "$SETTLE_EPOCH")"
expect "settlement_finalized" "true" "$(jq -r '.settlement.finalized' <<<"$SETTLEMENT")"
RELEASED="$(jq -r '.released_amount // "0"' <<<"$SETTLEMENT")"
expect "settlement_released_value" "true" "$([[ "$RELEASED" != "0" && -n "$RELEASED" ]] && echo true || echo false)"
phase_end "settle" "slot=$SLOT_ID epoch=$SETTLE_EPOCH released=$RELEASED"

echo
phase_begin
echo "=== 4. past the second boundary, then into epoch 3 with live participation ==="
wait_height_live $((EPOCH_LENGTH * 2 + 1)) || die "the chain did not reach the second epoch boundary"
wait_height_live "$EXPORT_TARGET_HEIGHT" || die "the chain did not reach the mid-epoch export region"
if read_required_uint EPOCH_NOW current_epoch; then
  expect "current_epoch_before_export" "3" "$EPOCH_NOW"
else fail "the current epoch could not be read"; fi
# The whole point of a mid-epoch export: there is in-progress participation to
# lose. Asserted before the export, so a boundary export cannot masquerade.
if read_required_uint AB_NONZERO active_blocks_nonzero_count; then
  expect "active_blocks_nonzero_slots" "true" "$([[ "$AB_NONZERO" -ge 2 ]] && echo true || echo false)"
else fail "the open-epoch active-block tally could not be read"; fi
phase_end "midepoch" "epoch=${EPOCH_NOW:-?} nonzero_slots=${AB_NONZERO:-?}"

echo
phase_begin
echo "=== 5. the export, and the height it is authoritative for ==="
# The export command opens the application database directly, and a running node
# holds the goleveldb lock. Stopping ONE of four leaves 3/4 voting power, above
# the 2/3 quorum, so the chain keeps producing and the height-pinned queries in
# the next phase stay answerable.
#
# A pre-stop height read is recorded, but is NOT the authoritative linkage: it
# races the final commit. The artifact decides what was exported.
if read_required_uint H_PRESTOP app_height 0; then
  note "node0 reported height $H_PRESTOP before stopping (supporting evidence only)"
else fail "node0's pre-stop height could not be read"; fi
kill_recorded "$NET/node0.pid" "$(node_home 0)"
sleep 3

EXPORT_DOC="$EVID_DIR/export.json"
EXPORT_ERR="$EVID_DIR/export-stderr.txt"
"$BIN" export --home "$(node_home 0)" --output-document "$EXPORT_DOC" >"$EXPORT_ERR" 2>&1
EXPORT_RC=$?
EXPORT_ARTIFACT_OK=false; EXPORT_HEIGHT_OK=false; EXPORT_SEMANTIC_OK=true
expect "export_succeeded" "0" "$EXPORT_RC" || EXPORT_SEMANTIC_OK=false
if [[ -s "$EXPORT_DOC" ]]; then EXPORT_ARTIFACT_OK=true; fi
expect "export_artifact_nonempty" "true" "$EXPORT_ARTIFACT_OK"
sha256_of "$EXPORT_DOC" >"$EVID_DIR/export.sha256"

# H_export comes from the artifact. Everything downstream pins to this number.
if read_required_uint EXPORT_INITIAL_HEIGHT jq -er '.initial_height | tonumber' "$EXPORT_DOC"; then
  H_EXPORT=$((EXPORT_INITIAL_HEIGHT - 1))
  EXPORT_HEIGHT_OK=true
  ok "export_height_from_artifact (initial_height=$EXPORT_INITIAL_HEIGHT -> H_export=$H_EXPORT)"
  record_assert "-" "export_height_from_artifact" "derived" "derived" PASS
  expect "export_height_is_mid_epoch" "true" \
    "$([[ $((H_EXPORT % EPOCH_LENGTH)) -ne 0 ]] && echo true || echo false)" || EXPORT_HEIGHT_OK=false
  expect "export_height_epoch_is_3" "3" "$(( H_EXPORT / EPOCH_LENGTH + 1 ))" || EXPORT_HEIGHT_OK=false
else
  fail "the exported document carries no usable initial_height"
  record_assert "-" "export_height_from_artifact" "derived" "unreadable" FAIL
fi

# The same export with --for-zero-height. The app's exporter ignores that
# argument, so this records an operational characteristic of the export CLI
# rather than a state-machine property.
"$BIN" export --home "$(node_home 0)" --for-zero-height \
  --output-document "$EVID_DIR/export-zero-height.json" >/dev/null 2>&1
if [[ -s "$EVID_DIR/export-zero-height.json" ]]; then
  ZH_SAME="$(jq -S . "$EVID_DIR/export-zero-height.json" 2>/dev/null | shasum -a 256 | awk '{print $1}')"
  BASE_SAME="$(jq -S . "$EXPORT_DOC" 2>/dev/null | shasum -a 256 | awk '{print $1}')"
  ZH_RESULT="$([[ "$ZH_SAME" == "$BASE_SAME" ]] && echo identical || echo differs)"
else
  ZH_RESULT="unproduced"
fi
# The operator note states affirmatively that the flag is inert at this baseline,
# so the drill asserts that claim rather than accepting whichever answer appeared.
# "differs" or "unproduced" would mean the documentation is wrong.
expect "for_zero_height_is_inert" "identical" "$ZH_RESULT"

start_node 0
wait_synced 0 120 || note "node0 did not resynchronize within the window; later agreement checks will show it"
phase_end "export" "H_export=${H_EXPORT:-?} zero_height=$ZH_RESULT"

echo
phase_begin
echo "=== 6. the live state at exactly the exported height ==="
# Pinned against nodes 1/2/3, which are still running and only a few blocks
# ahead, so H_export remains an available historical height.
NODE_FOR_QUERY=1
CAPTURE="$EVID_DIR/state-at-export-height.json"
if [[ -n "${H_EXPORT:-}" ]]; then
  QN=1   # a node that is still running; H_EXPORT is a few blocks behind its head
  hq() { "$BIN" "$@" --height "$H_EXPORT" --node "$(rpc_url "$QN")" --output json 2>/dev/null; }

  AB_AT="$(hq rewards-query current-active-blocks)"
  SLOTS_AT="$(hq coreslot-query slots)"
  MB_AT="$(hq rewards-query module-balances)"
  EP1="$(hq rewards-query epoch-reward 1)"
  EP2="$(hq rewards-query epoch-reward 2)"
  ENT1="$(hq rewards-query epoch-entitlements 1)"
  ENT2="$(hq rewards-query epoch-entitlements 2)"

  # The settlement identity set is derived from the ENTITLEMENTS, not from the
  # export. Taking it from the document would let an omitted row vanish from both
  # sides of the comparison and pass unnoticed.
  SET_ROWS="[]"
  IDENTS="$(jq -r '[(.entitlements // [])[] | "\(.slot_id) \(.epoch)"] | .[]' \
             <<<"$(jq -sc '{entitlements: (map(.entitlements // []) | add)}' <<<"$ENT1 $ENT2")" 2>/dev/null)"
  while read -r sid ep; do
    [[ -n "$sid" && -n "$ep" ]] || continue
    row="$(hq mining-query settlement "$sid" "$ep" | jq -c '.settlement // empty' 2>/dev/null)"
    [[ -n "$row" ]] && SET_ROWS="$(jq -c --argjson r "$row" '. + [$r]' <<<"$SET_ROWS")"
  done <<<"$IDENTS"

  jq -nc \
    --argjson ab "${AB_AT:-null}" --argjson slots "${SLOTS_AT:-null}" \
    --argjson mb "${MB_AT:-null}" --argjson e1 "${EP1:-null}" --argjson e2 "${EP2:-null}" \
    --argjson t1 "${ENT1:-null}" --argjson t2 "${ENT2:-null}" \
    --argjson sets "${SET_ROWS:-[]}" --argjson h "$H_EXPORT" \
    '{height:$h, active_blocks:$ab, raw_slots:$slots, module_balances:$mb,
      state:{
        epochs: [ ($e1.epoch_reward // empty), ($e2.epoch_reward // empty) ],
        entitlements: (($t1.entitlements // []) + ($t2.entitlements // [])),
        settlements: $sets,
        slots: ($slots.slots // []),
        balances: { liability: ($mb.outstanding_entitlement_liability // ""),
                    carry: ($mb.carry_forward_remainder // ""),
                    escrow: ($mb.rewards_balance // "") }
      }}' >"$CAPTURE" 2>/dev/null
  expect "state_captured_at_export_height" "true" "$([[ -s "$CAPTURE" ]] && echo true || echo false)"
  CAP_AB_NONZERO="$(jq -r '[(.active_blocks.active_blocks // [])[] | select((.blocks_active|tonumber) > 0)] | length' "$CAPTURE" 2>/dev/null)"
  expect "captured_active_blocks_nonzero" "true" \
    "$([[ "${CAP_AB_NONZERO:-0}" -ge 2 ]] && echo true || echo false)"
  # The capture is only a proof if it actually contains the settled objects the
  # export is going to be compared against.
  CAP_COUNTS="$(jq -r '"\(.state.epochs|length)/\(.state.entitlements|length)/\(.state.settlements|length)"' "$CAPTURE" 2>/dev/null)"
  expect "captured_state_is_populated" "2/8/8" "$CAP_COUNTS"
else
  fail "no exported height; the state capture cannot be pinned"
fi
phase_end "capture" "height=${H_EXPORT:-?} nonzero_slots=${CAP_AB_NONZERO:-?}"

echo
phase_begin
echo "=== 7. what the exported document actually carries ==="
# Semantic comparison against the state captured at the exported height, not a
# check that module keys exist. #108 claims the export contains the settled
# economic state, and presence does not establish that.
EXPORT_SUMMARY="$EVID_DIR/export-summary.json"
# Any failed comparison disqualifies export=PASS; a produced artifact is not the
# same as a correct one.
expect_export() { expect "$@" || EXPORT_SEMANTIC_OK=false; }

if [[ -s "$EXPORT_DOC" && -s "$CAPTURE" ]]; then
  # Both sides through the SAME projection. A separate comparison per source is
  # how the export gate and the restore gate came to check different things.
  EXPORT_STATE="$(jq -c '{
      epochs: (.app_state.rewards.finalized_epochs // []),
      entitlements: (.app_state.rewards.slot_entitlements // []),
      settlements: (.app_state.mining.settlements // []),
      slots: (.app_state.coreslot.slots // []),
      balances: { liability: (.app_state.rewards.outstanding_entitlement_liability // ""),
                  carry: (.app_state.rewards.state.carry_forward_remainder // ""),
                  escrow: "" }
    }' "$EXPORT_DOC" 2>/dev/null)"
  # Escrow is a bank balance, so it is derived from the export's own bank state
  # via the rewards module account rather than read from a rewards field.
  REWARDS_ADDR="$(jq -r '[.app_state.auth.accounts[]? | select(.name == "rewards") | .base_account.address] | first // ""' "$EXPORT_DOC" 2>/dev/null)"
  EXP_ESCROW="$(jq -r --arg a "$REWARDS_ADDR" '[.app_state.bank.balances[]? | select(.address == $a) | .coins[]? | select(.denom == "utwlt") | .amount] | first // "0"' "$EXPORT_DOC" 2>/dev/null)"
  EXPORT_STATE="$(jq -c --arg e "$EXP_ESCROW" '.balances.escrow = $e' <<<"$EXPORT_STATE")"

  CAPTURED_STATE="$(jq -c '.state' "$CAPTURE" 2>/dev/null)"
  CANON_CAPTURED="$(econ_canon "$CAPTURED_STATE")"
  CANON_EXPORT="$(econ_canon "$EXPORT_STATE")"
  if [[ -z "$CANON_CAPTURED" || -z "$CANON_EXPORT" ]]; then
    fail "the canonical economic state could not be computed on both sides"
    EXPORT_SEMANTIC_OK=false
  fi
  # One assertion over the complete projection: finalized-epoch economics,
  # entitlement amounts, released values, payout addresses, per-slot participation
  # for closed epochs, settlement workflow records, CoreSlot identity, liability,
  # carry and escrow. Any one field differing fails it.
  expect_export "export_state_matches_captured" "$CANON_CAPTURED" "$CANON_EXPORT"
  # Line counts named separately so a failure says WHICH class of object moved.
  expect_export "export_object_counts_match" \
    "$(grep -c . <<<"$CANON_CAPTURED")" "$(grep -c . <<<"$CANON_EXPORT")"

  # The TW-011 probe. Named for the act of determining, not for an answer.
  AB_IN_EXPORT="$(jq -r 'if (.app_state.rewards | has("active_blocks")) or (.app_state.rewards | has("slot_active_blocks")) then "present" else "absent" end' "$EXPORT_DOC" 2>/dev/null)"
  ok "export_active_blocks_classified ($AB_IN_EXPORT)"
  record_assert "-" "export_active_blocks_classified" "classified" "$AB_IN_EXPORT" PASS
  ORB_IN_EXPORT="$(jq -r '.app_state.rewards.open_reward_enabled_blocks // "absent"' "$EXPORT_DOC" 2>/dev/null)"
  ok "export_open_reward_blocks_classified ($ORB_IN_EXPORT)"
  record_assert "-" "export_open_reward_blocks_classified" "classified" "$ORB_IN_EXPORT" PASS
  # Whether the pre-export attribution survived the medium — answered from the
  # artifact, because asking a restarted chain could only ever report fresh
  # counters and would conceal the loss.
  PARTICIPATION_PRESERVED="$(participation_preservation "${CAP_AB_NONZERO:-0}" "$AB_IN_EXPORT")"
  ok "open_participation_preservation_classified ($PARTICIPATION_PRESERVED)"
  record_assert "-" "open_participation_preservation_classified" "classified" "$PARTICIPATION_PRESERVED" PASS

  jq -nc --arg canon "$CANON_EXPORT" --arg ab "$AB_IN_EXPORT" --arg orb "$ORB_IN_EXPORT" \
    --arg pres "$PARTICIPATION_PRESERVED" --arg zh "$ZH_RESULT" --argjson h "${H_EXPORT:-0}" \
    --argjson lines "$(grep -c . <<<"$CANON_EXPORT")" \
    '{export_height:$h, canonical_object_lines:$lines, canonical_state:$canon,
      open_epoch_per_slot_active_blocks:$ab, open_reward_enabled_blocks:$orb,
      open_participation_preservation:$pres, for_zero_height:$zh}' >"$EXPORT_SUMMARY" 2>/dev/null
else
  fail "the export document or the captured state is missing; no comparison is possible"
  EXPORT_SEMANTIC_OK=false
fi
phase_end "export_content" "active_blocks=${AB_IN_EXPORT:-?} epochs=${EXP_EPOCHS:-?}"

echo
phase_begin
echo "=== 8. a node with no data joins the running network ==="
# join_node builds a home with no application or database state, copies node 0's
# genesis, peers to every already-running node, and assigns its own ports. It is
# a non-validator: it holds no CoreSlot, which is the recovery case an operator
# actually faces.
JOIN_HOME="$(node_home "$JOIN_NODE")"
rm -rf "$JOIN_HOME"
JOIN_EMPTY="$([[ ! -d "$JOIN_HOME/data" ]] && echo true || echo false)"
JOIN_SYNCED="stalled"; JOIN_AGREE="disagree"
expect "join_started_from_empty_state" "true" "$JOIN_EMPTY"
if read_required_uint JOIN_START_HEIGHT latest_height 1; then
  note "the joining node starts against a network at height $JOIN_START_HEIGHT"
else fail "the network height at join time could not be read"; fi
NODE_COUNT_SAVED="$NODE_COUNT"
join_node "$JOIN_NODE"
# The node id derived from the home this drill just created. If a bind fails and
# something else is already answering on the join port, its RPC reports a
# different id, so sync and five-way agreement cannot be satisfied by an
# impersonator.
JOIN_EXPECT_ID="$("$BIN" tendermint show-node-id --home "$JOIN_HOME" 2>/dev/null)"
start_node "$JOIN_NODE"
JOIN_T0=$SECONDS
sleep 4
JOIN_PID="$(cat "$NET/node$JOIN_NODE.pid" 2>/dev/null)"
JOIN_OWNS="false"
if [[ "$JOIN_PID" =~ ^[0-9]+$ ]] && node_process_matches "$JOIN_PID" "$JOIN_HOME"; then JOIN_OWNS="true"; fi
expect "join_process_owns_home" "true" "$JOIN_OWNS" "$JOIN_NODE"
JOIN_RPC_ID="$(rpc_get "$JOIN_NODE" /status 2>/dev/null | jq -r '.result.node_info.id // ""' 2>/dev/null)"
expect "join_rpc_identity_matches_home" "$JOIN_EXPECT_ID" "$JOIN_RPC_ID" "$JOIN_NODE"
if wait_synced "$JOIN_NODE" 300; then JOIN_SYNCED="synced"; fi
JOIN_SECONDS=$((SECONDS - JOIN_T0))
expect "join_node_synced" "synced" "$JOIN_SYNCED" "$JOIN_NODE"
# Still the same process on the same home after synchronizing, so a node that
# died and was replaced mid-sync cannot carry the proof.
JOIN_OWNS_AFTER="false"
JOIN_PID_AFTER="$(cat "$NET/node$JOIN_NODE.pid" 2>/dev/null)"
if [[ "$JOIN_PID_AFTER" =~ ^[0-9]+$ ]] && [[ "$JOIN_PID_AFTER" == "$JOIN_PID" ]] \
   && node_process_matches "$JOIN_PID_AFTER" "$JOIN_HOME"; then JOIN_OWNS_AFTER="true"; fi
expect "join_process_owns_home_after_sync" "true" "$JOIN_OWNS_AFTER" "$JOIN_NODE"
read_required_uint JOIN_END_HEIGHT latest_height "$JOIN_NODE" \
  || fail "the joining node's height could not be read"
if agree_nodes "0 1 2 3 $JOIN_NODE" "post-join"; then JOIN_AGREE="agree"; fi
expect "join_app_hash_agrees" "agree" "$JOIN_AGREE" "$JOIN_NODE"
jq -nc --argjson s "${JOIN_START_HEIGHT:-0}" --argjson e "${JOIN_END_HEIGHT:-0}" \
  --argjson secs "$JOIN_SECONDS" --arg home "$JOIN_HOME" \
  --arg pid "${JOIN_PID:-}" --arg eid "${JOIN_EXPECT_ID:-}" --arg rid "${JOIN_RPC_ID:-}" \
  --arg owns "${JOIN_OWNS:-false}" --arg ownsafter "${JOIN_OWNS_AFTER:-false}" \
  '{start_height:$s, end_height:$e, sync_seconds:$secs, home:$home,
    pid:$pid, expected_node_id:$eid, rpc_node_id:$rid,
    process_owns_home:$owns, process_owns_home_after_sync:$ownsafter,
    operator_inputs:["the running network'"'"'s genesis.json",
                     "persistent_peers for at least one reachable member",
                     "the twilightd binary",
                     "non-colliding RPC/P2P/gRPC ports",
                     "no application or database state"],
    validator:false, method:"ordinary block sync (no state-sync snapshot)"}' \
  >"$EVID_DIR/join.json" 2>/dev/null
phase_end "join" "start=${JOIN_START_HEIGHT:-?} end=${JOIN_END_HEIGHT:-?} secs=$JOIN_SECONDS"

echo
phase_begin
echo "=== 9. starting a chain from the exported document, in isolation ==="
# The live network is stopped first. Same chain-id plus the same validator keys on
# one host is a double-signing setup; this chain has no slashing, but running it
# anyway would be sloppy rather than safe.
for (( i = 0; i <= JOIN_NODE; i++ )); do kill_recorded "$NET/node$i.pid" "$(node_home "$i")"; done
sleep 4

RESTORE_OUTCOME="NOT_ATTEMPTED"
RESTORE_DETAIL=""
RESTORE_RC=""
if [[ -s "$EXPORT_DOC" ]]; then
  rm -rf "$RESTORE_NET"; mkdir -p "$RESTORE_NET"
  for (( i = 0; i < NODE_COUNT; i++ )); do
    rhome="$RESTORE_NET/node$i"
    "$BIN" init "restore$i" --chain-id "$CHAIN_ID" --home "$rhome" >/dev/null 2>&1
    cp "$EXPORT_DOC" "$rhome/config/genesis.json"
    # The signing KEY is required for the exported validator set to sign. The
    # signing STATE is not copied: it records the later height the live chain
    # reached, and a chain restarting at H_export + 1 needs state consistent with
    # itself, not with a future it never had.
    cp "$(node_home "$i")/config/priv_validator_key.json" "$rhome/config/priv_validator_key.json"
    cp "$(node_home "$i")/config/node_key.json" "$rhome/config/node_key.json"
    printf '{"height":"0","round":0,"step":0}\n' >"$rhome/data/priv_validator_state.json"
  done
  rpeers=""
  for (( i = 0; i < NODE_COUNT; i++ )); do
    rid="$("$BIN" tendermint show-node-id --home "$RESTORE_NET/node$i" 2>/dev/null)"
    rpeers="${rpeers}${rpeers:+,}${rid}@127.0.0.1:$((27656 + i * 100))"
  done
  for (( i = 0; i < NODE_COUNT; i++ )); do
    rhome="$RESTORE_NET/node$i"
    sed -i.bak \
      -e "s#laddr = \"tcp://127.0.0.1:26657\"#laddr = \"tcp://127.0.0.1:$((27657 + i * 100))\"#" \
      -e "s#laddr = \"tcp://0.0.0.0:26656\"#laddr = \"tcp://0.0.0.0:$((27656 + i * 100))\"#" \
      -e "s#persistent_peers = \"\"#persistent_peers = \"${rpeers}\"#" \
      -e "s#pex = true#pex = false#" \
      -e "s#allow_duplicate_ip = false#allow_duplicate_ip = true#" \
      -e "s#^timeout_commit = .*#timeout_commit = \"200ms\"#" \
      "$rhome/config/config.toml"
    sed -i.bak -e "s#address = \"localhost:9090\"#address = \"localhost:$((19090 + i * 100))\"#" "$rhome/config/app.toml"
    rm -f "$rhome/config/"*.bak
    mkdir -p "$RESTORE_NET/logs"
    "$BIN" start --home "$rhome" --minimum-gas-prices 0utwlt --log_no_color \
      >>"$RESTORE_NET/logs/node$i.log" 2>&1 &
    echo "$!" >"$RESTORE_NET/node$i.pid"
  done
  sleep 25

  # Per-node refusal classification. "The process died" is not evidence of a
  # designed refusal, so each log is classified individually and only the
  # identified CoreSlot fresh-genesis class counts.
  R_ALIVE=0; R_REFUSED=0; R_CLASSES=""
  for (( i = 0; i < NODE_COUNT; i++ )); do
    rpid="$(cat "$RESTORE_NET/node$i.pid" 2>/dev/null)"
    if [[ "$rpid" =~ ^[0-9]+$ ]] && node_process_matches "$rpid" "$RESTORE_NET/node$i"; then
      R_ALIVE=$((R_ALIVE + 1)); rclass="alive"
    else
      rclass="$(refusal_class "$RESTORE_NET/logs/node$i.log")"
      [[ "$rclass" == "coreslot-fresh-genesis" ]] && R_REFUSED=$((R_REFUSED + 1))
    fi
    R_CLASSES="${R_CLASSES}${R_CLASSES:+,}node$i=$rclass"
  done
  REFUSAL="$(grep -hoE 'initialize coreslot genesis[^"]{0,200}' "$RESTORE_NET"/logs/node*.log 2>/dev/null | head -1)"
  [[ -n "$REFUSAL" ]] || REFUSAL="$(grep -hoE '(panic|ERR).{0,200}' "$RESTORE_NET"/logs/node*.log 2>/dev/null | head -1)"

  # Heights go through the validated readers. A read that failed is not a zero:
  # coercing it would let an unreadable first sample and a readable second one
  # fabricate an enormous apparent progress.
  restore_height() { restore_rpc_get "$1" /status | jq -er '.result.sync_info.latest_block_height'; }
  R_PROGRESS="n/a"; R_AGREE="n/a"; R_STATE="n/a"
  if (( R_ALIVE == NODE_COUNT )); then
    if read_required_uint R_H0 restore_height 0; then
      sleep 12
      if read_required_uint R_H1 restore_height 0; then
        if (( R_H1 >= R_H0 )); then R_PROGRESS=$((R_H1 - R_H0)); else R_PROGRESS="n/a"; fi
      fi
    fi
  fi

  # Everything below is only evaluated when a complete restored network is
  # actually running and has made the required progress; otherwise the inputs
  # stay n/a and the classifier refuses on them rather than on their absence.
  if [[ "$R_PROGRESS" =~ ^[0-9]+$ ]] && (( R_PROGRESS >= MIN_RESTORE_PROGRESS )); then
    restore_agreement "$NODE_COUNT"
    R_AGREE="$RESTORE_AGREEMENT_RESULT"
    # The restored chain through the SAME projection as the capture and the
    # export, so a lost finalized epoch, entitlement, released value or
    # settlement record is a mismatch rather than something nobody compared.
    rq_r() { "$BIN" "$@" --node "http://127.0.0.1:$(restore_rpc_port 0)" --output json 2>/dev/null; }
    R_SETS="[]"
    R_ENT1="$(rq_r rewards-query epoch-entitlements 1)"; R_ENT2="$(rq_r rewards-query epoch-entitlements 2)"
    R_IDENTS="$(jq -r '[(.entitlements // [])[] | "\(.slot_id) \(.epoch)"] | .[]' \
                 <<<"$(jq -sc '{entitlements: (map(.entitlements // []) | add)}' <<<"$R_ENT1 $R_ENT2")" 2>/dev/null)"
    while read -r sid ep; do
      [[ -n "$sid" && -n "$ep" ]] || continue
      row="$(rq_r mining-query settlement "$sid" "$ep" | jq -c '.settlement // empty' 2>/dev/null)"
      [[ -n "$row" ]] && R_SETS="$(jq -c --argjson r "$row" '. + [$r]' <<<"$R_SETS")"
    done <<<"$R_IDENTS"
    R_MB="$(rq_r rewards-query module-balances)"
    R_STATE_JSON="$(jq -nc \
      --argjson e1 "$(rq_r rewards-query epoch-reward 1)" --argjson e2 "$(rq_r rewards-query epoch-reward 2)" \
      --argjson t1 "${R_ENT1:-null}" --argjson t2 "${R_ENT2:-null}" --argjson sets "$R_SETS" \
      --argjson sl "$(rq_r coreslot-query slots)" --argjson mb "${R_MB:-null}" \
      '{epochs: [ ($e1.epoch_reward // empty), ($e2.epoch_reward // empty) ],
        entitlements: (($t1.entitlements // []) + ($t2.entitlements // [])),
        settlements: $sets, slots: ($sl.slots // []),
        balances: { liability: ($mb.outstanding_entitlement_liability // ""),
                    carry: ($mb.carry_forward_remainder // ""),
                    escrow: ($mb.rewards_balance // "") }}' 2>/dev/null)"
    CANON_RESTORED="$(econ_canon "${R_STATE_JSON:-}")"
    if [[ -n "$CANON_RESTORED" && "$CANON_RESTORED" == "${CANON_CAPTURED:-}" ]]; then
      R_STATE="match"
    else
      R_STATE="mismatch"
    fi
  fi

  RESTORE_OUTCOME="$(classify_restore_outcome "$NODE_COUNT" "$R_ALIVE" "$R_REFUSED" "$R_PROGRESS" \
                       "$R_AGREE" "$R_STATE" "${PARTICIPATION_PRESERVED:-unreadable}")"
  case "$RESTORE_OUTCOME" in
    REFUSED_AS_DESIGNED)
      RESTORE_DETAIL="every restored node refused the mid-life document under the CoreSlot fresh-genesis rule" ;;
    SUPPORTED)
      RESTORE_DETAIL="accepted, committed $R_PROGRESS blocks from height $R_H0, nodes agree, state matches, open-epoch participation present" ;;
    *)
      RESTORE_DETAIL="alive=$R_ALIVE refused_as_designed=$R_REFUSED/$NODE_COUNT progress=$R_PROGRESS agree=$R_AGREE state=$R_STATE participation=${PARTICIPATION_PRESERVED:-unreadable}" ;;
  esac
  for (( i = 0; i < NODE_COUNT; i++ )); do kill_recorded "$RESTORE_NET/node$i.pid" "$RESTORE_NET/node$i"; done
else
  RESTORE_OUTCOME="NOT_ATTEMPTED"
  RESTORE_DETAIL="no export document was produced"
fi

# REFUSED_AS_DESIGNED must be reconstructible from the evidence alone, so every
# node's own excerpt is preserved rather than one aggregate sample.
RESTORE_NODES_JSON="[]"
for (( i = 0; i < NODE_COUNT; i++ )); do
  nlog="$RESTORE_NET/logs/node$i.log"
  [[ -f "$nlog" ]] && cp "$nlog" "$EVID_DIR/restore-node$i.log" 2>/dev/null
  nclass="$(refusal_class "$nlog")"
  nexcerpt="$(grep -hoE 'initialize coreslot genesis.{0,200}' "$nlog" 2>/dev/null | head -1)"
  [[ -n "$nexcerpt" ]] || nexcerpt="$(grep -hoE '(panic|ERR).{0,200}' "$nlog" 2>/dev/null | head -1)"
  nstate="dead"
  npid="$(cat "$RESTORE_NET/node$i.pid" 2>/dev/null)"
  [[ "$npid" =~ ^[0-9]+$ ]] && node_process_matches "$npid" "$RESTORE_NET/node$i" && nstate="alive"
  RESTORE_NODES_JSON="$(jq -c --argjson n "$i" --arg st "$nstate" --arg c "$nclass" --arg x "$nexcerpt" \
    '. + [{node:$n, process_state:$st, refusal_class:$c, excerpt:$x}]' <<<"$RESTORE_NODES_JSON")"
done
printf '%s\n' "$RESTORE_NODES_JSON" | jq . >"$EVID_DIR/restore-nodes.json" 2>/dev/null

ok "restore_outcome_classified ($RESTORE_OUTCOME)"
record_assert "-" "restore_outcome_classified" "classified" "$RESTORE_OUTCOME" PASS
# The one restore assertion that actually asserts. An accepted-but-invalid
# continuation is a defect, and writing it down does not make it a result.
expect "restore_outcome_is_not_defect" "true" \
  "$([[ "$RESTORE_OUTCOME" != "DEFECT" && "$RESTORE_OUTCOME" != "NOT_ATTEMPTED" ]] && echo true || echo false)"
jq -nc --arg o "$RESTORE_OUTCOME" --arg d "$RESTORE_DETAIL" --arg r "${REFUSAL:-}" \
  --arg classes "${R_CLASSES:-}" --arg agree "${R_AGREE:-n/a}" --arg state "${R_STATE:-n/a}" \
  --arg part "${PARTICIPATION_PRESERVED:-unreadable}" \
  --arg arows "${RESTORE_AGREEMENT_ROWS:-}" --argjson aheight "${RESTORE_AGREEMENT_HEIGHT:-0}" \
  --arg prog "${R_PROGRESS:-n/a}" \
  --argjson rpcbase "$RESTORE_RPC_BASE" \
  --argjson nodes "$NODE_COUNT" --argjson alive "${R_ALIVE:-0}" \
  --argjson refused "${R_REFUSED:-0}" --argjson prog "${R_PROGRESS:-0}" \
  --argjson h0 "${R_H0:-0}" --argjson h1 "${R_H1:-0}" \
  '{outcome:$o, detail:$d, nodes:$nodes, nodes_alive:$alive,
    nodes_refused_as_designed:$refused, per_node_refusal_class:$classes,
    blocks_committed:$prog, height_first_seen:$h0, height_after_window:$h1,
    app_hash_agreement:$agree, persistent_state:$state, open_epoch_participation:$part,
    restored_rpc_base:$rpcbase, agreement_common_height:$aheight, agreement_rows:$arows,
    log_excerpt:$r}' >"$EVID_DIR/restore-attempt.json" 2>/dev/null
phase_end "restore" "outcome=$RESTORE_OUTCOME alive=${R_ALIVE:-0} progress=${R_PROGRESS:-n/a}"

echo
echo "================= export / restore / join ============="
echo "  source        $SOURCE_SHA"
echo "  binary        ${BIN_SHA:0:16}"
echo "  epoch length  $EPOCH_LENGTH"
echo "  exported at   H_export=${H_EXPORT:-?}  (initial_height=${EXPORT_INITIAL_HEIGHT:-?}, epoch 3, mid-epoch)"
echo "  participation ${CAP_AB_NONZERO:-?} slots non-zero at the exported height"
echo "  in the export per-slot active_blocks: ${AB_IN_EXPORT:-?} / open_reward_enabled_blocks: ${ORB_IN_EXPORT:-?}"
echo "  for-zero-hgt  ${ZH_RESULT:-?}"
echo "  join          node$JOIN_NODE from empty state -> ${JOIN_END_HEIGHT:-?} in ${JOIN_SECONDS:-?}s"
echo "  restore       $RESTORE_OUTCOME"
echo "  evidence      $EVID_DIR"
echo "======================================================="

DRILL_MANDATORY_FILES=(
  binaries.json export.json export.sha256 export-summary.json
  state-at-export-height.json join.json restore-attempt.json restore-nodes.json
  summary.csv assertions.jsonl hashes.jsonl
)
DRILL_VERDICT_LINES=(
  "export=$(export_outcome "${EXPORT_ARTIFACT_OK:-false}" "${EXPORT_HEIGHT_OK:-false}" "${EXPORT_SEMANTIC_OK:-false}")"
  "restore=$RESTORE_OUTCOME"
  "join=$(join_outcome "${JOIN_EMPTY:-false}" "${JOIN_OWNS:-false}" \
            "$([[ -n "${JOIN_EXPECT_ID:-}" && "${JOIN_RPC_ID:-}" == "${JOIN_EXPECT_ID:-x}" ]] && echo true || echo false)" \
            "${JOIN_SYNCED:-stalled}" "${JOIN_OWNS_AFTER:-false}" "${JOIN_AGREE:-disagree}")"
)
# The exact proof contract: how many phases a complete run records, how many
# assertions it makes, and how many times each one is made. Derived from the phase
# structure and then confirmed by a run, not copied from whatever a run emitted.
#
# Keyed on (assertion, node) rather than the name alone: an assertion that repeats
# across a fan-out can lose one repetition and gain another elsewhere while the
# per-name total is unchanged. Adding or renaming an assertion is meant to require
# editing this line.
DRILL_EXPECTED_PHASES=9
DRILL_EXPECTED_ASSERTIONS=33
DRILL_EXPECTED_MULTISET="active_blocks_nonzero_slots|-:1,captured_active_blocks_nonzero|-:1,captured_state_is_populated|-:1,chain_id_recorded|-:1,current_epoch_before_export|-:1,epoch_length_is_expected|-:1,export_active_blocks_classified|-:1,export_artifact_nonempty|-:1,export_height_epoch_is_3|-:1,export_height_from_artifact|-:1,export_height_is_mid_epoch|-:1,export_object_counts_match|-:1,export_open_reward_blocks_classified|-:1,export_state_matches_captured|-:1,export_succeeded|-:1,export_target_in_epoch_3|-:1,export_target_not_boundary|-:1,for_zero_height_is_inert|-:1,join_app_hash_agrees|4:1,join_node_synced|4:1,join_process_owns_home_after_sync|4:1,join_process_owns_home|4:1,join_rpc_identity_matches_home|4:1,join_started_from_empty_state|-:1,open_participation_preservation_classified|-:1,restore_outcome_classified|-:1,restore_outcome_is_not_defect|-:1,settlement_chunk_delivered|-:1,settlement_finalize_delivered|-:1,settlement_finalized|-:1,settlement_materialized|-:1,settlement_released_value|-:1,state_captured_at_export_height|-:1"
finalize_verdict
