package keeper

import (
	"context"
	"fmt"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/x/rewards/types"
)

func (k Keeper) InitGenesis(ctx context.Context, genState types.GenesisState) error {
	if err := types.ValidateGenesis(genState); err != nil {
		return err
	}
	// Canonical economic-address preflight (§25) over the COMPLETE input, before
	// the first write.
	//
	// The writes below are sequential — params, state, epoch config, pending
	// params, then every finalized epoch and claim record. Each individual setter
	// now enforces the rule, so an invalid address is always caught; but caught in
	// the loop it would be caught after params and the earlier records had already
	// been persisted, leaving a partially imported module behind a returned error.
	// Checking everything first makes rejection total.
	if err := k.validateGenesisEconomicAddresses(genState); err != nil {
		return err
	}
	// The height-bearing half of fresh-genesis validation, which only a caller
	// that knows the chain's first block can decide. Run as part of the same
	// total preflight, before the first write.
	initialHeight, err := effectiveInitialHeight(ctx)
	if err != nil {
		return err
	}
	if err := types.ValidateFreshGenesisInitialHeight(&genState, initialHeight); err != nil {
		return err
	}
	if err := k.SetParams(ctx, *genState.Params); err != nil {
		return err
	}
	if err := k.SetState(ctx, *genState.State); err != nil {
		return err
	}
	if err := k.SetCurrentEpochConfig(ctx, *genState.CurrentEpochConfig); err != nil {
		return err
	}
	if err := k.initEpochHistoryGenesis(ctx, genState); err != nil {
		return err
	}
	if err := k.initRewardConfigGenesis(ctx, genState); err != nil {
		return err
	}
	if err := k.initEntitlementGenesis(ctx, genState); err != nil {
		return err
	}
	if err := k.SetPauseState(ctx, *genState.PauseState); err != nil {
		return err
	}
	// Written explicitly rather than left to a default. After genesis an absent
	// counter is corruption, and GetOpenRewardEnabledBlocks refuses to invent one.
	if err := k.SetOpenRewardEnabledBlocks(ctx, genState.OpenRewardEnabledBlocks); err != nil {
		return err
	}
	if genState.HasPendingParams {
		if err := k.SetPendingParams(ctx, *genState.PendingParams); err != nil {
			return err
		}
	}
	for _, epoch := range genState.FinalizedEpochs {
		if err := k.SetFinalizedEpoch(ctx, *epoch); err != nil {
			return err
		}
	}
	for _, reward := range genState.ClaimRecords {
		if err := k.SetClaimRecord(ctx, *reward); err != nil {
			return err
		}
	}
	return k.assertGenesisEscrowMatchesCarry(ctx, *genState.Params, *genState.State)
}

// assertGenesisEscrowMatchesCarry proves the chain starts solvent.
//
// The document-level rules already establish that a fresh chain owes nothing:
// no entitlements, no claim records, and a canonical zero liability. What the
// document cannot see is the bank, and the bank is where the other half of the
// equation lives — a genesis that funds the rewards escrow beyond its declared
// carry starts life holding value no obligation and no carry accounts for, and a
// genesis that underfunds it starts unable to pay the carry it declares.
//
// Either way the discrepancy is invisible until the first epoch closes, where
// finalization asserts
//
//	escrow == outstanding liability + carry
//
// and halts the block. That is the right behavior and the wrong moment: the defect
// is in the genesis document, and refusing it at InitChain means the chain never
// produces a block rather than producing epoch_length of them and then stopping.
//
// Reading the bank here is well-defined because the app initializes bank before
// rewards. The relation asserted is the same one finalization asserts, with the
// liability term known to be zero.
func (k Keeper) assertGenesisEscrowMatchesCarry(
	ctx context.Context, params types.Params, state types.RewardsState,
) error {
	carry, err := types.ParseAmountString("carry forward remainder", state.CarryForwardRemainder)
	if err != nil {
		return types.ErrInvalidGenesis.Wrap(err.Error())
	}
	address := k.accountKeeper.GetModuleAddress(types.ModuleName)
	if address == nil {
		return types.ErrInvalidGenesis.Wrap("rewards module account is missing")
	}
	balance := k.bankKeeper.GetBalance(ctx, address, params.NativeDenom).Amount
	if !balance.Equal(carry) {
		return types.ErrInvalidGenesis.Wrapf(
			"genesis funds the rewards escrow with %s%s but declares a carry-forward remainder of %s; "+
				"a fresh chain owes nothing else, so the two must be equal",
			balance, params.NativeDenom, carry)
	}
	return nil
}

