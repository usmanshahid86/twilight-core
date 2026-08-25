#!/usr/bin/env bash
set -uo pipefail

# The four ways a validator leaves, and the guards that stop the set being
# emptied.
#
# Offline, inactivated, suspended and removed look similar from outside and are
# four different code paths with different consequences. The first surprises
# people: this chain has NO AUTO-JAIL, so a node that is merely switched off
# keeps its voting power and still counts against quorum. Leaving and being
# unreachable are opposite for liveness while looking identical from a distance.
#
# It also exercises the guards on the way down. x/coreslot enforces a hard floor
# of one active validator in all cases, and a policy floor at MinActiveSlots.
# What neither guard does is protect LIVENESS: with the shipped defaults both
# floors sit at 1, so a set can be walked from four down to two through entirely
# legal steps and arrive one failure away from a halt with nothing objecting.
# That is recorded as measured behaviour, not asserted as right or wrong.
#
# No epochs, no rewards, no settlement.

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
. "$ROOT/scripts/localnet/lib/drill-common.sh"

export TWILIGHT_LOCALNET_TIMEOUT_COMMIT="${DEPARTURES_TIMEOUT_COMMIT:-500ms}"
PROGRESS_BLOCKS=2
FAILURES=0
fail() { echo "  FAIL: $*" >&2; FAILURES=$((FAILURES + 1)); }
ok()   { echo "  ok: $*"; }
note() { echo "  note: $*"; }

if pgrep -x twilightd >/dev/null 2>&1; then
  echo "a twilightd process is already running; stop it first" >&2
  exit 2
fi
trap '"$ROOT/scripts/localnet/stop.sh" >/dev/null 2>&1 || true; pkill -f "twilightd start" >/dev/null 2>&1 || true' EXIT

cometbft_set() { rpc_get "${1:-0}" /validators 2>/dev/null | jq -r '(.result.validators // []) | length' 2>/dev/null || echo ""; }
wait_producing() {
  local obs="$1" start now deadline
  start="$(latest_height "$obs")"; [[ -z "$start" ]] && return 1
  deadline=$((SECONDS + 20))
  while ((SECONDS < deadline)); do
    now="$(latest_height "$obs")"
    [[ -n "$now" ]] && (( now - start >= PROGRESS_BLOCKS )) && return 0
    sleep 1
  done
  return 1
}
wait_rpc() { local d=$((SECONDS + 45)); while ((SECONDS < d)); do [[ -n "$(latest_height "$1")" ]] && return 0; sleep 1; done; return 1; }

echo "=== a four-validator set ==="
NODE_COUNT=4 "$ROOT/scripts/localnet/init.sh" >/dev/null 2>&1 || { echo "init failed" >&2; exit 1; }
NODE_COUNT=4 "$ROOT/scripts/localnet/start.sh" >/dev/null 2>&1 || { echo "start failed" >&2; exit 1; }
wait_rpc 0 || { echo "node0 never answered" >&2; exit 1; }
wait_producing 0 || { echo "the full set must produce before anything is taken away" >&2; exit 1; }
[[ "$(active_count)" == "4" && "$(cometbft_set 0)" == "4" ]] && ok "4 active, 4 in the CometBFT set" \
  || fail "expected 4 active and 4 in the set, got $(active_count)/$(cometbft_set 0)"

echo
echo "--- 1. OFFLINE: power retained, still counted against quorum ---"
stop_node 3
sleep 3
wait_producing 0 && ok "3 of 4 online still produces" || fail "3 of 4 should still produce"
[[ "$(slot_status 4)" == "SLOT_STATUS_ACTIVE" ]] && ok "the offline slot is still ACTIVE (no auto-jail)" \
  || fail "offline slot 4 status is $(slot_status 4), expected ACTIVE"
[[ "$(slot_power 4)" == "$(slot_voting_power)" ]] && ok "the offline slot keeps its voting power" \
  || fail "offline slot power is $(slot_power 4)"
[[ "$(cometbft_set 0)" == "4" ]] && ok "it is still in the CometBFT set" || fail "set is $(cometbft_set 0), expected 4"
start_node 3; wait_rpc 3 >/dev/null 2>&1; sleep 3
ok "node3 restarted"

echo
echo "--- 2. INACTIVATED: an authority transaction removes it from the set ---"
submit_authority inactivate 4 "maintenance" >/dev/null 2>&1
[[ "$LAST_TXCODE" == "0" ]] || fail "inactivate was refused (code $LAST_TXCODE)"
sleep 3
[[ "$(active_count)" == "3" ]] && ok "3 active after inactivate" || fail "active is $(active_count), expected 3"
[[ "$(cometbft_set 0)" == "3" ]] && ok "the CometBFT set followed to 3" || fail "set is $(cometbft_set 0), expected 3"
[[ "$(slot_power 4)" == "0" ]] && ok "its consensus power is zero" || fail "power is $(slot_power 4), expected 0"
wait_producing 0 && ok "the chain kept producing across the change" || fail "production stopped after inactivate"

