package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/twilight-project/twilight-core/x/mining/keeper"
	"github.com/twilight-project/twilight-core/x/mining/types"
)

// The three version histories, the boundary at which a scheduled change becomes
// effective, and the derived version indexes.
//
// # How promotion is exercised
//
// This profile exposes no update transaction, so there is no message that writes a
// schedule. The promotion primitive is nonetheless production-shaped and is driven
// here the way consensus drives it: a scheduled row is placed directly in state and
// the epoch boundary is then reached by closing a reward epoch through EndBlock.
// Nothing below admits a scheduled change through genesis, which fresh-genesis
// validation refuses on purpose, and nothing below adds a message to make testing
// convenient.
//
// Epochs are closed with no entitlements wherever the settlements themselves are
// not the subject. An empty epoch still advances the cursor, so it reaches the
// boundary without dragging in materialization.

const (
	trustedAS         = types.MiningDistributionMode_MINING_DISTRIBUTION_MODE_TRUSTED_AS_DISTRIBUTION
	protocolSelection = types.MiningDistributionMode_MINING_DISTRIBUTION_MODE_PROTOCOL_SELECTION
)

// TestModePromotionClosesThePredecessorAtExactlyTheSuccessorStart is the one
// permitted mutation of effective history.
//
// version, mode and valid_from_epoch are immutable once effective. The predecessor
// gains a valid_until_epoch_exclusive equal to the successor's start and nothing
// else changes, which is what makes the intervals contiguous and gap-free rather
// than merely ordered.
func TestModePromotionClosesThePredecessorAtExactlyTheSuccessorStart(t *testing.T) {
	k, ctx, rewards := initialized(t)
	require.NoError(t, k.ScheduledDistributionMode.Set(ctx, 2,
		types.ScheduledMiningDistributionMode{EffectiveEpoch: 2, Mode: protocolSelection}))

	rewards.finalize(1)
	require.NoError(t, k.EndBlock(ctx))

	predecessor, err := k.DistributionModeVersions.Get(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(1), predecessor.Version, "the version number is immutable")
	require.Equal(t, trustedAS, predecessor.Mode, "the mode is immutable")
	require.Equal(t, uint64(1), predecessor.ValidFromEpoch, "the start is immutable")
	require.Equal(t, uint64(2), predecessor.ValidUntilEpochExclusive,
		"closed at exactly the successor's start, leaving no gap and no overlap")

	successor, err := k.DistributionModeVersions.Get(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, uint64(2), successor.Version)
	require.Equal(t, protocolSelection, successor.Mode)
	require.Equal(t, uint64(2), successor.ValidFromEpoch)
	require.Zero(t, successor.ValidUntilEpochExclusive, "exactly one interval stays open")

	epochKey, found, err := keeper.LookupVersionEpochKey(ctx, k.DistributionModeVersionIndex, 2)
	require.NoError(t, err)
	require.True(t, found, "promotion records the derived index entry with the row")
	require.Equal(t, uint64(2), epochKey)

	pending, err := k.ScheduledDistributionMode.Has(ctx, 2)
	require.NoError(t, err)
	require.False(t, pending, "the schedule is consumed, so a later boundary cannot reapply it")
}

// TestPromotedModeGovernsTargetsFromItsOwnBindingBoundary checks that promotion
// changes what the chain actually resolves, not merely what it stores.
//
// A mode effective at epoch 2 binds targets from epoch 4 onward, because target N
// binds what was effective at N-2. Target 3 still binds the predecessor, and the
// predecessor is closed at 2, so this also proves the interval check admits an
// epoch inside a closed span rather than rejecting closed rows outright.
func TestPromotedModeGovernsTargetsFromItsOwnBindingBoundary(t *testing.T) {
	k, ctx, rewards := initialized(t)
	require.NoError(t, k.ScheduledDistributionMode.Set(ctx, 2,
		types.ScheduledMiningDistributionMode{EffectiveEpoch: 2, Mode: protocolSelection}))
	rewards.finalize(1)
	require.NoError(t, k.EndBlock(ctx))

	governing, err := k.DistributionModeForTarget(ctx, 3)
	require.NoError(t, err)
	require.Equal(t, uint64(1), governing.Version,
		"target 3 binds epoch 1, which the closed predecessor still covers")

	governing, err = k.DistributionModeForTarget(ctx, 4)
	require.NoError(t, err)
	require.Equal(t, uint64(2), governing.Version, "target 4 binds epoch 2, the successor's start")

	// Bootstrap targets bind the anchor rather than underflowing into the newest row.
	for _, target := range []uint64{1, 2} {
		governing, err := k.DistributionModeForTarget(ctx, target)
		require.NoError(t, err)
		require.Equal(t, uint64(1), governing.Version)
	}
}

