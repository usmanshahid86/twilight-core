package keeper_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	"github.com/twilight-project/twilight-core/x/mining/keeper"
	"github.com/twilight-project/twilight-core/x/mining/types"
	rewardstypes "github.com/twilight-project/twilight-core/x/rewards/types"
)

// Participant chunk admission.
//
// # What these tests can and cannot establish
//
// The rewards keeper here is a double that accumulates released amounts without
// moving balances, so nothing below proves that money actually arrives. That
// property needs a real bank and is established in the app integration test.
//
// What these DO establish is the admission set: every rule that decides whether a
// chunk may release value at all, and the atomicity of the transition once it may.
//
// # The two ceilings
//
// x/mining proves the settlement's derived participant ceiling. x/rewards
// independently proves its own entitlement ceiling against the escrow it owns, and
// that is proven against the real implementation by
// TestPayEntitlementRefusesOverRelease in x/rewards. The double deliberately does
// not re-implement it, so the tests below fail if x/mining's ceiling is weakened.

const (
	// Well above the 10,000 floor, and large enough that a chunk can sit under the
	// ceiling several times over.
	fixtureEntitlement = "1000000"
	// The AS credential, and three participants ordered by their leading byte.
	settlementSigner = 0x11
	participantA     = 0x21
	participantB     = 0x22
	participantC     = 0x23
)

// settlementFixture boots a keeper holding one materialized settlement for
// (slot 1, epoch 1), created the way consensus creates it — by closing a reward
// epoch — rather than written directly.
func settlementFixture(t *testing.T) (keeper.Keeper, sdk.Context, *rewardsKeeperMock) {
	t.Helper()
	core := &coreSlotKeeperMock{
		active:   []coreslottypes.CoreSlot{settlementSlot(1, account(settlementSigner))},
		policies: map[uint64]coreslottypes.SelectionPolicyVersion{1: policy(1, 2_500, 10)},
	}
	k, ctx, rewards := setupKeeperWithRewards(t, core, newRewardsMock())
	require.NoError(t, k.InitGenesis(ctx, *types.DefaultGenesis()))
	rewards.finalize(1, entitlement(1, 1, fixtureEntitlement))
	require.NoError(t, k.EndBlock(ctx))
	return k, ctx, rewards
}

// chunk builds a well-formed submission from the fixture's signer.
func chunk(index uint64, lines ...*types.SettlementChunkPayout) *types.MsgSubmitSettlementChunk {
	return &types.MsgSubmitSettlementChunk{
		SettlementAddress: account(settlementSigner),
		SlotId:            1,
		Epoch:             1,
		ChunkIndex:        index,
		Payouts:           lines,
	}
}

func line(marker byte, amount string) *types.SettlementChunkPayout {
	return &types.SettlementChunkPayout{Recipient: account(marker), Amount: amount}
}

// releasedAmount reads the AUTHORITATIVE released value, which lives on the
// entitlement in x/rewards and never on the settlement. It is read from the
// rewards double directly, so a settlement-side copy could not satisfy it even if
// one existed.
func releasedAmount(t *testing.T, rewards *rewardsKeeperMock, slotID, epoch uint64) string {
	t.Helper()
	owed, found, err := rewards.GetSlotEntitlement(context.Background(), slotID, epoch)
	require.NoError(t, err)
	require.True(t, found)
	return owed.ReleasedAmount
}

// moduleAccountAddress is the address of the module account the fixture's
// economic-address rule knows about. A module account is never a payable
// destination.
func moduleAccountAddress() string {
	return authtypes.NewModuleAddress(fixtureModuleAccount).String()
}

func nextChunkIndex(t *testing.T, k keeper.Keeper, ctx sdk.Context) uint64 {
	t.Helper()
	settlement, found, err := k.GetSettlement(ctx, 1, 1)
	require.NoError(t, err)
	require.True(t, found)
	return settlement.NextChunkIndex
}

// TestAChunkReleasesToEveryRecipientAndAdvancesTheCursor is the happy path, and
// the two things it asserts together are the point.
//
// The cursor advance and the released-amount increase are one transition. A design
// in which they could disagree would let a settlement be replayed at an index it
// had already paid, or be permanently stuck at an index it had not.
func TestAChunkReleasesToEveryRecipientAndAdvancesTheCursor(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)

	next, err := k.SubmitSettlementChunk(ctx, chunk(0,
		line(participantA, "50000"),
		line(participantB, "70000"),
	))
	require.NoError(t, err)
	require.Equal(t, uint64(1), next)
	require.Equal(t, uint64(1), nextChunkIndex(t, k, ctx))
	require.Equal(t, "120000", releasedAmount(t, rewards, 1, 1))
}

