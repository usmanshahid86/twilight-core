# Branch-Coverage Drills

Integration drills that force each value-critical economic branch of `x/rewards` to fire
through the assembled app and re-assert the accounting identity across the boundary. Halving
and treasury run through the real `FinalizeBlock` + `Commit` block loop; the multi-slot,
churn, carry-forward, and claim-range branches run through the keeper lifecycle
(`BeginBlock` / `EndBlock`) in the resolved runtime order.

## Why these exist

The [endurance soak](soak-c1-endurance.md) and the earlier app-level tests proved the rewards
**happy path** at scale: a single active slot, uniform active-block counts, and default
parameters. Under those parameters several core economic branches are *structurally
unreachable* — they never fire — even though each is covered by a keeper-level unit test:

- the first supply-threshold **halving** is ~1,460 epochs away from genesis;
- an even per-block subsidy divides evenly across a uniform active set, so the **carry-forward
  remainder** is always zero;
- a fixed, uniformly-active set never produces **non-uniform active-block** allocation or
  **active-set churn** mid-emission;
- the **treasury basis-point split** is off by default (0 bps), so the payout path is skipped;
- claims never approach the **`max_claim_epochs_per_tx`** cap.

These drills close that gap: they set parameters and drive blocks so that each branch is
exercised in a real block with the full app wiring — real mint and bank transfers, the real
CoreSlot keeper, and the resolved runtime `EndBlock` dispatch order (`coreslot` → `rewards`).

## What layer this is (and is not)

These are **integration tests** of the economic state machine, run in-process against the
assembled app. They are the standard home for forcing economic boundary conditions, because
the trigger states (a cumulative supply sitting just below a halving threshold, a nonzero
carry-in, a mid-epoch suspension) can be set precisely and asserted deterministically.

They are **not** multi-node tests. The finalization code path is identical whether invoked by
the runtime block loop or directly, and runtime `FinalizeBlock` dispatch is already proven
elsewhere (`app/rewards_hardening_test.go`); the single-slot drills here still run through the
real `FinalizeBlock` loop as a runtime anchor. What remains explicitly **out of scope** is
*cross-node agreement* on these specific transitions — proving that every validator computes
the same app-hash when a halving fires or the treasury splits. The endurance soak ran default
parameters, so no node has yet crossed a halving or split a treasury in a networked setting.
That is a determinism concern, not a logic concern, and is deferred to the multi-host endurance
track.

## The drills

Source: `app/rewards_drills_test.go`. Each drill asserts the rewards invariants
(`supply-cap`, `cumulative-emitted`, `module-balance-coverage`, `denom-correctness`,
`closed-epoch-immutability`) against the real bank in addition to the branch-specific checks.

| Drill | Branch forced | How it is forced | Headline result |
|---|---|---|---|
| `TestDrillHalvingSubsidyDecay` | Halving / subsidy decay | `maxSupply=1000`, `subsidy=100`, `epoch=5` blocks (real `FinalizeBlock`) | Epoch 1 mints 500 (tier 0); epoch 2 mints exactly 250 — subsidy halves across the `maxSupply/2` threshold |
| `TestDrillNonzeroCarryForward` | Carry-forward remainder | 2 equal slots, odd `subsidy=416191`, `epoch=1` | Epoch 1 leaves `carryOut=1`; epoch 2 folds `carryIn=1` into the pool (416191 → 416192) and divides cleanly |
| `TestDrillNonUniformBlocksAndChurn` | Non-uniform blocks + active-set churn | 2 slots; slot 1 suspended via the real CoreSlot msg server after block 1 of a 3-block epoch | Active blocks 1 vs 3 → rewards 75 vs 225 (ratio 1:3); the suspended-but-earned slot still finalizes and is claimable |
| `TestDrillTreasuryBpsSplit` | Treasury basis-point split | `subsidy=1000`, `epoch=2`, `EmissionTreasuryShareBps=1000` (real `FinalizeBlock`) | Emission 2000 minted in full; 200 sent to the treasury address; 1800 distributed to the slot |
| `TestDrillMaxClaimEpochsCap` | `max_claim_epochs_per_tx` cap | `MaxClaimEpochsPerTx=3`, 4 finalized epochs | A 4-epoch span is rejected and pays nothing; the maximum 3-epoch span succeeds; the leftover epoch stays claimable |
| `TestDrillCombinedAllBranches` | All of the above, composing | 3 slots, `maxSupply=1000`, `subsidy=100`, `epoch=3`, 10% treasury, `cap=2`, slot suspended mid-run | Branches compose; the full identity holds at every epoch boundary |

## The accounting identity under treasury

The zero-treasury identity is `claimed + unclaimed + carry = cumulative_emitted` (and, under
zero premine, `supply = cumulative_emitted`). The treasury split mints the full emission and
then sends a portion out of the rewards module, so the per-epoch relationship telescopes to:

```
cumulative_emitted = Σ treasury + Σ allocated + carry
supply             = cumulative_emitted           (zero premine; treasury coins stay in supply)
```

The combined drill checks this reconciliation against every finalized epoch at each boundary.
The module-balance coverage invariant is unaffected by the treasury split: treasury coins are
excluded from both sides (they are neither a claim record nor carry), so
`module_balance ≥ carry + Σ unclaimed` continues to hold.

## Result

All six drills pass. Each branch is confirmed to fire through the assembled app with correct,
exact accounting, and the invariant set holds throughout. Run with:

```
go test ./app/ -run TestDrill -count=1
```
