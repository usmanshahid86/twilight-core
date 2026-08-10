package keeper_test

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"

	sdked25519 "github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"

	"github.com/twilight-project/twilight-core/x/coreslot/keeper"
	"github.com/twilight-project/twilight-core/x/coreslot/types"
)

// consHexForMarker mirrors the keeper's consensus-address derivation (lowercase hex of
// the ed25519 pubkey address) for the slot fixtures built by pubkey(t, marker). This is
// the exact key the REST route /twilight/coreslot/v1/reserved-consensus-address/{hex}
// expects.
func consHexForMarker(marker byte) string {
	key := make([]byte, sdked25519.PubKeySize)
	key[0] = marker
	pk := sdked25519.PubKey{Key: key}
	return hex.EncodeToString(pk.Address().Bytes())
}

// TestReservedConsensusAddressQuery_GenesisFixture seeds a reservation through genesis
// and asserts the ReservedConsensusAddress query returns it — the handler that backs the
// REST /reserved-consensus-address/{hex} 200 response.
func TestReservedConsensusAddressQuery_GenesisFixture(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	params := types.DefaultParams(authority, emergency)

	consAddr := make([]byte, 20)
	for i := range consAddr {
		consAddr[i] = byte(i + 1)
	}
	op := sdk.AccAddress(append([]byte{2}, make([]byte, 19)...)).String()
	_, err := k.InitGenesis(ctx, &types.GenesisState{
		Params: &params,
		// Genesis requires at least one active slot; the reservation below is the
		// fixture under test and is independent of this slot's consensus key.
		Slots:      []*types.CoreSlot{slot(t, 1, op, 5, types.SlotStatus_SLOT_STATUS_ACTIVE, 1)},
		NextSlotId: 2,
		ReservedConsensusAddresses: []*types.ReservedConsensusAddress{{
			ConsAddress:   consAddr,
			SlotId:        9,
			ReservedUntil: 1234,
			Reason:        "key rotation",
		}},
	})
	require.NoError(t, err)

	qs := keeper.NewQueryServer(k)
	key := hex.EncodeToString(consAddr) // lowercase hex, matching the stored key

	resp, err := qs.ReservedConsensusAddress(ctx, &types.QueryReservedConsensusAddressRequest{ConsensusAddress: key})
	require.NoError(t, err)
	require.NotNil(t, resp.Reservation)
	require.Equal(t, consAddr, resp.Reservation.ConsAddress)
	require.Equal(t, uint64(9), resp.Reservation.SlotId)
	require.Equal(t, int64(1234), resp.Reservation.ReservedUntil)
	require.Equal(t, "key rotation", resp.Reservation.Reason)

	// Negative: an unknown consensus address is not found (the REST 404/not-found path).
	_, err = qs.ReservedConsensusAddress(ctx, &types.QueryReservedConsensusAddressRequest{
		ConsensusAddress: hex.EncodeToString(make([]byte, 20)),
	})
	require.Error(t, err)
}

