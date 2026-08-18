package keeper

import (
	"context"

	"cosmossdk.io/collections"

	"github.com/twilight-project/twilight-core/x/mining/types"
)

// Exact lookup of a configuration version, and the bounded proof that a version
// which is not indexed genuinely does not exist.
//
// # Why this is not a map lookup
//
// The three mining histories are keyed by the epoch a version becomes effective
// at. Version NUMBERS are unique and strictly increasing but NOT contiguous, so a
// derived version -> effective-epoch index exists to make lookup by version cheap.
//
// That index is an accelerator and never membership authority. If a missing index
// entry were answered with "no such version", then corrupting one entry would make
// an existing canonical record publicly invisible — and invisible is exactly what a
// caller reconciling its own state cannot distinguish from never-existed.
//
// So a missing entry is only ever an answer once the ABSENCE has been proven from
// canonical state. Everything else is corruption.

// versionClassification is the outcome of an exact-version lookup.
type versionClassification int

const (
	// versionFound: the canonical record exists and was cross-checked.
	versionFound versionClassification = iota
	// versionAboveLatest: beyond the newest version the chain has ever assigned.
	versionAboveLatest
	// versionIntentionalGap: PROVEN to fall strictly between two adjacent canonical
	// records, so no such version was ever assigned.
	versionIntentionalGap
)

// resolveExactVersion locates the canonical history key for a version number, or
// proves the version does not exist.
//
// Generic over the record, because the three families store different shapes but
// share one numbering rule. The caller supplies how to read a version number out of
// a record and how to validate one, so each family keeps its own record semantics.
//
// Every read below is a point lookup or a single bounded seek. Nothing scans, and
// no answer costs work proportional to how many versions the chain has accepted.
func resolveExactVersion[V any](
	ctx context.Context,
	history collections.Map[uint64, V],
	index collections.Map[uint64, uint64],
	version uint64,
	versionOf func(V) uint64,
	validate func(key uint64, record V) error,
	family string,
) (uint64, versionClassification, error) {
	if version == 0 {
		return 0, versionFound, types.ErrInvalidState.Wrapf("%s version numbers start at 1", family)
	}

	// The newest record, in one reverse seek. Its version is the highest the chain
	// has ever assigned, because versions increase with the epochs that carry them.
	latestKey, latestRecord, found, err := seekLatestVersion(ctx, history)
	if err != nil {
		return 0, versionFound, err
	}
	if !found {
		// Fresh genesis writes a version 1 for every family, so an empty history is
		// not an ordinary state a query may report around.
		return 0, versionFound, types.ErrInvalidState.Wrapf("%s history is empty", family)
	}
	if err := validate(latestKey, latestRecord); err != nil {
		return 0, versionFound, err
	}
	if version > versionOf(latestRecord) {
		return 0, versionAboveLatest, nil
	}

	epochKey, indexed, err := lookupVersionEpochKey(ctx, index, version)
	if err != nil {
		return 0, versionFound, err
	}
	if indexed {
		// The index is a locator only. The record it points at is reread and held to
		// the number that was asked for, so a mispointed entry surfaces as corruption
		// rather than silently answering with a neighboring version.
		record, err := history.Get(ctx, epochKey)
		if err != nil {
			return 0, versionFound, types.ErrInvalidState.Wrapf(
				"%s version %d is indexed at epoch %d, which holds no readable record: %v",
				family, version, epochKey, err)
		}
		if err := validate(epochKey, record); err != nil {
			return 0, versionFound, err
		}
		if versionOf(record) != version {
			return 0, versionFound, types.ErrInvalidState.Wrapf(
				"%s version %d is indexed at epoch %d, which holds version %d",
				family, version, epochKey, versionOf(record))
		}
		return epochKey, versionFound, nil
	}

	// Not indexed, and not above the latest. Either the number was never assigned —
	// a gap between two adjacent records — or a canonical record exists whose index
	// entry has been lost. Those are opposite answers, so the gap has to be PROVEN.
	if err := proveVersionGap(ctx, history, index, version, versionOf, validate, family); err != nil {
		return 0, versionFound, err
	}
	return 0, versionIntentionalGap, nil
}

