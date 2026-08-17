package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	appparams "github.com/twilight-project/twilight-core/app/params"
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

// TestShouldFinalizeAtHeightUsesCanonicalHistory replaces the retired
// settlement-pause gate.
//
// Two properties are asserted together because they are the same decision: the
// boundary comes from EpochConfigVersion history rather than a passed-in
// snapshot, and reaching it finalizes regardless of the pause state.
//
// The configured geometry is the ratified minimum rather than a toy length,
// because this is a block-path admission decision: an epoch of 5 blocks could
// never exist on a chain, so proving the comparison against one would prove it
// only for a configuration consensus refuses.
func TestShouldFinalizeAtHeightUsesCanonicalHistory(t *testing.T) {
	params := types.DefaultParams()
	params.EpochLengthBlocks = appparams.HardMinEpochLengthBlocks
	core := &coreSlotKeeperMock{}
	k, ctx, _ := setupAccountingKeeper(t, core, 1, params)

	// accountingState opens epoch 1 at height 1, so with length 360 the epoch runs
	// heights 1..360.
	for _, tc := range []struct {
		height uint64
		want   bool
	}{
		{height: 359, want: false},
		{height: 360, want: true},
	} {
		got, err := k.ShouldFinalizeAtHeight(ctx, tc.height)
		require.NoError(t, err)
		require.Equalf(t, tc.want, got, "height %d", tc.height)
	}

	// Past the boundary is not "still ready". The closing block already committed
	// without finalizing, so a later block cannot pick the transition up.
	_, err := k.ShouldFinalizeAtHeight(ctx, 361)
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Contains(t, err.Error(), "due to finalize at height 360")

	// A pause does not move the boundary. Epoch time continues while paused, and
	// gating finalization here would strand the epoch permanently once the next
	// one opens at BeginBlock.
	require.NoError(t, k.SetPauseState(ctx, types.RewardsPauseState{CurrentPaused: true}))
	got, err := k.ShouldFinalizeAtHeight(ctx, 360)
	require.NoError(t, err)
	require.True(t, got, "a paused epoch still reaches its canonical boundary")
}

// TestEpochGeometryComesFromHistoryNotTheDeprecatedMirror proves the snapshot
// mirror is inert: corrupting it cannot move a boundary.
func TestEpochGeometryComesFromHistoryNotTheDeprecatedMirror(t *testing.T) {
	params := types.DefaultParams()
	params.EpochLengthBlocks = appparams.HardMinEpochLengthBlocks
	k, ctx, _ := setupAccountingKeeper(t, &coreSlotKeeperMock{}, 1, params)

	end, err := k.EpochEndHeight(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(360), end)

	cfg, err := k.GetCurrentEpochConfig(ctx)
	require.NoError(t, err)
	cfg.EpochLengthBlocks = 999
	require.NoError(t, k.SetCurrentEpochConfig(ctx, cfg))

	end, err = k.EpochEndHeight(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(360), end, "the deprecated mirror must not influence canonical geometry")
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
