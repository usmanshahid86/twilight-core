package keeper

import (
	"context"

	"cosmossdk.io/collections"

	"github.com/cosmos/cosmos-sdk/types/query"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/twilight-project/twilight-core/x/mining/types"
)

// The read surface a settlement worker recovers from.
//
// # Classification is the contract
//
// Every handler distinguishes three outcomes and never blurs them:
//
//	InvalidArgument   the request itself is malformed
//	NotFound          genuine, modeled absence
//	Internal          canonical state that exists and cannot be trusted
//
// Turning corruption into NotFound would be the worst failure available to a
// read-only surface: a worker reconciling its own position cannot distinguish "this
// never existed" from "this exists and I could not read it", and would resume as
// though an obligation it actually owes had never been created. Nothing below
// synthesizes a default to complete a response, and nothing repairs state.
//
// # Bounds
//
// Every listing is bounded server-side and independent of chain lifetime. These are
// API resource bounds, not economic parameters, so they are constants here rather
// than consensus state.

// maxCollectionPageSize bounds one page of any canonical collection query, matching
// the bound the rewards query surface already applies.
const maxCollectionPageSize = 100

type queryServer struct{ Keeper }

func NewQueryServer(k Keeper) types.QueryServer { return queryServer{Keeper: k} }

// boundedPage caps a caller's page request and refuses the request shapes whose
// cost is not bounded by the page.
//
// offset walks the iterator row by row, and count_total consumes it to the end of
// the prefix, so either would let a caller ask for work proportional to the whole
// collection while appearing to request one page. The key cursor is the supported
// way to continue.
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

// corrupt maps unreadable or self-contradictory canonical state onto Internal.
//
// The message is preserved because it names which invariant failed, which is what
// an operator triaging a halted worker needs; the CODE is what stops a client
// treating it as absence.
func corrupt(err error) error {
	return status.Error(codes.Internal, err.Error())
}

func (q queryServer) SettlementClock(
	ctx context.Context, _ *types.QuerySettlementClockRequest,
) (*types.QuerySettlementClockResponse, error) {
	// No default. Genesis writes the clock explicitly, so an absent value on an
	// initialized chain is corruption — reporting zero would tell a worker every
	// deadline had just been reset.
	clock, err := q.GetSettlementClock(ctx)
	if err != nil {
		return nil, corrupt(err)
	}
	return &types.QuerySettlementClockResponse{SettlementClock: clock}, nil
}

// Settlement returns one settlement together with the correlated and derived values
// a worker needs to decide its next operation.
func (q queryServer) Settlement(
	ctx context.Context, req *types.QuerySettlementRequest,
) (*types.QuerySettlementResponse, error) {
	if req == nil || req.SlotId == 0 || req.Epoch == 0 {
		return nil, status.Error(codes.InvalidArgument, "slot id and epoch must be positive")
	}
	settlement, found, err := q.GetSettlement(ctx, req.SlotId, req.Epoch)
	if err != nil {
		return nil, corrupt(err)
	}
	if !found {
		// Ordinary absence: most (slot, epoch) pairs never produce a settlement.
		return nil, status.Errorf(codes.NotFound,
			"no settlement exists for slot %d in epoch %d", req.SlotId, req.Epoch)
	}

	// The monetary authority is the entitlement, never the settlement. A settlement
	// whose entitlement is missing or names another obligation is corruption: the two
	// are created in the same transition.
	entitlement, err := q.requireEntitlementFor(ctx, settlement)
	if err != nil {
		return nil, corrupt(err)
	}
	amount, err := entitlement.Amount()
	if err != nil {
		return nil, corrupt(err)
	}
	released, err := entitlement.Released()
	if err != nil {
		return nil, corrupt(err)
	}
	if released.GT(amount) {
		return nil, status.Errorf(codes.Internal,
			"the entitlement for slot %d in epoch %d has released %s of %s",
			settlement.SlotId, settlement.Epoch, released, amount)
	}
	ceiling, err := ParticipantDistributionCeiling(settlement, entitlement)
	if err != nil {
		return nil, corrupt(err)
	}

	// The anchor is mandatory companion state for every mode, on the same terms
	// finalization applies: a settlement exists only because its epoch produced one.
	anchor, err := q.requireEpochAnchor(ctx, settlement.Epoch)
	if err != nil {
		return nil, corrupt(err)
	}
	clock, err := q.GetSettlementClock(ctx)
	if err != nil {
		return nil, corrupt(err)
	}

	deadline, permissionless, err := q.derivedFinalizationState(ctx, settlement, anchor, clock)
	if err != nil {
		return nil, corrupt(err)
	}

	return &types.QuerySettlementResponse{
		Settlement:                     &settlement,
		EntitlementAmount:              amount.String(),
		ReleasedAmount:                 released.String(),
		PayoutAddress:                  entitlement.PayoutAddress,
		RemainingAmount:                amount.Sub(released).String(),
		ParticipantDistributionCeiling: ceiling.String(),
		CreatedSettlementClock:         anchor.CreatedSettlementClock,
		DeadlineClock:                  deadline,
		PermissionlessFinalizationNow:  permissionless,
		CurrentSettlementClock:         clock,
	}, nil
}

