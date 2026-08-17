package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"

	"github.com/twilight-project/twilight-core/x/mining/types"
)

// The chain-wide distribution-mode history.
//
// # Why this family is shaped differently
//
// The two parameter histories say "this version is effective from epoch E". This
// one says "this version is valid over [from, until)". The interval is canonical
// and load-bearing: it is what lets a reader confirm the history covers every
// epoch with exactly one mode, without deriving that coverage from neighboring
// keys.
//
// Exactly one row is open at any time, carrying valid_until_epoch_exclusive = 0.
// Every earlier row is closed at precisely its successor's start, so the intervals
// are contiguous, non-overlapping and gap-free.

// validateModeRecord checks a stored mode row against the key it was found under
// and against its own invariants.
func validateModeRecord(key uint64, version types.MiningDistributionModeVersion) error {
	if version.ValidFromEpoch != key {
		return types.ErrInvalidState.Wrapf(
			"distribution mode version stored at epoch %d declares valid_from_epoch %d",
			key, version.ValidFromEpoch)
	}
	return version.Validate()
}

// GenesisDistributionMode returns the permanent anchor of the mode history.
//
// It reads the row at epoch 1 directly and requires it to be version 1. That is
// stricter than "the earliest row": the anchor is protocol identity, and a history
// whose first interval does not begin at epoch 1 cannot cover the chain's first
// targets.
func (k Keeper) GenesisDistributionMode(ctx context.Context) (types.MiningDistributionModeVersion, error) {
	version, err := k.DistributionModeVersions.Get(ctx, 1)
	if errors.Is(err, collections.ErrNotFound) {
		return types.MiningDistributionModeVersion{}, types.ErrParamsNotFound.Wrap(
			"the initial distribution mode valid from epoch 1 is absent")
	}
	if err != nil {
		return types.MiningDistributionModeVersion{}, types.ErrInvalidState.Wrapf(
			"the initial distribution mode could not be read: %v", err)
	}
	if err := validateModeRecord(1, version); err != nil {
		return types.MiningDistributionModeVersion{}, err
	}
	if version.Version != 1 {
		return types.MiningDistributionModeVersion{}, types.ErrInvalidState.Wrapf(
			"the initial distribution mode must be version 1 valid from epoch 1, found version %d",
			version.Version)
	}
	return version, nil
}

// DistributionModeForTarget returns the mode governing a target epoch.
//
// Target N uses the mode effective at its N-2 boundary; targets 1 and 2 have no
// such boundary inside chain history and bootstrap to the genesis anchor, which is
// this family's own stated rule rather than one inherited from another history.
//
// The resolved row is required to actually COVER the binding epoch. A history that
// resolved a closed interval for an epoch past its end would be answering with a
// mode that had already stopped applying, which is the one thing the interval
// shape exists to make detectable.
func (k Keeper) DistributionModeForTarget(
	ctx context.Context, target uint64,
) (types.MiningDistributionModeVersion, error) {
	bindingEpoch, bootstrap, err := bindingEpochForTarget(target)
	if err != nil {
		return types.MiningDistributionModeVersion{}, err
	}
	if bootstrap {
		return k.GenesisDistributionMode(ctx)
	}
	// The permanent anchor, before the seek. A seek alone cannot notice that the
	// anchor has gone: any later row answers "greatest key at or before" perfectly
	// well, so a history missing its origin would keep resolving against remains.
	if _, err := k.GenesisDistributionMode(ctx); err != nil {
		return types.MiningDistributionModeVersion{}, err
	}

	key, version, found, err := seekVersionKeyAtOrBefore(ctx, k.DistributionModeVersions, bindingEpoch)
	if err != nil {
		return types.MiningDistributionModeVersion{}, err
	}
	if !found {
		return types.MiningDistributionModeVersion{}, types.ErrParamsNotFound.Wrapf(
			"no distribution mode is valid at or before epoch %d", bindingEpoch)
	}
	if err := validateModeRecord(key, version); err != nil {
		return types.MiningDistributionModeVersion{}, err
	}
	if version.ValidUntilEpochExclusive != 0 && bindingEpoch >= version.ValidUntilEpochExclusive {
		return types.MiningDistributionModeVersion{}, types.ErrInvalidState.Wrapf(
			"distribution mode version %d covers epochs [%d, %d) and does not cover epoch %d",
			version.Version, version.ValidFromEpoch, version.ValidUntilEpochExclusive, bindingEpoch)
	}
	return version, nil
}

// openDistributionMode returns the single row whose interval is still open.
//
// The newest row is the open one on a canonical history, so this is a reverse seek
// rather than a scan. It is checked rather than assumed: finding a closed newest
// row means the history covers no epoch past its end, and promotion must refuse to
// extend it rather than reopen it.
func (k Keeper) openDistributionMode(ctx context.Context) (uint64, types.MiningDistributionModeVersion, error) {
	key, version, found, err := seekLatestVersion(ctx, k.DistributionModeVersions)
	if err != nil {
		return 0, types.MiningDistributionModeVersion{}, err
	}
	if !found {
		return 0, types.MiningDistributionModeVersion{}, types.ErrParamsNotFound.Wrap(
			"distribution mode history is empty")
	}
	if err := validateModeRecord(key, version); err != nil {
		return 0, types.MiningDistributionModeVersion{}, err
	}
	if version.ValidUntilEpochExclusive != 0 {
		return 0, types.MiningDistributionModeVersion{}, types.ErrInvalidState.Wrapf(
			"the newest distribution mode version %d is already closed at epoch %d; "+
				"a canonical history keeps exactly one open interval",
			version.Version, version.ValidUntilEpochExclusive)
	}
	return key, version, nil
}