// TestChunksProgressInOrderAndAccumulate proves the cursor is a position, not a
// counter, and that each chunk is measured against what the previous ones released.
func TestChunksProgressInOrderAndAccumulate(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)

	for index, amount := range []string{"100000", "200000", "300000"} {
		next, err := k.SubmitSettlementChunk(ctx, chunk(uint64(index), line(participantA, amount)))
		require.NoErrorf(t, err, "chunk %d", index)
		require.Equal(t, uint64(index+1), next)
	}
	require.Equal(t, "600000", releasedAmount(t, rewards, 1, 1))

	// The same recipient across different chunks is fine; only within one chunk is
	// a repeat refused.
	require.Equal(t, uint64(3), nextChunkIndex(t, k, ctx))
}

// TestAnAcceptedChunkCannotBeReplayed is the property a caller that lost a
// response depends on.
//
// Resubmitting an accepted index must be rejected rather than treated as the next
// chunk, because treating it as the next chunk would pay the same recipients
// twice against one authorization.
func TestAnAcceptedChunkCannotBeReplayed(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)
	accepted := chunk(0, line(participantA, "50000"))

	_, err := k.SubmitSettlementChunk(ctx, accepted)
	require.NoError(t, err)
	releasedOnce := releasedAmount(t, rewards, 1, 1)
	callsOnce := rewards.payCalls

	_, err = k.SubmitSettlementChunk(ctx, accepted)
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Contains(t, err.Error(), "expects chunk 1, not chunk 0")

	require.Equal(t, releasedOnce, releasedAmount(t, rewards, 1, 1), "a replay releases nothing")
	require.Equal(t, callsOnce, rewards.payCalls, "and never reaches the release boundary")
	require.Equal(t, uint64(1), nextChunkIndex(t, k, ctx))

	// The unambiguous recovery rule: the cursor says which chunk to send next.
	_, err = k.SubmitSettlementChunk(ctx, chunk(1, line(participantA, "50000")))
	require.NoError(t, err)
}

// TestChunksMayNotSkipAhead closes the other direction. A caller cannot reserve a
// later index and leave a gap that some other submission fills.
func TestChunksMayNotSkipAhead(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)

	_, err := k.SubmitSettlementChunk(ctx, chunk(1, line(participantA, "50000")))
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Contains(t, err.Error(), "expects chunk 0, not chunk 1")
	require.Zero(t, rewards.payCalls)
}

// TestOnlyTheSettlementAddressMaySubmitChunks pins the authorization.
//
// The Slot's payout address, its operator address and any other account are all
// refused: the settlement credential is a distinct, separately rotatable identity
// precisely so that compromising it does not require compromising the operator.
func TestOnlyTheSettlementAddressMaySubmitChunks(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)

	unauthorized := chunk(0, line(participantA, "50000"))
	unauthorized.SettlementAddress = account(0x99)

	_, err := k.SubmitSettlementChunk(ctx, unauthorized)
	require.ErrorIs(t, err, types.ErrInvalidAddress)
	require.Contains(t, err.Error(), "only the settlement address of slot 1")
	require.Zero(t, rewards.payCalls)
	require.Zero(t, nextChunkIndex(t, k, ctx))
}

// TestChunkAdmissionComparesDecodedSignerBytes proves the authorization is on the
// account and not on the spelling.
//
// A bech32 address is case-insensitive in its data part, so an uppercase encoding
// names the same account. Refusing it would be refusing the right signer for a
// reason that has nothing to do with authority.
func TestChunkAdmissionComparesDecodedSignerBytes(t *testing.T) {
	k, ctx, _ := settlementFixture(t)

	msg := chunk(0, line(participantA, "50000"))
	msg.SettlementAddress = strings.ToUpper(account(settlementSigner))

	_, err := k.SubmitSettlementChunk(ctx, msg)
	require.NoError(t, err)
}

