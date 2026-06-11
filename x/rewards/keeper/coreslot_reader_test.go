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
	}
	inactive := coreslottypes.CoreSlot{
		SlotId: 2, OperatorAddress: addr(3), PayoutAddress: addr(4),
		Status: coreslottypes.SlotStatus_SLOT_STATUS_INACTIVE, ConsensusPower: 0,
	}
	core := &coreSlotKeeperMock{
		slots: map[uint64]coreslottypes.CoreSlot{1: active, 2: inactive},
		weights: map[uint64]coreslottypes.OperatorRewardWeight{
			1: {SlotId: 1, FinalWeight: "1.500000000000000000"},
			2: {SlotId: 2, FinalWeight: "2.000000000000000000"},
		},
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
	require.Equal(t, "1.500000000000000000", snapshots[0].RewardWeight.String())

	snapshot, err := k.GetSlotRewardSnapshot(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, uint64(2), snapshot.SlotID)
	require.Equal(t, "2.000000000000000000", snapshot.RewardWeight.String())
	require.NotEqual(t, active.ConsensusPower, snapshot.RewardWeight.TruncateInt64(), "snapshot must not derive weight from consensus power")
}

func TestCoreSlotRewardSnapshotRejectsInvalidAddresses(t *testing.T) {
	tests := []coreslottypes.CoreSlot{
		{SlotId: 1, OperatorAddress: "invalid", PayoutAddress: addr(2), Status: coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE},
		{SlotId: 1, OperatorAddress: addr(1), PayoutAddress: "", Status: coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE},
	}
	for _, slot := range tests {
		core := &coreSlotKeeperMock{
			slots:   map[uint64]coreslottypes.CoreSlot{1: slot},
			weights: map[uint64]coreslottypes.OperatorRewardWeight{1: {SlotId: 1, FinalWeight: "1.000000000000000000"}},
			active:  []coreslottypes.CoreSlot{slot},
		}
		k, ctx, _ := setupKeeper(t, core)
		_, err := k.GetActiveSlotSnapshots(ctx)
		require.Error(t, err)
	}
}

func TestCoreSlotRewardSnapshotRejectsNegativeWeight(t *testing.T) {
	slot := coreslottypes.CoreSlot{
		SlotId: 1, OperatorAddress: addr(1), PayoutAddress: addr(2),
		Status: coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE,
	}
	core := &coreSlotKeeperMock{
		slots:   map[uint64]coreslottypes.CoreSlot{1: slot},
		weights: map[uint64]coreslottypes.OperatorRewardWeight{1: {SlotId: 1, FinalWeight: "-1"}},
		active:  []coreslottypes.CoreSlot{slot},
	}
	k, ctx, _ := setupKeeper(t, core)
	_, err := k.GetActiveSlotSnapshots(ctx)
	require.Error(t, err)
}
