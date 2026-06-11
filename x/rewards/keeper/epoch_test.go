package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/twilight-project/twilight-core/x/rewards/keeper"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

func TestConfiguredEpochEndHeightUsesCurrentSnapshot(t *testing.T) {
	state := accountingState(4)
	state.CurrentEpochStartHeight = 100
	cfg := types.DefaultEpochConfigSnapshot(types.DefaultParams())
	cfg.EpochLengthBlocks = 20

	endHeight, err := keeper.ConfiguredEpochEndHeight(state, cfg)
	require.NoError(t, err)
	require.Equal(t, uint64(119), endHeight)

	latestParams := types.DefaultParams()
	latestParams.EpochLengthBlocks = 2
	require.NotEqual(t, latestParams.EpochLengthBlocks, cfg.EpochLengthBlocks)

	endHeightAfterParamChange, err := keeper.ConfiguredEpochEndHeight(state, cfg)
	require.NoError(t, err)
	require.Equal(t, uint64(119), endHeightAfterParamChange)
}

func TestShouldFinalizeAtHeightAndSettlementPause(t *testing.T) {
	state := accountingState(1)
	state.CurrentEpochStartHeight = 10
	cfg := types.DefaultEpochConfigSnapshot(types.DefaultParams())
	cfg.EpochLengthBlocks = 5

	shouldFinalize, err := keeper.ShouldFinalizeAtHeight(13, state, cfg, true)
	require.NoError(t, err)
	require.False(t, shouldFinalize)

	shouldFinalize, err = keeper.ShouldFinalizeAtHeight(14, state, cfg, true)
	require.NoError(t, err)
	require.True(t, shouldFinalize)

	shouldFinalize, err = keeper.ShouldFinalizeAtHeight(20, state, cfg, true)
	require.NoError(t, err)
	require.True(t, shouldFinalize)

	shouldFinalize, err = keeper.ShouldFinalizeAtHeight(20, state, cfg, false)
	require.NoError(t, err)
	require.False(t, shouldFinalize)

	shouldFinalize, err = keeper.ShouldFinalizeAtHeight(20, state, cfg, true)
	require.NoError(t, err)
	require.True(t, shouldFinalize)
}

func TestConfiguredEpochEndHeightRejectsInvalidInputs(t *testing.T) {
	cfg := types.DefaultEpochConfigSnapshot(types.DefaultParams())

	_, err := keeper.ConfiguredEpochEndHeight(types.RewardsState{}, cfg)
	require.Error(t, err)

	state := accountingState(1)
	cfg.EpochLengthBlocks = 0
	_, err = keeper.ConfiguredEpochEndHeight(state, cfg)
	require.Error(t, err)

	state.CurrentEpochStartHeight = ^uint64(0)
	cfg.EpochLengthBlocks = 2
	_, err = keeper.ConfiguredEpochEndHeight(state, cfg)
	require.Error(t, err)
}
