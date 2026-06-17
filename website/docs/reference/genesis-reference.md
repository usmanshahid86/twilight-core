---
title: Genesis Reference
---

# Genesis Reference

The rewards genesis schema (`x/rewards/types`, proto `twilight.rewards.v1`). For
how genesis is initialized and a non-reference overview, see
[Chain → Genesis](../chain/genesis.md).

## `GenesisState` fields

| Field | Type | Notes |
|---|---|---|
| `params` | `Params` | Rewards parameters ([reference](params-reference.md)) |
| `state` | `RewardsState` | Current epoch counters |
| `current_epoch_config` | `EpochConfigSnapshot` | The open epoch's frozen config |
| `pending_params` | `Params` | Present only if a params update is queued |
| `has_pending_params` | bool | Presence flag for `pending_params` |
| `finalized_epochs` | `EpochReward[]` | Immutable finalized epoch aggregates |
| `claim_records` | `EligibleSlotReward[]` | Per-`(slot, epoch)` claim rows |

### `RewardsState`

| Field | Type |
|---|---|
| `current_epoch` | uint64 |
| `current_epoch_start_height` | uint64 |
| `cumulative_emitted` | string (int) |
| `carry_forward_remainder` | string (int) |

## Default genesis

```json
{
  "params": { "native_denom": "utwlt", "max_supply": "21000000000000",
              "initial_block_subsidy": "416190", "epoch_length_blocks": "17280",
              "distribution_method": "DISTRIBUTION_METHOD_UNIFORM_ACTIVE_BLOCKS",
              "remainder_policy": "REMAINDER_POLICY_CARRY_FORWARD",
              "emissions_enabled": true, "epoch_settlement_enabled": true,
              "claims_enabled": true, "fee_collection_enabled": false,
              "fee_distribution_enabled": false, "fee_denom": "utwlt",
              "fee_distribution_mode": "FEE_DISTRIBUTION_MODE_NONE",
              "weighted_rewards_enabled": false, "treasury_address": "",
              "emission_treasury_share_bps": "0", "fee_treasury_share_bps": "0" },
  "state": { "current_epoch": "1", "current_epoch_start_height": "1",
             "cumulative_emitted": "0", "carry_forward_remainder": "0" },
  "current_epoch_config": { "...": "built from params" },
  "has_pending_params": false
}
```

:::note
The snippet above illustrates the shape. The authoritative default genesis is the
one emitted by `twilightd init` (rewards is in the CLI basic manager); always
generate and inspect the real file:

```bash
twilightd init <moniker> --chain-id <id>
jq '.app_state.rewards' ~/.twilightd/config/genesis.json
```
:::

## Validation rules (genesis)

- `cumulative_emitted` ≤ `max_supply`;
- `native_denom` must be `utwlt`;
- a claim record must reference a finalized epoch;
- no duplicate finalized epoch or claim record;
- standard `Params` validation ([params reference](params-reference.md)).

No premine: a production-shaped default has `cumulative_emitted = 0` and no
rewards balances. Localnet fixtures that fund accounts are development-only.