// derivedFinalizationState computes the deadline and whether finalization is open to
// anyone, without introducing a dependency the transition itself does not have.
//
// An operator-only settlement is finalizable immediately by any valid account and
// never becomes deadline-gated, so nothing here resolves settlement parameters,
// derives a window or compares the clock for it. Its deadline is reported as the
// anchor clock, which is the derived representation the architecture defines — and
// reporting it must not be read as making it an authorization boundary.
//
// A finalized settlement reports false. There is no finalization left to perform, so
// a client must never read this field as an invitation to retry a terminal
// transition.
func (q queryServer) derivedFinalizationState(
	ctx context.Context,
	settlement types.Settlement,
	anchor types.SettlementEpochAnchor,
	clock uint64,
) (deadline uint64, permissionless bool, err error) {
	if settlement.SettlementMode == types.SettlementMode_SETTLEMENT_MODE_OPERATOR_ONLY {
		return anchor.CreatedSettlementClock, !settlement.Finalized, nil
	}

	params, err := q.SettlementParamsForTarget(ctx, settlement.Epoch)
	if err != nil {
		return 0, false, err
	}
	if params.Version != settlement.SettlementParamsVersion {
		return 0, false, types.ErrInvalidState.Wrapf(
			"the settlement for slot %d in epoch %d was created under settlement parameters version %d, "+
				"but epoch %d binds version %d",
			settlement.SlotId, settlement.Epoch, settlement.SettlementParamsVersion,
			settlement.Epoch, params.Version)
	}
	if err := requireAnchorHasElapsed(settlement, anchor, clock); err != nil {
		return 0, false, err
	}
	deadline, err = q.DeadlineClock(ctx, settlement, anchor, params)
	if err != nil {
		return 0, false, err
	}
	return deadline, !settlement.Finalized && clock >= deadline, nil
}

// OpenSettlements lists a Slot's outstanding settlements through the derived index.
//
// The index locates rows; it never decides whether one is open. Every entry is
// resolved to its canonical settlement and cross-checked, so a stale entry pointing
// at a finalized row, or at no row at all, is reported as corruption rather than
// answered around. There is no query-time repair: a divergence between a derived
// index and the rows it describes is corruption, not a cache to refresh.
func (q queryServer) OpenSettlements(
	ctx context.Context, req *types.QueryOpenSettlementsRequest,
) (*types.QueryOpenSettlementsResponse, error) {
	if req == nil || req.SlotId == 0 {
		return nil, status.Error(codes.InvalidArgument, "slot id must be positive")
	}
	page, err := boundedPage(req.Pagination)
	if err != nil {
		return nil, err
	}

	settlements, pageRes, err := query.CollectionPaginate(
		ctx, q.Keeper.OpenSettlementsBySlot, page,
		func(key collections.Pair[uint64, uint64], _ uint64) (*types.Settlement, error) {
			settlement, found, err := q.GetSettlement(ctx, key.K1(), key.K2())
			if err != nil {
				return nil, err
			}
			if !found {
				return nil, types.ErrInvalidState.Wrapf(
					"the open-settlement index names slot %d in epoch %d, which has no settlement",
					key.K1(), key.K2())
			}
			if settlement.Finalized {
				return nil, types.ErrInvalidState.Wrapf(
					"the open-settlement index names slot %d in epoch %d, which is finalized",
					key.K1(), key.K2())
			}
			return &settlement, nil
		},
		// Bounded to one Slot's prefix, so the cost tracks that Slot's outstanding
		// rows rather than the whole collection or its lifetime history.
		query.WithCollectionPaginationPairPrefix[uint64, uint64](req.SlotId),
	)
	if err != nil {
		return nil, corrupt(err)
	}
	return &types.QueryOpenSettlementsResponse{Settlements: settlements, Pagination: pageRes}, nil
}

// --- configuration history -------------------------------------------------

func (q queryServer) DistributionModeVersion(
	ctx context.Context, req *types.QueryDistributionModeVersionRequest,
) (*types.QueryDistributionModeVersionResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "a request is required")
	}
	record, err := exactVersion(
		ctx, q.Keeper.DistributionModeVersions, q.DistributionModeVersionIndex, req.Version,
		func(v types.MiningDistributionModeVersion) uint64 { return v.Version },
		validateModeRecord, "distribution mode",
	)
	if err != nil {
		return nil, err
	}
	return &types.QueryDistributionModeVersionResponse{Version: record}, nil
}

