#!/usr/bin/env bash
#
# Vulnerability gate. Fails on any reachable advisory that is not explicitly accepted.
#
# govulncheck's JSON mode always exits 0, so pass/fail is computed here from the findings
# themselves. An advisory counts as reachable when its trace resolves to a called symbol
# (trace[0].function is set); module- and package-level findings for code we do not call are
# ignored, which matches the gate described in REVIEW.md.
#
# Accepted advisories live in .govulncheck-allow.json and belong there only when the advisory
# is reachable AND has no available fix. Each entry expires on its review_by date, after which
# this script fails until the entry is re-justified or removed.
#
# Used by both `make vuln` and CI so the two cannot drift.

set -euo pipefail

# Pinned deliberately (like protoc and golangci-lint) so a new govulncheck release cannot
# change gating behaviour without a code change.
GOVULNCHECK_VERSION="v1.5.0"

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ALLOWFILE="${ROOT}/.govulncheck-allow.json"
TARGET="${1:-./...}"

command -v jq >/dev/null 2>&1 || { echo "vulncheck: jq is required but not installed" >&2; exit 2; }

TMP="$(mktemp -d)"
trap 'rm -rf "${TMP}"' EXIT

echo "vulncheck: scanning ${TARGET} with govulncheck ${GOVULNCHECK_VERSION}"
go run "golang.org/x/vuln/cmd/govulncheck@${GOVULNCHECK_VERSION}" -format json "${TARGET}" \
  > "${TMP}/scan.json"

# Guard against a silent false green. govulncheck always emits a config message, so if the
# output cannot be parsed as expected -- an upstream format change, a truncated run -- the
# reachable set would come back empty and every advisory would look accepted. Fail instead.
if ! jq -e 'select(.config)' "${TMP}/scan.json" >/dev/null 2>&1; then
  echo "vulncheck: FAIL could not parse govulncheck output (no config message)." >&2
  echo "           Refusing to report a clean scan; check the govulncheck version/format." >&2
  exit 2
fi

# Advisories whose vulnerable symbols are actually called.
jq -r 'select(.finding and (.finding.trace[0].function != null)) | .finding.osv' \
  "${TMP}/scan.json" | sort -u > "${TMP}/reachable"

if [ -f "${ALLOWFILE}" ]; then
  jq -r '.allow[].id' "${ALLOWFILE}" | sort -u > "${TMP}/allowed"
else
  : > "${TMP}/allowed"
fi

status=0
today="$(date -u +%Y-%m-%d)"

# 1. Expired acceptances. An exception must not outlive its review date.
if [ -f "${ALLOWFILE}" ]; then
  while IFS="$(printf '\t')" read -r id review_by; do
    [ -z "${id}" ] && continue
    if [ -z "${review_by}" ] || [ "${review_by}" = "null" ]; then
      echo "vulncheck: FAIL ${id} has no review_by date" >&2
      status=1
    elif [[ "${review_by}" < "${today}" ]]; then
      echo "vulncheck: FAIL ${id} acceptance expired on ${review_by} (today ${today})." >&2
      echo "           Re-justify or remove it in .govulncheck-allow.json." >&2
      status=1
    fi
  done < <(jq -r '.allow[] | [.id, .review_by] | @tsv' "${ALLOWFILE}")
fi

# 2. Reachable advisories that were never accepted.
unaccepted="$(comm -23 "${TMP}/reachable" "${TMP}/allowed" || true)"
if [ -n "${unaccepted}" ]; then
  while read -r id; do
    [ -z "${id}" ] && continue
    echo "vulncheck: FAIL ${id} is reachable and not accepted" >&2
    jq -r --arg id "${id}" \
      'select(.osv and .osv.id == $id) | "           " + (.osv.summary // .osv.details // "")' \
      "${TMP}/scan.json" | head -1 >&2
  done <<EOF
${unaccepted}
EOF
  status=1
fi

# 3. Acceptances that are no longer reachable. Warn only: an upstream fix landing is a good
#    outcome and must not turn CI red, it just needs tidying.
stale="$(comm -13 "${TMP}/reachable" "${TMP}/allowed" || true)"
if [ -n "${stale}" ]; then
  while read -r id; do
    [ -z "${id}" ] && continue
    echo "vulncheck: note ${id} is accepted but no longer reachable; remove it from .govulncheck-allow.json"
  done <<EOF
${stale}
EOF
fi

if [ "${status}" -eq 0 ]; then
  accepted="$(wc -l < "${TMP}/allowed" | tr -d ' ')"
  echo "vulncheck: OK - no unaccepted reachable advisories (${accepted} accepted)"
fi

exit "${status}"
