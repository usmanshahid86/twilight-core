---
title: Validation Reports
---

# Validation Reports

The rewards module was built in phases, each with an **independent validation
pass**. The per-phase implementation and validation reports live under
`docs/research/` in the repository as local-only source material; they are
**summarized** here rather than republished, because they contain working notes
and absolute paths that are not appropriate for a public site.

:::note Current status
All phases through Phase 10 are validated. **Phase 11** (production zero-premine
genesis drill, longer multi-epoch soak, release-candidate drills) is **pending**.
Nothing below implies mainnet readiness.
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
| 10 | Multi-node finalization + claim; export/import; fail-closed | Validated | four-node localnet finalization + real claim with cross-node app-hash agreement; full app export/import; fail-closed `FinalizeBlock` | zero-premine drill; soak |
| 11 | Release-candidate drills + docs | **Pending** | — | production zero-premine localnet; multi-epoch soak; operator drills |

## What each layer of testing proves

| Test layer | Risk it covers |
|---|---|
| `x/rewards/keeper` unit tests | emission/distribution/claim/params **economics** |
| `x/rewards/types` tests | params validation, genesis schema |
| `x/rewards/client/cli` tests | CLI request/message construction |
| `app` tests | runtime wiring, export/import, fail-closed lifecycle |
| `make localnet-rewards-smoke` | **multi-node** finalization/claim determinism |

## Reproducing the validation

```bash
go test ./x/rewards/... -count=1
go test ./app -count=1
go test ./... -count=1
make localnet-rewards-smoke   # four-node finalization + claim
```

See [Localnet](../chain/localnet.md) and [Release Readiness](../chain/release-readiness.md).
