#!/usr/bin/env bash
# Builds release artifacts from the committed tree, and refuses anything else.
#
# A release artifact asserts it is a particular version built from a particular
# commit. Operators pre-stage binaries with cosmovisor's downloader disabled and
# verify them by hash, so a stamp that is confidently wrong is worse than one
# that is missing: the checksum hashes the artifact faithfully and cannot
# disclose that the source differed from the commit named on it.
#
# Two ways that happened while this was a Makefile recipe:
#
#   - dirtiness was a Make variable, and GNU Make lets a command-line assignment
#     override any assignment in the makefile. `make build-release DIRTY=` blanked
#     the guard and produced officially named artifacts from a modified tree.
#   - the guard consulted `git diff-index`, which sees tracked files only, and a
#     follow-up that enumerated .go/go.mod/go.sum still missed .s — the toolchain
#     also consumes .s, .c, .h and .syso, and //go:embed reaches any extension.
#     Untracked build inputs compiled into the binary while the tree reported clean.
#
# Both existed because the release was built from the mutable worktree. This
# builds from `git archive HEAD` instead, so the artifact is the commit it claims
# by construction: no Make variable reaches inside, and untracked files are absent
# from the archive rather than merely undetected. The guards below remain, because
# refusing early with a clear reason beats silently building something different
# from what the operator is looking at.
set -uo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

RELEASE_DIR="${RELEASE_DIR:-build/release}"
TARGETS="${RELEASE_TARGETS:-linux/amd64 linux/arm64 darwin/arm64}"

refuse() { echo "refusing to build a release: $1" >&2; shift; [[ $# -gt 0 ]] && printf '%s\n' "$@" >&2; exit 1; }

command -v git >/dev/null 2>&1 || refuse "git is required to establish provenance"
git rev-parse HEAD >/dev/null 2>&1 || refuse "not a git repository, so the commit cannot be established"

# --- guards, evaluated here rather than as Make variables so no caller can blank
# --- them from the command line.
if ! git diff-index --quiet HEAD -- 2>/dev/null; then
  refuse "uncommitted changes to tracked files" "$(git --no-pager diff --stat HEAD --)"
fi

# Any untracked file outside the allowlist. Enumerating build-relevant extensions
# does not close: the toolchain also consumes .s, .c, .h and .syso, and //go:embed
# can pull in a file of any extension. So this is default-deny, and the allowlist
# names what is known safe rather than guessing what is dangerous.
#
# docs/specs/ is the one entry — user-owned material this project keeps untracked
# by convention, which the compiler cannot reach. Everything gitignored, build/
# included, is already excluded by --exclude-standard.
UNTRACKED="$(git ls-files --others --exclude-standard -- . ':(exclude)docs/specs' 2>/dev/null)"
if [[ -n "$UNTRACKED" ]]; then
  refuse "untracked files present; a release is built only from a clean tree" "$UNTRACKED"
fi

COMMIT="$(git rev-parse HEAD)"
VERSION="${VERSION:-$(git describe --tags --always 2>/dev/null || echo unknown)}"
BUILD_TAGS="${BUILD_TAGS:-}"

# Source comes from the commit, not the working directory.
SRC="$(mktemp -d)"
trap 'rm -rf "$SRC"' EXIT
git archive HEAD | tar -x -C "$SRC" || refuse "could not export HEAD"

LDFLAGS="-X github.com/cosmos/cosmos-sdk/version.Version=$VERSION \
-X github.com/cosmos/cosmos-sdk/version.Commit=$COMMIT \
-X github.com/cosmos/cosmos-sdk/version.BuildTags=$BUILD_TAGS"

# Only now is anything written, so a refusal above leaves whatever was already
# in the release directory untouched rather than clearing it first.
rm -rf "$RELEASE_DIR" && mkdir -p "$RELEASE_DIR"
OUT="$ROOT/$RELEASE_DIR"

for t in $TARGETS; do
  os="${t%%/*}"; arch="${t##*/}"
  name="twilightd-$VERSION-$os-$arch"
  echo "  building $RELEASE_DIR/$name  (from $COMMIT)"
  ( cd "$SRC" && CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
      go build -trimpath -ldflags "$LDFLAGS" -o "$OUT/$name" ./cmd/twilightd ) \
    || refuse "build failed for $t"
done

( cd "$OUT" && { command -v sha256sum >/dev/null && sha256sum twilightd-* \
                 || shasum -a 256 twilightd-*; } > SHA256SUMS ) || refuse "could not write checksums"

echo
echo "  $RELEASE_DIR/SHA256SUMS"
cat "$OUT/SHA256SUMS"