echo
echo "--- 3. the guards on the way down ---"
submit_authority inactivate 3 "walking down" >/dev/null 2>&1
[[ "$LAST_TXCODE" == "0" ]] || fail "inactivate to 2 was refused (code $LAST_TXCODE)"
sleep 3
[[ "$(active_count)" == "2" ]] && note "walked 4 -> 2 through legal steps; no guard objected" \
  || fail "active is $(active_count), expected 2"
note "the set is now one failure from a halt, and nothing prevented reaching it"

submit_authority inactivate 2 "to the floor" >/dev/null 2>&1
[[ "$LAST_TXCODE" == "0" ]] || fail "inactivate to 1 was refused (code $LAST_TXCODE)"
sleep 3
[[ "$(active_count)" == "1" ]] && ok "reached one active validator" || fail "active is $(active_count), expected 1"

submit_authority inactivate 1 "the last one" >/dev/null 2>&1
[[ "$LAST_TXCODE" != "0" ]] && ok "inactivating the last validator is refused (code $LAST_TXCODE)" \
  || fail "the last active validator was inactivated — the set can be emptied"
[[ "$(active_count)" == "1" ]] && ok "still one active" || fail "active is $(active_count) after a refused call"

echo
echo "--- 4. reactivation restores the set ---"
for s in 2 3 4; do
  submit_authority activate "$s" >/dev/null 2>&1
  [[ "$LAST_TXCODE" == "0" ]] || fail "reactivating slot $s was refused (code $LAST_TXCODE)"
  sleep 2
done
[[ "$(active_count)" == "4" ]] && ok "back to 4 active" || fail "active is $(active_count), expected 4"
[[ "$(cometbft_set 0)" == "4" ]] && ok "the CometBFT set followed back to 4" || fail "set is $(cometbft_set 0)"

echo
echo "--- 5. REMOVED: terminal, and the consensus key is reserved ---"
pub4="$(sed -n 's/.*"value": "\([^"]*\)".*/\1/p' "$(node_home 3)/config/priv_validator_key.json" | head -1)"
submit_authority inactivate 4 "before removal" >/dev/null 2>&1
sleep 2
submit_authority remove 4 "retired" >/dev/null 2>&1
[[ "$LAST_TXCODE" == "0" ]] || fail "remove was refused (code $LAST_TXCODE)"
sleep 3
[[ "$(slot_status 4)" =~ REMOVED ]] && ok "slot 4 is terminal ($(slot_status 4))" \
  || fail "slot 4 status is $(slot_status 4), expected REMOVED"

newop="$(sed -n 's/.*"address":"\([^"]*\)".*/\1/p' "$NET/operator2.json" | head -1)"
submit_authority register "$newop" "$newop" "$newop" "$pub4" "reuse-attempt" >/dev/null 2>&1
[[ "$LAST_TXCODE" != "0" ]] && ok "re-registering the removed consensus key is refused (code $LAST_TXCODE)" \
  || fail "a removed validator's consensus key was reused while reserved"

echo
echo "--- 6. rotation with no quorum margin ---"
[[ "$(active_count)" == "3" ]] || note "active is $(active_count) going into rotation"
# A rotation is two acts, and only doing the first halts the chain: the set starts
# expecting a key that no running node holds, so that validator cannot sign and a
# three-member set — which tolerates nothing — stops. The operator must install
# the new key on the node as well. Discovering that here is the point of running
# the rotation with no quorum margin.
keyout="$("$ROOT/scripts/localnet/gen-consensus-key.sh" rotated-slot3)"
newkey="$(cut -f1 <<<"$keyout")"
newfile="$(cut -f2 <<<"$keyout")"
submit_authority rotate-key 3 "$newkey" >/dev/null 2>&1
[[ "$LAST_TXCODE" == "0" ]] || fail "rotate-key was refused (code $LAST_TXCODE)"
sleep 2
# slot 3 is node2's; give it the key it is now expected to sign with.
stop_node 2
cp "$newfile" "$(node_home 2)/config/priv_validator_key.json"
start_node 2
wait_rpc 2 >/dev/null 2>&1
sleep 4
wait_producing 0 && ok "the chain produced across a rotation with no margin" \
  || fail "production stopped across the rotation"
[[ "$(cometbft_set 0)" == "3" ]] && ok "the set size is unchanged by a rotation" \
  || fail "set is $(cometbft_set 0), expected 3"

echo
echo "=============== validator departures ==============="
echo "  offline      power retained, still counted, no auto-jail"
echo "  inactivated  power to zero, set follows, chain continues"
echo "  removed      terminal, consensus key reserved against reuse"
echo "  guards       the last validator cannot be taken away;"
echo "               nothing prevents walking the set down to two"
echo "===================================================="
if (( FAILURES > 0 )); then
  echo "validator departures: FAIL ($FAILURES)" >&2
  exit 1
fi
echo "validator departures: PASS"
