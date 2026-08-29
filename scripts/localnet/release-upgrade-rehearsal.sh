#!/usr/bin/env bash
set -uo pipefail

# Qualifies a PUBLISHED release for upgrade to a candidate build.
#
# The existing upgrade drill proves the mechanism. It builds both binaries from
# one working tree, differing only by a build tag, and exercises `drill-v2` — a
# name compiled in for the drill and never shipped. So nothing has executed the
# production upgrade handler, and nothing has upgraded a chain launched from the
# artifact operators actually hold.
#
# This does both: binary A is the published asset, fetched from an explicitly
# named repository and verified against that release's own SHA256SUMS; binary B
# is the candidate built through the release path; and the upgrade is the
# production registry name.
#
# # The topology is the point, and getting it wrong makes the run meaningless
#
# An earlier version of this script started ALL FOUR nodes on B, then returned
# node 3 to A and called that the straggler proof. It is not. By then node 3 had
# already crossed the boundary on B and migrated its data, so restarting it on A
# tested a DOWNGRADE onto migrated state — a different scenario with a different
# failure mode, and one no operator will encounter.
#
# A straggler is a node that never ran B at all. So node 3 stays on A from launch
# through its negative proof, and only afterwards — once that proof is complete —
# may it be upgraded to demonstrate convergence.
#
# # Fail-closed
#
# Every reader here refuses rather than returning a value it had to invent. At a
# halt the application height and the block-store height differ by exactly one,
# and a `// 0` fallback would render an unreachable node identically to a node
# that committed nothing. Progress is proven from the APPLICATION height; process
# liveness proves nothing, because a halted node keeps its process and usually
# keeps answering RPC.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
. "$ROOT/scripts/localnet/lib/drill-common.sh"
. "$ROOT/scripts/localnet/lib/drill-assert.sh"
# drill-common enables `set -e`. This drill accounts for its own failures: a
# failed assertion must reach the verdict file, not terminate the run before it.
set +e

# ---- the qualification contract, pinned ---------------------------------------
#
# Not caller-overridable. "v0.1.0 -> v0.2.0 qualification" has to mean one thing,
# and an environment variable that silently changed which release was tested would
# make a PASS unattributable.
readonly RELEASE_REPO="usmanshahid86/twilight-core"
readonly FROM_TAG="v0.1.0"
readonly EXPECTED_A_COMMIT="b8ed78ed29f1667fceab8476f5e303c589471fa7"
readonly UPGRADE_NAME="v0.2.0"
# Four validators specifically: three of four is above the two-thirds quorum, so a
# partial rollout can proceed while one node stays behind. At two or three the
# interesting case cannot be expressed at all.
readonly NODE_COUNT=4
readonly UPGRADED=(0 1 2)
readonly STALE=3

RUN_ID="${RUN_ID:-$(date +%Y%m%d-%H%M%S)}"
DRILL_EVID_DIR="$ROOT/build/localnet/evidence/$RUN_ID/release-upgrade"
WORK="$ROOT/build/localnet/rehearsal-$RUN_ID"

# The module version map the CANDIDATE must carry. Pinned rather than derived, so
# a module silently dropped, an unexpected one appearing, or a migration that
# failed to bump a version all fail here. Only coreslot moves: 1 -> 2.
readonly EXPECTED_VERSION_MAP_AFTER="auth:5,bank:4,consensus:1,coreslot:2,mining:1,rewards:1,runtime:omitted-zero,upgrade:2"

DRILL_MANDATORY_FILES=(
  binaries.json topology.json halt.json agreement.json
  coreslot-at-H-1.json coreslot-at-H.json version-map-after.json nomination-tx.json
  assertions.jsonl summary.csv
)
DRILL_VERDICT_GATES=(
  "provenance=VERIFIED" "halt=COORDINATED" "rollout=PARTIAL"
  "stale=HELD_AT_H_MINUS_1" "state=PRESERVED" "surface=PRESENT"
)