// TestReservedConsensusAddressQuery_RemovalLifecycle drives the realistic creation path:
// an active slot is inactivated then removed, which reserves its consensus address for
// the reuse-lockout window; the query then returns that reservation.
func TestReservedConsensusAddressQuery_RemovalLifecycle(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	params := types.DefaultParams(authority, emergency)
	op1 := sdk.AccAddress(append([]byte{2}, make([]byte, 19)...)).String()
	op2 := sdk.AccAddress(append([]byte{3}, make([]byte, 19)...)).String()

	// Two active slots so removing one stays above the minimum-active-slots floor.
	_, err := k.InitGenesis(ctx, &types.GenesisState{
		Params: &params,
		Slots: []*types.CoreSlot{
			slot(t, 1, op1, 7, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
			slot(t, 2, op2, 8, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
		},
		NextSlotId: 3,
	})
	require.NoError(t, err)

	msgs := keeper.NewMsgServer(k)
	// Active slots must be inactivated/suspended before removal.
	_, err = msgs.InactivateCoreSlot(ctx, &types.MsgInactivateCoreSlot{
		AuthorityOrOperator: authority, SlotId: 1, Reason: "maintenance",
	})
	require.NoError(t, err)
	_, err = msgs.RemoveCoreSlot(ctx, &types.MsgRemoveCoreSlot{
		Authority: authority, SlotId: 1, Reason: "decommission",
	})
	require.NoError(t, err)

	qs := keeper.NewQueryServer(k)
	key := consHexForMarker(7) // the removed slot's consensus address

	resp, err := qs.ReservedConsensusAddress(ctx, &types.QueryReservedConsensusAddressRequest{ConsensusAddress: key})
	require.NoError(t, err)
	require.NotNil(t, resp.Reservation)
	require.Equal(t, uint64(1), resp.Reservation.SlotId)
	require.Equal(t, "decommission", resp.Reservation.Reason)
	require.NotZero(t, resp.Reservation.ReservedUntil)
}

func TestCoreSlotsQueryPagination(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	params := types.DefaultParams(authority, emergency)
	_, err := k.InitGenesis(ctx, &types.GenesisState{
		Params: &params,
		Slots: []*types.CoreSlot{
			querySlot(t, 1, types.SlotStatus_SLOT_STATUS_ACTIVE, params.SlotVotingPower),
			querySlot(t, 2, types.SlotStatus_SLOT_STATUS_INACTIVE, 0),
			querySlot(t, 3, types.SlotStatus_SLOT_STATUS_ACTIVE, params.SlotVotingPower),
			querySlot(t, 4, types.SlotStatus_SLOT_STATUS_SUSPENDED, 0),
			querySlot(t, 5, types.SlotStatus_SLOT_STATUS_ACTIVE, params.SlotVotingPower),
		},
		NextSlotId: 6,
	})
	require.NoError(t, err)

	qs := keeper.NewQueryServer(k)

	page1, err := qs.CoreSlots(ctx, &types.QueryCoreSlotsRequest{
		Pagination: &query.PageRequest{Limit: 2, CountTotal: true},
	})
	require.NoError(t, err)
	require.Equal(t, []uint64{1, 2}, querySlotIDs(page1.Slots))
	require.NotNil(t, page1.Pagination)
	require.NotEmpty(t, page1.Pagination.NextKey)
	require.Equal(t, uint64(5), page1.Pagination.Total)

	page2, err := qs.CoreSlots(ctx, &types.QueryCoreSlotsRequest{
		Pagination: &query.PageRequest{Key: page1.Pagination.NextKey, Limit: 2},
	})
	require.NoError(t, err)
	require.Equal(t, []uint64{3, 4}, querySlotIDs(page2.Slots))
	require.NotEmpty(t, page2.Pagination.NextKey)

	page3, err := qs.CoreSlots(ctx, &types.QueryCoreSlotsRequest{
		Pagination: &query.PageRequest{Key: page2.Pagination.NextKey, Limit: 2},
	})
	require.NoError(t, err)
	require.Equal(t, []uint64{5}, querySlotIDs(page3.Slots))
	require.Empty(t, page3.Pagination.NextKey)

	reverse, err := qs.CoreSlots(ctx, &types.QueryCoreSlotsRequest{
		Pagination: &query.PageRequest{Limit: 2, Reverse: true},
	})
	require.NoError(t, err)
	require.Equal(t, []uint64{5, 4}, querySlotIDs(reverse.Slots))
	require.NotEmpty(t, reverse.Pagination.NextKey)

	activePage1, err := qs.CoreSlots(ctx, &types.QueryCoreSlotsRequest{
		Status:     types.SlotStatus_SLOT_STATUS_ACTIVE,
		Pagination: &query.PageRequest{Limit: 2},
	})
	require.NoError(t, err)
	require.Equal(t, []uint64{1, 3}, querySlotIDs(activePage1.Slots))
	require.NotEmpty(t, activePage1.Pagination.NextKey)
	for _, slot := range activePage1.Slots {
		require.Equal(t, types.SlotStatus_SLOT_STATUS_ACTIVE, slot.Status)
	}

	activePage2, err := qs.CoreSlots(ctx, &types.QueryCoreSlotsRequest{
		Status:     types.SlotStatus_SLOT_STATUS_ACTIVE,
		Pagination: &query.PageRequest{Key: activePage1.Pagination.NextKey, Limit: 2},
	})
	require.NoError(t, err)
	require.Equal(t, []uint64{5}, querySlotIDs(activePage2.Slots))
	require.Empty(t, activePage2.Pagination.NextKey)
	for _, slot := range activePage2.Slots {
		require.Equal(t, types.SlotStatus_SLOT_STATUS_ACTIVE, slot.Status)
	}

	active, err := qs.ActiveCoreSlots(ctx, &types.QueryActiveCoreSlotsRequest{})
	require.NoError(t, err)
	require.Equal(t, []uint64{1, 3, 5}, querySlotIDs(active.Slots))
	require.Nil(t, active.Pagination)

	emptyKeeper, emptyCtx, _, _ := setup(t)
	empty, err := keeper.NewQueryServer(emptyKeeper).CoreSlots(emptyCtx, &types.QueryCoreSlotsRequest{
		Pagination: &query.PageRequest{Limit: 2},
	})
	require.NoError(t, err)
	require.Empty(t, empty.Slots)
	require.NotNil(t, empty.Pagination)
	require.Empty(t, empty.Pagination.NextKey)
}

func querySlot(t *testing.T, id uint64, status types.SlotStatus, power int64) *types.CoreSlot {
	t.Helper()
	return slot(t, id, queryOperator(byte(id+20)), byte(id), status, power)
}

func queryOperator(marker byte) string {
	addr := make([]byte, 20)
	addr[0] = marker
	return sdk.AccAddress(addr).String()
}

func querySlotIDs(slots []*types.CoreSlot) []uint64 {
	ids := make([]uint64, 0, len(slots))
	for _, slot := range slots {
		ids = append(ids, slot.SlotId)
	}
	return ids
}
