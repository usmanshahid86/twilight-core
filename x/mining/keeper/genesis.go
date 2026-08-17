package keeper

import (
	"context"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	appparams "github.com/twilight-project/twilight-core/app/params"
	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	"github.com/twilight-project/twilight-core/x/mining/types"
)

// InitGenesis imports the mining histories and initializes the settlement clock
// and materialization cursor.
//
// The preflight runs to completion before the first write. Every rule that can
// reject this document is applied first — the pure document rules, then the
// cross-module rule that needs already-imported CoreSlot state, then the
// height-bearing rule — so a rejected genesis leaves no partially initialized
// module behind a returned error. A check interleaved with the writes would
// persist the mode history before discovering that an ACTIVE Slot's policy
// violates the Selection parameters it was supposed to satisfy.
func (k Keeper) InitGenesis(ctx context.Context, genState types.GenesisState) error {
	if err := types.ValidateGenesis(genState); err != nil {
		return err
	}
	initialHeight, err := genesisInitialHeight(ctx)
	if err != nil {
		return err
	}
	if err := k.validateActiveSlotPolicies(ctx, genState, initialHeight); err != nil {
		return err
	}

	for _, version := range genState.DistributionModeVersions {
		if err := importGenesisCollection(
			ctx, k.DistributionModeVersions, version.ValidFromEpoch, *version,
		); err != nil {
			return err
		}
	}
	for _, scheduled := range genState.ScheduledDistributionModes {
		if err := importGenesisCollection(
			ctx, k.ScheduledDistributionMode, scheduled.EffectiveEpoch, *scheduled,
		); err != nil {
			return err
		}
	}
	for _, version := range genState.SelectionParamsVersions {
		if err := importGenesisCollection(
			ctx, k.SelectionParamsVersions, version.EffectiveEpoch, *version,
		); err != nil {
			return err
		}
	}
	for _, scheduled := range genState.ScheduledSelectionParams {
		if err := importGenesisCollection(
			ctx, k.ScheduledSelectionParams, scheduled.EffectiveEpoch, *scheduled,
		); err != nil {
			return err
		}
	}
	for _, version := range genState.SettlementParamsVersions {
		if err := importGenesisCollection(
			ctx, k.SettlementParamsVersions, version.EffectiveEpoch, *version,
		); err != nil {
			return err
		}
	}
	for _, scheduled := range genState.ScheduledSettlementParams {
		if err := importGenesisCollection(
			ctx, k.ScheduledSettlementParams, scheduled.EffectiveEpoch, *scheduled,
		); err != nil {
			return err
		}
	}
	for _, anchor := range genState.SettlementEpochAnchors {
		if err := importGenesisCollection(
			ctx, k.SettlementEpochAnchors, anchor.Epoch, *anchor,
		); err != nil {
			return err
		}
	}
	for _, settlement := range genState.Settlements {
		if err := importGenesisCollection(
			ctx, k.Settlements, settlementKey(settlement.SlotId, settlement.Epoch), *settlement,
		); err != nil {
			return err
		}
	}

	// The clock and the cursor are written explicitly rather than left to a
	// default. After genesis an absent value is corruption, and no read path
	// invents one: a settlement clock that defaults to zero on a running chain
	// would reopen every expired deadline at once.
	if err := k.SettlementClock.Set(ctx, genState.SettlementClock); err != nil {
		return err
	}
	if err := k.LastProcessedRewardEpoch.Set(ctx, genState.LastProcessedRewardEpoch); err != nil {
		return err
	}

	return k.rebuildOpenSettlementIndex(ctx)
}

