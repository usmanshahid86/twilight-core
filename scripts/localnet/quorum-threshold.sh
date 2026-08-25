#!/usr/bin/env bash
set -uo pipefail

# How many validators does this chain need, and how many can it lose?
#
# CometBFT commits on MORE THAN 2/3 of total voting power. This chain makes that
# arithmetic unusually clean: SlotVotingPower is a single chain parameter and
# genesis validates that every ACTIVE slot's ConsensusPower equals it, so there is
# no weighted quorum and tolerance is a function of the count alone.
#
# This measures it rather than asserting it. For each set size it starts a real
# chain, stops nodes one at a time, and records the size at which blocks stop
# advancing. The output is a table:
#
#   n   quorum needs   tolerates
#
# Two rows are worth knowing before choosing a topology, and neither is obvious:
# 3 -> 4 is the first step that buys ANY fault tolerance, and 4 -> 5 buys none —
# it adds a spare, not resilience.
#
# Deliberately no epochs, no rewards, no settlement. This is validator-set
# mechanics; mixing in economics would triple the runtime and blur which half
# failed.
#
# An offline validator is NOT removed from the set. This chain has no auto-jail,
# so a stopped node keeps its voting power and still counts against quorum —
# which is exactly what makes these thresholds worth measuring on the real thing.

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
BIN="${BIN:-$ROOT/build/twilightd}"
NET="${TWILIGHT_LOCALNET_HOME:-/tmp/twilight-quorum}"
MAX_N="${QUORUM_MAX_N:-5}"
# Seconds to watch for progress after each stop. At the localnet block rate this
# is tens of blocks — long enough that "no progress" means halted rather than
# slow.
OBSERVE_SECONDS="${QUORUM_OBSERVE_SECONDS:-12}"
# Blocks of advance that count as "still producing".
PROGRESS_BLOCKS=2
# Boot grace: how long a freshly started node may take before its RPC answers.
BOOT_SECONDS="${QUORUM_BOOT_SECONDS:-45}"
# Where the measured table is written. A study whose result lives only in a
# terminal has to be re-run to be consulted, and then it is not a reference.
REPORT="${QUORUM_REPORT:-$ROOT/docs/testing/quorum-threshold-table.md}"

export BIN NET TWILIGHT_LOCALNET_HOME="$NET"

need() { command -v "$1" >/dev/null 2>&1 || { echo "missing required command: $1" >&2; exit 2; }; }
need curl
need python3

rpc_port() { echo $((26657 + $1 * 100)); }
height_of() {
  curl -fsS "http://127.0.0.1:$(rpc_port "$1")/status" 2>/dev/null \
    | python3 -c "import json,sys;print(json.load(sys.stdin)['result']['sync_info']['latest_block_height'])" 2>/dev/null \
    || echo ""
}
validator_count() {
  curl -fsS "http://127.0.0.1:$(rpc_port "$1")/validators" 2>/dev/null \
    | python3 -c "import json,sys;print(len(json.load(sys.stdin)['result']['validators']))" 2>/dev/null \
    || echo ""
}

# Refuse to run beside another localnet: every script here binds the same ports,
# so a leftover node answers for a chain this script did not build and every
# number below would describe it instead.
if pgrep -x twilightd >/dev/null 2>&1; then
  echo "a twilightd process is already running; stop it before measuring quorum" >&2
  exit 2
fi

teardown() { "$ROOT/scripts/localnet/stop.sh" >/dev/null 2>&1 || true; pkill -f "twilightd start" >/dev/null 2>&1 || true; }
trap teardown EXIT

# wait_rpc <observer> — wait for a node's RPC to answer at all.
#
# Separated from progress on purpose. A node that has not finished booting and a
# chain that has stopped committing both look like "no height", and conflating
# them reports every set size as halted — which is exactly what a first version
# of this script did.
wait_rpc() {
  local observer="$1" deadline=$((SECONDS + BOOT_SECONDS))
  while ((SECONDS < deadline)); do
    [[ -n "$(height_of "$observer")" ]] && return 0
    sleep 1
  done
  return 1
}

# wait_producing <observer> — true if the chain advances PROGRESS_BLOCKS within
# the observation window. Assumes the observer's RPC is already answering.
wait_producing() {
  local observer="$1" start now deadline
  start="$(height_of "$observer")"
  [[ -z "$start" ]] && return 1
  deadline=$((SECONDS + OBSERVE_SECONDS))
  while ((SECONDS < deadline)); do
    now="$(height_of "$observer")"
    if [[ -n "$now" ]] && (( now - start >= PROGRESS_BLOCKS )); then return 0; fi
    sleep 1
  done
  return 1
}

declare -a ROWS=()
FAILURES=0