// TestAScheduleForALaterBoundaryIsNotYetDue is the case that would halt a chain if
// the schedule were read as "the pending change must be for this boundary".
//
// An update is accepted well before it takes effect, so the schedule is
// legitimately occupied across boundaries it is not due at. Those boundaries must
// pass without applying it and without failing, and the change must still apply
// when its own boundary arrives.
func TestAScheduleForALaterBoundaryIsNotYetDue(t *testing.T) {
	k, ctx, rewards := initialized(t)
	require.NoError(t, k.ScheduledDistributionMode.Set(ctx, 3,
		types.ScheduledMiningDistributionMode{EffectiveEpoch: 3, Mode: protocolSelection}))

	rewards.finalize(1)
	require.NoError(t, k.EndBlock(ctx), "the epoch-1 boundary is not this change's boundary")

	has, err := k.DistributionModeVersions.Has(ctx, 2)
	require.NoError(t, err)
	require.False(t, has, "nothing is promoted early")
	still, err := k.ScheduledDistributionMode.Has(ctx, 3)
	require.NoError(t, err)
	require.True(t, still, "and the pending change survives the boundary it was not due at")

	rewards.finalize(2)
	require.NoError(t, k.EndBlock(ctx))

	successor, err := k.DistributionModeVersions.Get(ctx, 3)
	require.NoError(t, err)
	require.Equal(t, uint64(2), successor.Version)
	require.Equal(t, uint64(3), successor.ValidFromEpoch)
	predecessor, err := k.DistributionModeVersions.Get(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(3), predecessor.ValidUntilEpochExclusive)
}

// TestAPastDueScheduleIsCorruptionRatherThanASilentSkip is the other side of that
// classification.
//
// A pending change keyed BEFORE the boundary now opening was due at a boundary that
// has already passed and was never consumed, which means the chain has been running
// under a configuration it had already accepted a replacement for. Skipping it
// silently would make that permanent.
func TestAPastDueScheduleIsCorruptionRatherThanASilentSkip(t *testing.T) {
	k, ctx, rewards := initialized(t)
	rewards.finalize(1)
	require.NoError(t, k.EndBlock(ctx))

	// Now due at epoch 3, but keyed at 2 — the boundary that just passed.
	require.NoError(t, k.ScheduledDistributionMode.Set(ctx, 2,
		types.ScheduledMiningDistributionMode{EffectiveEpoch: 2, Mode: protocolSelection}))
	rewards.finalize(2)

	err := k.EndBlock(ctx)
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Contains(t, err.Error(), "already passed")
}

// TestAScheduleHoldingTwoPendingChangesIsRefused pins the absence of a queue.
//
// The protocol schedules at most one pending change per family. Promoting one of
// two would silently choose which change takes effect, and the loser would sit in
// state indistinguishable from a change that had merely not arrived yet.
func TestAScheduleHoldingTwoPendingChangesIsRefused(t *testing.T) {
	k, ctx, rewards := initialized(t)
	for _, epoch := range []uint64{2, 5} {
		require.NoError(t, k.ScheduledDistributionMode.Set(ctx, epoch,
			types.ScheduledMiningDistributionMode{EffectiveEpoch: epoch, Mode: protocolSelection}))
	}
	rewards.finalize(1)

	err := k.EndBlock(ctx)
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Contains(t, err.Error(), "at most one may be pending")

	has, err := k.DistributionModeVersions.Has(ctx, 2)
	require.NoError(t, err)
	require.False(t, has, "no change is applied while the schedule is ambiguous")
}

