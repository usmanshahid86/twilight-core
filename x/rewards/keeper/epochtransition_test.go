package keeper_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	appparams "github.com/twilight-project/twilight-core/app/params"
	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	"github.com/twilight-project/twilight-core/x/rewards/keeper"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// The BeginBlock epoch transition.
//
// These cover the ordering that makes a block's reward treatment a property of
// the block rather than of what executed inside it: the epoch opens, its counter
// resets, a due pause applies, and only then is anything sampled or credited.

// Every configured length below is inside the ratified [360, 720] interval.
//
// These are block-path tests: they drive the real BeginBlock/EndBlock transition
// against configured geometry, and the geometry a chain can actually be
// configured with is bounded. Running them at a toy length would demonstrate the
// transition only for an epoch consensus refuses to admit. The pure recurrence is
// covered separately, where small lengths are legitimate.
const (
	shortEpoch = appparams.HardMinEpochLengthBlocks // 360
	longEpoch  = appparams.HardMaxEpochLengthBlocks // 720
)

// beginBlocks runs BeginBlock over an inclusive height range.
func beginBlocks(t *testing.T, k keeper.Keeper, ctx sdk.Context, from, to int64) {
	t.Helper()
	for height := from; height <= to; height++ {
		require.NoErrorf(t, k.BeginBlock(ctx.WithBlockHeight(height)), "BeginBlock at height %d", height)
	}
}

// transitionKeeper builds a keeper with one ACTIVE slot and a canonical history
// anchored at height 1 with the given epoch length.
func transitionKeeper(t *testing.T, length uint64) (keeper.Keeper, sdk.Context) {
	t.Helper()
	params := types.DefaultParams()
	params.EpochLengthBlocks = length
	core := &coreSlotKeeperMock{
		active: []coreslottypes.CoreSlot{
			accountingSlot(1, coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE),
			accountingSlot(2, coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE),
		},
		// Finalization snapshots the payout address of every participating slot,
		// so the records have to exist for an epoch that credited them.
		slots: map[uint64]coreslottypes.CoreSlot{
			1: {SlotId: 1, OperatorAddress: addr(1), PayoutAddress: addr(2), Status: coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE},
			2: {SlotId: 2, OperatorAddress: addr(3), PayoutAddress: addr(4), Status: coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE},
		},
		weights: map[uint64]coreslottypes.OperatorRewardWeight{
			1: {SlotId: 1, FinalWeight: "1.000000000000000000"},
			2: {SlotId: 2, FinalWeight: "1.000000000000000000"},
		},
	}
	k, ctx, _ := setupAccountingKeeper(t, core, 1, params)
	return k, ctx
}

// TestEpochAdvancesAtBeginBlockNotEndBlock is the trap the architecture calls out
// explicitly: an EndBlock advance makes every scheduled configuration unreachable,
// because the schedule is consumed at the first BeginBlock of the epoch it opens.
func TestEpochAdvancesAtBeginBlockNotEndBlock(t *testing.T) {
	k, ctx := transitionKeeper(t, shortEpoch)

	// Heights 1..360 are epoch 1.
	beginBlocks(t, k, ctx, 1, 360)
	state, err := k.GetState(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), state.CurrentEpoch)

	// The boundary EndBlock finalizes without advancing.
	require.NoError(t, k.EndBlock(ctx.WithBlockHeight(360)))
	state, err = k.GetState(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), state.CurrentEpoch, "EndBlock must not advance the epoch")

	// Height 361 opens epoch 2 through BeginBlock.
	require.NoError(t, k.BeginBlock(ctx.WithBlockHeight(361)))
	state, err = k.GetState(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), state.CurrentEpoch)
	require.Equal(t, uint64(361), state.CurrentEpochStartHeight)
}

// TestOpenCounterResetsBeforeSamplingTheNewEpoch is the arithmetic consequence of
// the reset ordering: without it the first block of every epoch would continue
// the previous epoch's total and inflate that epoch's emission by a whole epoch.
func TestOpenCounterResetsBeforeSamplingTheNewEpoch(t *testing.T) {
	k, ctx := transitionKeeper(t, shortEpoch)

	beginBlocks(t, k, ctx, 1, 360)
	blocks, err := k.GetOpenRewardEnabledBlocks(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(360), blocks, "epoch 1 counted every one of its blocks")

	require.NoError(t, k.EndBlock(ctx.WithBlockHeight(360)))

	require.NoError(t, k.BeginBlock(ctx.WithBlockHeight(361)))
	blocks, err = k.GetOpenRewardEnabledBlocks(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), blocks,
		"the first enabled block of a new epoch counts exactly 1, not the previous total plus one")
}

