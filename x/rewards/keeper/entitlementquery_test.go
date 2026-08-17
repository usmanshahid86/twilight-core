package keeper_test

import (
	"testing"

	sdkmath "cosmossdk.io/math"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/cosmos/cosmos-sdk/types/query"

	"github.com/twilight-project/twilight-core/x/rewards/keeper"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// Query semantics for the entitlement and reward-configuration surfaces.
//
// One rule governs all of it: a client must be able to distinguish "nothing is
// owed" from "the chain cannot say". Genuine absence is NotFound; state that
// exists and cannot be trusted is Internal, never an empty response and never a
// raw collections error.

func TestSlotEntitlementQueryDistinguishesAbsenceFromCorruption(t *testing.T) {
	k, ctx, _ := setupEntitlements(t)
	server := keeper.NewQueryServer(k)
	require.NoError(t, k.CreateSlotEntitlement(ctx, entitlementFor(1, 1, "500")))

	t.Run("an existing obligation", func(t *testing.T) {
		resp, err := server.SlotEntitlement(ctx, &types.QuerySlotEntitlementRequest{SlotId: 1, Epoch: 1})
		require.NoError(t, err)
		require.Equal(t, "500", resp.Entitlement.EntitlementAmount)
		require.Equal(t, "0", resp.Entitlement.ReleasedAmount)
	})

	t.Run("a slot that earned nothing is NotFound", func(t *testing.T) {
		_, err := server.SlotEntitlement(ctx, &types.QuerySlotEntitlementRequest{SlotId: 4, Epoch: 1})
		require.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("zero identifiers are InvalidArgument", func(t *testing.T) {
		_, err := server.SlotEntitlement(ctx, &types.QuerySlotEntitlementRequest{SlotId: 0, Epoch: 1})
		require.Equal(t, codes.InvalidArgument, status.Code(err))
		_, err = server.SlotEntitlement(ctx, &types.QuerySlotEntitlementRequest{SlotId: 1, Epoch: 0})
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("a corrupt record is Internal, not absent", func(t *testing.T) {
		corrupt := entitlementFor(2, 1, "500")
		corrupt.ReleasedAmount = "501"
		require.NoError(t, k.SlotEntitlements.Set(ctx, entitlementTestKey(2, 1), corrupt))

		_, err := server.SlotEntitlement(ctx, &types.QuerySlotEntitlementRequest{SlotId: 2, Epoch: 1})
		require.Equal(t, codes.Internal, status.Code(err),
			"a client must not read corruption as 'nothing is owed'")
	})
}

func TestSlotEntitlementsByEpochIsBoundedAndOrdered(t *testing.T) {
	k, ctx, _ := setupEntitlements(t)
	server := keeper.NewQueryServer(k)
	for _, slotID := range []uint64{9, 2, 40, 1, 17} {
		require.NoError(t, k.CreateSlotEntitlement(ctx, entitlementFor(slotID, 4, "10")))
		require.NoError(t, k.CreateSlotEntitlement(ctx, entitlementFor(slotID, 5, "10")))
	}

	t.Run("ascending by slot id, scoped to one epoch", func(t *testing.T) {
		resp, err := server.SlotEntitlementsByEpoch(ctx,
			&types.QuerySlotEntitlementsByEpochRequest{Epoch: 4})
		require.NoError(t, err)
		require.Len(t, resp.Entitlements, 5)
		got := make([]uint64, 0, len(resp.Entitlements))
		for _, entitlement := range resp.Entitlements {
			require.Equal(t, uint64(4), entitlement.Epoch, "the range must not leak into a neighboring epoch")
			got = append(got, entitlement.SlotId)
		}
		require.Equal(t, []uint64{1, 2, 9, 17, 40}, got)
	})

	t.Run("paginated", func(t *testing.T) {
		resp, err := server.SlotEntitlementsByEpoch(ctx, &types.QuerySlotEntitlementsByEpochRequest{
			Epoch: 4, Pagination: &query.PageRequest{Limit: 2},
		})
		require.NoError(t, err)
		require.Len(t, resp.Entitlements, 2)
		require.NotEmpty(t, resp.Pagination.NextKey, "a bounded page must offer a cursor")
	})

	t.Run("an epoch with no obligations is empty, not an error", func(t *testing.T) {
		resp, err := server.SlotEntitlementsByEpoch(ctx,
			&types.QuerySlotEntitlementsByEpochRequest{Epoch: 9})
		require.NoError(t, err)
		require.Empty(t, resp.Entitlements)
	})

	t.Run("a corrupt row fails the page rather than being skipped", func(t *testing.T) {
		corrupt := entitlementFor(3, 4, "10")
		corrupt.EntitlementAmount = "lots"
		require.NoError(t, k.SlotEntitlements.Set(ctx, entitlementTestKey(3, 4), corrupt))

		_, err := server.SlotEntitlementsByEpoch(ctx,
			&types.QuerySlotEntitlementsByEpochRequest{Epoch: 4})
		require.Equal(t, codes.Internal, status.Code(err),
			"a silently shortened epoch would look like an epoch that owed less")
	})

	t.Run("a zero epoch is InvalidArgument", func(t *testing.T) {
		_, err := server.SlotEntitlementsByEpoch(ctx, &types.QuerySlotEntitlementsByEpochRequest{Epoch: 0})
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}

func TestRewardConfigVersionsQueryExposesHistoryAndTheSingleSchedule(t *testing.T) {
	k, ctx, _ := setupRewardConfig(t)
	server := keeper.NewQueryServer(k)
	seedRewardVersion(t, k, ctx, rewardVersionAt(2, 4, "20"))

	t.Run("history, with no schedule pending", func(t *testing.T) {
		resp, err := server.RewardConfigVersions(ctx, &types.QueryRewardConfigVersionsRequest{})
		require.NoError(t, err)
		require.Len(t, resp.Versions, 2)
		require.Equal(t, []uint64{1, 2}, []uint64{resp.Versions[0].Version, resp.Versions[1].Version})
		require.Nil(t, resp.Scheduled)
	})

	t.Run("the single pending change is a field, not a page", func(t *testing.T) {
		require.NoError(t, k.ScheduledRewardConfigs.Set(ctx, 2, types.ScheduledRewardConfig{
			EffectiveEpoch: 2, InitialBlockSubsidy: "77",
		}))
		resp, err := server.RewardConfigVersions(ctx, &types.QueryRewardConfigVersionsRequest{})
		require.NoError(t, err)
		require.NotNil(t, resp.Scheduled)
		require.Equal(t, "77", resp.Scheduled.InitialBlockSubsidy)
	})

	t.Run("a schedule holding more than the one admissible entry is Internal", func(t *testing.T) {
		require.NoError(t, k.ScheduledRewardConfigs.Set(ctx, 7, types.ScheduledRewardConfig{
			EffectiveEpoch: 7, InitialBlockSubsidy: "80",
		}))
		_, err := server.RewardConfigVersions(ctx, &types.QueryRewardConfigVersionsRequest{})
		require.Equal(t, codes.Internal, status.Code(err),
			"a second scheduled entry is corruption, not a queue to page through")
	})

	t.Run("a malformed history row is Internal", func(t *testing.T) {
		k, ctx, _ := setupRewardConfig(t)
		server := keeper.NewQueryServer(k)
		require.NoError(t, k.RewardConfigVersions.Set(ctx, 4, rewardVersionAt(2, 9, "20")))

		_, err := server.RewardConfigVersions(ctx, &types.QueryRewardConfigVersionsRequest{})
		require.Equal(t, codes.Internal, status.Code(err))
	})
}

// TestModuleBalancesExposesSolvency makes the escrow relation observable without
// database access, which is what lets an operator check the chain's own
// finalization assertion from outside.
func TestModuleBalancesExposesSolvency(t *testing.T) {
	k, ctx, bank := setupEntitlements(t)
	server := keeper.NewQueryServer(k)
	require.NoError(t, k.CreateSlotEntitlement(ctx, entitlementFor(1, 1, "500")))
	state, err := k.GetState(ctx)
	require.NoError(t, err)
	state.CarryForwardRemainder = "7"
	require.NoError(t, k.SetState(ctx, state))
	bank.credit(moduleAccountAddress(), coins(507))

	resp, err := server.ModuleBalances(ctx, &types.QueryModuleBalancesRequest{})
	require.NoError(t, err)
	require.Equal(t, "500", resp.OutstandingEntitlementLiability)
	require.Equal(t, "7", resp.CarryForwardRemainder)
	require.Equal(t, "507", resp.RewardsBalance)

	liability, ok := sdkmath.NewIntFromString(resp.OutstandingEntitlementLiability)
	require.True(t, ok)
	carry, ok := sdkmath.NewIntFromString(resp.CarryForwardRemainder)
	require.True(t, ok)
	require.Equal(t, resp.RewardsBalance, liability.Add(carry).String(),
		"the response alone must be enough to check solvency")
}

// TestModuleBalancesFailsClosedOnACorruptLiability keeps the observability
// surface from reporting a plausible zero.
func TestModuleBalancesFailsClosedOnACorruptLiability(t *testing.T) {
	k, ctx, _ := setupEntitlements(t)
	server := keeper.NewQueryServer(k)
	require.NoError(t, k.OutstandingEntitlementLiability.Set(ctx, "owed"))

	_, err := server.ModuleBalances(ctx, &types.QueryModuleBalancesRequest{})
	require.Equal(t, codes.Internal, status.Code(err))
}
