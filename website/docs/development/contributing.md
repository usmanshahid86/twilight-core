---
title: Contributing
---

# Contributing

## Ground rules for the chain

- **Determinism first.** No wall-clock time, randomness, environment variables, or
  CometBFT-local config in any state transition. Iterate sorted collections, never
  raw Go map order, before writing state, sending funds, or emitting events.
- **CoreSlot is the sole validator-update emitter.** Rewards (and any future
  module) must use the modern `appmodule` lifecycle (error-only) and must never
  return validator updates.
- **`utwlt` only in accounting.** Never let `twlt`/`TWLT`/`Twilight` enter
  accounting state; they are display metadata.
- **Immutable monetary fields.** `native_denom` and `max_supply` are immutable
  after genesis.
- **Fail-closed.** Rewards lifecycle faults must propagate (halt the block), never
  half-commit.

## Before opening a PR

```bash
gofmt -l .            # no output
go vet ./...
go test ./... -count=1
go build ./cmd/twilightd
```

If you change proto:

```bash
make proto
git diff --exit-code -- proto x/coreslot/types x/rewards/types
```

If you change anything multi-node-relevant, run:

```bash
make localnet-rewards-smoke
```

## Docs (this site)

The docs site lives in `website/` and is isolated from the Go build. Build it
separately:

```bash
cd website && npm install && npm run build   # onBrokenLinks/onBrokenAnchors: throw
```

Documentation rules:

- Trace every documented behavior to actual code, an actual CLI command, or an
  actual report. Otherwise mark it `:::note Pending Phase 11 confirmation`.
- No mainnet/production-ready claims. Distinguish production defaults from
  localnet/test settings. Keep the funded-fixture caveat where relevant.
- Single-source numbers via `website/docs/_snippets/constants.mdx`.
- CLI help captures under `website/generated/cli/` are audit artifacts; write
  clean reference tables, don't paste walls of `--help`.

## Commit / PR conventions

Keep changes scoped and reviewable. Do not bundle economics changes with docs or
wiring changes. See `CONTRIBUTING.md` in the repository if present for repo-level
conventions.
