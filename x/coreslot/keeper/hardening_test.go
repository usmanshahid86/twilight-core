package keeper_test

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	sdked25519 "github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"
	gogoproto "github.com/cosmos/gogoproto/proto"
	anypb "github.com/cosmos/gogoproto/types/any"

	"github.com/twilight-project/twilight-core/x/coreslot/keeper"
	"github.com/twilight-project/twilight-core/x/coreslot/types"
)

// consAddrHex mirrors keeper.consensusKey for assertions about ByConsensus.
func consAddrHex(t *testing.T, any *anypb.Any) string {
	t.Helper()
	var pk sdked25519.PubKey
	require.NoError(t, gogoproto.Unmarshal(any.Value, &pk))
	return hex.EncodeToString(pk.Address().Bytes())
}

func hasEvent(ctx sdk.Context, evtType string) bool {
	for _, e := range ctx.EventManager().Events() {
		if e.Type == evtType {
			return true
		}
	}
	return false
}

func twoActiveGenesis(t *testing.T, k keeper.Keeper, ctx sdk.Context, authority, emergency string) (op1, op2 string) {
	t.Helper()
	params := types.DefaultParams(authority, emergency)
	params.MinActiveSlots = 1
	op1 = sdk.AccAddress(append([]byte{2}, make([]byte, 19)...)).String()
	op2 = sdk.AccAddress(append([]byte{3}, make([]byte, 19)...)).String()
	_, err := initGenesis(t, k, ctx, &types.GenesisState{Params: &params, Slots: []*types.CoreSlot{
		slot(t, 1, op1, 1, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
		slot(t, 2, op2, 2, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
	}, NextSlotId: 3})
	require.NoError(t, err)
	return op1, op2
}

// --- F1: cancel pending rotation on lifecycle change ---

func TestPendingRotationCancelledOnInactivate(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	twoActiveGenesis(t, k, ctx, authority, emergency)
	msgs := keeper.NewMsgServer(k)

	newPK := pubkey(t, 9)
	_, err := msgs.RotateConsensusKey(ctx, &types.MsgRotateConsensusKey{Authority: authority, SlotId: 1, NewConsensusPubkey: newPK})
	require.NoError(t, err)
	staged, err := k.ByConsensus.Has(ctx, consAddrHex(t, newPK))
	require.NoError(t, err)
	require.True(t, staged, "new key should be staged while rotation pending")

	_, err = msgs.InactivateCoreSlot(ctx, &types.MsgInactivateCoreSlot{AuthorityOrOperator: authority, SlotId: 1, Reason: "maintenance"})
	require.NoError(t, err)

	pending, err := k.Rotations.Has(ctx, 1)
	require.NoError(t, err)
	require.False(t, pending, "pending rotation must be cancelled")
	staged, err = k.ByConsensus.Has(ctx, consAddrHex(t, newPK))
	require.NoError(t, err)
	require.False(t, staged, "staged new key must be released")

	slot1, err := k.Slots.Get(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, consAddrHex(t, pubkey(t, 1)), consAddrHex(t, slot1.ConsensusPubkey), "slot must keep its original key")
	require.True(t, hasEvent(ctx, types.EventTypeRotationCancelled))
}

func TestPendingRotationCancelledOnSuspend(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	twoActiveGenesis(t, k, ctx, authority, emergency)
	msgs := keeper.NewMsgServer(k)

	newPK := pubkey(t, 9)
	_, err := msgs.RotateConsensusKey(ctx, &types.MsgRotateConsensusKey{Authority: authority, SlotId: 1, NewConsensusPubkey: newPK})
	require.NoError(t, err)

	_, err = msgs.SuspendCoreSlot(ctx, &types.MsgSuspendCoreSlot{Authority: emergency, SlotId: 1, Reason: "evidence"})
	require.NoError(t, err)

	pending, err := k.Rotations.Has(ctx, 1)
	require.NoError(t, err)
	require.False(t, pending)
	staged, err := k.ByConsensus.Has(ctx, consAddrHex(t, newPK))
	require.NoError(t, err)
	require.False(t, staged)
}

func TestPendingRotationCancelledOnRemove(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	params := types.DefaultParams(authority, emergency)
	op := sdk.AccAddress(append([]byte{2}, make([]byte, 19)...)).String()
	// One active slot keeps the active set non-empty; register a second pending
	// slot that we will remove with an injected stale rotation.
	_, err := initGenesis(t, k, ctx, &types.GenesisState{Params: &params, Slots: []*types.CoreSlot{
		slot(t, 1, op, 1, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
	}, NextSlotId: 2})
	require.NoError(t, err)
	msgs := keeper.NewMsgServer(k)
	op2 := sdk.AccAddress(append([]byte{3}, make([]byte, 19)...)).String()
	res, err := msgs.RegisterCoreSlot(ctx, registerMsg(t, authority, op2, op2, 2))
	require.NoError(t, err)

	// Inject a pending rotation on the still-PENDING slot 2 (defensive cleanup path).
	stagedPK := pubkey(t, 9)
	require.NoError(t, k.ByConsensus.Set(ctx, consAddrHex(t, stagedPK), res.SlotId))
	require.NoError(t, k.Rotations.Set(ctx, res.SlotId, types.PendingKeyRotation{SlotId: res.SlotId, OldPubkey: pubkey(t, 2), NewPubkey: stagedPK, RequestedHeight: 1, EffectiveHeight: 2}))

	_, err = msgs.RemoveCoreSlot(ctx, &types.MsgRemoveCoreSlot{Authority: authority, SlotId: res.SlotId, Reason: "decommission"})
	require.NoError(t, err)

	pending, err := k.Rotations.Has(ctx, res.SlotId)
	require.NoError(t, err)
	require.False(t, pending, "removal must cancel any stale pending rotation")
	staged, err := k.ByConsensus.Has(ctx, consAddrHex(t, stagedPK))
	require.NoError(t, err)
	require.False(t, staged, "staged key must be released on removal")
}

func TestRemovedSlotRotationDoesNotMutate(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	twoActiveGenesis(t, k, ctx, authority, emergency)
	msgs := keeper.NewMsgServer(k)

	newPK := pubkey(t, 9)
	_, err := msgs.RotateConsensusKey(ctx, &types.MsgRotateConsensusKey{Authority: authority, SlotId: 1, NewConsensusPubkey: newPK})
	require.NoError(t, err)

	// Force a REMOVED slot while leaving the rotation queued, to exercise the
	// EndBlock defense (F1) directly. The ACTIVE membership index is cleared with
	// it so this stays a test about the stale rotation rather than about index
	// divergence, which has its own guard.
	slot1, err := k.Slots.Get(ctx, 1)
	require.NoError(t, err)
	slot1.Status, slot1.ConsensusPower = types.SlotStatus_SLOT_STATUS_REMOVED, 0
	require.NoError(t, k.Slots.Set(ctx, 1, slot1))
	require.NoError(t, k.ActiveSlots.Remove(ctx, 1))

	ctx = ctx.WithBlockHeight(2)
	_, err = k.EndBlock(ctx)
	require.NoError(t, err)

	after, err := k.Slots.Get(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, types.SlotStatus_SLOT_STATUS_REMOVED, after.Status)
	require.Equal(t, consAddrHex(t, pubkey(t, 1)), consAddrHex(t, after.ConsensusPubkey), "removed slot must not be mutated by a stale rotation")
	pending, err := k.Rotations.Has(ctx, 1)
	require.NoError(t, err)
	require.False(t, pending, "stale rotation must be dropped")
	staged, err := k.ByConsensus.Has(ctx, consAddrHex(t, newPK))
	require.NoError(t, err)
	require.False(t, staged)
}

// --- F2: reject second pending rotation ---

func TestSecondRotationRejected(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	twoActiveGenesis(t, k, ctx, authority, emergency)
	msgs := keeper.NewMsgServer(k)

	_, err := msgs.RotateConsensusKey(ctx, &types.MsgRotateConsensusKey{Authority: authority, SlotId: 1, NewConsensusPubkey: pubkey(t, 9)})
	require.NoError(t, err)

	second := pubkey(t, 10)
	_, err = msgs.RotateConsensusKey(ctx, &types.MsgRotateConsensusKey{Authority: authority, SlotId: 1, NewConsensusPubkey: second})
	require.ErrorIs(t, err, types.ErrPendingRotationExists)

	staged, err := k.ByConsensus.Has(ctx, consAddrHex(t, second))
	require.NoError(t, err)
	require.False(t, staged, "rejected rotation must not mutate ByConsensus")
}

// --- F3: events ---

func TestLifecycleEventsEmitted(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	params := types.DefaultParams(authority, emergency)
	op1 := sdk.AccAddress(append([]byte{2}, make([]byte, 19)...)).String()
	_, err := initGenesis(t, k, ctx, &types.GenesisState{Params: &params, Slots: []*types.CoreSlot{
		slot(t, 1, op1, 1, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
	}, NextSlotId: 2})
	require.NoError(t, err)
	msgs := keeper.NewMsgServer(k)

	op2 := sdk.AccAddress(append([]byte{3}, make([]byte, 19)...)).String()
	res, err := msgs.RegisterCoreSlot(ctx, registerMsg(t, authority, op2, op2, 2))
	require.NoError(t, err)
	_, err = msgs.ActivateCoreSlot(ctx, &types.MsgActivateCoreSlot{Authority: authority, SlotId: res.SlotId})
	require.NoError(t, err)
	_, err = msgs.UpdatePayoutAddress(ctx, &types.MsgUpdatePayoutAddress{Operator: op2, SlotId: res.SlotId, NewPayoutAddress: op1})
	require.NoError(t, err)
	_, err = msgs.UpdateOperatorMetadata(ctx, &types.MsgUpdateOperatorMetadata{Operator: op2, SlotId: res.SlotId, Metadata: &types.OperatorMetadata{Moniker: "n"}})
	require.NoError(t, err)
	_, err = msgs.InactivateCoreSlot(ctx, &types.MsgInactivateCoreSlot{AuthorityOrOperator: authority, SlotId: res.SlotId, Reason: "x"})
	require.NoError(t, err)
	_, err = msgs.SuspendCoreSlot(ctx, &types.MsgSuspendCoreSlot{Authority: emergency, SlotId: res.SlotId, Reason: "y"})
	require.NoError(t, err)
	_, err = msgs.RemoveCoreSlot(ctx, &types.MsgRemoveCoreSlot{Authority: authority, SlotId: res.SlotId, Reason: "z"})
	require.NoError(t, err)
	updated := types.DefaultParams(authority, emergency)
	_, err = msgs.UpdateParams(ctx, &types.MsgUpdateParams{Authority: authority, Params: &updated})
	require.NoError(t, err)

	for _, evt := range []string{
		types.EventTypeRegistered, types.EventTypeActivated, types.EventTypePayoutUpdated,
		types.EventTypeMetadataUpdated, types.EventTypeInactivated, types.EventTypeSuspended,
		types.EventTypeRemoved, types.EventTypeParamsUpdated,
	} {
		require.Truef(t, hasEvent(ctx, evt), "expected event %s", evt)
	}
}

func TestValidatorUpdateEventsEmitted(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	params := types.DefaultParams(authority, emergency)
	op1 := sdk.AccAddress(append([]byte{2}, make([]byte, 19)...)).String()
	_, err := initGenesis(t, k, ctx, &types.GenesisState{Params: &params, Slots: []*types.CoreSlot{
		slot(t, 1, op1, 1, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
	}, NextSlotId: 2})
	require.NoError(t, err)
	msgs := keeper.NewMsgServer(k)

	op2 := sdk.AccAddress(append([]byte{3}, make([]byte, 19)...)).String()
	res, err := msgs.RegisterCoreSlot(ctx, registerMsg(t, authority, op2, op2, 2))
	require.NoError(t, err)
	_, err = msgs.ActivateCoreSlot(ctx, &types.MsgActivateCoreSlot{Authority: authority, SlotId: res.SlotId})
	require.NoError(t, err)

	updates, err := k.EndBlock(ctx)
	require.NoError(t, err)
	require.Len(t, updates, 1)
	require.True(t, hasEvent(ctx, types.EventTypeValidatorUpdateEmitted))

	// Active key rotation emits coreslot_key_rotated when applied in EndBlock.
	_, err = msgs.RotateConsensusKey(ctx, &types.MsgRotateConsensusKey{Authority: authority, SlotId: 1, NewConsensusPubkey: pubkey(t, 8)})
	require.NoError(t, err)
	ctx = ctx.WithBlockHeight(2)
	_, err = k.EndBlock(ctx)
	require.NoError(t, err)
	require.True(t, hasEvent(ctx, types.EventTypeKeyRotated))
}

func TestRotationCancelEventEmitted(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	twoActiveGenesis(t, k, ctx, authority, emergency)
	msgs := keeper.NewMsgServer(k)

	_, err := msgs.RotateConsensusKey(ctx, &types.MsgRotateConsensusKey{Authority: authority, SlotId: 1, NewConsensusPubkey: pubkey(t, 9)})
	require.NoError(t, err)
	_, err = msgs.InactivateCoreSlot(ctx, &types.MsgInactivateCoreSlot{AuthorityOrOperator: authority, SlotId: 1, Reason: "m"})
	require.NoError(t, err)
	require.True(t, hasEvent(ctx, types.EventTypeRotationCancelled))
}

// --- F4: collapse same-key power change ---

func TestSameKeyPowerChangeSingleUpdate(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	params := types.DefaultParams(authority, emergency)
	op1 := sdk.AccAddress(append([]byte{2}, make([]byte, 19)...)).String()
	_, err := initGenesis(t, k, ctx, &types.GenesisState{Params: &params, Slots: []*types.CoreSlot{
		slot(t, 1, op1, 1, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
	}, NextSlotId: 2})
	require.NoError(t, err)

	// Change power while keeping the same consensus key active (the latent F4
	// case): the diff must emit exactly one update at the new power.
	slot1, err := k.Slots.Get(ctx, 1)
	require.NoError(t, err)
	slot1.ConsensusPower = 5
	require.NoError(t, k.Slots.Set(ctx, 1, slot1))

	ctx = ctx.WithBlockHeight(2)
	updates, err := k.EndBlock(ctx)
	require.NoError(t, err)
	require.Len(t, updates, 1, "same-key power change must collapse to one update")
	require.Equal(t, int64(5), updates[0].Power)
}

// --- F6: emergency cannot empty validator set ---

func TestEmergencySuspendCannotRemoveLastValidator(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	params := types.DefaultParams(authority, emergency)
	params.AllowEmergencyBelowMinActive = true
	op1 := sdk.AccAddress(append([]byte{2}, make([]byte, 19)...)).String()
	_, err := initGenesis(t, k, ctx, &types.GenesisState{Params: &params, Slots: []*types.CoreSlot{
		slot(t, 1, op1, 1, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
	}, NextSlotId: 2})
	require.NoError(t, err)
	msgs := keeper.NewMsgServer(k)

	_, err = msgs.SuspendCoreSlot(ctx, &types.MsgSuspendCoreSlot{Authority: emergency, SlotId: 1, Reason: "evidence"})
	require.ErrorIs(t, err, types.ErrCannotRemoveLastValidator)

	slot1, err := k.Slots.Get(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, types.SlotStatus_SLOT_STATUS_ACTIVE, slot1.Status, "last validator must remain active")
}

// --- F7: genesis export/import round trip ---

func TestExportImportRoundTrip(t *testing.T) {
	k1, ctx1, authority, emergency := setup(t)
	twoActiveGenesis(t, k1, ctx1, authority, emergency)

	exported, err := k1.ExportGenesis(ctx1)
	require.NoError(t, err)
	require.Len(t, exported.LastAppliedValidators, 2)
	require.NoError(t, exported.Validate())

	k2, ctx2, _, _ := setup(t)
	updates, err := k2.InitGenesis(ctx2, exported)
	require.NoError(t, err)
	require.Len(t, updates, 2)

	reExported, err := k2.ExportGenesis(ctx2)
	require.NoError(t, err)
	require.Equal(t, exported, reExported, "export/import round trip must be stable")
}
