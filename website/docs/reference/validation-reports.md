---
title: Validation Reports
---

# Validation Reports

The rewards module was built in phases, each with a **validation pass**. The
detailed per-phase working notes live under `docs/research/` in the repository as
local-only source material and are **summarized** here. Curated, sanitized
evidence is published in the repository under `docs/testing/` — including the
endurance soak, cross-host fault-tolerance, module-simulation, and branch-coverage
reports, indexed by `validation-summary.md`.

:::note Current status
All phases through Phase 10 are validated, and the Phase 11 launch-readiness work
— the zero-premine endurance soak, operational and branch-coverage drills,
cross-host fault-tolerance drills, and randomized module simulations — is
complete. What remains before mainnet is the production on-chain-upgrade procedure
and an independent external audit. Nothing here implies mainnet readiness.
:::

| Phase | Scope | Status | Key proof | Deferred to next |
|---|---|---|---|---|
| 1 | Proto + types scaffold | Validated | deterministic proto gen; type/default/validation tests | — |
| 2 | Keeper state + CoreSlot read interfaces | Validated | collections schema; genesis round-trip; read-only CoreSlot API | — |
| 3 | Emission math | Validated | exact halving/subsidy/clipping vectors; pure functions | — |
| 4 | Active-block accounting | Validated | BeginBlock crediting; epoch-boundary helpers; pause-safe | — |
| 5–7 | Finalization, distribution, claims, params, pause/resume, invariants | Validated | atomic (cache-context) finalization; cap-before-mint; grouped-payout claims; 5 invariants | — |
| 8 | App / runtime wiring | Validated | `InitChain` + `FinalizeBlock` dispatch; module accounts; sole validator-update emitter; app-routed authority msg | — |
| 9 | Query service + CLI | Validated (with required fixes, then applied) | read-only queries; pagination; CLI behavior tests | — |
| 10 | Multi-node finalization + claim; export/import; fail-closed | Validated | four-node localnet finalization + real claim with cross-node app-hash agreement; full app export/import; fail-closed `FinalizeBlock` | — |
| 11 | Launch-readiness: soak, drills, simulations | Validated | 48 h zero-premine endurance soak; operational + branch-coverage drills; cross-host fault-tolerance drills; randomized module simulations | production upgrade procedure; external audit |

## What each layer of testing proves

| Test layer | Risk it covers |
|---|---|
| `x/rewards/keeper` unit tests | emission/distribution/claim/params **economics** |
| `x/rewards/types` tests | params validation, genesis schema |
| `x/rewards/client/cli` tests | CLI request/message construction |
| `app` tests | runtime wiring, export/import, fail-closed lifecycle |
| `make localnet-rewards-smoke` | **multi-node** finalization/claim determinism |
| `make drills` + branch-coverage drills | lifecycle, restart/rotation, quorum, and off-happy-path economic branches |
| endurance soak + cross-host fault-tolerance drills | determinism, exact accounting, and liveness/safety/recovery over time and across hosts |
| randomized module simulations | invariant coverage over seeded valid/invalid operation orderings |

## Reproducing the validation

```bash
go test ./x/rewards/... -count=1
go test ./app -count=1
go test ./... -count=1
make localnet-rewards-smoke   # four-node finalization + claim
```

See [Localnet](../chain/localnet.md) and [Release Readiness](../chain/release-readiness.md).
