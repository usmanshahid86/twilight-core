package keeper_test

import (
	"testing"

	sdkmath "cosmossdk.io/math"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"

	"github.com/twilight-project/twilight-core/x/rewards/keeper"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// The query contract, stated as three properties.
//
//   - genuine absence is NotFound, canonical state that cannot be trusted is
//     Internal, and neither is ever an empty successful response;
//   - a returned record has passed the rules a consequential read applies, so a
//     client is never handed state the chain itself would refuse to compute with;
//   - the observable ordering a response promises is the one it delivers.
//
// The second is the one worth being precise about. A query that returned a record
// the release path would refuse is worse than an error: the client has no way to
// tell, and will act on it.

// TestRewardConfigVersionPointQuery is S2.
func TestRewardConfigVersionPointQuery(t *testing.T) {
	k, ctx, _ := setupRewardConfig(t)
	server := keeper.NewQueryServer(k)
	seedRewardVersion(t, k, ctx, rewardVersionAt(2, 4, "20"))
	seedRewardVersion(t, k, ctx, rewardVersionAt(3, 9, "30"))

	t.Run("by version number", func(t *testing.T) {
		for _, tc := range []struct {
			number         uint64
			effectiveEpoch uint64
			subsidy        string
		}{
			{number: 1, effectiveEpoch: 1, subsidy: "10"},
			{number: 2, effectiveEpoch: 4, subsidy: "20"},
			{number: 3, effectiveEpoch: 9, subsidy: "30"},
		} {
			resp, err := server.RewardConfigVersion(ctx, &types.QueryRewardConfigVersionRequest{Version: tc.number})
			require.NoError(t, err)
			require.Equal(t, tc.number, resp.Version.Version)
			require.Equal(t, tc.effectiveEpoch, resp.Version.EffectiveEpoch)
			require.Equal(t, tc.subsidy, resp.Version.InitialBlockSubsidy)
		}
	})

	t.Run("by effective epoch, exactly", func(t *testing.T) {
		resp, err := server.RewardConfigVersion(ctx, &types.QueryRewardConfigVersionRequest{EffectiveEpoch: 4})
		require.NoError(t, err)
		require.Equal(t, uint64(2), resp.Version.Version)

		// Epoch 5 is GOVERNED by version 2 but nothing became effective at it. The
		// selector is identity, not applicability, so this is a genuine absence — a
		// predecessor seek here would report version 2 as effective at every epoch
		// from 4 to 8.
		_, err = server.RewardConfigVersion(ctx, &types.QueryRewardConfigVersionRequest{EffectiveEpoch: 5})
		require.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("a version that was never accepted is NotFound", func(t *testing.T) {
		_, err := server.RewardConfigVersion(ctx, &types.QueryRewardConfigVersionRequest{Version: 4})
		require.Equal(t, codes.NotFound, status.Code(err))
		_, err = server.RewardConfigVersion(ctx, &types.QueryRewardConfigVersionRequest{Version: 99})
		require.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("exactly one selector", func(t *testing.T) {
		for _, req := range []*types.QueryRewardConfigVersionRequest{
			{},
			{Version: 1, EffectiveEpoch: 1},
			nil,
		} {
			_, err := server.RewardConfigVersion(ctx, req)
			require.Equal(t, codes.InvalidArgument, status.Code(err))
		}
	})
}

func TestRewardConfigVersionPointQueryFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		corrupt func(*testing.T, keeper.Keeper, sdk.Context)
		request *types.QueryRewardConfigVersionRequest
	}{
		{
			name:    "a malformed record",
			request: &types.QueryRewardConfigVersionRequest{EffectiveEpoch: 4},
			corrupt: func(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
				seedRewardVersion(t, k, ctx, rewardVersionAt(2, 4, "010"))
			},
		},
		{
			name:    "a record stored under the wrong key",
			request: &types.QueryRewardConfigVersionRequest{EffectiveEpoch: 4},
			corrupt: func(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
				require.NoError(t, k.RewardConfigVersions.Set(ctx, 4, rewardVersionAt(2, 7, "20")))
			},
		},
		{
			name:    "an inadmissible treasury destination behind a positive share",
			request: &types.QueryRewardConfigVersionRequest{Version: 2},
			corrupt: func(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
				version := rewardVersionAt(2, 4, "20")
				version.EmissionTreasuryShareBps = 1_000
				version.TreasuryAddress = "not-an-address"
				seedRewardVersion(t, k, ctx, version)
			},
		},
		{
			name:    "a missing permanent anchor",
			request: &types.QueryRewardConfigVersionRequest{Version: 2},
			corrupt: func(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
				seedRewardVersion(t, k, ctx, rewardVersionAt(2, 4, "20"))
				require.NoError(t, k.RewardConfigVersions.Remove(ctx, 1))
			},
		},
		{
			name:    "a non-monotonic edge",
			request: &types.QueryRewardConfigVersionRequest{EffectiveEpoch: 4},
			corrupt: func(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
				// A version number that does not advance with its effective epoch.
				seedRewardVersion(t, k, ctx, rewardVersionAt(1, 4, "20"))
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx, _ := setupRewardConfig(t)
			server := keeper.NewQueryServer(k)
			tc.corrupt(t, k, ctx)

			_, err := server.RewardConfigVersion(ctx, tc.request)
			require.Equal(t, codes.Internal, status.Code(err),
				"malformed canonical state must not be reported as absence or as a raw error")
		})
	}
}

func TestRewardConfigHistoryQueryFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		corrupt func(*testing.T, keeper.Keeper, sdk.Context)
	}{
		{
			name: "a malformed row",
			corrupt: func(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
				seedRewardVersion(t, k, ctx, rewardVersionAt(2, 4, "0x20"))
			},
		},
		{
			name: "an inadmissible treasury destination",
			corrupt: func(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
				version := rewardVersionAt(2, 4, "20")
				version.EmissionTreasuryShareBps = 1_000
				version.TreasuryAddress = zeroAddress()
				seedRewardVersion(t, k, ctx, version)
			},
		},
		{
			name: "a broken anchor",
			corrupt: func(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
				seedRewardVersion(t, k, ctx, rewardVersionAt(2, 4, "20"))
				require.NoError(t, k.RewardConfigVersions.Remove(ctx, 1))
			},
		},
		{
			name: "a non-monotonic edge inside the page",
			corrupt: func(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
				// A gap: the ratified sequence is contiguous, so 1 then 3 is corrupt.
				seedRewardVersion(t, k, ctx, rewardVersionAt(3, 4, "20"))
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx, _ := setupRewardConfig(t)
			server := keeper.NewQueryServer(k)
			tc.corrupt(t, k, ctx)

			_, err := server.RewardConfigVersions(ctx, &types.QueryRewardConfigVersionsRequest{})
			require.Equal(t, codes.Internal, status.Code(err))
		})
	}

	t.Run("an intact history is served", func(t *testing.T) {
		k, ctx, _ := setupRewardConfig(t)
		server := keeper.NewQueryServer(k)
		seedRewardVersion(t, k, ctx, rewardVersionAt(2, 4, "20"))
		seedRewardVersion(t, k, ctx, rewardVersionAt(3, 9, "30"))

		resp, err := server.RewardConfigVersions(ctx, &types.QueryRewardConfigVersionsRequest{})
		require.NoError(t, err)
		require.Len(t, resp.Versions, 3)
	})
}

func TestModuleBalancesFailsClosedOnUnreadableAmounts(t *testing.T) {
	t.Run("a malformed carry", func(t *testing.T) {
		k, ctx, _ := setupEntitlements(t)
		server := keeper.NewQueryServer(k)
		state, err := k.GetState(ctx)
		require.NoError(t, err)
		state.CarryForwardRemainder = "not-an-amount"
		require.NoError(t, k.State.Set(ctx, state))

		_, err = server.ModuleBalances(ctx, &types.QueryModuleBalancesRequest{})
		require.Equal(t, codes.Internal, status.Code(err),
			"a response whose members cannot be added up must not be published as solvency evidence")
	})

	t.Run("a malformed liability", func(t *testing.T) {
		k, ctx, _ := setupEntitlements(t)
		server := keeper.NewQueryServer(k)
		require.NoError(t, k.OutstandingEntitlementLiability.Set(ctx, "not-an-amount"))

		_, err := server.ModuleBalances(ctx, &types.QueryModuleBalancesRequest{})
		require.Equal(t, codes.Internal, status.Code(err))
	})
}

func TestEntitlementQueriesValidateTheirRelations(t *testing.T) {
	seedCorruptEntitlement := func(t *testing.T, k keeper.Keeper, ctx sdk.Context, mutate func(*types.SlotEntitlement)) {
		t.Helper()
		entitlement := entitlementFor(1, 1, "500")
		mutate(&entitlement)
		require.NoError(t, k.SlotEntitlements.Set(ctx, collections.Join(uint64(1), uint64(1)), entitlement))
	}

	for _, tc := range []struct {
		name   string
		mutate func(*types.SlotEntitlement)
	}{
		{
			name: "a payout destination that is no longer admissible",
			mutate: func(e *types.SlotEntitlement) {
				e.PayoutAddress = testModuleAddress(testModuleAccountName)
			},
		},
		{
			name:   "a governing configuration version that never governed the epoch",
			mutate: func(e *types.SlotEntitlement) { e.RewardConfigVersion = 7 },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx, _ := setupEntitlements(t)
			server := keeper.NewQueryServer(k)
			seedCorruptEntitlement(t, k, ctx, tc.mutate)

			_, err := server.SlotEntitlement(ctx, &types.QuerySlotEntitlementRequest{SlotId: 1, Epoch: 1})
			require.Equal(t, codes.Internal, status.Code(err))

			_, err = server.SlotEntitlementsByEpoch(ctx, &types.QuerySlotEntitlementsByEpochRequest{Epoch: 1})
			require.Equal(t, codes.Internal, status.Code(err))
		})
	}
}

