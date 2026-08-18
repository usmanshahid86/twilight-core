package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"

	"github.com/twilight-project/twilight-core/internal/checked"
	"github.com/twilight-project/twilight-core/x/mining/types"
)

// Settlement clock, epoch anchoring, and deterministic materialization.
//
// Nothing in this file moves value. It cannot: x/mining holds no bank keeper. That
// is the structural form of the architecture's rule that consensus performs no
// automatic payout — there is no automatic payout because there is nothing to pay
// with.

// GetSettlementClock reads the canonical settlement clock.
//
// There is deliberately no default. Genesis writes it explicitly, so after
// initialization an absent value is corruption: defaulting a missing clock to zero
// on a running chain would reset every derived deadline and reopen participant
// windows that had already closed.
func (k Keeper) GetSettlementClock(ctx context.Context) (uint64, error) {
	clock, err := k.SettlementClock.Get(ctx)
	if err != nil {
		return 0, types.ErrInvalidState.Wrapf("settlement clock could not be read: %v", err)
	}
	return clock, nil
}

// GetLastProcessedRewardEpoch reads the materialization cursor, on the same terms.
func (k Keeper) GetLastProcessedRewardEpoch(ctx context.Context) (uint64, error) {
	cursor, err := k.LastProcessedRewardEpoch.Get(ctx)
	if err != nil {
		return 0, types.ErrInvalidState.Wrapf(
			"processed reward epoch cursor could not be read: %v", err)
	}
	return cursor, nil
}

// advanceSettlementClock ticks the clock once if this block permits release.
//
// The pause state consulted is the one effective at the BEGINNING of the block. In
// x/rewards a pause accepted in block H is scheduled for H+1 and applied at that
// block's BeginBlock, so the already-applied value read here at EndBlock is exactly
// the beginning-of-block state, whatever transactions ran in between. That is what
// makes the tick independent of where in the block a pause transaction landed.
func (k Keeper) advanceSettlementClock(ctx context.Context) error {
	enabled, err := k.rewardsKeeper.SettlementReleaseEnabled(ctx)
	if err != nil {
		return types.ErrInvalidState.Wrapf("settlement release state could not be read: %v", err)
	}
	if !enabled {
		return nil
	}
	clock, err := k.GetSettlementClock(ctx)
	if err != nil {
		return err
	}
	next, err := checked.AddUint64(clock, 1)
	if err != nil {
		return types.ErrInvalidState.Wrap("the settlement clock is exhausted")
	}
	return k.SettlementClock.Set(ctx, next)
}

