---
title: Rewards Query API
---

# Rewards Query API

Authoritative per-command reference for `twilightd rewards-query`. All commands
are read-only. Narrative usage is in [Queries](../rewards/queries.md). Raw
`--help` captures: `website/generated/cli/rewards-query-*-help.txt`.

The underlying gRPC service is `twilight.rewards.v1.Query`.

## `params`
Current rewards parameters. → `params` ([field list](../rewards/params.md)).

## `epoch-info`
Open-epoch state. → `state` (`current_epoch`, `current_epoch_start_height`,
`cumulative_emitted`, `carry_forward_remainder`), `current_epoch_config`,
`current_epoch_end_height`, `has_pending_params`, `pending_params`.

## `next-halving`
Halving view. → `info`: `current_tier`, `current_block_subsidy`, `next_threshold`,
`remaining_until_next_halving`, `cumulative_emitted`, `max_supply`,
`has_next_halving`.

## `epoch-reward [epoch]`
Finalized epoch aggregate. Args: `epoch` (uint, required, > 0). Returns
`NotFound` if the epoch is not finalized. → `epoch_reward`: `epoch_number`,
`start_height`, `end_height`, `minted_emission`, `carry_in`,
`distributable_fees`, `treasury_amount`, `reward_pool`, `allocated_amount`,
`carry_out`, `cumulative_emitted_after_epoch`, `rewards[]`.

## `slot-rewards [slot-id]`
Claim records for a slot. Args: `slot-id` (uint, required, > 0).
**Paginated** (ascending epoch). → `rewards[]` (`slot_id`, `epoch_number`,
`operator_address`, `payout_address`, `blocks_active`, `reward_weight`,
`effective_weight`, `amount`, `claimed`, `claimed_at_height`), `pagination`.

## `claimable [slot-id] [start-epoch] [end-epoch]`
Unclaimed positive rewards in an inclusive range. Args all uint, required;
`start ≤ end`. → `rewards[]`, `total_amount`.

## `cumulative-emitted`
→ `cumulative_emitted`, `max_supply`.

## `supply-schedule`
→ `params`, `next_halving` (same `NextHalvingInfo` as `next-halving`).

## `current-active-blocks`
Active-block counters for the open epoch. **Paginated** (ascending slot id).
→ `epoch_number`, `active_blocks[]` (`slot_id`, `blocks_active`), `pagination`.

## `module-balances`
→ `denom` (`utwlt`), `rewards_balance`, `fee_pool_balance`.

## Pagination flags

`slot-rewards` and `current-active-blocks` accept `--limit`, `--offset`,
`--page`, `--page-key`, `--count-total`, `--reverse`. Use the `next_key` from a
response as the next `--page-key`. Other commands take no pagination flags.

## Errors

- Missing/zero required id → `InvalidArgument`.
- Invalid range (`start > end`) → `InvalidArgument`.
- Non-finalized `epoch-reward` → `NotFound`.
