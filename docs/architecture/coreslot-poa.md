# Core-Slot PoA Architecture

Twilight v1 uses Cosmos SDK `v0.53.7` and CometBFT `v0.38.21`. The runtime app
includes auth, bank, consensus params, tx, `x/coreslot`, and `x/rewards`. It
deliberately omits staking, distribution, slashing, mint, and governance.

This document describes the CoreSlot module design. The decision to choose an
independent CoreSlot PoA module is recorded in [ADR-0001](adr/0001-coreslot-poa.md);
rewards and emission are specified in [ADR-0002](adr/0002-rewards-emission.md); and the
[architecture overview](overview.md) is the read-first system map.

## Validator Ownership

`x/coreslot` owns validator admission, lifecycle state, consensus keys, and
the persisted last-applied validator set. It is the only module in the app's
EndBlock order that returns CometBFT validator updates, and the only module
whose genesis initialization returns them.

Lifecycle messages never accept raw validator updates. They update core-slot
state. EndBlock derives the desired active validator map and returns a
canonical consensus-address-sorted diff. Active key rotation is delayed and
emits old-key power zero plus new-key active power in one atomic EndBlock.

## Authority

Genesis defines a normal authority and a separate emergency authority. Normal
authority controls registration, activation, removal, rotation, and params.
Emergency authority can suspend only. Operators can update their own payout
address and metadata and may self-inactivate while preserving `MinActiveSlots`.

## Token and Rewards

`utwlt` is the native base denomination (the only denom used for stateful
accounting); `twlt` is the six-decimal display denomination and `TWLT` is the
symbol. There is no inflation, staking, or `x/distribution` reward flow.

Operator rewards are owned by the shipped [`x/rewards`](../../x/rewards) module — a
bounded, deterministic block subsidy with supply-threshold halving, allocated to
active operators as entitlements and released by settlement (see
[ADR-0002](adr/0002-rewards-emission.md)).
`OperatorRewardWeight` is stored on the slot, separate from consensus power, so it
never affects validator selection; in v1 it is metadata only (see the interface
below).

## Reward Weight and Consensus Power Interface

This interface is the contract between validator selection (this module) and reward
distribution (`x/rewards`); it keeps the two dimensions independent.

- **`CoreSlot.ConsensusPower` controls CometBFT validator power only.** It is the
  sole input to the `ValidatorUpdate` power that `x/coreslot` EndBlock returns.
  Active slots carry `Params.SlotVotingPower`; every non-active slot carries `0`.
- **`x/coreslot` EndBlock never derives validator updates from reward weight.**
  EndBlock reads only slot `Status` and `ConsensusPower`
  (`x/coreslot/keeper/endblock.go`); reward weight is not referenced there.
- **`x/rewards` never derives rewards from CometBFT voting power.** In v1, reward
  allocation is by **uniform active-block participation** — each slot's share is
  proportional to the number of blocks it was active during the epoch — together with
  slot lifecycle status. Consensus power is never an input to payout.
- **`OperatorRewardWeight` is snapshotted but not used for allocation in v1.** The
  configured weight is recorded with each epoch's allocation for forward
  compatibility, but a weighted distribution mode is explicitly rejected in v1; only
  uniform active-block allocation is accepted (see
  [ADR-0002](adr/0002-rewards-emission.md)). Operators should not expect
  weight-proportional payouts in v1.
- **Changing reward weight must not change any `ValidatorUpdate`.** A reward-weight
  edit changes future payout configuration only; the consensus set and powers are
  unaffected. The EndBlock derivation and
  `x/coreslot/keeper/hardening_test.go::TestSameKeyPowerChangeSingleUpdate` depend
  only on status and power.

`x/rewards` reads `x/coreslot` active-slot status to credit active-block participation;
it does not write to, nor take a power input from, the validator-set derivation path.

## Future Modules

Economic staking may be introduced later as a separate non-validator-set
module. If ecosystem compatibility requires a staking-shaped query surface,
the acceptable fallback is a C1 read-only projection derived from coreslot.
It must never register staking mutation routes or staking EndBlock.

A proprietary, evaluation-only PoA implementation was reviewed for architectural
comparison only; no code, tests, proto, or store layout were reused.
