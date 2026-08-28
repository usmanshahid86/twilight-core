#!/usr/bin/env bash
# Checks that the vulnerability gate runs under the Go version go.mod declares.
#
# scripts/vulncheck.sh pins the govulncheck VERSION and, since #117, the
# TOOLCHAIN as well. Ordinary CI cannot detect the loss of the second:
# actions/setup-go has already selected the go.mod toolchain, so deleting the
# export or restoring GOTOOLCHAIN=auto leaves every CI job green while a
# developer machine with a newer Go in its module cache silently scans under a
# different compiler — which is how #117 was found, as a package-loading failure
# that reads exactly like a vulnerability finding.
#
# So the pin needs a test that can fail. This one runs the REAL script with a
# fake `go` first on PATH, and inspects the environment that child receives.
#
# # The false-pass rule
#
# The expected version is read here, from go.mod, with an expression deliberately
# unlike the production one — and the observed value comes from the fake `go`.
# Deriving both from the same production extractor would agree with itself and
# prove nothing, which is the failure mode this repository has now shipped twice.
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

PASSED=0; FAILED=0
check() { # <name> <expected> <actual>
  if [[ "$2" == "$3" ]]; then printf '  ok    %-46s %s\n' "$1" "$2"; PASSED=$((PASSED+1))
  else printf '  FAIL  %-46s expected=%s actual=%s\n' "$1" "$2" "$3" >&2; FAILED=$((FAILED+1)); fi
}

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

# --- the expected value, read independently of the production extractor -------
#
# Production uses awk on the `go` directive. This uses sed on the same line, so a
# broken production expression cannot make the two agree by construction.
EXPECTED_GO="$(sed -n 's/^go[[:space:]]\{1,\}\([0-9][0-9.]*\)[[:space:]]*$/\1/p' "$ROOT/go.mod" | head -1)"
if [[ -z "$EXPECTED_GO" ]]; then
  echo "refusing to run: could not read the go directive from go.mod" >&2
  exit 2
fi
EXPECTED="go${EXPECTED_GO}"

# --- a fake `go` that records the environment it is handed --------------------
#
# It emits the minimal output the production script needs to complete, so the
# real script is exercised end to end rather than partially. It never compiles
# anything, so the result cannot depend on which toolchains this machine has.
mkdir -p "$WORK/bin"
cat > "$WORK/bin/go" <<'FAKE'
#!/usr/bin/env bash
# Record what the caller set, once, then satisfy the caller.
printf '%s\n' "${GOTOOLCHAIN-<unset>}" > "${FAKE_GO_RECORD}"
case "$1" in
  run)
    # govulncheck -format json: an empty finding stream is valid and parses.
    printf '{"config":{"protocol_version":"v1.0.0"}}\n'
    ;;
  env) echo "" ;;
esac
exit 0
FAKE
chmod +x "$WORK/bin/go"

run_gate() { # <ambient GOTOOLCHAIN> <go.mod path override or empty> -> prints recorded value
  local ambient="$1"
  export FAKE_GO_RECORD="$WORK/recorded"
  : > "$FAKE_GO_RECORD"
  PATH="$WORK/bin:$PATH" GOTOOLCHAIN="$ambient" "$ROOT/scripts/vulncheck.sh" ./... >/dev/null 2>&1
  cat "$FAKE_GO_RECORD" 2>/dev/null || echo "<no child ran>"
}

echo "=== the gate forces the go.mod toolchain over an ambient one ==="
# A deliberately different ambient value. If the production export were removed,
# the child would inherit THIS instead, which is precisely the #117 failure.
check "child receives the go.mod toolchain"  "$EXPECTED" "$(run_gate go1.26.0)"
check "ambient value did not win"            "different" \
  "$([[ "$(run_gate go1.26.0)" != "go1.26.0" ]] && echo different || echo AMBIENT_WON)"

echo
echo "=== and does so regardless of what the ambient value is ==="
check "auto is overridden"                   "$EXPECTED" "$(run_gate auto)"
check "an unset ambient is still pinned"     "$EXPECTED" "$(run_gate "")"

echo
echo "=== a go.mod it cannot read is refused, not guessed ==="
# Run against a copy of the tree whose go directive is missing or malformed. The
# script derives the version from ROOT/go.mod, so ROOT has to move with it.
setup_broken_root() { # <go.mod first line> -> prints a root path
  local directive="$1" dir
  dir="$WORK/broken-$RANDOM"
  mkdir -p "$dir/scripts"
  cp "$ROOT/scripts/vulncheck.sh" "$dir/scripts/"
  cp "$ROOT/.govulncheck-allow.json" "$dir/" 2>/dev/null || true
  printf '%s\nmodule example.com/x\n' "$directive" > "$dir/go.mod"
  echo "$dir"
}

for label in "missing:module example.com/x" "malformed:go not-a-version"; do
  name="${label%%:*}"; directive="${label#*:}"
  dir="$(setup_broken_root "$directive")"
  export FAKE_GO_RECORD="$WORK/recorded-$name"
  : > "$FAKE_GO_RECORD"
  PATH="$WORK/bin:$PATH" GOTOOLCHAIN=go1.26.0 "$dir/scripts/vulncheck.sh" ./... >/dev/null 2>&1
  rc=$?
  check "a $name go directive exits non-zero" "nonzero" \
    "$([[ $rc -ne 0 ]] && echo nonzero || echo zero)"
  check "  ...and runs no scan"               "yes" \
    "$([[ ! -s "$FAKE_GO_RECORD" ]] && echo yes || echo no)"
done

echo
if (( FAILED > 0 )); then
  echo "vulncheck pin: FAIL ($FAILED of $((PASSED+FAILED)))" >&2; exit 1
fi
echo "vulncheck pin: PASS ($PASSED checks)"
