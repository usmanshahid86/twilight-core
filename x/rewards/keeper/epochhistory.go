package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"

	appparams "github.com/twilight-project/twilight-core/app/params"
	"github.com/twilight-project/twilight-core/internal/checked"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// Canonical epoch geometry.
//
// EpochConfigVersion history is the sole authority for where an epoch starts and
// ends. Nothing derives a boundary from Params.epoch_length_blocks or from the
// EpochConfigSnapshot mirror; both survive only as deprecated compatibility data
// that genesis pins to this history.
//
// Boundaries are DERIVED, never stored per epoch:
//
//	EpochStartHeight(N) = V.effective_start_height
//	                    + (N - V.effective_epoch) * V.epoch_length_blocks
//	EpochEndHeight(N)   = EpochStartHeight(N+1) - 1
//
// where V is the greatest version whose effective_epoch <= N. Every step uses
// checked arithmetic: epoch and height are unsigned, so an unchecked subtraction
// or multiplication would not fail, it would silently name a different block.

// epochConfigVersionFor returns the version governing epoch N.
//
// Resolution is a single predecessor seek, not a walk: the map is keyed by
// effective epoch under a big-endian codec, so a descending iteration bounded
// above by N stops at the first — and therefore greatest — applicable entry.
//
// A stored row whose effective_epoch disagrees with the key it was found under is
// corruption rather than an answer, and is refused. Genuine absence (no version
// at or below N) is reported as ErrEpochConfigNotFound so callers can tell "this
// epoch predates the history" from "the history could not be read".
func (k Keeper) epochConfigVersionFor(ctx context.Context, epoch uint64) (types.EpochConfigVersion, error) {
	if epoch == 0 {
		return types.EpochConfigVersion{}, types.ErrInvalidState.Wrap("epoch numbers start at 1")
	}

	// Bounded above by the requested epoch, descending, so the first entry is the
	// greatest effective_epoch <= epoch.
	rng := new(collections.Range[uint64]).EndInclusive(epoch).Descending()

	iter, err := k.EpochConfigVersions.Iterate(ctx, rng)
	if err != nil {
		return types.EpochConfigVersion{}, types.ErrInvalidState.Wrapf(
			"epoch configuration history could not be read: %v", err)
	}
	defer iter.Close()

	if !iter.Valid() {
		return types.EpochConfigVersion{}, types.ErrEpochConfigNotFound.Wrapf(
			"no epoch configuration version is effective at or before epoch %d", epoch)
	}
	key, err := iter.Key()
	if err != nil {
		return types.EpochConfigVersion{}, types.ErrInvalidState.Wrapf(
			"epoch configuration history key could not be read: %v", err)
	}
	version, err := iter.Value()
	if err != nil {
		return types.EpochConfigVersion{}, types.ErrInvalidState.Wrapf(
			"epoch configuration version at effective epoch %d could not be read: %v", key, err)
	}
	if version.EffectiveEpoch != key {
		return types.EpochConfigVersion{}, types.ErrInvalidState.Wrapf(
			"epoch configuration version stored at effective epoch %d declares effective epoch %d",
			key, version.EffectiveEpoch)
	}
	if version.EpochLengthBlocks == 0 || version.EffectiveStartHeight == 0 || version.Version == 0 {
		return types.EpochConfigVersion{}, types.ErrInvalidState.Wrapf(
			"epoch configuration version %d at effective epoch %d is malformed",
			version.Version, version.EffectiveEpoch)
	}
	return version, nil
}

// EpochLengthForEpoch returns the canonical epoch length governing epoch N.
func (k Keeper) EpochLengthForEpoch(ctx context.Context, epoch uint64) (uint64, error) {
	version, err := k.epochConfigVersionFor(ctx, epoch)
	if err != nil {
		return 0, err
	}
	return version.EpochLengthBlocks, nil
}

// EpochStartHeight returns the first block height of epoch N.
//
// It resolves through immutable history only. A scheduled configuration that has
// not yet been consumed does not move the start of the epoch it becomes
// effective at — that epoch's start is fixed by the length of the epoch before
// it — so this is exact for every historical epoch and for the next one.
// Projecting further ahead must walk the schedule; see ProjectEpochStartHeight.
func (k Keeper) EpochStartHeight(ctx context.Context, epoch uint64) (uint64, error) {
	version, err := k.epochConfigVersionFor(ctx, epoch)
	if err != nil {
		return 0, err
	}
	return epochStartFrom(version, epoch)
}