// proveVersionGap establishes that no canonical record carries a version number, in
// a bounded number of seeks.
//
// # The proof
//
// Find the nearest indexed version below and above. Read the canonical records they
// locate. Then take ONE step through the canonical history from the lower record: if
// the very next record is the upper one, the two are adjacent in canonical order,
// and no record can exist between them — so the version being asked about was never
// assigned.
//
// That last step is what makes the proof sound, and it is the step a plausible
// implementation omits. Neighboring INDEX entries prove nothing on their own,
// because the index is what is under suspicion: a history of v1, v3, v5 whose entry
// for v3 has been lost presents exactly the same neighbors as a genuine gap between
// v1 and v5. Only the canonical history can distinguish them, and it does so in one
// step rather than a scan.
func proveVersionGap[V any](
	ctx context.Context,
	history collections.Map[uint64, V],
	index collections.Map[uint64, uint64],
	version uint64,
	versionOf func(V) uint64,
	validate func(key uint64, record V) error,
	family string,
) error {
	lowerVersion, lowerEpoch, found, err := seekIndexedVersionBelow(ctx, index, version)
	if err != nil {
		return err
	}
	if !found {
		// Every family's history begins at version 1, so some indexed version at or
		// below this one must exist. None means the index has lost its origin, and
		// nothing below can be proven against a history whose start is unknown.
		return types.ErrInvalidState.Wrapf(
			"%s version %d is not indexed and no earlier version is either; "+
				"the version index has lost its origin", family, version)
	}
	upperVersion, upperEpoch, found, err := seekIndexedVersionAbove(ctx, index, version)
	if err != nil {
		return err
	}
	if !found {
		// The caller already proved this version does not exceed the latest canonical
		// one, so an indexed version above it must exist.
		return types.ErrInvalidState.Wrapf(
			"%s version %d is within the assigned range but no later version is indexed; "+
				"the version index is incomplete", family, version)
	}

	lower, err := history.Get(ctx, lowerEpoch)
	if err != nil {
		return types.ErrInvalidState.Wrapf(
			"%s version %d is indexed at epoch %d, which holds no readable record: %v",
			family, lowerVersion, lowerEpoch, err)
	}
	if err := validate(lowerEpoch, lower); err != nil {
		return err
	}
	upper, err := history.Get(ctx, upperEpoch)
	if err != nil {
		return types.ErrInvalidState.Wrapf(
			"%s version %d is indexed at epoch %d, which holds no readable record: %v",
			family, upperVersion, upperEpoch, err)
	}
	if err := validate(upperEpoch, upper); err != nil {
		return err
	}
	if versionOf(lower) != lowerVersion || versionOf(upper) != upperVersion {
		return types.ErrInvalidState.Wrapf(
			"%s versions %d and %d are indexed at epochs holding versions %d and %d",
			family, lowerVersion, upperVersion, versionOf(lower), versionOf(upper))
	}
	// Unreachable as written: the two seeks are already strict, so the predecessor is
	// below and the successor above by construction. No test asserts this arm. It is
	// kept because the bracketing is the property the adjacency step below is proving
	// something ABOUT, and a future change to either seek's bound would otherwise
	// silently invalidate the proof rather than fail here.
	if lowerVersion >= version || version >= upperVersion {
		return types.ErrInvalidState.Wrapf(
			"%s version %d is not bracketed by its indexed neighbors %d and %d",
			family, version, lowerVersion, upperVersion)
	}

	// The one canonical step. Anything other than the upper record here means a
	// record sits between them, so the missing index entry is a lost entry and not a
	// gap — and the absence cannot be proven.
	nextEpoch, found, err := seekNextHistoryKey(ctx, history, lowerEpoch)
	if err != nil {
		return err
	}
	if !found || nextEpoch != upperEpoch {
		return types.ErrInvalidState.Wrapf(
			"%s version %d cannot be proven absent: the record after epoch %d is not the "+
				"indexed record at epoch %d, so a canonical record may exist between versions %d and %d",
			family, version, lowerEpoch, upperEpoch, lowerVersion, upperVersion)
	}
	return nil
}

// seekIndexedVersionBelow returns the greatest indexed version strictly below the
// given one, in a single descending seek.
func seekIndexedVersionBelow(
	ctx context.Context, index collections.Map[uint64, uint64], version uint64,
) (indexedVersion, epochKey uint64, found bool, err error) {
	if version <= 1 {
		return 0, 0, false, nil
	}
	rng := new(collections.Range[uint64]).EndInclusive(version - 1).Descending()
	return firstIndexEntry(ctx, index, rng)
}

// seekIndexedVersionAbove returns the smallest indexed version strictly above the
// given one, in a single ascending seek.
func seekIndexedVersionAbove(
	ctx context.Context, index collections.Map[uint64, uint64], version uint64,
) (indexedVersion, epochKey uint64, found bool, err error) {
	rng := new(collections.Range[uint64]).StartExclusive(version)
	return firstIndexEntry(ctx, index, rng)
}

func firstIndexEntry(
	ctx context.Context, index collections.Map[uint64, uint64], rng *collections.Range[uint64],
) (indexedVersion, epochKey uint64, found bool, err error) {
	iter, iterErr := index.Iterate(ctx, rng)
	if iterErr != nil {
		return 0, 0, false, types.ErrInvalidState.Wrapf(
			"version index could not be read: %v", iterErr)
	}
	defer iter.Close()
	if !iter.Valid() {
		return 0, 0, false, nil
	}
	key, keyErr := iter.Key()
	if keyErr != nil {
		return 0, 0, false, types.ErrInvalidState.Wrapf(
			"version index key could not be read: %v", keyErr)
	}
	value, valErr := iter.Value()
	if valErr != nil {
		return 0, 0, false, types.ErrInvalidState.Wrapf(
			"version index entry for version %d could not be read: %v", key, valErr)
	}
	return key, value, true, nil
}

// seekNextHistoryKey returns the canonical history key immediately after the given
// one. Exactly one step; never a scan.
func seekNextHistoryKey[V any](
	ctx context.Context, history collections.Map[uint64, V], after uint64,
) (uint64, bool, error) {
	rng := new(collections.Range[uint64]).StartExclusive(after)
	iter, err := history.Iterate(ctx, rng)
	if err != nil {
		return 0, false, types.ErrInvalidState.Wrapf("version history could not be read: %v", err)
	}
	defer iter.Close()
	if !iter.Valid() {
		return 0, false, nil
	}
	key, err := iter.Key()
	if err != nil {
		return 0, false, types.ErrInvalidState.Wrapf(
			"version history key could not be read: %v", err)
	}
	return key, true, nil
}
