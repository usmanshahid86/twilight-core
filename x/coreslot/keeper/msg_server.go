package keeper

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/x/coreslot/types"
)

type msgServer struct{ Keeper }

func NewMsgServer(k Keeper) types.MsgServer { return msgServer{Keeper: k} }

// cancelRotationAndEmit cancels any pending consensus-key rotation for the slot
// during a lifecycle change (F1) and emits coreslot_rotation_cancelled.
func (m msgServer) cancelRotationAndEmit(ctx context.Context, slot types.CoreSlot) error {
	rotation, cancelled, err := m.cancelPendingRotation(ctx, slot.SlotId)
	if err != nil {
		return err
	}
	if cancelled {
		// The slot's current key (unchanged by cancellation) is the "old"/current
		// consensus address; the staged-but-never-active key is the "new" one.
		curConsAddr, _, err := consensusKey(slot.ConsensusPubkey)
		if err != nil {
			return err
		}
		newConsAddr, _, err := consensusKey(rotation.NewPubkey)
		if err != nil {
			return err
		}
		height := sdk.UnwrapSDKContext(ctx).BlockHeight()
		emitRotationCancelled(ctx, slot.SlotId, slot.OperatorAddress, curConsAddr, newConsAddr, types.RotationCancelReasonLifecycle, height)
	}
	return nil
}

func (m msgServer) RegisterCoreSlot(ctx context.Context, msg *types.MsgRegisterCoreSlot) (*types.MsgRegisterCoreSlotResponse, error) {
	params, err := m.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	if msg.Authority != params.Authority && !(params.AllowSelfRegistration && msg.Authority == msg.OperatorAddress) {
		return nil, types.ErrUnauthorized
	}
	if _, err := sdk.AccAddressFromBech32(msg.OperatorAddress); err != nil {
		return nil, err
	}
	if _, err := sdk.AccAddressFromBech32(msg.PayoutAddress); err != nil {
		return nil, err
	}
	if err := types.ValidateMetadata(msg.Metadata); err != nil {
		return nil, err
	}
	if exists, err := m.ByOperator.Has(ctx, msg.OperatorAddress); err != nil {
		return nil, err
	} else if exists {
		return nil, types.ErrDuplicateOperator
	}
	key, _, err := m.ensureConsensusAvailable(ctx, msg.ConsensusPubkey)
	if err != nil {
		return nil, err
	}
	id, err := m.NextSlotID.Get(ctx)
	if err != nil || id == 0 {
		id = 1
	}
	height := sdk.UnwrapSDKContext(ctx).BlockHeight()
	slot := types.CoreSlot{
		SlotId: id, OperatorAddress: msg.OperatorAddress, ConsensusPubkey: msg.ConsensusPubkey,
		PayoutAddress: msg.PayoutAddress, Status: types.SlotStatus_SLOT_STATUS_PENDING,
		RewardWeight: types.DefaultRewardWeight, CreatedHeight: height, UpdatedHeight: height, Metadata: msg.Metadata,
	}
	if err := m.Slots.Set(ctx, id, slot); err != nil {
		return nil, err
	}
	if err := m.ByOperator.Set(ctx, slot.OperatorAddress, id); err != nil {
		return nil, err
	}
	if err := m.ByConsensus.Set(ctx, key, id); err != nil {
		return nil, err
	}
	if err := m.RewardWeights.Set(ctx, id, types.OperatorRewardWeight{
		SlotId: id, BaseWeight: types.DefaultRewardWeight, UptimeWeight: types.DefaultRewardWeight,
		PerformanceWeight: types.DefaultRewardWeight, StakeWeight: "0.000000000000000000",
		ExternalWeight: "0.000000000000000000", FinalWeight: types.DefaultRewardWeight, UpdatedHeight: height,
	}); err != nil {
		return nil, err
	}
	if err := m.NextSlotID.Set(ctx, id+1); err != nil {
		return nil, err
	}
	emitRegistered(ctx, id, slot.OperatorAddress, key)
	return &types.MsgRegisterCoreSlotResponse{SlotId: id}, nil
}

