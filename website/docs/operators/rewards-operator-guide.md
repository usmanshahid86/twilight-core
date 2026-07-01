---
title: Rewards Operator Guide
---

# Rewards Operator Guide

How to operate and monitor the rewards module day to day. For a pure command
cheat-sheet see [Operator Runbook](../rewards/operator-runbook.md); for metrics
see [Monitoring](monitoring.md).

## What you are watching

Each epoch, the chain mints a `utwlt` pool and allocates it to active operators;
operators (or anyone) then claim to the snapshotted payout addresses. Your job is
to confirm this happens correctly and to respond to pauses or faults.

## Per-epoch health check

After each epoch boundary:

1. **Epoch advanced** — `epoch-info` shows `current_epoch` incremented.
2. **Emission correct** — `epoch-reward <closed-epoch>` `minted_emission` matches
   the expected block subsidy × blocks, and `cumulative-emitted` advanced by that
   amount.
3. **Allocation balances** — operator allocation plus carry-out reconciles to the
   finalized reward pool.
4. **Coverage** — `module-balances.rewards_balance` ≥ unclaimed + carry.
5. **Agreement** (multi-node) — all nodes agree on app hash (`agree.sh`).

```bash
twilightd rewards-query epoch-info --node <rpc>
twilightd rewards-query epoch-reward <epoch> --node <rpc>
twilightd rewards-query cumulative-emitted --node <rpc>
twilightd rewards-query module-balances --node <rpc>
```

## Claiming

Anyone can trigger a claim; funds go to the snapshotted payout. Operators
typically claim their own slot:

```bash
twilightd rewards-query claimable <slot-id> <start> <end> --node <rpc>
twilightd rewards claim <slot-id> <start> <end> --from <signer> \
  --chain-id <chain-id> --node <rpc> --gas 600000 --fees 0utwlt --yes
```

See [Claims](../rewards/claims.md).

## Halving awareness

The per-block subsidy halves as cumulative emitted crosses supply thresholds.
Track the schedule:

```bash
twilightd rewards-query next-halving --node <rpc>
twilightd rewards-query supply-schedule --node <rpc>
```

Near the cap the subsidy can floor to zero (terminal sub-cap dust) — emission
legitimately becomes zero while cumulative stays below the cap. This is expected
(see [Economics](../rewards/economics.mdx#halving-and-the-supply-cap)).

## When things pause

If emissions/settlement/claims are paused (by the emergency authority), behavior
changes predictably — see [Epoch Lifecycle → pause interactions](../rewards/epoch-lifecycle.md#pause-interactions)
and [Authority & Emergency Guide](authority-and-emergency-guide.md). Use
[Troubleshooting](../rewards/troubleshooting.md) to diagnose.
