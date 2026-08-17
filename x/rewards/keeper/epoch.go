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
//
// The comparison is EXACT, not "at or past". A boundary is a single block, and it
// is consumed exactly once:
//
//	height <  canonical end -> not ready
//	height == canonical end -> finalize
//	height >  canonical end -> ErrInvalidState
//
// The third case is not a late finalization to be caught up. Finalizing under a
// later block's clock would mint for an epoch whose closing block already
// committed without it, and would do so using whatever counters the intervening
// blocks left behind — a monetary transition executed against the wrong epoch.
// Because the boundary can only be passed if a block that owed the transition did
// not run it, arriving here at all means state has already diverged, so the block
// halts instead of transacting on it.
func (k Keeper) ShouldFinalizeAtHeight(ctx context.Context, height uint64) (bool, error) {
	state, err := k.GetState(ctx)
	if err != nil {
		return false, err
	}
	if err := k.verifyCurrentEpochAnchor(ctx, state); err != nil {
		return false, err
	}
	endHeight, err := k.EpochEndHeight(ctx, state.CurrentEpoch)
	if err != nil {
		return false, err
	}
	switch {
	case height < endHeight:
		return false, nil
	case height == endHeight:
		return true, nil
	default:
		return false, types.ErrInvalidState.Wrapf(
			"epoch %d was due to finalize at height %d but the current block is %d",
			state.CurrentEpoch, endHeight, height)
	}
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
