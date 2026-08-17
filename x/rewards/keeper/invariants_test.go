package keeper_test

import (
	"testing"

	sdkmath "cosmossdk.io/math"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/x/rewards/types"
)

func TestSupplyAndCumulativeInvariants(t *testing.T) {
	k, ctx, bank := setupAccountingKeeper(t, &coreSlotKeeperMock{}, 1, types.DefaultParams())

	_, broken := k.SupplyCapInvariant()(ctx)
	require.False(t, broken)
	bank.supply = sdk.NewInt64Coin(types.DefaultParams().NativeDenom, 21_000_000_000_001)
	_, broken = k.SupplyCapInvariant()(ctx)
	require.True(t, broken)

	_, broken = k.CumulativeEmittedInvariant()(ctx)
	require.False(t, broken)
	state := accountingState(1)
	state.CumulativeEmitted = "21000000000001"
	require.NoError(t, k.State.Set(ctx, state))
	_, broken = k.CumulativeEmittedInvariant()(ctx)
	require.True(t, broken)
}

// TestModuleBalanceCoverageInvariant measures the obligation the entitlement way:
// the O(1) liability plus carry, not a full walk of unclaimed claim records.
func TestModuleBalanceCoverageInvariant(t *testing.T) {
	k, ctx, bank := setupAccountingKeeper(t, &coreSlotKeeperMock{}, 1, rewardConfigParams())
	state, err := k.GetState(ctx)
	require.NoError(t, err)
	state.CarryForwardRemainder = "3"
	require.NoError(t, k.SetState(ctx, state))
	require.NoError(t, k.CreateSlotEntitlement(ctx, entitlementFor(1, 1, "1")))

	moduleAddress := sdk.AccAddress(make([]byte, 20))
	denom := types.DefaultParams().NativeDenom
	bank.balances = map[string]sdk.Coin{moduleAddress.String(): sdk.NewInt64Coin(denom, 4)}
	_, broken := k.ModuleBalanceCoverageInvariant()(ctx)
	require.False(t, broken, "escrow of 4 covers a liability of 1 plus carry of 3")

	bank.balances[moduleAddress.String()] = sdk.NewInt64Coin(denom, 3)
	_, broken = k.ModuleBalanceCoverageInvariant()(ctx)
	require.True(t, broken)
}

// TestEntitlementLiabilityInvariant is the backstop for the O(1) accumulator.
//
// Without it the accumulator is unfalsifiable: every consumer reads the same
// stored number, so a drift would be invisible to all of them.
func TestEntitlementLiabilityInvariant(t *testing.T) {
	k, ctx, _ := setupAccountingKeeper(t, &coreSlotKeeperMock{}, 1, rewardConfigParams())
	require.NoError(t, k.CreateSlotEntitlement(ctx, entitlementFor(1, 1, "500")))

	_, broken := k.EntitlementLiabilityInvariant()(ctx)
	require.False(t, broken)

	// Drift the accumulator away from the records it summarizes.
	require.NoError(t, k.SetOutstandingEntitlementLiability(ctx, sdkmath.NewInt(499)))
	_, broken = k.EntitlementLiabilityInvariant()(ctx)
	require.True(t, broken)
}

func TestDenomAndClosedEpochImmutabilityInvariants(t *testing.T) {
	k, ctx, _ := setupAccountingKeeper(t, &coreSlotKeeperMock{}, 1, types.DefaultParams())
	_, broken := k.DenomCorrectnessInvariant()(ctx)
	require.False(t, broken)

	params := types.DefaultParams()
	params.NativeDenom = "twlt"
	require.NoError(t, k.Params.Set(ctx, params))
	_, broken = k.DenomCorrectnessInvariant()(ctx)
	require.True(t, broken)
	require.NoError(t, k.Params.Set(ctx, types.DefaultParams()))

	epoch := validEpoch(1, types.DefaultParams())
	reward := validClaim(1, 1)
	reward.Claimed = true
	reward.ClaimedAtHeight = 5
	epoch.Rewards = []*types.EligibleSlotReward{&reward}
	require.NoError(t, k.FinalizedEpochs.Set(ctx, 1, epoch))
	_, broken = k.ClosedEpochImmutabilityInvariant()(ctx)
	require.True(t, broken)

	require.Error(t, k.SetFinalizedEpoch(ctx, epoch), "finalized aggregate overwrite must be rejected")
}

func TestClaimRecordCollectionKeyTypeRemainsStable(t *testing.T) {
	k, ctx, _ := setupAccountingKeeper(t, &coreSlotKeeperMock{}, 1, types.DefaultParams())
	require.NoError(t, k.ClaimRecords.Set(ctx, collections.Join(uint64(1), uint64(1)), validClaim(1, 1)))
}