// TestRecipientsMustBeStrictlyAscendingByAddressBytes gives one chunk exactly one
// canonical form.
//
// Ordering by decoded bytes yields uniqueness for free and does more: two
// submissions naming the same recipients in different orders are the same chunk,
// so a duplicate cannot hide behind a reordering.
func TestRecipientsMustBeStrictlyAscendingByAddressBytes(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)

	for name, lines := range map[string][]*types.SettlementChunkPayout{
		"descending": {line(participantB, "50000"), line(participantA, "50000")},
		"duplicate":  {line(participantA, "50000"), line(participantA, "50000")},
		"unsorted middle": {
			line(participantA, "50000"), line(participantC, "50000"), line(participantB, "50000"),
		},
	} {
		t.Run(name, func(t *testing.T) {
			before := rewards.payCalls
			_, err := k.SubmitSettlementChunk(ctx, chunk(0, lines...))
			require.ErrorIs(t, err, types.ErrInvalidState)
			require.Contains(t, err.Error(), "strictly ascending")
			require.Equal(t, before, rewards.payCalls, "nothing is released")
			require.Zero(t, nextChunkIndex(t, k, ctx))
		})
	}

	// Ascending is accepted.
	_, err := k.SubmitSettlementChunk(ctx, chunk(0,
		line(participantA, "50000"), line(participantB, "50000"), line(participantC, "50000")))
	require.NoError(t, err)
}

// TestEveryRecipientAmountIsAtOrAboveTheFloor closes dust fan-out.
//
// On a feeless chain a below-floor line is a cheap way to create permanent
// accounts in bulk. A zero line is the same hazard at its limit, and is never a
// legitimate participant payout: the protocol's zero-value allowances cover a zero
// mint, a zero treasury share and a zero operator remainder, none of which is a
// participant line.
func TestEveryRecipientAmountIsAtOrAboveTheFloor(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)

	for name, amount := range map[string]string{
		"zero":           "0",
		"one below":      "9999",
		"far below":      "1",
		"below in a set": "500",
	} {
		t.Run(name, func(t *testing.T) {
			before := rewards.payCalls
			_, err := k.SubmitSettlementChunk(ctx, chunk(0,
				line(participantA, "50000"), line(participantB, amount)))
			require.ErrorIs(t, err, types.ErrInvalidState)
			require.Contains(t, err.Error(), "below the minimum participant payout")
			require.Equal(t, before, rewards.payCalls,
				"the whole chunk is refused before the first transfer")
		})
	}

	// Exactly at the floor is admitted.
	_, err := k.SubmitSettlementChunk(ctx, chunk(0,
		line(participantA, types.DefaultMinRecipientPayoutAmount)))
	require.NoError(t, err)
}

// TestAmountsMustBeCanonicalBaseTenIntegers guards the parser hazard.
//
// The SDK's own integer parser infers a radix, so "010" would be 8 and "0x10"
// would be 16. An externally supplied amount must go through the canonical
// digit-only scan or a caller could name one number and be paid another.
func TestAmountsMustBeCanonicalBaseTenIntegers(t *testing.T) {
	k, ctx, _ := settlementFixture(t)

	for _, amount := range []string{"0x10", "010", "+50000", "-50000", "50_000", " 50000", "50000 ", ""} {
		_, err := k.SubmitSettlementChunk(ctx, chunk(0, line(participantA, amount)))
		require.ErrorIsf(t, err, types.ErrInvalidState, "amount %q must be refused", amount)
	}
}

// TestRecipientsMustBeCanonicalEconomicDestinations applies the one app-level rule.
//
// The same rule that governs an entitlement's payout snapshot and a Slot's
// settlement address governs a participant line, and it is injected rather than
// re-derived so the three cannot come to disagree about what a payable address is.
func TestRecipientsMustBeCanonicalEconomicDestinations(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)

	for name, recipient := range map[string]string{
		"not bech32":      "not-an-address",
		"empty":           "",
		"wrong prefix":    "cosmos1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq",
		"module account":  moduleAccountAddress(),
		"truncated bytes": "twilight1qqqqqq",
	} {
		t.Run(name, func(t *testing.T) {
			before := rewards.payCalls
			_, err := k.SubmitSettlementChunk(ctx, chunk(0,
				line(participantA, "50000"),
				&types.SettlementChunkPayout{Recipient: recipient, Amount: "50000"}))
			require.Error(t, err)
			require.Equal(t, before, rewards.payCalls)
		})
	}
}

