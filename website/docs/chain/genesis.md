---
title: Genesis
---

# Genesis

The localnet fixtures described on this page fund accounts and are **not** a
production genesis. Production monetary genesis is expected to be zero-premine —
total supply rising only through epoch emission. See
[Status & Validation](status-and-validation.md) for maturity status.

## Module init order

`InitGenesis` runs in this order (set in `app/config.go`):

```text
auth → bank → consensus → coreslot → rewards
```

`auth` and `bank` initialize first so module accounts exist before rewards
genesis runs. CoreSlot precedes rewards. Rewards `InitGenesis` does **not** mint
or send — it only writes state.

## Module accounts created at genesis

| Account | Permission |
|---|---|
| `rewards` | `Minter` |
| `rewards_fee_pool` | _(none)_ |

These are created by the `auth` module from `ModuleAccountPermissions` in
`app/config.go`, not lazily. See [Module Accounts](../reference/module-accounts.md).

## Rewards default genesis

Fresh default rewards genesis (from `x/rewards/types/defaults.go`):

- `params` — production defaults (see [Parameters](../rewards/params.md)).
- `state` — `current_epoch = 1`, `current_epoch_start_height = 1`,
  `cumulative_emitted = "0"`, `carry_forward_remainder = "0"`.
- `current_epoch_config` — the epoch snapshot built from `params`.
- no pending params, no finalized epochs, no slot entitlements.

No premine: default genesis sets `cumulative_emitted = 0` and adds no rewards
balances. Total supply begins at zero and rises only through emission.

The full schema and a sample JSON are in
[Genesis Reference](../reference/genesis-reference.md).

## Inspecting genesis

```bash
twilightd init <moniker> --chain-id <chain-id>
# the generated genesis includes the rewards default genesis,
# because rewards is registered in the CLI basic manager.
jq '.app_state.rewards' ~/.twilightd/config/genesis.json
```

## Localnet fixtures (not production)

The localnet `init.sh` funds the authority and emergency accounts with
`1,000,000,000,000utwlt` each and registers four active CoreSlots. This is a
development fixture: it is funded (premined) and is **not** the production
zero-premine genesis. See [Localnet](localnet.md).
