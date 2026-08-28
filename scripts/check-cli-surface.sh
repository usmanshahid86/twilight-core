#!/usr/bin/env bash
# Checks the CoreSlot transaction surface against a REAL BINARY.
#
# The Go test in cmd/twilightd/cmd asserts the assembled cobra tree, which is
# necessary but not sufficient: it proves a command exists, not that an operator
# can run it. The defect this guards against lived entirely in flag parsing —
# `tx coreslot register-core-slot --consensus-pubkey …` failed before signing and
# before any network call, on a command the tree said was present.
#
# So this builds the binary and runs the thing an operator runs, end to end,
# through --generate-only. No node, no network, no keys beyond a throwaway
# keyring.
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

PASSED=0; FAILED=0
check() { # <name> <expected> <actual>
  if [[ "$2" == "$3" ]]; then printf '  ok    %-52s %s\n' "$1" "$2"; PASSED=$((PASSED+1))
  else printf '  FAIL  %-52s expected=%s actual=%s\n' "$1" "$2" "$3" >&2; FAILED=$((FAILED+1)); fi
}

command -v jq >/dev/null 2>&1 || { echo "refusing to run: jq is required" >&2; exit 2; }

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
BIN="$WORK/twilightd"
HOME_DIR="$WORK/home"

echo "=== building ==="
go build -o "$BIN" ./cmd/twilightd || { echo "build failed" >&2; exit 2; }

# A fixed key rather than a generated one: the expected Any is then a constant
# this script states outright, so a bug that mangles the key cannot be masked by
# comparing the output against itself.
PUBKEY_B64="ng/9/P+38duhpl+kHTjbI85xBqRKhjItaG8ftfNXBOQ="
PUBKEY_JSON="{\"@type\":\"/cosmos.crypto.ed25519.PubKey\",\"key\":\"$PUBKEY_B64\"}"

ADDR="$("$BIN" keys add probe --keyring-backend test --home "$HOME_DIR" --output json 2>/dev/null \
        | jq -r .address)"
[[ -n "$ADDR" && "$ADDR" != null ]] || { echo "could not create a probe key" >&2; exit 2; }

# register <operator> <payout> <settlement> <consensus-pubkey> <moniker>
register() { # <pubkey-argument> -> generated tx JSON on stdout
  "$BIN" tx coreslot register "$ADDR" "$ADDR" "$ADDR" "$1" surface-probe \
    --from probe --keyring-backend test --home "$HOME_DIR" \
    --chain-id surface-check --generate-only 2>/dev/null
}

pubkey_field() { # <tx-json> <jq-path>
  jq -r ".body.messages[0].consensus_pubkey.$2 // \"MISSING\"" <<<"$1" 2>/dev/null || echo "MISSING"
}

echo
echo "=== the canonical surface runs, with a bare base64 key ==="
BARE_TX="$(register "$PUBKEY_B64")"
check "bare base64 produces a transaction"  "MsgRegisterCoreSlot" \
  "$(jq -r '.body.messages[0]."@type"' <<<"$BARE_TX" 2>/dev/null | sed 's|.*\.||')"
check "bare base64 pubkey type"             "/cosmos.crypto.ed25519.PubKey" \
  "$(pubkey_field "$BARE_TX" '"@type"')"
check "bare base64 pubkey value"            "$PUBKEY_B64" "$(pubkey_field "$BARE_TX" 'key')"

echo
echo "=== the same command accepts show-validator output verbatim ==="
# `twilightd tendermint show-validator` prints the JSON object below. Before this
# change the CLI took only the inner .key, so onboarding meant hand-extracting it.
JSON_TX="$(register "$PUBKEY_JSON")"
check "show-validator JSON produces a transaction" "MsgRegisterCoreSlot" \
  "$(jq -r '.body.messages[0]."@type"' <<<"$JSON_TX" 2>/dev/null | sed 's|.*\.||')"
check "show-validator JSON pubkey type"     "/cosmos.crypto.ed25519.PubKey" \
  "$(pubkey_field "$JSON_TX" '"@type"')"
check "show-validator JSON pubkey value"    "$PUBKEY_B64" "$(pubkey_field "$JSON_TX" 'key')"

echo
echo "=== the two forms are interchangeable, not merely both accepted ==="
# Byte equality of the whole message, so admission cannot depend on which way an
# operator copied the key.
check "both forms yield an identical message" "same" \
  "$([[ "$(jq -Sc '.body.messages[0]' <<<"$BARE_TX")" \
       == "$(jq -Sc '.body.messages[0]' <<<"$JSON_TX")" ]] && echo same || echo DIFFERENT)"

echo
echo "=== the generated tree is gone ==="
# These are the names AutoCLI derived from the Msg service. Each one took an Any
# consensus key it could not parse. They must not resolve at all now.
# Probed WITHOUT --help, deliberately. `--help` on an unknown subcommand shows the
# parent's help and exits 0, which is correct; the case that matters is the one a
# stale script actually runs.
#
# Exiting non-zero is the property under test. A parent that carries subcommands
# but is not itself runnable falls through to help and exits ZERO on an unknown
# name — so removing the generated tree without a RunE would have turned a loud
# parse failure into a silent success that registered nobody.
for generated in register-core-slot rotate-consensus-key update-settlement-address; do
  out="$("$BIN" tx coreslot "$generated" 2>&1)"; rc=$?
  check "tx coreslot $generated fails"        "rejected" \
    "$([[ $rc -ne 0 ]] && echo rejected || echo ACCEPTED)"
  check "  ...naming it as unknown"           "yes" \
    "$(grep -qi 'unknown command' <<<"$out" && echo yes || echo no)"
done

echo
echo "=== the root-level compatibility surface still works ==="
# scripts/localnet/*.sh drive admission through this path; moving the canonical
# surface must not move this one.
ROOT_TX="$("$BIN" coreslot register "$ADDR" "$ADDR" "$ADDR" "$PUBKEY_B64" surface-probe \
  --from probe --keyring-backend test --home "$HOME_DIR" \
  --chain-id surface-check --generate-only 2>/dev/null)"
check "root-level coreslot register works"  "$PUBKEY_B64" "$(pubkey_field "$ROOT_TX" 'key')"

echo
echo "=== a bad key is refused, not silently accepted ==="
"$BIN" tx coreslot register "$ADDR" "$ADDR" "$ADDR" \
  '{"@type":"/cosmos.crypto.secp256k1.PubKey","key":"'"$PUBKEY_B64"'"}' surface-probe \
  --from probe --keyring-backend test --home "$HOME_DIR" \
  --chain-id surface-check --generate-only >/dev/null 2>&1
bad_rc=$?
check "wrong @type is rejected"             "rejected" \
  "$([[ $bad_rc -ne 0 ]] && echo rejected || echo ACCEPTED)"

echo
if (( FAILED > 0 )); then
  echo "cli surface: FAIL ($FAILED of $((PASSED+FAILED)))" >&2; exit 1
fi
echo "cli surface: PASS ($PASSED checks)"
