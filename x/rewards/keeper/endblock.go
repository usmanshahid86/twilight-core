package keeper

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/internal/checked"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// EndBlock finalizes the open epoch when this block is its canonical last.
//
// Finalization is unconditional at that boundary — see ShouldFinalizeAtHeight for
// why it must not be gated on the pause state. It does NOT advance the epoch
// counter; the next epoch becomes current at its own first BeginBlock.
func (k Keeper) EndBlock(ctx context.Context) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	cacheGoCtx := sdk.WrapSDKContext(cacheCtx)

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
	if err := k.finalizeEpoch(cacheGoCtx); err != nil {
		return err
	}
	write()
	return nil
}
