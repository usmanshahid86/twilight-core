package keeper

import (
	"context"

	"github.com/twilight-project/twilight-core/internal/checked"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// The single canonical rewards-pause state (§16).
//
// V2.2 replaces independent accrual/settlement/payout pause switches with one
// state, and makes its transitions H+1: a pause or resume accepted in block H
// takes effect at the beginning of H+1, before any reward sampling. That delay is
// what makes a block's reward treatment a property of the block rather than of
// the order in which its transactions happened to execute.
//
// Two things follow and are load-bearing elsewhere:
//
//   - the value visible to handlers executing in H is the value committed at the
//     end of H-1, so a pause scheduled during H must not appear effective to
//     anything running in H;
//   - pausing stops accrual and release, but NOT epoch time. Epoch numbering
//     advances and epochs still finalize; a fully paused epoch simply counts zero
//     reward-enabled blocks and therefore emits nothing (§30).

// GetPauseState returns the canonical pause state.
//
// There is no default. Genesis writes the state explicitly, so after
// initialization an absent or unreadable record is corruption; synthesizing
// "not paused" would resume emission and monetary release on the strength of a
// failed read.
func (k Keeper) GetPauseState(ctx context.Context) (types.RewardsPauseState, error) {
	state, err := k.PauseState.Get(ctx)
	if err != nil {
		return types.RewardsPauseState{}, types.ErrInvalidState.Wrapf(
			"rewards pause state could not be read: %v", err)
	}
	if err := state.Validate(); err != nil {
		return types.RewardsPauseState{}, err
	}
	return state, nil
}

// SetPauseState writes the canonical pause state after validating its shape.
func (k Keeper) SetPauseState(ctx context.Context, state types.RewardsPauseState) error {
	if err := state.Validate(); err != nil {
		return err
	}
	return k.PauseState.Set(ctx, state)
}

// RewardsPaused reports whether reward accrual is paused for the current block,
// using the already-effective value.
func (k Keeper) RewardsPaused(ctx context.Context) (bool, error) {
	state, err := k.GetPauseState(ctx)
	if err != nil {
		return false, err
	}
	return state.CurrentPaused, nil
}

// SettlementReleaseEnabled reports whether monetary release is permitted for the
// current block.
//
// It reads the already-applied value and never the pending one. A pause accepted
// earlier in this same block is scheduled for H+1 and must not be visible here:
// treating it as effective would make a release depend on whether it executed
// before or after the pause transaction in the same block, which is exactly the
// ordering dependence the H+1 rule removes.
func (k Keeper) SettlementReleaseEnabled(ctx context.Context) (bool, error) {
	paused, err := k.RewardsPaused(ctx)
	if err != nil {
		return false, err
	}
	return !paused, nil
}

// SchedulePauseTransition records a pause or resume accepted in block H for H+1.
//
// Multiple accepted changes in one block resolve by transaction order: each
// overwrites the pending value, so the last accepted one is what H+1 applies.
// Scheduling a value equal to the current one is permitted and is not a no-op
// rejection — it is how an operator cancels a transition already pending in the
// same block.
func (k Keeper) SchedulePauseTransition(ctx context.Context, height int64, paused bool) error {
	current, err := k.GetPauseState(ctx)
	if err != nil {
		return err
	}
	unsignedHeight, err := checked.Uint64FromInt64(height)
	if err != nil {
		return types.ErrInvalidState.Wrapf("block height %d is not representable: %v", height, err)
	}
	effective, err := checked.AddUint64(unsignedHeight, 1)
	if err != nil {
		return types.ErrInvalidState.Wrap("pause transition height overflows")
	}
	current.HasPending = true
	current.PendingValue = paused
	current.PendingEffectiveHeight = effective
	return k.SetPauseState(ctx, current)
}

// applyPendingPauseTransition promotes a pending transition whose effective
// height is the current block, and clears the pending fields.
//
// It is called at the beginning of the block, before any sampling, so the whole
// block sees one value. A pending height in the past is state divergence rather
// than a late transition: heights are consumed exactly once, so a transition that
// was due earlier and is still pending means the block that owed it did not run
// this path. It fails closed rather than applying the transition late, which
// would change reward treatment for blocks that already committed.
func (k Keeper) applyPendingPauseTransition(ctx context.Context, height uint64) error {
	state, err := k.GetPauseState(ctx)
	if err != nil {
		return err
	}
	if !state.HasPending {
		return nil
	}
	if state.PendingEffectiveHeight > height {
		return nil
	}
	if state.PendingEffectiveHeight < height {
		return types.ErrInvalidState.Wrapf(
			"a rewards pause transition was due at height %d but is still pending at height %d",
			state.PendingEffectiveHeight, height)
	}
	return k.SetPauseState(ctx, types.RewardsPauseState{CurrentPaused: state.PendingValue})
}
