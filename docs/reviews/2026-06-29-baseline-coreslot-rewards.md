# Baseline Review: CoreSlot and Rewards

- **Date:** 2026-06-29
- **Reviewed as of commit:** `eddb9d4`
- **Type:** Internal baseline review (code- and test-grounded). **Not** an independent
  security audit.

This is a curated, dated baseline review of the consensus- and value-critical modules. It
records the current state of the implementation against its design intent
([ADR-0001](../architecture/adr/0001-coreslot-poa.md),
[ADR-0002](../architecture/adr/0002-rewards-emission.md), and the
[architecture overview](../architecture/overview.md)). Supporting evidence is indexed in the
[validation summary](../testing/validation-summary.md).

## 1. Summary

The reviewed paths — CoreSlot validator-set control and the Rewards v1 emission/claim flow —
match the documented v1 design and are backed by unit, integration, and endurance evidence. The
core safety properties (single validator-update emitter, intentional module omission,
deterministic bounded emission, exact accounting) are enforced by code and tests, not only by
convention.

## 2. Scope

**In scope:** `x/coreslot`, `x/rewards`, and the app wiring relevant to validator-set control
and rewards (`app/`).

**Out of scope:**

- Future modules and integrations not present in this repository.
- Explorer / indexer behaviour, except where CoreSlot/rewards query compatibility is noted.
- Production key custody and operational deployment.
- The disabled weighted-rewards and fee-funded-rewards branches beyond their v1 rejection
  guards (their distribution logic is not implemented).
- Independent external security audit (still required before mainnet).

## 3. Reviewed components

- CoreSlot lifecycle and validator-update derivation (`x/coreslot/keeper`).
- Module omission: `x/staking`, `x/distribution`, `x/mint`, `x/gov`, `x/slashing`.
- The sole-validator-update-emitter invariant (`app/`).
- Rewards epoch finalization, supply cap and supply-threshold halving (`x/rewards/keeper`).
- Uniform active-block allocation and carry-forward remainder.
- Claim safety and accounting.
- Rewards genesis export/import.
- Emergency controls.

## 4. Key findings

- **Single validator-update emitter.** `x/coreslot` is the only module in the EndBlock order
  that returns `[]abci.ValidatorUpdate`; lifecycle messages carry intent only and the EndBlocker
  derives a canonical, sorted diff. An app-level test asserts exactly one validator-update
  emitter.
- **Module omission is structural.** `x/staking`, `x/distribution`, `x/mint`, `x/gov`, and
  `x/slashing` are absent from the module manager and expose no message handlers; this is
  asserted by test, not left to configuration.
- **Bounded, deterministic emission.** The per-block subsidy and supply-threshold halving are
  pure integer arithmetic with no wall-clock, floating-point, or random inputs. A halving
  threshold crossed mid-epoch is handled per block — the crossing block is clipped exactly to
  the threshold, and later blocks in the epoch use the halved subsidy. Cumulative emission is
  clipped so it can never exceed `MaxSupply`, which is itself immutable after genesis.
- **Allocation by active-block participation.** Each epoch's pool is split in proportion to
  per-slot active-block counts; the configured reward weight is snapshotted but not used for
  allocation in v1. The integer-division remainder (and the full pool when no slots are
  eligible) becomes the carry-forward remainder added to the next epoch.
- **Claim safety.** Claims are permissionless to trigger but pay the payout address snapshotted
  at finalization; a claimed `(slot, epoch)` record is marked claimed (replay rejected); a
  multi-epoch claim range is atomic — a missing or already-claimed epoch fails the whole claim.
- **Lifecycle does not rewrite recorded allocations.** Key rotation keeps slot identity, and
  removal preserves the slot record and its claim records, so previously earned, unclaimed
  rewards remain claimable.
- **Emergency controls are independent.** Emission, epoch settlement, and claims can each be
  paused independently by the emergency authority; pausing emission defers issuance (cumulative
  does not advance) rather than losing it; pausing claims still accrues allocations.

## 5. Invariants checked

- `claimed + unclaimed + carry = cumulative_emitted` (equal to total supply under the
  zero-premine default) — checked by the module-balance coverage invariant and the endurance
  run.
- Exactly one module returns validator updates.
- Cumulative emission never exceeds `MaxSupply`.
- Rewards genesis export then import reproduces identical state.

## 6. Risks and limitations

- **Disabled economic branches.** Weighted rewards and fee-funded rewards are code-gated;
  their allocation logic is unimplemented and must be built and validated before activation.
  Treasury share is implemented but off by default.
- **Endurance coverage is happy-path.** The long-running endurance evidence exercises the
  steady state; several economic branches (halving, non-zero carry, non-uniform participation,
  treasury, active-set churn, the claim-range cap) are unit-tested but not yet driven in a
  long integration run. See the [soak C1 report](../testing/soak-c1-endurance.md).
- **Tooling compatibility.** Tooling that assumes a populated staking set must use the CoreSlot
  queries instead; a read-only staking projection remains the documented fallback if ever
  required.
- **Custody and governance of the authorities** are out of scope here.

## 7. Status

The reviewed CoreSlot and Rewards v1 paths are code-grounded with no unresolved correctness
findings in this baseline review. This document is not an independent security audit; a
pre-mainnet external review remains required.
