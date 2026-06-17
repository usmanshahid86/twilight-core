---
title: Rewards Tx API
---

# Rewards Tx API

Authoritative per-message reference for `twilightd rewards`. Narrative usage is in
[Transactions](../rewards/transactions.md). Raw `--help` captures:
`website/generated/cli/rewards-*-help.txt`. The underlying messages are in
`twilight.rewards.v1`.

## `update-params [params-json-file]` → `MsgUpdateRewardsParams`

| Field | Value |
|---|---|
| `authority` | from `--from`; must equal the CoreSlot authority |
| `params` | decoded from the JSON file (full `Params`) |

- Queues `PendingParams`; activates at the next epoch boundary.
- Rejected if `authority` ≠ CoreSlot authority, if it changes immutable
  `native_denom`/`max_supply`, or if it enables an unsupported v1 feature.

## `pause` → `MsgPauseRewards`

| Field | Source |
|---|---|
| `emergency_authority` | `--from`; must equal CoreSlot emergency authority |
| `pause_emissions` | `--emissions` |
| `pause_epoch_settlement` | `--settlement` |
| `pause_claims` | `--claims` |

- Immediate; toggles only the requested flags.
- Rejected if not the emergency authority, or if no flag is set.

## `resume` → `MsgResumeRewards`

Same shape as `pause` with `resume_*` fields and `--emissions/--settlement/--claims`.

## `claim [slot-id] [start-epoch] [end-epoch]` → `MsgClaimRewards`

| Field | Source |
|---|---|
| `signer` | `--from` (any valid account) |
| `slot_id` | arg 1 |
| `start_epoch` | arg 2 |
| `end_epoch` | arg 3 |

- Pays each record's snapshotted payout address (grouped by payout across a
  range); the signer receives nothing unless it is a payout.
- Mints nothing; total supply unchanged.
- Rejected if claims are disabled, an epoch is not finalized, a record is missing
  or already claimed, the amount is non-positive, the range exceeds
  `max_claim_epochs_per_tx`, or the module balance is insufficient.

## Authority summary

| Message | Authority |
|---|---|
| `MsgUpdateRewardsParams` | CoreSlot authority |
| `MsgPauseRewards` / `MsgResumeRewards` | CoreSlot emergency authority |
| `MsgClaimRewards` | any signer |

The CLI never infers or hardcodes authority; the message server enforces it.