// promoteScheduledDistributionMode turns a mode scheduled for the given epoch into
// immutable history.
//
// # The one permitted mutation of effective history
//
// version, mode and valid_from_epoch are immutable once effective. The ONLY
// permitted change to an already-effective row is the one-time closure of the
// immediate predecessor when its successor becomes effective:
//
//	valid_until_epoch_exclusive: 0 -> successor.valid_from_epoch
//
// After that closure the predecessor is fully immutable. A nonzero
// valid_until_epoch_exclusive is never rewritten, which is why the preflight below
// refuses to proceed against an already-closed predecessor rather than overwriting
// its boundary.
//
// # Atomicity
//
// Every effect commits together or none does. The caller supplies the cache: this
// runs inside the single EndBlock transition, so a failure at any step leaves the
// predecessor open, the successor absent and the schedule unconsumed — which is
// exactly the state a later block can retry from. Nothing here repairs malformed
// history; a history that is already wrong is refused, not corrected.
func (k Keeper) promoteScheduledDistributionMode(ctx context.Context, effectiveEpoch uint64) error {
	scheduled, found, err := k.scheduledDistributionModeFor(ctx, effectiveEpoch)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}

	predecessorKey, predecessor, err := k.openDistributionMode(ctx)
	if err != nil {
		return err
	}
	if effectiveEpoch <= predecessor.ValidFromEpoch {
		return types.ErrInvalidState.Wrapf(
			"a distribution mode scheduled for epoch %d cannot follow version %d valid from epoch %d",
			effectiveEpoch, predecessor.Version, predecessor.ValidFromEpoch)
	}
	nextVersion, err := nextVersionNumber(predecessor.Version)
	if err != nil {
		return err
	}
	// The successor's key must be free. Finding a row there means the history
	// already contains what this promotion would create, which is corruption rather
	// than an idempotent retry.
	if exists, err := k.DistributionModeVersions.Has(ctx, effectiveEpoch); err != nil {
		return types.ErrInvalidState.Wrapf(
			"distribution mode history at epoch %d could not be checked: %v", effectiveEpoch, err)
	} else if exists {
		return types.ErrInvalidState.Wrapf(
			"a distribution mode version already exists at epoch %d", effectiveEpoch)
	}

	successor := types.MiningDistributionModeVersion{
		Version:                  nextVersion,
		Mode:                     scheduled.Mode,
		ValidFromEpoch:           effectiveEpoch,
		ValidUntilEpochExclusive: 0,
	}
	if err := successor.Validate(); err != nil {
		return err
	}

	// Close the predecessor at exactly the successor's start, then append. The
	// order is deliberate: the closure is the mutation of existing history and is
	// performed against a row this function has already proven to be open.
	predecessor.ValidUntilEpochExclusive = successor.ValidFromEpoch
	if err := validateModeRecord(predecessorKey, predecessor); err != nil {
		return err
	}
	if err := k.DistributionModeVersions.Set(ctx, predecessorKey, predecessor); err != nil {
		return err
	}
	if err := k.DistributionModeVersions.Set(ctx, successor.ValidFromEpoch, successor); err != nil {
		return err
	}
	if err := setVersionIndexEntry(
		ctx, k.DistributionModeVersionIndex, successor.Version, successor.ValidFromEpoch,
	); err != nil {
		return err
	}
	if err := k.ScheduledDistributionMode.Remove(ctx, effectiveEpoch); err != nil {
		return types.ErrInvalidState.Wrapf(
			"scheduled distribution mode at epoch %d could not be consumed: %v", effectiveEpoch, err)
	}
	return nil
}

// scheduledDistributionModeFor reads the pending mode change due at one boundary.
func (k Keeper) scheduledDistributionModeFor(
	ctx context.Context, effectiveEpoch uint64,
) (types.ScheduledMiningDistributionMode, bool, error) {
	key, scheduled, due, err := scheduledEntryFor(
		ctx, k.ScheduledDistributionMode, effectiveEpoch, "distribution mode")
	if err != nil || !due {
		return types.ScheduledMiningDistributionMode{}, false, err
	}
	if scheduled.EffectiveEpoch != key {
		return types.ScheduledMiningDistributionMode{}, false, types.ErrInvalidState.Wrapf(
			"scheduled distribution mode stored at epoch %d declares effective epoch %d",
			key, scheduled.EffectiveEpoch)
	}
	if err := validateDistributionModeValue(scheduled.Mode); err != nil {
		return types.ScheduledMiningDistributionMode{}, false, err
	}
	return scheduled, true, nil
}

func validateDistributionModeValue(mode types.MiningDistributionMode) error {
	switch mode {
	case types.MiningDistributionMode_MINING_DISTRIBUTION_MODE_TRUSTED_AS_DISTRIBUTION,
		types.MiningDistributionMode_MINING_DISTRIBUTION_MODE_PROTOCOL_SELECTION:
		return nil
	default:
		return types.ErrInvalidState.Wrapf("distribution mode %s is not a canonical mode", mode)
	}
}
