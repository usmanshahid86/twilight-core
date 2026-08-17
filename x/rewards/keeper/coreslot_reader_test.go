package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
)

func TestCoreSlotRewardSnapshots(t *testing.T) {
	active := coreslottypes.CoreSlot{
		SlotId: 1, OperatorAddress: addr(1), PayoutAddress: addr(2),
		Status: coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE, ConsensusPower: 999,
		ActivationSequence: 3,
	}
	inactive := coreslottypes.CoreSlot{
		SlotId: 2, OperatorAddress: addr(3), PayoutAddress: addr(4),
		Status: coreslottypes.SlotStatus_SLOT_STATUS_INACTIVE, ConsensusPower: 0,
		ActivationSequence: 5,
	}
	core := &coreSlotKeeperMock{
		slots:  map[uint64]coreslottypes.CoreSlot{1: active, 2: inactive},
		active: []coreslottypes.CoreSlot{active, inactive},
	}
	k, ctx, _ := setupKeeper(t, core)

	snapshots, err := k.GetActiveSlotSnapshots(ctx)
	require.NoError(t, err)
	require.Len(t, snapshots, 1)
	require.Equal(t, uint64(1), snapshots[0].SlotID)
	require.Equal(t, active.OperatorAddress, snapshots[0].OperatorAddress.String())
	require.Equal(t, active.PayoutAddress, snapshots[0].PayoutAddress.String())
	require.Equal(t, active.Status, snapshots[0].Status)
	// Audit context for the entitlement, carried verbatim from CoreSlot. It is
	// deliberately NOT derived from consensus power, which is a different quantity
	// that happens to be present on the same record.
	require.Equal(t, uint64(3), snapshots[0].ActivationSequence)

	snapshot, err := k.GetSlotRewardSnapshot(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, uint64(2), snapshot.SlotID)
	require.Equal(t, uint64(5), snapshot.ActivationSequence)
	require.Equal(t, inactive.Status, snapshot.Status,
		"a non-ACTIVE Slot still snapshots: an earned entitlement survives lifecycle change")
}

func TestCoreSlotRewardSnapshotRejectsInvalidAddresses(t *testing.T) {
	tests := []coreslottypes.CoreSlot{
		{SlotId: 1, OperatorAddress: "invalid", PayoutAddress: addr(2), Status: coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE},
		{SlotId: 1, OperatorAddress: addr(1), PayoutAddress: "", Status: coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE},
	}
	for _, slot := range tests {
		core := &coreSlotKeeperMock{
			slots:  map[uint64]coreslottypes.CoreSlot{1: slot},
			active: []coreslottypes.CoreSlot{slot},
		}
		k, ctx, _ := setupKeeper(t, core)
		_, err := k.GetActiveSlotSnapshots(ctx)
		require.Error(t, err)
	}
}

// The reward-weight rejection test is deliberately gone rather than relaxed.
// x/rewards no longer reads the weight at all -- V2 entitlement shares are
// participation-relative -- so there is nothing left here to reject. The weight
// remains CoreSlot's own metadata and CoreSlot validates it.
