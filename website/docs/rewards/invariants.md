---
title: Invariants
---

# Invariants

The module defines five callable invariants in `x/rewards/keeper/invariants.go`.
The chain does not run the `crisis` module, so they are **callable backstops**
exercised in tests (and runnable by tooling) rather than auto-run on-chain. The
multi-node localnet smoke checks all five against a real bank after finalize and
after claim.

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
rewards module balance ≥ Σ(unclaimed claim record amounts) + carry_forward_remainder
```

By construction this is expected to hold after each finalize and claim: the
rewards module balance must cover all unclaimed claim records plus the
carry-forward remainder.

## DenomCorrectnessInvariant

```text
native_denom == utwlt AND fee_denom == utwlt
AND no display metadata (alphabetic chars) in amount strings
```

Prevents a display denom (`twlt`/`TWLT`/`Twilight`) from leaking into accounting.

## ClosedEpochImmutabilityInvariant

```text
no finalized epoch aggregate's embedded rewards carry a claimed marker
```

Claims mutate only the **separate** claim-record collection; the finalized epoch
aggregate is immutable. `SetFinalizedEpoch` additionally rejects overwriting an
existing epoch.

## Relationship to operators

These are accounting safety nets, not routine checks. If you build monitoring,
the most operationally useful derived checks are supply ≤ cap, cumulative ≤ cap,
and module balance ≥ unclaimed + carry — all queryable via
[`cumulative-emitted`](queries.md) and [`module-balances`](queries.md). See
[Monitoring](../operators/monitoring.md).
