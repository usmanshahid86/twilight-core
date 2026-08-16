package keeper

import (
	"context"
	"encoding/hex"
	"errors"

	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"cosmossdk.io/collections"

	"github.com/cosmos/cosmos-sdk/types/query"

	"github.com/twilight-project/twilight-core/x/coreslot/types"
)

// grpcStatusError attaches a gRPC status code to a module error without
// discarding the error itself.
//
// Returning a bare status would throw away the very thing being reported: the
// module distinguishes ordinary absence from state corruption, and an in-process
// caller comparing with errors.Is is entitled to see which one it got. Wrapping
// keeps that chain intact while status.FromError — used by the gRPC server and by
// the REST gateway to pick an HTTP code — finds the code through GRPCStatus.
type grpcStatusError struct {
	code codes.Code
	err  error
}

func (e grpcStatusError) Error() string { return e.err.Error() }

func (e grpcStatusError) Unwrap() error { return e.err }

func (e grpcStatusError) GRPCStatus() *grpcstatus.Status {
	return grpcstatus.New(e.code, e.err.Error())
}

// policyQueryError maps a keeper-level Selection-policy error onto the public
// query contract.
//
// A slot or version that does not exist is an ordinary answer and must reach a
// caller as NotFound — a REST 404 rather than a server error. State that exists
// and cannot be trusted is categorically different: a history/index
// contradiction, or a stored value that will not decode, stays a fail-closed
// internal fault and must never be flattened into "not found", which would tell
// a client the data was never written when the database holding it is broken.
//
// Unreadable stored state is classified where it is READ, not here, for two
// reasons. The read site knows which collection failed and can say so, and
// deciding it here would mean a blanket default that swallowed errors already
// carrying a meaningful transport code — a canceled or timed-out query would be
// reported as chain corruption. So anything still unclassified by the time it
// reaches this mapper is passed through with whatever code it already has.
func policyQueryError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, types.ErrSlotNotFound), errors.Is(err, types.ErrSelectionPolicyNotFound):
		return grpcStatusError{code: codes.NotFound, err: err}
	case errors.Is(err, types.ErrInvalidSelectionPolicy):
		return grpcStatusError{code: codes.Internal, err: err}
	default:
		return err
	}
}

type queryServer struct{ Keeper }

func NewQueryServer(k Keeper) types.QueryServer { return queryServer{Keeper: k} }

func (q queryServer) Params(ctx context.Context, _ *types.QueryParamsRequest) (*types.QueryParamsResponse, error) {
	params, err := q.Keeper.Params.Get(ctx)
	return &types.QueryParamsResponse{Params: &params}, err
}

func (q queryServer) CoreSlot(ctx context.Context, req *types.QueryCoreSlotRequest) (*types.QueryCoreSlotResponse, error) {
	slot, err := q.getSlot(ctx, req.SlotId)
	return &types.QueryCoreSlotResponse{Slot: &slot}, err
}

func (q queryServer) CoreSlots(ctx context.Context, req *types.QueryCoreSlotsRequest) (*types.QueryCoreSlotsResponse, error) {
	var pageReq *query.PageRequest
	status := types.SlotStatus_SLOT_STATUS_UNSPECIFIED
	if req != nil {
		pageReq = req.Pagination
		status = req.Status
	}

	slots, pageRes, err := query.CollectionFilteredPaginate(
		ctx,
		q.Slots,
		pageReq,
		func(_ uint64, slot types.CoreSlot) (bool, error) {
			return status == types.SlotStatus_SLOT_STATUS_UNSPECIFIED || status == slot.Status, nil
		},
		func(_ uint64, slot types.CoreSlot) (*types.CoreSlot, error) {
			slotCopy := slot
			return &slotCopy, nil
		},
	)
	if err != nil {
		return nil, err
	}
	return &types.QueryCoreSlotsResponse{Slots: slots, Pagination: pageRes}, nil
}

// ActiveCoreSlots reads the ACTIVE membership index through the keeper's own
// enumeration rather than scanning every slot ever registered and filtering.
//
// Sharing GetActiveSlots is the point: there is exactly one active-set
// enumeration in the module, so this query inherits its bound (proportional to
// the active set, itself capped at HardMaxActiveCoreSlots), its ascending
// slot-ID order, CoreSlot as the authoritative payload, and its fail-closed
// behavior when index and records disagree. A second implementation here could
// drift from all four.
//
// The result is unpaginated because the active set is immutably bounded at 100;
// that bound is what makes returning it whole safe, not an assumption about
// deployment size.
func (q queryServer) ActiveCoreSlots(ctx context.Context, _ *types.QueryActiveCoreSlotsRequest) (*types.QueryCoreSlotsResponse, error) {
	slots, err := q.GetActiveSlots(ctx)
	if err != nil {
		return nil, err
	}
	resp := &types.QueryCoreSlotsResponse{Slots: make([]*types.CoreSlot, 0, len(slots))}
	for i := range slots {
		resp.Slots = append(resp.Slots, &slots[i])
	}
	return resp, nil
}

func (q queryServer) CoreSlotByOperator(ctx context.Context, req *types.QueryCoreSlotByOperatorRequest) (*types.QueryCoreSlotResponse, error) {
	id, err := q.ByOperator.Get(ctx, req.OperatorAddress)
	if err != nil {
		return nil, types.ErrSlotNotFound
	}
	return q.CoreSlot(ctx, &types.QueryCoreSlotRequest{SlotId: id})
}

