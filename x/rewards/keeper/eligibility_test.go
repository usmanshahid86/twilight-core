package keeper_test

import (
	"testing"

	"cosmossdk.io/collections"
	"github.com/stretchr/testify/require"

	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

func TestBeginBlockCreditsOnlyActiveSlots(t *testing.T) {
	for _, status := range []coreslottypes.SlotStatus{
		coreslottypes.SlotStatus_SLOT_STATUS_UNSPECIFIED,
		coreslottypes.SlotStatus_SLOT_STATUS_PENDING,
		coreslottypes.SlotStatus_SLOT_STATUS_INACTIVE,
		coreslottypes.SlotStatus_SLOT_STATUS_SUSPENDED,
		coreslottypes.SlotStatus_SLOT_STATUS_REMOVED,
	} {
		t.Run(status.String(), func(t *testing.T) {
			coreSlots := &coreSlotKeeperMock{active: []coreslottypes.CoreSlot{
				accountingSlot(1, status),
			}}
			k, ctx, _ := setupAccountingKeeper(t, coreSlots, 1, types.DefaultParams())

			require.ErrorIs(t, k.BeginBlock(ctx), types.ErrInvalidState)
			_, err := k.GetActiveBlocks(ctx, 1, 1)
			require.ErrorIs(t, err, collections.ErrNotFound)
		})
	}

	t.Run("active", func(t *testing.T) {
		coreSlots := &coreSlotKeeperMock{active: []coreslottypes.CoreSlot{
			accountingSlot(1, coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE),
		}}
		k, ctx, _ := setupAccountingKeeper(t, coreSlots, 1, types.DefaultParams())

		require.NoError(t, k.BeginBlock(ctx))
		blocks, err := k.GetActiveBlocks(ctx, 1, 1)
		require.NoError(t, err)
		require.Equal(t, uint64(1), blocks)
	})
}

func TestBeginBlockValidatesAllSlotsBeforeCrediting(t *testing.T) {
	coreSlots := &coreSlotKeeperMock{active: []coreslottypes.CoreSlot{
		accountingSlot(1, coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE),
		accountingSlot(2, coreslottypes.SlotStatus_SLOT_STATUS_INACTIVE),
	}}
	k, ctx, _ := setupAccountingKeeper(t, coreSlots, 1, types.DefaultParams())

	require.ErrorIs(t, k.BeginBlock(ctx), types.ErrInvalidState)
	_, err := k.GetActiveBlocks(ctx, 1, 1)
	require.ErrorIs(t, err, collections.ErrNotFound)
}