// materializeFinalizedEpoch creates the complete settlement set for whichever
// reward epoch closed in this block, if any.
//
// # Discovery is O(1)
//
// At most one epoch can finalize per block and this runs every block, so the only
// epoch that can need materializing is the one after the cursor. Looking it up
// directly avoids any scan whose cost would track how many epochs the chain has
// closed.
//
// The check for a SECOND pending epoch afterwards is not defensive padding. If one
// exists, materialization has fallen behind, and an epoch that gets materialized
// later would be anchored to a settlement clock from the wrong block — which
// silently moves every deadline it derives. Falling behind is corruption, not a
// backlog to drain.
//
// # All or nothing
//
// The cursor advances only together with the complete set. There is no partial
// materialization to resume: the caller's cache discards everything on any failure,
// so a block either creates every settlement that epoch owes or fails.
func (k Keeper) materializeFinalizedEpoch(ctx context.Context) (materialized uint64, err error) {
	cursor, err := k.GetLastProcessedRewardEpoch(ctx)
	if err != nil {
		return 0, err
	}
	target, err := checked.AddUint64(cursor, 1)
	if err != nil {
		return 0, types.ErrInvalidState.Wrap("the processed reward epoch cursor is exhausted")
	}
	finalized, found, err := k.rewardsKeeper.GetFinalizedEpoch(ctx, target)
	if err != nil {
		return 0, types.ErrInvalidState.Wrapf(
			"finalized reward epoch %d could not be read: %v", target, err)
	}
	if !found {
		// Absence is the ordinary answer, but it is only ordinary if nothing LATER
		// is finalized. A gap — target absent while its immediate successor exists —
		// means the finalized history and this cursor have come apart, and a
		// later-materialized epoch would be anchored to the wrong settlement clock.
		//
		// The probe is bounded to exactly one epoch. It is not a search for how far
		// behind the cursor is: any gap at all is corruption, so one lookup either
		// proves there is none or proves there is.
		if err := k.requireNoFinalizedSuccessor(ctx, target); err != nil {
			return 0, err
		}
		return 0, nil
	}
	// The row must be the row it was filed under. A record stored at key N that
	// declares a different epoch is not proof that N finalized, and every quantity
	// below — the entitlement set, the anchor, the cursor — would be attributed to
	// an epoch that never closed.
	if finalized.EpochNumber != target {
		return 0, types.ErrInvalidState.Wrapf(
			"the finalized reward epoch stored at epoch %d declares epoch %d",
			target, finalized.EpochNumber)
	}

	entitlements, err := k.rewardsKeeper.IterateEntitlementsForEpoch(ctx, target)
	if err != nil {
		return 0, types.ErrInvalidState.Wrapf(
			"entitlements for epoch %d could not be read: %v", target, err)
	}

	created := 0
	if len(entitlements) > 0 {
		// The configuration governing this target, resolved ONCE for the whole set.
		// Both bind at the target's own boundary, so they are properties of the epoch
		// rather than of any row in it; resolving per entitlement would repeat one
		// history seek for every participating Slot.
		mode, err := k.DistributionModeForTarget(ctx, target)
		if err != nil {
			return 0, err
		}
		params, err := k.SettlementParamsForTarget(ctx, target)
		if err != nil {
			return 0, err
		}
		settlementMode, err := resolveSettlementMode(mode)
		if err != nil {
			return 0, err
		}
		for _, entitlement := range entitlements {
			if entitlement.SlotId == 0 || entitlement.Epoch != target {
				return 0, types.ErrInvalidState.Wrapf(
					"epoch %d yielded an entitlement for slot %d in epoch %d",
					target, entitlement.SlotId, entitlement.Epoch)
			}
			settlement := types.Settlement{
				SlotId:                  entitlement.SlotId,
				Epoch:                   target,
				DistributionModeVersion: mode.Version,
				SettlementMode:          settlementMode,
				SettlementParamsVersion: params.Version,
				NextChunkIndex:          0,
				Finalized:               false,
				FinalizedHeight:         0,
				FinalizationReason:      types.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_UNSPECIFIED,
			}
			if err := k.createSettlement(ctx, settlement); err != nil {
				return 0, err
			}
			created++
		}
	}

	if created > 0 {
		clock, err := k.GetSettlementClock(ctx)
		if err != nil {
			return 0, err
		}
		if err := k.createEpochAnchor(ctx, types.SettlementEpochAnchor{
			Epoch:                  target,
			CreatedSettlementClock: clock,
		}); err != nil {
			return 0, err
		}
	}

	if err := k.LastProcessedRewardEpoch.Set(ctx, target); err != nil {
		return 0, err
	}

	// Having caught up, nothing further may be waiting.
	if err := k.requireNoFinalizedSuccessor(ctx, target); err != nil {
		return 0, err
	}
	return target, nil
}

// requireNoFinalizedSuccessor proves nothing is finalized immediately past an
// epoch, in exactly one lookup.
//
// Both callers need the same guarantee for the same reason, and the reason is not
// tidiness: an epoch materialized in a later block than the one that closed it
// would be anchored to that later block's settlement clock, silently moving every
// deadline derived from it. Falling behind is corruption, not a backlog to drain —
// which is why this probes one epoch and never scans.
func (k Keeper) requireNoFinalizedSuccessor(ctx context.Context, epoch uint64) error {
	successor, err := checked.AddUint64(epoch, 1)
	if err != nil {
		return types.ErrInvalidState.Wrap("the processed reward epoch cursor is exhausted")
	}
	found, err := k.finalizedEpochExists(ctx, successor)
	if err != nil {
		return err
	}
	if found {
		return types.ErrInvalidState.Wrapf(
			"reward epoch %d is finalized while epoch %d is not yet materialized; "+
				"materialization has fallen behind and later anchors would carry the wrong settlement clock",
			successor, epoch)
	}
	return nil
}

