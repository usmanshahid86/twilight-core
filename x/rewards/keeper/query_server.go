package keeper

import (
	"context"

	"cosmossdk.io/collections"
	sdkmath "cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/types/query"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/twilight-project/twilight-core/x/rewards/types"
)

type queryServer struct{ Keeper }

// NewQueryServer returns a read-only gRPC query server. It performs no state
// mutation, minting, sending, or lifecycle action.
func NewQueryServer(k Keeper) types.QueryServer { return queryServer{Keeper: k} }

func (q queryServer) Params(ctx context.Context, _ *types.QueryParamsRequest) (*types.QueryParamsResponse, error) {
	params, err := q.GetParams(ctx)
	if err != nil {
		return nil, err
	}
	return &types.QueryParamsResponse{Params: &params}, nil
}

func (q queryServer) EpochInfo(ctx context.Context, _ *types.QueryEpochInfoRequest) (*types.QueryEpochInfoResponse, error) {
	state, err := q.GetState(ctx)
	if err != nil {
		return nil, err
	}
	cfg, err := q.GetCurrentEpochConfig(ctx)
	if err != nil {
		return nil, err
	}
	endHeight, err := ConfiguredEpochEndHeight(state, cfg)
	if err != nil {
		return nil, err
	}
	resp := &types.QueryEpochInfoResponse{
		State:                 &state,
		CurrentEpochConfig:    &cfg,
		CurrentEpochEndHeight: endHeight,
	}
	if pending, found, err := q.GetPendingParams(ctx); err != nil {
		return nil, err
	} else if found {
		resp.HasPendingParams = true
		resp.PendingParams = &pending
	}
	return resp, nil
}

func (q queryServer) NextHalving(ctx context.Context, _ *types.QueryNextHalvingRequest) (*types.QueryNextHalvingResponse, error) {
	info, err := q.nextHalvingInfo(ctx)
	if err != nil {
		return nil, err
	}
	return &types.QueryNextHalvingResponse{Info: info}, nil
}

func (q queryServer) EpochReward(ctx context.Context, req *types.QueryEpochRewardRequest) (*types.QueryEpochRewardResponse, error) {
	if req == nil || req.EpochNumber == 0 {
		return nil, status.Error(codes.InvalidArgument, "epoch number must be positive")
	}
	epoch, found, err := q.GetFinalizedEpoch(ctx, req.EpochNumber)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, status.Errorf(codes.NotFound, "finalized epoch %d not found", req.EpochNumber)
	}
	return &types.QueryEpochRewardResponse{EpochReward: &epoch}, nil
}

func (q queryServer) SlotRewards(ctx context.Context, req *types.QuerySlotRewardsRequest) (*types.QuerySlotRewardsResponse, error) {
	if req == nil || req.SlotId == 0 {
		return nil, status.Error(codes.InvalidArgument, "slot id must be positive")
	}
	// ClaimRecords is keyed (slotID, epoch); prefix by slot yields ascending epoch order.
	rewards, pageRes, err := query.CollectionPaginate(
		ctx, q.ClaimRecords, req.Pagination,
		func(_ collections.Pair[uint64, uint64], reward types.EligibleSlotReward) (*types.EligibleSlotReward, error) {
			r := reward
			return &r, nil
		},
		query.WithCollectionPaginationPairPrefix[uint64, uint64](req.SlotId),
	)
	if err != nil {
		return nil, err
	}
	return &types.QuerySlotRewardsResponse{Rewards: rewards, Pagination: pageRes}, nil
}

func (q queryServer) ClaimableRewards(ctx context.Context, req *types.QueryClaimableRewardsRequest) (*types.QueryClaimableRewardsResponse, error) {
	if req == nil || req.SlotId == 0 || req.StartEpoch == 0 || req.EndEpoch < req.StartEpoch {
		return nil, status.Error(codes.InvalidArgument, "invalid slot id or epoch range")
	}
	rows, err := q.IterateClaimRecordsForSlot(ctx, req.SlotId, req.StartEpoch, req.EndEpoch)
	if err != nil {
		return nil, err
	}
	total := sdkmath.ZeroInt()
	rewards := make([]*types.EligibleSlotReward, 0, len(rows))
	for i := range rows {
		reward := rows[i]
		if reward.Claimed {
			continue
		}
		amount, err := types.ParseAmountString("claim amount", reward.Amount)
		if err != nil {
			return nil, err
		}
		if !amount.IsPositive() {
			continue
		}
		total = total.Add(amount)
		r := reward
		rewards = append(rewards, &r)
	}
	return &types.QueryClaimableRewardsResponse{Rewards: rewards, TotalAmount: total.String()}, nil
}

