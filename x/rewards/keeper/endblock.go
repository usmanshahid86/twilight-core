package keeper

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/internal/checked"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// EndBlock finalizes the open epoch when this block is its canonical last, then
// promotes any reward configuration scheduled for the epoch that follows.
//
// Finalization is unconditional at that boundary — see ShouldFinalizeAtHeight for
// why it must not be gated on the pause state. It does NOT advance the epoch
// counter; the next epoch becomes current at its own first BeginBlock.
//
// # Why promotion lives here and in this order
//
// §94 places reward-configuration promotion in the closing epoch's EndBlock, and
// only after that epoch's monetary transition has succeeded. Statement order
// gives the "only after" half: a finalization error returns before promotion is
// reached, so a configuration can never become effective beside a mint that
// failed.
//
// Sharing ONE cache gives the converse half. If promotion ran in a cache of its
// own, a promotion failure would leave the finalization already committed — an
// epoch closed and paid beside a schedule that was supposed to be consumed in the
// same block and now never will be. Both halves of the transition commit together
// or the block fails.
//
// This is deliberately not the epoch-geometry mechanism. That schedule is
// consumed at the first BeginBlock of the epoch it becomes effective at, because
// geometry must be known before the block can be attributed to an epoch. Reward
// configuration binds two epochs ahead of any target it affects, so nothing needs
// it early, and consuming it early would make it effective one boundary sooner
// than the architecture allows.
func (k Keeper) EndBlock(ctx context.Context) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	cacheGoCtx := cacheCtx

	height, err := checked.Uint64FromInt64(cacheCtx.BlockHeight())
	if err != nil {
		return types.ErrInvalidState.Wrapf("block height is not representable: %v", err)
	}
	ready, err := k.ShouldFinalizeAtHeight(cacheGoCtx, height)
	if err != nil {
		return err
	}
	if !ready {
		return nil
	}
	// Read the closing epoch before finalization runs. finalizeEpoch does not
	// advance the counter, but reading it first means the promotion target is
	// derived from the epoch that was actually closed rather than from whatever
	// state the finalizer left behind.
	state, err := k.GetState(cacheGoCtx)
	if err != nil {
		return err
	}
	if err := k.finalizeEpoch(cacheGoCtx); err != nil {
		return err
	}
	if err := k.promoteScheduledRewardConfig(cacheGoCtx, state.CurrentEpoch); err != nil {
		return err
	}
	write()
	return nil
}
