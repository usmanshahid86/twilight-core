package keeper

import (
	"context"

	"cosmossdk.io/collections"

	abci "github.com/cometbft/cometbft/abci/types"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/x/coreslot/types"
)

func (k Keeper) InitGenesis(ctx context.Context, genesis *types.GenesisState) ([]abci.ValidatorUpdate, error) {
	if err := genesis.Validate(); err != nil {
		return nil, err
	}
	// Total preflight over the COMPLETE input, before the first write.
	//
	// GenesisState.Validate is a pure types-level check with no access to the
	// chain's initial height or to the injected app-derived address capability,
	// so it decides everything the records alone can decide and no more. The two
	// remaining legs are enforced here.
	//
	// Both run as separate passes rather than inside the write loop deliberately:
	// a check interleaved with writes would persist params and the first few
	// slots before discovering a blocked address or a misnormalized height in a
	// later one, leaving a partially imported chain behind a returned error.
	initialHeight, err := genesisInitialHeight(ctx)
	if err != nil {
		return nil, err
	}
	if err := validateGenesisInitialHeight(genesis, initialHeight); err != nil {
		return nil, err
	}
	if err := k.validateGenesisEconomicAddresses(genesis); err != nil {
		return nil, err
	}
	if err := k.Params.Set(ctx, *genesis.Params); err != nil {
		return nil, err
	}
	for _, slot := range genesis.Slots {
		key, _, err := consensusKey(slot.ConsensusPubkey)
		if err != nil {
			return nil, err
		}
		if err := k.Slots.Set(ctx, slot.SlotId, *slot); err != nil {
			return nil, err
		}
		if err := k.ByOperator.Set(ctx, slot.OperatorAddress, slot.SlotId); err != nil {
			return nil, err
		}
		if err := k.ByConsensus.Set(ctx, key, slot.SlotId); err != nil {
			return nil, err
		}
		// ACTIVE membership is established here, in the same pass that writes the
		// record, so the index cannot start life disagreeing with the slot rows.
		if slot.Status == types.SlotStatus_SLOT_STATUS_ACTIVE {
			if err := k.setSlotActive(ctx, slot.SlotId); err != nil {
				return nil, err
			}
		}
	}
	for _, policy := range genesis.SelectionPolicies {
		if err := k.SelectionPolicies.Set(ctx, policyKey(policy.SlotId, policy.PolicyVersion), *policy); err != nil {
			return nil, err
		}
	}
	for _, rotation := range genesis.PendingKeyRotations {
		if err := k.Rotations.Set(ctx, rotation.SlotId, *rotation); err != nil {
			return nil, err
		}
	}
	for _, reservation := range genesis.ReservedConsensusAddresses {
		key := fmtHex(reservation.ConsAddress)
		if err := k.Reserved.Set(ctx, key, *reservation); err != nil {
			return nil, err
		}
	}
	for _, weight := range genesis.RewardWeights {
		if err := k.RewardWeights.Set(ctx, weight.SlotId, *weight); err != nil {
			return nil, err
		}
	}
	// GenesisState.Validate already required NextSlotId to exceed every assigned
	// identifier, so it is stored as supplied rather than recomputed. Deriving it
	// here instead would silently repair a genesis file that does not meet the
	// contract, which §80 does not authorize.
	if err := k.NextSlotID.Set(ctx, genesis.NextSlotId); err != nil {
		return nil, err
	}
	updates, err := k.diffAndPersist(ctx)
	if err != nil {
		return nil, err
	}
	if err := k.assertGenesisValidatorConsistency(ctx, updates); err != nil {
		return nil, err
	}
	return updates, nil
}

// validateGenesisInitialHeight pins the fresh-genesis heights §80 normalizes,
// against the chain's own initial height rather than a second independent source.
//
// PENDING rows carry the never-activated sentinel and were already checked by
// GenesisState.Validate; what needs the chain's height is the ACTIVE case, where
// both the activation height and the reward-accounting effective height must be
// initial_height exactly. Fresh genesis is the explicit exception to the runtime
// H+1 rule: a genesis ACTIVE slot is effective from the first block, not the one
// after it.
// genesisInitialHeight resolves the height the chain's first block will carry,
// from the context InitGenesis is given.
//
// The SDK's InitChain deliberately runs genesis with a block height of ZERO for a
// chain starting at height 1 — "On a new chain, we consider the init chain block
// height as 0, even though req.InitialHeight is 1 by default" — and only puts a
// real height on the header when InitialHeight is greater than 1. baseapp applies
// exactly this normalization to its own copy ("If initial height is 0, set it to
// 1"), so mirroring it here reads the SDK's value under the SDK's convention
// rather than introducing a second, independent notion of the initial height.
//
// A negative height cannot be produced by a well-formed InitChain and is refused
// rather than normalized, because silently treating it as 1 would let a
// nonsensical context define consensus state.
func genesisInitialHeight(ctx context.Context) (int64, error) {
	height := sdk.UnwrapSDKContext(ctx).BlockHeight()
	if height < 0 {
		return 0, types.ErrInvalidGenesis.Wrapf("genesis context height %d is negative", height)
	}
	if height == 0 {
		return 1, nil
	}
	return height, nil
}

