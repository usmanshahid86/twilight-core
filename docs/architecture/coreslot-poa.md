# Core-Slot PoA Architecture

Nyks v1 uses Cosmos SDK `v0.53.7` and CometBFT `v0.38.21`. The runtime app
includes auth, bank, consensus params, tx, and `x/coreslot`. It deliberately
omits staking, distribution, slashing, mint, and governance.

## Validator Ownership

`x/coreslot` owns validator admission, lifecycle state, consensus keys, and
the persisted last-applied validator set. It is the only module in the app's
EndBlock order and the only module whose genesis initialization returns
CometBFT validator updates.

Lifecycle messages never accept raw validator updates. They update core-slot
state. EndBlock derives the desired active validator map and returns a
canonical consensus-address-sorted diff. Active key rotation is delayed and
emits old-key power zero plus new-key active power in one atomic EndBlock.

## Authority

Genesis defines a normal authority and a separate emergency authority. Normal
authority controls registration, activation, removal, rotation, and params.
Emergency authority can suspend only. Operators can update their own payout
address and metadata and may self-inactivate while preserving `MinActiveSlots`.

## Token And Rewards

`unyks` is the native base denomination; `NYKS` is the six-decimal display
denomination. There is no inflation or staking reward flow in v1.
`OperatorRewardWeight` is stored separately from consensus power so a later
claim-based reward module can use it without changing consensus.

## Reward Weight and Consensus Power Interface

This interface is **frozen before `x/emissions` is added**. It is the contract
between validator selection (this module) and future reward distribution.

- **`CoreSlot.ConsensusPower` controls CometBFT validator power only.** It is the
  sole input to the `ValidatorUpdate` power that `x/coreslot` EndBlock returns.
  Active slots carry `Params.SlotVotingPower`; every non-active slot carries `0`.
- **`OperatorRewardWeight.FinalWeight` controls future emissions / reward
  distribution only.** It is never read by consensus.
- **`x/coreslot` EndBlock must never derive validator updates from reward
  weight.** EndBlock reads only slot `Status` and `ConsensusPower`
  (`x/coreslot/keeper/endblock.go`); reward weight is not referenced there.
- **`x/emissions` must never derive operator rewards from CometBFT voting
  power.** Reward eligibility and amounts must come from `OperatorRewardWeight`
  (and slot lifecycle status), not from `ConsensusPower` or the validator set.
- **Equal v1 distribution means equal `FinalWeight = 1.0`, not equal validator
  power.** The two coincide numerically in v1 (every active slot has power 1 and
  weight 1.0) but are independent dimensions: changing one must not be read as a
  change to the other.
- **Changing reward weight must not change any `ValidatorUpdate`.** A
  reward-weight edit changes payout math only; the consensus set and powers are
  unaffected. The EndBlock derivation and
  `x/coreslot/keeper/hardening_test.go::TestSameKeyPowerChangeSingleUpdate`
  depend only on status/power.

`x/emissions`, when added, consumes `x/coreslot` lifecycle **events** (e.g.
`coreslot_validator_update_emitted`, which carries `slot_id`,
`operator_address`, `consensus_address`, `power`, `height`) and the
`OperatorRewardWeight` table. It must not write to, nor take a power input from,
the validator-set derivation path.

## Future Modules

Economic staking may be introduced later as a separate non-validator-set
module. If ecosystem compatibility requires a staking-shaped query surface,
the acceptable fallback is a C1 read-only projection derived from coreslot.
It must never register staking mutation routes or staking EndBlock.

No Cosmos Enterprise PoA code, tests, comments, constants, proto, or store
layout were copied. Enterprise PoA was used only as an architectural checklist.
