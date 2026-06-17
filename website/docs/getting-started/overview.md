---
title: Overview
---

# Getting Started — Overview

Twilight is a minimal Cosmos SDK / CometBFT **Proof-of-Authority** chain:

- **CoreSlot** (`x/coreslot`) owns the validator set — the only module that emits
  validator updates. There is no staking, delegation, or slashing.
- **Rewards** (`x/rewards`) mints scheduled `utwlt` block rewards each epoch and
  pays them to the active operators' snapshotted payout addresses.

If you are new, read in this order:

1. [Install](install.md) — build `twilightd`.
2. [Quickstart](quickstart.md) — run a localnet and query rewards.
3. [Chain concepts](chain-concepts.md) — the mental model.
4. [Glossary](glossary.md) — terminology.

Then dive into the [Rewards overview](../rewards/overview.mdx),
[Chain architecture](../chain/architecture.md), or the
[Operator guides](../operators/node-operator-guide.md).

:::note Current status
Implementation is validated through Phase 10 (including a multi-node localnet
finalization + claim proof). Production zero-premine genesis and longer soak
drills remain Phase 11 — see [Release Readiness](../chain/release-readiness.md).
:::
