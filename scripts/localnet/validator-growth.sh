#!/usr/bin/env bash
set -uo pipefail

# A chain that starts with one validator and grows to five, the way a real
# network does: each new node syncs FIRST and is admitted to the validator set
# only once it is caught up.
#
# That ordering is the substance of this drill, not a detail. Activating a slot
# whose node is still syncing puts a validator in the set that is producing
# nothing, and quorum stalls — which looks like a defect and is not. Encoding the
# correct sequence is most of what this proves.
#
# Nothing else here tests a real join. lifecycle-e2e registers and activates a
# fifth slot, but no node is ever started for it: the set grows to include a
# member that does not exist, and the chain survives only because the other four
# still meet quorum. That is a registry test. This is a network one, and it is
# the untested half of the export/restore/join issue.
#
# At every transition three things are asserted together:
#   * the CometBFT validator set equals x/coreslot's ACTIVE set — the live proof
#     that x/coreslot is the only source of ValidatorUpdates;
#   * every running node agrees on the app hash;
#   * the chain keeps producing across the change.
#
# No epochs, no rewards, no settlement: this is validator-set mechanics.

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
. "$ROOT/scripts/localnet/lib/drill-common.sh"

TARGET_N="${GROWTH_TARGET_N:-5}"
# A joining node must be able to catch up, and at the usual localnet block time it
# cannot: a single validator commits roughly five blocks a second with no
# consensus round to wait for, while a syncing node replays fewer than four. The
# gap GROWS, and "caught up before admission" becomes unachievable by
# construction. That is an artifact of an unrealistically fast chain, not a
# property of the software — production block times are seconds, where sync
# outpaces production comfortably. So this drill runs at a realistic pace.
export TWILIGHT_LOCALNET_TIMEOUT_COMMIT="${GROWTH_TIMEOUT_COMMIT:-1s}"
BOOT_SECONDS="${GROWTH_BOOT_SECONDS:-45}"
SYNC_SECONDS="${GROWTH_SYNC_SECONDS:-60}"
PROGRESS_BLOCKS=2

FAILURES=0
fail() { echo "  FAIL: $*" >&2; FAILURES=$((FAILURES + 1)); }
ok()   { echo "  ok: $*"; }

if pgrep -x twilightd >/dev/null 2>&1; then
  echo "a twilightd process is already running; stop it before growing a set" >&2
  exit 2
fi
trap '"$ROOT/scripts/localnet/stop.sh" >/dev/null 2>&1 || true; pkill -f "twilightd start" >/dev/null 2>&1 || true' EXIT

cometbft_validator_count() {
  rpc_get "$1" /validators 2>/dev/null | jq -r '(.result.validators // []) | length' 2>/dev/null || echo ""
}
catching_up() {
  rpc_get "$1" /status 2>/dev/null | jq -r '.result.sync_info.catching_up' 2>/dev/null || echo ""
}

wait_rpc() { # <node>
  local deadline=$((SECONDS + BOOT_SECONDS))
  while ((SECONDS < deadline)); do
    [[ -n "$(latest_height "$1")" ]] && return 0
    sleep 1
  done
  return 1
}

wait_producing() { # <observer>
  local start now deadline
  start="$(latest_height "$1")"; [[ -z "$start" ]] && return 1
  deadline=$((SECONDS + 20))
  while ((SECONDS < deadline)); do
    now="$(latest_height "$1")"
    [[ -n "$now" ]] && (( now - start >= PROGRESS_BLOCKS )) && return 0
    sleep 1
  done
  return 1
}

# prepare_node <index> — a node home that is NOT in genesis: same document,
# its own keys and ports, peered at node 0. This is what an operator does before
# asking to be admitted.
prepare_node() {
  local i="$1" home; home="$(node_home "$i")"
  "$BIN" init "node$i" --chain-id "$CHAIN_ID" --home "$home" >/dev/null 2>&1
  "$BIN" keys add "operator$i" --keyring-backend test --home "$home" --output json \
    >"$NET/operator$i.json" 2>/dev/null
  cp "$(node_home 0)/config/genesis.json" "$home/config/genesis.json"

  local id0 rpc p2p grpc
  id0="$("$BIN" tendermint show-node-id --home "$(node_home 0)")"
  rpc=$((26657 + i * 100)); p2p=$((26656 + i * 100)); grpc=$((9090 + i * 100))
  sed -i.bak \
    -e "s#laddr = \"tcp://127.0.0.1:26657\"#laddr = \"tcp://127.0.0.1:${rpc}\"#" \
    -e "s#laddr = \"tcp://0.0.0.0:26656\"#laddr = \"tcp://0.0.0.0:${p2p}\"#" \
    -e "s#persistent_peers = \"\"#persistent_peers = \"${id0}@127.0.0.1:26656\"#" \
    -e "s#pex = true#pex = false#" \
    -e "s#allow_duplicate_ip = false#allow_duplicate_ip = true#" \
    -e "s#^timeout_commit = .*#timeout_commit = \"${TWILIGHT_LOCALNET_TIMEOUT_COMMIT:-200ms}\"#" \
    "$home/config/config.toml"
  sed -i.bak -e "s#address = \"localhost:9090\"#address = \"localhost:${grpc}\"#" "$home/config/app.toml"
  rm -f "$home/config/"*.bak
}

