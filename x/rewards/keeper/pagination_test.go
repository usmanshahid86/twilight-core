package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"cosmossdk.io/collections"
	"cosmossdk.io/log"
	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil/integration"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"

	"github.com/twilight-project/twilight-core/x/rewards/keeper"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// Bounded pagination for the canonical collection queries.
//
// The collections these surfaces expose are not small. One epoch holds an
// entitlement per participating Slot, bounded by the ratified active-slot maximum
// times the maximum epoch length under churn; reward-configuration history grows
// for the life of the chain and is never pruned.
//
// The standard SDK pagination defaults were written for collections whose size is
// a product of usage, and against these they are a denial-of-service surface
// rather than a page:
//
//   - a nil or zero limit becomes 100 AND silently turns count_total ON, and
//     counting consumes the iterator to the end of the prefix;
//   - offset is implemented by advancing the iterator one row at a time;
//   - a caller-supplied limit is not capped at all.
//
// Every case below measures rows the iterator actually advanced over, not rows
// returned. That distinction is the whole test: count_total and offset both walk
// the collection WITHOUT reading a value, so a measurement of returned rows would
// report the two worst cases as free.

const paginationPageMax = 100

func TestEntitlementEnumerationIsBoundedByThePageMaximum(t *testing.T) {
	const total = 400

	t.Run("refused requests do not walk a single row", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			page *query.PageRequest
		}{
			{"offset", &query.PageRequest{Offset: 300, Limit: 10}},
			{"count_total", &query.PageRequest{CountTotal: true, Limit: 10}},
			{"reverse", &query.PageRequest{Reverse: true, Limit: 10}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				k, ctx, access := setupCountingPrefix(t, types.SlotEntitlementsPrefix.Bytes())
				seedEntitlements(t, k, ctx, total)
				*access = storeAccess{}

				_, err := keeper.NewQueryServer(k).SlotEntitlementsByEpoch(ctx,
					&types.QuerySlotEntitlementsByEpochRequest{Epoch: 1, Pagination: tc.page})
				require.Equal(t, codes.InvalidArgument, status.Code(err))
				require.Zero(t, access.rows, "a refused request must not touch the collection")
			})
		}
	})

	for _, tc := range []struct {
		name string
		page *query.PageRequest
	}{
		{"nil pagination", nil},
		{"zero limit", &query.PageRequest{}},
		{"oversized limit", &query.PageRequest{Limit: 100_000}},
		{"limit at the maximum", &query.PageRequest{Limit: paginationPageMax}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx, access := setupCountingPrefix(t, types.SlotEntitlementsPrefix.Bytes())
			seedEntitlements(t, k, ctx, total)
			*access = storeAccess{}

			resp, err := keeper.NewQueryServer(k).SlotEntitlementsByEpoch(ctx,
				&types.QuerySlotEntitlementsByEpochRequest{Epoch: 1, Pagination: tc.page})
			require.NoError(t, err)
			require.Len(t, resp.Entitlements, paginationPageMax,
				"the server page maximum caps the response regardless of what was asked for")
			require.LessOrEqual(t, access.rows, paginationPageMax+1,
				"one page must not walk past its own end; %d of %d rows were visited", access.rows, total)
			require.NotEmpty(t, resp.Pagination.NextKey, "the cursor to the next page is returned")
			require.Zero(t, resp.Pagination.Total, "no total is computed, because computing one is unbounded")
		})
	}

	t.Run("the key cursor pages forward without skipping or repeating", func(t *testing.T) {
		k, ctx, access := setupCountingPrefix(t, types.SlotEntitlementsPrefix.Bytes())
		seedEntitlements(t, k, ctx, 250)
		server := keeper.NewQueryServer(k)

		seen := make([]uint64, 0, 250)
		var key []byte
		for page := 0; page < 5; page++ {
			*access = storeAccess{}
			resp, err := server.SlotEntitlementsByEpoch(ctx, &types.QuerySlotEntitlementsByEpochRequest{
				Epoch: 1, Pagination: &query.PageRequest{Key: key, Limit: paginationPageMax},
			})
			require.NoError(t, err)
			require.LessOrEqual(t, access.rows, paginationPageMax+1, "every page is individually bounded")
			for _, entitlement := range resp.Entitlements {
				seen = append(seen, entitlement.SlotId)
			}
			key = resp.Pagination.NextKey
			if len(key) == 0 {
				break
			}
		}
		require.Len(t, seen, 250, "every row is visited exactly once across the pages")
		for i, slotID := range seen {
			require.Equal(t, uint64(i+1), slotID, "ascending slot_id order is preserved across pages")
		}
	})
}

