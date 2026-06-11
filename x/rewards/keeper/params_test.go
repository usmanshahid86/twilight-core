package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	appparams "github.com/twilight-project/twilight-core/app/params"
	"github.com/twilight-project/twilight-core/x/rewards/keeper"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

func TestParamsAndPendingParamsStorage(t *testing.T) {
	k, ctx, _ := setupKeeper(t, &coreSlotKeeperMock{})
	params := types.DefaultParams()
	require.NoError(t, k.SetParams(ctx, params))
	got, err := k.GetParams(ctx)
	require.NoError(t, err)
	require.Equal(t, params, got)

	_, found, err := k.GetPendingParams(ctx)
	require.NoError(t, err)
	require.False(t, found)

	pending := params
	pending.EpochLengthBlocks++
	require.NoError(t, k.SetPendingParams(ctx, pending))
	gotPending, found, err := k.GetPendingParams(ctx)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, pending, gotPending)

	require.NoError(t, k.ClearPendingParams(ctx))
	require.NoError(t, k.ClearPendingParams(ctx))
	_, found, err = k.GetPendingParams(ctx)
	require.NoError(t, err)
	require.False(t, found)
}

func TestParamsStorageRejectsInvalidAndImmutablePendingParams(t *testing.T) {
	k, ctx, _ := setupKeeper(t, &coreSlotKeeperMock{})
	params := types.DefaultParams()
	require.NoError(t, k.SetParams(ctx, params))

	invalid := params
	invalid.FeeCollectionEnabled = true
	require.Error(t, k.SetParams(ctx, invalid))

	immutable := params
	immutable.NativeDenom = appparams.NativeDisplayDenom
	require.ErrorIs(t, k.SetPendingParams(ctx, immutable), types.ErrImmutableParam)
}

func TestBuildEpochConfigSnapshot(t *testing.T) {
	params := types.DefaultParams()
	params.EpochLengthBlocks = 99
	params.InitialBlockSubsidy = "123"
	snapshot, err := keeper.BuildEpochConfigSnapshot(params)
	require.NoError(t, err)
	require.Equal(t, types.DefaultEpochSnapshotVersion, snapshot.SnapshotVersion)
	require.Equal(t, params.EpochLengthBlocks, snapshot.EpochLengthBlocks)
	require.Equal(t, params.InitialBlockSubsidy, snapshot.InitialBlockSubsidy)
	require.Equal(t, params.DistributionMethod, snapshot.DistributionMethod)
	require.Equal(t, params.RemainderPolicy, snapshot.RemainderPolicy)
	require.Equal(t, params.FeeDenom, snapshot.FeeDenom)
}
