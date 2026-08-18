package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/x/mining/keeper"
	"github.com/twilight-project/twilight-core/x/mining/types"
	rewardstypes "github.com/twilight-project/twilight-core/x/rewards/types"
)

// The settlement clock, epoch anchoring, and deterministic materialization.
//
// Two properties are asserted throughout and are worth stating once. The clock is
// not a block height — it counts only blocks on which the chain was willing to move
// value — and materialization performs no bank operation, which is proven by the
// release methods never being called rather than by comparing balances.

func entitlement(slotID, epoch uint64, amount string) rewardstypes.SlotEntitlement {
	return rewardstypes.SlotEntitlement{
		SlotId:                         slotID,
		Epoch:                          epoch,
		TotalBlocksActive:              10,
		EntitlementAmount:              amount,
		ReleasedAmount:                 "0",
		PayoutAddress:                  "twilight1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq",
		RewardConfigVersion:            1,
		ActivationSequenceAtEpochClose: 1,
		CreatedHeight:                  100,
	}
}

// initialized is the fixture every consensus-transition test in this gate uses.
//
// It asserts on the way out that neither release method was reached. No path this
// gate exercises may move value, so the check belongs to the fixture rather than to
// the two tests that happen to be about it — a test added later that quietly
// released would otherwise pass.
func initialized(t *testing.T) (keeper.Keeper, sdk.Context, *rewardsKeeperMock) {
	t.Helper()
	k, ctx, rewards := setupKeeperWithRewards(t, &coreSlotKeeperMock{}, newRewardsMock())
	require.NoError(t, k.InitGenesis(ctx, *types.DefaultGenesis()))
	t.Cleanup(func() {
		require.Zero(t, rewards.payCalls, "no consensus path may release participant value")
		require.Zero(t, rewards.remainderCalls, "no consensus path may release the operator remainder")
	})
	return k, ctx, rewards
}

// TestSettlementClockCountsReleaseEnabledBlocksNotHeights is the distinction the
// whole deadline model rests on.
//
// If the clock were a block height, a pause would consume a settlement's window
// while the chain was refusing to let anyone use it. Counting only release-enabled
// blocks is what makes a pause freeze deadlines instead.
func TestSettlementClockCountsReleaseEnabledBlocksNotHeights(t *testing.T) {
	k, ctx, rewards := initialized(t)

	for i := 0; i < 3; i++ {
		require.NoError(t, k.EndBlock(ctx))
	}
	clock, err := k.GetSettlementClock(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(3), clock, "one tick per release-enabled block")

	// Pause: blocks keep happening, the clock does not.
	rewards.releaseEnabled = false
	for i := 0; i < 5; i++ {
		require.NoError(t, k.EndBlock(ctx))
	}
	clock, err = k.GetSettlementClock(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(3), clock, "a paused chain produces blocks but no settlement time")

	// Resume: it continues from where it stopped rather than catching up.
	rewards.releaseEnabled = true
	require.NoError(t, k.EndBlock(ctx))
	clock, err = k.GetSettlementClock(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(4), clock, "the paused blocks are not repaid on resume")
}

// TestMaterializationCreatesExactlyOneSettlementPerNonzeroEntitlement is the 1:1
// rule, together with the two absences that make it exact.
func TestMaterializationCreatesExactlyOneSettlementPerNonzeroEntitlement(t *testing.T) {
	k, ctx, rewards := initialized(t)
	rewards.finalize(1,
		entitlement(1, 1, "500"),
		entitlement(2, 1, "300"),
	)

	require.NoError(t, k.EndBlock(ctx))

	for _, slotID := range []uint64{1, 2} {
		settlement, found, err := k.GetSettlement(ctx, slotID, 1)
		require.NoError(t, err)
		require.Truef(t, found, "slot %d must have a settlement", slotID)
		require.Equal(t, types.SettlementMode_SETTLEMENT_MODE_TRUSTED_AS, settlement.SettlementMode)
		require.Zero(t, settlement.NextChunkIndex)
		require.False(t, settlement.Finalized)
		require.Equal(t,
			types.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_UNSPECIFIED,
			settlement.FinalizationReason,
			"an open settlement records no authorization arm")
	}

	// A Slot that earned nothing has no entitlement and therefore no settlement.
	_, found, err := k.GetSettlement(ctx, 3, 1)
	require.NoError(t, err)
	require.False(t, found)

	// Exactly one anchor, carrying the clock this block produced.
	anchor, found, err := k.GetSettlementEpochAnchor(ctx, 1)
	require.NoError(t, err)
	require.True(t, found)
	clock, err := k.GetSettlementClock(ctx)
	require.NoError(t, err)
	require.Equal(t, clock, anchor.CreatedSettlementClock)

	cursor, err := k.GetLastProcessedRewardEpoch(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), cursor)

	// The whole point of this gate: nothing moved.
	require.Zero(t, rewards.payCalls, "materialization must not release participant value")
	require.Zero(t, rewards.remainderCalls, "materialization must not release the operator remainder")
}

