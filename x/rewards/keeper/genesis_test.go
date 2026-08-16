package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	appparams "github.com/twilight-project/twilight-core/app/params"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

func TestDefaultGenesisInitExportRoundTripWithoutBankCalls(t *testing.T) {
	k, ctx, bank := setupKeeper(t, &coreSlotKeeperMock{})
	genesis := types.DefaultGenesis()
	require.NoError(t, k.InitGenesis(ctx, *genesis))
	exported, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Equal(t, genesis, exported)
	require.Zero(t, bank.mintCalls)
	require.Zero(t, bank.sendCalls)
}

func TestPopulatedGenesisInitExportRoundTrip(t *testing.T) {
	k, ctx, bank := setupKeeper(t, &coreSlotKeeperMock{})
	genesis := types.DefaultGenesis()
	pending := types.DefaultParams()
	pending.MaxClaimEpochsPerTx++
	genesis.HasPendingParams = true
	genesis.PendingParams = &pending
	epoch := validEpoch(1, *genesis.Params)
	claim := validClaim(1, 1)
	genesis.FinalizedEpochs = []*types.EpochReward{&epoch}
	genesis.ClaimRecords = []*types.EligibleSlotReward{&claim}

	require.NoError(t, k.InitGenesis(ctx, *genesis))
	exported, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Equal(t, genesis, exported)
	require.Zero(t, bank.mintCalls)
	require.Zero(t, bank.sendCalls)
}

func TestGenesisValidationBoundaries(t *testing.T) {
	t.Run("invalid denom", func(t *testing.T) {
		genesis := types.DefaultGenesis()
		genesis.Params.NativeDenom = appparams.NativeDisplayDenom
		require.Error(t, genesis.Validate())
	})

	t.Run("cumulative at cap accepted", func(t *testing.T) {
		genesis := types.DefaultGenesis()
		genesis.State.CumulativeEmitted = genesis.Params.MaxSupply
		require.NoError(t, genesis.Validate())
	})

	t.Run("cumulative above cap rejected", func(t *testing.T) {
		genesis := types.DefaultGenesis()
		genesis.State.CumulativeEmitted = "21000000000001"
		require.Error(t, genesis.Validate())
	})

	t.Run("duplicate finalized epoch", func(t *testing.T) {
		genesis := types.DefaultGenesis()
		epoch := validEpoch(1, *genesis.Params)
		genesis.FinalizedEpochs = []*types.EpochReward{&epoch, &epoch}
		require.Error(t, genesis.Validate())
	})

	t.Run("duplicate claim record", func(t *testing.T) {
		genesis := types.DefaultGenesis()
		epoch := validEpoch(1, *genesis.Params)
		claim := validClaim(1, 1)
		genesis.FinalizedEpochs = []*types.EpochReward{&epoch}
		genesis.ClaimRecords = []*types.EligibleSlotReward{&claim, &claim}
		require.Error(t, genesis.Validate())
	})

	t.Run("claim without finalized epoch", func(t *testing.T) {
		genesis := types.DefaultGenesis()
		claim := validClaim(1, 1)
		genesis.ClaimRecords = []*types.EligibleSlotReward{&claim}
		require.Error(t, genesis.Validate())
	})
}