// finalizedEpochExists reports whether an epoch is finalized, holding the stored
// row to the key it was found under.
//
// The identity check is not redundant with the caller's: a successor probe that
// accepted a mismatched row would let a misfiled record answer "yes, later work is
// waiting" for an epoch that never closed, halting the chain on a lie.
func (k Keeper) finalizedEpochExists(ctx context.Context, epoch uint64) (bool, error) {
	finalized, found, err := k.rewardsKeeper.GetFinalizedEpoch(ctx, epoch)
	if err != nil {
		return false, types.ErrInvalidState.Wrapf(
			"finalized reward epoch %d could not be read: %v", epoch, err)
	}
	if !found {
		return false, nil
	}
	if finalized.EpochNumber != epoch {
		return false, types.ErrInvalidState.Wrapf(
			"the finalized reward epoch stored at epoch %d declares epoch %d",
			epoch, finalized.EpochNumber)
	}
	return true, nil
}

// resolveSettlementMode is the single canonical rule mapping a target's bound
// distribution mode onto the settlement mode its rows are created under.
//
// In this profile it consults ONLY the target-bound distribution mode. It must not
// read Selection commitments, results, candidate state or beacon state — none of
// which exist here — and a target bound to protocol Selection is refused outright
// rather than guessed at. A later tranche extends this one rule to produce the
// other two arms; it does not restructure Settlement to do so.
func resolveSettlementMode(mode types.MiningDistributionModeVersion) (types.SettlementMode, error) {
	switch mode.Mode {
	case types.MiningDistributionMode_MINING_DISTRIBUTION_MODE_TRUSTED_AS_DISTRIBUTION:
		return types.SettlementMode_SETTLEMENT_MODE_TRUSTED_AS, nil
	case types.MiningDistributionMode_MINING_DISTRIBUTION_MODE_PROTOCOL_SELECTION:
		return types.SettlementMode_SETTLEMENT_MODE_UNSPECIFIED, types.ErrUnsupportedFeature.Wrapf(
			"distribution mode version %d selects protocol selection, which has no producer in this profile",
			mode.Version)
	default:
		return types.SettlementMode_SETTLEMENT_MODE_UNSPECIFIED, types.ErrInvalidState.Wrapf(
			"distribution mode version %d has no canonical mode", mode.Version)
	}
}

// createSettlement writes one new settlement row and its OPEN index entry.
//
// Write-once by explicit check. A settlement is an obligation workflow over an
// entitlement that can be released exactly once; overwriting one would reset
// next_chunk_index and re-authorize chunks that were already paid.
func (k Keeper) createSettlement(ctx context.Context, settlement types.Settlement) error {
	if err := settlement.Validate(); err != nil {
		return err
	}
	key := settlementKey(settlement.SlotId, settlement.Epoch)
	exists, err := k.Settlements.Has(ctx, key)
	if err != nil {
		return types.ErrInvalidState.Wrapf(
			"settlement for slot %d in epoch %d could not be checked: %v",
			settlement.SlotId, settlement.Epoch, err)
	}
	if exists {
		return types.ErrInvalidState.Wrapf(
			"a settlement for slot %d in epoch %d already exists",
			settlement.SlotId, settlement.Epoch)
	}
	if err := k.Settlements.Set(ctx, key, settlement); err != nil {
		return err
	}
	return k.OpenSettlementsBySlot.Set(ctx, key, settlement.Epoch)
}