// TestAChunkMayNotExceedTheParticipantCeiling is the monetary bound this module
// owns.
//
// It is measured from the entitlement-side released amount, not from a
// settlement-side copy. A second copy inside Settlement would be a duplicate
// monetary authority that could diverge from the value the release boundary
// actually enforces, and this check would then be proving the wrong number.
func TestAChunkMayNotExceedTheParticipantCeiling(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)

	// One unit over the entitlement in a single chunk.
	_, err := k.SubmitSettlementChunk(ctx, chunk(0, line(participantA, "1000001")))
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Contains(t, err.Error(), "above the participant distribution ceiling")
	require.Zero(t, rewards.payCalls)

	// Exactly the entitlement is admitted.
	_, err = k.SubmitSettlementChunk(ctx, chunk(0, line(participantA, fixtureEntitlement)))
	require.NoError(t, err)
	require.Equal(t, fixtureEntitlement, releasedAmount(t, rewards, 1, 1))

	// And the ceiling counts what earlier chunks already released: nothing is left.
	callsBefore := rewards.payCalls
	_, err = k.SubmitSettlementChunk(ctx, chunk(1, line(participantB, "10000")))
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Contains(t, err.Error(), "above the participant distribution ceiling")
	require.Equal(t, callsBefore, rewards.payCalls)
}

// TestAChunkMayNotExceedTheConfiguredRecipientCount bounds the work one chunk can
// force, which is what keeps a chunk O(recipients) with a small constant.
func TestAChunkMayNotExceedTheConfiguredRecipientCount(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)

	lines := make([]*types.SettlementChunkPayout, 0, 33)
	for i := 0; i < 33; i++ {
		lines = append(lines, line(byte(0x30+i), "10000"))
	}
	_, err := k.SubmitSettlementChunk(ctx, chunk(0, lines...))
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Contains(t, err.Error(), "above the immutable maximum of 32")
	require.Zero(t, rewards.payCalls)

	// Exactly the maximum is admitted.
	_, err = k.SubmitSettlementChunk(ctx, chunk(0, lines[:32]...))
	require.NoError(t, err)
}

// TestASettlementMayNotExceedItsConfiguredChunkCount is the other half of the
// bound: a settlement is at most four chunks, so its total work is bounded too.
func TestASettlementMayNotExceedItsConfiguredChunkCount(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)

	for index := uint64(0); index < types.DefaultMaxChunksPerSettlement; index++ {
		_, err := k.SubmitSettlementChunk(ctx, chunk(index, line(participantA, "10000")))
		require.NoErrorf(t, err, "chunk %d", index)
	}
	callsBefore := rewards.payCalls

	_, err := k.SubmitSettlementChunk(ctx, chunk(
		types.DefaultMaxChunksPerSettlement, line(participantA, "10000")))
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Contains(t, err.Error(), "has used them all")
	require.Equal(t, callsBefore, rewards.payCalls)

	// The settlement stays OPEN — chunks being closed is a derived condition, not a
	// stored lifecycle state, and the operator remainder is still owed.
	settlement, found, err := k.GetSettlement(ctx, 1, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.False(t, settlement.Finalized)
}

// TestChunksAreRefusedAtAndAfterTheDeadline pins the boundary exactly.
//
// The deadline is measured in settlement-clock ticks, and one tick before it the
// chunk is still admissible. Reaching it closes chunks permanently; it never
// changes who receives funds.
func TestChunksAreRefusedAtAndAfterTheDeadline(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)

	settlement, _, err := k.GetSettlement(ctx, 1, 1)
	require.NoError(t, err)
	deadline, err := k.SettlementDeadlineClock(ctx, settlement)
	require.NoError(t, err)

	// One tick before the deadline: still open.
	require.NoError(t, k.SettlementClock.Set(ctx, deadline-1))
	_, err = k.SubmitSettlementChunk(ctx, chunk(0, line(participantA, "50000")))
	require.NoError(t, err)

	// Exactly at the deadline: closed.
	require.NoError(t, k.SettlementClock.Set(ctx, deadline))
	callsBefore := rewards.payCalls
	_, err = k.SubmitSettlementChunk(ctx, chunk(1, line(participantA, "50000")))
	require.ErrorIs(t, err, types.ErrUnsupportedFeature)
	require.Contains(t, err.Error(), "participant window")
	require.Equal(t, callsBefore, rewards.payCalls)

	// And past it.
	require.NoError(t, k.SettlementClock.Set(ctx, deadline+1_000))
	_, err = k.SubmitSettlementChunk(ctx, chunk(1, line(participantA, "50000")))
	require.ErrorIs(t, err, types.ErrUnsupportedFeature)
	require.Equal(t, uint64(1), nextChunkIndex(t, k, ctx))
}