func (q queryServer) CoreSlotByConsensusAddress(ctx context.Context, req *types.QueryCoreSlotByConsensusAddressRequest) (*types.QueryCoreSlotResponse, error) {
	raw, err := hex.DecodeString(req.ConsensusAddress)
	if err != nil {
		return nil, err
	}
	id, err := q.ByConsensus.Get(ctx, hex.EncodeToString(raw))
	if err != nil {
		return nil, types.ErrSlotNotFound
	}
	return q.CoreSlot(ctx, &types.QueryCoreSlotRequest{SlotId: id})
}

func (q queryServer) PendingKeyRotations(ctx context.Context, _ *types.QueryPendingKeyRotationsRequest) (*types.QueryPendingKeyRotationsResponse, error) {
	resp := &types.QueryPendingKeyRotationsResponse{}
	err := q.Rotations.Walk(ctx, nil, func(_ uint64, rotation types.PendingKeyRotation) (bool, error) {
		rotationCopy := rotation
		resp.Rotations = append(resp.Rotations, &rotationCopy)
		return false, nil
	})
	return resp, err
}

func (q queryServer) LastAppliedValidators(ctx context.Context, _ *types.QueryLastAppliedValidatorsRequest) (*types.QueryLastAppliedValidatorsResponse, error) {
	resp := &types.QueryLastAppliedValidatorsResponse{}
	err := q.LastApplied.Walk(ctx, nil, func(_ string, validator types.LastAppliedValidator) (bool, error) {
		validatorCopy := validator
		resp.Validators = append(resp.Validators, &validatorCopy)
		return false, nil
	})
	return resp, err
}

func (q queryServer) ReservedConsensusAddress(ctx context.Context, req *types.QueryReservedConsensusAddressRequest) (*types.QueryReservedConsensusAddressResponse, error) {
	reservation, err := q.Reserved.Get(ctx, req.ConsensusAddress)
	return &types.QueryReservedConsensusAddressResponse{Reservation: &reservation}, err
}

// SelectionPolicy returns the version a slot currently points at. It resolves
// through the stored pointer rather than by scanning history, so it answers the
// same question the ACTIVE-slot invariant asks.
func (q queryServer) SelectionPolicy(ctx context.Context, req *types.QuerySelectionPolicyRequest) (*types.QuerySelectionPolicyResponse, error) {
	slot, err := q.getSlot(ctx, req.SlotId)
	if err != nil && !errors.Is(err, types.ErrSlotNotFound) {
		// The record is present but unreadable. getSlot deliberately propagates
		// that rather than calling it absence, and the classification belongs
		// here: to this query it is a failure to resolve a policy, not an answer
		// about whether the slot exists. Message handlers keep the raw error.
		return nil, policyQueryError(types.ErrInvalidSelectionPolicy.Wrapf(
			"slot %d record could not be read: %v", req.SlotId, err))
	}
	if err != nil {
		return nil, policyQueryError(err)
	}
	policy, err := q.currentPolicy(ctx, slot)
	if err != nil {
		return nil, policyQueryError(err)
	}
	return &types.QuerySelectionPolicyResponse{Policy: &policy}, nil
}

// SelectionPolicyVersion returns one exact historical version. Absence is
// reported as not-found rather than as an empty response, so a caller cannot
// mistake "no such version" for "a version with zero values".
func (q queryServer) SelectionPolicyVersion(ctx context.Context, req *types.QuerySelectionPolicyVersionRequest) (*types.QuerySelectionPolicyResponse, error) {
	policy, err := q.SelectionPolicies.Get(ctx, policyKey(req.SlotId, req.PolicyVersion))
	if err != nil {
		// Only an absent key is "no such version". A present key whose bytes will
		// not decode is a storage failure, and reporting it as absence would tell
		// a client the version was never written when in fact it cannot be read.
		if errors.Is(err, collections.ErrNotFound) {
			return nil, policyQueryError(types.ErrSelectionPolicyNotFound.Wrapf("slot %d version %d", req.SlotId, req.PolicyVersion))
		}
		return nil, policyQueryError(types.ErrInvalidSelectionPolicy.Wrapf(
			"slot %d version %d could not be read: %v", req.SlotId, req.PolicyVersion, err))
	}
	// The row was addressed by (slot, version), so its stored identity has to
	// agree with the key it was found under. A row that disagrees is corruption
	// rather than an answer: returning it would hand the caller some other slot's
	// or version's policy under the identity it asked about, and reporting it as
	// not-found would hide a contradiction behind an ordinary absence.
	if policy.SlotId != req.SlotId || policy.PolicyVersion != req.PolicyVersion {
		return nil, policyQueryError(types.ErrInvalidSelectionPolicy.Wrapf(
			"slot %d version %d row identity does not match its key", req.SlotId, req.PolicyVersion))
	}
	return &types.QuerySelectionPolicyResponse{Policy: &policy}, nil
}

// SelectionPolicyAtHeight returns the version whose half-open interval contains
// the requested height, resolved through the seek index.
func (q queryServer) SelectionPolicyAtHeight(ctx context.Context, req *types.QuerySelectionPolicyAtHeightRequest) (*types.QuerySelectionPolicyResponse, error) {
	policy, err := q.Keeper.SelectionPolicyAtHeight(ctx, req.SlotId, req.AtHeight)
	if err != nil {
		return nil, policyQueryError(err)
	}
	return &types.QuerySelectionPolicyResponse{Policy: &policy}, nil
}

func (q queryServer) RewardWeight(ctx context.Context, req *types.QueryRewardWeightRequest) (*types.QueryRewardWeightResponse, error) {
	weight, err := q.RewardWeights.Get(ctx, req.SlotId)
	return &types.QueryRewardWeightResponse{RewardWeight: &weight}, err
}
