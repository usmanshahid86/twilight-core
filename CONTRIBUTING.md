# Contributing to Twilight Core

Thanks for your interest in Twilight Core — a Cosmos SDK / CometBFT Proof-of-Authority
chain (`x/coreslot` for validator admission, `x/rewards` for the emission economy). The
standard staking, slashing, gov, mint, and distribution modules are **intentionally
omitted** (not wired into the app) — the validator set and token economics are handled
entirely by `x/coreslot` and `x/rewards`.

This is a young project under active development. Contributions are welcome; because the
code is consensus- and value-critical, the bar for changes to `x/coreslot`, `x/rewards`,
and `app/` is high.

## Before you start

- For anything non-trivial, **open an issue first** to discuss the approach.
- For a **security vulnerability, do not open an issue** — follow
  [`SECURITY.md`](SECURITY.md).
- Read the design background in [`docs/architecture/`](docs/architecture/) (ADRs) and the
  module docs on the documentation site under [`website/`](website/).

## Development setup

Requires **Go 1.25.x** (see `go.mod`).

```bash
make build     # stamped binary at build/twilightd
make test      # go test ./...
make fmt       # gofmt
make lint      # golangci-lint (matches CI)
make proto     # regenerate protobuf (only if you changed proto/)
```

Local end-to-end and chaos checks:

```bash
make localnet-smoke           # 4-node localnet sanity
make localnet-rewards-epoch-smoke  # rewards epoch finalization + entitlements
make drills                   # lifecycle + restart-rotation + quorum drills
```

## Branching & commits

- Work on a feature branch off **`main`**, and open a PR back into `main`. There is no
  long-lived integration branch: `main` is the trunk, and every merge is a merge commit
  (never a squash or rebase) so a reviewed head stays identifiable in the history.
- A release is a **tag on `main`**, not a branch promotion.
- Use **[Conventional Commits](https://www.conventionalcommits.org/)** — e.g.
  `feat(rewards): ...`, `fix(coreslot): ...`, `docs: ...`, `chore(ci): ...`.
- Keep PRs focused and reviewable; describe the *why*, not just the *what*.
- If you changed `proto/`, regenerate with `make proto` and commit the result.
- If you changed behavior, add/adjust tests; consensus/economic changes should also be
  covered by a drill or simulation where practical.

## Releases

Versions follow the two-class rule in
[ADR-0003](docs/architecture/adr/0003-upgrade-path.md):

- **minor** (`v0.2.0`, `v0.3.0`, …) — a state-machine change. Ships a registered upgrade
  handler **named after the version it upgrades to**, and needs a coordinated halt.
- **patch** (`v0.1.1`, `v0.2.1`, …) — node-local only: pruning, RPC, indexer, p2p. No
  upgrade handler; operators roll one at a time.

`v0.1.0` is the **public genesis build** — the first version carrying `x/upgrade`, with the
upgrade proven end to end and export/restore characterized. Nothing upgrades *to* it, so it
registers no handler. Earlier commits carry descriptive tags rather than version numbers,
because a chain launched from a build without `x/upgrade` can never be upgraded and numbering
it would imply a migration path that does not exist.

The handler registry in `app/upgrades.go` is **append-only**: a released name can never be
renamed or edited, because a syncing node must replay the same handler at the same height.

Each release publishes **SHA-256 checksums** for its binaries. Operators run cosmovisor with
`DAEMON_ALLOW_DOWNLOAD_BINARIES=false` and verify pre-staged binaries by hash, so the
checksum is the artifact that matters, not the download.

`make build` stamps the version and commit at link time and writes to
`build/twilightd`; `twilightd version --long` reports them. The chain and binary names are
compiled in, so even an unstamped `go build ./cmd/twilightd` identifies itself — an
unstamped build reports an empty version, which is honest, because it was not released.

`make build-release` produces the release artifacts and their checksums:

```bash
make build-release VERSION=v0.1.0
```

It refuses a tree with uncommitted changes to tracked files, before anything is built: an
artifact named for a version but built from uncommitted work would report a commit its source
does not match, and the checksum would hash it faithfully without disclosing that. `make build`
stays usable on a dirty tree and appends `-dirty` to whatever version it is given.
`make check-release-stamping` covers both.

Binaries target the platforms validators actually run:

| target | why |
|---|---|
| `linux/amd64` | the dominant validator platform |
| `linux/arm64` | Graviton/Ampere validators |
| `darwin/arm64` | developer convenience; not for validators |

They build `CGO_ENABLED=0`, so the artifacts are static and the default `goleveldb` backend
is used. RocksDB is an indirect dependency and is not compiled in without its build tag.

## Review & quality gates

Every change must pass **CI** (build, tests, `golangci-lint`, gofmt, `go mod tidy`) before
merge. We additionally run a multi-model review pass on changes; see [`REVIEW.md`](REVIEW.md)
for the process and the PR checklist. Consensus-critical changes require maintainer
approval.

## Determinism rules (important for a chain)

State-machine code must be **deterministic** across nodes:

- No `time.Now()`, `rand`, goroutines, or map-iteration-order dependence in any
  consensus path (BeginBlock/EndBlock/Msg handlers/keeper state transitions). Sort before
  iterating maps.
- Fail **closed**: on an unexpected condition in finalization, return an error so the
  block fails safely with **no partial state committed** — finalization runs in a cache
  context that is only written on full success — rather than committing inconsistent state.
- Never introduce a second source of `ValidatorUpdate`s — the validator set is owned
  exclusively by `x/coreslot`.

## License & DCO

By contributing, you agree your contributions are licensed under the project's
[Apache-2.0](LICENSE) license. Please sign off your commits (`git commit -s`,
[Developer Certificate of Origin](https://developercertificate.org/)).

## Code of Conduct

This project follows the [Code of Conduct](CODE_OF_CONDUCT.md). By participating you are
expected to uphold it.
