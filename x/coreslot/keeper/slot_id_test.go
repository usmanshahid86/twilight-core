package keeper_test

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	storetypes "cosmossdk.io/store/types"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/x/coreslot/keeper"
	"github.com/twilight-project/twilight-core/x/coreslot/types"
)

// Registration allocates a slot identifier from a stored counter, and every write
// that follows is an unconditional Set. An identifier that is already in use
// therefore does not collide — it overwrites a live slot's record, both address
// indexes, its reward weight and its version-1 Selection policy. These tests pin
// that the counter is never guessed at.

// moduleStoreSnapshot captures every raw key and value in the module's store.
//
// Registration touches six collections, so a per-collection snapshot would have
// to enumerate them and would quietly stop covering anything added later.
// Comparing the whole keyspace as bytes proves no write landed ANYWHERE, and
// unlike a typed snapshot it still works when the value under test is
// deliberately undecodable.
func moduleStoreSnapshot(t *testing.T, ctx sdk.Context, storeKey *storetypes.KVStoreKey) map[string]string {
	t.Helper()
	snapshot := map[string]string{}
	iter := ctx.KVStore(storeKey).Iterator(nil, nil)
	for ; iter.Valid(); iter.Next() {
		snapshot[hex.EncodeToString(iter.Key())] = hex.EncodeToString(iter.Value())
	}
	require.NoError(t, iter.Close())
	return snapshot
}

// TestRegistrationRefusesUntrustworthySlotIDCounter proves registration fails
// closed on every counter state it cannot trust, rather than falling back to a
// default identifier.
//
// None of these fixtures is reachable from a conforming chain: genesis requires
// the counter to exceed every assigned identifier and registration advances it
// with a checked increment. That is the point — the fallback only ever fired when
// state was ALREADY damaged, and its effect was to damage it further.
func TestRegistrationRefusesUntrustworthySlotIDCounter(t *testing.T) {
	for _, tc := range []struct {
		name       string
		corrupt    func(t *testing.T, k keeper.Keeper, ctx sdk.Context, storeKey *storetypes.KVStoreKey)
		wantDetail string
	}{
		{
			name: "the counter value cannot be decoded",
			corrupt: func(t *testing.T, _ keeper.Keeper, ctx sdk.Context, storeKey *storetypes.KVStoreKey) {
				corruptOnlyRawValue(t, ctx, storeKey, types.NextSlotIDKey)
			},
			wantDetail: "could not be read",
		},
		{
			name: "the counter is absent",
			corrupt: func(t *testing.T, k keeper.Keeper, ctx sdk.Context, _ *storetypes.KVStoreKey) {
				require.NoError(t, k.NextSlotID.Remove(ctx))
			},
			wantDetail: "is not set",
		},
		{
			name: "the counter is zero",
			corrupt: func(t *testing.T, k keeper.Keeper, ctx sdk.Context, _ *storetypes.KVStoreKey) {
				require.NoError(t, k.NextSlotID.Set(ctx, 0))
			},
			wantDetail: "is zero",
		},
		{
			name: "the counter names a slot that already exists",
			corrupt: func(t *testing.T, k keeper.Keeper, ctx sdk.Context, _ *storetypes.KVStoreKey) {
				require.NoError(t, k.NextSlotID.Set(ctx, 1))
			},
			wantDetail: "names slot 1, which already exists",
		},
		{
			name: "the counter names a slot whose own record is corrupt",
			corrupt: func(t *testing.T, k keeper.Keeper, ctx sdk.Context, storeKey *storetypes.KVStoreKey) {
				// The occupancy check must not depend on decoding the occupant. A
				// slot whose stored record is unreadable is still very much taken,
				// and reading it as free would hand the next registration the one
				// identifier guaranteed to destroy evidence of the corruption.
				require.NoError(t, k.NextSlotID.Set(ctx, 1))
				corruptOnlyRawValue(t, ctx, storeKey, types.SlotsPrefix)
			},
			wantDetail: "names slot 1, which already exists",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx, authority, emergency, storeKey := setupWithRawStore(t)
			policySlotGenesis(t, k, ctx, authority, emergency)
			msgs := keeper.NewMsgServer(k)

			tc.corrupt(t, k, ctx, storeKey)

			// Taken AFTER corruption: the damaged values are part of the state that
			// must survive untouched, and a rejection that "repaired" one of them
			// would be a mutation too.
			ctx = ctx.WithEventManager(sdk.NewEventManager())
			before := moduleStoreSnapshot(t, ctx, storeKey)

			op := sdk.AccAddress(append([]byte{3}, make([]byte, 19)...)).String()
			_, err := msgs.RegisterCoreSlot(ctx, registerMsg(t, authority, op, op, 2))
			require.ErrorIs(t, err, types.ErrInvalidTransition)
			require.Contains(t, err.Error(), tc.wantDetail,
				"the rejection must name the counter fault it refused")

			require.Equal(t, before, moduleStoreSnapshot(t, ctx, storeKey),
				"a refused registration must leave the entire module store byte-identical")
			require.Zero(t, countEvents(ctx, types.EventTypeRegistered),
				"a refused registration must not announce a slot that was never written")
		})
	}
}

