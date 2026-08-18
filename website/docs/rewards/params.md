---
title: Parameters
---

# Rewards Parameters

The rewards `Params` are stored on-chain and updated via
[`update-params`](transactions.md#update-params) (CoreSlot authority, queued to
the next epoch boundary) or, for the three runtime flags, via
[`pause`/`resume`](transactions.md#pause--resume) (CoreSlot emergency authority,
immediate). Rewards params store **no authority field** of their own — authority
and emergency authority are read from CoreSlot.

Numeric defaults (max supply, subsidy, epoch length, denom, distribution mode)
are listed under [Economics](economics.mdx#production-defaults).

## Field reference

JSON field names are the proto snake-case names used in genesis and
`update-params` files.

| Field | Type | Mutable? | Notes |
|---|---|---|---|
| `native_denom` | string | **Immutable** | Must be `utwlt`. Rejected if changed. |
| `max_supply` | string (int) | **Immutable** | Hard supply cap. Rejected if changed. |
| `initial_block_subsidy` | string (int) | Mutable (queued) | Tier-0 per-block subsidy. Monetary-policy sensitive. |
| `target_block_time_seconds` | uint64 | Mutable (queued) | Informational target block time. |
| `epoch_length_blocks` | uint64 | Mutable (queued) | Blocks per epoch. The current epoch settles under its snapshot; the new value applies to the next epoch. |
| `halving_mode` | enum | Mutable (queued) | Only `HALVING_MODE_SUPPLY_THRESHOLD` is supported. |
| `distribution_method` | enum | Mutable (queued) | Only `DISTRIBUTION_METHOD_UNIFORM_ACTIVE_BLOCKS` is supported in v1. |
| `remainder_policy` | enum | Mutable (queued) | Only `REMAINDER_POLICY_CARRY_FORWARD` is supported in v1. |
| `max_claim_epochs_per_tx` | uint64 | **Deprecated** | Bounded a claim transaction's epoch span. The claim path is retired, so this gates nothing; validation still requires it to be nonzero so existing genesis files round-trip. |
| `emissions_enabled` | bool | **Deprecated** | Superseded by the canonical pause state; carries no authority. |
| `epoch_settlement_enabled` | bool | **Deprecated** | Superseded by the canonical pause state; carries no authority. |
| `claims_enabled` | bool | **Deprecated** | Superseded by the canonical pause state; carries no authority. |
| `fee_collection_enabled` | bool | Mutable (queued) | Must be `false` in v1 (enabling is rejected). |
| `fee_distribution_enabled` | bool | Mutable (queued) | Must be `false` in v1 (enabling is rejected). |
| `fee_denom` | string | Mutable (queued) | Must equal `native_denom` (`utwlt`). |
| `fee_distribution_mode` | enum | Mutable (queued) | Must be `FEE_DISTRIBUTION_MODE_NONE` in v1. |
| `treasury_address` | string | Mutable (queued) | Required (valid Twilight address) only if a treasury share > 0. |
| `emission_treasury_share_bps` | uint64 | Mutable (queued) | Basis points (≤ 10000) of emission sent to treasury. Default 0. |
| `fee_treasury_share_bps` | uint64 | Mutable (queued) | Inert while fees are disabled. ≤ 10000. Default 0. |
| `weighted_rewards_enabled` | bool | Mutable (queued) | Must be `false` in v1 (enabling is rejected). |

The `Params` proto reserves field 1 (the former authority field); authority is
not stored in rewards.

## Mutability summary

- **Immutable forever:** `native_denom`, `max_supply`.
- **Immediate (emergency authority):** the canonical pause state, set by
  `rewards pause` / `rewards resume` and effective at H+1. It is not a param.
- **Deprecated, on the wire only:** `emissions_enabled`,
  `epoch_settlement_enabled`, `claims_enabled`, `max_claim_epochs_per_tx`,
  `epoch_length_blocks`. Their field numbers are never recycled.
- **Queued (normal authority, activates next epoch boundary):** everything else.

## v1 feature guards

Params validation rejects: a non-`utwlt` denom, a fee denom ≠ native denom,
enabling fee collection/distribution, a non-`NONE` fee mode, weighted rewards,
non-uniform distribution, non-supply-threshold halving, BPS values > 10000, and a
treasury share > 0 without a valid treasury address.
