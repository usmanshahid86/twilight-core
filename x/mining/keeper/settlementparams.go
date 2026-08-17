package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	sdkmath "cosmossdk.io/math"

	"github.com/twilight-project/twilight-core/internal/checked"
	"github.com/twilight-project/twilight-core/x/mining/types"
	rewardstypes "github.com/twilight-project/twilight-core/x/rewards/types"
)

// Settlement-validity parameters and the quantities derived from them.
//
// These parameters decide what a settlement may pay and how long it has to pay it,
// so they are bound to a target BEFORE that target's preparation boundary opens. A
// governance change accepted afterwards cannot reduce or reinterpret an
// already-bound target's payout capacity or deadline.

func validateSettlementParamsRecord(key uint64, version types.SettlementParamsVersion) error {
	if version.EffectiveEpoch != key {
		return types.ErrInvalidState.Wrapf(
			"settlement params version stored at epoch %d declares effective epoch %d",
			key, version.EffectiveEpoch)
	}
	return version.Validate()
}

// GenesisSettlementParams returns the permanent anchor of the settlement-parameter
// history.
func (k Keeper) GenesisSettlementParams(ctx context.Context) (types.SettlementParamsVersion, error) {
	version, err := k.SettlementParamsVersions.Get(ctx, 1)
	if errors.Is(err, collections.ErrNotFound) {
		return types.SettlementParamsVersion{}, types.ErrParamsNotFound.Wrap(
			"the initial settlement parameters effective at epoch 1 are absent")
	}
	if err != nil {
		return types.SettlementParamsVersion{}, types.ErrInvalidState.Wrapf(
			"the initial settlement parameters could not be read: %v", err)
	}
	if err := validateSettlementParamsRecord(1, version); err != nil {
		return types.SettlementParamsVersion{}, err
	}
	if version.Version != 1 {
		return types.SettlementParamsVersion{}, types.ErrInvalidState.Wrapf(
			"the initial settlement parameters must be version 1 effective at epoch 1, found version %d",
			version.Version)
	}
	return version, nil
}

// SettlementParamsForTarget returns the parameters governing a target epoch.
//
// Bootstrap targets whose binding boundary predates chain history use the genesis
// version; no synthetic pre-genesis version is invented. This family states that
// rule in its own right rather than inheriting it from another history.
func (k Keeper) SettlementParamsForTarget(
	ctx context.Context, target uint64,
) (types.SettlementParamsVersion, error) {
	bindingEpoch, bootstrap, err := bindingEpochForTarget(target)
	if err != nil {
		return types.SettlementParamsVersion{}, err
	}
	if bootstrap {
		return k.GenesisSettlementParams(ctx)
	}
	if _, err := k.GenesisSettlementParams(ctx); err != nil {
		return types.SettlementParamsVersion{}, err
	}

	key, version, found, err := seekVersionKeyAtOrBefore(ctx, k.SettlementParamsVersions, bindingEpoch)
	if err != nil {
		return types.SettlementParamsVersion{}, err
	}
	if !found {
		return types.SettlementParamsVersion{}, types.ErrParamsNotFound.Wrapf(
			"no settlement parameters are effective at or before epoch %d", bindingEpoch)
	}
	if err := validateSettlementParamsRecord(key, version); err != nil {
		return types.SettlementParamsVersion{}, err
	}
	return version, nil
}

// promoteScheduledSettlementParams turns a scheduled parameter set into immutable
// history.
//
// Simpler than the mode promotion because this record carries no interval: there is
// no predecessor to close, so appending the successor and consuming the schedule is
// the whole transition. Like that one, it runs inside the caller's cache and
// commits with the rest of the block transition or not at all.
func (k Keeper) promoteScheduledSettlementParams(ctx context.Context, effectiveEpoch uint64) error {
	scheduled, found, err := k.scheduledSettlementParamsFor(ctx, effectiveEpoch)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	latestKey, latest, latestFound, err := seekLatestVersion(ctx, k.SettlementParamsVersions)
	if err != nil {
		return err
	}
	if !latestFound {
		return types.ErrParamsNotFound.Wrap("settlement parameter history is empty")
	}
	if err := validateSettlementParamsRecord(latestKey, latest); err != nil {
		return err
	}
	if effectiveEpoch <= latest.EffectiveEpoch {
		return types.ErrInvalidState.Wrapf(
			"settlement parameters scheduled for epoch %d cannot follow version %d effective at epoch %d",
			effectiveEpoch, latest.Version, latest.EffectiveEpoch)
	}
	nextVersion, err := nextVersionNumber(latest.Version)
	if err != nil {
		return err
	}
	successor := types.SettlementParamsVersion{
		Version:                  nextVersion,
		EffectiveEpoch:           effectiveEpoch,
		SettlementWindowEpochs:   scheduled.SettlementWindowEpochs,
		MaxRecipientsPerChunk:    scheduled.MaxRecipientsPerChunk,
		MaxChunksPerSettlement:   scheduled.MaxChunksPerSettlement,
		MinRecipientPayoutAmount: scheduled.MinRecipientPayoutAmount,
	}
	if err := successor.Validate(); err != nil {
		return err
	}
	if err := k.SettlementParamsVersions.Set(ctx, successor.EffectiveEpoch, successor); err != nil {
		return err
	}
	if err := setVersionIndexEntry(
		ctx, k.SettlementParamsVersionIndex, successor.Version, successor.EffectiveEpoch,
	); err != nil {
		return err
	}
	if err := k.ScheduledSettlementParams.Remove(ctx, effectiveEpoch); err != nil {
		return types.ErrInvalidState.Wrapf(
			"scheduled settlement parameters at epoch %d could not be consumed: %v", effectiveEpoch, err)
	}
	return nil
}