func (m msgServer) ActivateCoreSlot(ctx context.Context, msg *types.MsgActivateCoreSlot) (*types.MsgActivateCoreSlotResponse, error) {
	params, err := m.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	if msg.Authority != params.Authority {
		return nil, types.ErrUnauthorized
	}
	slot, err := m.getSlot(ctx, msg.SlotId)
	if err != nil {
		return nil, err
	}
	if slot.Status != types.SlotStatus_SLOT_STATUS_PENDING && slot.Status != types.SlotStatus_SLOT_STATUS_INACTIVE && slot.Status != types.SlotStatus_SLOT_STATUS_SUSPENDED {
		return nil, types.ErrInvalidTransition
	}
	count, err := m.activeCount(ctx)
	if err != nil {
		return nil, err
	}
	if count >= params.MaxActiveSlots {
		return nil, types.ErrMaxActiveSlots
	}
	consAddr, _, err := consensusKey(slot.ConsensusPubkey)
	if err != nil {
		return nil, err
	}
	oldStatus := slot.Status
	height := sdk.UnwrapSDKContext(ctx).BlockHeight()
	slot.Status, slot.ConsensusPower, slot.ActivatedHeight, slot.UpdatedHeight = types.SlotStatus_SLOT_STATUS_ACTIVE, params.SlotVotingPower, height, height
	if err := m.Slots.Set(ctx, slot.SlotId, slot); err != nil {
		return nil, err
	}
	emitActivated(ctx, slot.SlotId, slot.OperatorAddress, oldStatus, consAddr, slot.ConsensusPower)
	return &types.MsgActivateCoreSlotResponse{}, nil
}

func (m msgServer) InactivateCoreSlot(ctx context.Context, msg *types.MsgInactivateCoreSlot) (*types.MsgInactivateCoreSlotResponse, error) {
	params, err := m.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	slot, err := m.getSlot(ctx, msg.SlotId)
	if err != nil {
		return nil, err
	}
	if msg.AuthorityOrOperator != params.Authority && msg.AuthorityOrOperator != slot.OperatorAddress {
		return nil, types.ErrUnauthorized
	}
	if slot.Status != types.SlotStatus_SLOT_STATUS_ACTIVE {
		return nil, types.ErrInvalidTransition
	}
	count, err := m.activeCount(ctx)
	if err != nil {
		return nil, err
	}
	if count <= params.MinActiveSlots {
		return nil, types.ErrMinActiveSlots
	}
	consAddr, _, err := consensusKey(slot.ConsensusPubkey)
	if err != nil {
		return nil, err
	}
	oldStatus := slot.Status
	slot.Status, slot.ConsensusPower, slot.UpdatedHeight = types.SlotStatus_SLOT_STATUS_INACTIVE, 0, sdk.UnwrapSDKContext(ctx).BlockHeight()
	if err := m.Slots.Set(ctx, slot.SlotId, slot); err != nil {
		return nil, err
	}
	if err := m.cancelRotationAndEmit(ctx, slot); err != nil {
		return nil, err
	}
	emitInactivated(ctx, slot.SlotId, slot.OperatorAddress, consAddr, oldStatus, msg.Reason)
	return &types.MsgInactivateCoreSlotResponse{}, nil
}

func (m msgServer) SuspendCoreSlot(ctx context.Context, msg *types.MsgSuspendCoreSlot) (*types.MsgSuspendCoreSlotResponse, error) {
	params, err := m.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	if msg.Authority != params.Authority && msg.Authority != params.EmergencyAuthority {
		return nil, types.ErrUnauthorized
	}
	slot, err := m.getSlot(ctx, msg.SlotId)
	if err != nil {
		return nil, err
	}
	if slot.Status == types.SlotStatus_SLOT_STATUS_REMOVED || slot.Status == types.SlotStatus_SLOT_STATUS_SUSPENDED {
		return nil, types.ErrInvalidTransition
	}
	if slot.Status == types.SlotStatus_SLOT_STATUS_ACTIVE {
		count, err := m.activeCount(ctx)
		if err != nil {
			return nil, err
		}
		// AllowEmergencyBelowMinActive may permit crossing MinActiveSlots, but
		// a hard floor of one active validator is enforced in all cases (F6):
		// draining the set to zero would halt CometBFT.
		if count <= 1 {
			return nil, types.ErrCannotRemoveLastValidator
		}
		if !params.AllowEmergencyBelowMinActive && count <= params.MinActiveSlots {
			return nil, types.ErrMinActiveSlots
		}
	}
	consAddr, _, err := consensusKey(slot.ConsensusPubkey)
	if err != nil {
		return nil, err
	}
	oldStatus := slot.Status
	height := sdk.UnwrapSDKContext(ctx).BlockHeight()
	slot.Status, slot.ConsensusPower, slot.SuspendedHeight, slot.UpdatedHeight = types.SlotStatus_SLOT_STATUS_SUSPENDED, 0, height, height
	if err := m.Slots.Set(ctx, slot.SlotId, slot); err != nil {
		return nil, err
	}
	if err := m.cancelRotationAndEmit(ctx, slot); err != nil {
		return nil, err
	}
	emitSuspended(ctx, slot.SlotId, slot.OperatorAddress, consAddr, oldStatus, msg.Reason)
	return &types.MsgSuspendCoreSlotResponse{}, nil
}

