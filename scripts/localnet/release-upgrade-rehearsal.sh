#!/usr/bin/env bash
set -uo pipefail

# Upgrades a chain launched from a PUBLISHED RELEASE to a candidate build.
#
# The existing upgrade drill proves the mechanism: it builds two binaries from one
# working tree, differing only by a build tag, and drives them through a
# coordinated halt. What it cannot prove is that the artifact operators were
# actually given can be upgraded to the artifact they will be given next — the
# two binaries there share a source revision, and the upgrade name it exercises
# (drill-v2) is compiled in for the drill and never shipped.
#
# So this runs the real thing: binary A is the released artifact, downloaded and
# verified against its published checksum; binary B is the candidate, built the
# way a release is built; and the upgrade is the production-registry name.
#
# # What the first hand-run of this taught, and what is encoded here
#
# The procedure was first executed by hand from a written document, and three of
# its defects are now closed in code rather than in prose:
#
#   The document said to point BIN at the downloaded artifact and run init.sh,
#   which rebuilds over BIN unconditionally. The published binary would have been
#   replaced by a local rebuild and the run would have PASSED while testing the
#   wrong thing. init.sh now takes LOCALNET_SKIP_BUILD, and this drill verifies
#   the artifact's hash again after setup.
#
#   The document built the candidate with a bare `go build`, producing a binary
#   reporting no version and no commit — so the artifact under test carried none
#   of the provenance the run existed to establish. The candidate is now built
#   through the release path, and its stamp is asserted.
#
#   The host carried a live node, and these scripts bind fixed ports. Running as
#   written would have bound over a port a production service depended on. Ports
#   are checked before anything starts.
#
# # Fail-closed
#
# Every assertion must be able to fail. The trap specific to an upgrade is that a
# halted node keeps its process and often keeps serving RPC, so "still alive" and
# "still answering" both read as healthy on a node that has stopped. Progress is
# therefore measured by committed height, never by liveness.

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
. "$ROOT/scripts/localnet/lib/drill-common.sh"
. "$ROOT/scripts/localnet/lib/drill-assert.sh"

NODE_COUNT="${NODE_COUNT:-4}"
RUN_ID="${RUN_ID:-$(date +%Y%m%d-%H%M%S)}"

# The release to upgrade FROM, and the upgrade name to schedule. The name must
# match an entry in the candidate's production registry, or the candidate cannot
# execute the plan it is handed.
FROM_TAG="${FROM_TAG:-v0.1.0}"
UPGRADE_NAME="${UPGRADE_NAME:-v0.2.0}"
CANDIDATE_REF="${CANDIDATE_REF:-HEAD}"

DRILL_EVID_DIR="$ROOT/build/localnet/evidence/$RUN_ID/release-upgrade"
WORK="$ROOT/build/localnet/rehearsal-$RUN_ID"

# The straggler asserts on a specific halt reason, so a node that stopped for an
# unrelated cause cannot be mistaken for the case under test.
HALT_MARKER="UPGRADE \"$UPGRADE_NAME\" NEEDED"

DRILL_MANDATORY_FILES=(binaries.json versions.json state.json halt.json assertions.jsonl summary.csv)
DRILL_VERDICT_GATES=("halt=COORDINATED" "upgrade=APPLIED" "state=PRESERVED" "straggler=HALTED")

die() { echo "rehearsal: $*" >&2; exit 2; }
sha256() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}';
           else shasum -a 256 "$1" | awk '{print $1}'; fi; }

# host_artifact names the released asset for this machine. The rehearsal is worth
# most on the platform operators run, but it must be runnable where it is
# developed too.
host_artifact() {
  local os arch
  case "$(uname -s)" in Linux) os=linux ;; Darwin) os=darwin ;; *) die "unsupported OS $(uname -s)" ;; esac
  case "$(uname -m)" in x86_64|amd64) arch=amd64 ;; arm64|aarch64) arch=arm64 ;; *) die "unsupported arch $(uname -m)" ;; esac
  echo "twilightd-$FROM_TAG-$os-$arch"
}

# ---- 0. preflight -----------------------------------------------------------

[[ -e "$DRILL_EVID_DIR" ]] && die "$DRILL_EVID_DIR already exists; use a fresh RUN_ID"
command -v jq >/dev/null 2>&1 || die "jq is required"
command -v gh >/dev/null 2>&1 || die "gh is required to fetch the published release"

