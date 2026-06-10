#!/usr/bin/env bash
set -euo pipefail

# Drill 2: restart-after-rotation. Rotate an active validator's consensus key via
# a real MsgRotateConsensusKey, swap that node's priv_validator_key.json to the
# new key material, restart the node, and prove it rejoins with the new key at
# full power while the old key holds zero power.
#
# Inspired by scenario1.sh::replace_node3_rotation_key, but driven by the real
# CLI + the gen-consensus-key.sh helper (no hand-edited unsafe state — we swap a
# CometBFT-generated priv_validator_key.json and leave priv_validator_state.json
# in place; current height > last-signed height, so no height regression).
#
# Target node/slot: node3 / slot 4 (node3's genesis slot).

export DRILL="restart-rotation"
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
# shellcheck source=scripts/localnet/lib/drill-common.sh
source "$ROOT/scripts/localnet/lib/drill-common.sh"

FAILS=0
note_fail() { FAILS=$((FAILS + 1)); }
trap 'teardown_localnet' EXIT

NODE=3
SLOT=4

# The Any consensus pubkey is rendered as {"@type":...,"key":"<base64>"} — the
# same base64 ed25519 pubkey `rotate-key` consumes and gen-consensus-key.sh prints.
slot4_applied_pubkey() { q last-applied 2>/dev/null | jq -r '(.validators // [])[] | select(.slot_id=="'"$SLOT"'") | .consensus_pubkey.key' 2>/dev/null | head -1; }
applied_count_for_pubkey() { q last-applied 2>/dev/null | jq -r --arg v "$1" '[(.validators // [])[] | select(.consensus_pubkey.key==$v)] | length' 2>/dev/null || echo 0; }
applied_power_for_pubkey() { q last-applied 2>/dev/null | jq -r --arg v "$1" '([(.validators // [])[] | select(.consensus_pubkey.key==$v) | .power][0]) // "0"' 2>/dev/null || echo "?"; }