func (k Keeper) scheduledSettlementParamsFor(
	ctx context.Context, effectiveEpoch uint64,
) (types.ScheduledSettlementParams, bool, error) {
	key, scheduled, due, err := scheduledEntryFor(
		ctx, k.ScheduledSettlementParams, effectiveEpoch, "settlement parameters")
	if err != nil || !due {
		return types.ScheduledSettlementParams{}, false, err
	}
	if scheduled.EffectiveEpoch != key {
		return types.ScheduledSettlementParams{}, false, types.ErrInvalidState.Wrapf(
			"scheduled settlement parameters stored at epoch %d declare effective epoch %d",
			key, scheduled.EffectiveEpoch)
	}
	return scheduled, true, nil
}

// promoteScheduledSelectionParams mirrors the settlement-parameter promotion.
//
// It exists so the mode and both parameter families share one promotion story at
// the epoch boundary. Nothing in this profile reads Selection parameters on a
// consequential path, but leaving the family without a promotion path would mean a
// later tranche discovering the boundary has no seam.
func (k Keeper) promoteScheduledSelectionParams(ctx context.Context, effectiveEpoch uint64) error {
	key, scheduled, due, err := scheduledEntryFor(
		ctx, k.ScheduledSelectionParams, effectiveEpoch, "selection parameters")
	if err != nil {
		return err
	}
	if !due {
		return nil
	}
	if scheduled.EffectiveEpoch != key {
		return types.ErrInvalidState.Wrapf(
			"scheduled selection parameters stored at epoch %d declare effective epoch %d",
			key, scheduled.EffectiveEpoch)
	}
	latestKey, latest, latestFound, err := seekLatestVersion(ctx, k.SelectionParamsVersions)
	if err != nil {
		return err
	}
	if !latestFound {
		return types.ErrParamsNotFound.Wrap("selection parameter history is empty")
	}
	if latest.EffectiveEpoch != latestKey {
		return types.ErrInvalidState.Wrapf(
			"selection params version stored at epoch %d declares effective epoch %d",
			latestKey, latest.EffectiveEpoch)
	}
	if effectiveEpoch <= latest.EffectiveEpoch {
		return types.ErrInvalidState.Wrapf(
			"selection parameters scheduled for epoch %d cannot follow version %d effective at epoch %d",
			effectiveEpoch, latest.Version, latest.EffectiveEpoch)
	}
	nextVersion, err := nextVersionNumber(latest.Version)
	if err != nil {
		return err
	}
	successor := types.SelectionParamsVersion{
		Version:                             nextVersion,
		EffectiveEpoch:                      effectiveEpoch,
		MaxSelectionRateBps:                 scheduled.MaxSelectionRateBps,
		MaxSelectedParticipantsPerSelection: scheduled.MaxSelectedParticipantsPerSelection,
		MaxCandidatesPerSelection:           scheduled.MaxCandidatesPerSelection,
		BeaconStartOffsetBlocks:             scheduled.BeaconStartOffsetBlocks,
		BeaconWindowBlocks:                  scheduled.BeaconWindowBlocks,
		MinExternalBeaconBlocks:             scheduled.MinExternalBeaconBlocks,
		MinDistinctExternalProposers:        scheduled.MinDistinctExternalProposers,
	}
	if err := successor.Validate(); err != nil {
		return err
	}
	if err := k.SelectionParamsVersions.Set(ctx, successor.EffectiveEpoch, successor); err != nil {
		return err
	}
	if err := setVersionIndexEntry(
		ctx, k.SelectionParamsVersionIndex, successor.Version, successor.EffectiveEpoch,
	); err != nil {
		return err
	}
	if err := k.ScheduledSelectionParams.Remove(ctx, effectiveEpoch); err != nil {
		return types.ErrInvalidState.Wrapf(
			"scheduled selection parameters at epoch %d could not be consumed: %v", effectiveEpoch, err)
	}
	return nil
}

