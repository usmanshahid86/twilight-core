package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	sdkmath "cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/types/query"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/twilight-project/twilight-core/internal/checked"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// epochProjectionHorizon bounds how far ahead a boundary query will walk the
// schedule. Beyond it the query returns a deterministic not-found rather than an
// approximated or clamped height (§68). It bounds query work only and is not a
// protocol value: no consensus path projects boundaries.
const epochProjectionHorizon = 1_000

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
	// Canonical geometry, from EpochConfigVersion history rather than from the
	// deprecated snapshot mirror the response still carries for compatibility.
	startHeight, err := q.EpochStartHeight(ctx, state.CurrentEpoch)
	if err != nil {
		return nil, epochQueryError(err)
	}
	endHeight, err := q.EpochEndHeight(ctx, state.CurrentEpoch)
	if err != nil {
		return nil, epochQueryError(err)
	}
	length, err := q.EpochLengthForEpoch(ctx, state.CurrentEpoch)
	if err != nil {
		return nil, epochQueryError(err)
	}
	openBlocks, err := q.GetOpenRewardEnabledBlocks(ctx)
	if err != nil {
		return nil, epochQueryError(err)
	}
	resp := &types.QueryEpochInfoResponse{
		State:                    &state,
		CurrentEpochConfig:       &cfg,
		CurrentEpochEndHeight:    endHeight,
		CurrentEpochStartHeight:  startHeight,
		CurrentEpochLengthBlocks: length,
		OpenRewardEnabledBlocks:  openBlocks,
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

// epochQueryError maps an epoch-geometry failure onto the public query contract.
//
// The distinction the module maintains internally has to survive to the caller:
// an epoch with no applicable configuration version, or one beyond the supported
// projection horizon, is an ordinary answer and reaches a client as NotFound.
// History that exists and cannot be trusted is a state-integrity failure and must
// not be flattened into "not found", which would tell a client the epoch was
// never configured when in fact the database holding it is broken.
//
// Anything already carrying a transport code — a canceled or timed-out query —
// is passed through untouched rather than relabelled.
func epochQueryError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, types.ErrEpochConfigNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, types.ErrInvalidState):
		return status.Error(codes.Internal, err.Error())
	default:
		return err
	}
}

// EpochConfigVersions returns the canonical epoch-configuration history together
// with any future schedule.
func (q queryServer) EpochConfigVersions(
	ctx context.Context, req *types.QueryEpochConfigVersionsRequest,
) (*types.QueryEpochConfigVersionsResponse, error) {
	var pageReq *query.PageRequest
	if req != nil {
		pageReq = req.Pagination
	}
	versions, pageRes, err := query.CollectionPaginate(
		ctx, q.Keeper.EpochConfigVersions, pageReq,
		func(_ uint64, version types.EpochConfigVersion) (*types.EpochConfigVersion, error) {
			value := version
			return &value, nil
		},
	)
	if err != nil {
		return nil, epochQueryError(err)
	}
	resp := &types.QueryEpochConfigVersionsResponse{Versions: versions, Pagination: pageRes}
	if err := q.ScheduledEpochConfigs.Walk(ctx, nil, func(_ uint64, scheduled types.ScheduledEpochConfig) (bool, error) {
		value := scheduled
		resp.Scheduled = append(resp.Scheduled, &value)
		return false, nil
	}); err != nil {
		return nil, epochQueryError(err)
	}
	return resp, nil
}

// EpochBoundaries returns the canonical start and end height of one epoch.
//
// Boundaries beyond the supported derivation horizon are refused rather than
// clamped or invented (§68).
func (q queryServer) EpochBoundaries(
	ctx context.Context, req *types.QueryEpochBoundariesRequest,
) (*types.QueryEpochBoundariesResponse, error) {
	if req == nil || req.EpochNumber == 0 {
		return nil, status.Error(codes.InvalidArgument, "epoch number must be positive")
	}
	start, err := q.ProjectEpochStartHeight(ctx, req.EpochNumber, epochProjectionHorizon)
	if err != nil {
		return nil, epochQueryError(err)
	}
	next, err := q.ProjectEpochStartHeight(ctx, req.EpochNumber+1, epochProjectionHorizon)
	if err != nil {
		return nil, epochQueryError(err)
	}
	length, err := q.EpochLengthForEpoch(ctx, req.EpochNumber)
	if err != nil {
		return nil, epochQueryError(err)
	}
	// Checked like every other boundary step. A validated history cannot produce a
	// zero start height, but deriving an end by subtracting from a value this
	// function did not compute itself is exactly where an unchecked operation
	// would go unnoticed.
	end, err := checked.SubUint64(next, 1)
	if err != nil {
		return nil, epochQueryError(types.ErrInvalidState.Wrapf(
			"epoch %d end height underflows", req.EpochNumber))
	}
	return &types.QueryEpochBoundariesResponse{
		EpochNumber:       req.EpochNumber,
		StartHeight:       start,
		EndHeight:         end,
		EpochLengthBlocks: length,
	}, nil
}

// RewardsPauseState returns the canonical pause state and whether monetary
// release is permitted for the current block.
func (q queryServer) RewardsPauseState(
	ctx context.Context, _ *types.QueryRewardsPauseStateRequest,
) (*types.QueryRewardsPauseStateResponse, error) {
	state, err := q.GetPauseState(ctx)
	if err != nil {
		return nil, epochQueryError(err)
	}
	enabled, err := q.SettlementReleaseEnabled(ctx)
	if err != nil {
		return nil, epochQueryError(err)
	}
	return &types.QueryRewardsPauseStateResponse{PauseState: &state, ReleaseEnabled: enabled}, nil
}
