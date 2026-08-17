package keeper_test

import (
	"context"
	"errors"
	"testing"

	"cosmossdk.io/collections"
	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

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

	// Move the open epoch forward to where the canonical history actually places
	// it. An arbitrary start height would be corruption, not a fixture: state and
	// history must name the same block for the epoch they both describe.
	state, err := k.GetState(ctx)
	require.NoError(t, err)
	state.CurrentEpoch = 2
	state.CurrentEpochStartHeight, err = k.EpochStartHeight(ctx, 2)
	require.NoError(t, err)
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

// TestConsensusCountersRefuseToWrap covers the two per-block counters that feed
// allocation and emission.
//
// Neither can reach its maximum on a real chain, which is exactly why an
// unchecked increment would be invisible until it mattered: wrapping a slot's
// participation to zero silently transfers its share of the pool to the other
// slots, and wrapping the reward-enabled count collapses the epoch's emission.
// Both are value transfers, not arithmetic curiosities.
func TestConsensusCountersRefuseToWrap(t *testing.T) {
	const maxUint64 = ^uint64(0)

	t.Run("a slot's active-block count", func(t *testing.T) {
		coreSlots := &coreSlotKeeperMock{active: []coreslottypes.CoreSlot{
			accountingSlot(1, coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE),
		}}
		k, ctx, _ := setupAccountingKeeper(t, coreSlots, 1, types.DefaultParams())
		require.NoError(t, k.SetActiveBlocks(ctx, 1, 1, maxUint64))

		require.ErrorIs(t, k.IncrementActiveBlocks(ctx, 1, 1), types.ErrInvalidState)
		require.ErrorIs(t, k.BeginBlock(ctx), types.ErrInvalidState)

		// And the wrapped value was never written.
		blocks, err := k.GetActiveBlocks(ctx, 1, 1)
		require.NoError(t, err)
		require.Equal(t, maxUint64, blocks)
	})

	t.Run("the open reward-enabled block count", func(t *testing.T) {
		k, ctx, _ := setupAccountingKeeper(t, &coreSlotKeeperMock{}, 1, types.DefaultParams())
		require.NoError(t, k.SetOpenRewardEnabledBlocks(ctx, maxUint64))

		require.ErrorIs(t, k.BeginBlock(ctx), types.ErrInvalidState)
		blocks, err := k.GetOpenRewardEnabledBlocks(ctx)
		require.NoError(t, err)
		require.Equal(t, maxUint64, blocks)
	})
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

	t.Run("missing epoch history", func(t *testing.T) {
		// BeginBlock resolves the next epoch's start from canonical history, not
		// from the deprecated snapshot. With no history it cannot decide whether
		// this block opens an epoch, and must fail closed rather than assume not.
		k, ctx, _ := setupKeeper(t, &coreSlotKeeperMock{})
		params := types.DefaultParams()
		require.NoError(t, k.SetParams(ctx, params))
		require.NoError(t, k.SetState(ctx, accountingState(1)))
		require.ErrorIs(t, k.BeginBlock(ctx), types.ErrEpochConfigNotFound)
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
) (keeper.Keeper, sdk.Context, *bankKeeperMock) {
	t.Helper()
	k, ctx, bank := setupKeeper(t, coreSlots)
	require.NoError(t, k.SetParams(ctx, params))
	state := accountingState(epoch)
	require.NoError(t, k.SetState(ctx, state))
	cfg, err := keeper.BuildEpochConfigSnapshot(params)
	require.NoError(t, err)
	require.NoError(t, k.SetCurrentEpochConfig(ctx, cfg))
	seedEpochTimeline(t, k, ctx, params, state)
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
