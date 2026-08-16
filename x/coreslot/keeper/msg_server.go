package keeper

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"

	appparams "github.com/twilight-project/twilight-core/app/params"
	"github.com/twilight-project/twilight-core/internal/checked"
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
	// Registration is authority-only. Architecture §19 states that fresh V2.2
	// exposes no permissionless self-registration consensus path, so the branch
	// that consulted Params.AllowSelfRegistration is gone rather than merely
	// disabled: the parameter is deprecated and admission already refuses a true
	// value, but authorization must not depend on a stored flag either way.
	if msg.Authority != params.Authority {
		return nil, types.ErrUnauthorized
	}
	// Address admission, after the authorization check so an unauthorized caller
	// learns nothing about which addresses the chain would accept, and before any
	// state is touched.
	//
	// The fields are held to DIFFERENT rules on purpose. The operator address is
	// a control identity: §18 requires it to be valid, but the protocol never
	// sends to it, so refusing a bank-blocked operator would deny an operator the
	// protocol permits. The payout and settlement addresses are where value
	// actually goes and take the full canonical economic rule (§25).
	if _, err := m.economicAddresses.ParseAccountAddress(msg.OperatorAddress); err != nil {
		return nil, types.ErrInvalidAddress.Wrapf("operator address: %v", err)
	}
	if _, err := m.economicAddresses.Validate(msg.PayoutAddress); err != nil {
		return nil, types.ErrInvalidAddress.Wrapf("payout address: %v", err)
	}
	// Mandatory from normal V2.2 registration onward (§24) — for the PENDING row
	// this creates, not only once the slot activates.
	if _, err := m.economicAddresses.Validate(msg.SettlementAddress); err != nil {
		return nil, types.ErrInvalidAddress.Wrapf("settlement address: %v", err)
	}
	if err := types.ValidateMetadata(msg.Metadata); err != nil {
		return nil, err
	}
	if msg.InitialSelectionPolicy == nil {
		return nil, types.ErrInvalidSelectionPolicy.Wrap("an initial selection policy is required")
	}
	if err := types.ValidateSelectionPolicyValues(msg.InitialSelectionPolicy.SelectionRateBps, msg.InitialSelectionPolicy.MaxSelectedParticipants); err != nil {
		return nil, err
	}
	if exists, err := m.ByOperator.Has(ctx, msg.OperatorAddress); err != nil {
		return nil, err
	} else if exists {
		return nil, types.ErrDuplicateOperator
	}
	// Resolved BEFORE ensureConsensusAvailable, which may release a stale
	// reservation and is therefore the first thing in this handler that writes.
	// A counter fault is knowable without touching state, so it is settled while
	// a rejection still costs nothing — the same reason every other predictable
	// failure here is checked ahead of the first Set.
	id, err := m.nextSlotID(ctx)
	if err != nil {
		return nil, err
	}
	key, _, err := m.ensureConsensusAvailable(ctx, msg.ConsensusPubkey)
	if err != nil {
		return nil, err
	}
	height := sdk.UnwrapSDKContext(ctx).BlockHeight()
	slot := types.CoreSlot{
		SlotId: id, OperatorAddress: msg.OperatorAddress, ConsensusPubkey: msg.ConsensusPubkey,
		PayoutAddress: msg.PayoutAddress, Status: types.SlotStatus_SLOT_STATUS_PENDING,
		RewardWeight: types.DefaultRewardWeight, CreatedHeight: height, UpdatedHeight: height, Metadata: msg.Metadata,
		SettlementAddress: msg.SettlementAddress,
		// A newly registered slot has never been activated, so the whole
		// activation generation stays at the zero sentinel and the slot does not
		// enter the ACTIVE membership index.
		ActivationSequence:              0,
		ActivationEffectiveHeight:       0,
		CurrentSelectionPolicyVersion:   initialPolicyVersion,
		LastSelectionPolicyUpdateHeight: 0,
	}
	if err := m.Slots.Set(ctx, id, slot); err != nil {
		return nil, err
	}
	if err := m.writeInitialPolicy(ctx, id, height, msg.InitialSelectionPolicy); err != nil {
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
	// Checked: an unchecked increment at the top of the range would wrap to zero
	// and hand the next registration an identifier that is already in use.
	nextID, err := checked.AddUint64(id, 1)
	if err != nil {
		return nil, types.ErrInvalidTransition.Wrapf("slot id space exhausted: %v", err)
	}
	if err := m.NextSlotID.Set(ctx, nextID); err != nil {
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
	// Two ceilings, both enforced. The configured operational maximum binds first
	// and governance may lower it; the immutable ceiling is the outer guarantee
	// that no state exceeds it whatever governance does. Params validation caps
	// the former by the latter, which makes the second check redundant today —
	// and that is exactly why it is asserted rather than assumed: a resource
	// closure that holds only while a configurable value was validated correctly
	// is not a closure.
	if count >= params.MaxActiveSlots {
		return nil, types.ErrMaxActiveSlots
	}
	if count >= appparams.HardMaxActiveCoreSlots {
		return nil, types.ErrMaxActiveSlots.Wrapf("hard maximum %d active core slots", appparams.HardMaxActiveCoreSlots)
	}
	consAddr, _, err := consensusKey(slot.ConsensusPubkey)
	if err != nil {
		return nil, err
	}
	oldStatus := slot.Status
	height := sdk.UnwrapSDKContext(ctx).BlockHeight()
	// The activation generation advances on EVERY successful activation, including
	// every reactivation, so a slot that lapsed and returned is distinguishable
	// from one that never left. Both increments are checked: consensus code does
	// not get to rely on Go's wrapping overflow, because a wrapped value is
	// committed identically by every node and so is indistinguishable from a
	// correct one.
	nextSequence, err := checked.AddUint64(slot.ActivationSequence, 1)
	if err != nil {
		return nil, types.ErrInvalidTransition.Wrapf("slot %d activation sequence exhausted: %v", slot.SlotId, err)
	}
	effectiveHeight, err := checked.AddInt64(height, 1)
	if err != nil {
		return nil, types.ErrInvalidTransition.Wrapf("slot %d activation effective height overflows: %v", slot.SlotId, err)
	}
	slot.Status, slot.ConsensusPower = types.SlotStatus_SLOT_STATUS_ACTIVE, params.SlotVotingPower
	slot.ActivationSequence = nextSequence
	slot.ActivatedHeight, slot.UpdatedHeight = height, height
	// Reward accounting samples CoreSlot state at BeginBlock, so a slot activated
	// in block H first earns credit in block H+1 (§20). Fresh genesis is the
	// explicit exception and uses initial_height for both (§21).
	slot.ActivationEffectiveHeight = effectiveHeight
	// The POST-transition record is what must satisfy the ACTIVE invariant. The
	// pre-transition row is PENDING or lapsed and would fail an ACTIVE-only check
	// for reasons activation is about to fix.
	if err := m.validateActiveSlotInvariant(ctx, slot); err != nil {
		return nil, err
	}
	if err := m.Slots.Set(ctx, slot.SlotId, slot); err != nil {
		return nil, err
	}
	if err := m.setSlotActive(ctx, slot.SlotId); err != nil {
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
	// Leaving ACTIVE drops membership in the same state transition that writes the
	// record, so index and status can never disagree at a block boundary.
	if err := m.clearSlotActive(ctx, slot.SlotId); err != nil {
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
	// Suspension is reachable from any non-terminal status, so this may be a
	// no-op removal for a slot that was not ACTIVE — Remove tolerates an absent
	// key, and unconditionally clearing is what keeps the invariant total.
	if err := m.clearSlotActive(ctx, slot.SlotId); err != nil {
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
	// Removal is only reachable from a non-active status, so this should already
	// be absent; clearing unconditionally means no path can leave the index
	// holding a terminal slot.
	if err := m.clearSlotActive(ctx, slot.SlotId); err != nil {
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
	if err := operatorMutationAllowed(slot); err != nil {
		return nil, err
	}
	// The stored operator identity was admitted canonically at registration and
	// is the authorization subject here, not a value destination; only the new
	// payout address is a fresh economic admission.
	if _, err := m.economicAddresses.Validate(msg.NewPayoutAddress); err != nil {
		return nil, types.ErrInvalidAddress.Wrapf("payout address: %v", err)
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
	if err := operatorMutationAllowed(slot); err != nil {
		return nil, err
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

// operatorMutationAllowed gates the operator-controlled configuration surface by
// slot status. Suspension freezes that surface until authority resolves the
// slot's lifecycle, and removal is terminal (§22, §24).
//
// Authority-only lifecycle and consensus remediation remain available under their
// own messages; this gate constrains what an operator may change about a slot,
// not what authority may do to it.
func operatorMutationAllowed(slot types.CoreSlot) error {
	switch slot.Status {
	case types.SlotStatus_SLOT_STATUS_PENDING,
		types.SlotStatus_SLOT_STATUS_ACTIVE,
		types.SlotStatus_SLOT_STATUS_INACTIVE:
		return nil
	default:
		return types.ErrInvalidTransition.Wrapf(
			"operator configuration is frozen for slot %d with status %s", slot.SlotId, slot.Status)
	}
}

func (m msgServer) UpdateSettlementAddress(ctx context.Context, msg *types.MsgUpdateSettlementAddress) (*types.MsgUpdateSettlementAddressResponse, error) {
	slot, err := m.getSlot(ctx, msg.SlotId)
	if err != nil {
		return nil, err
	}
	if msg.Operator != slot.OperatorAddress {
		return nil, types.ErrUnauthorized
	}
	if err := operatorMutationAllowed(slot); err != nil {
		return nil, err
	}
	// §24 requires an identical replacement to be rejected rather than silently
	// accepted. Compared before economic validation so a no-op is reported as a
	// no-op regardless of whether the stored value would still pass admission.
	if msg.SettlementAddress == slot.SettlementAddress {
		return nil, types.ErrNoOpUpdate.Wrapf("slot %d already uses this settlement address", slot.SlotId)
	}
	// The settlement address is a value destination (§25), so the replacement
	// takes the full economic rule — and takes it before any state is written.
	if _, err := m.economicAddresses.Validate(msg.SettlementAddress); err != nil {
		return nil, types.ErrInvalidAddress.Wrapf("settlement address: %v", err)
	}
	slot.SettlementAddress, slot.UpdatedHeight = msg.SettlementAddress, sdk.UnwrapSDKContext(ctx).BlockHeight()
	if err := m.Slots.Set(ctx, slot.SlotId, slot); err != nil {
		return nil, err
	}
	emitSettlementUpdated(ctx, slot.SlotId, slot.OperatorAddress)
	return &types.MsgUpdateSettlementAddressResponse{}, nil
}

func (m msgServer) UpdateSelectionPolicy(ctx context.Context, msg *types.MsgUpdateSelectionPolicy) (*types.MsgUpdateSelectionPolicyResponse, error) {
	params, err := m.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	slot, err := m.getSlot(ctx, msg.SlotId)
	if err != nil {
		return nil, err
	}
	if msg.Operator != slot.OperatorAddress {
		return nil, types.ErrUnauthorized
	}
	if err := operatorMutationAllowed(slot); err != nil {
		return nil, err
	}
	// §27 LOCAL validity only. There is no global ceiling on
	// max_selected_participants, and whether a policy must additionally satisfy a
	// global operational envelope is unresolved — x/coreslot cannot see those
	// parameters and must not read x/mining to find them.
	if err := types.ValidateSelectionPolicyValues(msg.SelectionRateBps, msg.MaxSelectedParticipants); err != nil {
		return nil, err
	}
	current, err := m.currentPolicy(ctx, slot)
	if err != nil {
		return nil, err
	}
	// §26 rejects an identical replacement. Checked before the cooldown so a no-op
	// is reported as a no-op rather than as a rate-limit failure.
	if current.SelectionRateBps == msg.SelectionRateBps && current.MaxSelectedParticipants == msg.MaxSelectedParticipants {
		return nil, types.ErrNoOpUpdate.Wrapf("slot %d already uses this selection policy", slot.SlotId)
	}

	height := sdk.UnwrapSDKContext(ctx).BlockHeight()
	// The cooldown reads the CURRENT STORED configured value, never a compile-time
	// default, and never derives its input from the policy history: the canonical
	// source is the stored CoreSlot field. A zero means the slot has had no
	// post-registration update, which §26 exempts — registration's version 1 does
	// not consume the cooldown, so exactly one immediate corrective update is
	// unrestricted.
	if slot.LastSelectionPolicyUpdateHeight != 0 {
		cooldown, err := checked.Int64FromUint64(params.SelectionPolicyUpdateCooldownBlocks)
		if err != nil {
			return nil, types.ErrInvalidParams.Wrapf("selection policy update cooldown is not representable: %v", err)
		}
		earliest, err := checked.AddInt64(slot.LastSelectionPolicyUpdateHeight, cooldown)
		if err != nil {
			return nil, types.ErrSelectionPolicyCooldown.Wrapf("slot %d cooldown window overflows: %v", slot.SlotId, err)
		}
		if height < earliest {
			return nil, types.ErrSelectionPolicyCooldown.Wrapf(
				"slot %d may update at height %d, current height %d", slot.SlotId, earliest, height)
		}
	}

	// Both increments are checked and computed BEFORE any write, so an exhausted
	// height or version space leaves the whole transition unperformed rather than
	// half-applied.
	effective, err := checked.AddInt64(height, 1)
	if err != nil {
		return nil, types.ErrInvalidSelectionPolicy.Wrapf("slot %d policy effective height overflows: %v", slot.SlotId, err)
	}
	nextVersion, err := checked.AddUint64(current.PolicyVersion, 1)
	if err != nil {
		return nil, types.ErrInvalidSelectionPolicy.Wrapf("slot %d policy version space exhausted: %v", slot.SlotId, err)
	}

	// The last thing checked before the first write is the seam itself: that the
	// stored current version is coherent with the index, and that neither
	// successor key is already occupied. A conflict here is malformed state, not a
	// user error, and it must be refused rather than overwritten.
	if err := m.validatePolicyTransitionSeam(ctx, slot, current, height, effective, nextVersion); err != nil {
		return nil, err
	}

	// Everything that can fail has now failed. From here the six writes of the
	// transition are performed together.
	//
	// Closing the outgoing version is the ONE permitted write to an existing
	// history row: its exclusive end moves 0 -> H+1 and nothing else about it
	// changes. Once closed it is immutable forever.
	closed := current
	closed.ValidUntilHeightExclusive = effective
	if err := m.SelectionPolicies.Set(ctx, policyKey(closed.SlotId, closed.PolicyVersion), closed); err != nil {
		return nil, err
	}
	if err := m.writePolicyVersion(ctx, types.SelectionPolicyVersion{
		SlotId:                    slot.SlotId,
		PolicyVersion:             nextVersion,
		SelectionRateBps:          msg.SelectionRateBps,
		MaxSelectedParticipants:   msg.MaxSelectedParticipants,
		ValidFromHeight:           effective,
		ValidUntilHeightExclusive: 0,
	}); err != nil {
		return nil, err
	}
	slot.CurrentSelectionPolicyVersion = nextVersion
	// The transaction height H, not the version's effective height H+1. §26's
	// cooldown is measured from when the update was accepted.
	slot.LastSelectionPolicyUpdateHeight = height
	slot.UpdatedHeight = height
	if err := m.Slots.Set(ctx, slot.SlotId, slot); err != nil {
		return nil, err
	}
	emitSelectionPolicyUpdated(ctx, slot.SlotId, slot.OperatorAddress, nextVersion, effective)
	return &types.MsgUpdateSelectionPolicyResponse{PolicyVersion: nextVersion}, nil
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
