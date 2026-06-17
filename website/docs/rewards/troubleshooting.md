---
title: Troubleshooting
---

# Troubleshooting

Symptom → likely cause → what to check. For the deeper safety/threat discussion
see [Security & Failure Modes](security-and-failure-modes.md).

| Symptom | Likely cause | Check / fix |
|---|---|---|
| Epoch not finalizing | settlement paused, or boundary not reached | `rewards-query epoch-info` (height vs `current_epoch_end_height`); `rewards-query params` → `epoch_settlement_enabled` |
| Mint is zero at the boundary | emissions paused, or subsidy floored to 0 near cap | `params.emissions_enabled`; `rewards-query next-halving` (`current_block_subsidy`) |
| `cumulative_emitted` not advancing | emissions paused | `params.emissions_enabled`; resume via emergency authority if intended |
| Claim rejected | claims paused / epoch not finalized / already claimed / range too large / insufficient balance | `params.claims_enabled`; `rewards-query epoch-reward <e>`; `rewards-query slot-rewards <s>` (`claimed`); `rewards-query module-balances` |
| Double claim rejected | record already claimed | `slot-rewards <slot>` shows `claimed: true` — expected |
| `claimable` empty | nothing finalized for the range, or already claimed | `epoch-reward`, `slot-rewards` |
| Params update rejected | immutable field changed / unsupported feature / wrong authority | [Parameters → v1 feature guards](params.md#v1-feature-guards) |
| `pause`/`resume` rejected | no flag set, or wrong emergency authority | pass at least one of `--emissions/--settlement/--claims`; use the CoreSlot emergency authority |
| Pagination returns empty `next_key` | last page reached | normal — stop paging |
| Node stuck / repeated EndBlock error | fail-closed finalization fault | inspect node logs; rewards halts the block on a fault by design — see [Security & Failure Modes](security-and-failure-modes.md) |
| Nodes disagree on app hash | **critical — possible state fork** | stop and investigate before continuing; see [Localnet](../chain/localnet.md) and [Incident Response](../operators/incident-response.md) |

## Useful one-liners

```bash
# Is rewards paused in any dimension?
twilightd rewards-query params --node <rpc> --output json \
  | jq '{emissions_enabled, epoch_settlement_enabled, claims_enabled}'

# Does the module cover what it owes?
twilightd rewards-query module-balances --node <rpc> --output json
twilightd rewards-query cumulative-emitted --node <rpc> --output json
```
