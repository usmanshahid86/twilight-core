package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"

	"github.com/twilight-project/twilight-core/x/rewards/types"
)

func (k Keeper) GetParams(ctx context.Context) (types.Params, error) {
	return k.Params.Get(ctx)
}

func (k Keeper) SetParams(ctx context.Context, params types.Params) error {
	if err := params.Validate(); err != nil {
		return err
	}
	if err := k.validateParamsTreasury("params", params); err != nil {
		return err
	}
	return k.Params.Set(ctx, params)
}

func (k Keeper) GetPendingParams(ctx context.Context) (types.Params, bool, error) {
	params, err := k.PendingParams.Get(ctx)
	if errors.Is(err, collections.ErrNotFound) {
		return types.Params{}, false, nil
	}
	return params, err == nil, err
}

func (k Keeper) SetPendingParams(ctx context.Context, params types.Params) error {
	current, err := k.GetParams(ctx)
	if err != nil {
		return err
	}
	if err := types.ValidateUpdate(current, params); err != nil {
		return err
	}
	if err := k.validateParamsTreasury("pending params", params); err != nil {
		return err
	}
	return k.PendingParams.Set(ctx, params)
}

func (k Keeper) ClearPendingParams(ctx context.Context) error {
	err := k.PendingParams.Remove(ctx)
	if errors.Is(err, collections.ErrNotFound) {
		return nil
	}
	return err
}

func BuildEpochConfigSnapshot(params types.Params) (types.EpochConfigSnapshot, error) {
	if err := params.Validate(); err != nil {
		return types.EpochConfigSnapshot{}, err
	}
	snapshot := types.DefaultEpochConfigSnapshot(params)
	if err := snapshot.Validate(); err != nil {
		return types.EpochConfigSnapshot{}, err
	}
	return snapshot, nil
}
