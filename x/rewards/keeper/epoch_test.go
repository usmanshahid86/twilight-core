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

// TestShouldFinalizeAtHeightUsesCanonicalHistory replaces the retired
// settlement-pause gate.
//
// Two properties are asserted together because they are the same decision: the
// boundary comes from EpochConfigVersion history rather than a passed-in
// snapshot, and reaching it finalizes regardless of the pause state.
func TestShouldFinalizeAtHeightUsesCanonicalHistory(t *testing.T) {
	params := types.DefaultParams()
	params.EpochLengthBlocks = 5
	core := &coreSlotKeeperMock{}
	k, ctx, _ := setupAccountingKeeper(t, core, 1, params)

	// accountingState opens epoch 1 at height 1, so with length 5 the epoch runs
	// heights 1..5.
	for _, tc := range []struct {
		height uint64
		want   bool
	}{
		{height: 4, want: false},
		{height: 5, want: true},
		{height: 6, want: true},
	} {
		got, err := k.ShouldFinalizeAtHeight(ctx, tc.height)
		require.NoError(t, err)
		require.Equalf(t, tc.want, got, "height %d", tc.height)
	}

	// A pause does not move the boundary. Epoch time continues while paused, and
	// gating finalization here would strand the epoch permanently once the next
	// one opens at BeginBlock.
	require.NoError(t, k.SetPauseState(ctx, types.RewardsPauseState{CurrentPaused: true}))
	got, err := k.ShouldFinalizeAtHeight(ctx, 5)
	require.NoError(t, err)
	require.True(t, got, "a paused epoch still reaches its canonical boundary")
}

// TestEpochGeometryComesFromHistoryNotTheDeprecatedMirror proves the snapshot
// mirror is inert: corrupting it cannot move a boundary.
func TestEpochGeometryComesFromHistoryNotTheDeprecatedMirror(t *testing.T) {
	params := types.DefaultParams()
	params.EpochLengthBlocks = 5
	k, ctx, _ := setupAccountingKeeper(t, &coreSlotKeeperMock{}, 1, params)

	end, err := k.EpochEndHeight(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(5), end)

	cfg, err := k.GetCurrentEpochConfig(ctx)
	require.NoError(t, err)
	cfg.EpochLengthBlocks = 999
	require.NoError(t, k.SetCurrentEpochConfig(ctx, cfg))

	end, err = k.EpochEndHeight(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(5), end, "the deprecated mirror must not influence canonical geometry")
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
