# ADR-0001: CoreSlot Proof-of-Authority — an independent validator module

- **Status:** Accepted — implemented in [`x/coreslot`](../../../x/coreslot)
- **Date:** 2026-06-10 (decision); curated for publication 2026-06-29
- **Relates to:** [`coreslot-poa.md`](../coreslot-poa.md),
  [`../../security/staking-omission-or-inert-staking.md`](../../security/staking-omission-or-inert-staking.md)

## Summary

In short: Twilight does not use token staking to decide who validates blocks. Validator
admission, lifecycle, consensus-key rotation, and validator-update emission are owned by
`x/coreslot`, a dedicated PoA module. This gives the chain a single, auditable control plane
for consensus-set changes and keeps staking structurally off the consensus path.

## Context

Twilight Core is a Proof-of-Authority chain: validator-set membership is governed by an
authority, not by open token staking. The core architectural question was **how to own the
validator set** on top of Cosmos SDK / CometBFT. Three approaches exist, each evidenced by
real implementations:

- **A — Staking-backed PoA.** Keep `x/staking` as the validator engine and drive it from an
  admin layer (mint synthetic shares / route `MsgCreateValidator`). Staking remains the
  emitter of CometBFT validator updates; admission is restricted by filtering staking
  messages on a *live* engine.
- **B — Independent module.** A purpose-built module owns validator state and is the **sole**
  emitter of validator updates; `x/staking` is not used for consensus at all.
- **C — Hybrid mirror.** An independent module is the source of truth, with staking
  maintained as a read-only projection so staking-shaped tooling still works.

A hard CometBFT/SDK constraint shaped the analysis: **two modules cannot both return
validator updates in the same block.** The SDK module manager errors (`validator EndBlock
updates already set by a previous module`) and halts `FinalizeBlock`. So exactly one module
must own emission — which disqualifies the "both emit" hybrid variants.

Twilight's required validator lifecycle is also richer than add/remove —
`register → activate → inactivate / suspend → reactivate → remove`, plus consensus-key
rotation, payout address, and reward weight. That does not map cleanly onto staking's
power-zero + jail model.

We surveyed public staking-backed PoA modules (which keep staking as the emitter and apply
partial, sometimes non-recursive, message filters), and non-staking PoA architectures in
which a purpose-built module seeds genesis validators and emits validator updates directly
while staking is absent from the module manager. These references were used for architectural
comparison only; no code was reused. That non-staking model is the closest analogue to
Twilight's goal.

## Decision

**Adopt Option B: an independent `x/coreslot` module** as the single authority and single
emitter of the validator set.

- Validator state is Twilight-owned `CoreSlot` records (slot id, operator, consensus pubkey,
  payout address, status, consensus power, reward weight, heights) with unique operator and
  consensus-address indexes.
- `x/coreslot` is the **only** module in the EndBlocker order that returns
  `[]abci.ValidatorUpdate`. It derives the desired active set from slot state, diffs against
  a persisted `LastAppliedValidators`, emits one canonical update per changed consensus
  address (activate: `new@power`; inactivate/suspend/remove: `old@0`; rotation: `old@0` then
  `new@power`), rejects duplicates, sorts canonically, and persists only on full success.
- Lifecycle is an explicit `PENDING / ACTIVE / INACTIVE / SUSPENDED / REMOVED` state machine
  with authority-gated transitions; removal is non-destructive (no stake to burn).
- Reward weight is **separate state** from consensus power, so operators can later be paid on
  a weight independent of voting power (v1: equal weight).
- Consensus-key rotation is coreslot-owned and delayed, emitting `old@0 / new@power`
  atomically while a slot is active.

## Implementation refinements (decision → shipped code)

The shipped implementation tightened the original decision in two important ways:

1. **Staking is fully omitted, not "inert."** The memo allowed keeping an inert staking
   *keeper* for queries/migration. The app does not: `staking`, `distribution`, `slashing`,
   `gov`, and `mint` are **absent from the module manager entirely**. A test asserts their
   omission *and* that `x/coreslot` is the only validator-update-emitting EndBlock module.
   Rationale and trade-offs are recorded in
   [`staking-omission-or-inert-staking.md`](../../security/staking-omission-or-inert-staking.md).
