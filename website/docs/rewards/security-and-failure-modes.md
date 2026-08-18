---
title: Security & Failure Modes
---

# Security & Failure Modes

## Invariants

The module defines five callable invariants (`x/rewards/keeper/invariants.go`).
They are callable backstops; the chain does not run the `crisis` module, so they
are exercised in tests rather than auto-run on-chain.

| Invariant | Checks | A failure implies |
|---|---|---|
| `SupplyCapInvariant` | bank `utwlt` supply ≤ `max_supply` | over-mint / cap breach |
| `CumulativeEmittedInvariant` | `cumulative_emitted` ≤ `max_supply` | accounting drift past the cap |
| `ModuleBalanceCoverageInvariant` | rewards balance ≥ outstanding entitlements + carry | the module cannot cover what it owes |
| `EntitlementLiabilityInvariant` | the O(1) liability accumulator equals the full scan of entitlement records | the accumulator drifted from what it summarizes |
| `DenomCorrectnessInvariant` | native/fee denom = `utwlt`; no display metadata in amounts | a display denom leaked into accounting |
| `ClosedEpochImmutabilityInvariant` | finalized aggregates embed no per-slot rows; no entitlement releases past its bound | a closed epoch was mutated |

After each finalization and each release, module-balance coverage is expected to
hold: `balance ≥ outstanding entitlements + carry`. These invariants are also
checked in app and localnet validation against real bank state.

## Fail-closed lifecycle

Rewards `BeginBlock` and `EndBlock` are **fail-closed**. A CoreSlot contract
violation, a missing reward snapshot, a cap breach, or any finalization fault
returns an error rather than degrading silently. Under the runtime,
`baseapp.FinalizeBlock` surfaces the error and **discards the block's state**, so
the fault **halts the block rather than half-committing**.

This is the intended safety posture for a monetary module: a halt is recoverable
(the emergency authority can pause settlement; operators can patch), but a
half-committed or silently-wrong mint is not. App-level validation covers this behavior: a forced finalization fault makes `FinalizeBlock` return an error and
leaves the committed height unchanged with no finalized epoch written.

:::warning Operator implication
Because rewards is fail-closed, a genuine finalization fault stops block
production until resolved. Keep the CoreSlot active-set contract and rewards
lifecycle aligned (a removed/suspended slot that earned credit is safe — CoreSlot
retains its data). Monitor for repeated EndBlock errors in node logs.
:::

## Determinism

No rewards state transition reads wall-clock time, randomness, environment
variables, or CometBFT-local config. Finalization and release iterate sorted
collections (never raw Go map order). Cross-node app-hash agreement after
finalization is the multi-node evidence.

## Failure-mode reference

| Symptom | Likely cause | Check |
|---|---|---|
| Epoch not finalizing | boundary not reached | `epoch-info` (`current_epoch_end_height`, height) |
| Mint is zero at finalization | rewards paused, or subsidy floored to 0 near cap | `pause-state`; `next-halving` |
| Release rejected | rewards paused, epoch not finalized, no entitlement, or the amount exceeds what remains | `pause-state`; `epoch-reward`; `module-balances` |
| Params update rejected | immutable field changed, unsupported feature, or wrong authority | see [Parameters](params.md#v1-feature-guards) |
| App-hash divergence across nodes | **critical** — a state fork | stop, investigate before continuing; see [Localnet](../chain/localnet.md) |
| Pagination returns empty `next_key` | last page reached | normal; stop paging |

## Threat-model notes

- **Authority key compromise** (CoreSlot authority): could queue malicious params
  at the next boundary, but **cannot** change `native_denom`/`max_supply`
  (immutable) and cannot pause (that is emergency authority). Rotate via CoreSlot.
- **Emergency key compromise:** could pause rewards (denial-of-service on accrual
  and release), but cannot mint, redirect payouts, or change immutable fields.
  Rotate via CoreSlot.
- **Payout destination:** an operator's remainder can only reach the payout
  address snapshotted at finalization. No caller can redirect it.
