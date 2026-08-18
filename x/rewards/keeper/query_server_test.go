package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"

	"github.com/twilight-project/twilight-core/x/rewards/keeper"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

func setupQueryServer(t *testing.T) (types.QueryServer, sdk.Context, keeper.Keeper) {
	t.Helper()
	k, ctx, _ := setupKeeper(t, &coreSlotKeeperMock{})
	params := types.DefaultParams()
	require.NoError(t, k.SetParams(ctx, params))
	require.NoError(t, k.SetState(ctx, types.RewardsState{CurrentEpoch: 3, CurrentEpochStartHeight: 50, CumulativeEmitted: "1000", CarryForwardRemainder: "10"}))
	cfg, err := keeper.BuildEpochConfigSnapshot(params)
	require.NoError(t, err)
	require.NoError(t, k.SetCurrentEpochConfig(ctx, cfg))
	seedEpochTimeline(t, k, ctx, params, types.RewardsState{CurrentEpoch: 3, CurrentEpochStartHeight: 50})
	require.NoError(t, k.SetFinalizedEpoch(ctx, validEpoch(1, params)))
	require.NoError(t, k.SetActiveBlocks(ctx, 3, 1, 5))
	require.NoError(t, k.SetActiveBlocks(ctx, 3, 2, 3))
	return keeper.NewQueryServer(k), ctx, k
}

func TestQueryServerReadsAndErrors(t *testing.T) {
	qs, ctx, k := setupQueryServer(t)
	params := types.DefaultParams()

	// Params
	pres, err := qs.Params(ctx, &types.QueryParamsRequest{})
	require.NoError(t, err)
	require.Equal(t, params.MaxSupply, pres.Params.MaxSupply)
	require.Equal(t, params.NativeDenom, pres.Params.NativeDenom)

	// EpochInfo
	einfo, err := qs.EpochInfo(ctx, &types.QueryEpochInfoRequest{})
	require.NoError(t, err)
	require.Equal(t, uint64(3), einfo.State.CurrentEpoch)
	require.Equal(t, 50+types.DefaultEpochLengthBlocks-1, einfo.CurrentEpochEndHeight)
	require.False(t, einfo.HasPendingParams)

	// NextHalving (tier 0, full subsidy, first threshold ahead)
	nh, err := qs.NextHalving(ctx, &types.QueryNextHalvingRequest{})
	require.NoError(t, err)
	require.Equal(t, uint64(0), nh.Info.CurrentTier)
	require.Equal(t, "416190", nh.Info.CurrentBlockSubsidy)
	require.True(t, nh.Info.HasNextHalving)

	// EpochReward: found, not-found, invalid
	er, err := qs.EpochReward(ctx, &types.QueryEpochRewardRequest{EpochNumber: 1})
	require.NoError(t, err)
	require.Equal(t, uint64(1), er.EpochReward.EpochNumber)
	_, err = qs.EpochReward(ctx, &types.QueryEpochRewardRequest{EpochNumber: 99})
	require.Equal(t, codes.NotFound, status.Code(err))
	_, err = qs.EpochReward(ctx, &types.QueryEpochRewardRequest{EpochNumber: 0})
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	// CumulativeEmitted
	ce, err := qs.CumulativeEmitted(ctx, &types.QueryCumulativeEmittedRequest{})
	require.NoError(t, err)
	require.Equal(t, "1000", ce.CumulativeEmitted)
	require.Equal(t, params.MaxSupply, ce.MaxSupply)

	// SupplySchedule
	ss, err := qs.SupplySchedule(ctx, &types.QuerySupplyScheduleRequest{})
	require.NoError(t, err)
	require.NotNil(t, ss.Params)
	require.NotNil(t, ss.NextHalving)

	// CurrentEpochActiveBlocks: deterministic ascending slotID order
	ab, err := qs.CurrentEpochActiveBlocks(ctx, &types.QueryCurrentEpochActiveBlocksRequest{})
	require.NoError(t, err)
	require.Equal(t, uint64(3), ab.EpochNumber)
	require.Len(t, ab.ActiveBlocks, 2)
	require.Equal(t, uint64(1), ab.ActiveBlocks[0].SlotId)
	require.Equal(t, uint64(5), ab.ActiveBlocks[0].BlocksActive)
	require.Equal(t, uint64(2), ab.ActiveBlocks[1].SlotId)

	// ModuleBalances: native denom, zero balances under the mock bank
	mb, err := qs.ModuleBalances(ctx, &types.QueryModuleBalancesRequest{})
	require.NoError(t, err)
	require.Equal(t, params.NativeDenom, mb.Denom)
	require.Equal(t, "0", mb.RewardsBalance)
	require.Equal(t, "0", mb.FeePoolBalance)

	// Queries are read-only: state is unchanged after all of the above.
	state, err := k.GetState(ctx)
	require.NoError(t, err)
	require.Equal(t, "1000", state.CumulativeEmitted)
	require.Equal(t, uint64(3), state.CurrentEpoch)
}

func TestQueryServerPagination(t *testing.T) {
	k, ctx, _ := setupKeeper(t, &coreSlotKeeperMock{})
	params := types.DefaultParams()
	require.NoError(t, k.SetParams(ctx, params))
	require.NoError(t, k.SetState(ctx, types.RewardsState{CurrentEpoch: 7, CurrentEpochStartHeight: 1, CumulativeEmitted: "0", CarryForwardRemainder: "0"}))
	qs := keeper.NewQueryServer(k)

	// --- CurrentEpochActiveBlocks: three slots for the open epoch, paginated. ---
	for slot := uint64(1); slot <= 3; slot++ {
		require.NoError(t, k.SetActiveBlocks(ctx, 7, slot, slot*10))
	}
	ab1, err := qs.CurrentEpochActiveBlocks(ctx, &types.QueryCurrentEpochActiveBlocksRequest{Pagination: &query.PageRequest{Limit: 2}})
	require.NoError(t, err)
	require.Len(t, ab1.ActiveBlocks, 2)
	require.Equal(t, uint64(1), ab1.ActiveBlocks[0].SlotId)
	require.Equal(t, uint64(2), ab1.ActiveBlocks[1].SlotId)
	require.NotEmpty(t, ab1.Pagination.NextKey)

	ab2, err := qs.CurrentEpochActiveBlocks(ctx, &types.QueryCurrentEpochActiveBlocksRequest{Pagination: &query.PageRequest{Key: ab1.Pagination.NextKey}})
	require.NoError(t, err)
	require.Len(t, ab2.ActiveBlocks, 1)
	require.Equal(t, uint64(3), ab2.ActiveBlocks[0].SlotId)
	require.Empty(t, ab2.Pagination.NextKey)

	// Empty: point the open epoch at one with no active-block rows.
	require.NoError(t, k.SetState(ctx, types.RewardsState{CurrentEpoch: 99, CurrentEpochStartHeight: 1, CumulativeEmitted: "0", CarryForwardRemainder: "0"}))
	abEmpty, err := qs.CurrentEpochActiveBlocks(ctx, &types.QueryCurrentEpochActiveBlocksRequest{})
	require.NoError(t, err)
	require.Empty(t, abEmpty.ActiveBlocks)
	require.Equal(t, uint64(99), abEmpty.EpochNumber)
}
