package keeper

import (
	"context"
	"encoding/hex"

	"github.com/cosmos/cosmos-sdk/types/query"

	"github.com/twilight-project/twilight-core/x/coreslot/types"
)

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
	if err != nil {
		return nil, err
	}
	policy, err := q.currentPolicy(ctx, slot)
	if err != nil {
		return nil, err
	}
	return &types.QuerySelectionPolicyResponse{Policy: &policy}, nil
}

// SelectionPolicyVersion returns one exact historical version. Absence is
// reported as not-found rather than as an empty response, so a caller cannot
// mistake "no such version" for "a version with zero values".
func (q queryServer) SelectionPolicyVersion(ctx context.Context, req *types.QuerySelectionPolicyVersionRequest) (*types.QuerySelectionPolicyResponse, error) {
	policy, err := q.SelectionPolicies.Get(ctx, policyKey(req.SlotId, req.PolicyVersion))
	if err != nil {
		return nil, types.ErrSelectionPolicyNotFound.Wrapf("slot %d version %d", req.SlotId, req.PolicyVersion)
	}
	return &types.QuerySelectionPolicyResponse{Policy: &policy}, nil
}

// SelectionPolicyAtHeight returns the version whose half-open interval contains
// the requested height, resolved through the seek index.
func (q queryServer) SelectionPolicyAtHeight(ctx context.Context, req *types.QuerySelectionPolicyAtHeightRequest) (*types.QuerySelectionPolicyResponse, error) {
	policy, err := q.Keeper.SelectionPolicyAtHeight(ctx, req.SlotId, req.AtHeight)
	if err != nil {
		return nil, err
	}
	return &types.QuerySelectionPolicyResponse{Policy: &policy}, nil
}

func (q queryServer) RewardWeight(ctx context.Context, req *types.QueryRewardWeightRequest) (*types.QueryRewardWeightResponse, error) {
	weight, err := q.RewardWeights.Get(ctx, req.SlotId)
	return &types.QueryRewardWeightResponse{RewardWeight: &weight}, err
}
