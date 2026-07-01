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

## Rewards are claimed, not auto-paid

Finalization writes immutable per-slot **claim records** snapshotting each slot's
payout address and amount. Anyone can submit a claim transaction; funds always go
to the snapshotted payout address. Claims move already-minted balance and never
change total supply.

## Two authorities

- **Authority** (CoreSlot) — admits slots and queues rewards params updates
  (activated at the next epoch boundary).
- **Emergency authority** (CoreSlot) — can immediately pause/resume rewards
  emissions, settlement, or claims.

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
