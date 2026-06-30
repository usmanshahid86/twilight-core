# Module Simulations

Seeded, deterministic, model-based **state-machine simulations** of the two custom modules
(`x/coreslot`, `x/rewards`). They drive long random sequences of operations against the
assembled app and assert the invariants after every step — randomized coverage that fixed
scenario tests can't give.

## Why these exist

The Cosmos SDK's own module-simulation framework (`x/simulation`, `simapp`) is hardwired to a
**staking** module to generate accounts, weights, and operations. Twilight is Proof-of-Authority
— `x/coreslot` owns the validator set and there is no staking/gov/mint/distribution — so the
stock simulator does not apply. Without an adaptation, every test was a *fixed* path, so
invariants were only ever checked along hand-picked scenarios. These sims exercise the invariants
over **seeded, randomly-sampled valid (and invalid) operation orderings** — many orderings a
fixed scenario never reaches, though sampled, not an exhaustive state-space search.

## What they are (and are not)

- **Deterministic randomized invariant testing.** Each seed uses `math/rand` seeded by the seed
  value; the seed is in the subtest name, so any failure reproduces exactly. No new dependency.
- **Not** stock SDK simulation, **not** multi-node / app-hash agreement evidence (that is the
  localnet/devnet track), and **not** an external audit.
- They verify **invariants across paths**, not exact economic *values* — e.g. the rewards sim
  confirms the accounting identity survives a halving crossing, but the exact halving math is
  verified by `TestDrillHalvingSubsidyDecay` and the emission unit tests.

## CoreSlot lifecycle sim — `app/coreslot_sim_test.go`

`TestCoreSlotLifecycleSimulation` (8 seeds × 300 ops). Random register / activate / inactivate /
suspend / remove interleaved with real `EndBlock` validator-set diffs. A reference model of slot
statuses is kept in lockstep with the chain; every operation's expected success/failure is
predicted from the model and asserted. After each step it checks: active-power conservation, the
`MinActiveSlots` floor, no duplicate active consensus keys, model/chain status agreement,
removed/suspended row retention, and that `EndBlock` emits **exactly** the validator updates
implied by the active-key diff (one update per added/removed key).

`TestCoreSlotGuardRejections` covers the negative-path guards (each scenario isolates one guard,
asserted by sentinel error): authority rejection, duplicate-consensus-key rejection, active-slot
removal rejection, `MaxActiveSlots` pressure, and the hard floor of one active validator (made
decisive via `AllowEmergencyBelowMinActive`).

## Rewards economic sim — `app/rewards_sim_test.go`

`TestRewardsEconomicSimulation` (8 seeds × 220 ops). Randomized params (subsidy, epoch length,
treasury bps, max supply, claim cap) over a multi-slot zero-premine chain, driving epoch advances,
mid-epoch churn, valid/invalid claims (predicted from chain state, with the exact rejection reason
asserted), and emergency pause/resume. After **every** operation it asserts the five rewards
invariants and the full accounting identity:

```
cumulative == Σ MintedEmission
cumulative == Σ TreasuryAmount + Σ AllocatedAmount + carry
supply     == cumulative                 (zero premine)
treasury balance == Σ TreasuryAmount
```

A rejected claim is additionally proven atomic (no module-balance or supply change). A standing
**coverage assertion** fails the suite if, across the seeds, any claimed branch was not actually
exercised (successful + rejected claims, cap rejection, replay rejection, claims-disabled
rejection, nonzero carry, treasury split, halving crossing) — guarding against silent regression
to a vacuous run.

## Result

Both sims pass; the suites are deterministic and reproducible. Adversarially reviewed, including
mutation testing (deliberately breaking `endblock.go` / `distribution.go` / `claims.go` and
confirming the sims fail). Run with:

```
go test ./app/ -run 'TestCoreSlotLifecycleSimulation|TestCoreSlotGuardRejections|TestRewardsEconomicSimulation' -count=1
```
