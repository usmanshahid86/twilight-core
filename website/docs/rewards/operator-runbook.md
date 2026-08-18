---
title: Operator Runbook
---

# Rewards Operator Runbook

A command cheat-sheet for operating the rewards module. For the narrative guide,
see [Rewards Operator Guide](../operators/rewards-operator-guide.md); for incident
response see [Incident Response](../operators/incident-response.md).

All commands take `--node <rpc>`; add `--output json` for machine output.

## Monitor the current epoch

```bash
twilightd rewards-query epoch-info --node <rpc>
# current_epoch, current_epoch_start_height, current_epoch_end_height, has_pending_params
twilightd rewards-query current-active-blocks --limit 100 --node <rpc>
```

## Monitor supply and balances

```bash
twilightd rewards-query cumulative-emitted --node <rpc>   # cumulative vs max_supply
twilightd rewards-query module-balances --node <rpc>      # rewards + fee_pool balance
twilightd rewards-query next-halving --node <rpc>         # tier, subsidy, next threshold
```

## Inspect a finalized epoch

```bash
twilightd rewards-query epoch-reward <epoch> --node <rpc>
# minted_emission, reward_pool, allocated_amount, carry_out, rewards[]
```

## How a slot's rewards are released

Rewards accrue at finalization as a per-slot **entitlement** held in the rewards
module account. There is no claim transaction: entitlements are released by
settlement in `x/mining`, which pays participants and returns the remainder to
the operator. See [Epoch Lifecycle](epoch-lifecycle.md).

## Verify cross-node agreement (localnet)

```bash
scripts/localnet/agree.sh         # app/validators/next-validators hash agreement
```

## Authority actions

| Action | Command | Authority |
|---|---|---|
| Queue params update | `rewards update-params ./params.json --from <auth>` | CoreSlot authority |
| Pause | `rewards pause --from <emg>` | CoreSlot emergency authority |
| Resume | `rewards resume --from <emg>` | CoreSlot emergency authority |

See [Authority & Emergency Guide](../operators/authority-and-emergency-guide.md).

## Operator check

> **Operator check:** after each epoch boundary, confirm `cumulative_emitted`
> changed by the expected bounded emission for that epoch,
> `module-balances.rewards_balance` covers unreleased entitlements + carry, and (on a
> multi-node network) all nodes agree on app hash.