func (q queryServer) SelectionParamsVersion(
	ctx context.Context, req *types.QuerySelectionParamsVersionRequest,
) (*types.QuerySelectionParamsVersionResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "a request is required")
	}
	record, err := exactVersion(
		ctx, q.Keeper.SelectionParamsVersions, q.SelectionParamsVersionIndex, req.Version,
		func(v types.SelectionParamsVersion) uint64 { return v.Version },
		validateSelectionParamsRecord, "selection parameters",
	)
	if err != nil {
		return nil, err
	}
	return &types.QuerySelectionParamsVersionResponse{Version: record}, nil
}

func (q queryServer) SettlementParamsVersion(
	ctx context.Context, req *types.QuerySettlementParamsVersionRequest,
) (*types.QuerySettlementParamsVersionResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "a request is required")
	}
	record, err := exactVersion(
		ctx, q.Keeper.SettlementParamsVersions, q.SettlementParamsVersionIndex, req.Version,
		func(v types.SettlementParamsVersion) uint64 { return v.Version },
		validateSettlementParamsRecord, "settlement parameters",
	)
	if err != nil {
		return nil, err
	}
	return &types.QuerySettlementParamsVersionResponse{Version: record}, nil
}

// exactVersion runs the ratified lookup and maps its classification onto the public
// query codes.
//
// Only a PROVEN absence becomes NotFound. Everything the proof could not establish
// is Internal, which is the whole point: a lost index entry and a version that was
// never assigned look identical from the index alone, and answering the first as
// though it were the second would hide an existing record behind a clean 404.
func exactVersion[V any](
	ctx context.Context,
	history collections.Map[uint64, V],
	index collections.Map[uint64, uint64],
	version uint64,
	versionOf func(V) uint64,
	validate func(key uint64, record V) error,
	family string,
) (*V, error) {
	if version == 0 {
		return nil, status.Errorf(codes.InvalidArgument, "%s version numbers start at 1", family)
	}
	epochKey, class, err := resolveExactVersion(ctx, history, index, version, versionOf, validate, family)
	if err != nil {
		return nil, corrupt(err)
	}
	switch class {
	case versionAboveLatest, versionIntentionalGap:
		return nil, status.Errorf(codes.NotFound, "%s version %d does not exist", family, version)
	}
	record, err := history.Get(ctx, epochKey)
	if err != nil {
		return nil, status.Errorf(codes.Internal,
			"%s version %d could not be read at epoch %d: %v", family, version, epochKey, err)
	}
	return &record, nil
}

func (q queryServer) DistributionModeVersions(
	ctx context.Context, req *types.QueryDistributionModeVersionsRequest,
) (*types.QueryDistributionModeVersionsResponse, error) {
	var page *query.PageRequest
	if req != nil {
		page = req.Pagination
	}
	versions, pageRes, err := listVersions(ctx, q.Keeper.DistributionModeVersions, page, validateModeRecord)
	if err != nil {
		return nil, err
	}
	return &types.QueryDistributionModeVersionsResponse{Versions: versions, Pagination: pageRes}, nil
}

func (q queryServer) SelectionParamsVersions(
	ctx context.Context, req *types.QuerySelectionParamsVersionsRequest,
) (*types.QuerySelectionParamsVersionsResponse, error) {
	var page *query.PageRequest
	if req != nil {
		page = req.Pagination
	}
	versions, pageRes, err := listVersions(ctx, q.Keeper.SelectionParamsVersions, page, validateSelectionParamsRecord)
	if err != nil {
		return nil, err
	}
	return &types.QuerySelectionParamsVersionsResponse{Versions: versions, Pagination: pageRes}, nil
}

func (q queryServer) SettlementParamsVersions(
	ctx context.Context, req *types.QuerySettlementParamsVersionsRequest,
) (*types.QuerySettlementParamsVersionsResponse, error) {
	var page *query.PageRequest
	if req != nil {
		page = req.Pagination
	}
	versions, pageRes, err := listVersions(ctx, q.Keeper.SettlementParamsVersions, page, validateSettlementParamsRecord)
	if err != nil {
		return nil, err
	}
	return &types.QuerySettlementParamsVersionsResponse{Versions: versions, Pagination: pageRes}, nil
}

// listVersions pages one configuration history in canonical ascending order.
//
// A malformed record encountered while building a page fails the whole page. There
// is no best-effort listing: a history returned with one row quietly omitted or
// normalized would be a history the caller could not reconcile against, and a
// caller reconciling configuration is exactly who asks for this.
func listVersions[V any](
	ctx context.Context,
	history collections.Map[uint64, V],
	page *query.PageRequest,
	validate func(key uint64, record V) error,
) ([]*V, *query.PageResponse, error) {
	bounded, err := boundedPage(page)
	if err != nil {
		return nil, nil, err
	}
	records, pageRes, err := query.CollectionPaginate(ctx, history, bounded,
		func(key uint64, record V) (*V, error) {
			if err := validate(key, record); err != nil {
				return nil, err
			}
			value := record
			return &value, nil
		})
	if err != nil {
		return nil, nil, corrupt(err)
	}
	return records, pageRes, nil
}
