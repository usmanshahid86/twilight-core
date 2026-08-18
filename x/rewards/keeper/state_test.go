package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"

	"github.com/twilight-project/twilight-core/x/rewards/keeper"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

func TestStateAndCurrentEpochConfigStorage(t *testing.T) {
	k, ctx, _ := setupKeeper(t, &coreSlotKeeperMock{})
	params := types.DefaultParams()
	require.NoError(t, k.SetParams(ctx, params))

	state := types.RewardsState{CurrentEpoch: 2, CurrentEpochStartHeight: 20, CumulativeEmitted: "100", CarryForwardRemainder: "3"}
	require.NoError(t, k.SetState(ctx, state))
	gotState, err := k.GetState(ctx)
	require.NoError(t, err)
	require.Equal(t, state, gotState)

	config, err := keeper.BuildEpochConfigSnapshot(params)
	require.NoError(t, err)
	require.NoError(t, k.SetCurrentEpochConfig(ctx, config))
	gotConfig, err := k.GetCurrentEpochConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, config, gotConfig)
}

func TestStateRejectsCumulativeAboveMaxSupply(t *testing.T) {
	k, ctx, _ := setupKeeper(t, &coreSlotKeeperMock{})
	params := types.DefaultParams()
	require.NoError(t, k.SetParams(ctx, params))
	state := types.RewardsState{
		CurrentEpoch: 1, CurrentEpochStartHeight: 1,
		CumulativeEmitted: "21000000000001", CarryForwardRemainder: "0",
	}
	require.ErrorIs(t, k.SetState(ctx, state), types.ErrInvalidState)
}

func TestActiveBlockStorageHelpers(t *testing.T) {
	k, ctx, _ := setupKeeper(t, &coreSlotKeeperMock{})
	require.NoError(t, k.SetActiveBlocks(ctx, 2, 3, 7))
	require.NoError(t, k.IncrementActiveBlocks(ctx, 2, 1))
	require.NoError(t, k.IncrementActiveBlocks(ctx, 2, 1))
	require.NoError(t, k.SetActiveBlocks(ctx, 3, 1, 99))

	blocks, err := k.GetActiveBlocks(ctx, 2, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(2), blocks)
	rows, err := k.IterateActiveBlocksForEpoch(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, []uint64{1, 3}, []uint64{rows[0].SlotId, rows[1].SlotId})
	require.Equal(t, []uint64{2, 7}, []uint64{rows[0].BlocksActive, rows[1].BlocksActive})

	require.NoError(t, k.DeleteActiveBlocksForEpoch(ctx, 2))
	_, err = k.GetActiveBlocks(ctx, 2, 1)
	require.ErrorIs(t, err, collections.ErrNotFound)
	blocks, err = k.GetActiveBlocks(ctx, 3, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(99), blocks)
}

func TestFinalizedEpochStorage(t *testing.T) {
	k, ctx, _ := setupKeeper(t, &coreSlotKeeperMock{})
	params := types.DefaultParams()
	epoch := validEpoch(2, params)
	require.NoError(t, k.SetFinalizedEpoch(ctx, epoch))
	gotEpoch, found, err := k.GetFinalizedEpoch(ctx, 2)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, epoch, gotEpoch)
	_, found, err = k.GetFinalizedEpoch(ctx, 3)
	require.NoError(t, err)
	require.False(t, found)
}
