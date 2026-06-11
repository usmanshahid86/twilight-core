package keeper

import (
	"context"

	sdkmath "cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"

	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
)

type SlotRewardSnapshot struct {
	SlotID          uint64
	OperatorAddress sdk.AccAddress
	PayoutAddress   sdk.AccAddress
	Status          coreslottypes.SlotStatus
	RewardWeight    sdkmath.LegacyDec
}

func (k Keeper) GetActiveSlotSnapshots(ctx context.Context) ([]SlotRewardSnapshot, error) {
	slots, err := k.coreSlotKeeper.GetActiveSlots(ctx)
	if err != nil {
		return nil, err
	}
	snapshots := make([]SlotRewardSnapshot, 0, len(slots))
	for _, slot := range slots {
		if slot.Status != coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE {
			continue
		}
		snapshot, err := k.slotRewardSnapshot(ctx, slot)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

func (k Keeper) GetSlotRewardSnapshot(ctx context.Context, slotID uint64) (SlotRewardSnapshot, error) {
	slot, err := k.coreSlotKeeper.GetSlot(ctx, slotID)
	if err != nil {
		return SlotRewardSnapshot{}, err
	}
	return k.slotRewardSnapshot(ctx, slot)
}

func (k Keeper) slotRewardSnapshot(ctx context.Context, slot coreslottypes.CoreSlot) (SlotRewardSnapshot, error) {
	operator, err := sdk.AccAddressFromBech32(slot.OperatorAddress)
	if err != nil {
		return SlotRewardSnapshot{}, err
	}
	payout, err := sdk.AccAddressFromBech32(slot.PayoutAddress)
	if err != nil {
		return SlotRewardSnapshot{}, err
	}
	weight, err := k.coreSlotKeeper.GetRewardWeight(ctx, slot.SlotId)
	if err != nil {
		return SlotRewardSnapshot{}, err
	}
	finalWeight, err := sdkmath.LegacyNewDecFromStr(weight.FinalWeight)
	if err != nil {
		return SlotRewardSnapshot{}, err
	}
	if finalWeight.IsNegative() {
		return SlotRewardSnapshot{}, coreslottypes.ErrInvalidParams.Wrap("reward weight cannot be negative")
	}
	return SlotRewardSnapshot{
		SlotID:          slot.SlotId,
		OperatorAddress: operator,
		PayoutAddress:   payout,
		Status:          slot.Status,
		RewardWeight:    finalWeight,
	}, nil
}
