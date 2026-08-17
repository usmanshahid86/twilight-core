package keeper_test

import (
	"testing"

	"cosmossdk.io/collections"
	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	appparams "github.com/twilight-project/twilight-core/app/params"
	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	"github.com/twilight-project/twilight-core/x/rewards/keeper"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

func TestEndBlockFinalizesAndAllocatesEpoch(t *testing.T) {
	k, ctx, bank, _ := setupFinalization(t, false)
	ctx = ctx.WithBlockHeight(finalizationEndHeight)

	require.NoError(t, k.EndBlock(ctx))
	require.Equal(t, 1, bank.mintCalls)
	// 360 reward-enabled blocks at a subsidy of 10.
	require.Equal(t, "3600utwlt", bank.minted.String())

	epoch, found, err := k.GetFinalizedEpoch(ctx, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "3600", epoch.MintedEmission)
	require.Equal(t, "3600", epoch.RewardPool)
	require.Equal(t, "3600", epoch.AllocatedAmount)
	require.Equal(t, "0", epoch.CarryOut)
	require.Equal(t, uint64(360), epoch.RewardEnabledBlocks)
	require.Len(t, epoch.Rewards, 2)
	require.Equal(t, []string{"900", "2700"}, []string{epoch.Rewards[0].Amount, epoch.Rewards[1].Amount})
	require.True(t, hasEvent(ctx, types.EventTypeEpochFinalized))

	state, err := k.GetState(ctx)
	require.NoError(t, err)
	// Finalization does not advance the epoch; the next epoch opens at its own
	// first BeginBlock, which is also where a scheduled configuration is consumed.
	require.Equal(t, uint64(1), state.CurrentEpoch)
	require.Equal(t, uint64(1), state.CurrentEpochStartHeight)
	require.Equal(t, "3600", state.CumulativeEmitted)
	_, err = k.GetActiveBlocks(ctx, 1, 1)
	require.ErrorIs(t, err, collections.ErrNotFound)
}

func TestFinalizeRejectsUnsupportedSnapshotModes(t *testing.T) {
	k, ctx, _, _ := setupFinalization(t, true)
	cfg, err := k.GetCurrentEpochConfig(ctx)
	require.NoError(t, err)
	cfg.WeightedRewardsEnabled = true
	require.NoError(t, k.CurrentEpochConfig.Set(ctx, cfg))

	require.ErrorIs(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)), types.ErrUnsupportedFeature)
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
	require.NoError(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight-1)))
	require.Zero(t, bank.mintCalls)

	// Paused, at the boundary: the epoch still finalizes.
	require.NoError(t, k.SetPauseState(ctx, types.RewardsPauseState{CurrentPaused: true}))
	require.NoError(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))

	epoch, found, err := k.GetFinalizedEpoch(ctx, 1)
	require.NoError(t, err)
	require.True(t, found, "a paused epoch must still produce a finalized record")
	require.Equal(t, uint64(360), epoch.RewardEnabledBlocks,
		"the fixture's counter is preserved on the finalized record")
}

func TestFinalizeEmptyActiveSetMintsAndCarries(t *testing.T) {
	k, ctx, bank, _ := setupFinalization(t, true)
	require.NoError(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))
	require.Equal(t, "3600utwlt", bank.minted.String())

	state, err := k.GetState(ctx)
	require.NoError(t, err)
	require.Equal(t, "3600", state.CumulativeEmitted)
	require.Equal(t, "3600", state.CarryForwardRemainder)
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
		require.NoError(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))
		require.Equal(t, 1, bank.mintCalls)
		state, err := k.GetState(ctx)
		require.NoError(t, err)
		require.Equal(t, "3600", state.CumulativeEmitted)
	})

	t.Run("mint failure", func(t *testing.T) {
		k, ctx, bank, _ := setupFinalization(t, false)
		bank.failMint()
		require.Error(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))
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

	require.NoError(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))
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

	require.Error(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))
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
		// Five below the 500000 halving threshold: the first block is clipped to 5,
		// and the remaining 359 run at the halved subsidy of 5.
		state.CumulativeEmitted = "499995"
		require.NoError(t, k.SetState(ctx, state))

		require.NoError(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))
		require.Equal(t, "1800utwlt", bank.minted.String())
		state, err = k.GetState(ctx)
		require.NoError(t, err)
		require.Equal(t, "501795", state.CumulativeEmitted)
	})

	t.Run("terminal zero subsidy", func(t *testing.T) {
		k, ctx, bank, _ := setupFinalization(t, true)
		state, err := k.GetState(ctx)
		require.NoError(t, err)
		state.CumulativeEmitted = "999999"
		require.NoError(t, k.SetState(ctx, state))

		require.NoError(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))
		require.Zero(t, bank.mintCalls)
		state, err = k.GetState(ctx)
		require.NoError(t, err)
		require.Equal(t, "999999", state.CumulativeEmitted)
	})
}

// The finalization fixture runs at the ratified minimum epoch length.
//
// Finalization is a block-path transition against configured geometry, so the
// geometry has to be one a chain can be configured with. The participation rows
// are also arithmetically possible under that geometry — a slot credited in more
// blocks than the epoch counted is not a fixture, it is the corruption the
// finalizer now refuses.
const (
	finalizationEpochLength = appparams.HardMinEpochLengthBlocks // 360
	finalizationEndHeight   = int64(finalizationEpochLength)     // epoch 1 runs 1..360
)