// TestAPausedChainRefusesEveryChunk covers the global gate.
//
// A pause blocks every action that moves value. Because the clock does not advance
// while paused, it freezes the deadline rather than consuming it — so a chunk
// refused during a pause is still admissible on resume, against the same
// settlement state.
func TestAPausedChainRefusesEveryChunk(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)
	rewards.releaseEnabled = false

	_, err := k.SubmitSettlementChunk(ctx, chunk(0, line(participantA, "50000")))
	require.ErrorIs(t, err, types.ErrUnsupportedFeature)
	require.Contains(t, err.Error(), "paused")
	require.Zero(t, rewards.payCalls)
	require.Zero(t, nextChunkIndex(t, k, ctx))

	rewards.releaseEnabled = true
	_, err = k.SubmitSettlementChunk(ctx, chunk(0, line(participantA, "50000")))
	require.NoError(t, err, "the pause changed no settlement field")
}

// TestTheChunkTransitionIsAtomic is the rollback proof.
//
// The release boundary refuses after x/mining has admitted the chunk. Nothing from
// the transition may survive — in particular the cursor must not advance, or the
// caller would be told to send chunk n+1 for money that was never released.
func TestTheChunkTransitionIsAtomic(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)
	rewards.payErr = errors.New("the bank refused the second transfer")

	_, err := k.SubmitSettlementChunk(ctx, chunk(0,
		line(participantA, "50000"), line(participantB, "50000")))
	require.Error(t, err)
	require.Equal(t, 1, rewards.payCalls, "the release was attempted")

	require.Zero(t, nextChunkIndex(t, k, ctx), "and the cursor did not advance")
	require.Equal(t, "0", releasedAmount(t, rewards, 1, 1))

	// The settlement is unchanged, so the same chunk can be retried once the cause
	// is gone.
	rewards.payErr = nil
	next, err := k.SubmitSettlementChunk(ctx, chunk(0,
		line(participantA, "50000"), line(participantB, "50000")))
	require.NoError(t, err)
	require.Equal(t, uint64(1), next)
}

// TestChunksRequireAnExistingOpenSettlement covers the two lifecycle refusals.
//
// Absence is ordinary — most (slot, epoch) pairs never produce a settlement — while
// a finalized settlement is terminal and admits nothing further, because its
// remainder has already been paid to the operator.
func TestChunksRequireAnExistingOpenSettlement(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)

	missing := chunk(0, line(participantA, "50000"))
	missing.Epoch = 9
	_, err := k.SubmitSettlementChunk(ctx, missing)
	require.ErrorIs(t, err, types.ErrSettlementNotFound)

	settlement, _, err := k.GetSettlement(ctx, 1, 1)
	require.NoError(t, err)
	settlement.Finalized = true
	settlement.FinalizedHeight = 10
	settlement.FinalizationReason =
		types.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_AUTHORIZED_EARLY
	require.NoError(t, k.Settlements.Set(ctx, collections.Join(uint64(1), uint64(1)), settlement))

	_, err = k.SubmitSettlementChunk(ctx, chunk(0, line(participantA, "50000")))
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Contains(t, err.Error(), "finalized and admits no further chunks")
	require.Zero(t, rewards.payCalls)
}

// TestOperatorOnlySettlementsAdmitNoChunks covers an arm this profile cannot reach.
//
// Its entire entitlement belongs to the operator remainder, so its participant
// ceiling is zero and no participant line can be admitted. Asserted from directly
// constructed state, without any Selection applicability logic to reach the mode.
func TestOperatorOnlySettlementsAdmitNoChunks(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)
	setSettlementMode(t, k, ctx, types.SettlementMode_SETTLEMENT_MODE_OPERATOR_ONLY)

	_, err := k.SubmitSettlementChunk(ctx, chunk(0, line(participantA, "50000")))
	require.ErrorIs(t, err, types.ErrUnsupportedFeature)
	require.Contains(t, err.Error(), "operator-only")
	require.Zero(t, rewards.payCalls)
}

