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

## Check and claim a slot's rewards

```bash
twilightd rewards-query slot-rewards <slot-id> --limit 10 --node <rpc>
twilightd rewards-query claimable <slot-id> <start> <end> --node <rpc>

twilightd rewards claim <slot-id> <start> <end> \
  --from <signer> --chain-id <chain-id> --node <rpc> --gas 600000 --fees 0utwlt --yes
```

Funds go to the snapshotted payout address. See [Claims](claims.md).

## Verify cross-node agreement (localnet)

```bash
scripts/localnet/agree.sh         # app/validators/next-validators hash agreement
```

## Authority actions

| Action | Command | Authority |
|---|---|---|
| Queue params update | `rewards update-params ./params.json --from <auth>` | CoreSlot authority |
| Pause | `rewards pause --emissions --settlement --claims --from <emg>` | CoreSlot emergency authority |
| Resume | `rewards resume --claims --from <emg>` | CoreSlot emergency authority |

See [Authority & Emergency Guide](../operators/authority-and-emergency-guide.md).

## Operator check

> **Operator check:** after each epoch boundary, confirm `cumulative_emitted`
> increased by the expected emission, `module-balances.rewards_balance` covers
> unclaimed + carry, and (on a multi-node network) all nodes agree on app hash.
