package keeper_test

import (
	"context"
	"errors"
	"testing"

	"cosmossdk.io/collections"
	"github.com/stretchr/testify/require"

	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	"github.com/twilight-project/twilight-core/x/rewards/keeper"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

func TestBeginBlockIncrementsActiveSlotsOncePerBlock(t *testing.T) {
	coreSlots := &coreSlotKeeperMock{active: []coreslottypes.CoreSlot{
		accountingSlot(3, coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE),
		accountingSlot(1, coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE),
	}}
	k, ctx, _ := setupAccountingKeeper(t, coreSlots, 1, types.DefaultParams())

	require.NoError(t, k.BeginBlock(ctx))
	require.NoError(t, k.BeginBlock(ctx))

	rows, err := k.IterateActiveBlocksForEpoch(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, []types.SlotActiveBlocks{
		{SlotId: 1, BlocksActive: 2},
		{SlotId: 3, BlocksActive: 2},
	}, rows)
}

func TestBeginBlockCountersAreScopedByEpoch(t *testing.T) {
	coreSlots := &coreSlotKeeperMock{active: []coreslottypes.CoreSlot{
		accountingSlot(1, coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE),
	}}
	k, ctx, _ := setupAccountingKeeper(t, coreSlots, 1, types.DefaultParams())
	require.NoError(t, k.BeginBlock(ctx))

	state, err := k.GetState(ctx)
	require.NoError(t, err)
	state.CurrentEpoch = 2
	state.CurrentEpochStartHeight = 20
	require.NoError(t, k.SetState(ctx, state))
	require.NoError(t, k.BeginBlock(ctx))
	require.NoError(t, k.BeginBlock(ctx))

	epochOne, err := k.GetActiveBlocks(ctx, 1, 1)
	require.NoError(t, err)
	epochTwo, err := k.GetActiveBlocks(ctx, 2, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(1), epochOne)
	require.Equal(t, uint64(2), epochTwo)
}

func TestBeginBlockEmptyActiveSetSucceeds(t *testing.T) {
	k, ctx, _ := setupAccountingKeeper(t, &coreSlotKeeperMock{}, 1, types.DefaultParams())
	require.NoError(t, k.BeginBlock(ctx))

	rows, err := k.IterateActiveBlocksForEpoch(ctx, 1)
	require.NoError(t, err)
	require.Empty(t, rows)
}

func TestBeginBlockUsesActiveSetObservedAtBlockStart(t *testing.T) {
	coreSlots := &coreSlotKeeperMock{active: []coreslottypes.CoreSlot{
		accountingSlot(1, coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE),
	}}
	k, ctx, _ := setupAccountingKeeper(t, coreSlots, 1, types.DefaultParams())

	require.NoError(t, k.BeginBlock(ctx))
	coreSlots.active = []coreslottypes.CoreSlot{
		accountingSlot(2, coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE),
	}
	require.NoError(t, k.BeginBlock(ctx))

	slotOne, err := k.GetActiveBlocks(ctx, 1, 1)
	require.NoError(t, err)
	slotTwo, err := k.GetActiveBlocks(ctx, 1, 2)
	require.NoError(t, err)
	require.Equal(t, uint64(1), slotOne)
	require.Equal(t, uint64(1), slotTwo)
}

func TestBeginBlockAccountingIgnoresPauseFlags(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*types.Params)
	}{
		{name: "settlement disabled", mutate: func(p *types.Params) { p.EpochSettlementEnabled = false }},
		{name: "emissions disabled", mutate: func(p *types.Params) { p.EmissionsEnabled = false }},
		{name: "claims disabled", mutate: func(p *types.Params) { p.ClaimsEnabled = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := types.DefaultParams()
			tc.mutate(&params)
			coreSlots := &coreSlotKeeperMock{active: []coreslottypes.CoreSlot{
				accountingSlot(1, coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE),
			}}
			k, ctx, _ := setupAccountingKeeper(t, coreSlots, 1, params)

			require.NoError(t, k.BeginBlock(ctx))
			blocks, err := k.GetActiveBlocks(ctx, 1, 1)
			require.NoError(t, err)
			require.Equal(t, uint64(1), blocks)
		})
	}
}

func TestBeginBlockRequiresStateConfigAndCoreSlotRead(t *testing.T) {
	t.Run("missing state", func(t *testing.T) {
		k, ctx, _ := setupKeeper(t, &coreSlotKeeperMock{})
		require.ErrorIs(t, k.BeginBlock(ctx), collections.ErrNotFound)
	})

	t.Run("missing config", func(t *testing.T) {
		k, ctx, _ := setupKeeper(t, &coreSlotKeeperMock{})
		params := types.DefaultParams()
		require.NoError(t, k.SetParams(ctx, params))
		require.NoError(t, k.SetState(ctx, accountingState(1)))
		require.ErrorIs(t, k.BeginBlock(ctx), collections.ErrNotFound)
	})

	t.Run("CoreSlot read failure", func(t *testing.T) {
		expected := errors.New("CoreSlot read failed")
		coreSlots := &failingActiveSlotsKeeper{coreSlotKeeperMock: &coreSlotKeeperMock{}, err: expected}
		k, ctx, _ := setupAccountingKeeper(t, coreSlots, 1, types.DefaultParams())
		require.ErrorIs(t, k.BeginBlock(ctx), expected)
	})
}

type failingActiveSlotsKeeper struct {
	*coreSlotKeeperMock
	err error
}

func (m *failingActiveSlotsKeeper) GetActiveSlots(context.Context) ([]coreslottypes.CoreSlot, error) {
	return nil, m.err
}

func setupAccountingKeeper(
	t *testing.T,
	coreSlots keeper.CoreSlotKeeper,
	epoch uint64,
	params types.Params,
) (keeper.Keeper, context.Context, *bankKeeperMock) {
	t.Helper()
	k, ctx, bank := setupKeeper(t, coreSlots)
	require.NoError(t, k.SetParams(ctx, params))
	require.NoError(t, k.SetState(ctx, accountingState(epoch)))
	cfg, err := keeper.BuildEpochConfigSnapshot(params)
	require.NoError(t, err)
	require.NoError(t, k.SetCurrentEpochConfig(ctx, cfg))
	return k, ctx, bank
}

func accountingState(epoch uint64) types.RewardsState {
	return types.RewardsState{
		CurrentEpoch:            epoch,
		CurrentEpochStartHeight: 1,
		CumulativeEmitted:       "0",
		CarryForwardRemainder:   "0",
	}
}

func accountingSlot(id uint64, status coreslottypes.SlotStatus) coreslottypes.CoreSlot {
	return coreslottypes.CoreSlot{SlotId: id, Status: status}
}