// TestModePromotionRefusesAnAlreadyClosedPredecessor protects the immutability of
// a boundary that has already been set.
//
// A nonzero valid_until_epoch_exclusive is never rewritten. Extending the newest
// row past its own end would reopen an interval the chain has already stopped
// resolving against, so promotion refuses rather than repairing.
func TestModePromotionRefusesAnAlreadyClosedPredecessor(t *testing.T) {
	k, ctx, rewards := initialized(t)
	closed, err := k.DistributionModeVersions.Get(ctx, 1)
	require.NoError(t, err)
	closed.ValidUntilEpochExclusive = 9
	require.NoError(t, k.DistributionModeVersions.Set(ctx, 1, closed))

	require.NoError(t, k.ScheduledDistributionMode.Set(ctx, 2,
		types.ScheduledMiningDistributionMode{EffectiveEpoch: 2, Mode: protocolSelection}))
	rewards.finalize(1)

	err = k.EndBlock(ctx)
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Contains(t, err.Error(), "exactly one open interval")
}

// TestModePromotionCannotMoveTheHistoryBackwards covers a boundary at or before the
// open row's own start.
//
// The successor would begin before the row it is supposed to follow, which is not a
// history that can be closed into contiguous intervals at all: closing the
// predecessor at the successor's start would give it an empty or inverted span.
func TestModePromotionCannotMoveTheHistoryBackwards(t *testing.T) {
	k, ctx, rewards := initialized(t)
	// An open row starting later than the boundary about to open. The anchor at
	// epoch 1 is left open too, so the failure below is the ordering rule rather
	// than the one-open-interval rule.
	require.NoError(t, k.DistributionModeVersions.Set(ctx, 5, types.MiningDistributionModeVersion{
		Version: 2, Mode: trustedAS, ValidFromEpoch: 5,
	}))
	require.NoError(t, k.ScheduledDistributionMode.Set(ctx, 2,
		types.ScheduledMiningDistributionMode{EffectiveEpoch: 2, Mode: protocolSelection}))
	rewards.finalize(1)

	err := k.EndBlock(ctx)
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Contains(t, err.Error(), "cannot follow version 2 valid from epoch 5")

	has, err := k.DistributionModeVersions.Has(ctx, 2)
	require.NoError(t, err)
	require.False(t, has)
}

// TestModePromotionIsAllOrNothing proves the transition commits together.
//
// The derived index is written last and is write-once, so pre-occupying the
// successor's version number makes promotion fail AFTER it has already closed the
// predecessor and appended the successor. Nothing from the block may survive: the
// predecessor must be open, the successor absent and the schedule unconsumed —
// exactly the state a later block can retry from.
func TestModePromotionIsAllOrNothing(t *testing.T) {
	k, ctx, rewards := initialized(t)
	require.NoError(t, k.DistributionModeVersionIndex.Set(ctx, 2, 99))
	require.NoError(t, k.ScheduledDistributionMode.Set(ctx, 2,
		types.ScheduledMiningDistributionMode{EffectiveEpoch: 2, Mode: protocolSelection}))
	rewards.finalize(1)

	err := k.EndBlock(ctx)
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Contains(t, err.Error(), "already indexed")

	predecessor, err := k.DistributionModeVersions.Get(ctx, 1)
	require.NoError(t, err)
	require.Zero(t, predecessor.ValidUntilEpochExclusive, "the predecessor closure is rolled back")

	has, err := k.DistributionModeVersions.Has(ctx, 2)
	require.NoError(t, err)
	require.False(t, has, "the successor is rolled back")

	pending, err := k.ScheduledDistributionMode.Has(ctx, 2)
	require.NoError(t, err)
	require.True(t, pending, "the schedule is unconsumed, so the boundary can be retried")

	cursor, err := k.GetLastProcessedRewardEpoch(ctx)
	require.NoError(t, err)
	require.Zero(t, cursor, "and the materialization the block also performed is discarded with it")
}

