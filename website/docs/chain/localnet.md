---
title: Localnet
---

# Localnet

Two four-node smoke targets exist. They cover different things — do not
conflate them.

## `make localnet-smoke` (default)

Starts a four-node network on the **production default** profile and checks that
all nodes agree.

**Covers:** node startup and basic cross-node agreement (app hash, validators
hash, next-validators hash) at a common height.

**Does not cover:** rewards finalization. The default rewards epoch is 17,280
blocks, so the default smoke (which reaches a low height) never closes a rewards
epoch.

```bash
make localnet-smoke
```

## `make localnet-rewards-epoch-smoke`

Starts an **isolated** four-node network (`/tmp/twilight-rewards-localnet`) and
drives it through a full epoch boundary.

**Covers:** rewards finalization, exact minting, active-block participation
allocation, the entitlements the epoch creates, and cross-node app-hash agreement
before and after finalization.

This is **not a money-movement proof**. It closes an epoch and checks the
obligation the epoch produced; it submits no settlement chunk, so no value is
released. The transaction that releases participant value is
`MsgSubmitSettlementChunk` in `x/mining`.

```bash
make localnet-rewards-epoch-smoke
```

The script (`scripts/localnet/rewards-smoke.sh`) edits **only its own isolated
generated genesis**; production defaults and the default smoke are untouched. The
epoch length must sit inside the ratified immutable interval [360, 720], so the
run uses a fast block time rather than a short epoch.

### What a run produces

| Stage | Result |
|---|---|
| Pre-finalization | epoch 1 open; 4 active-block rows; module balance 0; 4-node hash agreement |
| After finalization | epoch advances to 2; minted `149,828,400utwlt` (= 360 × 416,190); 4 entitlements × `37,457,100utwlt`; escrow holds the full emission; carry 0; 4-node hash agreement |

The minted emission is **per block over the epoch** (`360 × 416,190`), not per
slot — distribution then splits the minted pool across the 4 active slots. See
[Rewards economics](../rewards/economics.mdx).

:::warning Funded development fixture
The rewards smoke runs on a **funded** development fixture (the localnet funds two
accounts with `1,000,000,000,000utwlt` each, so total supply after finalization is
`2,000,004,161,900utwlt`). It exercises deterministic rewards behavior and exact
supply accounting **under that fixture**. A production **zero-premine**
monetary-genesis run is a separate case — see
[Status & Validation](status-and-validation.md).
:::

## Cross-node agreement check

`scripts/localnet/agree.sh` queries every node and verifies they agree on the
**app hash**, **validators hash**, and **next-validators hash** at a common
height. App-hash divergence is the catastrophic failure it guards against — a
silent state fork. The rewards smoke runs this check at each state transition.
