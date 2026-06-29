# ADR-0002: Rewards emission and supply-threshold halving

- **Status:** Accepted — implemented in [`x/rewards`](../../../x/rewards)
- **Date:** 2026-06-29
- **Relates to:** [ADR-0001](0001-coreslot-poa.md) (reward weight is separate state from
  consensus power)

## Summary

In short: Twilight pays its CoreSlot operators from a bounded, deterministic block subsidy —
not from token staking or `x/distribution`. Emission accrues per block, is minted once per
epoch into the rewards module account, and is allocated to eligible operators by active-block
participation; operators then claim their recorded allocations. Supply is capped, and the
subsidy halves on a **supply threshold**, not a block height. The default genesis has
**no premine**.

## Context

Twilight has no `x/staking`, `x/distribution`, or `x/mint` (see [ADR-0001](0001-coreslot-poa.md)),
so the standard "stake → power → distribution" reward path does not exist. The chain still
needs to pay the operators who run the validator set. The design constraints were:

- **Deterministic and bounded.** Emission must be reproducible across nodes (a consensus
  path) and converge to a hard maximum supply — no open-ended inflation.
- **Decoupled from consensus power.** Per [ADR-0001](0001-coreslot-poa.md), reward weight is
  separate state from voting power, so operators can later be paid on a weight independent of
  their equal consensus power.
- **Self-contained.** No reliance on `x/distribution`/`x/mint` machinery that the chain does
  not wire.
- **Auditable accounting.** Every minted unit must be attributable: `claimed + unclaimed +
  carry = cumulative_emitted` (which equals total supply under the default zero-premine
  genesis).

## Decision

**Emit a bounded block subsidy on an epoch schedule, with supply-threshold halving, allocated
to eligible active operators and paid out by claim.**

- **Epoch settlement.** Subsidy accrues per block; an epoch is a fixed block count
  (`EpochLengthBlocks`, default `17280`). At epoch finalization the epoch's emission is
  **minted once** into the rewards module account and recorded against `cumulative_emitted`.
- **Bounded supply.** `MaxSupply` is a hard cap (default `21000000000000 utwlt` = 21,000,000
  `twlt`); emission is clipped so cumulative emission never exceeds it, after which the subsidy
  is zero.
- **Supply-threshold halving.** The per-block subsidy halves each time cumulative emission
  crosses the next supply threshold (50%, 75%, 87.5%, … of `MaxSupply`) — **not** a fixed
  block-height schedule. `HALVING_MODE_SUPPLY_THRESHOLD` is the only supported mode; any other
  is rejected at parameter validation.
- **Halving is evaluated per block, including mid-epoch.** An epoch's emission is the **sum of
  per-block subsidies**, with the running cumulative updated after every block — not a single
  rate applied to the whole epoch. If cumulative emission crosses a threshold partway through
  an epoch, the blocks before the crossing use the higher subsidy, the crossing block is
  clipped to land exactly on the threshold, and the remaining blocks of the same epoch use the
  halved subsidy. This is exact integer arithmetic and therefore deterministic across nodes.
  It is not a rare edge case: with the default parameters the first 50% threshold is reached
  at roughly 1,460 daily epochs (~4 years) and lands mid-epoch, so one epoch straddles each
  halving boundary by design.
- **Allocation by uniform active-block participation (v1).** An epoch's pool is split across
  the eligible active slots in proportion to how many blocks each was active during the epoch:
  `amount = pool × blocks_active / Σ blocks_active`. Slots active for the **whole** epoch share
  equally; a slot active for only part of the epoch receives proportionally less. Active-block
  participation is accrued per block *during* the epoch, so eligibility is epoch-time: a later
  key rotation, suspension, removal, or payout-address change does **not** rewrite an
  already-recorded allocation. The configured per-slot `RewardWeight` is snapshotted into the
  record but is **not** used for allocation in v1.
- **Carry-forward remainder.** The unallocated pool after integer division (the sum of
  allocations subtracted from the pool — including the case of zero eligible slots) is the
  `carry_forward_remainder`. It is persisted and added to the **next** epoch's pool;
  `REMAINDER_POLICY_CARRY_FORWARD` is the only policy supported in v1.
- **Claim-based payout.** Allocations are claimable per `(slot, epoch)` until claimed.
  Triggering a claim is **permissionless** (any signer); payout goes to the payout address
  **snapshotted at epoch finalization** — not to the caller, and not to the slot's *current*
  payout address. Each claimed `(slot, epoch)` record is marked claimed, preventing
  replay/double-claim. A claim may span an epoch range (bounded by `MaxClaimEpochsPerTx`,
  default `100`), and the range is **atomic**: a missing (unfinalized) or already-claimed epoch
  fails the whole claim.
- **No premine.** The default genesis mints nothing before the first epoch finalizes.
- **Emergency controls.** An emergency authority can independently pause emission, epoch
  settlement, or claims.