// TestRegistrationUsesTheStoredSlotIDCounter is the positive control. Without it
// the rejections above could be satisfied by a handler that refused everything.
func TestRegistrationUsesTheStoredSlotIDCounter(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	policySlotGenesis(t, k, ctx, authority, emergency)
	msgs := keeper.NewMsgServer(k)

	stored, err := k.NextSlotID.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), stored, "genesis leaves the counter beyond every assigned identifier")

	op := sdk.AccAddress(append([]byte{3}, make([]byte, 19)...)).String()
	res, err := msgs.RegisterCoreSlot(ctx, registerMsg(t, authority, op, op, 2))
	require.NoError(t, err)
	require.Equal(t, uint64(2), res.SlotId, "the identifier comes from the counter, not from a default")

	advanced, err := k.NextSlotID.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(3), advanced)

	// The slot that was already there is untouched — the property the counter
	// discipline exists to protect.
	existing, err := k.GetSlot(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(1), existing.SlotId)
	require.Equal(t, uint64(1), existing.CurrentSelectionPolicyVersion)
	require.Equal(t, uint64(1), policyRow(t, k, ctx, 1, 1).PolicyVersion)
}

// TestRegistrationCounterFaultIsSettledBeforeAnyWrite proves the counter is
// resolved ahead of the first mutating step rather than after it.
//
// ensureConsensusAvailable releases an expired reservation, so it writes. If the
// counter were still read afterwards, a direct keeper invocation could reject the
// registration having already consumed that reservation — correctness that
// depended on transaction-cache rollback rather than on ordering.
func TestRegistrationCounterFaultIsSettledBeforeAnyWrite(t *testing.T) {
	k, ctx, authority, emergency, storeKey := setupWithRawStore(t)
	policySlotGenesis(t, k, ctx, authority, emergency)
	msgs := keeper.NewMsgServer(k)

	// An expired reservation for the key the registration will present: the one
	// piece of state ensureConsensusAvailable would remove on its way through.
	consKey := consAddrHex(t, pubkey(t, 2))
	require.NoError(t, k.Reserved.Set(ctx, consKey, types.ReservedConsensusAddress{
		ConsAddress: []byte{2}, SlotId: 9, ReservedUntil: 0, Reason: "expired lockout",
	}))
	require.NoError(t, k.NextSlotID.Set(ctx, 1)) // already taken

	before := moduleStoreSnapshot(t, ctx, storeKey)

	op := sdk.AccAddress(append([]byte{3}, make([]byte, 19)...)).String()
	_, err := msgs.RegisterCoreSlot(ctx, registerMsg(t, authority, op, op, 2))
	require.ErrorIs(t, err, types.ErrInvalidTransition)

	require.Equal(t, before, moduleStoreSnapshot(t, ctx, storeKey))
	stillReserved, err := k.Reserved.Has(ctx, consKey)
	require.NoError(t, err)
	require.True(t, stillReserved, "the reservation must survive a registration rejected for a counter fault")
}
