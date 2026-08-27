# Multi-validator upgrade drill

Measured by `scripts/localnet/upgrade-drill.sh` (`make localnet-upgrade-drill`).
Four validators, two separately built binaries, one real coordinated halt.

The in-process tests in `app/` already prove the upgrade mechanism inside a single
application: schedule from a binary without the handler, ride the pre-height window,
halt, swap, resume, migrate once. None of that needs consensus, so none of it proves
what only goes wrong with more than one node. That is what this drill is for.

## What only this can establish

- every validator halts at the **same** height
- the upgraded nodes agree on the app hash across the boundary
- a validator left on the old binary **fails closed** rather than following the
  network with pre-migration logic
- wall-clock downtime does not consume the settlement clock
- entitlements, escrow and the validator set survive the boundary untouched

## The two binaries

Both are built from one source revision in the same run, and differ in exactly the
compiled upgrade registry:

| | build | registry |
|---|---|---|
| **A** | `go build ./cmd/twilightd` | ships as-is — does **not** contain `drill-v2` |
| **B** | `go build -tags upgradedrill ./cmd/twilightd` | adds `drill-v2` |

Their SHA-256 sums are recorded in `binaries.json`, because "which bytes ran" is the
question a binary-swap drill answers. A test asserts the production registry excludes
`drill-v2` — a released binary carrying a handler for an upgrade the network might
later schedule is a latent halt, since upstream aborts every block between the
scheduling transaction and that height on any node that already holds the handler.

## Why four validators

Quorum is more than 2/3 of voting power. At four, a partial rollout of three can
resume while the fourth is still down — the situation every real rollout passes
through. At two or three, every operator must succeed simultaneously and the
interesting case cannot be expressed at all.

## The migration is deliberately not idempotent

`drill-v2` moves CoreSlot's `key_rotation_delay_blocks` from 1 to 2 and **requires 1
on entry**. A migration that simply assigned 2 would prove only that it ran *at least*
once, and would pass identically if applied twice.

Because a second execution fails and halts the block, **a node that restarts and keeps
committing is itself the proof the migration did not run again**. The value is queryable
from every node, so the drill proves the migration from committed consensus state
rather than from an in-process counter.

## Two things that look like bugs and are not

**The halted node is hung, not gone.** x/upgrade returns an error from PreBlock,
CometBFT panics its consensus routine, and the process stays up serving RPC with
consensus dead. Waiting for the process to exit waits forever; treating an unreachable
RPC as the halt signal would let a genuine crash pass as success. The drill uses the
application height standing still while the node still answers, corroborated by the
upgrade-required line in its log.

**The application height and the block-store height diverge at the boundary — and only
the application one means "committed".** CometBFT stores a block once consensus agrees
on it, then asks the application to apply it. When x/upgrade refuses, the store holds H
while the application is still at H-1. Asserting against `/status` reads the
stored-but-unapplied block and reports a failure that did not happen, so the drill reads
`/abci_info`.

The same asymmetry explains why the stale validator's RPC is unreachable during the
negative proof: it refuses the boundary while replaying the stored block, before the RPC
server binds.

## Non-goals — stated so they are not mistaken for covered

**Cosmovisor is not exercised.** Production operation uses it, with automatic downloads
disabled and binaries pre-staged and hash-verified. The drill swaps binaries **manually
and deliberately**: it is proving chain upgrade semantics, and a Cosmovisor failure must
not be able to masquerade as a consensus or x/upgrade failure. Validating that tooling is
separate work, and this drill does not claim to have done it.

**Store-layout migration is not exercised.** `drill-v2` declares no `StoreUpgrades`, so
`Added`, `Deleted` and `Renamed` store changes remain unproven. That is a known gap, not
a covered case.

## Evidence

Written to `build/localnet/evidence/<run-id>/upgrade/`:

| file | contents |
|---|---|
| `binaries.json` | source commit, both build commands, both SHA-256 sums |
| `upgrade.jsonl` | per node and phase: heights, halt observation, upgrade-info, binary in use |
| `economics.jsonl` | clock at H-1/H/H+1, escrow, liability, carry, migration marker |
| `hashes.jsonl` | app / validators / next-validators hashes at each checkpoint |
| `nodeN-upgrade-info.json` | each validator's own halt metadata, preserved |

## Cost

Eight to ten minutes. A protocol epoch must close before a settlement can span the
boundary, and the epoch length is **not** shortened — only block time is. Deliberately
excluded from `make drills`, which would otherwise become too slow to run often.

## Related

- [ADR-0003](../architecture/adr/0003-upgrade-path.md) — the upgrade path decision
- [quorum-threshold-table.md](quorum-threshold-table.md) — why three of four is a quorum