// TestEmptyEpochCreatesNothingAndStillAdvancesTheCursor covers the case that is
// easy to implement as an error.
//
// A fully paused epoch, or one in which no Slot participated, finalizes with no
// nonzero entitlement. It produces no settlement and no anchor — and the cursor
// must still advance, or every later epoch would be blocked behind it forever.
func TestEmptyEpochCreatesNothingAndStillAdvancesTheCursor(t *testing.T) {
	k, ctx, rewards := initialized(t)
	rewards.finalize(1)

	require.NoError(t, k.EndBlock(ctx))

	_, found, err := k.GetSettlementEpochAnchor(ctx, 1)
	require.NoError(t, err)
	require.False(t, found, "an epoch with no settlements has no anchor")

	cursor, err := k.GetLastProcessedRewardEpoch(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), cursor, "the cursor advances past an empty epoch")

	// And the next epoch is still materializable.
	rewards.finalize(2, entitlement(1, 2, "100"))
	require.NoError(t, k.EndBlock(ctx))
	_, found, err = k.GetSettlement(ctx, 1, 2)
	require.NoError(t, err)
	require.True(t, found)
}

// TestMaterializationRefusesToFallBehind pins the reason the cursor may not lag.
//
// An epoch materialized in a later block would be anchored to that block's
// settlement clock, silently moving every deadline it derives. Falling behind is
// therefore corruption rather than a backlog to drain, and the block halts.
func TestMaterializationRefusesToFallBehind(t *testing.T) {
	k, ctx, rewards := initialized(t)
	rewards.finalize(1, entitlement(1, 1, "500"))
	rewards.finalize(2, entitlement(1, 2, "500"))

	err := k.EndBlock(ctx)
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Contains(t, err.Error(), "fallen behind")

	// The whole transition is discarded: no settlement, no anchor, no cursor move.
	_, found, err := k.GetSettlement(ctx, 1, 1)
	require.NoError(t, err)
	require.False(t, found)
	cursor, err := k.GetLastProcessedRewardEpoch(ctx)
	require.NoError(t, err)
	require.Zero(t, cursor)
	clock, err := k.GetSettlementClock(ctx)
	require.NoError(t, err)
	require.Zero(t, clock, "the clock tick is discarded with the rest of the transition")
}

// TestMaterializationIsAtomicAcrossTheWholeEpoch proves the set is all-or-nothing.
//
// The second entitlement is malformed, so materialization fails partway. Nothing
// from the block may survive — including the first settlement, which was already
// written when the failure occurred.
func TestMaterializationIsAtomicAcrossTheWholeEpoch(t *testing.T) {
	k, ctx, rewards := initialized(t)
	broken := entitlement(2, 1, "300")
	broken.Epoch = 7 // declares an epoch other than the one it was enumerated for
	rewards.finalize(1, entitlement(1, 1, "500"), broken)

	require.Error(t, k.EndBlock(ctx))

	_, found, err := k.GetSettlement(ctx, 1, 1)
	require.NoError(t, err)
	require.False(t, found, "the first settlement must not survive a failure on the second")
	_, found, err = k.GetSettlementEpochAnchor(ctx, 1)
	require.NoError(t, err)
	require.False(t, found)
	cursor, err := k.GetLastProcessedRewardEpoch(ctx)
	require.NoError(t, err)
	require.Zero(t, cursor)
}

