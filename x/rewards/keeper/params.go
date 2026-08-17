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

// buildEpochConfigSnapshot rebuilds the epoch configuration snapshot for the
// non-geometry economics the finalizer still reads, and repopulates its
// deprecated epoch-length mirror from canonical history.
//
// The mirror is taken from EpochConfigVersion rather than from Params on
// purpose. Params.epoch_length_blocks is frozen genesis/compatibility data that
// no runtime path may change, so copying it forward would republish a value that
// is only correct because nothing is allowed to move it. Reading the authority
// instead means the mirror cannot drift even if that ever changes.
//
// It remains a mirror: nothing derives a boundary from it.
func (k Keeper) buildEpochConfigSnapshot(ctx context.Context, params types.Params) (types.EpochConfigSnapshot, error) {
	snapshot, err := BuildEpochConfigSnapshot(params)
	if err != nil {
		return types.EpochConfigSnapshot{}, err
	}
	state, err := k.GetState(ctx)
	if err != nil {
		return types.EpochConfigSnapshot{}, err
	}
	length, err := k.EpochLengthForEpoch(ctx, state.CurrentEpoch)
	if err != nil {
		return types.EpochConfigSnapshot{}, err
	}
	snapshot.EpochLengthBlocks = length
	if err := snapshot.Validate(); err != nil {
		return types.EpochConfigSnapshot{}, err
	}
	return snapshot, nil
}
