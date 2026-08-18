---
title: Invariants
---

# Invariants

The module defines six callable invariants in `x/rewards/keeper/invariants.go`.
The chain does not run the `crisis` module, so they are **callable backstops**
exercised in tests (and runnable by tooling) rather than auto-run on-chain.

## SupplyCapInvariant

```text
bank total supply of utwlt ≤ max_supply
```

Guards against any over-mint. A failure means more `utwlt` exists than the cap
allows.

## CumulativeEmittedInvariant

```text
state.cumulative_emitted ≤ max_supply
```

The accounting view of emission must also respect the cap. Enforced primarily by
the emission math (clipping) and the pre-mint assertion; this is the backstop.

## ModuleBalanceCoverageInvariant

```text
rewards module balance ≥ outstanding entitlement liability + carry_forward_remainder
```

Solvency, not exactness. Finalization asserts the stronger equality at the one
instant it is entitled to — immediately after a complete monetary transition —
while this backstop is callable at any height, where a surplus in escrow is not a
solvency failure.

## EntitlementLiabilityInvariant

```text
outstanding entitlement liability == Σ(entitlement amount − released amount)
```

The accumulator the block path reads is an O(1) summary of the entitlement
records. This is the full scan it exists to avoid, run where its cost is
acceptable: without it a drift in the accumulator would be invisible to every
consumer, since they all read the same stored number.

## DenomCorrectnessInvariant

```text
native_denom == utwlt AND fee_denom == utwlt
AND no display metadata (alphabetic chars) in amount strings
```

Prevents a display denom (`twlt`/`TWLT`/`Twilight`) from leaking into accounting.

## ClosedEpochImmutabilityInvariant

```text
no finalized epoch aggregate embeds per-slot rows
AND every entitlement's released amount stays within its bounded amount
```

The canonical per-slot record is the entitlement, so an aggregate carrying rows
would be a second immutable copy of the same obligation, free to drift from the
one that can actually be paid. `released_amount` is the only field permitted to
move on a live entitlement, and it may never pass the amount it is bounded by.
`SetFinalizedEpoch` additionally rejects overwriting an existing epoch.

## Relationship to operators

These are accounting safety nets, not routine checks. If you build monitoring,
the most operationally useful derived checks are supply ≤ cap, cumulative ≤ cap,
and module balance ≥ outstanding entitlements + carry — all queryable via
[`cumulative-emitted`](queries.md) and [`module-balances`](queries.md). See
[Monitoring](../operators/monitoring.md).