// TestASettlementIsNeverOverwritten pins the write-once rule on the row that
// records how much of an entitlement has already been released.
//
// A settlement is an obligation workflow over value that can leave escrow exactly
// once. Overwriting one resets next_chunk_index, which re-authorizes every chunk
// that was already paid — so a second creation must halt the block rather than
// replace the row.
func TestASettlementIsNeverOverwritten(t *testing.T) {
	k, ctx, rewards := initialized(t)
	inProgress := types.Settlement{
		SlotId: 1, Epoch: 1,
		DistributionModeVersion: 1,
		SettlementMode:          types.SettlementMode_SETTLEMENT_MODE_TRUSTED_AS,
		SettlementParamsVersion: 1,
		NextChunkIndex:          2,
	}
	require.NoError(t, k.Settlements.Set(ctx, collections.Join(uint64(1), uint64(1)), inProgress))
	rewards.finalize(1, entitlement(1, 1, "500"))

	err := k.EndBlock(ctx)
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Contains(t, err.Error(), "already exists")

	settlement, found, err := k.GetSettlement(ctx, 1, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(2), settlement.NextChunkIndex, "the released-chunk position is untouched")
}

// TestAnEpochAnchorIsNeverOverwritten pins the same rule on the value every
// deadline in that epoch is counted from.
//
// The anchor is the sole record of when an epoch's settlements were created. A
// second one would move deadlines that were already in force, reopening participant
// windows that had closed or closing ones that had not.
func TestAnEpochAnchorIsNeverOverwritten(t *testing.T) {
	k, ctx, rewards := initialized(t)
	require.NoError(t, k.SettlementEpochAnchors.Set(ctx, 1, types.SettlementEpochAnchor{
		Epoch: 1, CreatedSettlementClock: 900,
	}))
	rewards.finalize(1, entitlement(1, 1, "500"))

	err := k.EndBlock(ctx)
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Contains(t, err.Error(), "anchor already exists")

	anchor, found, err := k.GetSettlementEpochAnchor(ctx, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(900), anchor.CreatedSettlementClock)
}

// TestOpenIndexTracksTheCanonicalRows proves the derived index is written with the
// settlements it describes.
func TestOpenIndexTracksTheCanonicalRows(t *testing.T) {
	k, ctx, rewards := initialized(t)
	rewards.finalize(1, entitlement(1, 1, "500"), entitlement(2, 1, "300"))
	require.NoError(t, k.EndBlock(ctx))

	for _, slotID := range []uint64{1, 2} {
		epoch, err := k.OpenSettlementsBySlot.Get(ctx, collections.Join(slotID, uint64(1)))
		require.NoError(t, err)
		require.Equal(t, uint64(1), epoch)
	}
}

// TestDerivedDeadlineCountsSettlementEnabledBlocks is the arithmetic every
// authorization decision depends on.
//
// window_blocks is the configured window multiplied by the target's epoch length,
// and the deadline counts that many CLOCK ticks forward from the anchor — not that
// many heights.
func TestDerivedDeadlineCountsSettlementEnabledBlocks(t *testing.T) {
	k, ctx, rewards := initialized(t)
	// Three release-enabled blocks before the epoch closes, so the anchor is 4.
	for i := 0; i < 3; i++ {
		require.NoError(t, k.EndBlock(ctx))
	}
	rewards.finalize(1, entitlement(1, 1, "500"))
	require.NoError(t, k.EndBlock(ctx))

	settlement, found, err := k.GetSettlement(ctx, 1, 1)
	require.NoError(t, err)
	require.True(t, found)
	anchor, found, err := k.GetSettlementEpochAnchor(ctx, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(4), anchor.CreatedSettlementClock)

	params, err := k.SettlementParamsForTarget(ctx, 1)
	require.NoError(t, err)

	window, err := k.SettlementWindowBlocks(ctx, 1, params)
	require.NoError(t, err)
	require.Equal(t, types.DefaultSettlementWindowEpochs*360, window)

	deadline, err := k.DeadlineClock(ctx, settlement, anchor, params)
	require.NoError(t, err)
	require.Equal(t, anchor.CreatedSettlementClock+window, deadline)
}

// TestASettlementDeadlineIsResolvedFromItsOwnEpochAnchor covers the entry point
// every later authorization decision will go through.
//
// It resolves both inputs itself, so the three ways a deadline could be derived
// from state that does not describe the obligation are all closed here rather than
// left to each caller: a missing anchor, an anchor from another epoch, and
// parameters other than the ones the row was created under.
func TestASettlementDeadlineIsResolvedFromItsOwnEpochAnchor(t *testing.T) {
	k, ctx, rewards := initialized(t)
	rewards.finalize(1, entitlement(1, 1, "500"))
	require.NoError(t, k.EndBlock(ctx))

	settlement, found, err := k.GetSettlement(ctx, 1, 1)
	require.NoError(t, err)
	require.True(t, found)
	anchor, _, err := k.GetSettlementEpochAnchor(ctx, 1)
	require.NoError(t, err)

	deadline, err := k.SettlementDeadlineClock(ctx, settlement)
	require.NoError(t, err)
	require.Equal(t, anchor.CreatedSettlementClock+types.DefaultSettlementWindowEpochs*360, deadline)

	// Parameters other than the ones the row records are refused rather than used.
	forged := settlement
	forged.SettlementParamsVersion = 2
	_, err = k.SettlementDeadlineClock(ctx, forged)
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Contains(t, err.Error(), "binds version 1")

	// A settlement whose anchor has gone is corruption, not an open-ended deadline.
	require.NoError(t, k.SettlementEpochAnchors.Remove(ctx, 1))
	_, err = k.SettlementDeadlineClock(ctx, settlement)
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Contains(t, err.Error(), "no settlement epoch anchor")
}