// epochStartFrom applies the canonical start-height recurrence for one version.
func epochStartFrom(version types.EpochConfigVersion, epoch uint64) (uint64, error) {
	elapsed, err := checked.SubUint64(epoch, version.EffectiveEpoch)
	if err != nil {
		return 0, types.ErrInvalidState.Wrapf(
			"epoch %d precedes the version effective at epoch %d", epoch, version.EffectiveEpoch)
	}
	span, err := checked.MulUint64(elapsed, version.EpochLengthBlocks)
	if err != nil {
		return 0, types.ErrInvalidState.Wrapf(
			"epoch %d start height overflows: %v", epoch, err)
	}
	start, err := checked.AddUint64(version.EffectiveStartHeight, span)
	if err != nil {
		return 0, types.ErrInvalidState.Wrapf(
			"epoch %d start height overflows: %v", epoch, err)
	}
	return start, nil
}

// EpochEndHeight returns the inclusive final height of epoch N, derived as the
// block before the next epoch begins. It is never stored.
func (k Keeper) EpochEndHeight(ctx context.Context, epoch uint64) (uint64, error) {
	next, err := checked.AddUint64(epoch, 1)
	if err != nil {
		return 0, types.ErrInvalidState.Wrapf("epoch %d has no representable successor", epoch)
	}
	start, err := k.EpochStartHeight(ctx, next)
	if err != nil {
		return 0, err
	}
	end, err := checked.SubUint64(start, 1)
	if err != nil {
		return 0, types.ErrInvalidState.Wrapf("epoch %d end height underflows", epoch)
	}
	return end, nil
}

// ProjectEpochStartHeight resolves the start height of an arbitrary future epoch
// by walking the ordered schedule on top of immutable history.
//
// This exists because the simple predecessor formula is only exact up to the
// next unconsumed schedule entry: a scheduled length effective at S changes every
// boundary from S onward, and the history does not know about it yet. Callers on
// the block path never need this — BeginBlock asks only about the next epoch,
// whose start no schedule can move — so it is a query-side helper.
//
// maxSteps bounds the walk. Beyond it the answer is refused rather than
// approximated: a boundary outside the supported derivation horizon is a
// deterministic not-found, never an invented or clamped height.
func (k Keeper) ProjectEpochStartHeight(ctx context.Context, epoch, maxSteps uint64) (uint64, error) {
	version, err := k.epochConfigVersionFor(ctx, epoch)
	if err != nil {
		return 0, err
	}

	// Walk forward from the governing version, applying each scheduled length
	// change that falls strictly between it and the target epoch.
	cursor := version.EffectiveEpoch
	height := version.EffectiveStartHeight
	length := version.EpochLengthBlocks

	for steps := uint64(0); cursor < epoch; steps++ {
		if steps >= maxSteps {
			return 0, types.ErrEpochConfigNotFound.Wrapf(
				"epoch %d lies beyond the supported projection horizon of %d scheduled steps",
				epoch, maxSteps)
		}
		next, err := checked.AddUint64(cursor, 1)
		if err != nil {
			return 0, types.ErrInvalidState.Wrapf("epoch %d projection overflows", epoch)
		}
		height, err = checked.AddUint64(height, length)
		if err != nil {
			return 0, types.ErrInvalidState.Wrapf("epoch %d projection overflows", epoch)
		}
		scheduled, err := k.ScheduledEpochConfigs.Get(ctx, next)
		switch {
		case err == nil:
			if scheduled.EpochLengthBlocks == 0 || scheduled.EffectiveEpoch != next {
				return 0, types.ErrInvalidState.Wrapf(
					"scheduled epoch configuration at epoch %d is malformed", next)
			}
			length = scheduled.EpochLengthBlocks
		case errors.Is(err, collections.ErrNotFound):
			// No change at this boundary; the current length continues.
		default:
			return 0, types.ErrInvalidState.Wrapf(
				"scheduled epoch configuration at epoch %d could not be read: %v", next, err)
		}
		cursor = next
	}
	return height, nil
}