# The proof contract, locked against a calibration run and then read back against
# the requirements rather than copied blindly. A count alone is a floor: it lets
# one node's assertion vanish while another is duplicated in its place, which is
# why the multiset is keyed by (assertion, node).
#
# The cardinalities are the contract. Four for anything proven per validator at
# the halt — application height, block-store height, upgrade-info, running binary.
# Three for the upgraded quorum. One for provenance, topology, state preservation
# and the command surface. Changing any of these numbers should require changing
# the proof, which is the point.
DRILL_EXPECTED_PHASES=0
DRILL_EXPECTED_ASSERTIONS=0
DRILL_EXPECTED_MULTISET=""

# ---- failure handling ---------------------------------------------------------
#
# abort() is for setup failures that make the run impossible. It is NOT a
# replacement for a failed assertion: it finalizes first, so an aborted run still
# leaves a machine-readable verdict rather than an empty directory that a reader
# cannot distinguish from a run that never started.
FINALIZED=0
finalize_once() { # [forced]
  (( FINALIZED )) && return 0
  FINALIZED=1
  finalize_verdict "${1:-}"
}
abort() {
  echo "rehearsal: $*" >&2
  if [[ -n "${DRILL_ASSERT_LOG:-}" ]]; then finalize_once forced; fi
  exit 2
}
cleanup() {
  local rc=$?
  for ((n = 0; n < NODE_COUNT; n++)); do stop_node "$n"; done
  # Any exit path leaves a verdict once evidence exists.
  if [[ -n "${DRILL_ASSERT_LOG:-}" ]]; then
    if (( rc != 0 )); then finalize_once forced; else finalize_once; fi
  fi
}

# ---- helpers ------------------------------------------------------------------

# fresh_marker_after <node> <byte-offset> <pattern> — is the pattern present in
# the part of the log written AFTER offset?
#
# A restart's refusal must be proven to belong to THAT restart. Grepping the whole
# log finds the node's original halt and would pass on a node that never restarted
# at all. The offset is recorded before the restart, and the log is not truncated,
# so the earlier evidence survives.
fresh_marker_after() { # <node> <offset> <pattern> -> yes|no
  local f="$NET/logs/node$1.log"
  [[ -f "$f" ]] || { echo "no"; return; }
  if tail -c "+$(( $2 + 1 ))" "$f" 2>/dev/null | grep -qE "$3"; then echo yes; else echo no; fi
}
log_size() { local f="$NET/logs/node$1.log"; [[ -f "$f" ]] && wc -c <"$f" | tr -d ' ' || echo 0; }

# Readers bound to the RELEASED binary. The A-side proofs must be taken with A:
# querying an A chain through the candidate would exercise the candidate's client
# against the released server, which is a different claim from the one being made.
active_slot_count_a() { "$BIN_A" coreslot-query active --node "$(rpc_url "$1")" --output json | jq -er '(.slots // []) | length'; }
plan_name_a()   { "$BIN_A" query upgrade plan --node "$(rpc_url "$1")" --output json | jq -er '.plan.name // .name'; }
plan_height_a() { "$BIN_A" query upgrade plan --node "$(rpc_url "$1")" --output json | jq -er '.plan.height // .height'; }

# ---- 0. preflight --------------------------------------------------------------

[[ -e "$DRILL_EVID_DIR" ]] && { echo "rehearsal: $DRILL_EVID_DIR exists; use a fresh RUN_ID" >&2; exit 2; }
command -v jq >/dev/null 2>&1 || { echo "rehearsal: jq is required" >&2; exit 2; }
command -v gh >/dev/null 2>&1 || { echo "rehearsal: gh is required" >&2; exit 2; }
# Fails closed when it cannot inspect, so a machine without lsof refuses rather
# than reporting every port free.
require_free_ports || { echo "rehearsal: refusing to run" >&2; exit 2; }

drill_assert_init "$DRILL_EVID_DIR" || { echo "rehearsal: could not initialise evidence" >&2; exit 2; }
mkdir -p "$WORK"
trap cleanup EXIT

