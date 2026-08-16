package keeper_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	storetypes "cosmossdk.io/store/types"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/x/coreslot/keeper"
	"github.com/twilight-project/twilight-core/x/coreslot/types"
)

// Two places outside the Selection-policy surface still answered "no such slot"
// for state that exists and cannot be read. They are fixed together because they
// share a cause, but they are asserted separately because the consequence
// differs: a query answering wrongly misinforms a client, while EndBlock acting
// on a misreading changes consensus state.

// --- secondary-index queries ---

// TestSlotIndexQueryDistinguishesAbsenceFromCorruption pins the by-operator and
// by-consensus lookups to the same rule the policy queries follow.
func TestSlotIndexQueryDistinguishesAbsenceFromCorruption(t *testing.T) {
	unknownOperator := sdk.AccAddress(append([]byte{7}, make([]byte, 19)...)).String()

	for _, tc := range []struct {
		name    string
		prefix  []byte
		absent  func(client types.QueryClient) error
		present func(client types.QueryClient) error
	}{
		{
			name:   "by operator",
			prefix: types.OperatorPrefix,
			absent: func(client types.QueryClient) error {
				_, err := client.CoreSlotByOperator(context.Background(),
					&types.QueryCoreSlotByOperatorRequest{OperatorAddress: unknownOperator})
				return err
			},
			present: func(client types.QueryClient) error {
				_, err := client.CoreSlotByOperator(context.Background(),
					&types.QueryCoreSlotByOperatorRequest{OperatorAddress: registeredOperator()})
				return err
			},
		},
		{
			name:   "by consensus address",
			prefix: types.ConsensusPrefix,
			absent: func(client types.QueryClient) error {
				_, err := client.CoreSlotByConsensusAddress(context.Background(),
					&types.QueryCoreSlotByConsensusAddressRequest{ConsensusAddress: consHexForMarker(200)})
				return err
			},
			present: func(client types.QueryClient) error {
				_, err := client.CoreSlotByConsensusAddress(context.Background(),
					&types.QueryCoreSlotByConsensusAddressRequest{ConsensusAddress: consHexForMarker(1)})
				return err
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx, authority, emergency, storeKey := setupWithRawStore(t)
			policySlotGenesis(t, k, ctx, authority, emergency)

			client := policyQueryClient(t, k, ctx)

			// Present: the lookup works at all, so the codes below are about
			// classification rather than a query that fails no matter what.
			require.NoError(t, tc.present(client))

			// Absent: an ordinary answer.
			err := tc.absent(client)
			require.Error(t, err)
			require.Equal(t, codes.NotFound, grpcstatus.Code(err))

			// Present but unreadable: not an answer at all.
			corruptOnlyRawValue(t, ctx, storeKey, tc.prefix)
			err = tc.present(policyQueryClient(t, k, ctx))
			require.Error(t, err)
			require.NotEqual(t, codes.NotFound, grpcstatus.Code(err),
				"an unreadable index entry must never look like an unregistered operator or validator")
			require.Equal(t, codes.Internal, grpcstatus.Code(err))
		})
	}
}

// registeredOperator returns the operator address policySlotGenesis registers for
// slot 1.
func registeredOperator() string {
	return sdk.AccAddress(append([]byte{2}, make([]byte, 19)...)).String()
}

// --- EndBlock pending-rotation handling ---

// queueDueRotation stages a rotation for slot 1 that is already due, bypassing
// the message handler so the fixture can then damage the slot record underneath
// it — the state a lifecycle transition would otherwise have cleaned up.
func queueDueRotation(t *testing.T, k keeper.Keeper, ctx sdk.Context) (newConsKey string) {
	t.Helper()
	newPK := pubkey(t, 9)
	newConsKey = consAddrHex(t, newPK)
	require.NoError(t, k.Rotations.Set(ctx, 1, types.PendingKeyRotation{
		SlotId: 1, OldPubkey: pubkey(t, 1), NewPubkey: newPK,
		RequestedHeight: 0, EffectiveHeight: 1,
	}))
	require.NoError(t, k.ByConsensus.Set(ctx, newConsKey, 1))
	return newConsKey
}