for (( n = 1; n <= MAX_N; n++ )); do
  echo
  echo "=== set size n=$n ==="
  teardown; sleep 1
  NODE_COUNT="$n" "$ROOT/scripts/localnet/init.sh" >/dev/null 2>&1 || { echo "init failed at n=$n" >&2; exit 1; }
  NODE_COUNT="$n" "$ROOT/scripts/localnet/start.sh" >/dev/null 2>&1 || { echo "start failed at n=$n" >&2; exit 1; }

  # Baseline: the full set must produce before any conclusion about losing one.
  if ! wait_rpc 0; then
    echo "  n=$n: node0 RPC never answered within ${BOOT_SECONDS}s" >&2
    FAILURES=$((FAILURES + 1)); continue
  fi
  if ! wait_producing 0; then
    echo "  n=$n never produced blocks with the full set — cannot measure" >&2
    FAILURES=$((FAILURES + 1)); continue
  fi
  vset="$(validator_count 0)"
  echo "  full set producing; validators in the CometBFT set: ${vset:-unknown}"
  if [[ "$vset" != "$n" ]]; then
    echo "  the CometBFT set holds ${vset:-unknown} validators, expected $n" >&2
    FAILURES=$((FAILURES + 1))
  fi

  # Stop nodes from the highest index down, so node0 stays as the observer for
  # as long as it can be one.
  tolerated=0
  for (( k = 1; k < n; k++ )); do
    victim=$((n - k))
    kill "$(cat "$NET/node$victim.pid" 2>/dev/null)" >/dev/null 2>&1 || true
    sleep 2
    if wait_producing 0; then
      tolerated=$k
      echo "  $k of $n stopped -> still producing"
    else
      echo "  $k of $n stopped -> HALTED"
      break
    fi
  done

  # n=1 has no measurable stop: taking the only node down leaves nobody to ask.
  if (( n == 1 )); then
    echo "  (one validator: stopping it leaves no observer, so tolerance is 0 by construction)"
  fi

  needs=$(( n - tolerated ))
  expected=$(( (n - 1) / 3 ))
  status="ok"
  if (( tolerated != expected )); then
    status="MISMATCH (expected $expected)"
    FAILURES=$((FAILURES + 1))
  fi
  ROWS+=("$(printf '| %-3s | %-13s | %-9s | %s |' "$n" "$needs" "$tolerated" "$status")")
done

teardown

echo
echo "=============== quorum threshold table ==============="
printf '| %-3s | %-13s | %-9s | %s |\n' "n" "quorum needs" "tolerates" "status"
printf '|-----|---------------|-----------|--------|\n'
for r in ${ROWS[@]+"${ROWS[@]}"}; do echo "$r"; done
echo "======================================================"
echo
echo "Measured on a real chain: an offline validator keeps its voting power"
echo "(no auto-jail), so 'tolerates' counts nodes that can be down while the"
echo "remaining set still commits blocks."

# Write the table where it can be read without re-running the study.
{
  echo "# Quorum threshold table"
  echo
  echo "Measured by \`scripts/localnet/quorum-threshold.sh\` (\`make localnet-quorum-table\`)."
  echo "Regenerate rather than edit: every number here comes from a real chain that was"
  echo "started, degraded a node at a time, and observed."
  echo
  echo "CometBFT commits on **more than 2/3** of total voting power. This chain makes that"
  echo "arithmetic clean: \`SlotVotingPower\` is a single chain parameter and genesis validates"
  echo "that every ACTIVE slot's \`ConsensusPower\` equals it, so there is no weighted quorum"
  echo "and tolerance depends on the count alone."
  echo
  printf '| %s | %s | %s |\n' "validators (n)" "quorum needs" "tolerates offline"
  printf '|---|---|---|\n'
  for r in ${ROWS[@]+"${ROWS[@]}"}; do
    echo "$r" | awk -F'|' '{printf "| %s | %s | %s |\n", $2, $3, $4}'
  done
  echo
  echo "## What the table says"
  echo
  echo "- **3 to 4 is the first step that buys any fault tolerance.** A three-validator set"
  echo "  survives nothing; losing one halts it exactly as losing one of two does."
  echo "- **4 to 5 buys none.** It adds a spare, not resilience — both tolerate one loss."
  echo "  The next gain is at seven."
  echo "- **An offline validator is not removed from the set.** This chain has no auto-jail,"
  echo "  so a stopped node keeps its voting power and still counts against quorum. Leaving"
  echo "  and being switched off are opposite for quorum while looking identical from outside."
  echo
  echo "## Reading it for a deployment"
  echo
  echo "A two-validator network — the current devnet shape — tolerates nothing: either node"
  echo "stopping halts the chain until it returns. That is correct behaviour rather than a"
  echo "fault, and it is worth planning for as a normal event."
} > "$REPORT"
echo
echo "table written to ${REPORT#$ROOT/}"

if (( FAILURES > 0 )); then
  echo
  echo "quorum threshold study: FAIL ($FAILURES discrepancies)" >&2
  exit 1
fi
echo
echo "quorum threshold study: PASS"
