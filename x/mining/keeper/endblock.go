package keeper

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/internal/checked"
	"github.com/twilight-project/twilight-core/x/mining/types"
)

// EndBlock is the mining consensus transition, running after x/rewards.
//
// The order below is normative, and each step depends on the one before it:
//
//  1. tick the settlement clock if this block's beginning-of-block pause state
//     permitted release;
//  2. materialize the complete settlement set for any reward epoch that closed in
//     this block, anchoring it to the clock value from step 1;
//  3. if an epoch closed, promote whatever was scheduled for the epoch that
//     follows it.
//
// Step 2 must see step 1's value: the anchor records the clock the closing block
// produced, and every deadline derived from it counts forward from there. Step 3
// must come after step 2 so a configuration can never become effective beside a
// materialization that failed.
//
// The whole transition runs in one cache and commits only on full success. Partial
// application followed by a retry in a later block is forbidden — an epoch
// materialized under a different clock would carry deadlines that no longer follow
// from the block that closed it.
//
// **No bank operation occurs here, and none can.** x/mining holds no bank keeper,
// so the architecture's rule that consensus performs no automatic payout or
// finalization is enforced by an absent dependency rather than by a check.
func (k Keeper) EndBlock(ctx context.Context) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	if err := k.endBlock(cacheCtx); err != nil {
		return err
	}
	write()
	return nil
}

func (k Keeper) endBlock(ctx context.Context) error {
	if err := k.advanceSettlementClock(ctx); err != nil {
		return err
	}
	closed, err := k.materializeFinalizedEpoch(ctx)
	if err != nil {
		return err
	}
	if closed == 0 {
		// No epoch boundary in this block, so nothing can become effective at the
		// next one either. Promotion is strictly an epoch-boundary event.
		return nil
	}
	effectiveEpoch, err := checked.AddUint64(closed, 1)
	if err != nil {
		return types.ErrInvalidState.Wrap("the epoch space is exhausted")
	}
	if err := k.promoteScheduledDistributionMode(ctx, effectiveEpoch); err != nil {
		return err
	}
	if err := k.promoteScheduledSelectionParams(ctx, effectiveEpoch); err != nil {
		return err
	}
	return k.promoteScheduledSettlementParams(ctx, effectiveEpoch)
}