// validateActiveSlotPolicies is the cross-module admission rule x/mining owns.
//
// Canonical fresh genesis requires every ACTIVE CoreSlot's current Selection
// policy to satisfy the initial Selection parameters. The check lives here, and
// not in x/coreslot, because x/coreslot must gain no dependency on this module —
// the InitGenesis ordering exists precisely so that already-imported CoreSlot
// state is readable from here.
//
// It runs even though this profile executes no Selection. A genesis that admitted
// a policy exceeding the parameters would be a genesis whose Slots become
// unusable the moment Selection is switched on, and discovering that at the
// switch rather than at genesis is discovering it far too late.
func (k Keeper) validateActiveSlotPolicies(
	ctx context.Context, genState types.GenesisState, initialHeight int64,
) error {
	if len(genState.SelectionParamsVersions) == 0 || genState.SelectionParamsVersions[0] == nil {
		return types.ErrInvalidGenesis.Wrap("selection parameter history is required")
	}
	params := genState.SelectionParamsVersions[0]

	slots, err := k.coreSlotKeeper.GetActiveSlots(ctx)
	if err != nil {
		return types.ErrInvalidGenesis.Wrapf("active core slots could not be read: %v", err)
	}
	for _, slot := range slots {
		if slot.Status != coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE {
			continue
		}
		policy, err := k.coreSlotKeeper.SelectionPolicyAtHeight(ctx, slot.SlotId, initialHeight)
		if err != nil {
			return types.ErrInvalidGenesis.Wrapf(
				"selection policy for active slot %d could not be read at height %d: %v",
				slot.SlotId, initialHeight, err)
		}
		if err := appparams.ValidateSelectionRateBps(
			policy.SelectionRateBps, params.MaxSelectionRateBps,
		); err != nil {
			return types.ErrInvalidGenesis.Wrapf("active slot %d selection policy: %v", slot.SlotId, err)
		}
		if policy.MaxSelectedParticipants > params.MaxSelectedParticipantsPerSelection {
			return types.ErrInvalidGenesis.Wrapf(
				"active slot %d selects up to %d participants, above the initial selection parameter maximum of %d",
				slot.SlotId, policy.MaxSelectedParticipants, params.MaxSelectedParticipantsPerSelection)
		}
	}
	return nil
}

// rebuildOpenSettlementIndex derives the OPEN index from the canonical rows that
// were just imported.
//
// It is rebuilt rather than imported, and that is what makes it impossible for a
// genesis document to ship an index disagreeing with its own settlements: there
// is no second value to disagree with. At fresh genesis the settlement collection
// is empty and this writes nothing, but the seam is the one a continuation
// importer would reuse and is written now so the index never acquires a genesis
// field later.
func (k Keeper) rebuildOpenSettlementIndex(ctx context.Context) error {
	return k.Settlements.Walk(ctx, nil,
		func(key collections.Pair[uint64, uint64], settlement types.Settlement) (bool, error) {
			if err := validateSettlementRecord(key, settlement); err != nil {
				return true, err
			}
			if settlement.Finalized {
				return false, nil
			}
			if err := k.OpenSettlementsBySlot.Set(ctx, key, settlement.Epoch); err != nil {
				return true, err
			}
			return false, nil
		})
}

// validateSettlementRecord checks a stored settlement against the key it was
// found under and against its own invariants.
//
// The key check is not redundant with the record's own fields. A row that
// declares a different (slot, epoch) than the key it lives at would authorize
// work against one obligation while being reached through another, and no later
// check would notice because both halves are internally consistent.
func validateSettlementRecord(key collections.Pair[uint64, uint64], settlement types.Settlement) error {
	if settlement.SlotId != key.K1() || settlement.Epoch != key.K2() {
		return types.ErrInvalidState.Wrapf(
			"settlement stored at slot %d epoch %d declares slot %d epoch %d",
			key.K1(), key.K2(), settlement.SlotId, settlement.Epoch)
	}
	return settlement.Validate()
}

func settlementKey(slotID, epoch uint64) collections.Pair[uint64, uint64] {
	return collections.Join(slotID, epoch)
}

