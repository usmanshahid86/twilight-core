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
epoch finalization and a real claim, and asserts cross-node app-hash agreement.
It prints a `PASS` summary with the minted emission, per-slot reward, and claim
transaction hash. See [Localnet](../chain/localnet.md) for what it proves (and
the funded-fixture caveat).

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

## 4. Claim a reward

```bash
twilightd rewards claim 1 1 1 --from <signer> \
  --chain-id <chain-id> --node tcp://127.0.0.1:26657 \
  --gas 600000 --fees 0utwlt --yes
```

Funds go to slot 1's **snapshotted payout address**, not necessarily the signer.
See [Claims](../rewards/claims.md).

## 5. Default localnet (startup only)

```bash
make localnet-smoke
```

This proves node startup and agreement on the **production default** profile; it
does **not** close a rewards epoch (the default epoch is 17,280 blocks).
