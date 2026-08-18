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

- Sets the single canonical pause state; there are no per-area selectors.
- Takes effect at the beginning of the next block, before any reward sampling.
- Rejected if not the emergency authority.

## `resume` → `MsgResumeRewards`

Same shape as `pause`, clearing the same canonical state.

## Authority summary

| Message | Authority |
|---|---|
| `MsgUpdateRewardsParams` | CoreSlot authority |
| `MsgPauseRewards` / `MsgResumeRewards` | CoreSlot emergency authority |

The CLI never infers or hardcodes authority; the message server enforces it.