// TestOperatorOnlyDeadlineIsTheAnchorItself covers the arm this profile cannot
// reach but the handler shape must carry.
//
// An OPERATOR_ONLY settlement has no participant window to consume, so its deadline
// is the anchor clock and it is permissionlessly finalizable immediately.
func TestOperatorOnlyDeadlineIsTheAnchorItself(t *testing.T) {
	k, ctx, rewards := initialized(t)
	rewards.finalize(1, entitlement(1, 1, "500"))
	require.NoError(t, k.EndBlock(ctx))

	settlement, _, err := k.GetSettlement(ctx, 1, 1)
	require.NoError(t, err)
	settlement.SettlementMode = types.SettlementMode_SETTLEMENT_MODE_OPERATOR_ONLY

	anchor, _, err := k.GetSettlementEpochAnchor(ctx, 1)
	require.NoError(t, err)
	params, err := k.SettlementParamsForTarget(ctx, 1)
	require.NoError(t, err)

	deadline, err := k.DeadlineClock(ctx, settlement, anchor, params)
	require.NoError(t, err)
	require.Equal(t, anchor.CreatedSettlementClock, deadline)
}

// TestParticipantCeilingIsTotalOverEveryMode pins the totality of the switch.
//
// The ceiling is a monetary authorization, so a default arm would silently answer
// for a mode nobody had considered. Two arms are unreachable in this profile and
// are asserted anyway, from directly constructed state.
func TestParticipantCeilingIsTotalOverEveryMode(t *testing.T) {
	owed := entitlement(1, 1, "500")

	for _, tc := range []struct {
		mode types.SettlementMode
		want string
	}{
		{types.SettlementMode_SETTLEMENT_MODE_TRUSTED_AS, "500"},
		{types.SettlementMode_SETTLEMENT_MODE_SELECTED_PARTICIPANTS, "500"},
		{types.SettlementMode_SETTLEMENT_MODE_OPERATOR_ONLY, "0"},
	} {
		t.Run(tc.mode.String(), func(t *testing.T) {
			ceiling, err := keeper.ParticipantDistributionCeiling(
				types.Settlement{SlotId: 1, Epoch: 1, SettlementMode: tc.mode}, owed)
			require.NoError(t, err)
			require.Equal(t, tc.want, ceiling.String())
		})
	}

	t.Run("an unset mode has no ceiling", func(t *testing.T) {
		_, err := keeper.ParticipantDistributionCeiling(
			types.Settlement{SlotId: 1, Epoch: 1}, owed)
		require.ErrorIs(t, err, types.ErrInvalidState)
	})
}

// TestProtocolSelectionTargetIsRefusedRatherThanGuessed guards the boundary this
// profile must not cross.
//
// A target bound to protocol Selection has no candidate set, no commitment, no
// beacon and no result in this profile. Materializing it would mean inventing an
// applicability answer, so the block halts instead.
func TestProtocolSelectionTargetIsRefusedRatherThanGuessed(t *testing.T) {
	k, ctx, rewards := initialized(t)
	require.NoError(t, k.DistributionModeVersions.Set(ctx, 1, types.MiningDistributionModeVersion{
		Version:        1,
		Mode:           types.MiningDistributionMode_MINING_DISTRIBUTION_MODE_PROTOCOL_SELECTION,
		ValidFromEpoch: 1,
	}))
	rewards.finalize(1, entitlement(1, 1, "500"))

	err := k.EndBlock(ctx)
	require.ErrorIs(t, err, types.ErrUnsupportedFeature)

	_, found, err := k.GetSettlement(ctx, 1, 1)
	require.NoError(t, err)
	require.False(t, found)
}

