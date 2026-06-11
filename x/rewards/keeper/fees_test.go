package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/twilight-project/twilight-core/x/rewards/types"
)

func TestDormantFeePlumbing(t *testing.T) {
	k, ctx, _ := setupKeeper(t, &coreSlotKeeperMock{})
	params := types.DefaultParams()
	fees, err := k.GetDistributableFees(ctx, params)
	require.NoError(t, err)
	require.True(t, fees.IsZero())

	params.FeeCollectionEnabled = true
	_, err = k.GetDistributableFees(ctx, params)
	require.Error(t, err)
	require.Error(t, params.Validate())

	params = types.DefaultParams()
	params.FeeDistributionEnabled = true
	require.Error(t, params.Validate())
	params = types.DefaultParams()
	params.FeeDistributionMode = types.FeeDistributionMode_FEE_DISTRIBUTION_MODE_ACTIVE_SET_POOL
	require.Error(t, params.Validate())
	params = types.DefaultParams()
	params.FeeDenom = "other"
	require.Error(t, params.Validate())
}
