---
title: Localnet
---

# Localnet

Two four-node localnet targets exist. They prove different things — do not
conflate them.

## `make localnet-smoke` (default)

Starts a four-node network on the **production default** profile and checks that
all nodes agree.

**Proves:** node startup and basic cross-node agreement (app hash, validators
hash, next-validators hash) at a common height.

**Does not prove:** rewards finalization. The default rewards epoch is 17,280
blocks, so the default smoke (which reaches a low height) never closes a rewards
epoch.

```bash
make localnet-smoke
```

## `make localnet-rewards-smoke`

Starts an **isolated** four-node network (`/tmp/twilight-rewards-localnet`) with a
short rewards epoch and drives it through epoch finalization and a real claim.

**Proves:** rewards finalization, exact minting, uniform distribution, a real
claim transaction, and cross-node app-hash agreement before finalization, after
finalization, and after the claim.

```bash
make localnet-rewards-smoke
```

The script (`scripts/localnet/rewards-smoke.sh`) edits **only its own isolated
generated genesis** to set `epoch_length_blocks = 10`; production defaults and the
default smoke are untouched.

### What it observed (Phase 10)

| Stage | Height | Result |
|---|---|---|
| Pre-finalization | 2 | epoch 1 open; 4 active-block rows; module balance 0; 4-node hash agreement |
| After finalization | 10 | epoch advances to 2; minted `4,161,900utwlt` (= 10 × 416,190); 4 slots × `1,040,475utwlt`; carry 0; 4-node hash agreement |
| After claim | 13 | slot 1 claimed; module balance `3,121,425utwlt`; supply unchanged; double-claim fails; 4-node hash agreement |

The minted emission is **per block over the epoch** (`10 × 416,190`), not per
slot — distribution then splits the minted pool across the 4 active slots. See
[Rewards economics](../rewards/economics.mdx).

:::warning Funded development fixture
The Phase 10 rewards smoke ran on a **funded** development fixture (the localnet
funds two accounts with `1,000,000,000,000utwlt` each, so total supply after
finalization was `2,000,004,161,900utwlt`). It proves deterministic rewards
behavior and exact supply accounting **under that fixture**. A production
**zero-premine** monetary-genesis drill remains a Phase 11 item — see
[Status & Validation](status-and-validation.md).
:::

## Cross-node agreement check

`scripts/localnet/agree.sh` queries every node and verifies they agree on the
**app hash**, **validators hash**, and **next-validators hash** at a common
height. App-hash divergence is the catastrophic failure it guards against — a
silent state fork. The rewards smoke runs this check at each state transition.
