---
title: Release Readiness
---

# Release Readiness

:::note Current status
This page reflects the **Phase 10 validated** implementation. Production
zero-premine genesis and longer multi-epoch soak drills remain **Phase 11**
items. Nothing here implies mainnet readiness.
:::

The rewards module was built and validated in phases, each with an independent
validation pass. See [Validation Reports](../reference/validation-reports.md) for
the per-phase detail.

## What is validated today (through Phase 10)

| Area | Proven by |
|---|---|
| Emission math (supply-threshold halving, max-supply clipping, terminal dust) | Phase 3 keeper tests with exact vectors |
| Active-block accounting, epoch boundary helpers | Phase 4 keeper tests |
| Epoch finalization, uniform distribution, carry-forward, claims, params, pause/resume, invariants | Phase 5–7 keeper tests (atomic via cache contexts) |
| App/runtime wiring; module accounts; sole validator-update emitter | Phase 8 app tests (`InitChain` + `FinalizeBlock` dispatch) |
| Read-only query service + CLI + pagination | Phase 9 keeper/CLI tests |
| **Multi-node** finalization + real claim + cross-node app-hash agreement | Phase 10 four-node localnet smoke (actually run) |
| Full app export/import round-trip with rewards state | Phase 10 app test |
| Fail-closed lifecycle (a rewards EndBlock error halts the block, no partial commit) | Phase 10 app test (`FinalizeBlock` returns error; height does not advance) |

## What remains pending (Phase 11)

:::warning Not yet proven
- **Production zero-premine monetary-genesis localnet drill.** The Phase 10
  multi-node proof ran on a funded development fixture; deterministic finalization
  is proven, but a true zero-premine start (supply rising only from emission) is
  not yet drilled across nodes.
- **Longer multi-epoch soak.** Phase 10 closed one epoch. Carry-forward across
  many epochs, pending-param activation at boundaries, and sustained multi-node
  agreement over a long run remain to be drilled.
- **Release-candidate operator drills** and documentation hardening.
:::

## Determinism posture

All rewards state transitions are integer-only and deterministic: no wall-clock
time, randomness, environment variables, or CometBFT-local config affects rewards
state, and finalization/claim iterate sorted collections (never raw Go map order).
The Phase 10 four-node app-hash agreement after finalization and after claim is
the cross-node evidence of this.

## Monetary caveat (terminal sub-cap dust)

Max supply is a hard upper bound, not a guarantee that every final unit is
emitted. Integer halving can floor the per-block subsidy to zero before cumulative
emitted reaches max supply exactly, leaving a small unmintable remainder below the
cap. This is intended; no tail rule force-mints the remainder. See
[Rewards economics](../rewards/economics.mdx#halving-and-the-supply-cap).