// latestEpochConfigVersion returns the newest history entry, which is also the
// highest version number: §82 requires versions and effective epochs to increase
// together, and genesis plus the single append path below preserve that.
func (k Keeper) latestEpochConfigVersion(ctx context.Context) (types.EpochConfigVersion, error) {
	iter, err := k.EpochConfigVersions.Iterate(ctx, new(collections.Range[uint64]).Descending())
	if err != nil {
		return types.EpochConfigVersion{}, types.ErrInvalidState.Wrapf(
			"epoch configuration history could not be read: %v", err)
	}
	defer iter.Close()
	if !iter.Valid() {
		return types.EpochConfigVersion{}, types.ErrEpochConfigNotFound.Wrap(
			"epoch configuration history is empty")
	}
	version, err := iter.Value()
	if err != nil {
		return types.EpochConfigVersion{}, types.ErrInvalidState.Wrapf(
			"latest epoch configuration version could not be read: %v", err)
	}
	return version, nil
}

// appendEpochConfigVersion writes one immutable history entry.
//
// Creating a version at an effective epoch that already has one would rewrite
// history, so it is refused rather than overwritten — the same write-once
// discipline the rest of the module applies to immutable records.
func (k Keeper) appendEpochConfigVersion(ctx context.Context, version types.EpochConfigVersion) error {
	if err := version.Validate(); err != nil {
		return err
	}
	// The other admission point for canonical geometry. Fresh genesis checks the
	// anchor; this checks every version created afterwards, which today means a
	// consumed schedule entry. There is no schedule writer in this change, so the
	// path is unreachable — that is exactly why the bound belongs here rather
	// than only at the writer that does not exist yet.
	if err := appparams.ValidateEpochLengthBlocks(version.EpochLengthBlocks); err != nil {
		return types.ErrInvalidState.Wrap(err.Error())
	}
	exists, err := k.EpochConfigVersions.Has(ctx, version.EffectiveEpoch)
	if err != nil {
		return types.ErrInvalidState.Wrapf(
			"epoch configuration history could not be read at effective epoch %d: %v",
			version.EffectiveEpoch, err)
	}
	if exists {
		return types.ErrInvalidState.Wrapf(
			"an epoch configuration version is already effective at epoch %d", version.EffectiveEpoch)
	}
	return k.EpochConfigVersions.Set(ctx, version.EffectiveEpoch, version)
}

// consumeScheduledEpochConfig applies any configuration becoming effective at the
// epoch being opened, turning it into an immutable history version anchored at
// the current block height (§11).
//
// Returns whether a schedule entry was consumed.
func (k Keeper) consumeScheduledEpochConfig(ctx context.Context, epoch, height uint64) (bool, error) {
	scheduled, err := k.ScheduledEpochConfigs.Get(ctx, epoch)
	if errors.Is(err, collections.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, types.ErrInvalidState.Wrapf(
			"scheduled epoch configuration at epoch %d could not be read: %v", epoch, err)
	}
	if scheduled.EffectiveEpoch != epoch {
		return false, types.ErrInvalidState.Wrapf(
			"scheduled epoch configuration stored at epoch %d declares effective epoch %d",
			epoch, scheduled.EffectiveEpoch)
	}

	latest, err := k.latestEpochConfigVersion(ctx)
	if err != nil {
		return false, err
	}
	nextVersion, err := checked.AddUint64(latest.Version, 1)
	if err != nil {
		return false, types.ErrInvalidState.Wrap("epoch configuration version space is exhausted")
	}
	if err := k.appendEpochConfigVersion(ctx, types.EpochConfigVersion{
		Version:              nextVersion,
		EffectiveEpoch:       epoch,
		EffectiveStartHeight: height,
		EpochLengthBlocks:    scheduled.EpochLengthBlocks,
	}); err != nil {
		return false, err
	}
	if err := k.ScheduledEpochConfigs.Remove(ctx, epoch); err != nil {
		return false, types.ErrInvalidState.Wrapf(
			"scheduled epoch configuration at epoch %d could not be consumed: %v", epoch, err)
	}
	return true, nil
}
