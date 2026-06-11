package keeper

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

func (k Keeper) EndBlock(ctx context.Context) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	cacheGoCtx := sdk.WrapSDKContext(cacheCtx)

	state, err := k.GetState(cacheGoCtx)
	if err != nil {
		return err
	}
	cfg, err := k.GetCurrentEpochConfig(cacheGoCtx)
	if err != nil {
		return err
	}
	params, err := k.GetParams(cacheGoCtx)
	if err != nil {
		return err
	}
	ready, err := ShouldFinalizeAtHeight(uint64(cacheCtx.BlockHeight()), state, cfg, params.EpochSettlementEnabled)
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