# assert_set_matches_active <expected> <nodes...>
#
# The CometBFT set and x/coreslot's ACTIVE set must be the same size, and every
# running node must agree on the app hash. A divergence here would mean something
# other than x/coreslot had moved the validator set.
assert_set_matches_active() {
  local expected="$1"; shift
  local active cometbft
  active="$(active_count)"
  cometbft="$(cometbft_validator_count 0)"
  [[ "$active" == "$expected" ]]   || fail "coreslot ACTIVE set is $active, expected $expected"
  [[ "$cometbft" == "$expected" ]] || fail "CometBFT validator set is $cometbft, expected $expected"
  [[ "$active" == "$cometbft" ]]   || fail "coreslot ACTIVE ($active) != CometBFT set ($cometbft)"
  AGREE_NODES="$*" MIN_HEIGHT=2 "$ROOT/scripts/localnet/agree.sh" >/dev/null 2>&1 \
    || fail "nodes [$*] do not agree on the app hash"
}

echo "=== genesis: a single validator ==="
NODE_COUNT=1 "$ROOT/scripts/localnet/init.sh" >/dev/null 2>&1 || { echo "init failed" >&2; exit 1; }
NODE_COUNT=1 "$ROOT/scripts/localnet/start.sh" >/dev/null 2>&1 || { echo "start failed" >&2; exit 1; }
wait_rpc 0 || { echo "node0 never answered" >&2; exit 1; }
wait_producing 0 || { echo "a single-validator chain must produce blocks" >&2; exit 1; }
ok "one validator, producing"
assert_set_matches_active 1 0

running="0"
for (( i = 1; i < TARGET_N; i++ )); do
  n_before=$((i))
  echo
  echo "=== admitting node$i: set $n_before -> $((n_before + 1)) ==="

  prepare_node "$i"
  start_node "$i"
  wait_rpc "$i" || { fail "node$i RPC never answered"; break; }

  # Sync BEFORE admission. A node still catching up must not be a validator.
  synced=0
  deadline=$((SECONDS + SYNC_SECONDS))
  while ((SECONDS < deadline)); do
    if [[ "$(catching_up "$i")" == "false" ]]; then
      h_new="$(latest_height "$i")"; h_ref="$(latest_height 0)"
      if [[ -n "$h_new" && -n "$h_ref" ]] && (( h_ref - h_new <= 2 )); then synced=1; break; fi
    fi
    sleep 1
  done
  (( synced == 1 )) && ok "node$i caught up before admission" || fail "node$i never caught up"

  # It is syncing, but it is NOT yet a validator. Admission is a separate act.
  pre="$(cometbft_validator_count 0)"
  [[ "$pre" == "$n_before" ]] && ok "a synced node is not yet in the set ($pre)" \
    || fail "set is $pre before admission, expected $n_before"

  # Now admit it: register, then activate. Authority-only, by transaction.
  op="$(sed -n 's/.*"address":"\([^"]*\)".*/\1/p' "$NET/operator$i.json" | head -1)"
  pub="$(sed -n 's/.*"value": "\([^"]*\)".*/\1/p' "$(node_home "$i")/config/priv_validator_key.json" | head -1)"
  sid="$(next_slot_id)"
  # _submit always returns 0 and reports through LAST_TXCODE, so the result has
  # to be read from there — an `|| fail` on the exit status can never fire. And
  # no `--`: that separator belongs to lifecycle-e2e's run_action wrapper, which
  # strips it before calling here.
  submit_authority register "$op" "$op" "$op" "$pub" "node$i" >/dev/null 2>&1
  [[ "$LAST_TXCODE" == "0" ]] || fail "register for node$i was refused (code $LAST_TXCODE)"
  submit_authority activate "$sid" >/dev/null 2>&1
  [[ "$LAST_TXCODE" == "0" ]] || fail "activate for slot $sid was refused (code $LAST_TXCODE)"
  sleep 3

  running="$running $i"
  before_failures=$FAILURES
  wait_producing 0 || fail "the chain stopped producing after admitting node$i"
  assert_set_matches_active $((n_before + 1)) $running
  if (( FAILURES == before_failures )); then
    ok "set is now $((n_before + 1)), all of [$running] agreeing"
  fi
done

echo
final="$(cometbft_validator_count 0)"
echo "=============== validator growth ==============="
echo "  started at 1 validator, ended at ${final:-unknown}"
echo "  each node synced before admission, never after"
echo "  CometBFT set matched x/coreslot ACTIVE at every step"
echo "==============================================="
if (( FAILURES > 0 )); then
  echo "validator growth: FAIL ($FAILURES)" >&2
  exit 1
fi
echo "validator growth: PASS"