func (m msgServer) RemoveCoreSlot(ctx context.Context, msg *types.MsgRemoveCoreSlot) (*types.MsgRemoveCoreSlotResponse, error) {
	params, err := m.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	if msg.Authority != params.Authority {
		return nil, types.ErrUnauthorized
	}
	slot, err := m.getSlot(ctx, msg.SlotId)
	if err != nil {
		return nil, err
	}
	if slot.Status == types.SlotStatus_SLOT_STATUS_ACTIVE || slot.Status == types.SlotStatus_SLOT_STATUS_REMOVED {
		return nil, types.ErrInvalidTransition.Wrap("active slots must be inactivated or suspended before removal")
	}
	key, addr, err := consensusKey(slot.ConsensusPubkey)
	if err != nil {
		return nil, err
	}
	oldStatus := slot.Status
	height := sdk.UnwrapSDKContext(ctx).BlockHeight()
	slot.Status, slot.ConsensusPower, slot.RemovedHeight, slot.UpdatedHeight = types.SlotStatus_SLOT_STATUS_REMOVED, 0, height, height
	if err := m.Slots.Set(ctx, slot.SlotId, slot); err != nil {
		return nil, err
	}
	// Defensive: removal is only reachable from a non-active slot, whose pending
	// rotation (if any) was already cancelled on inactivate/suspend, but cancel
	// again so a REMOVED slot can never carry a stale rotation (F1).
	if err := m.cancelRotationAndEmit(ctx, slot); err != nil {
		return nil, err
	}
	_ = m.ByOperator.Remove(ctx, slot.OperatorAddress)
	_ = m.ByConsensus.Remove(ctx, key)
	if err := m.Reserved.Set(ctx, key, types.ReservedConsensusAddress{
		ConsAddress: addr, SlotId: slot.SlotId, ReservedUntil: height + int64(params.ConsensusKeyReuseLockout), Reason: msg.Reason,
	}); err != nil {
		return nil, err
	}
	emitRemoved(ctx, slot.SlotId, slot.OperatorAddress, oldStatus, key, msg.Reason)
	return &types.MsgRemoveCoreSlotResponse{}, nil
}

func (m msgServer) RotateConsensusKey(ctx context.Context, msg *types.MsgRotateConsensusKey) (*types.MsgRotateConsensusKeyResponse, error) {
	params, err := m.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	if msg.Authority != params.Authority {
		return nil, types.ErrUnauthorized
	}
	slot, err := m.getSlot(ctx, msg.SlotId)
	if err != nil {
		return nil, err
	}
	if slot.Status == types.SlotStatus_SLOT_STATUS_REMOVED {
		return nil, types.ErrInvalidTransition
	}
	// Reject a second rotation while one is already queued (F2). This check runs
	// before any mutation (in particular before ensureConsensusAvailable, which
	// may free a reservation) so a rejected request leaves state untouched.
	if pending, err := m.Rotations.Has(ctx, slot.SlotId); err != nil {
		return nil, err
	} else if pending {
		return nil, types.ErrPendingRotationExists
	}
	oldKey, oldAddr, err := consensusKey(slot.ConsensusPubkey)
	if err != nil {
		return nil, err
	}
	newKey, _, err := m.ensureConsensusAvailable(ctx, msg.NewConsensusPubkey)
	if err != nil {
		return nil, err
	}
	if err := m.ByConsensus.Set(ctx, newKey, slot.SlotId); err != nil {
		return nil, err
	}
	height := sdk.UnwrapSDKContext(ctx).BlockHeight()
	if slot.Status == types.SlotStatus_SLOT_STATUS_ACTIVE {
		effectiveHeight := height + int64(params.KeyRotationDelayBlocks)
		if err := m.Rotations.Set(ctx, slot.SlotId, types.PendingKeyRotation{
			SlotId: slot.SlotId, OldPubkey: slot.ConsensusPubkey, NewPubkey: msg.NewConsensusPubkey,
			RequestedHeight: height, EffectiveHeight: effectiveHeight,
		}); err != nil {
			return nil, err
		}
		emitKeyRotationRequested(ctx, slot.SlotId, slot.OperatorAddress, oldKey, newKey, effectiveHeight)
		return &types.MsgRotateConsensusKeyResponse{}, nil
	}
	_ = m.ByConsensus.Remove(ctx, oldKey)
	slot.ConsensusPubkey, slot.UpdatedHeight = msg.NewConsensusPubkey, height
	if err := m.Slots.Set(ctx, slot.SlotId, slot); err != nil {
		return nil, err
	}
	if err := m.Reserved.Set(ctx, oldKey, types.ReservedConsensusAddress{
		ConsAddress: oldAddr, SlotId: slot.SlotId, ReservedUntil: height + int64(params.ConsensusKeyReuseLockout), Reason: "key rotation",
	}); err != nil {
		return nil, err
	}
	// Non-active rotation takes effect immediately (no CometBFT update); power is
	// 0 for a non-active slot and the effective height is the current block.
	emitKeyRotated(ctx, slot.SlotId, slot.OperatorAddress, oldKey, newKey, slot.ConsensusPower, height)
	return &types.MsgRotateConsensusKeyResponse{}, nil
}