func TestRewardConfigHistoryIsBoundedByThePageMaximum(t *testing.T) {
	const total = 400

	t.Run("refused requests do not walk a single row", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			page *query.PageRequest
		}{
			{"offset", &query.PageRequest{Offset: 300, Limit: 10}},
			{"count_total", &query.PageRequest{CountTotal: true, Limit: 10}},
			{"reverse", &query.PageRequest{Reverse: true, Limit: 10}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				k, ctx, access := setupCountingPrefix(t, types.RewardConfigVersionsPrefix.Bytes())
				seedVersions(t, k, ctx, total)
				*access = storeAccess{}

				_, err := keeper.NewQueryServer(k).RewardConfigVersions(ctx,
					&types.QueryRewardConfigVersionsRequest{Pagination: tc.page})
				require.Equal(t, codes.InvalidArgument, status.Code(err))
				require.Zero(t, access.rows, "a refused request must not touch the collection")
			})
		}
	})

	for _, tc := range []struct {
		name string
		page *query.PageRequest
	}{
		{"nil pagination", nil},
		{"zero limit", &query.PageRequest{}},
		{"oversized limit", &query.PageRequest{Limit: 100_000}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx, access := setupCountingPrefix(t, types.RewardConfigVersionsPrefix.Bytes())
			seedVersions(t, k, ctx, total)
			*access = storeAccess{}

			resp, err := keeper.NewQueryServer(k).RewardConfigVersions(ctx,
				&types.QueryRewardConfigVersionsRequest{Pagination: tc.page})
			require.NoError(t, err)
			require.Len(t, resp.Versions, paginationPageMax)
			// The page walk, plus the single predecessor seek taken for the first row
			// of the page. That seek is one extra iterator advance and is the constant
			// the bound allows for.
			require.LessOrEqual(t, access.rows, paginationPageMax+2,
				"one page must not walk past its own end; %d of %d rows were visited", access.rows, total)
			require.NotEmpty(t, resp.Pagination.NextKey)
			require.Zero(t, resp.Pagination.Total)
		})
	}

	t.Run("the key cursor preserves ascending order across pages", func(t *testing.T) {
		k, ctx, access := setupCountingPrefix(t, types.RewardConfigVersionsPrefix.Bytes())
		seedVersions(t, k, ctx, 250)
		server := keeper.NewQueryServer(k)

		seen := make([]uint64, 0, 250)
		var key []byte
		for page := 0; page < 5; page++ {
			*access = storeAccess{}
			resp, err := server.RewardConfigVersions(ctx, &types.QueryRewardConfigVersionsRequest{
				Pagination: &query.PageRequest{Key: key, Limit: paginationPageMax},
			})
			require.NoError(t, err)
			require.LessOrEqual(t, access.rows, paginationPageMax+2)
			for _, version := range resp.Versions {
				seen = append(seen, version.Version)
			}
			key = resp.Pagination.NextKey
			if len(key) == 0 {
				break
			}
		}
		require.Len(t, seen, 250)
		for i, version := range seen {
			require.Equal(t, uint64(i+1), version)
		}
	})
}

// setupCountingPrefix builds a keeper with just enough canonical state for the
// query surfaces to run, routing every store operation under one prefix through a
// counter.
func setupCountingPrefix(t *testing.T, prefix []byte) (keeper.Keeper, sdk.Context, *storeAccess) {
	t.Helper()
	registry := codectypes.NewInterfaceRegistry()
	types.RegisterInterfaces(registry)
	keys := storetypes.NewKVStoreKeys(types.StoreKey)
	cms := integration.CreateMultiStore(keys, log.NewNopLogger())
	ctx := sdk.NewContext(cms, cmtproto.Header{Height: 1}, false, log.NewNopLogger())

	access := &storeAccess{}
	service := countingKVStoreService{
		inner:  runtime.NewKVStoreService(keys[types.StoreKey]),
		prefix: prefix,
		access: access,
	}
	k := keeper.NewKeeper(codec.NewProtoCodec(registry), service,
		accountKeeperMock{}, &bankKeeperMock{}, &coreSlotKeeperMock{}, testEconomicAddresses(t))

	params := rewardConfigParams()
	require.NoError(t, k.SetParams(ctx, params))
	require.NoError(t, k.SetState(ctx, types.RewardsState{
		CurrentEpoch: 1, CurrentEpochStartHeight: 1, CumulativeEmitted: "0", CarryForwardRemainder: "0",
	}))
	require.NoError(t, k.SetPauseState(ctx, types.RewardsPauseState{}))
	require.NoError(t, k.SetOpenRewardEnabledBlocks(ctx, 0))
	require.NoError(t, k.SetOutstandingEntitlementLiability(ctx, sdkmath.ZeroInt()))
	seedRewardConfigTimeline(t, k, ctx, params)
	return k, ctx, access
}

// seedEntitlements fills epoch 1 with one entitlement per slot, written directly
// so the fixture is not bounded by what a single finalization would produce.
// The payout address is derived across two bytes rather than one. The shared
// single-byte helper wraps past 255 and lands on the all-zero address, which the
// canonical rule refuses — and these fixtures deliberately run wider than that.
func seedEntitlements(t *testing.T, k keeper.Keeper, ctx sdk.Context, count int) {
	t.Helper()
	for slotID := 1; slotID <= count; slotID++ {
		entitlement := entitlementFor(uint64(slotID), 1, "100")
		payout := make([]byte, 20)
		payout[0] = byte(slotID % 251)
		payout[1] = byte(slotID/251) + 1
		entitlement.PayoutAddress = sdk.AccAddress(payout).String()
		require.NoError(t, k.SlotEntitlements.Set(ctx,
			collections.Join(uint64(1), uint64(slotID)), entitlement))
	}
}

// seedVersions extends the reward-configuration history to count entries,
// canonically ordered so the page validation has nothing to object to.
func seedVersions(t *testing.T, k keeper.Keeper, ctx sdk.Context, count int) {
	t.Helper()
	for i := 2; i <= count; i++ {
		seedRewardVersion(t, k, ctx,
			rewardVersionAt(uint64(i), uint64(i), sdkmath.NewInt(int64(10+i)).String()))
	}
}
