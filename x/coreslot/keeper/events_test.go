package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/x/coreslot/keeper"
	"github.com/twilight-project/twilight-core/x/coreslot/types"
)

func countEvents(ctx sdk.Context, evtType string) int {
	n := 0
	for _, e := range ctx.EventManager().Events() {
		if e.Type == evtType {
			n++
		}
	}
	return n
}

func firstEvent(t *testing.T, ctx sdk.Context, evtType string) sdk.Event {
	t.Helper()
	for _, e := range ctx.EventManager().Events() {
		if e.Type == evtType {
			return e
		}
	}
	t.Fatalf("event %s not found", evtType)
	return sdk.Event{}
}

func attrValue(t *testing.T, e sdk.Event, key string) string {
	t.Helper()
	for _, a := range e.Attributes {
		if a.Key == key {
			return a.Value
		}
	}
	t.Fatalf("attribute %q not found on event %s", key, e.Type)
	return ""
}

func oneActiveGenesis(t *testing.T, k keeper.Keeper, ctx sdk.Context, authority, emergency string) string {
	t.Helper()
	params := types.DefaultParams(authority, emergency)
	op1 := sdk.AccAddress(append([]byte{2}, make([]byte, 19)...)).String()
	_, err := initGenesis(t, k, ctx, &types.GenesisState{Params: &params, Slots: []*types.CoreSlot{
		slot(t, 1, op1, 1, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
	}, NextSlotId: 2})
	require.NoError(t, err)
	return op1
}

// TestEndBlockEventsEmittedExactlyOnce proves the F3 double-emit is gone:
// CacheContext.write() already propagates cached events to the parent once, so
// EndBlock must not re-emit them.
func TestEndBlockEventsEmittedExactlyOnce(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	oneActiveGenesis(t, k, ctx, authority, emergency)
	msgs := keeper.NewMsgServer(k)

	op2 := sdk.AccAddress(append([]byte{3}, make([]byte, 19)...)).String()
	res, err := msgs.RegisterCoreSlot(ctx, registerMsg(t, authority, op2, op2, 2))
	require.NoError(t, err)
	_, err = msgs.ActivateCoreSlot(ctx, &types.MsgActivateCoreSlot{Authority: authority, SlotId: res.SlotId})
	require.NoError(t, err)

	// Isolate the events produced by this single EndBlock call.
	ctx = ctx.WithEventManager(sdk.NewEventManager())
	updates, err := k.EndBlock(ctx)
	require.NoError(t, err)
	require.Len(t, updates, 1)
	require.Equal(t, 1, countEvents(ctx, types.EventTypeValidatorUpdateEmitted),
		"new active slot must produce exactly one validator-update event")

	// Active key rotation applied in EndBlock: exactly one key_rotated event.
	_, err = msgs.RotateConsensusKey(ctx, &types.MsgRotateConsensusKey{Authority: authority, SlotId: 1, NewConsensusPubkey: pubkey(t, 9)})
	require.NoError(t, err)
	ctx = ctx.WithBlockHeight(2).WithEventManager(sdk.NewEventManager())
	_, err = k.EndBlock(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, countEvents(ctx, types.EventTypeKeyRotated))
	// old@0 + new@power for slot 1 => exactly two validator-update events, no dupes.
	require.Equal(t, 2, countEvents(ctx, types.EventTypeValidatorUpdateEmitted))
}

// TestFailedEndBlockDoesNotEmitCachedEvents proves that when endBlock errors
// after emitting cached events, write() is never called and the parent context
// sees no events.
func TestFailedEndBlockDoesNotEmitCachedEvents(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	twoActiveGenesis(t, k, ctx, authority, emergency)

	// Inject (bypassing validation) a due rotation for slot 1 whose new key equals
	// slot 2's active key. EndBlock applies the rotation (emitting key_rotated into
	// the cache) and then diffAndPersist fails with a duplicate consensus key.
	require.NoError(t, k.Rotations.Set(ctx, 1, types.PendingKeyRotation{
		SlotId: 1, OldPubkey: pubkey(t, 1), NewPubkey: pubkey(t, 2), RequestedHeight: 0, EffectiveHeight: 1,
	}))

	ctx = ctx.WithEventManager(sdk.NewEventManager())
	_, err := k.EndBlock(ctx)
	require.ErrorIs(t, err, types.ErrDuplicateConsensusKey)

	require.Zero(t, countEvents(ctx, types.EventTypeKeyRotated), "cached key_rotated must not surface on failure")
	require.Zero(t, countEvents(ctx, types.EventTypeValidatorUpdateEmitted))
	require.Empty(t, ctx.EventManager().Events(), "failed EndBlock must emit no events")
}

func TestValidatorUpdateEventHasSlotAndOperator(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	oneActiveGenesis(t, k, ctx, authority, emergency)
	msgs := keeper.NewMsgServer(k)

	op2 := sdk.AccAddress(append([]byte{3}, make([]byte, 19)...)).String()
	res, err := msgs.RegisterCoreSlot(ctx, registerMsg(t, authority, op2, op2, 2))
	require.NoError(t, err)
	_, err = msgs.ActivateCoreSlot(ctx, &types.MsgActivateCoreSlot{Authority: authority, SlotId: res.SlotId})
	require.NoError(t, err)

	ctx = ctx.WithEventManager(sdk.NewEventManager())
	updates, err := k.EndBlock(ctx)
	require.NoError(t, err)
	require.Len(t, updates, 1)

	ev := firstEvent(t, ctx, types.EventTypeValidatorUpdateEmitted)
	require.Equal(t, "2", attrValue(t, ev, types.AttributeKeySlotID))
	require.Equal(t, op2, attrValue(t, ev, types.AttributeKeyOperatorAddress))
	require.Equal(t, consAddrHex(t, pubkey(t, 2)), attrValue(t, ev, types.AttributeKeyConsensusAddress))
	require.Equal(t, "1", attrValue(t, ev, types.AttributeKeyPower))
	require.Equal(t, "1", attrValue(t, ev, types.AttributeKeyHeight))
}

func TestKeyRotationEventsContainOldAndNewConsensusAddresses(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	op1, _ := twoActiveGenesis(t, k, ctx, authority, emergency)
	msgs := keeper.NewMsgServer(k)

	_, err := msgs.RotateConsensusKey(ctx, &types.MsgRotateConsensusKey{Authority: authority, SlotId: 1, NewConsensusPubkey: pubkey(t, 9)})
	require.NoError(t, err)

	// Requested event carries old/new addresses and the effective height.
	reqEv := firstEvent(t, ctx, types.EventTypeKeyRotationRequested)
	require.Equal(t, "1", attrValue(t, reqEv, types.AttributeKeySlotID))
	require.Equal(t, op1, attrValue(t, reqEv, types.AttributeKeyOperatorAddress))
	require.Equal(t, consAddrHex(t, pubkey(t, 1)), attrValue(t, reqEv, types.AttributeKeyOldConsensusAddress))
	require.Equal(t, consAddrHex(t, pubkey(t, 9)), attrValue(t, reqEv, types.AttributeKeyNewConsensusAddress))
	require.Equal(t, "2", attrValue(t, reqEv, types.AttributeKeyEffectiveHeight))

	ctx = ctx.WithBlockHeight(2).WithEventManager(sdk.NewEventManager())
	_, err = k.EndBlock(ctx)
	require.NoError(t, err)

	ev := firstEvent(t, ctx, types.EventTypeKeyRotated)
	require.Equal(t, "1", attrValue(t, ev, types.AttributeKeySlotID))
	require.Equal(t, op1, attrValue(t, ev, types.AttributeKeyOperatorAddress))
	require.Equal(t, consAddrHex(t, pubkey(t, 1)), attrValue(t, ev, types.AttributeKeyOldConsensusAddress))
	require.Equal(t, consAddrHex(t, pubkey(t, 9)), attrValue(t, ev, types.AttributeKeyNewConsensusAddress))
	require.Equal(t, "1", attrValue(t, ev, types.AttributeKeyPower))
	require.Equal(t, "2", attrValue(t, ev, types.AttributeKeyEffectiveHeight))
}

func TestRotationCancelEventContainsReasonAndAddresses(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	op1, _ := twoActiveGenesis(t, k, ctx, authority, emergency)
	msgs := keeper.NewMsgServer(k)

	_, err := msgs.RotateConsensusKey(ctx, &types.MsgRotateConsensusKey{Authority: authority, SlotId: 1, NewConsensusPubkey: pubkey(t, 9)})
	require.NoError(t, err)
	_, err = msgs.InactivateCoreSlot(ctx, &types.MsgInactivateCoreSlot{AuthorityOrOperator: authority, SlotId: 1, Reason: "maintenance"})
	require.NoError(t, err)

	ev := firstEvent(t, ctx, types.EventTypeRotationCancelled)
	require.Equal(t, "1", attrValue(t, ev, types.AttributeKeySlotID))
	require.Equal(t, op1, attrValue(t, ev, types.AttributeKeyOperatorAddress))
	require.Equal(t, consAddrHex(t, pubkey(t, 1)), attrValue(t, ev, types.AttributeKeyOldConsensusAddress))
	require.Equal(t, consAddrHex(t, pubkey(t, 9)), attrValue(t, ev, types.AttributeKeyNewConsensusAddress))
	require.Equal(t, types.RotationCancelReasonLifecycle, attrValue(t, ev, types.AttributeKeyReason))
	require.Equal(t, "1", attrValue(t, ev, types.AttributeKeyHeight))
}

func TestInactivatedEventExactValues(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	op1, _ := twoActiveGenesis(t, k, ctx, authority, emergency)
	msgs := keeper.NewMsgServer(k)

	ctx = ctx.WithEventManager(sdk.NewEventManager())
	const reason = "scheduled maintenance"
	_, err := msgs.InactivateCoreSlot(ctx, &types.MsgInactivateCoreSlot{
		AuthorityOrOperator: authority,
		SlotId:              1,
		Reason:              reason,
	})
	require.NoError(t, err)

	require.Equal(t, 1, countEvents(ctx, types.EventTypeInactivated))
	ev := firstEvent(t, ctx, types.EventTypeInactivated)
	require.Equal(t, "1", attrValue(t, ev, types.AttributeKeySlotID))
	require.Equal(t, op1, attrValue(t, ev, types.AttributeKeyOperatorAddress))
	require.Equal(t, consAddrHex(t, pubkey(t, 1)), attrValue(t, ev, types.AttributeKeyConsensusAddress))
	require.Equal(t, types.SlotStatus_SLOT_STATUS_ACTIVE.String(), attrValue(t, ev, types.AttributeKeyOldStatus))
	require.Equal(t, types.SlotStatus_SLOT_STATUS_INACTIVE.String(), attrValue(t, ev, types.AttributeKeyNewStatus))
	require.Equal(t, "0", attrValue(t, ev, types.AttributeKeyPower))
	require.Equal(t, reason, attrValue(t, ev, types.AttributeKeyReason))
}

func TestSuspendedEventExactValues(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	op1, _ := twoActiveGenesis(t, k, ctx, authority, emergency)
	msgs := keeper.NewMsgServer(k)

	ctx = ctx.WithEventManager(sdk.NewEventManager())
	const reason = "verified evidence"
	_, err := msgs.SuspendCoreSlot(ctx, &types.MsgSuspendCoreSlot{
		Authority: emergency,
		SlotId:    1,
		Reason:    reason,
	})
	require.NoError(t, err)

	require.Equal(t, 1, countEvents(ctx, types.EventTypeSuspended))
	ev := firstEvent(t, ctx, types.EventTypeSuspended)
	require.Equal(t, "1", attrValue(t, ev, types.AttributeKeySlotID))
	require.Equal(t, op1, attrValue(t, ev, types.AttributeKeyOperatorAddress))
	require.Equal(t, consAddrHex(t, pubkey(t, 1)), attrValue(t, ev, types.AttributeKeyConsensusAddress))
	require.Equal(t, types.SlotStatus_SLOT_STATUS_ACTIVE.String(), attrValue(t, ev, types.AttributeKeyOldStatus))
	require.Equal(t, types.SlotStatus_SLOT_STATUS_SUSPENDED.String(), attrValue(t, ev, types.AttributeKeyNewStatus))
	require.Equal(t, "0", attrValue(t, ev, types.AttributeKeyPower))
	require.Equal(t, reason, attrValue(t, ev, types.AttributeKeyReason))
}

func TestStaleRotationCancelEventExactValues(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	op1, _ := twoActiveGenesis(t, k, ctx, authority, emergency)
	msgs := keeper.NewMsgServer(k)

	newPK := pubkey(t, 9)
	_, err := msgs.RotateConsensusKey(ctx, &types.MsgRotateConsensusKey{
		Authority:          authority,
		SlotId:             1,
		NewConsensusPubkey: newPK,
	})
	require.NoError(t, err)

	// Bypass the lifecycle handler so the queued rotation remains stale and
	// EndBlock must defensively drop it without mutating the removed slot. The
	// ACTIVE membership index is cleared alongside the status: this fixture is
	// about the stale rotation, and leaving the index disagreeing with the record
	// would instead exercise the index-divergence guard.
	before, err := k.Slots.Get(ctx, 1)
	require.NoError(t, err)
	removed := before
	removed.Status = types.SlotStatus_SLOT_STATUS_REMOVED
	removed.ConsensusPower = 0
	require.NoError(t, k.Slots.Set(ctx, 1, removed))
	require.NoError(t, k.ActiveSlots.Remove(ctx, 1))

	ctx = ctx.WithBlockHeight(2).WithEventManager(sdk.NewEventManager())
	_, err = k.EndBlock(ctx)
	require.NoError(t, err)

	require.Equal(t, 1, countEvents(ctx, types.EventTypeRotationCancelled))
	ev := firstEvent(t, ctx, types.EventTypeRotationCancelled)
	require.Equal(t, "1", attrValue(t, ev, types.AttributeKeySlotID))
	require.Equal(t, op1, attrValue(t, ev, types.AttributeKeyOperatorAddress))
	require.Equal(t, consAddrHex(t, pubkey(t, 1)), attrValue(t, ev, types.AttributeKeyOldConsensusAddress))
	require.Equal(t, consAddrHex(t, newPK), attrValue(t, ev, types.AttributeKeyNewConsensusAddress))
	require.Equal(t, types.RotationCancelReasonStale, attrValue(t, ev, types.AttributeKeyReason))
	require.Equal(t, "2", attrValue(t, ev, types.AttributeKeyHeight))

	staged, err := k.ByConsensus.Has(ctx, consAddrHex(t, newPK))
	require.NoError(t, err)
	require.False(t, staged, "stale staged key must be released")
	pending, err := k.Rotations.Has(ctx, 1)
	require.NoError(t, err)
	require.False(t, pending, "stale rotation must be removed")
	after, err := k.Slots.Get(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, removed.Status, after.Status)
	require.Equal(t, consAddrHex(t, before.ConsensusPubkey), consAddrHex(t, after.ConsensusPubkey),
		"stale rotation must not mutate the slot consensus key")
}

// TestEventAttributesComplete drives the full lifecycle and asserts every event
// type carries its required (non-empty) attributes.
func TestEventAttributesComplete(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	op1, op2 := twoActiveGenesis(t, k, ctx, authority, emergency)
	_ = op1
	_ = op2
	msgs := keeper.NewMsgServer(k)

	op3 := sdk.AccAddress(append([]byte{4}, make([]byte, 19)...)).String()
	res3, err := msgs.RegisterCoreSlot(ctx, registerMsg(t, authority, op3, op3, 3))
	require.NoError(t, err)
	_, err = msgs.ActivateCoreSlot(ctx, &types.MsgActivateCoreSlot{Authority: authority, SlotId: res3.SlotId})
	require.NoError(t, err)
	_, err = msgs.UpdatePayoutAddress(ctx, &types.MsgUpdatePayoutAddress{Operator: op3, SlotId: res3.SlotId, NewPayoutAddress: op1})
	require.NoError(t, err)
	_, err = msgs.UpdateOperatorMetadata(ctx, &types.MsgUpdateOperatorMetadata{Operator: op3, SlotId: res3.SlotId, Metadata: &types.OperatorMetadata{Moniker: "n"}})
	require.NoError(t, err)
	// Driven through the real message so the event is produced by the production
	// path rather than emitted directly. Slot 3 is ACTIVE here, which is one of the
	// statuses that permit an operator mutation, and the destination is an ordinary
	// account distinct from the one registration set — an identical replacement is
	// rejected as a no-op and would emit nothing.
	_, err = msgs.UpdateSettlementAddress(ctx, &types.MsgUpdateSettlementAddress{Operator: op3, SlotId: res3.SlotId, SettlementAddress: testAccount(45)})
	require.NoError(t, err)
	// Also through the real message path. Slot 3 was registered in this block, so
	// its last-update height is still zero and the one free post-registration
	// update applies — no cooldown wait is needed to observe the event.
	_, err = msgs.UpdateSelectionPolicy(ctx, &types.MsgUpdateSelectionPolicy{
		Operator: op3, SlotId: res3.SlotId, SelectionRateBps: 1_500, MaxSelectedParticipants: 15,
	})
	require.NoError(t, err)
	updated := types.DefaultParams(authority, emergency)
	_, err = msgs.UpdateParams(ctx, &types.MsgUpdateParams{Authority: authority, Params: &updated})
	require.NoError(t, err)

	// Active rotation: requested now, applied (key_rotated + validator_update) at h2.
	_, err = msgs.RotateConsensusKey(ctx, &types.MsgRotateConsensusKey{Authority: authority, SlotId: 1, NewConsensusPubkey: pubkey(t, 9)})
	require.NoError(t, err)
	_, err = k.EndBlock(ctx) // slot3 joins -> validator_update_emitted
	require.NoError(t, err)

	ctx = ctx.WithBlockHeight(2)
	_, err = k.EndBlock(ctx) // applies slot1 rotation -> key_rotated
	require.NoError(t, err)

	// Queue a rotation on slot2 then cancel it via inactivate -> rotation_cancelled.
	_, err = msgs.RotateConsensusKey(ctx, &types.MsgRotateConsensusKey{Authority: authority, SlotId: 2, NewConsensusPubkey: pubkey(t, 8)})
	require.NoError(t, err)
	_, err = msgs.InactivateCoreSlot(ctx, &types.MsgInactivateCoreSlot{AuthorityOrOperator: authority, SlotId: 2, Reason: "maint"})
	require.NoError(t, err)
	// Suspend slot3 (still active; floor preserved by slot1).
	_, err = msgs.SuspendCoreSlot(ctx, &types.MsgSuspendCoreSlot{Authority: emergency, SlotId: res3.SlotId, Reason: "ev"})
	require.NoError(t, err)
	// Remove the now-inactive slot2.
	_, err = msgs.RemoveCoreSlot(ctx, &types.MsgRemoveCoreSlot{Authority: authority, SlotId: 2, Reason: "decom"})
	require.NoError(t, err)

	required := map[string][]string{
		types.EventTypeRegistered:           {types.AttributeKeySlotID, types.AttributeKeyOperatorAddress, types.AttributeKeyConsensusAddress, types.AttributeKeyNewStatus},
		types.EventTypeActivated:            {types.AttributeKeySlotID, types.AttributeKeyOperatorAddress, types.AttributeKeyOldStatus, types.AttributeKeyNewStatus, types.AttributeKeyConsensusAddress, types.AttributeKeyPower},
		types.EventTypeInactivated:          {types.AttributeKeySlotID, types.AttributeKeyOperatorAddress, types.AttributeKeyConsensusAddress, types.AttributeKeyOldStatus, types.AttributeKeyNewStatus, types.AttributeKeyPower, types.AttributeKeyReason},
		types.EventTypeSuspended:            {types.AttributeKeySlotID, types.AttributeKeyOperatorAddress, types.AttributeKeyConsensusAddress, types.AttributeKeyOldStatus, types.AttributeKeyNewStatus, types.AttributeKeyPower, types.AttributeKeyReason},
		types.EventTypeRemoved:              {types.AttributeKeySlotID, types.AttributeKeyOperatorAddress, types.AttributeKeyOldStatus, types.AttributeKeyNewStatus, types.AttributeKeyConsensusAddress, types.AttributeKeyReason},
		types.EventTypeKeyRotationRequested: {types.AttributeKeySlotID, types.AttributeKeyOperatorAddress, types.AttributeKeyOldConsensusAddress, types.AttributeKeyNewConsensusAddress, types.AttributeKeyEffectiveHeight},
		types.EventTypeKeyRotated:           {types.AttributeKeySlotID, types.AttributeKeyOperatorAddress, types.AttributeKeyOldConsensusAddress, types.AttributeKeyNewConsensusAddress, types.AttributeKeyPower, types.AttributeKeyEffectiveHeight},
		types.EventTypePayoutUpdated:        {types.AttributeKeySlotID, types.AttributeKeyOperatorAddress},
		types.EventTypeMetadataUpdated:      {types.AttributeKeySlotID, types.AttributeKeyOperatorAddress},
		// The settlement address itself is deliberately NOT an attribute: the event
		// records that the authorizing credential changed, and the current value is
		// read from the slot record. Pinning only what production emits keeps this
		// a test of the contract rather than a wish for a different one.
		types.EventTypeSettlementUpdated: {types.AttributeKeySlotID, types.AttributeKeyOperatorAddress},
		// Compact by design: the resulting version and its effective height locate
		// the change; the policy payload is read from the version the event names.
		types.EventTypeSelectionPolicyUpdated: {
			types.AttributeKeySlotID, types.AttributeKeyOperatorAddress,
			types.AttributeKeyPolicyVersion, types.AttributeKeyEffectiveHeight,
		},
		types.EventTypeParamsUpdated:          {types.AttributeKeyAuthority},
		types.EventTypeValidatorUpdateEmitted: {types.AttributeKeySlotID, types.AttributeKeyOperatorAddress, types.AttributeKeyConsensusAddress, types.AttributeKeyPower, types.AttributeKeyHeight},
		types.EventTypeRotationCancelled:      {types.AttributeKeySlotID, types.AttributeKeyOperatorAddress, types.AttributeKeyOldConsensusAddress, types.AttributeKeyNewConsensusAddress, types.AttributeKeyReason, types.AttributeKeyHeight},
	}

	for evtType, attrs := range required {
		require.Truef(t, hasEvent(ctx, evtType), "expected event %s", evtType)
		ev := firstEvent(t, ctx, evtType)
		for _, key := range attrs {
			require.NotEmptyf(t, attrValue(t, ev, key), "event %s attribute %s must be non-empty", evtType, key)
		}
	}

	// Presence and non-emptiness cannot tell a correct policy-update event from a
	// plausible-looking wrong one — the wrong slot, the operator of another slot,
	// a stale version, the transaction height instead of the effective height
	// would all satisfy the loop above. Slot 3 was registered and updated in block
	// 1, so its new version is 2 and takes effect at height 2.
	policyEv := firstEvent(t, ctx, types.EventTypeSelectionPolicyUpdated)
	require.Equal(t, "3", attrValue(t, policyEv, types.AttributeKeySlotID))
	require.Equal(t, op3, attrValue(t, policyEv, types.AttributeKeyOperatorAddress))
	require.Equal(t, "2", attrValue(t, policyEv, types.AttributeKeyPolicyVersion))
	require.Equal(t, "2", attrValue(t, policyEv, types.AttributeKeyEffectiveHeight))
}

// TestSelectionPolicyUpdatedEventExactValues pins the event at values that cannot
// be confused with one another: version 2 becoming effective at height 51 after a
// transaction in block 50. The comprehensive regression above necessarily runs in
// block 1, where the version and the effective height are both "2" and a handler
// that emitted one in place of the other would still pass.
func TestSelectionPolicyUpdatedEventExactValues(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	op := policySlotGenesis(t, k, ctx, authority, emergency)
	msgs := keeper.NewMsgServer(k)

	updateCtx := ctx.WithBlockHeight(50).WithEventManager(sdk.NewEventManager())
	res, err := msgs.UpdateSelectionPolicy(updateCtx, &types.MsgUpdateSelectionPolicy{
		Operator: op, SlotId: 1, SelectionRateBps: 1_234, MaxSelectedParticipants: 42,
	})
	require.NoError(t, err)
	require.Equal(t, uint64(2), res.PolicyVersion)

	require.Equal(t, 1, countEvents(updateCtx, types.EventTypeSelectionPolicyUpdated))
	ev := firstEvent(t, updateCtx, types.EventTypeSelectionPolicyUpdated)
	require.Equal(t, "1", attrValue(t, ev, types.AttributeKeySlotID))
	require.Equal(t, op, attrValue(t, ev, types.AttributeKeyOperatorAddress))
	require.Equal(t, "2", attrValue(t, ev, types.AttributeKeyPolicyVersion),
		"the event names the version the update produced")
	require.Equal(t, "51", attrValue(t, ev, types.AttributeKeyEffectiveHeight),
		"the effective height is H+1, not the transaction height")

	// The event agrees with what was actually written.
	written := policyRow(t, k, updateCtx, 1, res.PolicyVersion)
	require.Equal(t, int64(51), written.ValidFromHeight)
}
