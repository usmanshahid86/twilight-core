#!/usr/bin/env bash
#
# Protocol-vector conformance gate.
#
# Runs the tracked normative vector packs against the production and
# production-intended functions that implement them. The packs live in
# internal/consensusvectors/testdata and are executed from the repository's own
# bytes; nothing is fetched at run time.
#
# `go test -run <regex>` exits 0 when the regex matches nothing, and a package
# with no test files also exits 0. Either would turn this gate green while
# proving nothing, so every required test function is confirmed to EXIST with
# `go test -list` before it is run.
#
# Used by both `make consensus-vectors` and the CI job of the same name, so the
# two cannot drift.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"

# package:TestFunction pairs that must exist and pass.
REQUIRED=(
  "./internal/consensusvectors/:TestLoadDrawPack"
  "./internal/consensusvectors/:TestLoadSelectedDrawIDsPack"
  "./internal/consensusvectors/:TestLoadRewardPack"
  "./internal/consensusvectors/:TestProposerResolutionDeferralMatchesPack"
  "./internal/consensusvectors/:TestCaseLedgerRejectsExecutedDeferral"
  "./x/mining/types/selectionv1/:TestDrawPackConformance"
  "./x/mining/types/selectionv1/:TestSelectedDrawIDsPackConformance"
  "./x/rewards/keeper/:TestRewardPackConformance"
)

echo "consensus-vectors: confirming required conformance tests exist"
missing=0
for entry in "${REQUIRED[@]}"; do
  pkg="${entry%%:*}"
  test_name="${entry##*:}"
  # Collected into a variable rather than piped: under `pipefail`, `grep -q`
  # exits at the first match and the resulting SIGPIPE would fail the pipeline
  # precisely when the test DOES exist.
  listing="$(go test -list "^${test_name}\$" "${pkg}" 2>/dev/null || true)"
  if ! printf '%s\n' "${listing}" | grep -qx "${test_name}"; then
    echo "consensus-vectors: FAIL required test ${test_name} not found in ${pkg}" >&2
    missing=1
  fi
done
if [ "${missing}" -ne 0 ]; then
  echo "consensus-vectors: a conformance test was renamed or removed; restore it or update this gate." >&2
  exit 1
fi

# -count=1 defeats the test cache, so the gate re-executes the packs every run
# rather than reporting a cached result for bytes that may have changed.
echo "consensus-vectors: vector packs, loaders and deferral manifest"
go test -count=1 ./internal/consensusvectors/...

echo "consensus-vectors: Selection V1 conformance (draw r2, selected-draw-ids r1)"
go test -count=1 ./x/mining/types/selectionv1/...

echo "consensus-vectors: reward conformance (reward r1)"
go test -count=1 -run '^TestRewardPackConformance$' ./x/rewards/keeper/

echo "consensus-vectors: OK"
