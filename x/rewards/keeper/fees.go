package keeper

import (
	"context"

	"cosmossdk.io/math"

	"github.com/twilight-project/twilight-core/x/rewards/types"
)

func (k Keeper) GetDistributableFees(_ context.Context, params types.Params) (math.Int, error) {
	if params.FeeCollectionEnabled || params.FeeDistributionEnabled ||
		params.FeeDistributionMode != types.FeeDistributionMode_FEE_DISTRIBUTION_MODE_NONE {
		return math.Int{}, types.ErrUnsupportedFeature.Wrap("fee collection and distribution are disabled in v1")
	}
	return math.ZeroInt(), nil
}