// TestSettlementParamsPromotionAppendsWithoutClosingAnything covers the shape the
// two parameter families have and the mode family does not.
//
// These records carry an effective epoch rather than an interval, so there is no
// predecessor to close: appending the successor and consuming the schedule is the
// whole transition, and the previous version's record is untouched.
func TestSettlementParamsPromotionAppendsWithoutClosingAnything(t *testing.T) {
	k, ctx, rewards := initialized(t)
	before, err := k.SettlementParamsVersions.Get(ctx, 1)
	require.NoError(t, err)

	require.NoError(t, k.ScheduledSettlementParams.Set(ctx, 2, types.ScheduledSettlementParams{
		EffectiveEpoch:           2,
		SettlementWindowEpochs:   3,
		MaxRecipientsPerChunk:    16,
		MaxChunksPerSettlement:   2,
		MinRecipientPayoutAmount: "20000",
	}))
	rewards.finalize(1)
	require.NoError(t, k.EndBlock(ctx))

	after, err := k.SettlementParamsVersions.Get(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, before, after, "an effective parameter version is never rewritten")

	successor, err := k.SettlementParamsVersions.Get(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, uint64(2), successor.Version)
	require.Equal(t, uint64(2), successor.EffectiveEpoch)
	require.Equal(t, uint64(3), successor.SettlementWindowEpochs)
	require.Equal(t, "20000", successor.MinRecipientPayoutAmount)

	epochKey, found, err := keeper.LookupVersionEpochKey(ctx, k.SettlementParamsVersionIndex, 2)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(2), epochKey)

	pending, err := k.ScheduledSettlementParams.Has(ctx, 2)
	require.NoError(t, err)
	require.False(t, pending)

	// And it governs from its own binding boundary, in the same units as the mode.
	governing, err := k.SettlementParamsForTarget(ctx, 4)
	require.NoError(t, err)
	require.Equal(t, uint64(2), governing.Version)
	governing, err = k.SettlementParamsForTarget(ctx, 3)
	require.NoError(t, err)
	require.Equal(t, uint64(1), governing.Version)
}

// TestSelectionParamsPromotionSharesTheSameBoundary keeps the third family from
// drifting.
//
// Nothing in this profile reads Selection parameters on a consequential path, but a
// family without a promotion path would leave a later tranche to discover the epoch
// boundary has no seam for it.
func TestSelectionParamsPromotionSharesTheSameBoundary(t *testing.T) {
	k, ctx, rewards := initialized(t)
	require.NoError(t, k.ScheduledSelectionParams.Set(ctx, 2, types.ScheduledSelectionParams{
		EffectiveEpoch:                      2,
		MaxSelectionRateBps:                 1_000,
		MaxSelectedParticipantsPerSelection: 32,
		MaxCandidatesPerSelection:           512,
		BeaconStartOffsetBlocks:             48,
		BeaconWindowBlocks:                  24,
		MinExternalBeaconBlocks:             12,
		MinDistinctExternalProposers:        3,
	}))
	rewards.finalize(1)
	require.NoError(t, k.EndBlock(ctx))

	successor, err := k.SelectionParamsVersions.Get(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, uint64(2), successor.Version)
	require.Equal(t, uint64(1_000), successor.MaxSelectionRateBps)

	epochKey, found, err := keeper.LookupVersionEpochKey(ctx, k.SelectionParamsVersionIndex, 2)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(2), epochKey)
}

// TestPromotionRunsOnlyAtAnEpochBoundary pins when the boundary exists at all.
//
// Blocks on which no reward epoch closed are not boundaries, and promotion must not
// run on them at all — not merely find nothing due. The distinction matters because
// an implementation that promoted against the materialization cursor on every block
// would agree on the epoch a change takes effect at while making the BLOCK it takes
// effect on depend on when the schedule happened to be written.
//
// The change below is keyed at the boundary that has just passed, which is exactly
// the key such an implementation would find due on the very next ordinary block.
// Correct code never reads the schedule on one.
func TestPromotionRunsOnlyAtAnEpochBoundary(t *testing.T) {
	k, ctx, rewards := initialized(t)
	rewards.finalize(1)
	require.NoError(t, k.EndBlock(ctx))

	require.NoError(t, k.ScheduledDistributionMode.Set(ctx, 2,
		types.ScheduledMiningDistributionMode{EffectiveEpoch: 2, Mode: protocolSelection}))
	for i := 0; i < 5; i++ {
		require.NoError(t, k.EndBlock(ctx), "an ordinary block is not an epoch boundary")
	}

	has, err := k.DistributionModeVersions.Has(ctx, 2)
	require.NoError(t, err)
	require.False(t, has, "no epoch closed in those blocks, so no boundary opened")
	pending, err := k.ScheduledDistributionMode.Has(ctx, 2)
	require.NoError(t, err)
	require.True(t, pending, "and the schedule was never even read")
}

