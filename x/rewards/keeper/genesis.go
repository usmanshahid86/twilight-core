package keeper

import (
	"context"

	"cosmossdk.io/collections"

	"github.com/twilight-project/twilight-core/x/rewards/types"
)

func (k Keeper) InitGenesis(ctx context.Context, genState types.GenesisState) error {
	if err := types.ValidateGenesis(genState); err != nil {
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
