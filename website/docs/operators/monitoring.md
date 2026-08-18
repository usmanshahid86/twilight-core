---
title: Monitoring
---

# Monitoring

What to watch, and how to read it. All signals are available via queries and the
node RPC; there is no separate metrics exporter in the current version.

## Epoch / emission signals

| Signal | Source | Healthy |
|---|---|---|
| `current_epoch` | `epoch-info` | advances by 1 each boundary |
| `current_epoch_start_height` / `current_epoch_end_height` | `epoch-info` | end = start + epoch_length − 1 |
| `cumulative_emitted` | `cumulative-emitted` | monotonic non-decreasing; ≤ `max_supply` |
| `minted_emission` (per epoch) | `epoch-reward <e>` | matches expected bounded emission for that epoch, including halving and cap behavior |
| current subsidy / tier | `next-halving` | subsidy halves at each threshold; may reach 0 near cap |

## Balance / coverage signals

| Signal | Source | Healthy |
|---|---|---|
| `rewards_balance` | `module-balances` | ≥ outstanding entitlements + carry |
| `fee_pool_balance` | `module-balances` | `0` (fees dormant) |
| `carry_out` (per epoch) | `epoch-reward <e>` | `reward_pool − allocated_amount`, ≥ 0 |

## Pause-state signals

```bash
twilightd rewards-query pause-state --node <rpc> --output json
```

Rewards has one canonical pause state, not per-area flags. It reports
`pause_state` (including any pending transition) and `release_enabled`. If
paused, correlate with operator intent.

## Consensus / determinism signals (multi-node)

| Signal | Source | Healthy |
|---|---|---|
| app hash | block header / `agree.sh` | identical across all nodes at the same height |
| validators hash / next-validators hash | block header / `agree.sh` | identical across nodes |
| `num_val_updates` | `agree.sh` output | only CoreSlot ever produces these |

> **Operator check:** app-hash divergence across nodes is the single most
> important alarm — it indicates a state fork. Page on it immediately and see
> [Incident Response](incident-response.md).

## Logs

Watch node logs for repeated EndBlock errors (a fail-closed finalization fault
halts the block) and for `epoch_finalized` events (see
[Events](../rewards/events.md)).