// validateGenesisEconomicAddresses applies the canonical rule to every economic
// address the rewards genesis state would persist or later use.
//
// The inventory is the complete set of persisted address-bearing fields: the
// treasury destination of the active params, of any pending params, of the
// current epoch configuration, and of every canonical reward configuration and
// scheduled reward configuration; and, for each finalized epoch, the treasury
// destination inside its embedded configuration together with the operator and
// payout addresses of every embedded reward — plus the same pair on every
// standalone claim record.
func (k Keeper) validateGenesisEconomicAddresses(genState types.GenesisState) error {
	if genState.Params != nil {
		if err := k.validateParamsTreasury("genesis params", *genState.Params); err != nil {
			return err
		}
	}
	if genState.HasPendingParams && genState.PendingParams != nil {
		if err := k.validateParamsTreasury("genesis pending params", *genState.PendingParams); err != nil {
			return err
		}
	}
	if genState.CurrentEpochConfig != nil {
		if err := k.validateSnapshotTreasury("genesis current epoch config", *genState.CurrentEpochConfig); err != nil {
			return err
		}
	}
	for _, version := range genState.RewardConfigVersions {
		if version == nil {
			continue
		}
		if err := k.validateRewardConfigTreasury(
			fmt.Sprintf("genesis reward configuration version %d", version.Version), *version,
		); err != nil {
			return err
		}
	}
	for _, scheduled := range genState.ScheduledRewardConfigs {
		if scheduled == nil {
			continue
		}
		if err := k.validateScheduledRewardConfigTreasury(
			fmt.Sprintf("genesis scheduled reward configuration at epoch %d", scheduled.EffectiveEpoch), *scheduled,
		); err != nil {
			return err
		}
	}
	for _, epoch := range genState.FinalizedEpochs {
		if epoch == nil {
			continue
		}
		if err := k.validateFinalizedEpochAddresses(
			fmt.Sprintf("genesis finalized epoch %d", epoch.EpochNumber), *epoch,
		); err != nil {
			return err
		}
	}
	for _, reward := range genState.ClaimRecords {
		if reward == nil {
			continue
		}
		if err := k.validateRewardRecord(
			fmt.Sprintf("genesis claim record slot %d epoch %d", reward.SlotId, reward.EpochNumber), reward,
		); err != nil {
			return err
		}
	}
	for _, entitlement := range genState.SlotEntitlements {
		if entitlement == nil {
			continue
		}
		if err := k.validatePayoutDestination(
			fmt.Sprintf("genesis entitlement slot %d epoch %d", entitlement.SlotId, entitlement.Epoch),
			entitlement.PayoutAddress,
		); err != nil {
			return err
		}
	}
	return nil
}

func (k Keeper) ExportGenesis(ctx context.Context) (*types.GenesisState, error) {
	params, err := k.GetParams(ctx)
	if err != nil {
		return nil, err
	}
	state, err := k.GetState(ctx)
	if err != nil {
		return nil, err
	}
	config, err := k.GetCurrentEpochConfig(ctx)
	if err != nil {
		return nil, err
	}
	pauseState, err := k.GetPauseState(ctx)
	if err != nil {
		return nil, err
	}
	openBlocks, err := k.GetOpenRewardEnabledBlocks(ctx)
	if err != nil {
		return nil, err
	}
	liability, err := k.GetOutstandingEntitlementLiability(ctx)
	if err != nil {
		return nil, err
	}
	genesis := &types.GenesisState{
		Params:                          &params,
		State:                           &state,
		CurrentEpochConfig:              &config,
		PauseState:                      &pauseState,
		OpenRewardEnabledBlocks:         openBlocks,
		OutstandingEntitlementLiability: liability.String(),
	}
	if err := k.EpochConfigVersions.Walk(ctx, nil, func(_ uint64, version types.EpochConfigVersion) (bool, error) {
		value := version
		genesis.EpochConfigVersions = append(genesis.EpochConfigVersions, &value)
		return false, nil
	}); err != nil {
		return nil, err
	}
	if err := k.ScheduledEpochConfigs.Walk(ctx, nil, func(_ uint64, scheduled types.ScheduledEpochConfig) (bool, error) {
		value := scheduled
		genesis.ScheduledEpochConfigs = append(genesis.ScheduledEpochConfigs, &value)
		return false, nil
	}); err != nil {
		return nil, err
	}
	if err := k.RewardConfigVersions.Walk(ctx, nil, func(_ uint64, version types.RewardConfigVersion) (bool, error) {
		value := version
		genesis.RewardConfigVersions = append(genesis.RewardConfigVersions, &value)
		return false, nil
	}); err != nil {
		return nil, err
	}
	if err := k.ScheduledRewardConfigs.Walk(ctx, nil, func(_ uint64, scheduled types.ScheduledRewardConfig) (bool, error) {
		value := scheduled
		genesis.ScheduledRewardConfigs = append(genesis.ScheduledRewardConfigs, &value)
		return false, nil
	}); err != nil {
		return nil, err
	}
	if pending, found, err := k.GetPendingParams(ctx); err != nil {
		return nil, err
	} else if found {
		genesis.HasPendingParams = true
		genesis.PendingParams = &pending
	}
	if err := k.SlotEntitlements.Walk(ctx, nil,
		func(_ collections.Pair[uint64, uint64], entitlement types.SlotEntitlement) (bool, error) {
			value := entitlement
			genesis.SlotEntitlements = append(genesis.SlotEntitlements, &value)
			return false, nil
		}); err != nil {
		return nil, err
	}
	if err := k.FinalizedEpochs.Walk(ctx, nil, func(_ uint64, epoch types.EpochReward) (bool, error) {
		value := epoch
		genesis.FinalizedEpochs = append(genesis.FinalizedEpochs, &value)
		return false, nil
	}); err != nil {
		return nil, err
	}
	if err := k.ClaimRecords.Walk(ctx, nil, func(_ collections.Pair[uint64, uint64], reward types.EligibleSlotReward) (bool, error) {
		value := reward
		genesis.ClaimRecords = append(genesis.ClaimRecords, &value)
		return false, nil
	}); err != nil {
		return nil, err
	}
	return genesis, nil
}