require_free_ports || die "refusing to run"

drill_assert_init "$DRILL_EVID_DIR" || die "could not initialise evidence"
mkdir -p "$WORK"

# ---- 1. binaries ------------------------------------------------------------

echo "==> fetching the published $FROM_TAG artifact"
ARTIFACT="$(host_artifact)"
gh release download "$FROM_TAG" --pattern "$ARTIFACT" --pattern SHA256SUMS --dir "$WORK" --clobber \
  >/dev/null 2>&1 || die "could not download $ARTIFACT from release $FROM_TAG"

BIN_A="$WORK/$ARTIFACT"
chmod +x "$BIN_A"

# The published checksum is the whole reason to download rather than rebuild: it
# is what makes this the artifact operators hold, not a local approximation of it.
PUBLISHED_SHA="$(awk -v a="$ARTIFACT" '$2 == a { print $1 }' "$WORK/SHA256SUMS")"
[[ -n "$PUBLISHED_SHA" ]] || die "$ARTIFACT is absent from the published SHA256SUMS"

phase_begin
expect "released_artifact_matches_published_checksum" "$PUBLISHED_SHA" "$(sha256 "$BIN_A")"
expect "released_artifact_reports_its_version" "$FROM_TAG" \
  "$("$BIN_A" version --long 2>/dev/null | awk -F': *' '$1=="version" {print $2}')"

echo "==> building the candidate from $CANDIDATE_REF through the release path"
CANDIDATE_COMMIT="$(git -C "$ROOT" rev-parse "$CANDIDATE_REF")"
# Built the way a release is built, not with a bare `go build`. A candidate that
# reports no version and no commit carries none of the provenance this run exists
# to establish, and would be a different artifact from the one that ships.
RELEASE_DIR="build/localnet/rehearsal-$RUN_ID/release" VERSION="$UPGRADE_NAME" \
  make -C "$ROOT" build-release >"$DRILL_EVID_DIR/candidate-build.log" 2>&1 \
  || die "candidate build failed; see $DRILL_EVID_DIR/candidate-build.log"

BIN_B="$WORK/release/twilightd-$UPGRADE_NAME-$(uname -s | tr 'A-Z' 'a-z')-$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/')"
[[ -x "$BIN_B" ]] || die "the release build produced no artifact for this host at $BIN_B"

expect "candidate_reports_the_upgrade_version" "$UPGRADE_NAME" \
  "$("$BIN_B" version --long 2>/dev/null | awk -F': *' '$1=="version" {print $2}')"
expect "candidate_reports_its_commit" "$CANDIDATE_COMMIT" \
  "$("$BIN_B" version --long 2>/dev/null | awk -F': *' '$1=="commit" {print $2}')"

jq -n --arg tag "$FROM_TAG" --arg art "$ARTIFACT" --arg sa "$(sha256 "$BIN_A")" \
      --arg pub "$PUBLISHED_SHA" --arg name "$UPGRADE_NAME" --arg c "$CANDIDATE_COMMIT" \
      --arg sb "$(sha256 "$BIN_B")" \
  '{from: {tag: $tag, artifact: $art, sha256: $sa, published_sha256: $pub},
    to: {upgrade_name: $name, commit: $c, sha256: $sb}}' >"$DRILL_EVID_DIR/binaries.json"
phase_end "binaries" "from $FROM_TAG ($ARTIFACT) to $UPGRADE_NAME ($CANDIDATE_COMMIT)"

# ---- 2. launch on the released binary ---------------------------------------

echo "==> launching $NODE_COUNT validators on $FROM_TAG"
phase_begin
# LOCALNET_SKIP_BUILD is what stops init.sh replacing the artifact with a rebuild.
# The genesis is authored by the RELEASED binary, which is the state the upgrade
# has to carry across.
BIN="$BIN_A" LOCALNET_SKIP_BUILD=1 NODE_COUNT="$NODE_COUNT" \
  "$ROOT/scripts/localnet/init.sh" >"$DRILL_EVID_DIR/setup.log" 2>&1 || die "init failed"

# Re-verified AFTER setup, because the failure being guarded against is silent:
# a rebuild would leave a working binary of the right name and the wrong bytes.
expect "released_artifact_survived_setup" "$PUBLISHED_SHA" "$(sha256 "$BIN_A")"