// TestEntitlementEnumerationRefusesReversePagination is S7.
//
// The response promises ascending slot_id order, and the standard PageRequest
// carries a flag that would silently invert it. Two contracts cannot both hold,
// and the canonical one is the one settlement materialization walks.
func TestEntitlementEnumerationRefusesReversePagination(t *testing.T) {
	k, ctx, _ := setupEntitlements(t)
	server := keeper.NewQueryServer(k)
	for _, slotID := range []uint64{1, 2, 3} {
		require.NoError(t, k.CreateSlotEntitlement(ctx, entitlementFor(slotID, 1, "100")))
	}

	t.Run("ascending is the delivered order", func(t *testing.T) {
		resp, err := server.SlotEntitlementsByEpoch(ctx, &types.QuerySlotEntitlementsByEpochRequest{Epoch: 1})
		require.NoError(t, err)
		require.Len(t, resp.Entitlements, 3)
		for i, entitlement := range resp.Entitlements {
			require.Equal(t, uint64(i+1), entitlement.SlotId)
		}
	})

	t.Run("reverse is refused rather than served", func(t *testing.T) {
		_, err := server.SlotEntitlementsByEpoch(ctx, &types.QuerySlotEntitlementsByEpochRequest{
			Epoch:      1,
			Pagination: &query.PageRequest{Reverse: true, Limit: 10},
		})
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})

	t.Run("forward pagination still works", func(t *testing.T) {
		resp, err := server.SlotEntitlementsByEpoch(ctx, &types.QuerySlotEntitlementsByEpochRequest{
			Epoch:      1,
			Pagination: &query.PageRequest{Limit: 2},
		})
		require.NoError(t, err)
		require.Len(t, resp.Entitlements, 2)
		require.Equal(t, uint64(1), resp.Entitlements[0].SlotId)
		require.Equal(t, uint64(2), resp.Entitlements[1].SlotId)
		require.NotEmpty(t, resp.Pagination.NextKey)
	})
}

// TestRewardConfigVersionLookupCostIsBoundedByTheRequest states the reason a
// version-number lookup is acceptable as a query and unacceptable on a block path.
//
// The history is keyed by effective epoch, so a bare version number has no key.
// The walk is bounded because versions ascend with epochs: it stops at the first
// row that reaches or passes the number asked for. A request for version 1 costs
// one row however long the history is.
func TestRewardConfigVersionLookupCostIsBoundedByTheRequest(t *testing.T) {
	k, ctx, _ := setupRewardConfig(t)
	for i := uint64(2); i <= 40; i++ {
		seedRewardVersion(t, k, ctx, rewardVersionAt(i, i*3, sdkmath.NewIntFromUint64(10+i).String()))
	}
	server := keeper.NewQueryServer(k)

	resp, err := server.RewardConfigVersion(ctx, &types.QueryRewardConfigVersionRequest{Version: 1})
	require.NoError(t, err)
	require.Equal(t, uint64(1), resp.Version.EffectiveEpoch)

	// A number beyond the history terminates at the last row rather than running on.
	_, err = server.RewardConfigVersion(ctx, &types.QueryRewardConfigVersionRequest{Version: 1_000})
	require.Equal(t, codes.NotFound, status.Code(err))
}
