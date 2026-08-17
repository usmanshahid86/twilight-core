package keeper

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// EndBlock is the mining consensus transition, running after x/rewards.
//
// It will own three things: advancing the settlement clock on blocks whose
// beginning-of-block pause state permits release, materializing the complete
// settlement set for any reward epoch finalized in this block, and promoting a
// scheduled parameter version at an epoch boundary. All three arrive with the
// settlement gate; this gate stands the module up and wires the hook.
//
// Two properties hold now and must keep holding. The whole transition runs in one
// cache and commits only on full success, so a partial materialization can never
// be left behind for a later block to complete. And nothing here moves value:
// x/mining holds no bank keeper, so the architecture's "no automatic payout or
// finalization in EndBlock" is enforced by the absence of a dependency rather
// than by remembering not to call one.
func (k Keeper) EndBlock(ctx context.Context) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	if err := k.endBlock(cacheCtx); err != nil {
		return err
	}
	write()
	return nil
}

func (k Keeper) endBlock(_ context.Context) error {
	return nil
}