// TestScheduledConfigIsConsumedBeforeSamplingTheFirstBlock proves the schedule
// becomes immutable history at the opening block, and that the new geometry
// governs from there.
func TestScheduledConfigIsConsumedBeforeSamplingTheFirstBlock(t *testing.T) {
	k, ctx := transitionKeeper(t, shortEpoch)
	// The scheduled length must itself be admissible: consuming a schedule entry
	// creates canonical geometry, so it is an admission point like genesis.
	scheduledLen := longEpoch
	require.NoError(t, k.ScheduledEpochConfigs.Set(ctx, 2, types.ScheduledEpochConfig{
		EffectiveEpoch: 2, EpochLengthBlocks: scheduledLen,
	}))

	beginBlocks(t, k, ctx, 1, 360)
	require.NoError(t, k.EndBlock(ctx.WithBlockHeight(360)))
	require.NoError(t, k.BeginBlock(ctx.WithBlockHeight(361)))

	// The schedule entry is gone and history carries a new version anchored here.
	has, err := k.ScheduledEpochConfigs.Has(ctx, 2)
	require.NoError(t, err)
	require.False(t, has, "the schedule entry must be consumed exactly once")

	version, err := k.EpochConfigVersions.Get(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, uint64(2), version.Version)
	require.Equal(t, uint64(361), version.EffectiveStartHeight)
	require.Equal(t, scheduledLen, version.EpochLengthBlocks)

	// And epoch 2 now runs under the new length, starting at the block that
	// consumed the schedule.
	end, err := k.EpochEndHeight(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, 361+scheduledLen-1, end)

	// Sampling still happened for the opening block.
	blocks, err := k.GetOpenRewardEnabledBlocks(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), blocks)
}

// TestScheduledActivationRefreshesTheDeprecatedMirror covers the compatibility
// snapshot.
//
// The mirror carries no authority, but it is observable — EpochInfo returns it
// and finalization embeds it in the permanent epoch record. Left unrefreshed it
// would publish, and then archive, a length contradicting the geometry the epoch
// actually ran under.
func TestScheduledActivationRefreshesTheDeprecatedMirror(t *testing.T) {
	k, ctx := transitionKeeper(t, shortEpoch)
	require.NoError(t, k.ScheduledEpochConfigs.Set(ctx, 2, types.ScheduledEpochConfig{
		EffectiveEpoch: 2, EpochLengthBlocks: longEpoch,
	}))

	cfg, err := k.GetCurrentEpochConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, shortEpoch, cfg.EpochLengthBlocks)

	beginBlocks(t, k, ctx, 1, 360)
	require.NoError(t, k.EndBlock(ctx.WithBlockHeight(360)))
	require.NoError(t, k.BeginBlock(ctx.WithBlockHeight(361)))

	canonical, err := k.EpochLengthForEpoch(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, longEpoch, canonical)

	cfg, err = k.GetCurrentEpochConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, canonical, cfg.EpochLengthBlocks,
		"the mirror must not contradict the canonical length of the open epoch")

	// Non-geometry economics are untouched by the refresh: the schedule changes
	// the epoch's length, not its treasury or subsidy.
	require.Equal(t, types.DefaultParams().InitialBlockSubsidy, cfg.InitialBlockSubsidy)

	// And the query agrees with both.
	resp, err := keeper.NewQueryServer(k).EpochInfo(ctx, &types.QueryEpochInfoRequest{})
	require.NoError(t, err)
	require.Equal(t, canonical, resp.CurrentEpochLengthBlocks)
	require.Equal(t, canonical, resp.CurrentEpochConfig.EpochLengthBlocks)

	// Finalizing epoch 2 archives the same length in its embedded snapshot.
	beginBlocks(t, k, ctx, 362, 1080)
	require.NoError(t, k.EndBlock(ctx.WithBlockHeight(1080)))
	epoch, found, err := k.GetFinalizedEpoch(ctx, 2)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, canonical, epoch.Config.EpochLengthBlocks)
	require.Equal(t, uint64(361), epoch.StartHeight)
	require.Equal(t, uint64(1080), epoch.EndHeight)
}