// TestAMisfiledFinalizedEpochIsNotProofThatItClosed is the key/value agreement
// rule on the finalized history.
//
// A row stored at epoch N that declares some other epoch is not evidence that N
// finalized. Accepting it would attribute an entitlement set, an anchor and a
// cursor advance to an epoch that never closed — and every deadline derived from
// that anchor would follow from a block that has nothing to do with it.
func TestAMisfiledFinalizedEpochIsNotProofThatItClosed(t *testing.T) {
	k, ctx, rewards := initialized(t)
	rewards.finalizeAs(1, 7)
	rewards.entitlements[1] = []rewardstypes.SlotEntitlement{entitlement(1, 1, "500")}

	err := k.EndBlock(ctx)
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Contains(t, err.Error(), "stored at epoch 1 declares epoch 7")

	// The whole EndBlock transition is discarded, clock tick included.
	_, found, err := k.GetSettlement(ctx, 1, 1)
	require.NoError(t, err)
	require.False(t, found, "no settlement")
	_, found, err = k.GetSettlementEpochAnchor(ctx, 1)
	require.NoError(t, err)
	require.False(t, found, "no epoch anchor")
	cursor, err := k.GetLastProcessedRewardEpoch(ctx)
	require.NoError(t, err)
	require.Zero(t, cursor, "no cursor movement")
	clock, err := k.GetSettlementClock(ctx)
	require.NoError(t, err)
	require.Zero(t, clock, "no partial EndBlock state survives the outer cache")
}

// TestAGapInFinalizedHistoryIsRefusedRatherThanSkipped closes the case where the
// expected epoch is ABSENT but a later one exists.
//
// Before this check, absence returned "nothing to materialize" immediately, so a
// gap — target missing, successor present — was silently tolerated forever: the
// cursor could never advance past the hole, and the successor would eventually be
// materialized against a settlement clock from the wrong block.
//
// The probe is exactly one epoch. Any gap at all is corruption, so one lookup
// settles it; nothing scans and nothing drains a backlog.
func TestAGapInFinalizedHistoryIsRefusedRatherThanSkipped(t *testing.T) {
	k, ctx, rewards := initialized(t)
	// Epoch 1 never finalized; epoch 2 did.
	rewards.finalize(2, entitlement(1, 2, "500"))

	err := k.EndBlock(ctx)
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Contains(t, err.Error(), "reward epoch 2 is finalized while epoch 1 is not yet materialized")

	_, found, err := k.GetSettlement(ctx, 1, 2)
	require.NoError(t, err)
	require.False(t, found, "no settlement")
	_, found, err = k.GetSettlementEpochAnchor(ctx, 2)
	require.NoError(t, err)
	require.False(t, found, "no epoch anchor")
	cursor, err := k.GetLastProcessedRewardEpoch(ctx)
	require.NoError(t, err)
	require.Zero(t, cursor, "no cursor movement")
	clock, err := k.GetSettlementClock(ctx)
	require.NoError(t, err)
	require.Zero(t, clock, "complete EndBlock cache rollback")
}

// TestAnOrdinaryUnfinalizedBlockStillAdvancesTheClock keeps the gap probe from
// turning every quiet block into a failure.
//
// Neither the target nor its successor exists, which is what almost every block
// looks like. That must stay an ordinary no-op with the clock still ticking.
func TestAnOrdinaryUnfinalizedBlockStillAdvancesTheClock(t *testing.T) {
	k, ctx, _ := initialized(t)

	for i := 0; i < 3; i++ {
		require.NoError(t, k.EndBlock(ctx))
	}
	clock, err := k.GetSettlementClock(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(3), clock)
	cursor, err := k.GetLastProcessedRewardEpoch(ctx)
	require.NoError(t, err)
	require.Zero(t, cursor)
}

// TestAMisfiledSuccessorIsAlsoRefused covers the probe's own identity check.
//
// A misfiled row must not be able to answer "later work is waiting" either. If it
// could, a record filed under the wrong key would halt the chain on a lie rather
// than being reported as the corruption it is.
func TestAMisfiledSuccessorIsAlsoRefused(t *testing.T) {
	k, ctx, rewards := initialized(t)
	rewards.finalizeAs(2, 9)

	err := k.EndBlock(ctx)
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Contains(t, err.Error(), "stored at epoch 2 declares epoch 9")

	clock, err := k.GetSettlementClock(ctx)
	require.NoError(t, err)
	require.Zero(t, clock)
}
