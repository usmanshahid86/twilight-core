---
title: Queries
---

# Rewards Queries

All rewards queries are **read-only**. They follow the repository's top-level
command convention: `twilightd rewards-query <command>` (not `query rewards`).
Add `--node <rpc>` to target a specific node and `--output json` for machine
output. Raw `--help` captures for every command live under
`website/generated/cli/` (generated source material).

## Commands

| Command | Args | Purpose |
|---|---|---|
| `params` | — | Current rewards parameters |
| `epoch-info` | — | Current epoch, start height, configured end height, pending params |
| `next-halving` | — | Current tier, current subsidy, next threshold, remaining-to-halving |
| `epoch-reward` | `[epoch]` | Finalized epoch aggregate (minted, pool, allocated, carry, slot rewards) |
| `slot-rewards` | `[slot-id]` | Claim records for a slot (paginated, ascending epoch) |
| `claimable` | `[slot-id] [start-epoch] [end-epoch]` | Unclaimed positive rewards in a range + total |
| `cumulative-emitted` | — | Cumulative emitted + max supply |
| `supply-schedule` | — | Params + next-halving view |
| `current-active-blocks` | — | Active-block counters for the open epoch (paginated, ascending slot) |
| `module-balances` | — | `rewards` and `rewards_fee_pool` balances + denom |

## Examples

```bash
twilightd rewards-query params --node <rpc>
twilightd rewards-query epoch-info --node <rpc>
twilightd rewards-query epoch-reward 1 --node <rpc>
twilightd rewards-query slot-rewards 1 --limit 10 --node <rpc>
twilightd rewards-query claimable 1 1 1 --node <rpc>
twilightd rewards-query current-active-blocks --limit 10 --node <rpc>
twilightd rewards-query module-balances --node <rpc>
twilightd rewards-query cumulative-emitted --node <rpc>
```

## Pagination

`slot-rewards` and `current-active-blocks` are paginated (their collections grow
over time). They accept the standard Cosmos pagination flags:

| Flag | Meaning |
|---|---|
| `--limit` | Max rows per page (default 100) |
| `--offset` | Numeric offset |
| `--page` | Page number (offset = page × limit) |
| `--page-key` | Continuation key from a previous response's `next_key` |
| `--count-total` | Include total count |
| `--reverse` | Descending order |

The non-paginated commands do not expose these flags. Ordering is deterministic:
`slot-rewards` returns ascending epoch; `current-active-blocks` returns ascending
slot id.

## Selected output fields

- **`epoch-info`** → `state.current_epoch`, `state.current_epoch_start_height`,
  `current_epoch_end_height`, `has_pending_params`.
- **`epoch-reward`** → `epoch_reward.minted_emission`,
  `epoch_reward.reward_pool`, `epoch_reward.allocated_amount`,
  `epoch_reward.carry_out`, `epoch_reward.rewards[]` (per-slot).
- **`slot-rewards`** → `rewards[].epoch_number`, `rewards[].amount`,
  `rewards[].payout_address`, `rewards[].claimed`, `pagination.next_key`.
- **`module-balances`** → `denom` (`utwlt`), `rewards_balance`,
  `fee_pool_balance`.
- **`cumulative-emitted`** → `cumulative_emitted`, `max_supply`.
- **`next-halving`** → `info.current_tier`, `info.current_block_subsidy`,
  `info.next_threshold`, `info.remaining_until_next_halving`,
  `info.has_next_halving`.

A REST/gRPC-gateway surface is not wired in the current version (consistent with
CoreSlot); these are gRPC/CLI queries.