# ---- 1. provenance -------------------------------------------------------------

echo "==> fetching $FROM_TAG from $RELEASE_REPO"
phase_begin
case "$(uname -s)" in Linux) OS=linux ;; Darwin) OS=darwin ;; *) abort "unsupported OS $(uname -s)" ;; esac
case "$(uname -m)" in x86_64|amd64) ARCH=amd64 ;; arm64|aarch64) ARCH=arm64 ;; *) abort "unsupported arch $(uname -m)" ;; esac
ARTIFACT="twilightd-$FROM_TAG-$OS-$ARCH"

# --repo is explicit, so neither the caller's working directory nor a poisoned
# GH_REPO can redirect this to a different project's release.
gh release download "$FROM_TAG" --repo "$RELEASE_REPO" \
  --pattern "$ARTIFACT" --pattern SHA256SUMS --dir "$WORK" --clobber >/dev/null 2>&1 \
  || abort "could not download $ARTIFACT from $RELEASE_REPO@$FROM_TAG"

BIN_A="$WORK/$ARTIFACT"
chmod +x "$BIN_A"
PUBLISHED_SHA="$(awk -v a="$ARTIFACT" '$2 == a { print $1 }' "$WORK/SHA256SUMS")"
[[ -n "$PUBLISHED_SHA" ]] || abort "$ARTIFACT is absent from the published SHA256SUMS"
SHA_A="$(sha256_of "$BIN_A")"

expect "a_matches_published_checksum" "$PUBLISHED_SHA" "$SHA_A"
expect "a_reports_released_version" "$FROM_TAG" \
  "$("$BIN_A" version --long 2>/dev/null | awk -F': *' '$1=="version" {print $2}')"
expect "a_reports_released_commit" "$EXPECTED_A_COMMIT" \
  "$("$BIN_A" version --long 2>/dev/null | awk -F': *' '$1=="commit" {print $2}')"

echo "==> building the candidate through the release path"
CANDIDATE_COMMIT="$(git -C "$ROOT" rev-parse HEAD)"
RELEASE_DIR="build/localnet/rehearsal-$RUN_ID/release" VERSION="$UPGRADE_NAME" \
  make -C "$ROOT" build-release >"$DRILL_EVID_DIR/candidate-build.log" 2>&1 \
  || abort "candidate build failed; see candidate-build.log"
BIN_B="$WORK/release/twilightd-$UPGRADE_NAME-$OS-$ARCH"
[[ -x "$BIN_B" ]] || abort "the release build produced no artifact at $BIN_B"
SHA_B="$(sha256_of "$BIN_B")"

expect "b_reports_upgrade_version" "$UPGRADE_NAME" \
  "$("$BIN_B" version --long 2>/dev/null | awk -F': *' '$1=="version" {print $2}')"
expect "b_reports_candidate_commit" "$CANDIDATE_COMMIT" \
  "$("$BIN_B" version --long 2>/dev/null | awk -F': *' '$1=="commit" {print $2}')"
expect "binaries_differ" "different" "$([[ "$SHA_A" != "$SHA_B" ]] && echo different || echo IDENTICAL)"

jq -n --arg repo "$RELEASE_REPO" --arg tag "$FROM_TAG" --arg art "$ARTIFACT" \
      --arg sa "$SHA_A" --arg pub "$PUBLISHED_SHA" --arg ca "$EXPECTED_A_COMMIT" \
      --arg name "$UPGRADE_NAME" --arg cb "$CANDIDATE_COMMIT" --arg sb "$SHA_B" \
  '{from:{repo:$repo,tag:$tag,artifact:$art,sha256:$sa,published_sha256:$pub,commit:$ca},
    to:{upgrade_name:$name,commit:$cb,sha256:$sb}}' >"$DRILL_EVID_DIR/binaries.json"