// TestLateFinalizationFailsClosedWithoutAnyMonetaryEffect covers the height past
// the canonical boundary.
//
// This is not a delayed finalization to be caught up. The closing block already
// committed without minting, so running the transition now would mint for an
// epoch under a later block's clock, against counters the intervening blocks left
// behind. The assertion is therefore about absence: nothing monetary, and nothing
// persistent, may happen.
func TestLateFinalizationFailsClosedWithoutAnyMonetaryEffect(t *testing.T) {
	k, ctx, bank, _ := setupFinalization(t, false)

	// Give the treasury a share and the carry a nonzero value, so that a
	// finalization which did run would leave visible traces in both.
	params, err := k.GetParams(ctx)
	require.NoError(t, err)
	params.EmissionTreasuryShareBps = 1_000
	params.TreasuryAddress = addr(8)
	require.NoError(t, k.SetParams(ctx, params))
	cfg, err := keeper.BuildEpochConfigSnapshot(params)
	require.NoError(t, err)
	require.NoError(t, k.SetCurrentEpochConfig(ctx, cfg))

	state, err := k.GetState(ctx)
	require.NoError(t, err)
	state.CarryForwardRemainder = "7"
	require.NoError(t, k.SetState(ctx, state))

	err = k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight + 1))
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Contains(t, err.Error(), "due to finalize at height 360")

	require.Zero(t, bank.mintCalls, "no mint")
	require.Zero(t, bank.sendCalls, "no bank send")
	require.Empty(t, bank.sends)

	_, found, err := k.GetFinalizedEpoch(ctx, 1)
	require.NoError(t, err)
	require.False(t, found, "no finalized epoch write")

	after, err := k.GetState(ctx)
	require.NoError(t, err)
	require.Equal(t, "0", after.CumulativeEmitted, "no cumulative-emission mutation")
	require.Equal(t, "7", after.CarryForwardRemainder, "no carry mutation")
	require.Equal(t, uint64(1), after.CurrentEpoch)

	rows, err := k.IterateActiveBlocksForEpoch(ctx, 1)
	require.NoError(t, err)
	require.Len(t, rows, 2, "no participation cleanup")

	require.False(t, hasEvent(ctx, types.EventTypeEpochFinalized))
}

// TestFinalizationRefusesImpossibleParticipationCounters covers the two relations
// the block path cannot violate, and therefore the two that mean corruption.
//
// Both are checked BEFORE the first monetary operation, so the assertions are
// about the mint and the send never being attempted — not merely rolled back.
func TestFinalizationRefusesImpossibleParticipationCounters(t *testing.T) {
	t.Run("the open counter exceeds the canonical epoch length", func(t *testing.T) {
		k, ctx, bank, _ := setupFinalization(t, true)
		require.NoError(t, k.SetOpenRewardEnabledBlocks(ctx, finalizationEpochLength+1))

		err := k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight))
		require.ErrorIs(t, err, types.ErrInvalidState)
		require.Contains(t, err.Error(), "is only 360 blocks long")
		require.Zero(t, bank.mintCalls)
		require.Zero(t, bank.sendCalls)
		requireNothingFinalized(t, k, ctx)
	})

	t.Run("a participation row exceeds the reward-enabled count", func(t *testing.T) {
		k, ctx, bank, _ := setupFinalization(t, true)
		// A fully paused epoch counted no reward-enabled blocks, so no slot can have
		// been reward-active in one.
		require.NoError(t, k.SetOpenRewardEnabledBlocks(ctx, 0))
		require.NoError(t, k.SetActiveBlocks(ctx, 1, 1, 1))

		err := k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight))
		require.ErrorIs(t, err, types.ErrInvalidState)
		require.Contains(t, err.Error(), "recorded active for 1 blocks")
		require.Zero(t, bank.mintCalls)
		require.Zero(t, bank.sendCalls)
		requireNothingFinalized(t, k, ctx)
	})

	t.Run("corrupt participation rolls the whole finalization back", func(t *testing.T) {
		k, ctx, bank, _ := setupFinalization(t, false)
		// One row above the count. Everything else about the epoch is valid, so a
		// finalizer that validated late would already have minted by the time it
		// noticed.
		require.NoError(t, k.SetActiveBlocks(ctx, 1, 2, finalizationEpochLength+5))

		require.Error(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))
		require.Zero(t, bank.mintCalls)
		require.Zero(t, bank.sendCalls)
		requireNothingFinalized(t, k, ctx)

		// The participation rows themselves survive: cleanup is part of the
		// transition that did not happen.
		rows, err := k.IterateActiveBlocksForEpoch(ctx, 1)
		require.NoError(t, err)
		require.Len(t, rows, 2)
	})
}

// requireNothingFinalized asserts the epoch neither closed nor moved any of the
// running totals finalization owns.
func requireNothingFinalized(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
	t.Helper()
	_, found, err := k.GetFinalizedEpoch(ctx, 1)
	require.NoError(t, err)
	require.False(t, found)
	state, err := k.GetState(ctx)
	require.NoError(t, err)
	require.Equal(t, "0", state.CumulativeEmitted)
	require.Equal(t, "0", state.CarryForwardRemainder)
	require.Equal(t, uint64(1), state.CurrentEpoch)
}

func setupFinalization(t *testing.T, empty bool) (keeper.Keeper, sdk.Context, *bankKeeperMock, *coreSlotKeeperMock) {
	t.Helper()
	params := types.DefaultParams()
	params.MaxSupply = "1000000"
	params.InitialBlockSubsidy = "10"
	params.EpochLengthBlocks = finalizationEpochLength
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
		// 90 + 270 = 360, the epoch's reward-enabled block count: a 1:3 split of a
		// fully enabled epoch.
		require.NoError(t, k.SetActiveBlocks(ctx, 1, 1, 90))
		require.NoError(t, k.SetActiveBlocks(ctx, 1, 2, 270))
	}
	return k, ctx, bank, core
}
