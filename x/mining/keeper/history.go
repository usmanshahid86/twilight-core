package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"

	"github.com/twilight-project/twilight-core/internal/checked"
	"github.com/twilight-project/twilight-core/x/mining/types"
)

// Canonical version-history resolution shared by the three mining families.
//
// # What is shared and what is not
//
// The seek is shared: every family is keyed by the epoch its version takes effect
// at, so "the version governing epoch E" is a descending range bounded above by E,
// and "the newest version" is a descending range with no bound. That is one
// mechanism and it is written once.
//
// The RECORD is not shared. The distribution-mode record carries a validity
// interval; the two parameter records carry a single effective epoch. The helpers
// below are therefore generic over the KEY and hand the caller back a raw row to
// validate under its own rules, rather than flattening three record shapes into
// one.
//
// # Version numbering
//
// Unique and monotonically increasing, NOT contiguous. Promotion happens to derive
// the successor as latest+1, which is the only deterministic choice available, but
// nothing VALIDATES contiguity: a history that arrived with gaps is canonical and
// must keep resolving. Do not add a successor check here by analogy with the
// reward-configuration history, whose contiguity was separately ratified for a
// reason that does not apply to these families.

// seekVersionKeyAtOrBefore returns the greatest key in a history at or before the
// given epoch, reporting whether one exists.
//
// A descending range bounded above by the epoch lands directly on the answer, so
// resolution cost does not track how many versions the chain has accepted.
func seekVersionKeyAtOrBefore[V any](
	ctx context.Context, history collections.Map[uint64, V], epoch uint64,
) (uint64, V, bool, error) {
	var zero V
	rng := new(collections.Range[uint64]).EndInclusive(epoch).Descending()
	iter, err := history.Iterate(ctx, rng)
	if err != nil {
		return 0, zero, false, types.ErrInvalidState.Wrapf("version history could not be read: %v", err)
	}
	defer iter.Close()
	if !iter.Valid() {
		return 0, zero, false, nil
	}
	key, err := iter.Key()
	if err != nil {
		return 0, zero, false, types.ErrInvalidState.Wrapf("version history key could not be read: %v", err)
	}
	value, err := iter.Value()
	if err != nil {
		return 0, zero, false, types.ErrInvalidState.Wrapf(
			"version history entry at epoch %d could not be read: %v", key, err)
	}
	return key, value, true, nil
}

// seekLatestVersion returns the newest entry of a history, reporting whether one
// exists. One reverse seek, no walk.
func seekLatestVersion[V any](
	ctx context.Context, history collections.Map[uint64, V],
) (uint64, V, bool, error) {
	var zero V
	iter, err := history.Iterate(ctx, new(collections.Range[uint64]).Descending())
	if err != nil {
		return 0, zero, false, types.ErrInvalidState.Wrapf("version history could not be read: %v", err)
	}
	defer iter.Close()
	if !iter.Valid() {
		return 0, zero, false, nil
	}
	key, err := iter.Key()
	if err != nil {
		return 0, zero, false, types.ErrInvalidState.Wrapf("version history key could not be read: %v", err)
	}
	value, err := iter.Value()
	if err != nil {
		return 0, zero, false, types.ErrInvalidState.Wrapf(
			"latest version history entry could not be read: %v", err)
	}
	return key, value, true, nil
}

// setVersionIndexEntry records a derived version -> epoch-key mapping.
//
// Write-once. A version number that already has an entry makes "the row for
// version N" unanswerable, and since these histories do not require contiguous
// numbering there is no arithmetic that could disambiguate it afterwards.
func setVersionIndexEntry(
	ctx context.Context, index collections.Map[uint64, uint64], version, epochKey uint64,
) error {
	if version == 0 {
		return types.ErrInvalidState.Wrap("version numbers start at 1")
	}
	existing, err := index.Get(ctx, version)
	switch {
	case err == nil:
		return types.ErrInvalidState.Wrapf(
			"version %d is already indexed at epoch %d and cannot also be at epoch %d",
			version, existing, epochKey)
	case !errors.Is(err, collections.ErrNotFound):
		return types.ErrInvalidState.Wrapf(
			"version index entry for version %d could not be read: %v", version, err)
	}
	return index.Set(ctx, version, epochKey)
}

// lookupVersionEpochKey resolves a bare version number to its history key through
// the derived index, reporting whether one exists.
//
// A missing entry is ORDINARY ABSENCE here. That differs from the
// reward-configuration index, where the same condition is corruption, and the
// difference follows from the numbering rule rather than from a weaker posture:
// with contiguous versions "in range but unindexed" is decidable arithmetically,
// and with merely monotonic versions it is not. The caller is responsible for
// cross-checking the row this key reaches.
func lookupVersionEpochKey(
	ctx context.Context, index collections.Map[uint64, uint64], version uint64,
) (uint64, bool, error) {
	if version == 0 {
		return 0, false, types.ErrInvalidState.Wrap("version numbers start at 1")
	}
	epochKey, err := index.Get(ctx, version)
	if errors.Is(err, collections.ErrNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, types.ErrInvalidState.Wrapf(
			"version index entry for version %d could not be read: %v", version, err)
	}
	return epochKey, true, nil
}

