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

// How much of the history is validated, and why not more.
//
// A governing version is a monetary input: it decides which blocks belong to the
// epoch whose emission is about to be minted. So it is not enough that it decoded
// — it has to be checked. But checking the WHOLE history on every read would make
// per-block validation work grow with chain age, which is its own liveness
// failure, and would eventually make an old chain unable to produce a block.
//
// The rule applied here is bounded and local: validate the record actually being
// relied upon, plus the single adjacent history edge it is derived across. Every
// record was itself validated against its own predecessor at the moment it was
// appended, so a history built only through the append path is inductively
// continuous; the edge check is what detects a row that changed underneath that
// induction. Both checks are O(log versions) seeks and constant in chain age.
//
// validateEpochConfigRecord checks one history row against the key it was stored
// under and against the canonical shape every version must have.
func validateEpochConfigRecord(key uint64, version types.EpochConfigVersion) error {
	if version.EffectiveEpoch != key {
		return types.ErrInvalidState.Wrapf(
			"epoch configuration version stored at effective epoch %d declares effective epoch %d",
			key, version.EffectiveEpoch)
	}
	if version.Version == 0 || version.EffectiveEpoch == 0 || version.EffectiveStartHeight == 0 {
		return types.ErrInvalidState.Wrapf(
			"epoch configuration version %d at effective epoch %d is malformed: "+
				"version, effective epoch and effective start height must all be positive",
			version.Version, version.EffectiveEpoch)
	}
	// The ratified bound is re-checked on read, not only on write. A stored length
	// outside it cannot have been admitted by any current code path, so finding one
	// means the row is not the row that was written.
	if err := appparams.ValidateEpochLengthBlocks(version.EpochLengthBlocks); err != nil {
		return types.ErrInvalidState.Wrapf(
			"epoch configuration version %d at effective epoch %d is inadmissible: %v",
			version.Version, version.EffectiveEpoch, err)
	}
	return nil
}

// epochConfigPredecessor returns the history row immediately before the given
// effective epoch, reporting whether one exists.
func (k Keeper) epochConfigPredecessor(ctx context.Context, effectiveEpoch uint64) (types.EpochConfigVersion, bool, error) {
	rng := new(collections.Range[uint64]).EndExclusive(effectiveEpoch).Descending()
	iter, err := k.EpochConfigVersions.Iterate(ctx, rng)
	if err != nil {
		return types.EpochConfigVersion{}, false, types.ErrInvalidState.Wrapf(
			"epoch configuration history could not be read: %v", err)
	}
	defer iter.Close()
	if !iter.Valid() {
		return types.EpochConfigVersion{}, false, nil
	}
	key, err := iter.Key()
	if err != nil {
		return types.EpochConfigVersion{}, false, types.ErrInvalidState.Wrapf(
			"epoch configuration history key could not be read: %v", err)
	}
	version, err := iter.Value()
	if err != nil {
		return types.EpochConfigVersion{}, false, types.ErrInvalidState.Wrapf(
			"epoch configuration version at effective epoch %d could not be read: %v", key, err)
	}
	if err := validateEpochConfigRecord(key, version); err != nil {
		return types.EpochConfigVersion{}, false, err
	}
	return version, true, nil
}

// validateAdjacentHistoryEdge checks a version against the one immediately before
// it: the single edge the version's own start height is meaningful across.
//
// Three properties, all of which the append path establishes and none of which a
// later read may assume:
//
//   - versions and effective epochs increase together (§82). A version number
//     that does not advance with its effective epoch makes "latest" ambiguous.
//   - the successor starts exactly where the predecessor's recurrence puts it.
//     A gap or overlap here is a block that belongs to two epochs or to none.
func (k Keeper) validateAdjacentHistoryEdge(ctx context.Context, version types.EpochConfigVersion) error {
	predecessor, found, err := k.epochConfigPredecessor(ctx, version.EffectiveEpoch)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if predecessor.Version >= version.Version {
		return types.ErrInvalidState.Wrapf(
			"epoch configuration version %d at effective epoch %d does not advance past version %d at effective epoch %d",
			version.Version, version.EffectiveEpoch, predecessor.Version, predecessor.EffectiveEpoch)
	}
	expectedStart, err := epochStartFrom(predecessor, version.EffectiveEpoch)
	if err != nil {
		return err
	}
	if expectedStart != version.EffectiveStartHeight {
		return types.ErrInvalidState.Wrapf(
			"epoch configuration version %d is effective at epoch %d from height %d, but version %d places that epoch at height %d",
			version.Version, version.EffectiveEpoch, version.EffectiveStartHeight,
			predecessor.Version, expectedStart)
	}
	return nil
}