// SettlementWindowBlocks converts a target's configured window into a count of
// settlement-enabled blocks.
//
// The unit matters and is easy to get wrong: the result is measured in settlement
// CLOCK ticks, not chain heights. The clock does not advance while release is
// paused, so this is the number of blocks during which the chain was actually
// willing to move value — which is why a pause preserves a settlement's usable
// window rather than consuming it.
func (k Keeper) SettlementWindowBlocks(
	ctx context.Context, target uint64, params types.SettlementParamsVersion,
) (uint64, error) {
	epochLength, err := k.rewardsKeeper.EpochLengthForEpoch(ctx, target)
	if err != nil {
		return 0, types.ErrInvalidState.Wrapf(
			"epoch length for target %d could not be read: %v", target, err)
	}
	window, err := checked.MulUint64(params.SettlementWindowEpochs, epochLength)
	if err != nil {
		return 0, types.ErrInvalidState.Wrapf(
			"settlement window for target %d overflows: %d epochs of %d blocks",
			target, params.SettlementWindowEpochs, epochLength)
	}
	return window, nil
}

// DeadlineClock derives when a settlement's participant window closes.
//
// Derived from the epoch anchor plus the target-bound parameters, never stored. A
// stored copy would be a second authorization value that an import could set
// independently of the anchor it is supposed to follow from.
//
// An OPERATOR_ONLY settlement has no participant window to consume, so its deadline
// is the anchor clock itself and it is permissionlessly finalizable immediately.
func (k Keeper) DeadlineClock(
	ctx context.Context,
	settlement types.Settlement,
	anchor types.SettlementEpochAnchor,
	params types.SettlementParamsVersion,
) (uint64, error) {
	if settlement.SettlementMode == types.SettlementMode_SETTLEMENT_MODE_OPERATOR_ONLY {
		return anchor.CreatedSettlementClock, nil
	}
	window, err := k.SettlementWindowBlocks(ctx, settlement.Epoch, params)
	if err != nil {
		return 0, err
	}
	deadline, err := checked.AddUint64(anchor.CreatedSettlementClock, window)
	if err != nil {
		return 0, types.ErrInvalidState.Wrapf(
			"deadline for slot %d in epoch %d overflows the settlement clock",
			settlement.SlotId, settlement.Epoch)
	}
	return deadline, nil
}

// SettlementDeadlineClock resolves one settlement's participant deadline from
// state alone.
//
// Both inputs are resolved here rather than accepted from a caller. Passing them in
// would make it possible to derive a deadline from an anchor belonging to a
// different epoch, or from parameters bound to a different target, and the result
// would look like an ordinary number either way.
//
// The parameters the settlement RECORDS are then required to match the ones its
// epoch binds. History is immutable and a target's binding boundary has long since
// passed by the time anything asks for a deadline, so re-resolution must agree; a
// disagreement means the row and the history have come apart, and a deadline
// derived from either one alone would authorize or refuse a payout on state that no
// longer describes the obligation.
func (k Keeper) SettlementDeadlineClock(
	ctx context.Context, settlement types.Settlement,
) (uint64, error) {
	anchor, err := k.requireEpochAnchor(ctx, settlement.Epoch)
	if err != nil {
		return 0, err
	}
	params, err := k.SettlementParamsForTarget(ctx, settlement.Epoch)
	if err != nil {
		return 0, err
	}
	if params.Version != settlement.SettlementParamsVersion {
		return 0, types.ErrInvalidState.Wrapf(
			"the settlement for slot %d in epoch %d was created under settlement parameters version %d, "+
				"but epoch %d binds version %d",
			settlement.SlotId, settlement.Epoch, settlement.SettlementParamsVersion,
			settlement.Epoch, params.Version)
	}
	return k.DeadlineClock(ctx, settlement, anchor, params)
}

// ParticipantDistributionCeiling returns how much of an entitlement participant
// chunks may release.
//
// A TOTAL switch over every settlement mode, including the two this profile cannot
// reach. Totality is the point: the ceiling is a monetary authorization, and a
// default arm would silently answer for a mode nobody had thought about. Only the
// trusted arm is reachable here; the others exist so a later tranche adds a
// producer rather than a branch.
func ParticipantDistributionCeiling(
	settlement types.Settlement, entitlement rewardstypes.SlotEntitlement,
) (sdkmath.Int, error) {
	switch settlement.SettlementMode {
	case types.SettlementMode_SETTLEMENT_MODE_TRUSTED_AS,
		types.SettlementMode_SETTLEMENT_MODE_SELECTED_PARTICIPANTS:
		amount, err := entitlement.Amount()
		if err != nil {
			return sdkmath.Int{}, types.ErrInvalidState.Wrap(err.Error())
		}
		return amount, nil
	case types.SettlementMode_SETTLEMENT_MODE_OPERATOR_ONLY:
		return sdkmath.ZeroInt(), nil
	default:
		return sdkmath.Int{}, types.ErrInvalidState.Wrapf(
			"settlement for slot %d in epoch %d has no canonical settlement mode",
			settlement.SlotId, settlement.Epoch)
	}
}