// TestSelectedParticipantSettlementsAdmitChunks covers the other unreachable arm.
//
// Its handler behavior is implemented now so the Selection tranche adds a producer
// rather than a branch. It deliberately performs no Selection applicability check:
// no commitment, candidate list, beacon or result exists here to check against.
func TestSelectedParticipantSettlementsAdmitChunks(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)
	setSettlementMode(t, k, ctx, types.SettlementMode_SETTLEMENT_MODE_SELECTED_PARTICIPANTS)

	next, err := k.SubmitSettlementChunk(ctx, chunk(0, line(participantA, "50000")))
	require.NoError(t, err)
	require.Equal(t, uint64(1), next)
	require.Equal(t, "50000", releasedAmount(t, rewards, 1, 1))
}

// TestASettlementWithNoEntitlementIsCorruptionNotAbsence keeps a missing obligation
// from reading as a zero one.
//
// A settlement exists only because a nonzero entitlement existed when its epoch
// closed, and the two are created in the same transition. Treating the absence as
// "nothing released yet" would compute a ceiling from a record that is not there.
func TestASettlementWithNoEntitlementIsCorruptionNotAbsence(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)
	rewards.entitlements[1] = nil

	_, err := k.SubmitSettlementChunk(ctx, chunk(0, line(participantA, "50000")))
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Contains(t, err.Error(), "has no entitlement")
	require.Zero(t, rewards.payCalls)
}

// TestAChunkRequiresTheEpochAnchorItsDeadlineFollowsFrom refuses to invent a
// deadline.
//
// Every deadline in an epoch is counted from that epoch's single anchor. Without
// it there is no window, and an admission that proceeded anyway would be admitting
// against a window nobody set.
func TestAChunkRequiresTheEpochAnchorItsDeadlineFollowsFrom(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)
	require.NoError(t, k.SettlementEpochAnchors.Remove(ctx, 1))

	_, err := k.SubmitSettlementChunk(ctx, chunk(0, line(participantA, "50000")))
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Contains(t, err.Error(), "no settlement epoch anchor")
	require.Zero(t, rewards.payCalls)
}

// TestAChunkIsRefusedWhenTheBoundParametersDisagreeWithTheSettlement.
//
// History is immutable and a target's binding boundary has long since passed by the
// time a chunk arrives, so re-resolution must agree. A disagreement means the row
// and the history have come apart, and every bound derived from either alone —
// the recipient count, the chunk count, the floor, the deadline — would be wrong.
func TestAChunkIsRefusedWhenTheBoundParametersDisagreeWithTheSettlement(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)

	settlement, _, err := k.GetSettlement(ctx, 1, 1)
	require.NoError(t, err)
	settlement.SettlementParamsVersion = 7
	require.NoError(t, k.Settlements.Set(ctx, collections.Join(uint64(1), uint64(1)), settlement))

	_, err = k.SubmitSettlementChunk(ctx, chunk(0, line(participantA, "50000")))
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Contains(t, err.Error(), "binds version 1")
	require.Zero(t, rewards.payCalls)
}

// TestAChunkIsRefusedBeforeAnyStateIsReadOnAMalformedMessage covers the stateless
// pass.
//
// It is a cheap rejection of obvious garbage and never the only defense: the
// handler re-derives everything it needs, because stateless validation is not
// guaranteed to run on every execution path.
func TestAChunkIsRefusedBeforeAnyStateIsReadOnAMalformedMessage(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)

	for name, mutate := range map[string]func(*types.MsgSubmitSettlementChunk){
		"no slot":       func(m *types.MsgSubmitSettlementChunk) { m.SlotId = 0 },
		"no epoch":      func(m *types.MsgSubmitSettlementChunk) { m.Epoch = 0 },
		"no recipients": func(m *types.MsgSubmitSettlementChunk) { m.Payouts = nil },
		"nil line": func(m *types.MsgSubmitSettlementChunk) {
			m.Payouts = []*types.SettlementChunkPayout{nil}
		},
	} {
		t.Run(name, func(t *testing.T) {
			msg := chunk(0, line(participantA, "50000"))
			mutate(msg)
			_, err := k.SubmitSettlementChunk(ctx, msg)
			require.Error(t, err)
			require.Zero(t, rewards.payCalls)
		})
	}

	_, err := k.SubmitSettlementChunk(ctx, nil)
	require.ErrorIs(t, err, types.ErrInvalidState)
}

