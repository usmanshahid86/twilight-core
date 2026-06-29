# Validation Summary

A public index of the validation evidence for the consensus- and value-critical paths. It
points to the evidence; it is not a raw log dump. Supporting design is in the
[architecture overview](../architecture/overview.md) and the
[baseline review](../reviews/2026-06-29-baseline-coreslot-rewards.md).

## Evidence by claim

| Claim | Evidence | Result | Notes |
|---|---|---|---|
| Only `x/coreslot` emits validator updates | `app/app_test.go` (asserts a single validator-update EndBlock emitter) | PASS | Enforced; a second emitter would halt the block |
| Staking / distribution / mint / gov / slashing are not wired | `app/app_test.go` (asserts the modules are absent from the module manager) | PASS | Structural omission |
| CoreSlot lifecycle transitions are correct | `x/coreslot/keeper/*_test.go` (keeper, replacement, hardening, genesis carryover) | PASS | register/activate/inactivate/suspend/reactivate/remove + rotation |
| Reward-weight edits never change validator updates | `x/coreslot/keeper/hardening_test.go` | PASS | Consensus power and reward weight are independent |
| Emission never exceeds `MaxSupply` | `x/rewards/keeper/emission_test.go`, `finalize_test.go` | PASS | Per-block clip + finalization guard |
| Halving handles mid-epoch / multi-threshold crossings deterministically | `x/rewards/keeper/emission_test.go` (exact-value cases) | PASS | Integer math; crossing block clipped to threshold |
| `MaxSupply` is immutable after genesis | `x/rewards/keeper/params_update_test.go` | PASS | Parameter update rejected |
| Rewards allocate by active-block participation | `x/rewards/keeper/distribution_test.go` | PASS | `amount = pool × blocks_active / Σ blocks_active` |
| Carry-forward remainder is correct | `x/rewards/keeper/distribution_test.go`, `finalize_test.go` | PASS | Remainder → next epoch's pool |
| Claim replay is rejected | `x/rewards/keeper/claims_test.go` | PASS | Claimed `(slot, epoch)` cannot be re-claimed |
| Claim ranges are atomic | `x/rewards/keeper/claims_test.go` | PASS | Missing/already-claimed epoch fails the whole claim |
| Permissionless claim pays the snapshotted payout address | `x/rewards/keeper/claims_test.go`, `app/rewards_integration_test.go` | PASS | Caller ≠ payee; payout snapshotted at finalization |
| Accounting identity holds | `x/rewards/keeper/invariants_test.go` + endurance run | PASS | `claimed + unclaimed + carry = cumulative_emitted` |
| Emission math is pure (no time/float/random) | `x/rewards/keeper/hardening_test.go` | PASS | Asserts forbidden constructs are absent |
| Rewards genesis export/import is deterministic | `x/rewards/keeper/genesis_test.go` | PASS | Export then import reproduces identical state |
| Emergency pause (emission / settlement / claims) behaves correctly | `x/rewards/keeper/finalize_test.go`, `claims_test.go` | PASS | Independent flags |
| Local end-to-end sanity | `make localnet-smoke`, `make localnet-rewards-smoke` (`scripts/localnet/`) | PASS | Multi-node localnet smoke |
| Operational drills (lifecycle, restart/rotation, quorum) | `make drills` (`scripts/localnet/`) | PASS | Lifecycle, restart-rotation, quorum |
| Cross-node app-hash / validator-hash agreement | `scripts/localnet/agree.sh` | PASS | App, validators, and next-validators hash agreement |
| Multi-day endurance | [Soak C1 report](soak-c1-endurance.md) | PASS | 48 h continuous; exact accounting; zero-premine integrity |
| Off-happy-path economic branches are exercised in integration | [Branch-coverage drills](branch-coverage-drills.md) (`app/rewards_drills_test.go`) | PASS | Halving and treasury via `FinalizeBlock` + `Commit`; multi-slot, churn, carry-forward, and claim-range branches via the direct keeper lifecycle in runtime order; identity re-asserted |
| No known reachable dependency / toolchain vulnerabilities | `govulncheck` (blocking in CI, `.github/workflows/ci.yml`) | PASS | Go 1.25.11 + grpc v1.79.3 + x/net v0.53.0; 0 advisories affecting called symbols (non-called advisories in required modules do not gate) |

## Known exclusions

- **No independent external audit yet.** A pre-mainnet external review is still required.
- **Weighted rewards and fee-funded rewards** are not implemented beyond their v1 rejection
  guards; only uniform active-block allocation, carry-forward remainder, and supply-threshold
  halving are active.
- **Stock SDK simulation** is not used: it assumes a staking module, which Twilight
  intentionally omits. Determinism and import/export are covered by module-specific tests and
  cross-node hash agreement instead.
- **Endurance coverage is happy-path.** The multi-day soak ran default parameters, so the
  off-the-happy-path economic branches (halving, non-zero carry, non-uniform participation,
  treasury, active-set churn, the claim-range cap) never fired during it. Those branches are now
  exercised by the [branch-coverage drills](branch-coverage-drills.md) — halving and treasury
  through `FinalizeBlock` + `Commit`, the rest through the direct keeper lifecycle in runtime
  order; what remains deferred is *cross-node agreement* on those specific transitions (e.g. every
  validator computing the same app-hash when a halving fires), which belongs to the multi-host
  endurance track, not a single-process test.
- Public validation evidence is curated and sanitized; raw local run artifacts remain internal.