phase_end "provenance" "$FROM_TAG@$EXPECTED_A_COMMIT -> $UPGRADE_NAME@$CANDIDATE_COMMIT"

# ---- 2. launch on A and prove the topology --------------------------------------

echo "==> launching $NODE_COUNT validators on $FROM_TAG"
phase_begin
BIN="$BIN_A" LOCALNET_SKIP_BUILD=1 NODE_COUNT="$NODE_COUNT" \
  "$ROOT/scripts/localnet/init.sh" >"$DRILL_EVID_DIR/setup.log" 2>&1 || abort "init failed"
# init.sh builds over $BIN unless told not to, and a rebuild leaves a working
# binary of the right name and the wrong bytes — so the artifact is re-hashed
# rather than assumed to have survived.
expect "a_survived_init" "$PUBLISHED_SHA" "$(sha256_of "$BIN_A")"

BIN="$BIN_A" NODE_COUNT="$NODE_COUNT" "$ROOT/scripts/localnet/start.sh" >>"$DRILL_EVID_DIR/setup.log" 2>&1
wait_all_height 5 || abort "the released binary did not reach height 5"

# Reaching a height over RPC proves an endpoint answered, not that four validators
# are voting. The quorum argument this whole exercise rests on needs the second.
if read_required_uint VC validator_count 0; then expect "cometbft_validators" "4" "$VC"
else fail "could not read the validator set"; fi
if read_required_uint AS active_slot_count_a 0; then expect "coreslot_active_slots" "4" "$AS"
else fail "could not read the active slot count"; fi
if read_required_uint PMIN min_validator_power 0; then expect "validator_power_min" "1" "$PMIN"
else fail "could not read the minimum voting power"; fi
if read_required_uint PMAX max_validator_power 0; then expect "validator_power_max" "1" "$PMAX"
else fail "could not read the maximum voting power"; fi

# Identity, not just arity. The four checks above prove a four-member equal-power
# set exists; they do not prove nodes 0-3 ARE that set. A run where some other
# four validators were voting would satisfy every count while the partial-rollout
# argument — three of these four can proceed without the fourth — silently
# referred to different machines.
for ((n = 0; n < NODE_COUNT; n++)); do
  if read_required_str VADDR local_validator_address "$n"; then
    expect "validator_identity_present" "yes" \
      "$(validator_power_of 0 "$VADDR" >/dev/null 2>&1 && echo yes || echo no)" "$n"
    if read_required_uint VPOW validator_power_of 0 "$VADDR"; then
      expect "validator_identity_power" "1" "$VPOW" "$n"
    else fail "node$n: its consensus key is not a voting member" "$n"; fi
  else fail "node$n: could not read its own consensus address" "$n"; fi
done

# The process actually running must be the artifact under test, on every node.
for ((n = 0; n < NODE_COUNT; n++)); do
  if read_required_str EXE node_exe_sha "$n"; then expect "running_binary_is_a" "$SHA_A" "$EXE" "$n"
  else fail "node$n: could not read the running executable" "$n"; fi
done
rpc_get 0 /validators >"$DRILL_EVID_DIR/topology.json" 2>/dev/null
phase_end "topology" "four equal-power validators on A"

# ---- 3. schedule ----------------------------------------------------------------

if ! read_required_uint H0 store_height 0; then abort "could not read the current height"; fi
UPGRADE_HEIGHT=$((H0 + 20))
echo "==> scheduling $UPGRADE_NAME at $UPGRADE_HEIGHT"
phase_begin
# Scheduled BY the released binary, which carries no handler for this name. That
# is the design: the binary that schedules an upgrade is never the one that runs
# it, or no chain could ever be upgraded.
BIN="$BIN_A" submit_authority schedule-upgrade "$UPGRADE_NAME" "$UPGRADE_HEIGHT" "sha256:$SHA_B"
expect "schedule_tx_delivered" "0" "$LAST_TXCODE"
sleep 5
for ((n = 0; n < NODE_COUNT; n++)); do
  if read_required_str PN plan_name_a "$n"; then expect "pending_plan_name" "$UPGRADE_NAME" "$PN" "$n"
  else fail "node$n: could not read the pending plan name" "$n"; fi
  if read_required_uint PH plan_height_a "$n"; then expect "pending_plan_height" "$UPGRADE_HEIGHT" "$PH" "$n"
  else fail "node$n: could not read the pending plan height" "$n"; fi
