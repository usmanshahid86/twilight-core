package keeper_test

import (
	"testing"

	"cosmossdk.io/collections"
	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	"github.com/twilight-project/twilight-core/x/rewards/keeper"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

func TestEndBlockFinalizesAndAllocatesEpoch(t *testing.T) {
	k, ctx, bank, _ := setupFinalization(t, false)
	ctx = ctx.WithBlockHeight(2)

	require.NoError(t, k.EndBlock(ctx))
	require.Equal(t, 1, bank.mintCalls)
	require.Equal(t, "20utwlt", bank.minted.String())

	epoch, found, err := k.GetFinalizedEpoch(ctx, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "20", epoch.MintedEmission)
	require.Equal(t, "20", epoch.RewardPool)
	require.Equal(t, "20", epoch.AllocatedAmount)
	require.Equal(t, "0", epoch.CarryOut)
	require.Len(t, epoch.Rewards, 2)
	require.Equal(t, []string{"5", "15"}, []string{epoch.Rewards[0].Amount, epoch.Rewards[1].Amount})
	require.True(t, hasEvent(ctx, types.EventTypeEpochFinalized))

	state, err := k.GetState(ctx)
	require.NoError(t, err)
	// Finalization does not advance the epoch; the next epoch opens at its own
	// first BeginBlock, which is also where a scheduled configuration is consumed.
	require.Equal(t, uint64(1), state.CurrentEpoch)
	require.Equal(t, uint64(1), state.CurrentEpochStartHeight)
	require.Equal(t, "20", state.CumulativeEmitted)
	_, err = k.GetActiveBlocks(ctx, 1, 1)
	require.ErrorIs(t, err, collections.ErrNotFound)
}

func TestFinalizeRejectsUnsupportedSnapshotModes(t *testing.T) {
	k, ctx, _, _ := setupFinalization(t, true)
	cfg, err := k.GetCurrentEpochConfig(ctx)
	require.NoError(t, err)
	cfg.WeightedRewardsEnabled = true
	require.NoError(t, k.CurrentEpochConfig.Set(ctx, cfg))

	require.ErrorIs(t, k.EndBlock(ctx.WithBlockHeight(2)), types.ErrUnsupportedFeature)
	_, found, err := k.GetFinalizedEpoch(ctx, 1)
	require.NoError(t, err)
	require.False(t, found)
}

// TestEndBlockFinalizesRegardlessOfPause replaces the retired settlement-pause
// readiness gate.
//
// A paused epoch must still close. If it did not, the boundary would pass, the
// next epoch would open at BeginBlock, and the skipped epoch could never be
// finalized under a later block's clock — a permanent hole in the finalized
// sequence rather than a delayed record.
func TestEndBlockFinalizesRegardlessOfPause(t *testing.T) {
	k, ctx, bank, _ := setupFinalization(t, false)

	// Before the boundary nothing happens.
	require.NoError(t, k.EndBlock(ctx.WithBlockHeight(1)))
	require.Zero(t, bank.mintCalls)

	// Paused, at the boundary: the epoch still finalizes.
	require.NoError(t, k.SetPauseState(ctx, types.RewardsPauseState{CurrentPaused: true}))
	require.NoError(t, k.EndBlock(ctx.WithBlockHeight(2)))

	epoch, found, err := k.GetFinalizedEpoch(ctx, 1)
	require.NoError(t, err)
	require.True(t, found, "a paused epoch must still produce a finalized record")
	require.Equal(t, uint64(2), epoch.RewardEnabledBlocks,
		"the fixture's counter is preserved on the finalized record")
}

// TestFullyPausedEpochFinalizesWithoutEmissionOrEpochHole is the canonical
// zero-accrual case: an epoch that counted no reward-enabled blocks.
func TestFullyPausedEpochFinalizesWithoutEmissionOrEpochHole(t *testing.T) {
	k, ctx, bank, _ := setupFinalization(t, true)
	// A fully paused epoch accrues nothing. That is the only input emission needs
	// — no separate switch suppresses the mint.
	require.NoError(t, k.SetOpenRewardEnabledBlocks(ctx, 0))
	require.NoError(t, k.SetPauseState(ctx, types.RewardsPauseState{CurrentPaused: true}))

	require.NoError(t, k.EndBlock(ctx.WithBlockHeight(2)))

	require.Zero(t, bank.mintCalls, "zero reward-enabled blocks must mint nothing")
	epoch, found, err := k.GetFinalizedEpoch(ctx, 1)
	require.NoError(t, err)
	require.True(t, found, "the epoch must still close")
	require.Equal(t, "0", epoch.MintedEmission)
	require.Zero(t, epoch.RewardEnabledBlocks)

	state, err := k.GetState(ctx)
	require.NoError(t, err)
	require.Equal(t, "0", state.CumulativeEmitted)
	// Carry is preserved rather than consumed: with no participation there is
	// nothing to allocate, so the pool rolls forward whole.
	require.Equal(t, "0", state.CarryForwardRemainder)

	// And the sequence is contiguous: epoch 1 exists, so epoch 2 can follow it.
	_, found, err = k.GetFinalizedEpoch(ctx, 2)
	require.NoError(t, err)
	require.False(t, found)
}

func TestFinalizeEmptyActiveSetMintsAndCarries(t *testing.T) {
	k, ctx, bank, _ := setupFinalization(t, true)
	require.NoError(t, k.EndBlock(ctx.WithBlockHeight(2)))
	require.Equal(t, "20utwlt", bank.minted.String())

	state, err := k.GetState(ctx)
	require.NoError(t, err)
	require.Equal(t, "20", state.CumulativeEmitted)
	require.Equal(t, "20", state.CarryForwardRemainder)
	epoch, found, err := k.GetFinalizedEpoch(ctx, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.Empty(t, epoch.Rewards)
}

func TestFinalizeEmissionsDisabledAndMintFailureAtomicity(t *testing.T) {
	t.Run("the retired emissions switch no longer suppresses the mint", func(t *testing.T) {
		// emissions_enabled was one of three independent pause authorities and
		// carries none now. Emission follows the canonical counter alone, so an
		// epoch that accrued blocks mints even with the deprecated flag cleared.
		k, ctx, bank, _ := setupFinalization(t, true)
		params, err := k.GetParams(ctx)
		require.NoError(t, err)
		params.EmissionsEnabled = false
		require.NoError(t, k.SetParams(ctx, params))
		require.NoError(t, k.EndBlock(ctx.WithBlockHeight(2)))
		require.Equal(t, 1, bank.mintCalls)
		state, err := k.GetState(ctx)
		require.NoError(t, err)
		require.Equal(t, "20", state.CumulativeEmitted)
	})

	t.Run("mint failure", func(t *testing.T) {
		k, ctx, bank, _ := setupFinalization(t, false)
		bank.failMint()
		require.Error(t, k.EndBlock(ctx.WithBlockHeight(2)))
		_, found, err := k.GetFinalizedEpoch(ctx, 1)
		require.NoError(t, err)
		require.False(t, found)
		state, err := k.GetState(ctx)
		require.NoError(t, err)
		require.Equal(t, uint64(1), state.CurrentEpoch)
	})
}

func TestFinalizeActivatesPendingParamsAfterBoundary(t *testing.T) {
	k, ctx, _, _ := setupFinalization(t, true)
	current, err := k.GetParams(ctx)
	require.NoError(t, err)
	pending := current
	// Epoch length is governed by the canonical history and can no longer travel
	// through pending params; the subsidy still can.
	pending.InitialBlockSubsidy = "3"
	require.NoError(t, k.SetPendingParams(ctx, pending))

	require.NoError(t, k.EndBlock(ctx.WithBlockHeight(2)))
	active, err := k.GetParams(ctx)
	require.NoError(t, err)
	require.Equal(t, "3", active.InitialBlockSubsidy)
	cfg, err := k.GetCurrentEpochConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, "3", cfg.InitialBlockSubsidy)
	// The snapshot's epoch-length mirror is repopulated from canonical history,
	// never from the promoted params, so it keeps the authoritative value.
	length, err := k.EpochLengthForEpoch(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, length, cfg.EpochLengthBlocks)
	_, found, err := k.GetPendingParams(ctx)
	require.NoError(t, err)
	require.False(t, found)
}

func TestPendingActivationFailureDoesNotHalfCommitFinalization(t *testing.T) {
	k, ctx, _, _ := setupFinalization(t, true)
	invalid := types.DefaultParams()
	invalid.EpochLengthBlocks = 0
	require.NoError(t, k.PendingParams.Set(ctx, invalid))

	require.Error(t, k.EndBlock(ctx.WithBlockHeight(2)))
	_, found, err := k.GetFinalizedEpoch(ctx, 1)
	require.NoError(t, err)
	require.False(t, found)
	state, err := k.GetState(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), state.CurrentEpoch)
}