// setSettlementMode rewrites the materialized settlement's mode, which is the only
// way this profile can reach the two arms it cannot produce.
func setSettlementMode(t *testing.T, k keeper.Keeper, ctx sdk.Context, mode types.SettlementMode) {
	t.Helper()
	settlement, found, err := k.GetSettlement(ctx, 1, 1)
	require.NoError(t, err)
	require.True(t, found)
	settlement.SettlementMode = mode
	require.NoError(t, k.Settlements.Set(ctx, collections.Join(uint64(1), uint64(1)), settlement))
}

// setSettlementParams rewrites the bound parameter row in place, keeping its
// version so the settlement's recorded binding still agrees with it.
//
// This is how the CONFIGURED bounds are exercised distinctly from the immutable
// ones. The recommended defaults happen to equal the hard ceilings for both
// counts, so a test using the defaults cannot tell the two checks apart.
func setSettlementParams(
	t *testing.T, k keeper.Keeper, ctx sdk.Context, mutate func(*types.SettlementParamsVersion),
) {
	t.Helper()
	params, err := k.SettlementParamsVersions.Get(ctx, 1)
	require.NoError(t, err)
	mutate(&params)
	require.NoError(t, k.SettlementParamsVersions.Set(ctx, 1, params))
}

// TestTheConfiguredRecipientCountBindsBelowTheImmutableCeiling proves governance
// can tighten the per-chunk fan-out, and that the tighter value is the operative
// one.
func TestTheConfiguredRecipientCountBindsBelowTheImmutableCeiling(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)
	setSettlementParams(t, k, ctx, func(p *types.SettlementParamsVersion) {
		p.MaxRecipientsPerChunk = 2
	})

	_, err := k.SubmitSettlementChunk(ctx, chunk(0,
		line(participantA, "50000"), line(participantB, "50000"), line(participantC, "50000")))
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Contains(t, err.Error(), "above the configured maximum of 2")
	require.Zero(t, rewards.payCalls, "the immutable ceiling of 32 would have admitted this")

	_, err = k.SubmitSettlementChunk(ctx, chunk(0,
		line(participantA, "50000"), line(participantB, "50000")))
	require.NoError(t, err)
}

// TestTheConfiguredChunkCountBindsBelowTheImmutableCeiling is the same property for
// the number of chunks a settlement gets.
func TestTheConfiguredChunkCountBindsBelowTheImmutableCeiling(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)
	setSettlementParams(t, k, ctx, func(p *types.SettlementParamsVersion) {
		p.MaxChunksPerSettlement = 2
	})

	for index := uint64(0); index < 2; index++ {
		_, err := k.SubmitSettlementChunk(ctx, chunk(index, line(participantA, "10000")))
		require.NoErrorf(t, err, "chunk %d", index)
	}
	callsBefore := rewards.payCalls

	_, err := k.SubmitSettlementChunk(ctx, chunk(2, line(participantA, "10000")))
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Contains(t, err.Error(), "permits 2 chunks and has used them all")
	require.Equal(t, callsBefore, rewards.payCalls,
		"the immutable ceiling of 4 would have admitted this")
}

// TestTheConfiguredPayoutFloorBindsAboveTheImmutableOne proves governance can raise
// the participant minimum, and that raising it takes effect.
//
// It can only be raised. A stored row BELOW the immutable floor is refused when the
// parameter record is read, so the handler's floor is never reached with one — the
// larger-of-the-two it computes is a second line of defense rather than a reachable
// branch.
func TestTheConfiguredPayoutFloorBindsAboveTheImmutableOne(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)
	setSettlementParams(t, k, ctx, func(p *types.SettlementParamsVersion) {
		p.MinRecipientPayoutAmount = "50000"
	})

	_, err := k.SubmitSettlementChunk(ctx, chunk(0, line(participantA, "49999")))
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Contains(t, err.Error(), "below the minimum participant payout of 50000")
	require.Zero(t, rewards.payCalls, "the immutable floor of 10000 would have admitted this")

	_, err = k.SubmitSettlementChunk(ctx, chunk(0, line(participantA, "50000")))
	require.NoError(t, err)
}

