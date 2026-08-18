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

// TestPopulatedGenesisInitExportRoundTrip round-trips everything a fresh genesis
// is allowed to carry beyond the defaults.
//
// It used to seed a finalized epoch and a claim record. Fresh genesis now refuses
// closed-epoch state in either representation, so what remains variable is the
// pending-params pair and an explicitly paused start — which is the whole of the
// optional fresh-genesis surface, and still enough to catch an import that drops
// a field or an export that invents one.
func TestPopulatedGenesisInitExportRoundTrip(t *testing.T) {
	k, ctx, bank := setupKeeper(t, &coreSlotKeeperMock{})
	genesis := types.DefaultGenesis()
	pending := types.DefaultParams()
	pending.MaxClaimEpochsPerTx++
	genesis.HasPendingParams = true
	genesis.PendingParams = &pending
	genesis.PauseState = &types.RewardsPauseState{CurrentPaused: true}

	require.NoError(t, k.InitGenesis(ctx, *genesis))
	exported, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Equal(t, genesis, exported)
	require.Zero(t, bank.mintCalls)
	require.Zero(t, bank.sendCalls)
}

// TestInitGenesisNormalizesTheCanonicalPauseState is the module-level half of the
// ratified fresh-genesis pause rule: the import itself refuses a pending
// transition, and refuses it before writing anything.
func TestInitGenesisNormalizesTheCanonicalPauseState(t *testing.T) {
	t.Run("an explicit paused start is imported verbatim", func(t *testing.T) {
		k, ctx, _ := setupKeeper(t, &coreSlotKeeperMock{})
		genesis := types.DefaultGenesis()
		genesis.PauseState = &types.RewardsPauseState{CurrentPaused: true}
		require.NoError(t, k.InitGenesis(ctx, *genesis))

		state, err := k.GetPauseState(ctx)
		require.NoError(t, err)
		require.True(t, state.CurrentPaused)
		require.False(t, state.HasPending)
		require.Zero(t, state.PendingEffectiveHeight)
	})

	for _, tc := range []struct {
		name   string
		height uint64
	}{
		{name: "due at the initial height", height: 1},
		{name: "due at the initial height plus one", height: 2},
		{name: "due far in the future", height: 10_000},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx, _ := setupKeeper(t, &coreSlotKeeperMock{})
			genesis := types.DefaultGenesis()
			genesis.PauseState = &types.RewardsPauseState{
				HasPending: true, PendingValue: true, PendingEffectiveHeight: tc.height,
			}
			require.Error(t, k.InitGenesis(ctx, *genesis))
		})
	}
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
}
