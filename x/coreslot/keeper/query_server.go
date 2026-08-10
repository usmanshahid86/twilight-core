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

func (q queryServer) ActiveCoreSlots(ctx context.Context, _ *types.QueryActiveCoreSlotsRequest) (*types.QueryCoreSlotsResponse, error) {
	resp := &types.QueryCoreSlotsResponse{}
	err := q.Slots.Walk(ctx, nil, func(_ uint64, slot types.CoreSlot) (bool, error) {
		if slot.Status == types.SlotStatus_SLOT_STATUS_ACTIVE {
			slotCopy := slot
			resp.Slots = append(resp.Slots, &slotCopy)
		}
		return false, nil
	})
	return resp, err
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

func (q queryServer) RewardWeight(ctx context.Context, req *types.QueryRewardWeightRequest) (*types.QueryRewardWeightResponse, error) {
	weight, err := q.RewardWeights.Get(ctx, req.SlotId)
	return &types.QueryRewardWeightResponse{RewardWeight: &weight}, err
}
