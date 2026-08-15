package keeper

import (
	"context"
	"fmt"

	"cosmossdk.io/collections"

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
	if err := k.SetParams(ctx, *genState.Params); err != nil {
		return err
	}
	if err := k.SetState(ctx, *genState.State); err != nil {
		return err
	}
	if err := k.SetCurrentEpochConfig(ctx, *genState.CurrentEpochConfig); err != nil {
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
	return nil
}

// validateGenesisEconomicAddresses applies the canonical rule to every economic
// address the rewards genesis state would persist or later use.
//
// The inventory is the complete set of persisted address-bearing fields: the
// treasury destination of the active params, of any pending params, and of the
// current epoch configuration; and, for each finalized epoch, the treasury
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
		if err := k.validateRewardAddresses(
			fmt.Sprintf("genesis claim record slot %d epoch %d", reward.SlotId, reward.EpochNumber), reward,
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
	genesis := &types.GenesisState{Params: &params, State: &state, CurrentEpochConfig: &config}
	if pending, found, err := k.GetPendingParams(ctx); err != nil {
		return nil, err
	} else if found {
		genesis.HasPendingParams = true
		genesis.PendingParams = &pending
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