func validateGenesisInitialHeight(genesis *types.GenesisState, initialHeight int64) error {
	if initialHeight < 1 {
		return types.ErrInvalidGenesis.Wrapf("initial height must be at least 1, is %d", initialHeight)
	}
	for _, slot := range genesis.Slots {
		if slot.Status != types.SlotStatus_SLOT_STATUS_ACTIVE {
			continue
		}
		if slot.ActivatedHeight != initialHeight || slot.ActivationEffectiveHeight != initialHeight {
			return types.ErrInvalidGenesis.Wrapf(
				"active slot %d must have activated and activation-effective heights equal to the initial height %d",
				slot.SlotId, initialHeight)
		}
	}
	for _, policy := range genesis.SelectionPolicies {
		if policy.ValidFromHeight != initialHeight {
			return types.ErrInvalidGenesis.Wrapf(
				"slot %d policy must be valid from the initial height %d, is %d",
				policy.SlotId, initialHeight, policy.ValidFromHeight)
		}
	}
	return nil
}

// assertGenesisValidatorConsistency checks that the ACTIVE slot set, the ACTIVE
// membership index, the persisted LastApplied set and the emitted validator
// updates all describe the same slots — in BOTH directions. "Every ACTIVE slot
// has a validator" is only half of the contract; the half that catches a
// silently dropped or duplicated validator is that nothing else appears.
//
// This runs after the writes on purpose: it is not input validation, which has
// already completed in full, but an assertion over state this function just
// produced. It should be unreachable, and an InitGenesis error aborts chain
// start outright rather than leaving a partially imported chain behind.
//
// The equality with the CometBFT genesis validator set is enforced one level up:
// baseapp compares the updates returned here against the genesis validators and
// refuses to start on any mismatch. x/coreslot remains the sole source of those
// updates.
func (k Keeper) assertGenesisValidatorConsistency(ctx context.Context, updates []abci.ValidatorUpdate) error {
	activeSlots, err := k.GetActiveSlots(ctx)
	if err != nil {
		return err
	}
	active := make(map[uint64]struct{}, len(activeSlots))
	for _, slot := range activeSlots {
		active[slot.SlotId] = struct{}{}
	}
	// Cross-check the index against the records independently of GetActiveSlots,
	// which reads the index: walk every stored slot and confirm the two sets
	// coincide. This is the one place a full walk is acceptable — it runs once at
	// chain start, not per block, and its whole purpose is to prove the index is
	// a faithful view before any consensus path starts trusting it.
	var recordActive uint64
	if err := k.Slots.Walk(ctx, nil, func(id uint64, slot types.CoreSlot) (bool, error) {
		_, indexed := active[id]
		if (slot.Status == types.SlotStatus_SLOT_STATUS_ACTIVE) != indexed {
			return true, types.ErrInvalidGenesis.Wrapf("slot %d status and active index disagree", id)
		}
		if slot.Status == types.SlotStatus_SLOT_STATUS_ACTIVE {
			recordActive++
		}
		return false, nil
	}); err != nil {
		return err
	}
	if recordActive != uint64(len(active)) {
		return types.ErrInvalidGenesis.Wrap("active index holds entries with no slot record")
	}

	lastApplied := map[uint64]struct{}{}
	if err := k.LastApplied.Walk(ctx, nil, func(_ string, validator types.LastAppliedValidator) (bool, error) {
		if _, dup := lastApplied[validator.SlotId]; dup {
			return true, types.ErrInvalidGenesis.Wrapf("slot %d appears twice in last-applied validators", validator.SlotId)
		}
		lastApplied[validator.SlotId] = struct{}{}
		return false, nil
	}); err != nil {
		return err
	}
	if len(lastApplied) != len(active) {
		return types.ErrInvalidGenesis.Wrapf(
			"last-applied validator count %d does not match active slot count %d", len(lastApplied), len(active))
	}
	for id := range active {
		if _, ok := lastApplied[id]; !ok {
			return types.ErrInvalidGenesis.Wrapf("active slot %d is missing from last-applied validators", id)
		}
	}
	// One update per ACTIVE slot and nothing else. A second delta for a validator
	// already in the initial set would be rejected by CometBFT as a duplicate
	// changeset entry, so catching it here names the cause instead.
	if len(updates) != len(active) {
		return types.ErrInvalidGenesis.Wrapf(
			"emitted %d validator updates for %d active slots", len(updates), len(active))
	}
	return nil
}

