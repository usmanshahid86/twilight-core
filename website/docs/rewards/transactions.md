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
| `pause` | _(flags)_ | CoreSlot **emergency authority** |
| `resume` | _(flags)_ | CoreSlot **emergency authority** |
| `claim` | `[slot-id] [start-epoch] [end-epoch]` | **any** signer (pays snapshotted payout) |

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

Toggle the immediate runtime flags. At least one flag is required; each affects
only the named flag and takes effect immediately.

```bash
twilightd rewards pause  --emissions --settlement --claims --from <emergency-authority> ...
twilightd rewards resume --claims                          --from <emergency-authority> ...
```

| Flag | Effect of `pause` |
|---|---|
| `--emissions` | Finalization still runs but mints zero; cumulative emitted unchanged |
| `--settlement` | EndBlock does not finalize; active-block counting continues |
| `--claims` | Claim transactions are rejected; finalization unaffected |

`resume` re-enables the same flags. These do not touch pending params or closed
epochs. A no-flag invocation is rejected.

## `claim`

Triggers a claim for a slot over an inclusive epoch range; funds go to the
snapshotted payout address. Covered in detail under [Claims](claims.md).

```bash
twilightd rewards claim 1 1 1 --from <signer> --chain-id <chain-id> --node <rpc> --yes
```

## Failure cases

| Command | Failure |
|---|---|
| `update-params` | wrong authority; immutable field change; unsupported feature; invalid JSON |
| `pause` / `resume` | wrong emergency authority; no flags set |
| `claim` | see [Claims → failure cases](claims.md#failure-cases) |
