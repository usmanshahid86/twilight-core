# Test Coverage & Suite Self-Assessment

An internal, code-grounded assessment of the automated test suite: measured coverage, the
untested surface, and how the negative and randomized tests compare to a standard Cosmos SDK
chain. This is a **self-assessment, not an independent security audit** (that is a separate,
pre-mainnet step — see [`SECURITY.md`](../../SECURITY.md)).

Numbers are point-in-time (measured against `main` at the time of writing) and drift as code
changes; re-run the method below to refresh them.

## Method

```bash
# Per-package statement coverage (each package's own tests):
go test ./... -covermode=atomic -coverprofile=cover.out

# Integrated coverage of the custom modules from ALL tests (app-level + sims count too):
go test ./... -coverpkg=./x/...,./app/...,./cmd/... -coverprofile=cover-int.out
go tool cover -func=cover-int.out
```

Generated files (`*.pb.go`, `*.pb.gw.go`, `*.pulsar.go`) are excluded when judging
handwritten coverage; they inflate line counts and are not hand-tested.

## Measured coverage

| Layer | Statement coverage | Notes |
|---|---|---|
| Raw integrated total | ~26% | diluted by generated code, `cmd/`, `tools/` (untested) |
| Handwritten module code (mean/func) | ~67% | 235 handwritten funcs |
| `x/rewards/keeper` | ~80% | finalize / emission / claims funcs ~90% |
| `x/coreslot/keeper` — business logic | ~86% | lifecycle, validator-set diff |
| `x/coreslot/keeper/query_server.go` | ~18% | gRPC query handlers largely untested |
| `app/` (integration + sims) | ~62% | |
| `x/rewards/client/cli` | ~46% | |
| `x/coreslot/client/cli` | ~0% | exercised by localnet drills (bash), not Go tests |

**Coverage is not 100%, and 100% is not the target.** The goal is high coverage on the
consensus- and value-critical keeper logic (met, ~80–90%), not on generated code, module
boilerplate (`module.go`, `codec.go`), or CLI wiring.

## Known untested surface

- **`x/coreslot/keeper/query_server.go`** — the gRPC query handlers (`Params`, `CoreSlot`,
  `CoreSlots`, `ActiveCoreSlots`, `CoreSlotByOperator`, `RewardWeight`, …) lack Go unit tests.
- **CLI layer** — `x/coreslot/client/cli/*` (tx, query, genesis) and part of the rewards CLI
  are covered only indirectly by localnet drills, not by Go tests.
- **`FinalizeEpoch` wrapper** (`x/rewards/keeper/finalize.go`) shows 0% while the inner
  `finalizeEpoch` is ~75%; confirm the production finalization entrypoint is the tested path.
- **Boilerplate** (`module.go`, `codec.go`) — low value, deliberately not chased.

## Negative tests

Well-structured and above-average for a young chain: dedicated guard-rejection suites
(`TestCoreSlotGuardRejections`, the rewards guards) that **isolate one guard per case and
assert a sentinel error**, plus model-predicted success/failure at every step of the seeded
sims (~100 negative assertions across the suite). Not exhaustive — the 0%-coverage functions
and untested within-function error branches are negative paths not yet reached.

## Randomized / fuzz testing

- **No native Go fuzzing** (`func Fuzz`, `go test -fuzz`) and **no property-based library**
  (e.g. `rapid`, `gopter`) are currently used.
- What exists are **seeded, model-based state-machine simulations** (`app/coreslot_sim_test.go`
  8 seeds × 300 ops; `app/rewards_sim_test.go` seeds 1–4) — reproducible and invariant-checking,
  described in [`module-simulations.md`](module-simulations.md). These sample a **thin, fixed**
  input space; they are not coverage-guided, have no shrinking, and keep no regression corpus.
- The highest-ROI fuzz surface is currently unfuzzed: malformed input to the parse / decode /
  validate paths (genesis JSON, params JSON, consensus-key/pubkey parsing, denom validation,
  offline tx-decode) — exactly where fuzzing finds panics and round-trip breaks.

## Comparison to a standard Cosmos SDK chain

Present and solid: keeper unit tests, `msgServer` tests, genesis **init/export round-trip**
tests, invariant tests, determinism assertions.

Deliberately different: standard chains register a `module.SimulationManager` and run the
full-app simulation suite (`TestFullAppSimulation`, `TestAppStateDeterminism`,
`TestAppImportExport`, benchmark, multi-seed-long). Twilight has none of these — the stock
simulator is hardwired to `staking`, which a PoA chain does not run, so it is replaced with the
bespoke seeded sims above. Reasonable and documented, but narrower.

Genuinely missing vs. a mature chain: app-level import/export determinism simulation, a
multi-seed-long CI job, native fuzzing on decode/parse surfaces, and Go-level gRPC-query / CLI
tests.

## Remediation roadmap (priority order)

1. **Native Go fuzz targets** on the parse/decode surfaces (genesis JSON, params JSON,
   pubkey/consensus-key, denom, tx-decode) with a seed corpus + a short CI fuzz smoke.
2. **Unit-test the CoreSlot gRPC query handlers**; add CoreSlot CLI Go tests or mark them
   explicitly drill-covered.
3. **Widen the seeded sims** — many more seeds in a nightly long job; persist failing seeds as a
   regression corpus; consider a property library for auto-shrinking.
4. **App-level import/export determinism test** (the PoA analog of `TestAppImportExport`).
5. **Confirm the `FinalizeEpoch` entrypoint coverage**; set a CI coverage floor on the keeper
   packages (e.g. ≥ 80%) rather than chasing 100%.