BIN="$BIN_A" NODE_COUNT="$NODE_COUNT" "$ROOT/scripts/localnet/start.sh" >>"$DRILL_EVID_DIR/setup.log" 2>&1
trap 'for n in $(seq 0 $((NODE_COUNT - 1))); do stop_node "$n"; done' EXIT
wait_all_height 5 || die "the released binary did not reach height 5"
phase_end "launch" "four validators producing on $FROM_TAG"

# ---- 3. pre-upgrade state ---------------------------------------------------

module_version() { # <binary> <module>
  "$1" query upgrade module-versions --node "$(rpc_url 0)" -o json 2>/dev/null \
    | jq -r --arg m "$2" '.module_versions[] | select(.name==$m) | .version' 2>/dev/null
}
coreslot_state() { # <binary> -> canonical params+slots
  { "$1" coreslot-query params --node "$(rpc_url 0)" -o json 2>/dev/null
    "$1" coreslot-query slots  --node "$(rpc_url 0)" -o json 2>/dev/null; } | jq -Sc .
}

phase_begin
expect "coreslot_starts_at_the_released_version" "1" "$(module_version "$BIN_A" coreslot)"
STATE_BEFORE="$(coreslot_state "$BIN_A")"
expect "pre_upgrade_state_is_readable" "true" "$([[ -n "$STATE_BEFORE" ]] && echo true || echo false)"
phase_end "pre-state" "coreslot at consensus version 1"

# ---- 4. schedule -------------------------------------------------------------

H="$(latest_height 0)"
UPGRADE_HEIGHT=$((H + 20))
echo "==> scheduling $UPGRADE_NAME at height $UPGRADE_HEIGHT (now $H)"
phase_begin
# Scheduled BY THE RELEASED BINARY, which does not carry the handler. That is the
# design, not a shortcut: the binary that schedules an upgrade is never the one
# that executes it, and a chain that could only schedule upgrades it already knew
# how to run could never be upgraded at all.
BIN="$BIN_A" submit_authority schedule-upgrade "$UPGRADE_NAME" "$UPGRADE_HEIGHT" \
  "sha256:$(sha256 "$BIN_B")"
expect "schedule_accepted" "0" "$LAST_TXCODE"
sleep 4
expect "plan_names_the_upgrade" "$UPGRADE_NAME" \
  "$("$BIN_A" query upgrade plan --node "$(rpc_url 0)" -o json 2>/dev/null | jq -r '.plan.name // ""')"
expect "plan_carries_the_height" "$UPGRADE_HEIGHT" \
  "$("$BIN_A" query upgrade plan --node "$(rpc_url 0)" -o json 2>/dev/null | jq -r '.plan.height // ""')"
phase_end "schedule" "$UPGRADE_NAME at $UPGRADE_HEIGHT"

# ---- 5. the coordinated halt -------------------------------------------------

echo "==> waiting for the halt at $UPGRADE_HEIGHT"
phase_begin
for _ in $(seq 1 60); do
  done_count=0
  for ((n = 0; n < NODE_COUNT; n++)); do
    grep -qF "$HALT_MARKER" "$NET/logs/node$n.log" 2>/dev/null && done_count=$((done_count + 1))
  done
  (( done_count == NODE_COUNT )) && break
  sleep 3
done

halted_at=()
for ((n = 0; n < NODE_COUNT; n++)); do
  expect "node_halted_for_the_upgrade" "yes" \
    "$(grep -qF "$HALT_MARKER" "$NET/logs/node$n.log" 2>/dev/null && echo yes || echo no)" "$n"
  halted_at+=("$(latest_height "$n")")
done

# Every node must stop at the SAME height. Nodes halting at different heights is
# the failure this whole exercise is for: it means the network disagreed about
# where the boundary was.
uniq_heights="$(printf '%s\n' "${halted_at[@]}" | sort -u | wc -l | tr -d ' ')"
expect "all_nodes_halted_at_one_height" "1" "$uniq_heights"
expect "halt_height_is_the_scheduled_one" "$UPGRADE_HEIGHT" "${halted_at[0]}"

jq -n --arg scheduled "$UPGRADE_HEIGHT" --argjson at "$(printf '%s\n' "${halted_at[@]}" | jq -Rn '[inputs|tonumber]')" \
  '{scheduled_height: ($scheduled|tonumber), halted_at: $at}' >"$DRILL_EVID_DIR/halt.json"
phase_end "halt" "all $NODE_COUNT nodes stopped at ${halted_at[0]}"

