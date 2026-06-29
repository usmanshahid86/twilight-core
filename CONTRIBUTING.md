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

Requires **Go 1.23.x** (see `go.mod`).

```bash
make build     # go build ./cmd/twilightd
make test      # go test ./...
make fmt       # gofmt
make lint      # golangci-lint (matches CI)
make proto     # regenerate protobuf (only if you changed proto/)
```

Local end-to-end and chaos checks:

```bash
make localnet-smoke           # 4-node localnet sanity
make localnet-rewards-smoke   # rewards finalize + claim
make drills                   # lifecycle + restart-rotation + quorum drills
```

## Branching & commits

- Work on a feature branch off **`develop`**; `develop` merges to **`main`** for releases.
- Use **[Conventional Commits](https://www.conventionalcommits.org/)** — e.g.
  `feat(rewards): ...`, `fix(coreslot): ...`, `docs: ...`, `chore(ci): ...`.
- Keep PRs focused and reviewable; describe the *why*, not just the *what*.
- If you changed `proto/`, regenerate with `make proto` and commit the result.
- If you changed behavior, add/adjust tests; consensus/economic changes should also be
  covered by a drill or simulation where practical.

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