2. **Rewards live in [`x/rewards`](../../../x/rewards)** (epoch emission + claims), keyed on
   reward weight — not the placeholder `x/operatorrewards` name used in the memo. See
   [ADR-0002](0002-rewards-emission.md).

## Consequences

**Positive.** Exactly one audited code path can move the validator set, which minimizes the
blast radius of consensus-set bugs. The design supports Twilight's full validator lifecycle,
cleanly separates consensus power from reward weight and status, and allows active
consensus-key rotation without relying on staking semantics.

The staking-off-consensus property is **structural**: staking is not registered in the module
manager, exposes no message server, and cannot emit validator updates — so no governance,
authz, or message path can reach it. This is stronger than filtering messages on a live
staking engine.

**Negative / costs.** Twilight owns more code (emit/reap, canonical diffing, genesis
round-trip, rotation) and must validate Cosmos-tooling compatibility itself — export/import,
app-hash determinism, and validator-hash consistency are covered by Twilight-specific tests,
drills, and soak runs rather than inherited from staking. Stock SDK simulation (which assumes
staking) is replaced by module-specific determinism tests and operational drills.

**Future optionality.** Because staking is not used to secure consensus, an *economic* staking
layer can be added later as a constrained module without first prying it loose from the
validator set. Reward weight is modeled as independent state from day one so a future
stake-weight component need not touch consensus power.

## Alternatives considered

| Criterion | A: staking-backed | **B: independent `x/coreslot`** | C: hybrid mirror |
|---|---|---|---|
| Production safety | Weak — validator lifecycle remains coupled to staking economics and power-zero semantics | **Strong — single emitter/authority** | Medium — single emitter, mirror can drift |
| Cosmos tooling compatibility | Strong — stock tooling works | Medium — verify export/import + explorers | Strong — staking projection feeds tooling |
| Lifecycle expressiveness | Weak — power-zero / jail | **Strong — explicit state machine** | Strong (= B) |
| Staking attack surface | Weak — seal every msg path on a live engine | **Strong — engine not registered** | Strong — engine not an emitter |
| Future weighted rewards/power | Weak/Medium — coupled to power | **Strong — separate state** | Medium/Strong |
| Maintenance burden | Weak — track staking internals each upgrade | Medium — own a focused module | Weak — module *and* mirror |

Option B dominates on the safety, lifecycle, and future-flexibility axes v1 prioritizes; A
and C win mainly on stock-tooling compatibility, a one-time integration cost rather than an
ongoing safety property. **C1 (coreslot is source of truth and sole emitter; staking mirrored
read-only)** is retained as the only acceptable fallback *if* explorer/indexer compatibility
ever forces a populated staking set. The "both emit" variants (C2/C3) are rejected: they
either trip the single-emitter halt above or collapse back into Option A.

## Status of the original open questions

- **Rewards source** — resolved: epoch-scheduled emission minted at finalization (`x/rewards`),
  with supply-threshold halving; no `x/distribution`. See [ADR-0002](0002-rewards-emission.md).
- **Authority model** — resolved at the boundary level: a configurable `Authority` and a
  separate `EmergencyAuthority` address (set in genesis); the emergency authority can suspend
  slots and pause rewards. The operational custody model for these authorities (e.g. multisig
  vs single key) is out of scope for this ADR, which records only the validator-set ownership
  boundary.
- **Staking reachability** — resolved: staking is fully omitted (no msg server, no module);
  not routable in v1.
- **Simulation vs drills** — resolved: module-specific determinism tests + operational drills
  and soak runs instead of the staking-shaped SDK simulation harness.
- **Explorer/indexer compatibility** — tracked: downstream indexing uses the CoreSlot queries
  + CometBFT validators (see [`../../reference/explorer-integration-handoff.md`](../../reference/explorer-integration-handoff.md)); the C1 fallback trigger remains documented should a populated staking set ever be required.

## Enforcement

A test asserts that `x/coreslot` is the **only** module returning validator updates, so a
future module addition cannot silently start emitting and trip the single-emitter halt.
