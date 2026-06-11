package keeper

import (
	"context"
	"strconv"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/x/rewards/types"
)

func emitRewardsEvent(ctx context.Context, eventType string, attrs ...sdk.Attribute) {
	sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(eventType, attrs...))
}

func emitEpochFinalized(ctx context.Context, epoch types.EpochReward, eligible uint64) {
	emitRewardsEvent(ctx, types.EventTypeEpochFinalized,
		sdk.NewAttribute(types.AttributeKeyEpoch, strconv.FormatUint(epoch.EpochNumber, 10)),
		sdk.NewAttribute(types.AttributeKeyStartHeight, strconv.FormatUint(epoch.StartHeight, 10)),
		sdk.NewAttribute(types.AttributeKeyEndHeight, strconv.FormatUint(epoch.EndHeight, 10)),
		sdk.NewAttribute(types.AttributeKeyMintedEmission, epoch.MintedEmission),
		sdk.NewAttribute(types.AttributeKeyCumulativeEmitted, epoch.CumulativeEmittedAfterEpoch),
		sdk.NewAttribute(types.AttributeKeyRewardPool, epoch.RewardPool),
		sdk.NewAttribute(types.AttributeKeyAllocated, epoch.AllocatedAmount),
		sdk.NewAttribute(types.AttributeKeyCarryOut, epoch.CarryOut),
		sdk.NewAttribute(types.AttributeKeyEligibleSlots, strconv.FormatUint(eligible, 10)),
		sdk.NewAttribute(types.AttributeKeyDistributionMethod, epoch.DistributionMethod.String()),
	)
}

func emitTreasuryPaid(ctx context.Context, address, amount string) {
	emitRewardsEvent(ctx, types.EventTypeTreasuryPaid,
		sdk.NewAttribute(types.AttributeKeyPayoutAddress, address),
		sdk.NewAttribute(types.AttributeKeyAmount, amount),
	)
}