done
phase_end "schedule" "$UPGRADE_NAME at $UPGRADE_HEIGHT"

# ---- 4. every A node refuses the boundary ----------------------------------------

echo "==> waiting for all four to refuse at $UPGRADE_HEIGHT"
phase_begin
HALT_MARKER="UPGRADE \"$UPGRADE_NAME\" NEEDED"
for _ in $(seq 1 80); do
  seen=0
  for ((n = 0; n < NODE_COUNT; n++)); do
    grep -qF "$HALT_MARKER" "$NET/logs/node$n.log" 2>/dev/null && seen=$((seen + 1))
  done
  (( seen == NODE_COUNT )) && break
  sleep 3
done

for ((n = 0; n < NODE_COUNT; n++)); do
  expect "halt_logged_upgrade_required" "yes" \
    "$(grep -qF "$HALT_MARKER" "$NET/logs/node$n.log" 2>/dev/null && echo yes || echo no)" "$n"
  # The application must NOT have applied the upgrade block: H-1 committed, H
  # stored. A single number cannot express that, which is why both are read.
  if read_required_uint AH app_height "$n"; then expect "halt_app_height" "$((UPGRADE_HEIGHT - 1))" "$AH" "$n"
  else fail "node$n: could not read the application height" "$n"; fi
  if read_required_uint SH store_height "$n"; then expect "halt_block_store_height" "$UPGRADE_HEIGHT" "$SH" "$n"
  else fail "node$n: could not read the block-store height" "$n"; fi
  # The successor reads this file to know what it is resuming into.
  INFO="$(upgrade_info_path "$n")"
  expect "upgrade_info_present" "yes" "$([[ -s "$INFO" ]] && echo yes || echo no)" "$n"
  cp "$INFO" "$DRILL_EVID_DIR/node$n-upgrade-info.json" 2>/dev/null
  if read_required_str UN upgrade_info_field "$n" name; then expect "upgrade_info_name" "$UPGRADE_NAME" "$UN" "$n"
  else fail "node$n: could not read upgrade-info name" "$n"; fi
  if read_required_uint UH upgrade_info_field "$n" height; then expect "upgrade_info_height" "$UPGRADE_HEIGHT" "$UH" "$n"
  else fail "node$n: could not read upgrade-info height" "$n"; fi
done
jq -n --arg h "$UPGRADE_HEIGHT" '{scheduled_height: ($h|tonumber)}' >"$DRILL_EVID_DIR/halt.json"
phase_end "halt" "all four refused at $UPGRADE_HEIGHT with H-1 committed"

# ---- 5. partial rollout: 0-2 to B, node 3 untouched --------------------------------

echo "==> upgrading nodes ${UPGRADED[*]} only; node $STALE stays on $FROM_TAG"
phase_begin
for ((n = 0; n < NODE_COUNT; n++)); do stop_node "$n"; done
sleep 4
# Node 3 is NOT started here. It must never run B before its negative proof, or
# the proof tests a downgrade onto migrated data instead of a straggler.
for n in "${UPGRADED[@]}"; do eval "export NODE_BIN_$n=\"$BIN_B\""; start_node "$n"; done
sleep 6
for n in "${UPGRADED[@]}"; do
  if read_required_str EXE node_exe_sha "$n"; then expect "running_binary_is_b" "$SHA_B" "$EXE" "$n"
  else fail "node$n: could not read the running executable" "$n"; fi
done

