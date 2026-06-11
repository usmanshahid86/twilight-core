package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	sdkmath "cosmossdk.io/math"

	"github.com/twilight-project/twilight-core/x/rewards/types"
)

func (k Keeper) GetState(ctx context.Context) (types.RewardsState, error) {
	return k.State.Get(ctx)
}

func (k Keeper) SetState(ctx context.Context, state types.RewardsState) error {
	if state.CurrentEpoch == 0 || state.CurrentEpochStartHeight == 0 {
		return types.ErrInvalidState.Wrap("current epoch and start height must be positive")
	}
	cumulative, err := types.ParseAmountString("cumulative emitted", state.CumulativeEmitted)
	if err != nil {
		return types.ErrInvalidState.Wrap(err.Error())
	}
	if _, err := types.ParseAmountString("carry forward remainder", state.CarryForwardRemainder); err != nil {
		return types.ErrInvalidState.Wrap(err.Error())
	}
	params, err := k.GetParams(ctx)
	if err != nil {
		return err
	}
	maxSupply, _ := sdkmath.NewIntFromString(params.MaxSupply)
	if cumulative.GT(maxSupply) {
		return types.ErrInvalidState.Wrap("cumulative emitted exceeds max supply")
	}
	return k.State.Set(ctx, state)
}

func (k Keeper) GetCurrentEpochConfig(ctx context.Context) (types.EpochConfigSnapshot, error) {
	return k.CurrentEpochConfig.Get(ctx)
}

func (k Keeper) SetCurrentEpochConfig(ctx context.Context, cfg types.EpochConfigSnapshot) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	return k.CurrentEpochConfig.Set(ctx, cfg)
}

func (k Keeper) GetActiveBlocks(ctx context.Context, epoch, slotID uint64) (uint64, error) {
	return k.ActiveBlocks.Get(ctx, collections.Join(epoch, slotID))
}

func (k Keeper) SetActiveBlocks(ctx context.Context, epoch, slotID, blocks uint64) error {
	if epoch == 0 || slotID == 0 {
		return types.ErrInvalidState.Wrap("active-block key requires nonzero epoch and slot")
	}
	return k.ActiveBlocks.Set(ctx, collections.Join(epoch, slotID), blocks)
}

func (k Keeper) IncrementActiveBlocks(ctx context.Context, epoch, slotID uint64) error {
	blocks, err := k.GetActiveBlocks(ctx, epoch, slotID)
	if errors.Is(err, collections.ErrNotFound) {
		blocks = 0
	} else if err != nil {
		return err
	}
	return k.SetActiveBlocks(ctx, epoch, slotID, blocks+1)
}

func (k Keeper) DeleteActiveBlocksForEpoch(ctx context.Context, epoch uint64) error {
	keys := make([]collections.Pair[uint64, uint64], 0)
	err := k.ActiveBlocks.Walk(ctx, collections.NewPrefixedPairRange[uint64, uint64](epoch), func(key collections.Pair[uint64, uint64], _ uint64) (bool, error) {
		keys = append(keys, key)
		return false, nil
	})
	if err != nil {
		return err
	}
	for _, key := range keys {
		if err := k.ActiveBlocks.Remove(ctx, key); err != nil {
			return err
		}
	}
	return nil
}

func (k Keeper) IterateActiveBlocksForEpoch(ctx context.Context, epoch uint64) ([]types.SlotActiveBlocks, error) {
	rows := make([]types.SlotActiveBlocks, 0)
	err := k.ActiveBlocks.Walk(ctx, collections.NewPrefixedPairRange[uint64, uint64](epoch), func(key collections.Pair[uint64, uint64], blocks uint64) (bool, error) {
		rows = append(rows, types.SlotActiveBlocks{SlotId: key.K2(), BlocksActive: blocks})
		return false, nil
	})
	return rows, err
}

func (k Keeper) GetFinalizedEpoch(ctx context.Context, epoch uint64) (types.EpochReward, bool, error) {
	value, err := k.FinalizedEpochs.Get(ctx, epoch)
	if errors.Is(err, collections.ErrNotFound) {
		return types.EpochReward{}, false, nil
	}
	return value, err == nil, err
}

func (k Keeper) SetFinalizedEpoch(ctx context.Context, epoch types.EpochReward) error {
	if epoch.EpochNumber == 0 {
		return types.ErrInvalidState.Wrap("finalized epoch number must be nonzero")
	}
	return k.FinalizedEpochs.Set(ctx, epoch.EpochNumber, epoch)
}

func (k Keeper) GetClaimRecord(ctx context.Context, slotID, epoch uint64) (types.EligibleSlotReward, bool, error) {
	value, err := k.ClaimRecords.Get(ctx, collections.Join(slotID, epoch))
	if errors.Is(err, collections.ErrNotFound) {
		return types.EligibleSlotReward{}, false, nil
	}
	return value, err == nil, err
}

func (k Keeper) SetClaimRecord(ctx context.Context, reward types.EligibleSlotReward) error {
	if reward.SlotId == 0 || reward.EpochNumber == 0 {
		return types.ErrInvalidState.Wrap("claim record requires nonzero slot and epoch")
	}
	return k.ClaimRecords.Set(ctx, collections.Join(reward.SlotId, reward.EpochNumber), reward)
}

func (k Keeper) IterateClaimRecordsForSlot(ctx context.Context, slotID, startEpoch, endEpoch uint64) ([]types.EligibleSlotReward, error) {
	if slotID == 0 || startEpoch == 0 || endEpoch < startEpoch {
		return nil, types.ErrInvalidState.Wrap("invalid claim record range")
	}
	rows := make([]types.EligibleSlotReward, 0)
	rng := collections.NewPrefixedPairRange[uint64, uint64](slotID).StartInclusive(startEpoch).EndInclusive(endEpoch)
	err := k.ClaimRecords.Walk(ctx, rng, func(_ collections.Pair[uint64, uint64], reward types.EligibleSlotReward) (bool, error) {
		rows = append(rows, reward)
		return false, nil
	})
	return rows, err
}
