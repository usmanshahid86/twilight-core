---
title: Consensus & CoreSlot
---

# Consensus & CoreSlot

Twilight uses Cosmos SDK `v0.53.7` and CometBFT `v0.38.x`. The runtime app
includes `auth`, `bank`, `consensus` params, `tx`, `x/coreslot`, and `x/rewards`.
It deliberately omits `staking`, `distribution`, `slashing`, `mint`, and
`governance`.

## Validator ownership

`x/coreslot` owns validator admission, lifecycle state, consensus keys, and the
persisted last-applied validator set. It is the only module whose genesis
initialization returns CometBFT validator updates, and the only module that
returns validator updates from EndBlock.

Lifecycle messages never accept raw validator updates — they update CoreSlot
state. EndBlock derives the desired active validator map and returns a canonical
consensus-address-sorted diff. Active key rotation is delayed and emits the old
key with power zero plus the new key with active power in one atomic EndBlock.

## Authority and emergency authority

Genesis defines a **normal authority** and a separate **emergency authority**:

- Normal authority controls registration, activation, removal, rotation, and
  CoreSlot params.
- Emergency authority can **suspend** an active slot.
- Operators can update their own payout address and metadata, and may
  self-inactivate while preserving `MinActiveSlots`.

These same two authorities govern the rewards module: rewards params updates are
authorized by the CoreSlot **authority**, and rewards pause/resume by the CoreSlot
**emergency authority**. Rewards stores no authority of its own — see
[Rewards parameters](../rewards/params.md).

## Slot lifecycle and statuses

Slots move through `PENDING → ACTIVE → INACTIVE/SUSPENDED → REMOVED`. Only
`SLOT_STATUS_ACTIVE` slots are part of the validator set and earn rewards
active-block credit. Suspending an active slot is subject to a hard floor:
the last remaining active validator cannot be suspended (the set may never drain
to zero).

:::note
Suspend and remove **retain** the slot row, its operator/payout addresses, and
its reward-weight row (they change status and zero consensus power; they delete
only the operator/consensus *indexes*). This is why a slot that earned
active-block credit earlier in an epoch is still paid at finalization even if it
is suspended or removed before the epoch closes — see
[Active-block accounting](../rewards/economics.mdx#active-block-accounting).
:::

## Reward weight vs consensus power (frozen interface)

This is the contract between validator selection (CoreSlot) and reward
distribution (rewards). It is audit-critical:

- **`CoreSlot.ConsensusPower` controls CometBFT validator power only.** It is the
  sole input to the `ValidatorUpdate` power that CoreSlot EndBlock returns. Active
  slots carry `Params.SlotVotingPower`; every non-active slot carries `0`.
- **`OperatorRewardWeight.FinalWeight` controls reward distribution only** and is
  never read by consensus.
- **CoreSlot EndBlock never derives validator updates from reward weight.** It
  reads only slot `Status` and `ConsensusPower` (`x/coreslot/keeper/endblock.go`).
- **Rewards never reads `ConsensusPower` for accounting.** Reward eligibility and
  amounts come from active-block counters and `OperatorRewardWeight`, not from the
  validator set.
- In v1 (uniform distribution) every active slot has power `1` and weight `1.0`;
  the two coincide numerically but are independent dimensions. Changing a reward
  weight changes payout math only — it never changes a `ValidatorUpdate`.

CoreSlot emits lifecycle events (e.g. `coreslot_validator_update_emitted` with
`slot_id`, `operator_address`, `consensus_address`, `power`, `height`) that
indexers and the rewards module can consume without touching the validator-set
derivation path.

## Why staking is omitted

There is no economic staking, delegation, or slashing. Validator authority is an
explicit CoreSlot decision, not stake-weighted. If a staking-shaped query surface
is ever needed for ecosystem tooling, the intended approach is a read-only
projection derived from CoreSlot — never staking mutation routes or a staking
EndBlock. See `docs/security/staking-omission-or-inert-staking.md` in the
repository for the rationale.