// TestVersionNumbersAreMonotonicButNeedNotBeContiguous is the divergence from the
// reward-configuration history, and it is deliberate.
//
// Those versions are ratified contiguous, so "in range but unindexed" is decidable
// arithmetically and is treated as corruption. These are ratified unique and
// monotonically increasing only. A history that arrived with gaps is canonical and
// must keep resolving, and a promotion on top of it must not object.
func TestVersionNumbersAreMonotonicButNeedNotBeContiguous(t *testing.T) {
	k, ctx, rewards := initialized(t)

	// A history whose second row is version 5: versions 2 through 4 do not exist.
	anchor, err := k.DistributionModeVersions.Get(ctx, 1)
	require.NoError(t, err)
	anchor.ValidUntilEpochExclusive = 10
	require.NoError(t, k.DistributionModeVersions.Set(ctx, 1, anchor))
	require.NoError(t, k.DistributionModeVersions.Set(ctx, 10, types.MiningDistributionModeVersion{
		Version: 5, Mode: trustedAS, ValidFromEpoch: 10,
	}))
	require.NoError(t, k.DistributionModeVersionIndex.Set(ctx, 5, 10))

	governing, err := k.DistributionModeForTarget(ctx, 12)
	require.NoError(t, err)
	require.Equal(t, uint64(5), governing.Version, "a gapped history still resolves")

	// A promotion on top of it derives latest+1 and validates nothing about the gap.
	require.NoError(t, k.LastProcessedRewardEpoch.Set(ctx, 9))
	require.NoError(t, k.ScheduledDistributionMode.Set(ctx, 11,
		types.ScheduledMiningDistributionMode{EffectiveEpoch: 11, Mode: protocolSelection}))
	rewards.finalize(10)
	require.NoError(t, k.EndBlock(ctx))

	successor, err := k.DistributionModeVersions.Get(ctx, 11)
	require.NoError(t, err)
	require.Equal(t, uint64(6), successor.Version, "latest+1, not a contiguity repair")
}

// TestAMissingVersionIndexEntryIsOrdinaryAbsence states the consequence of that
// numbering rule for lookups.
//
// With merely monotonic versions there is no arithmetic that distinguishes "never
// assigned" from "assigned but unindexed", so a missing entry cannot be classified
// as corruption here the way it is for reward configurations. Callers cross-check
// the row the key reaches instead.
func TestAMissingVersionIndexEntryIsOrdinaryAbsence(t *testing.T) {
	k, ctx, _ := initialized(t)

	epochKey, found, err := keeper.LookupVersionEpochKey(ctx, k.DistributionModeVersionIndex, 1)
	require.NoError(t, err)
	require.True(t, found, "genesis rebuilds the index from the history it imported")
	require.Equal(t, uint64(1), epochKey)

	_, found, err = keeper.LookupVersionEpochKey(ctx, k.DistributionModeVersionIndex, 7)
	require.NoError(t, err, "absence is reported, not raised")
	require.False(t, found)

	_, _, err = keeper.LookupVersionEpochKey(ctx, k.DistributionModeVersionIndex, 0)
	require.ErrorIs(t, err, types.ErrInvalidState, "version numbers start at 1")
}

// TestVersionIndexesAreRebuiltForEveryFamily covers the derived state genesis
// carries no field for.
func TestVersionIndexesAreRebuiltForEveryFamily(t *testing.T) {
	k, ctx, _ := initialized(t)

	epochKey, found, err := keeper.LookupVersionEpochKey(ctx, k.SelectionParamsVersionIndex, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(1), epochKey)

	epochKey, found, err = keeper.LookupVersionEpochKey(ctx, k.SettlementParamsVersionIndex, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(1), epochKey)
}