// TestPauseAppliesAtHPlusOne covers the whole H/H+1 contract, including the
// same-block replacement rule.
func TestPauseAppliesAtHPlusOne(t *testing.T) {
	k, ctx := transitionKeeper(t, longEpoch)

	require.NoError(t, k.BeginBlock(ctx.WithBlockHeight(1)))

	// Accepted during height 1: scheduled for 2, not effective in 1.
	require.NoError(t, k.SchedulePauseTransition(ctx, 1, true))
	paused, err := k.RewardsPaused(ctx)
	require.NoError(t, err)
	require.False(t, paused, "H remains governed by the state effective at H")
	enabled, err := k.SettlementReleaseEnabled(ctx)
	require.NoError(t, err)
	require.True(t, enabled, "a pending H+1 pause must not be visible early")

	// A second change in the same block replaces the pending value.
	require.NoError(t, k.SchedulePauseTransition(ctx, 1, false))
	require.NoError(t, k.SchedulePauseTransition(ctx, 1, true))
	state, err := k.GetPauseState(ctx)
	require.NoError(t, err)
	require.True(t, state.PendingValue, "the last accepted value in the block wins")

	// Height 2 applies it before sampling: nothing is credited.
	require.NoError(t, k.BeginBlock(ctx.WithBlockHeight(2)))
	paused, err = k.RewardsPaused(ctx)
	require.NoError(t, err)
	require.True(t, paused)
	blocks, err := k.GetOpenRewardEnabledBlocks(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), blocks, "a paused block advances no counter")
	_, err = k.GetActiveBlocks(ctx, 1, 1)
	require.NoError(t, err)

	// Resume follows the same rule.
	require.NoError(t, k.SchedulePauseTransition(ctx, 2, false))
	require.NoError(t, k.BeginBlock(ctx.WithBlockHeight(3)))
	blocks, err = k.GetOpenRewardEnabledBlocks(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), blocks, "the resumed block is counted")
}

// TestPausedBlocksCreditNothing checks both counters together: §16 makes a paused
// block produce zero emission progression AND zero active reward credit.
func TestPausedBlocksCreditNothing(t *testing.T) {
	k, ctx := transitionKeeper(t, longEpoch)
	require.NoError(t, k.SetPauseState(ctx, types.RewardsPauseState{CurrentPaused: true}))

	require.NoError(t, k.BeginBlock(ctx.WithBlockHeight(1)))
	require.NoError(t, k.BeginBlock(ctx.WithBlockHeight(2)))

	blocks, err := k.GetOpenRewardEnabledBlocks(ctx)
	require.NoError(t, err)
	require.Zero(t, blocks)
	rows, err := k.IterateActiveBlocksForEpoch(ctx, 1)
	require.NoError(t, err)
	require.Empty(t, rows, "no slot may be credited during a pause")
}

// TestFullyPausedEpochsFinalizeContiguously drives the whole sequence rather
// than asserting the absence of a record.
//
// "Epoch 1 finalized and epoch 2 does not exist yet" is also what a permanent
// hole looks like from the outside, so it proves nothing on its own. What has to
// be shown is that the sequence CONTINUES: a fully paused epoch closes, the next
// one opens at its own first BeginBlock, and it closes too — with contiguous
// numbering and no epoch skipped.
func TestFullyPausedEpochsFinalizeContiguously(t *testing.T) {
	params := types.DefaultParams()
	params.EpochLengthBlocks = shortEpoch
	core := &coreSlotKeeperMock{active: []coreslottypes.CoreSlot{
		accountingSlot(1, coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE),
	}}
	k, ctx, bank := setupAccountingKeeper(t, core, 1, params)
	require.NoError(t, k.SetPauseState(ctx, types.RewardsPauseState{CurrentPaused: true}))

	// Epoch 1: heights 1..360, every block paused.
	beginBlocks(t, k, ctx, 1, 360)
	require.NoError(t, k.EndBlock(ctx.WithBlockHeight(360)))

	// Epoch 2 opens at 361 and runs to 720, still paused throughout.
	beginBlocks(t, k, ctx, 361, 720)
	state, err := k.GetState(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), state.CurrentEpoch, "BeginBlock must open the next epoch")
	require.Equal(t, uint64(361), state.CurrentEpochStartHeight)
	require.NoError(t, k.EndBlock(ctx.WithBlockHeight(720)))

	// Both epochs exist, in order, with nothing between or beyond them.
	for _, number := range []uint64{1, 2} {
		epoch, found, err := k.GetFinalizedEpoch(ctx, number)
		require.NoErrorf(t, err, "epoch %d", number)
		require.Truef(t, found, "epoch %d must have closed", number)
		require.Equal(t, number, epoch.EpochNumber)
		require.Zerof(t, epoch.RewardEnabledBlocks, "epoch %d counted no reward-enabled blocks", number)
		require.Equal(t, "0", epoch.MintedEmission)
	}
	require.Equal(t, uint64(1), firstFinalizedEpoch(t, k, ctx))
	require.Equal(t, uint64(2), lastFinalizedEpoch(t, k, ctx))
	require.Equal(t, 2, countFinalizedEpochs(t, k, ctx), "the sequence must have no hole and no extra")

	// A fully paused stretch emits nothing at all.
	require.Zero(t, bank.mintCalls)
	require.Zero(t, bank.sendCalls)
	after, err := k.GetState(ctx)
	require.NoError(t, err)
	require.Equal(t, "0", after.CumulativeEmitted)
}