main() {
  setup_localnet
  init_evidence
  local rotation_evidence="$EVID_DIR/rotation.jsonl"
  : >"$rotation_evidence"
  wait_all_height "${MIN_HEIGHT:-4}"

  echo "== restart-after-rotation: node$NODE / slot $SLOT =="
  if agree_nodes "" "00-initial"; then echo "  initial 4-node agreement: PASS"; else echo "  initial agreement: FAIL"; note_fail; fi

  local old_addr new_addr keyout new_pub new_keyfile old_pub
  old_addr="$(jq -r '.address' "$(node_home "$NODE")/config/priv_validator_key.json")"
  old_pub="$(slot4_applied_pubkey)"

  keyout="$("$ROOT/scripts/localnet/gen-consensus-key.sh" rotated-node$NODE)"
  new_pub="$(cut -f1 <<<"$keyout")"
  new_keyfile="$(cut -f2 <<<"$keyout")"
  new_addr="$(jq -r '.address' "$new_keyfile")"

  echo "  old consensus address: $old_addr"
  echo "  new consensus address: $new_addr"

  # 1) submit the rotation (authority), wait for it to take effect.
  local hb; hb="$(latest_height 0)"
  submit_authority rotate-key "$SLOT" "$new_pub"
  local code="$LAST_TXCODE" hash="$LAST_TXHASH" incH effH="" nvu="n/a"
  if [[ "$code" != "0" ]]; then echo "  rotate-key tx failed code=$code" >&2; note_fail; fi
  incH="$(tx_height "$hash")"
  if [[ -n "$incH" ]]; then
    effH=$((incH + 1)) # KeyRotationDelayBlocks = 1
    wait_all_height $((effH + 1)) || true
    if assert_num_val_updates_exact_at "01-rotate-key" "$effH" 2; then nvu="$LAST_NUM_VAL_UPDATES"; else nvu="FAIL"; note_fail; fi
  fi
  # rotation transition must emit exactly two updates (old@0, new@power)
  local rot_result="PASS"
  if [[ "$nvu" != "2" ]]; then rot_result="FAIL(num_val_updates=$nvu want 2)"; note_fail; fi
  # pending rotation should be cleared
  local pend; pend="$(q pending-rotations 2>/dev/null | jq -r '(.rotations // []) | length' 2>/dev/null || echo '?')"
  if [[ "$pend" != "0" ]]; then rot_result="$rot_result;FAIL(pending=$pend)"; note_fail; fi
  local rotate_agree="PASS"
  if ! agree_nodes "" "01-rotate-key"; then rotate_agree="FAIL"; rot_result="$rot_result;AGREE_FAIL"; note_fail; fi

  # 2) before restart: slot4's applied key is the NEW pubkey, old pubkey gone.
  local applied_new applied_old old_power new_power expected_power operator
  applied_new="$(slot4_applied_pubkey)"
  applied_old="$(applied_count_for_pubkey "$old_pub")"
  old_power="$(applied_power_for_pubkey "$old_pub")"
  new_power="$(applied_power_for_pubkey "$new_pub")"
  expected_power="$(slot_voting_power)"
  operator="$(q slot "$SLOT" 2>/dev/null | jq -r '.slot.operator_address // "?"')"
  if [[ "$applied_new" == "$new_pub" ]]; then echo "  slot$SLOT applied key is the NEW key: PASS"; else echo "  slot$SLOT applied key mismatch: PASS-key=$applied_new" >&2; note_fail; fi
  if [[ "$applied_old" == "0" ]]; then echo "  OLD key removed from applied set (power 0): PASS"; else echo "  OLD key still applied: FAIL" >&2; note_fail; fi
  if [[ "$old_power" == "0" && "$new_power" == "$expected_power" ]]; then
    echo "  rotation powers old=0 new=$new_power: PASS"
  else
    echo "  rotation power check FAIL old=$old_power new=$new_power expected_new=$expected_power" >&2
    rot_result="$rot_result;FAIL(rotation-powers)"; note_fail
  fi
  jq -cn \
    --arg drill "$DRILL" --arg run_id "$RUN_ID" --arg action "01-rotate-key" \
    --arg slot_id "$SLOT" --arg operator_address "$operator" --arg rotation_tx_hash "$hash" \
    --arg effective_height "$effH" --arg old_consensus_address "$old_addr" \
    --arg old_power_after "$old_power" --arg expected_old_power "0" \
    --arg new_consensus_address "$new_addr" --arg new_power_after "$new_power" \
    --arg expected_new_power "$expected_power" --arg timestamp "$(timestamp_utc)" \
    '{drill:$drill,run_id:$run_id,action:$action,slot_id:($slot_id|tonumber),operator_address:$operator_address,rotation_tx_hash:$rotation_tx_hash,effective_height:$effective_height,old_consensus_address:$old_consensus_address,old_power_after:$old_power_after,expected_old_power:$expected_old_power,new_consensus_address:$new_consensus_address,new_power_after:$new_power_after,expected_new_power:$expected_new_power,timestamp:$timestamp}' \
    >>"$rotation_evidence"
  record_row "01-rotate-key" "$hb" "$hash" "$code" "$(latest_height 0)" "$(active_count)" "$nvu" "$rotate_agree" "$rot_result"

  # 3) swap node3's key material and restart it.
  echo "  stopping node$NODE, swapping priv_validator_key.json, restarting..."
  stop_node "$NODE"
  sleep 1
  cp "$new_keyfile" "$(node_home "$NODE")/config/priv_validator_key.json"
  local resumeFrom; resumeFrom="$(latest_height 0)"
  start_node "$NODE"

  # 4) node3 must rejoin and catch up.
  local restart_result="PASS"
  if wait_height_node "$NODE" $((resumeFrom + 2)) 90; then echo "  node$NODE rejoined and is progressing: PASS"; else echo "  node$NODE did not rejoin: FAIL" >&2; restart_result="FAIL(no-rejoin)"; note_fail; fi

  # 5) full four-node agreement after restart.
  wait_all_height $((resumeFrom + 3)) || true
  local agree="PASS"
  if ! agree_nodes "" "02-restart-node$NODE"; then agree="FAIL"; restart_result="$restart_result;AGREE_FAIL"; note_fail; fi
  record_row "02-restart-node$NODE" "$resumeFrom" "-" "-" "$(latest_height 0)" "$(active_count)" "-" "$agree" "$restart_result"

  # 6) confirm new key has slot voting power, old key has none.
  local pwr; pwr="$(slot_power "$SLOT")"
  if [[ "$pwr" != "0" && "$pwr" != "?" ]]; then echo "  slot$SLOT consensus_power=$pwr (new key active): PASS"; else echo "  slot$SLOT power check FAIL ($pwr)" >&2; note_fail; fi

  echo "evidence: $SUMMARY"
  printf 'slot_id=%s operator_address=%s rotation_tx_hash=%s effective_height=%s old_addr=%s old_power_after=%s new_addr=%s new_power_after=%s expected_new_power=%s\n' \
    "$SLOT" "$operator" "$hash" "$effH" "$old_addr" "$old_power" "$new_addr" "$new_power" "$expected_power" >"$EVID_DIR/rotation-addresses.txt"

  if ((FAILS > 0)); then echo "restart-rotation: FAIL ($FAILS failures)" >&2; return 1; fi
  echo "restart-rotation: PASS"
}

main "$@"