// initEntitlementGenesis imports canonical entitlement state and the outstanding
// liability.
//
// The liability is written explicitly, exactly as the open reward-enabled counter
// is: after genesis an absent value is corruption, and no read path invents one.
func (k Keeper) initEntitlementGenesis(ctx context.Context, genState types.GenesisState) error {
	for _, entitlement := range genState.SlotEntitlements {
		if err := importGenesisCollection(
			ctx, k.SlotEntitlements, entitlementKey(entitlement.Epoch, entitlement.SlotId), *entitlement,
		); err != nil {
			return err
		}
	}
	liability, err := types.ParseAmountString(
		"outstanding entitlement liability", genState.OutstandingEntitlementLiability)
	if err != nil {
		return types.ErrInvalidGenesis.Wrap(err.Error())
	}
	return k.SetOutstandingEntitlementLiability(ctx, liability)
}

// initRewardConfigGenesis imports the canonical reward-configuration history and
// its schedule.
//
// Both collections route their duplicate/ordering treatment through
// importGenesisCollection, the single named seam described there.
func (k Keeper) initRewardConfigGenesis(ctx context.Context, genState types.GenesisState) error {
	for _, version := range genState.RewardConfigVersions {
		if err := importGenesisCollection(ctx, k.RewardConfigVersions, version.EffectiveEpoch, *version); err != nil {
			return err
		}
	}
	for _, scheduled := range genState.ScheduledRewardConfigs {
		if err := importGenesisCollection(ctx, k.ScheduledRewardConfigs, scheduled.EffectiveEpoch, *scheduled); err != nil {
			return err
		}
	}
	return nil
}

// initEpochHistoryGenesis imports the canonical epoch-configuration history and
// its schedule.
//
// Both collections route their duplicate/ordering treatment through
// importGenesisCollection, the single named seam described there.
func (k Keeper) initEpochHistoryGenesis(ctx context.Context, genState types.GenesisState) error {
	for _, version := range genState.EpochConfigVersions {
		if err := importGenesisCollection(ctx, k.EpochConfigVersions, version.EffectiveEpoch, *version); err != nil {
			return err
		}
	}
	for _, scheduled := range genState.ScheduledEpochConfigs {
		if err := importGenesisCollection(ctx, k.ScheduledEpochConfigs, scheduled.EffectiveEpoch, *scheduled); err != nil {
			return err
		}
	}
	return nil
}

// importGenesisCollection is the single seam through which every genesis
// collection introduced by the canonical epoch timeline is written.
//
// It exists because the general policy for malformed genesis collections —
// whether duplicate or non-canonically-ordered entries are rejected, normalized,
// or resolved last-wins — is an open architecture question that this module must
// not settle by accident. Routing every such write through one function makes the
// eventual ratified rule a one-place change instead of an audit of every import
// loop, and keeps the current behavior from hardening into protocol through
// repetition.
//
// Deliberately note what this does NOT do: it takes no position on that policy,
// and no test asserts one. The older finalized-epoch and claim-record collections
// predate this seam and carry their own long-standing rejection behavior, which
// is left untouched; that asymmetry is intentional while the question is open.
func importGenesisCollection[K, V any](
	ctx context.Context, collection collections.Map[K, V], key K, value V,
) error {
	return collection.Set(ctx, key, value)
}

// effectiveInitialHeight applies the SDK's initial-height convention: an absent
// or zero height means the chain starts at height 1, anything else is exact, and
// a negative height is refused.
func effectiveInitialHeight(ctx context.Context) (int64, error) {
	height := sdk.UnwrapSDKContext(ctx).BlockHeight()
	if height < 0 {
		return 0, types.ErrInvalidGenesis.Wrapf("initial height %d is negative", height)
	}
	if height == 0 {
		return 1, nil
	}
	return height, nil
}