func finalizedEpochNumbers(t *testing.T, k keeper.Keeper, ctx sdk.Context) []uint64 {
	t.Helper()
	numbers := make([]uint64, 0)
	require.NoError(t, k.FinalizedEpochs.Walk(ctx, nil, func(key uint64, _ types.EpochReward) (bool, error) {
		numbers = append(numbers, key)
		return false, nil
	}))
	return numbers
}

func countFinalizedEpochs(t *testing.T, k keeper.Keeper, ctx sdk.Context) int {
	t.Helper()
	return len(finalizedEpochNumbers(t, k, ctx))
}

func firstFinalizedEpoch(t *testing.T, k keeper.Keeper, ctx sdk.Context) uint64 {
	t.Helper()
	numbers := finalizedEpochNumbers(t, k, ctx)
	require.NotEmpty(t, numbers)
	return numbers[0]
}

func lastFinalizedEpoch(t *testing.T, k keeper.Keeper, ctx sdk.Context) uint64 {
	t.Helper()
	numbers := finalizedEpochNumbers(t, k, ctx)
	require.NotEmpty(t, numbers)
	return numbers[len(numbers)-1]
}

// TestPauseDoesNotStopEpochAdvancement is the counterweight to the two tests
// above: accrual stops, epoch time does not.
func TestPauseDoesNotStopEpochAdvancement(t *testing.T) {
	k, ctx := transitionKeeper(t, shortEpoch)
	require.NoError(t, k.SetPauseState(ctx, types.RewardsPauseState{CurrentPaused: true}))

	beginBlocks(t, k, ctx, 1, 361)
	state, err := k.GetState(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), state.CurrentEpoch, "epoch numbering continues while paused")
}

// TestEveryActiveSlotIsCreditedExactlyOnce pins the per-slot credit.
func TestEveryActiveSlotIsCreditedExactlyOnce(t *testing.T) {
	k, ctx := transitionKeeper(t, longEpoch)
	require.NoError(t, k.BeginBlock(ctx.WithBlockHeight(1)))

	for _, slot := range []uint64{1, 2} {
		blocks, err := k.GetActiveBlocks(ctx, 1, slot)
		require.NoError(t, err)
		require.Equalf(t, uint64(1), blocks, "slot %d", slot)
	}
	blocks, err := k.GetOpenRewardEnabledBlocks(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), blocks, "the block counter advances once per block, not once per slot")
}

// TestBeginBlockRollsBackCompletelyOnFailure proves the cache boundary.
//
// The failure is injected AFTER the epoch has opened and the counter has been
// reset, so a partial commit would be observable: the epoch would be open with a
// reset counter and no participation credited for the block that opened it.
func TestBeginBlockRollsBackCompletelyOnFailure(t *testing.T) {
	params := types.DefaultParams()
	params.EpochLengthBlocks = shortEpoch
	failure := errors.New("CoreSlot read failed")
	core := &failingActiveSlotsKeeper{coreSlotKeeperMock: &coreSlotKeeperMock{}, err: failure}
	k, ctx, _ := setupAccountingKeeper(t, core, 1, params)

	// Height 361 is the first block of epoch 2, so the failure lands after the
	// epoch has opened and the counter has been reset inside the cache.
	require.ErrorIs(t, k.BeginBlock(ctx.WithBlockHeight(361)), failure)

	// Nothing of the transition survived: the epoch did not open and the counter
	// was not reset.
	state, err := k.GetState(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), state.CurrentEpoch, "a failed BeginBlock must not open an epoch")
	require.Equal(t, uint64(1), state.CurrentEpochStartHeight)
}

