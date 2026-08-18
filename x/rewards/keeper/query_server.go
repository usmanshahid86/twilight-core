package keeper

import (
	"context"
	"errors"
	"fmt"

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

// maxScheduledEpochConfigsPerPage caps one page of the future schedule. Like the
// projection horizon it bounds query work only and is not a protocol value: no
// consensus path reads the schedule in pages.
const maxScheduledEpochConfigsPerPage = 100

// maxCollectionPageSize bounds one page of any canonical collection query below.
// Like the other query constants it bounds query work only and is not a protocol
// value: no consensus path paginates.
const maxCollectionPageSize = 100

// boundedPage turns a caller-supplied PageRequest into one whose cost is bounded
// by this server rather than by the caller or by the collection.
//
// # Why the SDK defaults are not usable here
//
// The standard pagination helper is written for collections whose size is a
// product of usage, and its defaults reflect that. Under the pinned SDK:
//
//   - a nil or zero limit becomes 100 AND silently turns count_total ON, and
//     counting the total consumes the iterator to the end of the prefix;
//   - offset is implemented by advancing the iterator one row at a time, so the
//     work is the offset, not the page;
//   - a caller-supplied limit is not capped at all.
//
// The collections these surfaces expose are not small. One epoch can hold an
// entitlement per participating Slot — bounded by the ratified active-slot maximum
// times the maximum epoch length under churn — and reward-configuration history
// grows for the life of the chain and is never pruned. So the ordinary defaults
// mean a single unauthenticated query can make a node materialize a whole epoch or
// a whole history, which is a denial-of-service surface rather than a page.
//
// # The policy
//
// Refuse what cannot be served in bounded work, and bound what can:
//
//	offset      -> InvalidArgument   (its cost is the offset)
//	count_total -> InvalidArgument   (its cost is the collection)
//	reverse     -> InvalidArgument   (these surfaces promise ascending order)
//	limit       -> capped at maxCollectionPageSize; 0 means the cap, not "count"
//	key         -> supported, and is the way to page
//
// Refusing rather than silently clamping matters for the first two. A caller that
// asked for a total and received a response without one would have no way to tell
// it was not answered, and would go on believing a number it never got.
func boundedPage(page *query.PageRequest) (*query.PageRequest, error) {
	bounded := &query.PageRequest{Limit: maxCollectionPageSize}
	if page == nil {
		return bounded, nil
	}
	if page.Offset != 0 {
		return nil, status.Error(codes.InvalidArgument,
			"offset pagination is not supported; page with the key cursor returned in next_key")
	}
	if page.CountTotal {
		return nil, status.Error(codes.InvalidArgument,
			"count_total is not supported; counting a canonical collection is unbounded work")
	}
	if page.Reverse {
		return nil, status.Error(codes.InvalidArgument,
			"results are returned in canonical ascending order; reverse pagination is not supported")
	}
	if page.Limit != 0 && page.Limit < maxCollectionPageSize {
		bounded.Limit = page.Limit
	}
	bounded.Key = page.Key
	return bounded, nil
}

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
	// Every read below is of canonical module state that InitGenesis wrote and no
	// path deletes. There is no "not configured yet" answer to give: after
	// initialization, an absent or undecodable record is corruption, and the
	// contract for corruption is Internal. Returning the raw collections error
	// would surface it as an unclassified gRPC Unknown, which tells a client
	// nothing and is indistinguishable from a transport fault.
	state, err := q.GetState(ctx)
	if err != nil {
		return nil, canonicalStateQueryError("rewards state", err)
	}
	cfg, err := q.GetCurrentEpochConfig(ctx)
	if err != nil {
		return nil, canonicalStateQueryError("current epoch configuration", err)
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
		return nil, canonicalStateQueryError("open reward-enabled block count", err)
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
		return nil, canonicalStateQueryError("pending params", err)
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
	// Read through the monetary boundary, not the raw Params.
	//
	// This response exists to be believed: it publishes the escrow balance beside
	// the two quantities it must equal. Selecting the denom from an unvalidated
	// Params would let a chain whose monetary configuration execution REFUSES to act
	// on publish a confident-looking, perfectly balanced tuple denominated in a
	// token the protocol does not account in. The query and the block path have to
	// agree on what a usable monetary configuration is, so they use one seam.
	params, err := q.MonetaryParams(ctx)
	if err != nil {
		return nil, canonicalStateQueryError("monetary params", err)
	}
	denom := params.NativeDenom
	rewardsAddr := q.accountKeeper.GetModuleAddress(types.ModuleName)
	feePoolAddr := q.accountKeeper.GetModuleAddress(types.FeePoolName)
	if rewardsAddr == nil || feePoolAddr == nil {
		return nil, status.Error(codes.FailedPrecondition, "rewards module accounts are not registered")
	}
	// The two quantities the escrow balance must cover, returned beside it so
	// solvency is checkable from the response alone rather than by correlating
	// three separate queries.
	state, err := q.GetState(ctx)
	if err != nil {
		return nil, canonicalStateQueryError("rewards state", err)
	}
	liability, err := q.GetOutstandingEntitlementLiability(ctx)
	if err != nil {
		return nil, canonicalStateQueryError("outstanding entitlement liability", err)
	}
	// The carry is parsed rather than copied through.
	//
	// The whole point of this response is that solvency is checkable from it:
	// rewards_balance == liability + carry. Forwarding an unparseable carry would
	// publish a triple that cannot be added up, and a client would have to work out
	// on its own that one member is not a number. The liability already fails
	// closed for the same reason, so the two are treated alike.
	carry, err := types.ParseAmountString("carry forward remainder", state.CarryForwardRemainder)
	if err != nil {
		return nil, canonicalStateQueryError("carry forward remainder", err)
	}
	return &types.QueryModuleBalancesResponse{
		Denom:                           denom,
		RewardsBalance:                  q.bankKeeper.GetBalance(ctx, rewardsAddr, denom).Amount.String(),
		FeePoolBalance:                  q.bankKeeper.GetBalance(ctx, feePoolAddr, denom).Amount.String(),
		OutstandingEntitlementLiability: liability.String(),
		CarryForwardRemainder:           carry.String(),
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

// canonicalStateQueryError maps a failed read of canonical module state onto the
// query contract.
//
// The distinction epochQueryError draws does not apply here, and pretending it
// does would be the bug. None of these records is optional: genesis writes all of
// them and nothing removes them, so there is no modeled absence for a client to
// be told about. collections.ErrNotFound on one of them means the store lost a
// record, which is a state-integrity failure and reaches the caller as Internal —
// never as NotFound, which would read as "this chain has no rewards state".
//
// An error already carrying a transport code is passed through, so a canceled or
// deadline-exceeded query is not relabelled as corruption.
func canonicalStateQueryError(what string, err error) error {
	if err == nil {
		return nil
	}
	if _, ok := status.FromError(err); ok && status.Code(err) != codes.Unknown {
		return err
	}
	return status.Errorf(codes.Internal, "canonical %s could not be read: %v", what, err)
}

// EpochConfigVersions returns the canonical epoch-configuration history together
// with any future schedule.
//
// Both collections are paginated, and independently: history is append-only and
// grows with every accepted geometry change, while the schedule is transient and
// grows with pending ones. An unbounded walk of either would let a single query
// force the node to materialize an entire collection, and the schedule is the
// more exposed of the two because its size is not bounded by chain age.
//
// Rows are validated on the way out. A malformed row is not history — presenting
// it as though it were would let a client build a boundary derivation on a record
// consensus itself would refuse, so the query fails closed exactly as the block
// path does.
func (q queryServer) EpochConfigVersions(
	ctx context.Context, req *types.QueryEpochConfigVersionsRequest,
) (*types.QueryEpochConfigVersionsResponse, error) {
	var historyPage *query.PageRequest
	var startEpoch uint64
	var limit uint32
	if req != nil {
		historyPage = req.Pagination
		startEpoch = req.ScheduledStartEpoch
		limit = req.ScheduledLimit
	}
	versions, pageRes, err := query.CollectionPaginate(
		ctx, q.Keeper.EpochConfigVersions, historyPage,
		func(key uint64, version types.EpochConfigVersion) (*types.EpochConfigVersion, error) {
			if err := validateEpochConfigRecord(key, version); err != nil {
				return nil, err
			}
			value := version
			return &value, nil
		},
	)
	if err != nil {
		return nil, epochQueryError(err)
	}
	scheduled, nextEpoch, err := q.scheduledEpochConfigPage(ctx, startEpoch, limit)
	if err != nil {
		return nil, epochQueryError(err)
	}
	return &types.QueryEpochConfigVersionsResponse{
		Versions:           versions,
		Scheduled:          scheduled,
		Pagination:         pageRes,
		ScheduledNextEpoch: nextEpoch,
	}, nil
}

// scheduledEpochConfigPage returns one bounded window of the future schedule,
// ascending by effective epoch, together with the cursor for the next window.
//
// It walks at most limit+1 entries: the extra one is read to learn whether the
// schedule continues, and is reported as a cursor rather than returned. So the
// work this query can be made to do is bounded by the caller's limit, capped by
// the server, and independent of how large the schedule actually is.
func (q queryServer) scheduledEpochConfigPage(
	ctx context.Context, startEpoch uint64, limit uint32,
) ([]*types.ScheduledEpochConfig, uint64, error) {
	size := uint64(limit)
	if size == 0 || size > maxScheduledEpochConfigsPerPage {
		size = maxScheduledEpochConfigsPerPage
	}
	rng := new(collections.Range[uint64])
	if startEpoch > 0 {
		rng = rng.StartInclusive(startEpoch)
	}
	iter, err := q.ScheduledEpochConfigs.Iterate(ctx, rng)
	if err != nil {
		return nil, 0, types.ErrInvalidState.Wrapf(
			"scheduled epoch configurations could not be read: %v", err)
	}
	defer iter.Close()

	page := make([]*types.ScheduledEpochConfig, 0, size)
	for ; iter.Valid(); iter.Next() {
		key, err := iter.Key()
		if err != nil {
			return nil, 0, types.ErrInvalidState.Wrapf(
				"scheduled epoch configuration key could not be read: %v", err)
		}
		if uint64(len(page)) == size {
			// One past the window: reported as the next cursor, not returned.
			return page, key, nil
		}
		value, err := iter.Value()
		if err != nil {
			return nil, 0, types.ErrInvalidState.Wrapf(
				"scheduled epoch configuration at epoch %d could not be read: %v", key, err)
		}
		if err := ValidateScheduledEpochConfigRecord(key, value); err != nil {
			return nil, 0, err
		}
		entry := value
		page = append(page, &entry)
	}
	return page, 0, nil
}

// EpochBoundaries returns the canonical start and end height of one epoch.
//
// Boundaries beyond the supported derivation horizon are refused rather than
// clamped or invented (§68).
//
// All three returned numbers describe ONE geometry. Start and end come from the
// schedule-aware projection, so the length must too — resolving it separately
// from immutable history alone would answer a different question for an epoch a
// pending schedule entry has already changed, and would publish a triple whose
// own members contradict each other.
func (q queryServer) EpochBoundaries(
	ctx context.Context, req *types.QueryEpochBoundariesRequest,
) (*types.QueryEpochBoundariesResponse, error) {
	if req == nil || req.EpochNumber == 0 {
		return nil, status.Error(codes.InvalidArgument, "epoch number must be positive")
	}
	// The successor is a real arithmetic step, not a formality: at the maximum
	// epoch number an unchecked +1 wraps to zero and the projection would then
	// answer about epoch 0 — an epoch that cannot exist — instead of failing.
	successor, err := checked.AddUint64(req.EpochNumber, 1)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument,
			"epoch %d has no representable successor, so its end height is not derivable",
			req.EpochNumber)
	}
	start, err := q.ProjectEpochStartHeight(ctx, req.EpochNumber, epochProjectionHorizon)
	if err != nil {
		return nil, epochQueryError(err)
	}
	next, err := q.ProjectEpochStartHeight(ctx, successor, epochProjectionHorizon)
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
	span, err := checked.SubUint64(next, start)
	if err != nil || span == 0 {
		return nil, epochQueryError(types.ErrInvalidState.Wrapf(
			"epoch %d projects to the empty span [%d, %d]", req.EpochNumber, start, next))
	}
	return &types.QueryEpochBoundariesResponse{
		EpochNumber:       req.EpochNumber,
		StartHeight:       start,
		EndHeight:         end,
		EpochLengthBlocks: span,
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

// SlotEntitlement returns one canonical obligation.
//
// Absence is a genuine NotFound: a Slot that did not participate, or whose share
// floored to zero, is owed nothing and has no record. Anything else — a row that
// disagrees with its key, an unparseable amount, a released amount above the
// entitlement — is state that exists and cannot be trusted, and is Internal. A
// client must be able to tell "nothing is owed" from "the chain cannot say".
func (q queryServer) SlotEntitlement(
	ctx context.Context, req *types.QuerySlotEntitlementRequest,
) (*types.QuerySlotEntitlementResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if req.SlotId == 0 || req.Epoch == 0 {
		return nil, status.Error(codes.InvalidArgument, "slot id and epoch must be positive")
	}
	entitlement, found, err := q.GetSlotEntitlement(ctx, req.SlotId, req.Epoch)
	if err != nil {
		return nil, canonicalStateQueryError("slot entitlement", err)
	}
	if !found {
		return nil, status.Errorf(codes.NotFound,
			"no entitlement exists for slot %d in epoch %d", req.SlotId, req.Epoch)
	}
	governing, err := q.RewardConfigForTarget(ctx, req.Epoch)
	if err != nil {
		return nil, canonicalStateQueryError("governing reward configuration", err)
	}
	if err := q.validateEntitlementRelations(entitlement, governing); err != nil {
		return nil, canonicalStateQueryError("slot entitlement", err)
	}
	return &types.QuerySlotEntitlementResponse{Entitlement: &entitlement}, nil
}

// validateEntitlementRelations applies the rules an entitlement cannot be checked
// against on its own.
//
// Two, both about the record's relationship to state outside it: the destination
// it names must still be an admissible one, and the configuration version it
// records must be the one that governed its epoch. A query that returned a record
// failing either would be publishing state as authoritative that the release path
// would refuse to act on — which is worse than an error, because a client would
// have no way to tell.
//
// The governing configuration is passed in rather than resolved here, so an
// enumeration over one epoch resolves it once for the whole page.
func (q queryServer) validateEntitlementRelations(
	entitlement types.SlotEntitlement, governing types.RewardConfigVersion,
) error {
	if err := q.validatePayoutDestination(
		fmt.Sprintf("entitlement for slot %d in epoch %d", entitlement.SlotId, entitlement.Epoch),
		entitlement.PayoutAddress,
	); err != nil {
		return err
	}
	if governing.Version != entitlement.RewardConfigVersion {
		return types.ErrInvalidState.Wrapf(
			"entitlement for slot %d in epoch %d records reward configuration version %d, but epoch %d is governed by version %d",
			entitlement.SlotId, entitlement.Epoch, entitlement.RewardConfigVersion,
			entitlement.Epoch, governing.Version)
	}
	return nil
}

// SlotEntitlementsByEpoch returns one epoch's obligations, ascending by slot_id.
//
// The ordering comes from the canonical key rather than from a sort applied here,
// so a client paging through the epoch sees the same sequence consensus does —
// which is what later settlement materialization will walk.
//
// # Why reverse pagination is refused rather than served
//
// The response promises ascending order, and the standard PageRequest carries a
// reverse flag that would silently break that promise. Two contracts cannot both
// be true, and the one to keep is the canonical one: settlement materialization
// walks this sequence, so a client that built against a descending page would be
// building against an order the protocol does not have. Refusing is also the only
// answer that stays honest as the surface grows — quietly re-sorting a reversed
// page would make the flag look supported while doing the opposite of what it
// says.
func (q queryServer) SlotEntitlementsByEpoch(
	ctx context.Context, req *types.QuerySlotEntitlementsByEpochRequest,
) (*types.QuerySlotEntitlementsByEpochResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if req.Epoch == 0 {
		return nil, status.Error(codes.InvalidArgument, "epoch must be positive")
	}
	// Bounded before anything is read, so a request this server will not serve
	// cannot walk a single row of the epoch on its way to being refused.
	page, err := boundedPage(req.Pagination)
	if err != nil {
		return nil, err
	}
	// Resolved once for the whole page. The governing configuration is a property
	// of the epoch, and the epoch is fixed by the request, so per-row resolution
	// would repeat one lookup for every entitlement returned.
	governing, err := q.RewardConfigForTarget(ctx, req.Epoch)
	if err != nil {
		return nil, canonicalStateQueryError("governing reward configuration", err)
	}
	entitlements, pageRes, err := query.CollectionPaginate(
		ctx, q.SlotEntitlements, page,
		func(key collections.Pair[uint64, uint64], entitlement types.SlotEntitlement) (*types.SlotEntitlement, error) {
			if err := validateEntitlementRecord(key, entitlement); err != nil {
				return nil, err
			}
			if err := q.validateEntitlementRelations(entitlement, governing); err != nil {
				return nil, err
			}
			value := entitlement
			return &value, nil
		},
		query.WithCollectionPaginationPairPrefix[uint64, uint64](req.Epoch),
	)
	if err != nil {
		return nil, canonicalStateQueryError("slot entitlements", err)
	}
	return &types.QuerySlotEntitlementsByEpochResponse{
		Entitlements: entitlements,
		Pagination:   pageRes,
	}, nil
}

// RewardConfigVersions returns the canonical reward-configuration history and the
// single pending change, if one exists.
//
// The schedule is a field rather than a paginated list because at most one entry
// is valid state (§82). Paginating it would suggest a queue the protocol does not
// have, and would hide the case this response makes visible instead: a schedule
// holding anything other than the one admissible entry is corruption, and is
// reported as such rather than returned as a page of rows.
func (q queryServer) RewardConfigVersions(
	ctx context.Context, req *types.QueryRewardConfigVersionsRequest,
) (*types.QueryRewardConfigVersionsResponse, error) {
	var requested *query.PageRequest
	if req != nil {
		requested = req.Pagination
	}
	// Bounded before anything is read. Reward-configuration history grows for the
	// life of the chain and is never pruned, so it is the surface where an unbounded
	// default costs the most.
	page, err := boundedPage(requested)
	if err != nil {
		return nil, err
	}
	// The permanent anchor, once for the whole page. A history that has lost it is
	// not a shorter history, and no page drawn from it is trustworthy.
	if err := q.requireRewardConfigAnchor(ctx); err != nil {
		return nil, canonicalStateQueryError("reward configuration history", err)
	}
	// Ordering is checked across the whole page and at its leading edge.
	//
	// The predecessor seek runs once, for the first row returned; every later row
	// is compared against the one before it, which the walk already has in hand. So
	// each edge that touches the page is checked, at a cost bounded by the page
	// rather than by the history behind it.
	var previous *types.RewardConfigVersion
	versions, pageRes, err := query.CollectionPaginate(
		// q.Keeper.X, not q.X: the method below shadows the promoted collection
		// field of the same name inside its own body.
		ctx, q.Keeper.RewardConfigVersions, page,
		func(key uint64, version types.RewardConfigVersion) (*types.RewardConfigVersion, error) {
			if err := q.validateResolvedRewardConfig(key, version); err != nil {
				return nil, err
			}
			if previous == nil {
				if err := q.validateAdjacentRewardConfigEdge(ctx, version); err != nil {
					return nil, err
				}
			} else if previous.Version+1 != version.Version || previous.EffectiveEpoch >= version.EffectiveEpoch {
				// Contiguity, not merely increase. A page that presented 1 then 3 as
				// canonical history would be publishing a sequence in which version 2
				// is simultaneously in range and absent — which is the state the
				// version lookup classifies as corruption, so the enumeration must not
				// serve it as ordinary data.
				return nil, types.ErrInvalidState.Wrapf(
					"reward configuration version %d at effective epoch %d does not immediately follow version %d at effective epoch %d; "+
						"canonical versions are contiguous",
					version.Version, version.EffectiveEpoch, previous.Version, previous.EffectiveEpoch)
			}
			value := version
			previous = &value
			return &value, nil
		},
	)
	if err != nil {
		return nil, canonicalStateQueryError("reward configuration history", err)
	}

	response := &types.QueryRewardConfigVersionsResponse{Versions: versions, Pagination: pageRes}
	state, err := q.GetState(ctx)
	if err != nil {
		return nil, canonicalStateQueryError("rewards state", err)
	}
	effectiveEpoch, err := checked.AddUint64(state.CurrentEpoch, 1)
	if err != nil {
		return nil, status.Errorf(codes.Internal,
			"epoch %d has no representable successor to schedule against", state.CurrentEpoch)
	}
	if err := q.assertSingleScheduledRewardConfig(ctx, effectiveEpoch); err != nil {
		return nil, canonicalStateQueryError("scheduled reward configuration", err)
	}
	scheduled, found, err := q.ScheduledRewardConfigFor(ctx, effectiveEpoch)
	if err != nil {
		return nil, canonicalStateQueryError("scheduled reward configuration", err)
	}
	if found {
		response.Scheduled = &scheduled
	}
	return response, nil
}

// RewardConfigVersion returns one historical reward configuration by its identity
// (§67).
//
// # The selectors are identity, not applicability
//
// Exactly one of version and effective_epoch must be set, and both name a stored
// record rather than asking which configuration applies somewhere. That
// distinction is the reason the target-binding rule is absent from this surface:
// which version governs a target is the N-2 rule, it is derived rather than
// stored, and exposing a lookup that answered it would give clients a second place
// to learn something consensus already decides. What a client actually needs is
// the record an entitlement names — an identity lookup — and that is what this is.
//
// effective_epoch is an EXACT key read, not the predecessor seek the block path
// performs. A configuration effective at epoch 4 is not "at" epoch 7, even though
// it governs it.
//
// Absence is a genuine NotFound: a version that was never accepted, or an epoch at
// which nothing became effective, is an ordinary answer. Malformed canonical state
// is Internal, and the record returned has passed the same rules a consequential
// read applies — so a client cannot be handed a configuration the chain itself
// would refuse to compute with.
func (q queryServer) RewardConfigVersion(
	ctx context.Context, req *types.QueryRewardConfigVersionRequest,
) (*types.QueryRewardConfigVersionResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if (req.Version == 0) == (req.EffectiveEpoch == 0) {
		return nil, status.Error(codes.InvalidArgument,
			"exactly one of version and effective_epoch must be set")
	}

	var (
		version types.RewardConfigVersion
		found   bool
		err     error
	)
	if req.Version != 0 {
		version, found, err = q.RewardConfigVersionByNumber(ctx, req.Version)
	} else {
		version, found, err = q.RewardConfigVersionAtEffectiveEpoch(ctx, req.EffectiveEpoch)
	}
	if err != nil {
		return nil, canonicalStateQueryError("reward configuration version", err)
	}
	if !found {
		if req.Version != 0 {
			return nil, status.Errorf(codes.NotFound,
				"no reward configuration version %d exists", req.Version)
		}
		return nil, status.Errorf(codes.NotFound,
			"no reward configuration became effective at epoch %d", req.EffectiveEpoch)
	}
	return &types.QueryRewardConfigVersionResponse{Version: &version}, nil
}