# ---- 6. swap and resume ------------------------------------------------------

echo "==> swapping every node to the candidate"
phase_begin
for ((n = 0; n < NODE_COUNT; n++)); do stop_node "$n"; done
sleep 4
for ((n = 0; n < NODE_COUNT; n++)); do eval "export NODE_BIN_$n=\"$BIN_B\""; start_node "$n"; done

# Past the boundary, not merely at it: a node that came up and stopped again would
# satisfy "reached the height" while the chain was dead.
wait_all_height $((UPGRADE_HEIGHT + 3)) || die "the network did not resume past the upgrade height"
sleep 6
expect "chain_is_still_producing" "true" \
  "$([[ "$(latest_height 0)" -gt $((UPGRADE_HEIGHT + 3)) ]] && echo true || echo false)"
phase_end "resume" "network past $UPGRADE_HEIGHT on the candidate"

# ---- 7. the boundary was crossed --------------------------------------------

phase_begin
expect "coreslot_advanced_to_version_2" "2" "$(module_version "$BIN_B" coreslot)"
expect "upgrade_is_recorded_as_applied" "$UPGRADE_HEIGHT" \
  "$("$BIN_B" query upgrade applied "$UPGRADE_NAME" --node "$(rpc_url 0)" -o json 2>/dev/null | jq -r '.height // ""')"

# The migration is a deliberate state no-op, so any difference here is a defect
# rather than a success.
expect "coreslot_state_survived_unchanged" "$STATE_BEFORE" "$(coreslot_state "$BIN_B")"

# The surface the upgrade exists to deliver.
expect "rotation_commands_are_present" "0" \
  "$("$BIN_B" tx coreslot nominate-authority --help >/dev/null 2>&1; echo $?)"

"$BIN_B" query upgrade module-versions --node "$(rpc_url 0)" -o json >"$DRILL_EVID_DIR/versions.json" 2>/dev/null
coreslot_state "$BIN_B" >"$DRILL_EVID_DIR/state.json"
phase_end "boundary" "coreslot 1 -> 2, state unchanged"

# ---- 8. the straggler --------------------------------------------------------

echo "==> restarting node $((NODE_COUNT - 1)) on the OLD binary"
phase_begin
LAST=$((NODE_COUNT - 1))
stop_node "$LAST"; sleep 3
: >"$NET/logs/node$LAST.log"
eval "export NODE_BIN_$LAST=\"$BIN_A\""
start_node "$LAST"
sleep 20

# It must refuse, and refuse for the RIGHT reason.
expect "straggler_refuses_to_follow" "yes" \
  "$(grep -qiE 'wrong app version|UPGRADE .* NEEDED' "$NET/logs/node$LAST.log" && echo yes || echo no)"

straggler_h="$(latest_height "$LAST")"
sleep 10
expect "straggler_makes_no_progress" "$straggler_h" "$(latest_height "$LAST")"

# And the rest of the network is unaffected: three of four is still above the
# two-thirds quorum, which is why this is run with four validators.
expect "quorum_keeps_producing" "true" \
  "$([[ "$(latest_height 0)" -gt "$straggler_h" ]] && echo true || echo false)"

# #154: a halted node commonly keeps its process and keeps answering RPC. Pinned
# deliberately — operators monitor liveness, and a stopped node that still reports
# catching_up=false is exactly what makes that monitoring lie.
expect "halted_straggler_still_serves_rpc" "true" \
  "$(rpc_get "$LAST" /status >/dev/null 2>&1 && echo true || echo false)"
phase_end "straggler" "node $LAST halted at $straggler_h while the network continued"

# ---- verdict -----------------------------------------------------------------

DRILL_VERDICT_LINES=(
  "halt=$([[ "$uniq_heights" == "1" ]] && echo COORDINATED || echo SPLIT)"
  "upgrade=$([[ "$(module_version "$BIN_B" coreslot)" == "2" ]] && echo APPLIED || echo NOT_APPLIED)"
  "state=$([[ "$(coreslot_state "$BIN_B")" == "$STATE_BEFORE" ]] && echo PRESERVED || echo CHANGED)"
  "straggler=$([[ "$(latest_height "$LAST")" -le "$((UPGRADE_HEIGHT + 1))" ]] && echo HALTED || echo FOLLOWED)"
  "from=$FROM_TAG"
  "to=$CANDIDATE_COMMIT"
)
finalize_verdict
