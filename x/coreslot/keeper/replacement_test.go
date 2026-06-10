package keeper_test

import (
	"encoding/hex"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	"github.com/stretchr/testify/require"

	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/nyks/nyks-core/x/coreslot/keeper"
	"github.com/nyks/nyks-core/x/coreslot/types"
)

// makeOps returns n distinct bech32 operator addresses with the given leading
// byte (kept away from authority/emergency/marker bytes used elsewhere).
func makeOps(prefix byte, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = sdk.AccAddress(append([]byte{prefix, byte(i)}, make([]byte, 18)...)).String()
	}
	return out
}

func lastAppliedBySlot(t *testing.T, k keeper.Keeper, ctx sdk.Context) map[uint64]int64 {
	t.Helper()
	out := map[uint64]int64{}
	require.NoError(t, k.LastApplied.Walk(ctx, nil, func(_ string, v types.LastAppliedValidator) (bool, error) {
		out[v.SlotId] = v.Power
		return false, nil
	}))
	return out
}

func activeSlotCount(t *testing.T, k keeper.Keeper, ctx sdk.Context) int {
	t.Helper()
	n := 0
	require.NoError(t, k.Slots.Walk(ctx, nil, func(_ uint64, s types.CoreSlot) (bool, error) {
		if s.Status == types.SlotStatus_SLOT_STATUS_ACTIVE {
			n++
		}
		return false, nil
	}))
	return n
}

func updatesByAddr(t *testing.T, updates []abci.ValidatorUpdate) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	for _, u := range updates {
		pk, err := cryptocodec.FromCmtProtoPublicKey(u.PubKey)
		require.NoError(t, err)
		out[hex.EncodeToString(pk.Address())] = u.Power
	}
	return out
}

// assertSortedNoDup checks the EndBlock changeset is strictly ascending by
// consensus address, which simultaneously proves deterministic ordering and the
// absence of duplicate addresses (CometBFT rejects duplicate changeset entries).
func assertSortedNoDup(t *testing.T, updates []abci.ValidatorUpdate) {
	t.Helper()
	prev := ""
	for i, u := range updates {
		pk, err := cryptocodec.FromCmtProtoPublicKey(u.PubKey)
		require.NoError(t, err)
		addr := hex.EncodeToString(pk.Address())
		if i > 0 {
			require.Less(t, prev, addr, "updates must be strictly ascending by consensus address (sorted, no duplicates)")
		}
		prev = addr
	}
}

// TestFullActiveSetReplacementSingleBlock turns over the entire active set in a
// single block using the supported pattern: activate the replacement set first,
// then inactivate the old set (so the active set never drops below MinActive),
// and apply the whole change in one EndBlock. CometBFT applies the resulting
// changeset atomically, so no empty-set window is ever committed.
func TestFullActiveSetReplacementSingleBlock(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	params := types.DefaultParams(authority, emergency) // SlotVotingPower=1, Min=1, Max=100
	oldOps := makeOps(10, 4)

	updates, err := k.InitGenesis(ctx, &types.GenesisState{Params: &params, Slots: []*types.CoreSlot{
		slot(t, 1, oldOps[0], 1, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
		slot(t, 2, oldOps[1], 2, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
		slot(t, 3, oldOps[2], 3, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
		slot(t, 4, oldOps[3], 4, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
	}, NextSlotId: 5})
	require.NoError(t, err)
	require.Len(t, updates, 4)

	msgs := keeper.NewMsgServer(k)
	newOps := makeOps(20, 4)
	newIDs := make([]uint64, 0, 4)
	for i := 0; i < 4; i++ {
		res, err := msgs.RegisterCoreSlot(ctx, &types.MsgRegisterCoreSlot{
			Authority: authority, OperatorAddress: newOps[i], PayoutAddress: newOps[i], ConsensusPubkey: pubkey(t, byte(11+i)),
		})
		require.NoError(t, err)
		_, err = msgs.ActivateCoreSlot(ctx, &types.MsgActivateCoreSlot{Authority: authority, SlotId: res.SlotId})
		require.NoError(t, err)
		newIDs = append(newIDs, res.SlotId)
	}
	// 8 active now; inactivate the 4 old slots (count never drops below Min).
	for id := uint64(1); id <= 4; id++ {
		_, err := msgs.InactivateCoreSlot(ctx, &types.MsgInactivateCoreSlot{AuthorityOrOperator: authority, SlotId: id, Reason: "rotation"})
		require.NoError(t, err)
	}

	ctx2 := ctx.WithBlockHeight(2).WithEventManager(sdk.NewEventManager())
	updates, err = k.EndBlock(ctx2)
	require.NoError(t, err)
	require.Len(t, updates, 8, "4 old leaving + 4 new joining")

	assertSortedNoDup(t, updates)
	byAddr := updatesByAddr(t, updates)
	for m := byte(1); m <= 4; m++ {
		require.Equal(t, int64(0), byAddr[consAddrHex(t, pubkey(t, m))], "old validator must leave at power 0")
	}
	for i := 0; i < 4; i++ {
		require.Equal(t, params.SlotVotingPower, byAddr[consAddrHex(t, pubkey(t, byte(11+i)))], "new validator must enter at SlotVotingPower")
	}

	require.Equal(t, 4, activeSlotCount(t, k, ctx2), "final active set size must equal the replacement set")

	la := lastAppliedBySlot(t, k, ctx2)
	require.Len(t, la, 4, "LastApplied must contain exactly the new set")
	for _, id := range newIDs {
		require.Equal(t, params.SlotVotingPower, la[id])
	}
}

// TestUnsafeFullDrainRejected documents that v1 lifecycle policy disallows
// emptying the active set: the active set can be drained only down to
// MinActiveSlots, and the final inactivation is rejected with ErrMinActiveSlots.
// (Emergency suspension to zero is likewise rejected; see
// TestEmergencySuspendCannotRemoveLastValidator.)
func TestUnsafeFullDrainRejected(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	params := types.DefaultParams(authority, emergency) // MinActiveSlots=1
	ops := makeOps(10, 4)
	_, err := k.InitGenesis(ctx, &types.GenesisState{Params: &params, Slots: []*types.CoreSlot{
		slot(t, 1, ops[0], 1, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
		slot(t, 2, ops[1], 2, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
		slot(t, 3, ops[2], 3, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
		slot(t, 4, ops[3], 4, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
	}, NextSlotId: 5})
	require.NoError(t, err)
	msgs := keeper.NewMsgServer(k)

	// Drain down to the MinActiveSlots floor.
	for id := uint64(1); id <= 3; id++ {
		_, err := msgs.InactivateCoreSlot(ctx, &types.MsgInactivateCoreSlot{AuthorityOrOperator: authority, SlotId: id, Reason: "x"})
		require.NoError(t, err)
	}
	require.Equal(t, 1, activeSlotCount(t, k, ctx))

	// The last active slot cannot be inactivated — the set must never empty.
	_, err = msgs.InactivateCoreSlot(ctx, &types.MsgInactivateCoreSlot{AuthorityOrOperator: authority, SlotId: 4, Reason: "x"})
	require.ErrorIs(t, err, types.ErrMinActiveSlots)
	require.Equal(t, 1, activeSlotCount(t, k, ctx), "active set must never be emptied")
}