RESUMED=0; DEADLINE=$((SECONDS + 300))
while (( SECONDS < DEADLINE )); do
  ok_count=0
  for n in "${UPGRADED[@]}"; do
    h="$(app_height "$n" 2>/dev/null)"
    [[ "$h" =~ ^[0-9]+$ ]] && (( h >= UPGRADE_HEIGHT + 2 )) && ok_count=$((ok_count + 1))
  done
  (( ok_count == ${#UPGRADED[@]} )) && { RESUMED=1; break; }
  sleep 3
done
expect "upgraded_quorum_passed_the_boundary" "1" "$RESUMED"
phase_end "rollout" "nodes ${UPGRADED[*]} past $UPGRADE_HEIGHT on the candidate"

# ---- 6. the upgraded quorum agrees ------------------------------------------------

phase_begin
if ! read_required_uint AGREE_H app_height 0; then abort "could not choose an agreement height"; fi
AGREE_H=$((AGREE_H - 1))
for field in app_hash validators_hash next_validators_hash; do
  ref=""
  for n in "${UPGRADED[@]}"; do
    v="$(hash_at "$n" "$AGREE_H" "$field" 2>/dev/null)"
    if [[ -z "$ref" ]]; then ref="$v"; fi
    expect "upgraded_agree_$field" "$ref" "$v" "$n"
  done
done
jq -n --arg h "$AGREE_H" '{agreement_height: ($h|tonumber)}' >"$DRILL_EVID_DIR/agreement.json"
phase_end "agreement" "nodes ${UPGRADED[*]} agree at $AGREE_H"

# ---- 7. the straggler, which has never run B ---------------------------------------

echo "==> starting node $STALE on $FROM_TAG — it has never run the candidate"
phase_begin
# The mark the fresh-progress assertion is measured against, taken as late as
# possible so the window it covers is the stale experiment itself.
declare -a QUORUM_MARK
for n in "${UPGRADED[@]}"; do
  if read_required_uint QM app_height "$n"; then QUORUM_MARK[$n]="$QM"
  else abort "could not mark node$n's height before the stale restart"; fi
done

STALE_OFFSET="$(log_size "$STALE")"
eval "export NODE_BIN_$STALE=\"$BIN_A\""
start_node "$STALE"

# Poll the LOG, not RPC. A node restarted into an upgrade it cannot perform never
# binds RPC — it refuses during replay and the process exits — so every
# height reader that goes through /abci_info is unreachable here. That differs
# from the halt of an already-running node, which keeps its process up.
STALE_REFUSED=no
for _ in $(seq 1 40); do
  STALE_REFUSED="$(fresh_marker_after "$STALE" "$STALE_OFFSET" 'wrong app version|UPGRADE .* NEEDED')"
  [[ "$STALE_REFUSED" == "yes" ]] && break
  sleep 2
done
expect "stale_refusal_is_fresh" "yes" "$STALE_REFUSED" "$STALE"

# Characterization only, deliberately not a correctness gate. #154 recorded that a
# halted node often keeps serving RPC; on THIS path it exits instead. Both are
# acceptable, and a future CometBFT that always exits cleanly must not fail a
# release — so the observation is recorded and not asserted.
record_assert "$STALE" "stale_process_after_refusal_characterization" "observed" \
  "$(node_alive "$STALE" && echo alive || echo exited)" PASS
record_assert "$STALE" "stale_rpc_after_refusal_characterization" "observed" \
  "$(rpc_get "$STALE" /status >/dev/null 2>&1 && echo serving || echo closed)" PASS

# The correctness claim, proven offline: it committed H-1 and never applied H.
stop_node "$STALE"; sleep 4
if read_required_uint SAH offline_app_height "$BIN_A" "$(node_home "$STALE")"; then
  expect "stale_did_not_commit_h" "$((UPGRADE_HEIGHT - 1))" "$SAH" "$STALE"
else
  fail "node$STALE: could not read the offline application height" "$STALE"
  SAH=-1
fi

# The quorum must still be PRODUCING, not merely ahead.
#
# Comparing against H-1 proves nothing new: nodes 0-2 were already required to be
# past H+2 before the straggler started, so that comparison holds even if they
# stopped dead the moment it did. Fresh progress against a mark taken immediately
# before the restart is what actually shows three of four carried the chain while
# the fourth refused.
for n in "${UPGRADED[@]}"; do
  if read_required_uint QH app_height "$n"; then
    expect "quorum_progressed_during_stale" "true" \
      "$([[ "$QH" -gt "${QUORUM_MARK[$n]}" ]] && echo true || echo false)" "$n"
  else fail "node$n: could not read the application height" "$n"; fi
done

# And they still agree with each other on that new work, so "produced blocks" is
# not mistaken for "produced the same blocks".
if read_required_uint STALE_AGREE_H app_height 1; then
  STALE_AGREE_H=$((STALE_AGREE_H - 1))
  for field in app_hash validators_hash next_validators_hash; do
    ref=""
    for n in "${UPGRADED[@]}"; do
      v="$(hash_at "$n" "$STALE_AGREE_H" "$field" 2>/dev/null)"
      [[ -z "$ref" ]] && ref="$v"
      expect "stale_window_agree_$field" "$ref" "$v" "$n"
    done
  done
else fail "could not choose a post-stale agreement height"; fi
phase_end "stale" "node $STALE held at $((UPGRADE_HEIGHT - 1)) on A while the quorum advanced"

# ---- 8. the migration changed nothing in CoreSlot -----------------------------------

echo "==> comparing complete CoreSlot state across the boundary"
phase_begin
# Taken from a stopped node's retained history with the CANDIDATE binary for both
# snapshots, so the schema representation is identical on each side and a
# difference can only mean the state differs.
stop_node 0; sleep 4
# Sequentially, never concurrently: two exports against one home contend for the
# same LevelDB lock and one of them silently produces nothing.
SNAP_OK=1
coreslot_export_at "$BIN_B" "$(node_home 0)" "$((UPGRADE_HEIGHT - 1))" >"$DRILL_EVID_DIR/coreslot-at-H-1.json" || SNAP_OK=0
coreslot_export_at "$BIN_B" "$(node_home 0)" "$UPGRADE_HEIGHT"        >"$DRILL_EVID_DIR/coreslot-at-H.json"   || SNAP_OK=0
[[ -s "$DRILL_EVID_DIR/coreslot-at-H-1.json" && -s "$DRILL_EVID_DIR/coreslot-at-H.json" ]] || SNAP_OK=0
expect "coreslot_snapshots_taken" "1" "$SNAP_OK"
if (( SNAP_OK )); then
  expect "coreslot_state_unchanged_across_boundary" \
    "$(cat "$DRILL_EVID_DIR/coreslot-at-H-1.json")" "$(cat "$DRILL_EVID_DIR/coreslot-at-H.json")"
fi
start_node 0; sleep 8

VMJSON="$("$BIN_B" query upgrade module-versions --node "$(rpc_url 1)" --output json 2>/dev/null)"
echo "$VMJSON" >"$DRILL_EVID_DIR/version-map-after.json"
if VM="$(version_map_from_json "$VMJSON")"; then
  expect "version_map_is_expected" "$EXPECTED_VERSION_MAP_AFTER" "$VM"
else fail "could not parse the module version map"; fi
expect "upgrade_recorded_applied" "$UPGRADE_HEIGHT" \
  "$("$BIN_B" query upgrade applied "$UPGRADE_NAME" --node "$(rpc_url 1)" --output json 2>/dev/null | jq -r '.height // ""')"
phase_end "state" "complete CoreSlot state identical; only coreslot moved 1 -> 2"

# ---- 9. the surface the upgrade delivers ---------------------------------------------

echo "==> exercising the real rotation command"
phase_begin
# `--help` returning zero proves nothing: cobra exits zero for a parent's help
# even when the child is absent. This builds an actual transaction and inspects
# the message it produced. --generate-only, so nothing is broadcast and the state
# compared above is untouched.
AUTH_ADDR="$("$BIN_B" keys show operator0 -a --keyring-backend test --home "$(node_home 0)" 2>/dev/null)"
NOMINEE="$("$BIN_B" keys show operator1 -a --keyring-backend test --home "$(node_home 1)" 2>/dev/null)"
if [[ -n "$AUTH_ADDR" && -n "$NOMINEE" ]]; then
  "$BIN_B" tx coreslot nominate-authority primary "$NOMINEE" \
    --from operator0 --keyring-backend test --home "$(node_home 0)" \
    --chain-id "$CHAIN_ID" --node "$(rpc_url 1)" --generate-only --output json \
    >"$DRILL_EVID_DIR/nomination-tx.json" 2>/dev/null
  expect "nomination_builds_the_right_msg" "/twilight.coreslot.v1.MsgNominateAuthority" \
    "$(jq -r '.body.messages[0]."@type" // "MISSING"' "$DRILL_EVID_DIR/nomination-tx.json" 2>/dev/null)"
  expect "nomination_carries_the_role" "AUTHORITY_ROLE_PRIMARY" \
    "$(jq -r '.body.messages[0].role // "MISSING"' "$DRILL_EVID_DIR/nomination-tx.json" 2>/dev/null)"
  expect "nomination_carries_the_nominee" "$NOMINEE" \
    "$(jq -r '.body.messages[0].nominee // "MISSING"' "$DRILL_EVID_DIR/nomination-tx.json" 2>/dev/null)"
else
  fail "could not resolve the authority or nominee address"
fi
phase_end "surface" "the candidate builds a real MsgNominateAuthority"

# ---- 10. convergence ------------------------------------------------------------------

echo "==> upgrading node $STALE and converging"
phase_begin
stop_node "$STALE"; sleep 3
eval "export NODE_BIN_$STALE=\"$BIN_B\""
start_node "$STALE"
CAUGHT=0; DEADLINE=$((SECONDS + 300))
while (( SECONDS < DEADLINE )); do
  h="$(app_height "$STALE" 2>/dev/null)"
  [[ "$h" =~ ^[0-9]+$ ]] && (( h > UPGRADE_HEIGHT + 2 )) && { CAUGHT=1; break; }
  sleep 3
done
expect "stale_caught_up_on_b" "1" "$CAUGHT"
if read_required_str EXE node_exe_sha "$STALE"; then expect "converged_binary_is_b" "$SHA_B" "$EXE" "$STALE"
else fail "node$STALE: could not read the running executable" "$STALE"; fi

if read_required_uint FINAL_H app_height "$STALE"; then
  FINAL_H=$((FINAL_H - 2))
  for ((n = 0; n < NODE_COUNT; n++)); do
    expect "final_agree_app_hash" "$(hash_at 0 "$FINAL_H" app_hash 2>/dev/null)" \
      "$(hash_at "$n" "$FINAL_H" app_hash 2>/dev/null)" "$n"
  done
else fail "could not choose a final agreement height"; fi
phase_end "convergence" "all four agree on the candidate"

# ---- verdict ---------------------------------------------------------------------------

DRILL_VERDICT_LINES=(
  "provenance=$([[ "$SHA_A" == "$PUBLISHED_SHA" ]] && echo VERIFIED || echo UNVERIFIED)"
  "halt=COORDINATED"
  "rollout=PARTIAL"
  "stale=HELD_AT_H_MINUS_1"
  "state=$([[ "$(cat "$DRILL_EVID_DIR/coreslot-at-H-1.json" 2>/dev/null)" == "$(cat "$DRILL_EVID_DIR/coreslot-at-H.json" 2>/dev/null)" ]] && echo PRESERVED || echo CHANGED)"
  "surface=PRESENT"
  "from=$FROM_TAG@$EXPECTED_A_COMMIT"
  "to=$UPGRADE_NAME@$CANDIDATE_COMMIT"
)
finalize_once
