package keeper

import (
	"context"

	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// ShouldFinalizeAtHeight reports whether the open epoch has reached its
// canonical end.
//
// The end height is derived from EpochConfigVersion history, never from the
// deprecated Params or snapshot mirrors.
//
// There is deliberately no pause parameter. Epoch finalization is UNCONDITIONAL
// at the canonical boundary: §16 stops accrual and release while paused but
// leaves epoch numbering running, and §30 describes a fully paused epoch as one
// that closes with B=0 and therefore emits nothing. Gating finalization on the
// pause state would be worse than merely late — because the epoch counter now
// advances at BeginBlock, the boundary would pass, the next epoch would open, and
// the skipped epoch could never be finalized under a later block's clock (§33).
// It would be a permanent hole in the finalized-epoch sequence.
func (k Keeper) ShouldFinalizeAtHeight(ctx context.Context, height uint64) (bool, error) {
	state, err := k.GetState(ctx)
	if err != nil {
		return false, err
	}
	endHeight, err := k.EpochEndHeight(ctx, state.CurrentEpoch)
	if err != nil {
		return false, err
	}
	return height >= endHeight, nil
}

// ConfiguredEpochEndHeight derives an epoch end from a stored snapshot.
//
// Retained only for the deprecated snapshot mirror's own validation. It is NOT
// an epoch-geometry authority: nothing on the block path may resolve a boundary
// through it. Use Keeper.EpochEndHeight.
//
// Deprecated: superseded by canonical EpochConfigVersion history.
func ConfiguredEpochEndHeight(state types.RewardsState, cfg types.EpochConfigSnapshot) (uint64, error) {
	if state.CurrentEpoch == 0 || state.CurrentEpochStartHeight == 0 {
		return 0, types.ErrInvalidState.Wrap("current epoch and start height must be positive")
	}
	if cfg.EpochLengthBlocks == 0 {
		return 0, types.ErrInvalidState.Wrap("current epoch config length must be positive")
	}

	delta := cfg.EpochLengthBlocks - 1
	if state.CurrentEpochStartHeight > ^uint64(0)-delta {
		return 0, types.ErrInvalidState.Wrap("configured epoch end height overflows uint64")
	}

	return state.CurrentEpochStartHeight + delta, nil
}