## Implementation refinements (decision → shipped code)

v1 ships **emission-only, uniform active-block** rewards. Three further economic levers exist
in the parameter/proto schema but fall into two distinct categories — they are **not** uniformly
"on by config":

1. **Weighted rewards — rejected, not merely off.** The configured-reward-weight path
   (`WeightedRewardsEnabled`, or a non-uniform `DistributionMethod`) is rejected by **both**
   parameter validation and epoch finalization (`ErrUnsupportedFeature`). Enabling it is a
   code-and-test change, not a parameter flip. Only `DISTRIBUTION_METHOD_UNIFORM_ACTIVE_BLOCKS`
   and `REMAINDER_POLICY_CARRY_FORWARD` are accepted.
2. **Fee-funded rewards — rejected.** Fee collection and distribution are blocked at validation
   and finalization; the distributable-fee amount is always zero, and ordinary transaction fees
   remain in the standard auth fee-collector — they do **not** flow to rewards. Enabling is a
   code-and-test change.
3. **Treasury share — implemented, defaults off.** The emission treasury split is applied in
   finalization (with a `treasury_paid` event) and is **flippable by parameter alone** — a
   non-zero `EmissionTreasuryShareBps` plus a valid `TreasuryAddress`. The v1 defaults are `0`
   / empty, so 100% of emission flows to operators.

The proto/parameter fields for all three exist, so turning any on needs **no schema migration**
— but only the treasury split is switch-on-ready; weighted rewards and fees require
implementation. `MaxSupply` is immutable: a parameter update may not change it.

## Consequences

**Positive.** Supply is hard-capped and the emission curve is fully deterministic, so every
node computes the same subsidy and the same `cumulative_emitted`. Reward accounting is
auditable by construction (`claimed + unclaimed + carry = cumulative_emitted`, equal to total
supply under zero-premine genesis), which the soak runs check continuously. The claim model
decouples *earning* from *paying out*: an operator's allocation is durable on-chain and can be
claimed later, in ranges, to the payout address recorded at finalization. Keeping weighted
rewards and fee distribution gated (and the treasury split off by default) in v1 keeps the live
surface small, while the schema leaves room for a deliberate, tested activation later.

**Negative / costs.** Twilight owns the emission and claim accounting itself rather than
inheriting `x/distribution`. Finalization runs in the consensus path, so it must be
deterministic and fail-closed; the implementation runs in a cache context written only on full
success. The economic branches beyond the v1 operator-emission path have different activation
profiles: treasury share is implemented but off by default, while weighted rewards and
fee-funded rewards are schema-present but code-gated and require implementation and validation
before activation.

## Alternatives considered

| Decision point | Chosen | Rejected alternative | Why |
|---|---|---|---|
| Halving trigger | Supply threshold | Fixed block height | Ties issuance to actual supply progress, not wall-clock/height drift; converges cleanly to the cap |
| Issuance source | Custom bounded subsidy | `x/mint` + `x/distribution` | Those modules are not wired (ADR-0001); custom emission stays deterministic and self-contained |
| Payout timing | Claim-based, per `(slot, epoch)` | Auto-pay every epoch | Durable, range-claimable allocations; decouples earning from payout; avoids per-epoch mass transfers |
| v1 allocation | Uniform active-block | Configured-reward-weight from day one | Active-block participation is the simplest correct v1; the configured-weight path is gated until implemented and validated |
| Supply | Hard cap, no premine | Open inflation / premine | Bounded, auditable, and credibly neutral at genesis |

## v1 scope (what is on vs. parameterized-off)

- **On:** epoch emission, supply-threshold halving, hard max supply, uniform active-block
  allocation, claim-based payout, emergency pause (emission / settlement / claims,
  independently), zero-premine default.
- **Schema-present but gated (code required to enable):** weighted rewards, fee-funded rewards.
- **Implemented but off by default (parameter-flippable):** treasury share.

## Enforcement

- The default genesis is asserted to be **zero-premine** (`cumulative_emitted == "0"` before
  the first finalization).
- The accounting identity `claimed + unclaimed + carry = cumulative_emitted` (equal to total
  supply only under zero-premine genesis) is checked continuously by the soak harness and by
  the module-balance coverage invariant.
- Parameter validation rejects any halving mode other than `HALVING_MODE_SUPPLY_THRESHOLD`,
  any change to `MaxSupply`, and any non-uniform distribution, weighted-rewards, or
  fee-distribution setting.
- Block-by-block emission — including a threshold crossing mid-epoch, multiple thresholds
  crossed within a single computation, and cap clipping (emission never exceeds `MaxSupply`) —
  is covered by unit tests asserting exact emitted and post-state cumulative values.
- Double-claim rejection and claim-range atomicity are covered by unit/integration tests, and
  rewards genesis export/import is covered by a deterministic round-trip test.
