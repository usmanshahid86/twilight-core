package keeper

import (
	"context"

	abci "github.com/cometbft/cometbft/abci/types"

	"github.com/nyks/nyks-core/x/coreslot/types"
)

func (k Keeper) InitGenesis(ctx context.Context, genesis *types.GenesisState) ([]abci.ValidatorUpdate, error) {
	if err := genesis.Validate(); err != nil {
		return nil, err
	}
	if err := k.Params.Set(ctx, *genesis.Params); err != nil {
		return nil, err
	}
	nextID := genesis.NextSlotId
	if nextID == 0 {
		nextID = 1
	}
	for _, slot := range genesis.Slots {
		if slot.SlotId >= nextID {
			nextID = slot.SlotId + 1
		}
		key, _, err := consensusKey(slot.ConsensusPubkey)
		if err != nil {
			return nil, err
		}
		if err := k.Slots.Set(ctx, slot.SlotId, *slot); err != nil {
			return nil, err
		}
		if slot.Status != types.SlotStatus_SLOT_STATUS_REMOVED {
			if err := k.ByOperator.Set(ctx, slot.OperatorAddress, slot.SlotId); err != nil {
				return nil, err
			}
			if err := k.ByConsensus.Set(ctx, key, slot.SlotId); err != nil {
				return nil, err
			}
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
	if err := k.NextSlotID.Set(ctx, nextID); err != nil {
		return nil, err
	}
	return k.diffAndPersist(ctx)
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
