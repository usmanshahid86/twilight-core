---
title: Quickstart
---

# Quickstart

This walks through running a localnet, finalizing a rewards epoch, and querying
state. It uses the isolated short-epoch rewards localnet.

## 1. Build

```bash
make build
```

## 2. Run the rewards localnet

```bash
make localnet-rewards-smoke
```

This starts a four-node network with a short rewards epoch, drives it through
epoch finalization, and asserts cross-node app-hash agreement. It prints a `PASS`
summary with the minted emission, the per-slot reward, and the entitlements the
epoch created. See [Localnet](../chain/localnet.md) for what it covers (and the
funded-fixture caveat).

## 3. Query rewards state

Against a running node (default first-node RPC `tcp://127.0.0.1:26657`):

```bash
twilightd rewards-query params --node tcp://127.0.0.1:26657
twilightd rewards-query epoch-info --node tcp://127.0.0.1:26657
twilightd rewards-query epoch-reward 1 --node tcp://127.0.0.1:26657
twilightd rewards-query slot-rewards 1 --limit 10 --node tcp://127.0.0.1:26657
twilightd rewards-query module-balances --node tcp://127.0.0.1:26657
twilightd rewards-query cumulative-emitted --node tcp://127.0.0.1:26657
```

See [Queries](../rewards/queries.md) for all commands and output fields.

## 4. How rewards are released

Rewards accrue at finalization as a per-slot **entitlement** held in the rewards
module account. There is no claim transaction: entitlements are released by
settlement in `x/mining`, which pays participants and returns the remainder to
the operator. See [Epoch Lifecycle](../rewards/epoch-lifecycle.md).

## 5. Default localnet (startup only)

```bash
make localnet-smoke
```

This confirms node startup and agreement on the **production default** profile; it
does **not** close a rewards epoch (the default epoch is 17,280 blocks).