// TestASettlementMayNotResolveAnotherSlotsEntitlement pins the identity check.
//
// A settlement and its entitlement are created in the same transition and name the
// same obligation. If a read ever returned a different one, the ceiling proven here
// would be another Slot's, and the release would be authorized against money that
// was never earned by this one.
func TestASettlementMayNotResolveAnotherSlotsEntitlement(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)
	mismatched := entitlement(1, 1, fixtureEntitlement)
	mismatched.Epoch = 4
	rewards.entitlements[1] = []rewardstypes.SlotEntitlement{mismatched}

	_, err := k.SubmitSettlementChunk(ctx, chunk(0, line(participantA, "50000")))
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Contains(t, err.Error(), "resolved an entitlement for slot 1 in epoch 4")
	require.Zero(t, rewards.payCalls)
}

// TestAFutureAnchorCannotAuthorizeAParticipantRelease is the temporal integrity
// rule the window check alone does not give.
//
// Admission proves current_clock < anchor_clock + window, which treats the anchor
// as trusted input. An anchor carrying a clock the chain has not reached pushes
// the deadline forward by exactly its excess, so a corrupted future anchor does
// not merely survive — it EXTENDS the participant window and can reopen one that
// had already closed.
//
// The anchor here is far enough ahead that the window check would happily admit
// the chunk, so the test genuinely reaches the deadline authorization arm rather
// than passing on some earlier unrelated rejection. Every other input is valid:
// an OPEN participant-capable settlement, a real entitlement, the correct signer,
// chunk 0, an in-range amount.
func TestAFutureAnchorCannotAuthorizeAParticipantRelease(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)

	// Prove the chunk is otherwise admissible at this exact clock, so the failure
	// below can only be the anchor.
	clock, err := k.GetSettlementClock(ctx)
	require.NoError(t, err)
	settlement, found, err := k.GetSettlement(ctx, 1, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t,
		types.SettlementMode_SETTLEMENT_MODE_TRUSTED_AS, settlement.SettlementMode)
	require.False(t, settlement.Finalized)
	require.Zero(t, settlement.NextChunkIndex)

	anchor, found, err := k.GetSettlementEpochAnchor(ctx, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.LessOrEqual(t, anchor.CreatedSettlementClock, clock, "the fixture anchor is sane")

	// Now put the anchor ahead of the canonical clock.
	anchor.CreatedSettlementClock = clock + 5_000
	require.NoError(t, k.SettlementEpochAnchors.Set(ctx, 1, anchor))

	_, err = k.SubmitSettlementChunk(ctx, chunk(0, line(participantA, "50000")))
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Contains(t, err.Error(), "ahead of the canonical clock")

	require.Zero(t, rewards.payCalls, "the release boundary is never reached")
	require.Equal(t, "0", releasedAmount(t, rewards, 1, 1), "released_amount is unchanged")
	require.Zero(t, nextChunkIndex(t, k, ctx), "next_chunk_index is unchanged")
}

// TestAnAnchorAtTheCurrentClockOrZeroStaysValid is the boundary the temporal check
// must not overshoot.
//
// Equality is legitimate — an epoch materialized in this very block anchors at the
// clock this block produced — and so is zero, which is what an epoch closing
// before the clock has ever ticked carries. Rejecting either would break ordinary
// settlements rather than corrupt ones.
func TestAnAnchorAtTheCurrentClockOrZeroStaysValid(t *testing.T) {
	t.Run("anchor equal to the current clock", func(t *testing.T) {
		k, ctx, _ := settlementFixture(t)
		clock, err := k.GetSettlementClock(ctx)
		require.NoError(t, err)
		anchor, _, err := k.GetSettlementEpochAnchor(ctx, 1)
		require.NoError(t, err)
		anchor.CreatedSettlementClock = clock
		require.NoError(t, k.SettlementEpochAnchors.Set(ctx, 1, anchor))

		_, err = k.SubmitSettlementChunk(ctx, chunk(0, line(participantA, "50000")))
		require.NoError(t, err)
	})

	t.Run("anchor and clock both zero", func(t *testing.T) {
		k, ctx, _ := settlementFixture(t)
		anchor, _, err := k.GetSettlementEpochAnchor(ctx, 1)
		require.NoError(t, err)
		anchor.CreatedSettlementClock = 0
		require.NoError(t, k.SettlementEpochAnchors.Set(ctx, 1, anchor))
		require.NoError(t, k.SettlementClock.Set(ctx, 0))

		_, err = k.SubmitSettlementChunk(ctx, chunk(0, line(participantA, "50000")))
		require.NoError(t, err)
	})
}
