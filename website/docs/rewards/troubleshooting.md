---
title: Troubleshooting
---

# Troubleshooting

Symptom → likely cause → what to check. For the deeper safety/threat discussion
see [Security & Failure Modes](security-and-failure-modes.md).

| Symptom | Likely cause | Check / fix |
|---|---|---|
| Epoch not finalizing | boundary not reached | `rewards-query epoch-info` (height vs `current_epoch_end_height`) |
| Mint is zero at the boundary | rewards paused, or subsidy floored to 0 near cap | `rewards-query pause-state`; `rewards-query next-halving` (`current_block_subsidy`) |
| `cumulative_emitted` not advancing | rewards paused | `rewards-query pause-state`; resume via emergency authority if intended |
| Release rejected | rewards paused / epoch not finalized / no entitlement / amount exceeds the remaining entitlement | `rewards-query pause-state`; `rewards-query epoch-reward <e>`; `rewards-query module-balances` |
| Params update rejected | immutable field changed / unsupported feature / wrong authority | [Parameters → v1 feature guards](params.md#v1-feature-guards) |
| `pause`/`resume` rejected | wrong emergency authority | use the CoreSlot emergency authority |
| Pagination returns empty `next_key` | last page reached | normal — stop paging |
| Node stuck / repeated EndBlock error | fail-closed finalization fault | inspect node logs; rewards halts the block on a fault by design — see [Security & Failure Modes](security-and-failure-modes.md) |
| Nodes disagree on app hash | **critical — possible state fork** | stop and investigate before continuing; see [Localnet](../chain/localnet.md) and [Incident Response](../operators/incident-response.md) |

## Useful one-liners

```bash
# Is rewards paused?
twilightd rewards-query pause-state --node <rpc> --output json

# Does the module cover what it owes?
twilightd rewards-query module-balances --node <rpc> --output json
twilightd rewards-query cumulative-emitted --node <rpc> --output json
```