// createEpochAnchor writes the single anchor for an epoch that produced
// settlements.
//
// Write-once, because the anchor is the sole record of when that epoch's
// settlements were created and every deadline they derive follows from it. A second
// anchor would move deadlines that were already in force.
func (k Keeper) createEpochAnchor(ctx context.Context, anchor types.SettlementEpochAnchor) error {
	if err := anchor.Validate(); err != nil {
		return err
	}
	exists, err := k.SettlementEpochAnchors.Has(ctx, anchor.Epoch)
	if err != nil {
		return types.ErrInvalidState.Wrapf(
			"settlement epoch anchor for epoch %d could not be checked: %v", anchor.Epoch, err)
	}
	if exists {
		return types.ErrInvalidState.Wrapf(
			"a settlement epoch anchor already exists for epoch %d", anchor.Epoch)
	}
	return k.SettlementEpochAnchors.Set(ctx, anchor.Epoch, anchor)
}

// GetSettlement returns one canonical settlement, reporting whether it exists.
//
// Absence is ordinary: most (slot, epoch) pairs never produce one, because most
// Slots earn nothing in most epochs. Anything else is corruption and never presents
// as absence — a handler that read a decode failure as "no settlement" would refuse
// a legitimate payout, and one that read it as an empty record would authorize a
// chunk against next_chunk_index zero.
func (k Keeper) GetSettlement(ctx context.Context, slotID, epoch uint64) (types.Settlement, bool, error) {
	if slotID == 0 || epoch == 0 {
		return types.Settlement{}, false, types.ErrInvalidState.Wrap(
			"settlement lookup requires a nonzero slot and epoch")
	}
	key := settlementKey(slotID, epoch)
	settlement, err := k.Settlements.Get(ctx, key)
	if errors.Is(err, collections.ErrNotFound) {
		return types.Settlement{}, false, nil
	}
	if err != nil {
		return types.Settlement{}, false, types.ErrInvalidState.Wrapf(
			"settlement for slot %d in epoch %d could not be read: %v", slotID, epoch, err)
	}
	if err := validateSettlementRecord(key, settlement); err != nil {
		return types.Settlement{}, false, err
	}
	return settlement, true, nil
}

// GetSettlementEpochAnchor returns the anchor for an epoch, reporting whether one
// exists.
//
// Absence is ordinary for an epoch that produced no settlements. For an epoch that
// DID produce settlements it is corruption, and the caller that holds a settlement
// is the one positioned to say so — see requireEpochAnchor.
func (k Keeper) GetSettlementEpochAnchor(
	ctx context.Context, epoch uint64,
) (types.SettlementEpochAnchor, bool, error) {
	if epoch == 0 {
		return types.SettlementEpochAnchor{}, false, types.ErrInvalidState.Wrap("epoch numbers start at 1")
	}
	anchor, err := k.SettlementEpochAnchors.Get(ctx, epoch)
	if errors.Is(err, collections.ErrNotFound) {
		return types.SettlementEpochAnchor{}, false, nil
	}
	if err != nil {
		return types.SettlementEpochAnchor{}, false, types.ErrInvalidState.Wrapf(
			"settlement epoch anchor for epoch %d could not be read: %v", epoch, err)
	}
	if anchor.Epoch != epoch {
		return types.SettlementEpochAnchor{}, false, types.ErrInvalidState.Wrapf(
			"settlement epoch anchor stored at epoch %d declares epoch %d", epoch, anchor.Epoch)
	}
	if err := anchor.Validate(); err != nil {
		return types.SettlementEpochAnchor{}, false, err
	}
	return anchor, true, nil
}

// requireEpochAnchor resolves the anchor a settlement depends on, treating absence
// as corruption.
//
// Reached only with a settlement in hand, and a settlement's existence proves its
// epoch produced at least one — the two are created in the same transition. A
// missing anchor here therefore means the pair came apart, and every deadline
// derived from it would be invented.
func (k Keeper) requireEpochAnchor(
	ctx context.Context, epoch uint64,
) (types.SettlementEpochAnchor, error) {
	anchor, found, err := k.GetSettlementEpochAnchor(ctx, epoch)
	if err != nil {
		return types.SettlementEpochAnchor{}, err
	}
	if !found {
		return types.SettlementEpochAnchor{}, types.ErrInvalidState.Wrapf(
			"epoch %d has settlements but no settlement epoch anchor", epoch)
	}
	return anchor, nil
}
