package keeper

import (
	"context"
	"sort"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/internal/checked"
	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// BeginBlock is the canonical rewards epoch transition (§95).
//
// The whole transition runs in a cache and commits only on full success. That
// matters more here than in most block paths because the steps are not
// independent: opening an epoch, resetting its counter, applying a pause and
// crediting participation are one accounting fact about the block. Committing a
// prefix of them would leave an epoch open with a counter belonging to its
// predecessor, or credit participation under a pause that had not been applied.
func (k Keeper) BeginBlock(ctx context.Context) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	if err := k.beginBlock(cacheCtx); err != nil {
		return err
	}
	write()
	return nil
}

func (k Keeper) beginBlock(ctx context.Context) error {
	height, err := checked.Uint64FromInt64(sdk.UnwrapSDKContext(ctx).BlockHeight())
	if err != nil {
		return types.ErrInvalidState.Wrapf("block height is not representable: %v", err)
	}

	// 1 & 2. Open a new epoch if this block starts one, consuming any scheduled
	// configuration and resetting the open counter.
	if err := k.advanceEpochIfDue(ctx, height); err != nil {
		return err
	}

	// 3. Apply a pause transition due at this height, before anything samples.
	if err := k.applyPendingPauseTransition(ctx, height); err != nil {
		return err
	}

	// 4. Sample the beginning-of-block state.
	state, err := k.GetState(ctx)
	if err != nil {
		return err
	}
	paused, err := k.RewardsPaused(ctx)
	if err != nil {
		return err
	}
	slots, err := k.coreSlotKeeper.GetActiveSlots(ctx)
	if err != nil {
		return err
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i].SlotId < slots[j].SlotId })

	// The external snapshot is validated in full BEFORE any mutation depending on
	// it. Validating inside the crediting loop would leave earlier slots credited
	// when a later one is rejected; the cache would discard that, but only because
	// the error propagates — the ordering here does not depend on that rescue.
	for _, slot := range slots {
		if slot.SlotId == 0 {
			return types.ErrInvalidState.Wrap("CoreSlot active-set contract returned zero slot ID")
		}
		if slot.Status != coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE {
			return types.ErrInvalidState.Wrapf(
				"CoreSlot active-set contract returned non-active slot %d with status %s",
				slot.SlotId,
				slot.Status.String(),
			)
		}
	}

	// 5. Credit the block only when reward accrual is enabled. A paused block
	// advances neither counter: §16 makes paused blocks create zero emission
	// progression and zero active reward credit, and §29 says they are never
	// backfilled.
	if paused {
		return nil
	}

	enabled, err := k.GetOpenRewardEnabledBlocks(ctx)
	if err != nil {
		return err
	}
	next, err := checked.AddUint64(enabled, 1)
	if err != nil {
		return types.ErrInvalidState.Wrap("reward-enabled block count overflows")
	}
	if err := k.SetOpenRewardEnabledBlocks(ctx, next); err != nil {
		return err
	}

	for _, slot := range slots {
		if err := k.IncrementActiveBlocks(ctx, state.CurrentEpoch, slot.SlotId); err != nil {
			return err
		}
	}
	return nil
}

// advanceEpochIfDue opens the next epoch when this block is its first.
//
// The epoch counter advances HERE and nowhere else. It used to advance inside
// epoch finalization, which is a different block and — more importantly — a
// different moment in the block: an EndBlock advance would make the scheduled
// configuration for the new epoch unreachable, because the schedule is consumed
// at the first BeginBlock of the epoch it becomes effective at. Every scheduled
// epoch-length change would be silently dropped.
func (k Keeper) advanceEpochIfDue(ctx context.Context, height uint64) error {
	state, err := k.GetState(ctx)
	if err != nil {
		return err
	}
	nextEpoch, err := checked.AddUint64(state.CurrentEpoch, 1)
	if err != nil {
		return types.ErrInvalidState.Wrap("epoch numbering is exhausted")
	}

	// The next epoch's start is fixed by the CURRENT epoch's length: a
	// configuration becoming effective at nextEpoch changes the epochs after it,
	// never its own start. So this resolves through immutable history alone and
	// cannot depend on a schedule entry it has not consumed yet.
	nextStart, err := k.EpochStartHeight(ctx, nextEpoch)
	if err != nil {
		return err
	}
	if height < nextStart {
		return nil
	}
	if height > nextStart {
		// Boundaries are consumed exactly once. Passing one without opening the
		// epoch means a block that owed this transition did not run it, and the
		// participation credited since then belongs to an epoch that never opened.
		return types.ErrInvalidState.Wrapf(
			"epoch %d was due to open at height %d but the current block is %d",
			nextEpoch, nextStart, height)
	}

	if _, err := k.consumeScheduledEpochConfig(ctx, nextEpoch, height); err != nil {
		return err
	}

	state.CurrentEpoch = nextEpoch
	state.CurrentEpochStartHeight = nextStart
	if err := k.SetState(ctx, state); err != nil {
		return err
	}
	// Reset before anything samples or credits: the counter is per open epoch,
	// and carrying the predecessor's total forward would inflate the first
	// epoch's emission by the whole previous epoch.
	return k.SetOpenRewardEnabledBlocks(ctx, 0)
}