// ExportGenesis writes the canonical mining state back out.
//
// The derived OPEN index is deliberately absent from the exported document, for
// the same reason it is absent from the imported one: it is reconstructible from
// the settlements beside it, and a document that could state it separately would
// be a document that could state it wrongly.
func (k Keeper) ExportGenesis(ctx context.Context) (*types.GenesisState, error) {
	clock, err := k.SettlementClock.Get(ctx)
	if err != nil {
		return nil, types.ErrInvalidState.Wrapf("settlement clock could not be read: %v", err)
	}
	cursor, err := k.LastProcessedRewardEpoch.Get(ctx)
	if err != nil {
		return nil, types.ErrInvalidState.Wrapf("processed reward epoch cursor could not be read: %v", err)
	}
	genesis := &types.GenesisState{SettlementClock: clock, LastProcessedRewardEpoch: cursor}

	if err := k.DistributionModeVersions.Walk(ctx, nil,
		func(_ uint64, version types.MiningDistributionModeVersion) (bool, error) {
			value := version
			genesis.DistributionModeVersions = append(genesis.DistributionModeVersions, &value)
			return false, nil
		}); err != nil {
		return nil, err
	}
	if err := k.ScheduledDistributionMode.Walk(ctx, nil,
		func(_ uint64, scheduled types.ScheduledMiningDistributionMode) (bool, error) {
			value := scheduled
			genesis.ScheduledDistributionModes = append(genesis.ScheduledDistributionModes, &value)
			return false, nil
		}); err != nil {
		return nil, err
	}
	if err := k.SelectionParamsVersions.Walk(ctx, nil,
		func(_ uint64, version types.SelectionParamsVersion) (bool, error) {
			value := version
			genesis.SelectionParamsVersions = append(genesis.SelectionParamsVersions, &value)
			return false, nil
		}); err != nil {
		return nil, err
	}
	if err := k.ScheduledSelectionParams.Walk(ctx, nil,
		func(_ uint64, scheduled types.ScheduledSelectionParams) (bool, error) {
			value := scheduled
			genesis.ScheduledSelectionParams = append(genesis.ScheduledSelectionParams, &value)
			return false, nil
		}); err != nil {
		return nil, err
	}
	if err := k.SettlementParamsVersions.Walk(ctx, nil,
		func(_ uint64, version types.SettlementParamsVersion) (bool, error) {
			value := version
			genesis.SettlementParamsVersions = append(genesis.SettlementParamsVersions, &value)
			return false, nil
		}); err != nil {
		return nil, err
	}
	if err := k.ScheduledSettlementParams.Walk(ctx, nil,
		func(_ uint64, scheduled types.ScheduledSettlementParams) (bool, error) {
			value := scheduled
			genesis.ScheduledSettlementParams = append(genesis.ScheduledSettlementParams, &value)
			return false, nil
		}); err != nil {
		return nil, err
	}
	if err := k.SettlementEpochAnchors.Walk(ctx, nil,
		func(_ uint64, anchor types.SettlementEpochAnchor) (bool, error) {
			value := anchor
			genesis.SettlementEpochAnchors = append(genesis.SettlementEpochAnchors, &value)
			return false, nil
		}); err != nil {
		return nil, err
	}
	if err := k.Settlements.Walk(ctx, nil,
		func(_ collections.Pair[uint64, uint64], settlement types.Settlement) (bool, error) {
			value := settlement
			genesis.Settlements = append(genesis.Settlements, &value)
			return false, nil
		}); err != nil {
		return nil, err
	}
	return genesis, nil
}

// importGenesisCollection is the single seam through which every mining genesis
// collection is written.
//
// It exists because the general policy for malformed genesis collections —
// whether duplicate or non-canonically-ordered entries are rejected, normalized,
// or resolved last-wins — is an open architecture question this module must not
// settle by accident. Routing every such write through one function makes the
// eventual ratified rule a one-place change instead of an audit of eight import
// loops, and keeps the current behavior from hardening into protocol through
// repetition.
//
// Deliberately note what this does NOT do: it takes no position on that policy,
// and no test asserts one. Canonically determined content rules — an empty
// schedule, a zero clock, an empty settlement collection — are enforced in
// document validation and are unaffected by the open question.
func importGenesisCollection[K, V any](
	ctx context.Context, collection collections.Map[K, V], key K, value V,
) error {
	return collection.Set(ctx, key, value)
}

// genesisInitialHeight applies the SDK's initial-height convention: an absent or
// zero height means the chain starts at height 1, anything else is exact, and a
// negative height is refused.
func genesisInitialHeight(ctx context.Context) (int64, error) {
	height := sdk.UnwrapSDKContext(ctx).BlockHeight()
	if height < 0 {
		return 0, types.ErrInvalidGenesis.Wrapf("initial height %d is negative", height)
	}
	if height == 0 {
		return 1, nil
	}
	return height, nil
}