// TestEndBlockHaltsWhenADueRotationsSlotCannotBeRead is the consensus-path half.
//
// Dropping a rotation frees the staged consensus key and deletes the queued
// rotation. Doing that because the slot record could not be read would mean
// changing consensus state on evidence the module does not have — so the block
// halts instead, for absence and corruption alike. Slot rows are terminal and a
// rotation is canceled by every lifecycle transition, so a due rotation naming
// an unreadable slot is divergence either way.
func TestEndBlockHaltsWhenADueRotationsSlotCannotBeRead(t *testing.T) {
	for _, tc := range []struct {
		name   string
		damage func(t *testing.T, k keeper.Keeper, ctx sdk.Context, storeKey *storetypes.KVStoreKey)
	}{
		{
			name: "the slot record is absent",
			damage: func(t *testing.T, k keeper.Keeper, ctx sdk.Context, _ *storetypes.KVStoreKey) {
				require.NoError(t, k.Slots.Remove(ctx, 1))
			},
		},
		{
			name: "the slot record cannot be decoded",
			damage: func(t *testing.T, k keeper.Keeper, ctx sdk.Context, storeKey *storetypes.KVStoreKey) {
				corruptOnlyRawValue(t, ctx, storeKey, types.SlotsPrefix)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx, authority, emergency, storeKey := setupWithRawStore(t)
			policySlotGenesis(t, k, ctx, authority, emergency)
			newConsKey := queueDueRotation(t, k, ctx)

			tc.damage(t, k, ctx, storeKey)

			ctx = ctx.WithEventManager(sdk.NewEventManager())
			_, err := k.EndBlock(ctx)
			require.Error(t, err, "an unreadable slot record must halt the block, not drop the rotation")
			require.ErrorIs(t, err, types.ErrInvalidTransition)
			require.Contains(t, err.Error(), "could not be read")

			// Nothing of the drop happened.
			pending, err := k.Rotations.Has(ctx, 1)
			require.NoError(t, err)
			require.True(t, pending, "the queued rotation must survive")
			staged, err := k.ByConsensus.Has(ctx, newConsKey)
			require.NoError(t, err)
			require.True(t, staged, "the staged consensus key must not be released")
			require.Zero(t, countEvents(ctx, types.EventTypeRotationCancelled),
				"a halted block must not announce a cancellation")
			require.Empty(t, ctx.EventManager().Events(), "a failed EndBlock emits no events")
		})
	}
}

// TestEndBlockStillDropsARotationForAnIneligibleSlot is the counterweight: the
// modeled stale case is driven by a record that WAS read, and is unchanged.
//
// Without this, halting on read failure could have been mistaken for "EndBlock
// never drops rotations", which would break the lifecycle guarantee that a
// non-active slot cannot carry a live rotation.
func TestEndBlockStillDropsARotationForAnIneligibleSlot(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	op := policySlotGenesis(t, k, ctx, authority, emergency)
	newConsKey := queueDueRotation(t, k, ctx)

	// Readable, but no longer eligible — the condition the stale drop exists for.
	stored, err := k.GetSlot(ctx, 1)
	require.NoError(t, err)
	stored.Status = types.SlotStatus_SLOT_STATUS_REMOVED
	stored.ConsensusPower = 0
	require.NoError(t, k.Slots.Set(ctx, 1, stored))
	require.NoError(t, k.ActiveSlots.Remove(ctx, 1))

	ctx = ctx.WithEventManager(sdk.NewEventManager())
	_, err = k.EndBlock(ctx)
	require.NoError(t, err)

	pending, err := k.Rotations.Has(ctx, 1)
	require.NoError(t, err)
	require.False(t, pending, "an ineligible slot's rotation is still dropped")
	staged, err := k.ByConsensus.Has(ctx, newConsKey)
	require.NoError(t, err)
	require.False(t, staged, "the staged key is still released")

	require.Equal(t, 1, countEvents(ctx, types.EventTypeRotationCancelled))
	ev := firstEvent(t, ctx, types.EventTypeRotationCancelled)
	require.Equal(t, "1", attrValue(t, ev, types.AttributeKeySlotID))
	require.Equal(t, op, attrValue(t, ev, types.AttributeKeyOperatorAddress))
	require.Equal(t, types.RotationCancelReasonStale, attrValue(t, ev, types.AttributeKeyReason))
}
