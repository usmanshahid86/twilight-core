package keeper_test

import (
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	"github.com/twilight-project/twilight-core/x/rewards/keeper"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

func allocationSnapshots() map[uint64]keeper.SlotRewardSnapshot {
	return map[uint64]keeper.SlotRewardSnapshot{
		1: {
			SlotID: 1, OperatorAddress: mustAddress(1), PayoutAddress: mustAddress(2),
			Status: coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE, ActivationSequence: 3,
		},
		2: {
			SlotID: 2, OperatorAddress: mustAddress(3), PayoutAddress: mustAddress(4),
			Status: coreslottypes.SlotStatus_SLOT_STATUS_INACTIVE, ActivationSequence: 4,
		},
	}
}

func TestAllocateSlotEntitlements(t *testing.T) {
	rows := []types.SlotActiveBlocks{{SlotId: 2, BlocksActive: 3}, {SlotId: 1, BlocksActive: 1}}

	entitlements, allocated, carry, nPos, err := keeper.AllocateSlotEntitlements(
		5, math.NewInt(10), rows, allocationSnapshots(), 2, 700)
	require.NoError(t, err)

	// Ascending slot_id regardless of the order the rows arrived in: the order
	// decides which Slot absorbs the floor, so it is part of the result.
	require.Equal(t, []uint64{1, 2}, []uint64{entitlements[0].SlotId, entitlements[1].SlotId})
	require.Equal(t, []string{"2", "7"},
		[]string{entitlements[0].EntitlementAmount, entitlements[1].EntitlementAmount})
	require.Equal(t, "9", allocated.String())
	require.Equal(t, "1", carry.String())
	require.Equal(t, uint64(2), nPos)

	// The immutable identity and audit context are carried from the snapshot and
	// from the binding the epoch resolved, not invented here.
	require.Equal(t, uint64(5), entitlements[0].Epoch)
	require.Equal(t, uint64(1), entitlements[0].TotalBlocksActive)
	require.Equal(t, "0", entitlements[0].ReleasedAmount)
	require.Equal(t, addr(2), entitlements[0].PayoutAddress)
	require.Equal(t, uint64(2), entitlements[0].RewardConfigVersion)
	require.Equal(t, uint64(700), entitlements[0].CreatedHeight)
	require.Equal(t, coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE, entitlements[0].SlotStatusAtEpochClose)
	require.Equal(t, uint64(3), entitlements[0].ActivationSequenceAtEpochClose)

	// A Slot that is no longer ACTIVE still earns for the epoch it participated in.
	require.Equal(t, coreslottypes.SlotStatus_SLOT_STATUS_INACTIVE, entitlements[1].SlotStatusAtEpochClose)
}

// TestAllocateSlotEntitlementsZeroParticipationCarriesTheWholePool covers §31's
// W == 0 rule: an unpaused epoch with no participating Slot still consumes its
// emission into carry, so mass inactivation cannot become an implicit
// emission-pause mechanism.
func TestAllocateSlotEntitlementsZeroParticipationCarriesTheWholePool(t *testing.T) {
	entitlements, allocated, carry, nPos, err := keeper.AllocateSlotEntitlements(
		1, math.NewInt(12), []types.SlotActiveBlocks{{SlotId: 1}}, nil, 1, 10)
	require.NoError(t, err)
	require.Empty(t, entitlements)
	require.True(t, allocated.IsZero())
	require.Equal(t, "12", carry.String())
	require.Zero(t, nPos)
}

// TestAllocateSlotEntitlementsCountsZeroSharesInNPos is the subtle half of the
// residue bound.
//
// A Slot whose share floors to zero participated: it contributed to the
// denominator and consumed a floor. It is not persisted, but it must still count
// toward n_pos, or the bound would be computed against too small a denominator
// and would reject a correct allocation.
func TestAllocateSlotEntitlementsCountsZeroSharesInNPos(t *testing.T) {
	snapshots := allocationSnapshots()
	snapshots[3] = keeper.SlotRewardSnapshot{
		SlotID: 3, OperatorAddress: mustAddress(5), PayoutAddress: mustAddress(6),
		Status: coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE, ActivationSequence: 1,
	}
	// A pool of 2 across weights 1/1/1 gives each Slot floor(2/3) = 0.
	rows := []types.SlotActiveBlocks{
		{SlotId: 1, BlocksActive: 1}, {SlotId: 2, BlocksActive: 1}, {SlotId: 3, BlocksActive: 1},
	}

	entitlements, allocated, carry, nPos, err := keeper.AllocateSlotEntitlements(
		1, math.NewInt(2), rows, snapshots, 1, 10)
	require.NoError(t, err)
	require.Empty(t, entitlements, "zero-amount entitlements are not persisted")
	require.Equal(t, "0", allocated.String())
	require.Equal(t, "2", carry.String())
	require.Equal(t, uint64(3), nPos, "a Slot whose share floors to zero still participated")

	// The residue bound holds against the participation count, not against the
	// number of persisted rows: carry 2 <= n_pos-1 = 2.
	require.True(t, carry.LTE(math.NewIntFromUint64(nPos).SubRaw(1)))
}

func TestAllocateSlotEntitlementsRejectsAMissingSnapshot(t *testing.T) {
	rows := []types.SlotActiveBlocks{{SlotId: 9, BlocksActive: 1}}
	_, _, _, _, err := keeper.AllocateSlotEntitlements(1, math.NewInt(10), rows, nil, 1, 10)
	require.ErrorIs(t, err, types.ErrInvalidState)
}

func mustAddress(marker byte) []byte {
	address := make([]byte, 20)
	address[0] = marker
	return address
}
