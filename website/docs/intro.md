---
title: Twilight Chain Documentation
slug: /intro
---

# Twilight Chain Documentation

Twilight is a [Cosmos SDK](https://docs.cosmos.network/) / CometBFT
**Proof-of-Authority (PoA)** application chain. Validator admission and validator
updates are owned exclusively by the **CoreSlot** module (`x/coreslot`); the
standard staking, distribution, slashing, and governance modules are omitted.

The chain mints scheduled block rewards through the **rewards** module
(`x/rewards`): each epoch, a `utwlt` reward pool is minted and allocated to the
active CoreSlot operators, then claimed to each operator's snapshotted payout
address.

:::note Current status
This documentation reflects the **Phase 10 validated** implementation
(app wiring, rewards economics, query/CLI, and a multi-node localnet finalization
+ claim proof). **Production zero-premine genesis and longer multi-epoch soak
drills remain Phase 11 items** and are not yet proven. Nothing here implies
mainnet readiness.
:::

## What `utwlt` is

`utwlt` is the **only** denomination used for on-chain accounting (balances,
minting, rewards, claims). `twlt` / `TWLT` / "Twilight" are **display metadata
only** (6 decimals) and never appear in accounting state.

## What is implemented today

- **CoreSlot PoA** — the sole authority over the validator set; the only module
  that emits validator updates.
- **x/rewards** — wired into the app runtime: supply-threshold block emission,
  uniform active-block distribution, epoch finalization, carry-forward,
  claim records, queued params updates, and emergency pause/resume.
- **Query & CLI surfaces** — read-only `rewards-query` commands and `rewards`
  transaction commands.
- **Validated proofs** — multi-node localnet rewards finalization + real claim
  transaction with cross-node app-hash agreement, full app export/import
  round-trip, and fail-closed lifecycle behavior.

## Still tracked for Phase 11

- Production **zero-premine** monetary-genesis localnet drill (the Phase 10 proof
  ran on a funded development fixture).
- Longer **multi-epoch soak** testing.
- Release-candidate operator drills and documentation hardening.

## Start here

- **[Rewards overview](rewards/overview.mdx)** — how block rewards are minted,
  distributed, and claimed.
- **Source:** the [`nyks-core` repository](https://github.com/twilight-project/nyks-core).

More sections (Getting Started, Chain Architecture, Operators, Reference,
Development) are being added; see the sidebar as it grows.