// TestBootstrapTargetsDoNotUnderflowIntoTheNewestVersion pins the hazard the
// binding rule is written to avoid structurally.
//
// Epoch numbers are unsigned. An unguarded N-2 for targets 1 and 2 underflows to an
// enormous epoch whose descending seek resolves the NEWEST version — the exact
// opposite of the intended answer, and silently correct-looking on a chain that has
// only ever had one version.
func TestBootstrapTargetsDoNotUnderflowIntoTheNewestVersion(t *testing.T) {
	for _, target := range []uint64{1, 2} {
		epoch, bootstrap, err := keeper.BindingEpochForTarget(target)
		require.NoError(t, err)
		require.True(t, bootstrap, "target %d has no binding boundary inside chain history", target)
		require.Zero(t, epoch)
	}

	epoch, bootstrap, err := keeper.BindingEpochForTarget(3)
	require.NoError(t, err)
	require.False(t, bootstrap)
	require.Equal(t, uint64(1), epoch)

	_, _, err = keeper.BindingEpochForTarget(0)
	require.ErrorIs(t, err, types.ErrInvalidState)
}

// TestMissingGenesisAnchorIsDetectedEvenWhenALaterVersionResolves is why the
// resolution path checks the anchor before it seeks.
//
// A seek alone cannot notice that the origin has gone: any later row answers
// "greatest key at or before" perfectly well, so a history missing its anchor would
// keep resolving against remains and would look healthy.
func TestMissingGenesisAnchorIsDetectedEvenWhenALaterVersionResolves(t *testing.T) {
	k, ctx, _ := initialized(t)
	require.NoError(t, k.DistributionModeVersions.Set(ctx, 10, types.MiningDistributionModeVersion{
		Version: 2, Mode: trustedAS, ValidFromEpoch: 10,
	}))
	require.NoError(t, k.DistributionModeVersions.Remove(ctx, 1))

	_, err := k.DistributionModeForTarget(ctx, 12)
	require.ErrorIs(t, err, types.ErrParamsNotFound)

	_, err = k.GenesisDistributionMode(ctx)
	require.ErrorIs(t, err, types.ErrParamsNotFound)
}

// TestAResolvedModeMustCoverTheEpochItWasResolvedFor is the check the interval
// shape exists to make possible.
//
// Answering with a closed interval for an epoch past its end would return a mode
// that had already stopped applying, and the caller has no other way to tell.
func TestAResolvedModeMustCoverTheEpochItWasResolvedFor(t *testing.T) {
	k, ctx, _ := initialized(t)
	anchor, err := k.DistributionModeVersions.Get(ctx, 1)
	require.NoError(t, err)
	anchor.ValidUntilEpochExclusive = 4
	require.NoError(t, k.DistributionModeVersions.Set(ctx, 1, anchor))

	governing, err := k.DistributionModeForTarget(ctx, 5)
	require.NoError(t, err)
	require.Equal(t, uint64(1), governing.Version, "target 5 binds epoch 3, inside [1, 4)")

	_, err = k.DistributionModeForTarget(ctx, 6)
	require.ErrorIs(t, err, types.ErrInvalidState, "target 6 binds epoch 4, which nothing covers")
	require.Contains(t, err.Error(), "does not cover epoch 4")
}

// TestAStoredRowMustAgreeWithTheKeyItLivesAt closes the last way a history can be
// internally consistent and still wrong.
func TestAStoredRowMustAgreeWithTheKeyItLivesAt(t *testing.T) {
	k, ctx, _ := initialized(t)
	require.NoError(t, k.DistributionModeVersions.Set(ctx, 1, types.MiningDistributionModeVersion{
		Version: 1, Mode: trustedAS, ValidFromEpoch: 3,
	}))

	_, err := k.GenesisDistributionMode(ctx)
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Contains(t, err.Error(), "declares valid_from_epoch 3")
}