// scheduledEntryFor returns a family's single pending change and says whether it
// is due at the given boundary.
//
// Three outcomes, and the middle one is why this is not a plain Get:
//
//   - key == effectiveEpoch — due now; the caller promotes it.
//   - key >  effectiveEpoch — scheduled for a LATER boundary. Not yet due, and not
//     an error. An update is accepted well before it takes effect, so the schedule
//     is legitimately occupied across boundaries it is not due at; treating that as
//     corruption would halt the chain at every epoch close in between.
//   - key <  effectiveEpoch — corruption. It was due at a boundary that has already
//     passed and was never consumed, so the chain has been running under a
//     configuration it had already accepted a replacement for.
//
// A second pending row is refused outright: the protocol schedules at most one
// change per family, and promoting one of two would silently choose which of them
// takes effect. The read stops after the second key, so a corrupt schedule holding
// many rows still costs O(1).
func scheduledEntryFor[V any](
	ctx context.Context, schedule collections.Map[uint64, V], effectiveEpoch uint64, family string,
) (uint64, V, bool, error) {
	var zero V
	iter, err := schedule.Iterate(ctx, nil)
	if err != nil {
		return 0, zero, false, types.ErrInvalidState.Wrapf(
			"the scheduled %s could not be read: %v", family, err)
	}
	defer iter.Close()
	if !iter.Valid() {
		return 0, zero, false, nil
	}
	key, err := iter.Key()
	if err != nil {
		return 0, zero, false, types.ErrInvalidState.Wrapf(
			"the scheduled %s key could not be read: %v", family, err)
	}
	value, err := iter.Value()
	if err != nil {
		return 0, zero, false, types.ErrInvalidState.Wrapf(
			"the %s scheduled at epoch %d could not be read: %v", family, key, err)
	}
	iter.Next()
	if iter.Valid() {
		second, err := iter.Key()
		if err != nil {
			return 0, zero, false, types.ErrInvalidState.Wrapf(
				"the scheduled %s key could not be read: %v", family, err)
		}
		return 0, zero, false, types.ErrInvalidState.Wrapf(
			"the %s schedule holds pending changes at epochs %d and %d; at most one may be pending",
			family, key, second)
	}
	if key < effectiveEpoch {
		return 0, zero, false, types.ErrInvalidState.Wrapf(
			"the %s scheduled at epoch %d was due at a boundary that has already passed; "+
				"the boundary now opening is epoch %d", family, key, effectiveEpoch)
	}
	return key, value, key == effectiveEpoch, nil
}

// nextVersionNumber derives the successor a promotion will assign.
//
// latest+1 under checked arithmetic. The result happens to be contiguous, and that
// is a property of this derivation rather than a rule the histories enforce: an
// imported history containing gaps stays canonical and keeps resolving, because
// nothing validates the relation this function produces.
func nextVersionNumber(latest uint64) (uint64, error) {
	next, err := checked.AddUint64(latest, 1)
	if err != nil {
		return 0, types.ErrInvalidState.Wrap("version space is exhausted")
	}
	return next, nil
}

// bindingEpochForTarget returns the epoch a target binds its configuration at.
//
// Target N binds what was effective at N-2, so nothing accepted after N's
// preparation boundary opens can change what N may pay or how long it has to pay.
//
// The bootstrap branch returns BEFORE the subtraction, structurally rather than by
// guarding it. Epoch numbers are unsigned, and an unguarded N-2 for targets 1 and
// 2 underflows to an enormous epoch whose descending seek resolves the NEWEST
// version — the exact opposite of the intended answer. Placing the decision above
// the arithmetic means no later edit can reorder a guard into that hazard.
//
// Both families that use this state the same bootstrap rule in their own sections
// rather than inheriting it by analogy.
func bindingEpochForTarget(target uint64) (epoch uint64, bootstrap bool, err error) {
	if target == 0 {
		return 0, false, types.ErrInvalidState.Wrap("epoch numbers start at 1")
	}
	if target <= 2 {
		return 0, true, nil
	}
	binding, err := checked.SubUint64(target, 2)
	if err != nil {
		return 0, false, types.ErrInvalidState.Wrapf(
			"target epoch %d has no representable binding boundary: %v", target, err)
	}
	return binding, false, nil
}
