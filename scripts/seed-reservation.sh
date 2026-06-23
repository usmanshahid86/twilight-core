#!/usr/bin/env bash
# Seed a deterministic ReservedConsensusAddress into a localnet's genesis so the REST
# route /twilight/coreslot/v1/reserved-consensus-address/{hex} can be smoke-tested for a
# 200. Run AFTER scripts/localnet/init.sh and BEFORE scripts/localnet/start.sh (genesis
# is fixed at height 0). Prints the lowercase-hex consensus address to export as
# RESERVED_CONS_HEX for scripts/smoke-api-surface.sh.
#
# Usage:
#   TWILIGHT_LOCALNET_HOME=/tmp/twilight-localnet ./scripts/seed-reservation.sh
#   export RESERVED_CONS_HEX="$(TWILIGHT_LOCALNET_HOME=... ./scripts/seed-reservation.sh -q)"
set -euo pipefail

NET="${TWILIGHT_LOCALNET_HOME:-/tmp/twilight-localnet}"
QUIET=0; [[ "${1:-}" == "-q" ]] && QUIET=1
log() { [[ "$QUIET" -eq 1 ]] || echo "$@" >&2; }

# 20-byte consensus address fixture (0xAB * 20). Key in state is lowercase hex.
HEX="$(printf 'ab%.0s' {1..20})"
B64="$(printf '%s' "$HEX" | xxd -r -p | base64)"

shopt -s nullglob
genfiles=("$NET"/node*/config/genesis.json)
[[ ${#genfiles[@]} -gt 0 ]] || { echo "no genesis files under $NET (run init.sh first)" >&2; exit 1; }

for g in "${genfiles[@]}"; do
  tmp="$g.tmp"
  jq --arg a "$B64" '.app_state.coreslot.reserved_consensus_addresses =
      [{"cons_address":$a,"slot_id":"9","reserved_until":"100000","reason":"smoke fixture"}]' \
    "$g" >"$tmp" && mv "$tmp" "$g"
done

log "seeded reservation into ${#genfiles[@]} genesis file(s) under $NET"
log "export RESERVED_CONS_HEX=$HEX   # then run scripts/smoke-api-surface.sh"
echo "$HEX"
