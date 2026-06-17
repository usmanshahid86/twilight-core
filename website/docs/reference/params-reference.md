---
title: Params Reference
---

# Params Reference

The full field-by-field `Params` table (types, defaults, effects) lives in
[Rewards → Parameters](../rewards/params.md). This page is the **mutability and
guard** reference — the machine view an auditor or tooling author needs.

## Mutability classification

| Class | Fields | Changed by | Timing |
|---|---|---|---|
| **Immutable** | `native_denom`, `max_supply` | nobody (rejected) | — |
| **Immediate** | `emissions_enabled`, `epoch_settlement_enabled`, `claims_enabled` | CoreSlot **emergency authority** | immediate |
| **Queued** | all other fields | CoreSlot **authority** | next epoch boundary |

Rewards `Params` store **no** authority field (proto reserves field 1); both
authorities are read from CoreSlot.

## v1 validation guards

`Params.Validate()` (in `x/rewards/types/validation.go`) rejects:

- `native_denom` ≠ `utwlt`;
- `fee_denom` ≠ `native_denom`;
- `max_supply` or `initial_block_subsidy` not positive;
- `epoch_length_blocks`, `target_block_time_seconds`, or `max_claim_epochs_per_tx`
  zero;
- `halving_mode` ≠ `HALVING_MODE_SUPPLY_THRESHOLD`;
- `distribution_method` ≠ `DISTRIBUTION_METHOD_UNIFORM_ACTIVE_BLOCKS`;
- `remainder_policy` ≠ `REMAINDER_POLICY_CARRY_FORWARD`;
- `fee_distribution_mode` ≠ `FEE_DISTRIBUTION_MODE_NONE`;
- `fee_collection_enabled`, `fee_distribution_enabled`, or
  `weighted_rewards_enabled` set to `true`;
- `emission_treasury_share_bps` or `fee_treasury_share_bps` > `10000`;
- a positive treasury share with an empty/invalid `treasury_address`.

`update-params` additionally rejects any change to the immutable fields via
`ValidateUpdate`.

## Supported enum values (v1)

| Enum | Supported value |
|---|---|
| `halving_mode` | `HALVING_MODE_SUPPLY_THRESHOLD` |
| `distribution_method` | `DISTRIBUTION_METHOD_UNIFORM_ACTIVE_BLOCKS` |
| `remainder_policy` | `REMAINDER_POLICY_CARRY_FORWARD` |
| `fee_distribution_mode` | `FEE_DISTRIBUTION_MODE_NONE` |

Other enum values exist in the proto (e.g. weighted distribution, fee modes) but
are **reserved for future upgrades** and rejected if activated in v1.
