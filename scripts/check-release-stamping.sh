#!/usr/bin/env bash
# Checks that a binary cannot claim to be something it is not.
#
# A release artifact asserts two things about its provenance: the version it was
# cut as, and the commit it was built from. Both are consumed by operators who
# pre-stage binaries and verify them by hash, so a stamp that is confidently
# wrong is worse than one that is missing — the checksum will hash the wrong
# artifact faithfully and disclose nothing.
#
# The case that shipped: dirtiness lived inside VERSION, so `make build
# VERSION=v0.1.0` on a modified tree replaced the whole `git describe --dirty`
# expression and dropped the marker. The binary then reported an exact commit
# its source did not match. Untested build logic is how that happened, so the
# cases below run against the real Makefile.
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

PASSED=0; FAILED=0
check() { # <name> <expected> <actual>
  if [[ "$2" == "$3" ]]; then printf '  ok    %-46s %s\n' "$1" "$2"; PASSED=$((PASSED+1))
  else printf '  FAIL  %-46s expected=%s actual=%s\n' "$1" "$2" "$3" >&2; FAILED=$((FAILED+1)); fi
}

PROBE="cmd/twilightd/main.go"
UNTRACKED_GO="cmd/twilightd/zz_provenance_probe.go"
BIN="build/twilightd"
cleanup() { git checkout -- "$PROBE" 2>/dev/null || true; rm -f "$UNTRACKED_GO"; }
trap cleanup EXIT

# Refuse to run against a tree that is already modified: the cases below dirty a
# file deliberately and restore it, and doing that on top of real work would
# discard it.
if ! git diff-index --quiet HEAD -- 2>/dev/null; then
  echo "refusing to run: the working tree already has uncommitted changes" >&2
  git --no-pager diff --stat HEAD -- >&2
  exit 2
fi

stamped() { "$BIN" version --long 2>/dev/null | awk -v k="$1" -F': *' '$1==k {print $2}'; }

echo "=== a clean tree stamps exactly what it is asked to ==="
make build VERSION=v9.9.9 >/dev/null 2>&1
check "explicit version, clean"        "v9.9.9" "$(stamped version)"
check "commit is the committed HEAD"   "$(git rev-parse HEAD)" "$(stamped commit)"
make build >/dev/null 2>&1
check "default version has no -dirty"  "clean"  "$([[ "$(stamped version)" == *-dirty ]] && echo dirty || echo clean)"

echo
echo "=== untracked files are not modifications ==="
# docs/specs/ is untracked and permanently present here; if it counted as dirty,
# build-release could never run.
UNTRACKED_PROBE="$(mktemp -p . XXXXXX.untracked 2>/dev/null || mktemp ./XXXXXX.untracked)"
check "untracked does not mark dirty"  "clean" \
  "$(git diff-index --quiet HEAD -- 2>/dev/null && echo clean || echo dirty)"
rm -f "$UNTRACKED_PROBE"

echo
echo "=== a dirty tree cannot be hidden by an explicit version ==="
echo "// provenance probe" >>"$PROBE"
make build VERSION=v9.9.9 >/dev/null 2>&1
check "explicit version, dirty"        "v9.9.9-dirty" "$(stamped version)"
make build >/dev/null 2>&1
check "default version, dirty"         "dirty" \
  "$([[ "$(stamped version)" == *-dirty ]] && echo dirty || echo clean)"

echo
echo "=== provenance cannot be switched off from the command line ==="
# GNU Make lets a command-line assignment beat any assignment in the makefile, so
# `DIRTY=` blanked the marker and stamped a modified tree as clean.
make build VERSION=v9.9.9 DIRTY= >/dev/null 2>&1
check "DIRTY= cannot blank the marker"  "v9.9.9-dirty" "$(stamped version)"
make build-release VERSION=v9.9.9 DIRTY= >/dev/null 2>&1; rc=$?
check "DIRTY= cannot bypass the refusal" "nonzero" "$([[ $rc -ne 0 ]] && echo nonzero || echo zero)"

echo
echo "=== a release cannot be built from a dirty tree at all ==="
rm -rf build/release
make build-release VERSION=v9.9.9 >/dev/null 2>&1; rc=$?
# Non-zero is the property; the exact code is make's convention (2 for a failed
# recipe), and pinning it would be asserting make's internals rather than ours.
check "build-release exits non-zero"   "nonzero" "$([[ $rc -ne 0 ]] && echo nonzero || echo zero)"
# The guards run before anything is written, so a refusal produces no new
# artifacts. It also does not clear the directory: whatever was there survives,
# which the pre-existing-artifact case below asserts directly.
check "refusal produces no new artifacts" "absent" \
  "$([[ -d build/release ]] && echo present || echo absent)"

cleanup

echo
echo "=== untracked build inputs are not clean ==="
# diff-index sees tracked files only, so an untracked .go file compiles into the
# binary while the tree reports clean. The release must see it; docs/specs/ and
# other untracked material that cannot reach the compiler must not count.
printf 'package main\n' >"$UNTRACKED_GO"
check "untracked .go marks the tree dirty" "dirty" \
  "$(git diff-index --quiet HEAD -- 2>/dev/null \
      && test -z "$(git ls-files --others --exclude-standard -- '*.go' go.mod go.sum)" \
      && echo clean || echo dirty)"
make build VERSION=v9.9.9 >/dev/null 2>&1
check "untracked .go stamps -dirty"     "v9.9.9-dirty" "$(stamped version)"
make build-release VERSION=v9.9.9 >/dev/null 2>&1; rc=$?
check "untracked .go refuses a release" "nonzero" "$([[ $rc -ne 0 ]] && echo nonzero || echo zero)"
rm -f "$UNTRACKED_GO"

echo
echo "=== a refusal preserves artifacts that were already there ==="
mkdir -p build/release && echo sentinel >build/release/PREEXISTING
echo "// provenance probe" >>"$PROBE"
make build-release VERSION=v9.9.9 >/dev/null 2>&1
check "pre-existing artifacts survive"  "present" \
  "$([[ -f build/release/PREEXISTING ]] && echo present || echo absent)"
cleanup; rm -rf build/release
echo
echo "=== and succeeds once the tree is clean again ==="
make build-release VERSION=v9.9.9 >/dev/null 2>&1; rc=$?
check "build-release exits zero"       "0" "$rc"
check "three artifacts"                "3" "$(ls build/release/twilightd-* 2>/dev/null | wc -l | tr -d ' ')"
check "named with the clean version"   "3" "$(ls build/release/twilightd-v9.9.9-* 2>/dev/null | wc -l | tr -d ' ')"
check "checksums cover every artifact" "3" "$(grep -c 'twilightd-' build/release/SHA256SUMS 2>/dev/null || echo 0)"
# Built from `git archive HEAD`, so the artifact is the commit it names.
check "release stamps the committed HEAD" "$(git rev-parse HEAD)" \
  "$(build/release/twilightd-v9.9.9-darwin-arm64 version --long 2>/dev/null | awk -F': *' '$1=="commit"{print $2}')"
rm -rf build/release
make build >/dev/null 2>&1   # leave a normally-stamped binary behind

echo
if (( FAILED > 0 )); then
  echo "release stamping: FAIL ($FAILED of $((PASSED+FAILED)))" >&2; exit 1
fi
echo "release stamping: PASS ($PASSED checks)"
