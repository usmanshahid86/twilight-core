---
title: Incident Response
---

# Incident Response

Playbooks for rewards-related incidents. For symptom lookup see
[Troubleshooting](../rewards/troubleshooting.md); for the safety model see
[Security & Failure Modes](../rewards/security-and-failure-modes.md).

## App-hash divergence across nodes (critical)

A state fork. Highest severity.

1. **Stop** affected nodes; do not let them continue producing.
2. Identify the divergence height (`agree.sh` / block headers).
3. Compare `app_state.rewards` and `app_state.coreslot` across nodes at that
   height (e.g. via `twilightd export`).
4. Do not resume until the cause is understood. The rewards path is designed and tested for deterministic execution, so
   divergence suggests a real defect or a non-identical binary/genesis across nodes
   — verify both are identical first.

## Repeated EndBlock error / chain halted

Rewards is fail-closed: a finalization fault halts the block rather than
half-committing.

1. Read node logs for the error returned from `FinalizeBlock`.
2. Confirm the committed height did not advance and no partial epoch was written
   (`epoch-info`, `epoch-reward`).
3. Resolve the underlying fault (e.g. a CoreSlot/rewards state inconsistency).
   The emergency authority can `pause --settlement` to stop finalization attempts
   while investigating; counting continues and re-enabling finalizes once.

## Settlement paused unexpectedly

```bash
twilightd rewards-query params --node <rpc> --output json | jq .epoch_settlement_enabled
```

If `false` and unintended, `resume --settlement` via the emergency authority. The
open epoch finalizes once at the first enabled EndBlock past its boundary.

## Claims failing for everyone

Check `claims_enabled`. If paused intentionally (incident containment), communicate
the window. If not, `resume --claims`. Per-claim failures (already claimed,
insufficient balance) are not incidents — see
[Troubleshooting](../rewards/troubleshooting.md).

## Wrong params queued

The latest queued `update-params` wins before the boundary. Queue a corrected
update from the authority before the next epoch finalizes. Already-activated
params apply only to the next epoch onward and can be re-queued.

## Module-balance coverage failure

If `module-balances.rewards_balance` < unclaimed + carry, stop and investigate —
this should not occur under the rewards accounting invariants (the coverage invariant holds after
every finalize/claim). Treat as a critical accounting defect.

## Key compromise

- **Emergency key:** can pause (DoS) only. Rotate via CoreSlot; resume any
  unwanted pauses.
- **Authority key:** can queue params at the next boundary only (not denom/cap,
  not pause). Rotate via CoreSlot; re-queue correct params.