// TestPromotionRefusesAMalformedSelectionParamsPredecessor is the rule that new
// canonical state is never built on an invalid predecessor.
//
// Promotion derives the successor's version number from the predecessor's and
// orders itself against the predecessor's effective epoch. A malformed
// predecessor would therefore be EXTENDED by a successful promotion — the history
// would gain a well-formed row descending from a broken one, which is harder to
// detect afterwards than the broken row alone.
//
// SelectionParams is not consequential to payout authorization in this profile and
// has no update transaction, so this is not a live money path. It is canonical
// consensus state all the same, and the promotion primitive is production-shaped.
func TestPromotionRefusesAMalformedSelectionParamsPredecessor(t *testing.T) {
	k, ctx, rewards := initialized(t)

	// Corrupt the canonical row in a way the key relationship cannot see: the row
	// still lives at, and declares, epoch 1 — only its contents are invalid.
	anchor, err := k.SelectionParamsVersions.Get(ctx, 1)
	require.NoError(t, err)
	anchor.MaxSelectedParticipantsPerSelection = 0
	require.NoError(t, k.SelectionParamsVersions.Set(ctx, 1, anchor))

	require.NoError(t, k.ScheduledSelectionParams.Set(ctx, 2, types.ScheduledSelectionParams{
		EffectiveEpoch:                      2,
		MaxSelectionRateBps:                 1_000,
		MaxSelectedParticipantsPerSelection: 32,
		MaxCandidatesPerSelection:           512,
		BeaconStartOffsetBlocks:             48,
		BeaconWindowBlocks:                  24,
		MinExternalBeaconBlocks:             12,
		MinDistinctExternalProposers:        3,
	}))
	rewards.finalize(1)

	err = k.EndBlock(ctx)
	require.ErrorIs(t, err, types.ErrInvalidState)

	// No successor, no index entry, and the schedule is unconsumed.
	has, err := k.SelectionParamsVersions.Has(ctx, 2)
	require.NoError(t, err)
	require.False(t, has, "no successor is appended on top of a broken predecessor")
	_, found, err := keeper.LookupVersionEpochKey(ctx, k.SelectionParamsVersionIndex, 2)
	require.NoError(t, err)
	require.False(t, found, "no version index entry is written")
	pending, err := k.ScheduledSelectionParams.Has(ctx, 2)
	require.NoError(t, err)
	require.True(t, pending, "the schedule is not consumed")

	// And every earlier part of the same EndBlock is unapplied.
	cursor, err := k.GetLastProcessedRewardEpoch(ctx)
	require.NoError(t, err)
	require.Zero(t, cursor, "the materialization in this block is discarded too")
	clock, err := k.GetSettlementClock(ctx)
	require.NoError(t, err)
	require.Zero(t, clock, "the clock tick is discarded with the rest of the transition")
}

// TestPromotionRefusesAMalformedPredecessorInEveryFamily is the symmetry evidence.
//
// The mode and settlement-parameter families already validated their predecessor
// in full — mode through validateModeRecord, settlement parameters through
// validateSettlementParamsRecord — and SelectionParams was the one that did not.
// Rather than assert that from reading the code, all three are exercised the same
// way here so the property cannot regress in any of them.
func TestPromotionRefusesAMalformedPredecessorInEveryFamily(t *testing.T) {
	t.Run("distribution mode", func(t *testing.T) {
		k, ctx, rewards := initialized(t)
		row, err := k.DistributionModeVersions.Get(ctx, 1)
		require.NoError(t, err)
		row.Mode = types.MiningDistributionMode_MINING_DISTRIBUTION_MODE_UNSPECIFIED
		require.NoError(t, k.DistributionModeVersions.Set(ctx, 1, row))
		require.NoError(t, k.ScheduledDistributionMode.Set(ctx, 2,
			types.ScheduledMiningDistributionMode{EffectiveEpoch: 2, Mode: protocolSelection}))
		rewards.finalize(1)

		require.ErrorIs(t, k.EndBlock(ctx), types.ErrInvalidState)
		has, err := k.DistributionModeVersions.Has(ctx, 2)
		require.NoError(t, err)
		require.False(t, has)
	})

	t.Run("settlement params", func(t *testing.T) {
		k, ctx, rewards := initialized(t)
		row, err := k.SettlementParamsVersions.Get(ctx, 1)
		require.NoError(t, err)
		row.MaxChunksPerSettlement = 0
		require.NoError(t, k.SettlementParamsVersions.Set(ctx, 1, row))
		require.NoError(t, k.ScheduledSettlementParams.Set(ctx, 2, types.ScheduledSettlementParams{
			EffectiveEpoch:           2,
			SettlementWindowEpochs:   3,
			MaxRecipientsPerChunk:    16,
			MaxChunksPerSettlement:   2,
			MinRecipientPayoutAmount: "20000",
		}))
		rewards.finalize(1)

		require.ErrorIs(t, k.EndBlock(ctx), types.ErrInvalidState)
		has, err := k.SettlementParamsVersions.Has(ctx, 2)
		require.NoError(t, err)
		require.False(t, has)
	})
}
