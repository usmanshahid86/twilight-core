---
title: Transactions
---

# Rewards Transactions

Rewards transactions follow the top-level `twilightd rewards <command>`
convention. They wrap the existing messages only; no message infers or bypasses
authority — the keeper enforces it. Raw `--help` captures are under
`website/generated/cli/`.

## Commands

| Command | Args | Authority required |
|---|---|---|
| `update-params` | `[params-json-file]` | CoreSlot **authority** |
| `pause` | _(none)_ | CoreSlot **emergency authority** |
| `resume` | _(none)_ | CoreSlot **emergency authority** |

The `--from`, `--chain-id`, `--node`, `--gas`, `--fees`, `--yes` flags are the
standard Cosmos tx flags.

## `update-params`

Queues a full `Params` update from a JSON file. The update is **queued** in
`PendingParams` and activates at the **next epoch boundary**; the current epoch
settles under its existing snapshot.

```bash
twilightd rewards update-params ./params.json --from <authority> \
  --chain-id <chain-id> --node <rpc> --yes
```

The simplest way to produce a valid `params.json` is to query the current params
and edit it:

```bash
twilightd rewards-query params --node <rpc> --output json > params.json
# edit mutable fields, then submit
```

:::warning Immutable fields
`native_denom` and `max_supply` are **immutable** after genesis. The keeper
rejects any update that changes them, and rejects enabling unsupported v1
features (weighted rewards, fee collection/distribution, non-`NONE` fee mode,
distribution methods other than `DISTRIBUTION_METHOD_UNIFORM_ACTIVE_BLOCKS`). Do not attempt to change the denom or cap via params.
:::

See [Parameters](params.md) for the full field list and mutability.

## `pause` / `resume`

Toggle the single canonical pause state. There are no per-area selectors: pausing
stops reward accrual and release together.

```bash
twilightd rewards pause  --from <emergency-authority> ...
twilightd rewards resume --from <emergency-authority> ...
```

A transition accepted in block H takes effect at the beginning of H+1, before any
reward sampling, so a block's reward treatment is a property of the block rather
than of the order its transactions happened to execute in.

Pausing does not stop epoch time: epoch numbering advances and epochs still
finalize. A fully paused epoch counts zero reward-enabled blocks and emits
nothing. These commands do not touch pending params or closed epochs.

## Failure cases

| Command | Failure |
|---|---|
| `update-params` | wrong authority; immutable field change; unsupported feature; invalid JSON |
| `pause` / `resume` | wrong emergency authority |