// TestBeginBlockFailsClosedOnCorruptActiveSet checks the external snapshot is
// validated in full before any credit is written.
func TestBeginBlockFailsClosedOnCorruptActiveSet(t *testing.T) {
	params := types.DefaultParams()
	params.EpochLengthBlocks = longEpoch
	core := &coreSlotKeeperMock{active: []coreslottypes.CoreSlot{
		accountingSlot(1, coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE),
		// The contract says every returned slot is ACTIVE. One that is not means
		// the index and the records disagree.
		accountingSlot(2, coreslottypes.SlotStatus_SLOT_STATUS_SUSPENDED),
	}}
	k, ctx, _ := setupAccountingKeeper(t, core, 1, params)

	require.ErrorIs(t, k.BeginBlock(ctx.WithBlockHeight(1)), types.ErrInvalidState)

	// The valid slot ahead of the invalid one must not have been credited.
	rows, err := k.IterateActiveBlocksForEpoch(ctx, 1)
	require.NoError(t, err)
	require.Empty(t, rows, "no slot may be credited from a snapshot that failed validation")
	blocks, err := k.GetOpenRewardEnabledBlocks(ctx)
	require.NoError(t, err)
	require.Zero(t, blocks)
}

// TestBeginBlockRefusesToSkipAnEpochBoundary covers the divergence case: a
// boundary is consumed exactly once, so arriving past one means a block that owed
// the transition never ran it.
func TestBeginBlockRefusesToSkipAnEpochBoundary(t *testing.T) {
	k, ctx := transitionKeeper(t, shortEpoch)
	// Epoch 2 opens at height 361; arriving at 362 with epoch 1 still open is
	// divergence, not a late transition to catch up on.
	require.ErrorIs(t, k.BeginBlock(ctx.WithBlockHeight(362)), types.ErrInvalidState)
}

// TestPendingPauseTransitionCannotBeAppliedLate is the same principle for the
// pause schedule.
func TestPendingPauseTransitionCannotBeAppliedLate(t *testing.T) {
	k, ctx := transitionKeeper(t, longEpoch)
	require.NoError(t, k.SetPauseState(ctx, types.RewardsPauseState{
		HasPending: true, PendingValue: true, PendingEffectiveHeight: 2,
	}))
	// Height 5: the transition was due at 2 and never applied.
	require.ErrorIs(t, k.BeginBlock(ctx.WithBlockHeight(5)), types.ErrInvalidState)
}

// TestOpenCounterIsNeverDefaulted proves an absent counter is treated as
// corruption rather than as zero.
//
// Defaulting would silently reset the block count that drives emission for the
// epoch being finalized: the chain would mint as though the epoch accrued nothing.
func TestOpenCounterIsNeverDefaulted(t *testing.T) {
	k, ctx := transitionKeeper(t, shortEpoch)
	require.NoError(t, k.OpenRewardEnabledBlocks.Remove(ctx))

	_, err := k.GetOpenRewardEnabledBlocks(ctx)
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.ErrorIs(t, k.EndBlock(ctx.WithBlockHeight(360)), types.ErrInvalidState)
}

// TestScheduledConfigOutsideTheRatifiedBoundsIsRefused proves the schedule is an
// admission point, not just a carrier.
//
// Consuming an entry creates a canonical EpochConfigVersion, so a length outside
// the ratified interval would install geometry the protocol forbids — and unlike
// genesis, it would do so at runtime with no operator review.
func TestScheduledConfigOutsideTheRatifiedBoundsIsRefused(t *testing.T) {
	for _, length := range []uint64{
		appparams.HardMinEpochLengthBlocks - 1,
		appparams.HardMaxEpochLengthBlocks + 1,
	} {
		k, ctx := transitionKeeper(t, shortEpoch)
		require.NoError(t, k.ScheduledEpochConfigs.Set(ctx, 2, types.ScheduledEpochConfig{
			EffectiveEpoch: 2, EpochLengthBlocks: length,
		}))

		err := k.BeginBlock(ctx.WithBlockHeight(361))
		require.Errorf(t, err, "scheduled length %d is outside the ratified interval", length)
		require.ErrorIs(t, err, types.ErrInvalidState)

		// And nothing of the transition survived.
		state, err := k.GetState(ctx)
		require.NoError(t, err)
		require.Equal(t, uint64(1), state.CurrentEpoch, "a refused schedule must not open the epoch")
	}
}
