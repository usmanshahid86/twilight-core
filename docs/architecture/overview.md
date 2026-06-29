# Twilight Core Architecture Overview

The read-first map of the Twilight Core chain. It orients; the design rationale lives in the
[ADRs](adr/README.md), and the supporting evidence lives in the
[baseline review](../reviews/2026-06-29-baseline-coreslot-rewards.md) and
[validation summary](../testing/validation-summary.md).

## 1. Summary

Twilight Core is a [Cosmos SDK](https://github.com/cosmos/cosmos-sdk) `v0.53.7` /
[CometBFT](https://github.com/cometbft/cometbft) `v0.38.21` **Proof-of-Authority** chain.
Validator membership is **authority-governed**, not token-staked: a dedicated module,
[`x/coreslot`](../../x/coreslot), owns the validator-set lifecycle and is the **only** module
that emits CometBFT validator updates. Operator rewards come from a bounded, deterministic
block subsidy in [`x/rewards`](../../x/rewards) — not from `x/distribution`. The native base
denomination is `utwlt` (`twlt` display, `TWLT` symbol, six decimals).

## 2. System model

The chain is a standard SDK application **minus** the staking-economic stack. There is no
delegation, no slashing, and no on-chain governance module. The validator set is a fixed-cap
set of authority-governed *CoreSlots*; operators are admitted to slots and run validators.
Consensus safety and liveness therefore depend on the authority-managed slot set rather than
on bonded stake. See [ADR-0001](adr/0001-coreslot-poa.md) for why this was chosen over a
staking-backed design, and [`coreslot-poa.md`](coreslot-poa.md) for the module design.

## 3. Wired and omitted modules

**Wired:** `auth`, `bank`, `consensus`, `tx`, `x/coreslot`, `x/rewards`.

**Intentionally omitted from the module manager:** `x/staking`, `x/distribution`, `x/mint`,
`x/gov`, `x/slashing`. They are not registered, expose no message server, and cannot emit
validator updates. This omission is a structural property of the app — standard account and
token operations (`bank`, `auth`) work as usual, but anything that assumes staking, on-chain
governance, or distribution does not exist here.

## 4. Validator-set flow

`x/coreslot` holds each slot's identity, consensus key, status, consensus power, payout
address, and reward weight. Slots move through an explicit lifecycle:

```
register → activate → inactivate / suspend → reactivate → remove   (+ consensus-key rotation)
```

Lifecycle messages express *intent*; they never accept raw validator updates. The module's
EndBlocker derives the desired active set from slot state, diffs it against the persisted
last-applied set, and returns a canonical, consensus-address-sorted set of
`[]abci.ValidatorUpdate`. **`x/coreslot` is the only module in the EndBlock order that returns
validator updates** — a single, reviewable control plane for consensus-set changes. (The SDK
halts a block if two modules both return updates, so single ownership is enforced, not just
preferred.) Active-key rotation emits the old key at power 0 and the new key at active power
atomically.

## 5. Rewards and emission flow

Operator rewards are a **bounded block subsidy**, settled per epoch:

- **Emission.** A per-block subsidy accrues over an epoch (`EpochLengthBlocks`, default
  `17,280`). At epoch finalization the epoch's emission is minted once into the rewards module
  account and recorded against `cumulative_emitted`.
- **Bounded supply + halving.** Emission is capped by `MaxSupply` and never exceeds it. The
  subsidy halves on a **supply threshold** (50%, 75%, … of `MaxSupply`), not a block height,
  and is evaluated **per block** — a threshold crossed mid-epoch reduces the rate for the rest
  of that epoch.
- **Allocation.** Each epoch's pool is split by **uniform active-block participation**: a
  slot's share is proportional to the blocks it was active that epoch. The configured reward
  weight is snapshotted but not used for allocation in v1.
- **Claims.** Allocations are claimable per `(slot, epoch)`. Triggering a claim is
  **permissionless**, and payout goes to the payout address **snapshotted at finalization** —
  not to the caller. Claimed records are marked claimed (no replay), and a multi-epoch claim
  range is atomic.

The default genesis has **no premine**. See [ADR-0002](adr/0002-rewards-emission.md) for the
full rewards/emission decision and parameters.

## 6. Consensus-path safety principles

State-machine code that runs on the consensus path is held to determinism and fail-closed
rules (a cross-cutting principle, recorded across the ADRs and the contributor
[review process](../../REVIEW.md) rather than a separate ADR):

- **Deterministic, integer-only.** No wall-clock time, floating point, randomness, or
  map-iteration-order dependence in consensus paths; emission math is integer arithmetic.
- **Fail-closed.** Epoch finalization runs in a cache context that is written only on full
  success — on any unexpected condition it errors without committing partial state.
- **Single validator-update emitter.** Only `x/coreslot` emits validator updates (§4).

## 7. v1 scope and non-goals

- **In v1:** authority-governed CoreSlot validator set, epoch emission with supply-threshold
  halving, uniform active-block allocation, claim-based payout, emergency pause (emission,
  settlement, and claims independently), zero-premine default. Treasury share is implemented
  but off by default (parameter-flippable).
- **Not in v1:** staking, slashing, on-chain governance, `x/distribution`, and `x/mint`;
  **weighted rewards** and **fee-funded rewards** are present in the parameter schema but are
  code-gated (rejected by validation and finalization) and require implementation before they
  can be enabled.

## 8. Related documents

- [ADR-0001 — CoreSlot Proof-of-Authority](adr/0001-coreslot-poa.md)
- [ADR-0002 — Rewards emission and supply-threshold halving](adr/0002-rewards-emission.md)
- [CoreSlot module design](coreslot-poa.md)
- [Baseline review — CoreSlot and Rewards](../reviews/2026-06-29-baseline-coreslot-rewards.md)
- [Validation summary](../testing/validation-summary.md)