func (q queryServer) CumulativeEmitted(ctx context.Context, _ *types.QueryCumulativeEmittedRequest) (*types.QueryCumulativeEmittedResponse, error) {
	state, err := q.GetState(ctx)
	if err != nil {
		return nil, err
	}
	params, err := q.GetParams(ctx)
	if err != nil {
		return nil, err
	}
	return &types.QueryCumulativeEmittedResponse{CumulativeEmitted: state.CumulativeEmitted, MaxSupply: params.MaxSupply}, nil
}

func (q queryServer) SupplySchedule(ctx context.Context, _ *types.QuerySupplyScheduleRequest) (*types.QuerySupplyScheduleResponse, error) {
	params, err := q.GetParams(ctx)
	if err != nil {
		return nil, err
	}
	info, err := q.nextHalvingInfo(ctx)
	if err != nil {
		return nil, err
	}
	return &types.QuerySupplyScheduleResponse{Params: &params, NextHalving: info}, nil
}

func (q queryServer) CurrentEpochActiveBlocks(ctx context.Context, req *types.QueryCurrentEpochActiveBlocksRequest) (*types.QueryCurrentEpochActiveBlocksResponse, error) {
	state, err := q.GetState(ctx)
	if err != nil {
		return nil, err
	}
	var pageReq *query.PageRequest
	if req != nil {
		pageReq = req.Pagination
	}
	// ActiveBlocks is keyed (epoch, slotID); prefix by the open epoch yields
	// ascending slotID order.
	blocks, pageRes, err := query.CollectionPaginate(
		ctx, q.ActiveBlocks, pageReq,
		func(key collections.Pair[uint64, uint64], blocks uint64) (*types.SlotActiveBlocks, error) {
			return &types.SlotActiveBlocks{SlotId: key.K2(), BlocksActive: blocks}, nil
		},
		query.WithCollectionPaginationPairPrefix[uint64, uint64](state.CurrentEpoch),
	)
	if err != nil {
		return nil, err
	}
	return &types.QueryCurrentEpochActiveBlocksResponse{EpochNumber: state.CurrentEpoch, ActiveBlocks: blocks, Pagination: pageRes}, nil
}

func (q queryServer) ModuleBalances(ctx context.Context, _ *types.QueryModuleBalancesRequest) (*types.QueryModuleBalancesResponse, error) {
	params, err := q.GetParams(ctx)
	if err != nil {
		return nil, err
	}
	denom := params.NativeDenom
	rewardsAddr := q.accountKeeper.GetModuleAddress(types.ModuleName)
	feePoolAddr := q.accountKeeper.GetModuleAddress(types.FeePoolName)
	if rewardsAddr == nil || feePoolAddr == nil {
		return nil, status.Error(codes.FailedPrecondition, "rewards module accounts are not registered")
	}
	return &types.QueryModuleBalancesResponse{
		Denom:          denom,
		RewardsBalance: q.bankKeeper.GetBalance(ctx, rewardsAddr, denom).Amount.String(),
		FeePoolBalance: q.bankKeeper.GetBalance(ctx, feePoolAddr, denom).Amount.String(),
	}, nil
}

// nextHalvingInfo computes the read-only supply-threshold halving view from the
// current cumulative emitted, max supply, and active initial subsidy, using the
// validated Phase 3 emission helpers. It mutates nothing.
func (q queryServer) nextHalvingInfo(ctx context.Context) (*types.NextHalvingInfo, error) {
	params, err := q.GetParams(ctx)
	if err != nil {
		return nil, err
	}
	state, err := q.GetState(ctx)
	if err != nil {
		return nil, err
	}
	cumulative, err := types.ParseAmountString("cumulative emitted", state.CumulativeEmitted)
	if err != nil {
		return nil, err
	}
	maxSupply, err := types.ParseAmountString("max supply", params.MaxSupply)
	if err != nil {
		return nil, err
	}
	initialSubsidy, err := types.ParseAmountString("initial block subsidy", params.InitialBlockSubsidy)
	if err != nil {
		return nil, err
	}
	tier, err := HalvingTier(cumulative, maxSupply)
	if err != nil {
		return nil, err
	}
	subsidy, err := NextBlockSubsidy(cumulative, maxSupply, initialSubsidy)
	if err != nil {
		return nil, err
	}
	threshold, hasNext, err := NextHalvingThreshold(cumulative, maxSupply)
	if err != nil {
		return nil, err
	}
	remaining := sdkmath.ZeroInt()
	nextThreshold := "0"
	if hasNext {
		nextThreshold = threshold.String()
		if diff := threshold.Sub(cumulative); diff.IsPositive() {
			remaining = diff
		}
	}
	return &types.NextHalvingInfo{
		CurrentTier:               tier,
		CurrentBlockSubsidy:       subsidy.String(),
		NextThreshold:             nextThreshold,
		RemainingUntilNextHalving: remaining.String(),
		CumulativeEmitted:         cumulative.String(),
		MaxSupply:                 maxSupply.String(),
		HasNextHalving:            hasNext,
	}, nil
}
