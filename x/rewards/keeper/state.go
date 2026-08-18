package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	sdkmath "cosmossdk.io/math"

	"github.com/twilight-project/twilight-core/internal/checked"
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

// GetOpenRewardEnabledBlocks returns the open epoch's reward-enabled block count.
//
// There is deliberately no default. Genesis writes zero explicitly, and the
// counter is rewritten whenever an epoch opens, so after initialization an absent
// or unreadable value is corruption. Defaulting it to zero would silently reset
// the block count that drives emission for the epoch being finalized — the chain
// would mint as though the epoch had accrued nothing.
func (k Keeper) GetOpenRewardEnabledBlocks(ctx context.Context) (uint64, error) {
	blocks, err := k.OpenRewardEnabledBlocks.Get(ctx)
	if err != nil {
		return 0, types.ErrInvalidState.Wrapf(
			"open reward-enabled block count could not be read: %v", err)
	}
	return blocks, nil
}

// SetOpenRewardEnabledBlocks writes the open epoch's reward-enabled block count.
func (k Keeper) SetOpenRewardEnabledBlocks(ctx context.Context, blocks uint64) error {
	return k.OpenRewardEnabledBlocks.Set(ctx, blocks)
}

func (k Keeper) GetCurrentEpochConfig(ctx context.Context) (types.EpochConfigSnapshot, error) {
	return k.CurrentEpochConfig.Get(ctx)
}

func (k Keeper) SetCurrentEpochConfig(ctx context.Context, cfg types.EpochConfigSnapshot) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := k.validateSnapshotTreasury("current epoch config", cfg); err != nil {
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

// IncrementActiveBlocks credits one slot with one reward-active block.
//
// Absence is the ordinary first credit of an epoch and starts the count at zero;
// only collections.ErrNotFound means absence, and any other read failure
// propagates rather than being treated as a missing row.
//
// The increment is checked. A consensus counter that feeds allocation must not be
// allowed to wrap: an unchecked +1 at the maximum would reset the slot's
// participation to zero and silently transfer its share of the pool to everyone
// else, which is a value transfer, not an overflow.
func (k Keeper) IncrementActiveBlocks(ctx context.Context, epoch, slotID uint64) error {
	blocks, err := k.GetActiveBlocks(ctx, epoch, slotID)
	if errors.Is(err, collections.ErrNotFound) {
		blocks = 0
	} else if err != nil {
		return err
	}
	next, err := checked.AddUint64(blocks, 1)
	if err != nil {
		return types.ErrInvalidState.Wrapf(
			"active-block count for slot %d in epoch %d overflows", slotID, epoch)
	}
	return k.SetActiveBlocks(ctx, epoch, slotID, next)
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
	exists, err := k.FinalizedEpochs.Has(ctx, epoch.EpochNumber)
	if err != nil {
		return err
	}
	if exists {
		return types.ErrInvalidState.Wrapf("finalized epoch %d is immutable", epoch.EpochNumber)
	}
	// A finalized epoch is permanent and carries economic addresses in both its
	// embedded configuration and its embedded reward records.
	if err := k.validateFinalizedEpochAddresses("finalized epoch", epoch); err != nil {
		return err
	}
	return k.FinalizedEpochs.Set(ctx, epoch.EpochNumber, epoch)
}