func TestFinalizeClipsEmissionAndPreservesTerminalDust(t *testing.T) {
	t.Run("threshold clipping", func(t *testing.T) {
		k, ctx, bank, _ := setupFinalization(t, true)
		state, err := k.GetState(ctx)
		require.NoError(t, err)
		state.CumulativeEmitted = "499"
		require.NoError(t, k.SetState(ctx, state))

		require.NoError(t, k.EndBlock(ctx.WithBlockHeight(2)))
		require.Equal(t, "6utwlt", bank.minted.String())
		state, err = k.GetState(ctx)
		require.NoError(t, err)
		require.Equal(t, "505", state.CumulativeEmitted)
	})

	t.Run("terminal zero subsidy", func(t *testing.T) {
		k, ctx, bank, _ := setupFinalization(t, true)
		state, err := k.GetState(ctx)
		require.NoError(t, err)
		state.CumulativeEmitted = "999"
		require.NoError(t, k.SetState(ctx, state))

		require.NoError(t, k.EndBlock(ctx.WithBlockHeight(2)))
		require.Zero(t, bank.mintCalls)
		state, err = k.GetState(ctx)
		require.NoError(t, err)
		require.Equal(t, "999", state.CumulativeEmitted)
	})
}

func setupFinalization(t *testing.T, empty bool) (keeper.Keeper, sdk.Context, *bankKeeperMock, *coreSlotKeeperMock) {
	t.Helper()
	params := types.DefaultParams()
	params.MaxSupply = "1000"
	params.InitialBlockSubsidy = "10"
	params.EpochLengthBlocks = 2
	core := &coreSlotKeeperMock{
		slots: map[uint64]coreslottypes.CoreSlot{
			1: {SlotId: 1, OperatorAddress: addr(1), PayoutAddress: addr(2), Status: coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE, ConsensusPower: 999},
			2: {SlotId: 2, OperatorAddress: addr(3), PayoutAddress: addr(4), Status: coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE, ConsensusPower: 1},
		},
		weights: map[uint64]coreslottypes.OperatorRewardWeight{
			1: {SlotId: 1, FinalWeight: "100.000000000000000000"},
			2: {SlotId: 2, FinalWeight: "1.000000000000000000"},
		},
	}
	k, ctx, bank := setupAccountingKeeper(t, core, 1, params)
	// Emission is driven by the epoch's reward-enabled block count, which
	// BeginBlock would have accrued. These fixtures write participation directly,
	// so they must also state the count; a fully enabled epoch counts one per
	// block of its configured length.
	require.NoError(t, k.SetOpenRewardEnabledBlocks(ctx, params.EpochLengthBlocks))
	if !empty {
		require.NoError(t, k.SetActiveBlocks(ctx, 1, 1, 1))
		require.NoError(t, k.SetActiveBlocks(ctx, 1, 2, 3))
	}
	return k, ctx, bank, core
}
