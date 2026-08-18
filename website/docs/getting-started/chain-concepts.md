---
title: Chain Concepts
---

# Chain Concepts

A compact mental model of how Twilight fits together.

## Validators are slots, not stakers

There is no staking. The validator set is a fixed-capacity set of **CoreSlots**.
A CoreSlot is admitted and activated by the CoreSlot authority and carries an
operator address, a payout address, a consensus key, and a reward weight. Only
`ACTIVE` slots vote and earn rewards. CoreSlot is the **only** module that changes
the validator set.

## Rewards are minted per block, distributed per epoch

Every block, the rewards module credits each active slot one **active block** for
the open epoch. At the epoch boundary it **finalizes**: it mints the block
subsidy summed over the epoch (clipped by the supply cap), builds a pool, and
allocates it by each slot's active-block participation. The minted amount depends on
blocks and the halving tier — not on the slot count.

## Rewards are escrowed, not auto-paid

Finalization writes one immutable per-slot **entitlement** snapshotting the slot's
payout address and the amount it earned. The value stays in the rewards module
account until **settlement** releases it: `x/mining` pays the epoch's participants
and returns the remainder to the snapshotted payout address. A release moves
already-minted balance and never changes total supply.

## Two authorities

- **Authority** (CoreSlot) — admits slots and queues rewards params updates
  (activated at the next epoch boundary).
- **Emergency authority** (CoreSlot) — can pause and resume rewards. There is one
  canonical pause state, effective at the start of the next block; it stops accrual
  and release together, but not epoch time.

Neither can change the immutable `native_denom` or `max_supply`.

## One denom

`utwlt` is the only accounting denom. `twlt`/`TWLT` are display metadata.

## Fail-closed

A rewards lifecycle fault halts the block rather than half-committing — a
deliberate safety posture for a monetary module. See
[Security & Failure Modes](../rewards/security-and-failure-modes.md).

## Next

- [Rewards economics](../rewards/economics.mdx)
- [Consensus & CoreSlot](../chain/consensus-and-coreslot.md)
- [Glossary](glossary.md)
