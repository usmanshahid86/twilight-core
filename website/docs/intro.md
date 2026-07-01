---
title: Twilight Chain Documentation
slug: /intro
---

# Twilight Chain Documentation

Twilight is a [Cosmos SDK](https://docs.cosmos.network/) / CometBFT
**Proof-of-Authority (PoA)** application chain. Validator admission and validator
updates are owned exclusively by the **CoreSlot** module (`x/coreslot`); the
standard staking, distribution, mint, slashing, and governance modules are omitted.

The chain mints scheduled block rewards through the **rewards** module
(`x/rewards`): each epoch, a `utwlt` reward pool is minted and allocated to the
active CoreSlot operators, then claimed to each operator's snapshotted payout
address.

Twilight Core is under active development — it is not yet mainnet-ready and has not
been externally audited. See [Status & Validation](chain/status-and-validation.md)
for what has been validated and what has not.

## What `utwlt` is

`utwlt` is the **only** denomination used for on-chain accounting (balances,
minting, rewards, claims). `twlt` / `TWLT` / "Twilight" are **display metadata
only** (6 decimals) and never appear in accounting state.

## Core capabilities

- **CoreSlot** (`x/coreslot`) — the sole authority over validator admission,
  lifecycle state, consensus keys, and validator-set updates.
- **Rewards** (`x/rewards`) — supply-threshold block emission, active-block
  participation allocation, epoch finalization, carry-forward remainder
  accounting, claim records, queued parameter updates, and emergency
  pause/resume.
- **Query and transaction surfaces** — CLI and gRPC/query interfaces for reading
  rewards state and submitting supported rewards transactions.

## Start here

- **[Getting Started](getting-started/overview.md)** — install, run a localnet,
  and query rewards.
- **[Rewards overview](rewards/overview.mdx)** — how block rewards are minted,
  distributed, and claimed.
- **[Chain architecture](chain/architecture.md)** — the PoA design and module
  layout.