func (m msgServer) UpdatePayoutAddress(ctx context.Context, msg *types.MsgUpdatePayoutAddress) (*types.MsgUpdatePayoutAddressResponse, error) {
	slot, err := m.getSlot(ctx, msg.SlotId)
	if err != nil {
		return nil, err
	}
	if msg.Operator != slot.OperatorAddress {
		return nil, types.ErrUnauthorized
	}
	if _, err := sdk.AccAddressFromBech32(msg.NewPayoutAddress); err != nil {
		return nil, err
	}
	slot.PayoutAddress, slot.UpdatedHeight = msg.NewPayoutAddress, sdk.UnwrapSDKContext(ctx).BlockHeight()
	if err := m.Slots.Set(ctx, slot.SlotId, slot); err != nil {
		return nil, err
	}
	emitPayoutUpdated(ctx, slot.SlotId, slot.OperatorAddress)
	return &types.MsgUpdatePayoutAddressResponse{}, nil
}

func (m msgServer) UpdateOperatorMetadata(ctx context.Context, msg *types.MsgUpdateOperatorMetadata) (*types.MsgUpdateOperatorMetadataResponse, error) {
	slot, err := m.getSlot(ctx, msg.SlotId)
	if err != nil {
		return nil, err
	}
	if msg.Operator != slot.OperatorAddress {
		return nil, types.ErrUnauthorized
	}
	if err := types.ValidateMetadata(msg.Metadata); err != nil {
		return nil, err
	}
	slot.Metadata, slot.UpdatedHeight = msg.Metadata, sdk.UnwrapSDKContext(ctx).BlockHeight()
	if err := m.Slots.Set(ctx, slot.SlotId, slot); err != nil {
		return nil, err
	}
	emitMetadataUpdated(ctx, slot.SlotId, slot.OperatorAddress)
	return &types.MsgUpdateOperatorMetadataResponse{}, nil
}

func (m msgServer) UpdateParams(ctx context.Context, msg *types.MsgUpdateParams) (*types.MsgUpdateParamsResponse, error) {
	if msg.Params == nil {
		return nil, types.ErrInvalidParams.Wrap("params are required")
	}
	current, err := m.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	if msg.Authority != current.Authority {
		return nil, types.ErrUnauthorized
	}
	if err := msg.Params.Validate(); err != nil {
		return nil, err
	}
	count, err := m.activeCount(ctx)
	if err != nil {
		return nil, err
	}
	if count > 0 && msg.Params.SlotVotingPower != current.SlotVotingPower {
		return nil, types.ErrInvalidParams.Wrap("cannot change voting power while active slots exist")
	}
	if err := m.Params.Set(ctx, *msg.Params); err != nil {
		return nil, err
	}
	emitParamsUpdated(ctx, msg.Authority)
	return &types.MsgUpdateParamsResponse{}, nil
}