// validateGenesisEconomicAddresses applies address admission to every slot the
// genesis state would persist, at the level each field warrants: the operator
// address is a control identity and is only required to be a valid account
// address (§18), while the payout and settlement addresses are value
// destinations and take the full canonical economic rule (§25).
//
// The settlement address is required for PENDING slots as well as ACTIVE ones:
// §24 makes it mandatory from normal registration onward, not from activation.
//
// Params.Authority and Params.EmergencyAuthority are deliberately absent: they
// are control-plane identities and are module accounts by design, so the
// economic rule would reject the chain's own governance.
func (k Keeper) validateGenesisEconomicAddresses(genesis *types.GenesisState) error {
	for _, slot := range genesis.Slots {
		if slot == nil {
			continue
		}
		if _, err := k.economicAddresses.ParseAccountAddress(slot.OperatorAddress); err != nil {
			return types.ErrInvalidAddress.Wrapf("slot %d operator address: %v", slot.SlotId, err)
		}
		if _, err := k.economicAddresses.Validate(slot.PayoutAddress); err != nil {
			return types.ErrInvalidAddress.Wrapf("slot %d payout address: %v", slot.SlotId, err)
		}
		if _, err := k.economicAddresses.Validate(slot.SettlementAddress); err != nil {
			return types.ErrInvalidAddress.Wrapf("slot %d settlement address: %v", slot.SlotId, err)
		}
	}
	return nil
}

func (k Keeper) ExportGenesis(ctx context.Context) (*types.GenesisState, error) {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	genesis := &types.GenesisState{Params: &params}
	genesis.NextSlotId, _ = k.NextSlotID.Get(ctx)
	if err := k.Slots.Walk(ctx, nil, func(_ uint64, value types.CoreSlot) (bool, error) {
		valueCopy := value
		genesis.Slots = append(genesis.Slots, &valueCopy)
		return false, nil
	}); err != nil {
		return nil, err
	}
	if err := k.Rotations.Walk(ctx, nil, func(_ uint64, value types.PendingKeyRotation) (bool, error) {
		valueCopy := value
		genesis.PendingKeyRotations = append(genesis.PendingKeyRotations, &valueCopy)
		return false, nil
	}); err != nil {
		return nil, err
	}
	if err := k.Reserved.Walk(ctx, nil, func(_ string, value types.ReservedConsensusAddress) (bool, error) {
		valueCopy := value
		genesis.ReservedConsensusAddresses = append(genesis.ReservedConsensusAddresses, &valueCopy)
		return false, nil
	}); err != nil {
		return nil, err
	}
	if err := k.RewardWeights.Walk(ctx, nil, func(_ uint64, value types.OperatorRewardWeight) (bool, error) {
		valueCopy := value
		genesis.RewardWeights = append(genesis.RewardWeights, &valueCopy)
		return false, nil
	}); err != nil {
		return nil, err
	}
	if err := k.LastApplied.Walk(ctx, nil, func(_ string, value types.LastAppliedValidator) (bool, error) {
		valueCopy := value
		genesis.LastAppliedValidators = append(genesis.LastAppliedValidators, &valueCopy)
		return false, nil
	}); err != nil {
		return nil, err
	}
	// Policy history. Walk order follows the (slot_id, policy_version) key
	// encoding, so the exported slice is deterministic. That is a property of
	// this traversal, not a claim about canonical export ordering in general,
	// which remains unratified.
	if err := k.SelectionPolicies.Walk(ctx, nil, func(_ collections.Pair[uint64, uint64], value types.SelectionPolicyVersion) (bool, error) {
		valueCopy := value
		genesis.SelectionPolicies = append(genesis.SelectionPolicies, &valueCopy)
		return false, nil
	}); err != nil {
		return nil, err
	}
	return genesis, nil
}

func fmtHex(value []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(value)*2)
	for i, b := range value {
		out[i*2], out[i*2+1] = digits[b>>4], digits[b&0x0f]
	}
	return string(out)
}