// epochConfigVersionFor returns the version governing epoch N.
//
// Resolution is a single predecessor seek, not a walk: the map is keyed by
// effective epoch under a big-endian codec, so a descending iteration bounded
// above by N stops at the first — and therefore greatest — applicable entry.
//
// The row found is validated locally and across its adjacent history edge before
// it is returned; see the note above validateEpochConfigRecord for why that is
// the boundary of the check. Genuine absence (no version at or below N) is
// reported as ErrEpochConfigNotFound so callers can tell "this epoch predates the
// history" from "the history could not be read".
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
	if err := validateEpochConfigRecord(key, version); err != nil {
		return types.EpochConfigVersion{}, err
	}
	if err := k.validateAdjacentHistoryEdge(ctx, version); err != nil {
		return types.EpochConfigVersion{}, err
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
			if err := ValidateScheduledEpochConfigRecord(next, scheduled); err != nil {
				return 0, err
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
//
// The last row is not trusted on the strength of being last. Its key is read and
// checked against the record, the record is validated, and so is the adjacent
// edge behind it — because this value is what the next appended version is
// derived FROM, and deriving a new history record on top of a malformed latest
// one would launder the corruption into a record that looks canonical.
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
	key, err := iter.Key()
	if err != nil {
		return types.EpochConfigVersion{}, types.ErrInvalidState.Wrapf(
			"latest epoch configuration history key could not be read: %v", err)
	}
	version, err := iter.Value()
	if err != nil {
		return types.EpochConfigVersion{}, types.ErrInvalidState.Wrapf(
			"latest epoch configuration version could not be read: %v", err)
	}
	if err := validateEpochConfigRecord(key, version); err != nil {
		return types.EpochConfigVersion{}, err
	}
	if err := k.validateAdjacentHistoryEdge(ctx, version); err != nil {
		return types.EpochConfigVersion{}, err
	}
	return version, nil
}

// appendEpochConfigVersion writes one immutable history entry.
//
// Every property the read path later relies on is established here: the record's
// own shape, the ratified epoch-length bound, and the adjacent edge to the
// history it extends. Strictly increasing effective epoch also subsumes
// write-once — a version at an effective epoch that already has one would rewrite
// history rather than extend it, and is refused rather than overwritten.
func (k Keeper) appendEpochConfigVersion(ctx context.Context, version types.EpochConfigVersion) error {
	if err := version.Validate(); err != nil {
		return err
	}
	if err := validateEpochConfigRecord(version.EffectiveEpoch, version); err != nil {
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
	latest, err := k.latestEpochConfigVersion(ctx)
	if err != nil {
		return err
	}
	if version.EffectiveEpoch <= latest.EffectiveEpoch {
		return types.ErrInvalidState.Wrapf(
			"epoch configuration version %d would take effect at epoch %d, at or before the latest version %d effective at epoch %d",
			version.Version, version.EffectiveEpoch, latest.Version, latest.EffectiveEpoch)
	}
	if version.Version <= latest.Version {
		return types.ErrInvalidState.Wrapf(
			"epoch configuration version %d does not advance past the latest version %d",
			version.Version, latest.Version)
	}
	expectedStart, err := epochStartFrom(latest, version.EffectiveEpoch)
	if err != nil {
		return err
	}
	if expectedStart != version.EffectiveStartHeight {
		return types.ErrInvalidState.Wrapf(
			"epoch configuration version %d would anchor epoch %d at height %d, but the preceding geometry places it at height %d",
			version.Version, version.EffectiveEpoch, version.EffectiveStartHeight, expectedStart)
	}
	return k.EpochConfigVersions.Set(ctx, version.EffectiveEpoch, version)
}

// ValidateScheduledEpochConfigRecord checks one schedule row against the key it
// was stored under and against the canonical shape a schedule entry must have.
//
// A scheduled entry becomes an immutable history version the moment its epoch
// opens, so it is held to the same admission bound as history itself: a schedule
// carrying an inadmissible length is refused where it is read, not silently
// promoted at the boundary.
func ValidateScheduledEpochConfigRecord(key uint64, scheduled types.ScheduledEpochConfig) error {
	if scheduled.EffectiveEpoch != key {
		return types.ErrInvalidState.Wrapf(
			"scheduled epoch configuration stored at epoch %d declares effective epoch %d",
			key, scheduled.EffectiveEpoch)
	}
	if scheduled.EffectiveEpoch == 0 {
		return types.ErrInvalidState.Wrap("scheduled epoch configuration has no effective epoch")
	}
	if err := appparams.ValidateEpochLengthBlocks(scheduled.EpochLengthBlocks); err != nil {
		return types.ErrInvalidState.Wrapf(
			"scheduled epoch configuration at epoch %d is inadmissible: %v",
			scheduled.EffectiveEpoch, err)
	}
	return nil
}

// verifyCurrentEpochAnchor checks that the stored open-epoch anchor agrees with
// the canonical geometry.
//
// RewardsState carries CurrentEpochStartHeight as a convenience, but the epoch's
// real start is derived from EpochConfigVersion history. Those two must name the
// same block. When they disagree, one of them is wrong and there is no way to
// tell which from inside the module — and both feed consequential work: the
// stored value is written into the finalized epoch record, while the derived one
// decides the boundary. Proceeding would either close the wrong epoch or record
// the wrong span for the one it closed, so the disagreement halts the block.
func (k Keeper) verifyCurrentEpochAnchor(ctx context.Context, state types.RewardsState) error {
	derived, err := k.EpochStartHeight(ctx, state.CurrentEpoch)
	if err != nil {
		return err
	}
	if derived != state.CurrentEpochStartHeight {
		return types.ErrInvalidState.Wrapf(
			"open epoch %d is recorded as starting at height %d but the canonical configuration history places it at height %d",
			state.CurrentEpoch, state.CurrentEpochStartHeight, derived)
	}
	return nil
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
	if err := ValidateScheduledEpochConfigRecord(epoch, scheduled); err != nil {
		return false, err
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
