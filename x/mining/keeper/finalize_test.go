package keeper_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/twilight-project/twilight-core/x/mining/keeper"
	"github.com/twilight-project/twilight-core/x/mining/types"
	rewardstypes "github.com/twilight-project/twilight-core/x/rewards/types"
)

// Settlement finalization: the terminal OPEN -> FINALIZED transition.
//
// The rewards double records that the operator remainder was released without
// moving balances, so what these establish is the AUTHORIZATION model: which arm
// applies, who may trigger it, what the settlement records afterwards, and that
// nothing commits when any part fails. That real money reaches the operator, and
// only the operator, is established on a real bank in the app tests.

const outsider = 0x91

func finalize(signer byte) *types.MsgFinalizeSettlement {
	return &types.MsgFinalizeSettlement{
		Signer: account(signer), SlotId: 1, Epoch: 1,
	}
}

// settledTo drives the fixture to a chosen released position so finalization has a
// specific remainder to pay.
func settledTo(t *testing.T, k keeper.Keeper, ctx sdk.Context, amount string) {
	t.Helper()
	_, err := k.SubmitSettlementChunk(ctx, chunk(0, line(participantA, amount)))
	require.NoError(t, err)
}

func settlementRow(t *testing.T, k keeper.Keeper, ctx sdk.Context) types.Settlement {
	t.Helper()
	settlement, found, err := k.GetSettlement(ctx, 1, 1)
	require.NoError(t, err)
	require.True(t, found)
	return settlement
}

// pastDeadline moves the canonical clock to a chosen offset from the settlement's
// derived deadline, through the clock the chain itself maintains.
func pastDeadline(t *testing.T, k keeper.Keeper, ctx sdk.Context, offset int64) {
	t.Helper()
	deadline, err := k.SettlementDeadlineClock(ctx, settlementRow(t, k, ctx))
	require.NoError(t, err)
	require.NoError(t, k.SettlementClock.Set(ctx, uint64(int64(deadline)+offset)))
}

// TestAuthorizedEarlyFinalizationReleasesTheRemainderAndRecordsItsArm is arm B.
//
// Inside the participant window only the settlement signer may finalize, because
// finalizing now forfeits whatever participant distribution had not happened yet.
func TestAuthorizedEarlyFinalizationReleasesTheRemainderAndRecordsItsArm(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)
	settledTo(t, k, ctx, "400000")

	reason, remainder, err := k.FinalizeSettlement(ctx, finalize(settlementSigner))
	require.NoError(t, err)
	require.Equal(t,
		types.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_AUTHORIZED_EARLY, reason)
	require.Equal(t, "600000", remainder.String())
	require.Equal(t, 1, rewards.remainderCalls, "the operator remainder was released once")

	settlement := settlementRow(t, k, ctx)
	require.True(t, settlement.Finalized)
	require.Equal(t, uint64(1), settlement.FinalizedHeight)
	require.Equal(t,
		types.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_AUTHORIZED_EARLY,
		settlement.FinalizationReason)
	require.Equal(t, fixtureEntitlement, releasedAmount(t, rewards, 1, 1),
		"a finalized settlement has released its entitlement in full")
}

// TestEarlyFinalizationRefusesAnyoneButTheSettlementSigner closes arm B's authority.
func TestEarlyFinalizationRefusesAnyoneButTheSettlementSigner(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)

	_, _, err := k.FinalizeSettlement(ctx, finalize(outsider))
	require.ErrorIs(t, err, types.ErrInvalidAddress)
	require.Zero(t, rewards.remainderCalls, "no remainder is released")

	settlement := settlementRow(t, k, ctx)
	require.False(t, settlement.Finalized)
	require.Zero(t, settlement.FinalizedHeight)
	require.Equal(t,
		types.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_UNSPECIFIED,
		settlement.FinalizationReason)
}

// TestEarlyFinalizationNeedsNoParticipantDistribution is deliberate authority, not
// an oversight.
//
// The settlement signer may distribute nothing and release the entire entitlement
// to the immutable operator address. That is inside the POC1 authority model, and
// requiring at least one chunk would be inventing a rule the protocol does not have.
func TestEarlyFinalizationNeedsNoParticipantDistribution(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)

	reason, remainder, err := k.FinalizeSettlement(ctx, finalize(settlementSigner))
	require.NoError(t, err)
	require.Equal(t,
		types.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_AUTHORIZED_EARLY, reason)
	require.Equal(t, fixtureEntitlement, remainder.String(), "the whole entitlement is the remainder")
	require.Zero(t, rewards.payCalls, "no participant chunk was ever submitted")
	require.Equal(t, 1, rewards.remainderCalls)
}

// TestAnUnusedChunkBudgetDoesNotBlockFinalization pins the other non-requirement.
//
// The configured chunk count is a ceiling, not a quota. A settlement with chunks
// left unused is finalizable, or an AS that chose to stop early could never close.
func TestAnUnusedChunkBudgetDoesNotBlockFinalization(t *testing.T) {
	k, ctx, _ := settlementFixture(t)
	settledTo(t, k, ctx, "100000")
	require.Equal(t, uint64(1), settlementRow(t, k, ctx).NextChunkIndex,
		"three of the four configured chunks are unused")

	_, _, err := k.FinalizeSettlement(ctx, finalize(settlementSigner))
	require.NoError(t, err)
}

// TestPermissionlessFinalizationAfterTheDeadline is arm C.
//
// Once the participant window has closed there is no authority left to protect, so
// anyone may push a stuck settlement to terminal — and gains nothing by doing so.
func TestPermissionlessFinalizationAfterTheDeadline(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)
	settledTo(t, k, ctx, "400000")
	pastDeadline(t, k, ctx, 0)

	reason, remainder, err := k.FinalizeSettlement(ctx, finalize(outsider))
	require.NoError(t, err)
	require.Equal(t,
		types.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_PERMISSIONLESS_AFTER_DEADLINE,
		reason)
	require.Equal(t, "600000", remainder.String())
	require.Equal(t, 1, rewards.remainderCalls)
	require.Equal(t,
		types.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_PERMISSIONLESS_AFTER_DEADLINE,
		settlementRow(t, k, ctx).FinalizationReason)
}

// TestTheDeadlineBoundarySelectsThePermissionlessArm pins equality exactly.
//
// At clock == deadline chunks are already permanently refused, so the window is
// over and the arm is permissionless. One tick earlier it is still the authorized
// signer's alone.
func TestTheDeadlineBoundarySelectsThePermissionlessArm(t *testing.T) {
	t.Run("one tick before the deadline is still authorized-only", func(t *testing.T) {
		k, ctx, rewards := settlementFixture(t)
		pastDeadline(t, k, ctx, -1)

		_, _, err := k.FinalizeSettlement(ctx, finalize(outsider))
		require.ErrorIs(t, err, types.ErrInvalidAddress)
		require.Zero(t, rewards.remainderCalls)

		reason, _, err := k.FinalizeSettlement(ctx, finalize(settlementSigner))
		require.NoError(t, err)
		require.Equal(t,
			types.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_AUTHORIZED_EARLY, reason)
	})

	t.Run("exactly at the deadline is permissionless", func(t *testing.T) {
		k, ctx, _ := settlementFixture(t)
		pastDeadline(t, k, ctx, 0)

		reason, _, err := k.FinalizeSettlement(ctx, finalize(outsider))
		require.NoError(t, err)
		require.Equal(t,
			types.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_PERMISSIONLESS_AFTER_DEADLINE,
			reason)
	})
}

// TestTheReasonFollowsTheArmAndNotTheCaller is the rule most easily got wrong.
//
// An authorized settlement signer finalizing AFTER the deadline records
// PERMISSIONLESS_AFTER_DEADLINE, because by then the transition no longer needed
// that signer's authority. Recording AUTHORIZED_EARLY would claim a permission was
// exercised that was not required, and would make the audit record describe the
// caller rather than the authorization path.
func TestTheReasonFollowsTheArmAndNotTheCaller(t *testing.T) {
	k, ctx, _ := settlementFixture(t)
	pastDeadline(t, k, ctx, 10)

	reason, _, err := k.FinalizeSettlement(ctx, finalize(settlementSigner))
	require.NoError(t, err)
	require.Equal(t,
		types.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_PERMISSIONLESS_AFTER_DEADLINE,
		reason, "the authorized signer does not get the early arm merely by being authorized")
	require.Equal(t,
		types.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_PERMISSIONLESS_AFTER_DEADLINE,
		settlementRow(t, k, ctx).FinalizationReason)
}

// TestOperatorOnlyFinalizesImmediatelyForAnySigner is arm A, and it is decided by
// MODE alone.
//
// An operator-only settlement has no participant window, so no deadline enters into
// it and it is finalizable immediately. The arm is recorded for any signer,
// including the settlement credential — the mode decides, never the caller.
func TestOperatorOnlyFinalizesImmediatelyForAnySigner(t *testing.T) {
	for name, signer := range map[string]byte{
		"an unrelated account":  outsider,
		"the settlement signer": settlementSigner,
	} {
		t.Run(name, func(t *testing.T) {
			k, ctx, rewards := settlementFixture(t)
			setSettlementMode(t, k, ctx, types.SettlementMode_SETTLEMENT_MODE_OPERATOR_ONLY)

			reason, remainder, err := k.FinalizeSettlement(ctx, finalize(signer))
			require.NoError(t, err)
			require.Equal(t,
				types.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_PERMISSIONLESS_OPERATOR_ONLY,
				reason)
			require.Equal(t, fixtureEntitlement, remainder.String(),
				"the whole entitlement belongs to the operator")
			require.Equal(t, 1, rewards.remainderCalls)
			require.Zero(t, rewards.payCalls, "no participant chunk is admissible in this mode")
		})
	}
}

// TestOperatorOnlyIgnoresTheDeadlineEntirely proves mode precedes time.
func TestOperatorOnlyIgnoresTheDeadlineEntirely(t *testing.T) {
	k, ctx, _ := settlementFixture(t)
	setSettlementMode(t, k, ctx, types.SettlementMode_SETTLEMENT_MODE_OPERATOR_ONLY)
	// Deliberately far inside the participant window, where a non-operator-only
	// settlement would demand the settlement signer.
	require.NoError(t, k.SettlementClock.Set(ctx, 1))

	reason, _, err := k.FinalizeSettlement(ctx, finalize(outsider))
	require.NoError(t, err)
	require.Equal(t,
		types.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_PERMISSIONLESS_OPERATOR_ONLY,
		reason)
}

// TestSelectedParticipantsFinalizesOnTheNonOperatorArms covers the third mode.
//
// Unreachable in this profile and implemented anyway so a later tranche adds a
// producer rather than a branch. It behaves as a participant-capable settlement:
// authorized inside the window, permissionless past the deadline.
func TestSelectedParticipantsFinalizesOnTheNonOperatorArms(t *testing.T) {
	k, ctx, _ := settlementFixture(t)
	setSettlementMode(t, k, ctx, types.SettlementMode_SETTLEMENT_MODE_SELECTED_PARTICIPANTS)

	_, _, err := k.FinalizeSettlement(ctx, finalize(outsider))
	require.ErrorIs(t, err, types.ErrInvalidAddress, "inside the window it is authorized-only")

	reason, _, err := k.FinalizeSettlement(ctx, finalize(settlementSigner))
	require.NoError(t, err)
	require.Equal(t,
		types.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_AUTHORIZED_EARLY, reason)
}

// TestAZeroRemainderFinalizesWithNoReleaseAtAll is the legitimate zero substep.
//
// A fully distributed entitlement owes the operator nothing. Finalization still
// happens, and it must not reach the release boundary at all: a zero-value transfer
// would touch an account for no reason, which on a feeless chain is a free
// account-creation primitive.
func TestAZeroRemainderFinalizesWithNoReleaseAtAll(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)
	settledTo(t, k, ctx, fixtureEntitlement)
	require.Equal(t, fixtureEntitlement, releasedAmount(t, rewards, 1, 1))

	reason, remainder, err := k.FinalizeSettlement(ctx, finalize(settlementSigner))
	require.NoError(t, err)
	require.Equal(t, "0", remainder.String())
	require.Zero(t, rewards.remainderCalls, "the release boundary is never reached")
	require.Equal(t,
		types.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_AUTHORIZED_EARLY, reason)
	require.True(t, settlementRow(t, k, ctx).Finalized)
}

// TestFinalizationIsTerminal proves the transition cannot repeat.
//
// A second finalization would pay the operator twice against one obligation.
func TestFinalizationIsTerminal(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)
	_, _, err := k.FinalizeSettlement(ctx, finalize(settlementSigner))
	require.NoError(t, err)
	callsAfterFirst := rewards.remainderCalls
	before := settlementRow(t, k, ctx)

	_, _, err = k.FinalizeSettlement(ctx, finalize(settlementSigner))
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Contains(t, err.Error(), "already finalized")
	require.Equal(t, callsAfterFirst, rewards.remainderCalls, "no second payout")
	require.Equal(t, before, settlementRow(t, k, ctx), "no terminal metadata is rewritten")
}

// TestAFinalizedSettlementAdmitsNoFurtherChunks is the A5/A4 cross-regression.
//
// The chunk handler already refuses a terminal settlement; what is new is that
// finalization can now produce one through the ordinary path rather than only from
// hand-written state.
func TestAFinalizedSettlementAdmitsNoFurtherChunks(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)
	settledTo(t, k, ctx, "100000")
	_, _, err := k.FinalizeSettlement(ctx, finalize(settlementSigner))
	require.NoError(t, err)
	paysBefore := rewards.payCalls

	_, err = k.SubmitSettlementChunk(ctx, chunk(1, line(participantB, "10000")))
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Contains(t, err.Error(), "finalized and admits no further chunks")
	require.Equal(t, paysBefore, rewards.payCalls)
}

// TestFinalizationRetiresTheOpenIndexEntry keeps the derived index honest.
//
// The OPEN index is rebuilt from canonical rows on import and skips finalized ones,
// so an entry left behind would make the index disagree with the settlements it
// describes — corruption rather than a stale convenience.
func TestFinalizationRetiresTheOpenIndexEntry(t *testing.T) {
	k, ctx, _ := settlementFixture(t)
	key := collections.Join(uint64(1), uint64(1))
	has, err := k.OpenSettlementsBySlot.Has(ctx, key)
	require.NoError(t, err)
	require.True(t, has, "the settlement starts open and indexed")

	_, _, err = k.FinalizeSettlement(ctx, finalize(settlementSigner))
	require.NoError(t, err)

	has, err = k.OpenSettlementsBySlot.Has(ctx, key)
	require.NoError(t, err)
	require.False(t, has, "a finalized settlement leaves the OPEN index")
}

// TestAPausedChainRefusesFinalization covers the global gate.
//
// A pause blocks every action that moves value, and changes no stored settlement
// field. Because the clock stops while paused, the deadline is frozen rather than
// consumed, so the arm on resume is the arm it would have been.
func TestAPausedChainRefusesFinalization(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)
	before := settlementRow(t, k, ctx)
	rewards.releaseEnabled = false

	_, _, err := k.FinalizeSettlement(ctx, finalize(settlementSigner))
	require.ErrorIs(t, err, types.ErrUnsupportedFeature)
	require.Contains(t, err.Error(), "paused")
	require.Zero(t, rewards.remainderCalls)
	require.Equal(t, before, settlementRow(t, k, ctx), "no settlement field changed")

	rewards.releaseEnabled = true
	_, _, err = k.FinalizeSettlement(ctx, finalize(settlementSigner))
	require.NoError(t, err)
}

// TestAFutureAnchorCannotAuthorizeFinalization is the temporal invariant on the
// finalization path.
//
// It matters here for a different reason than on the chunk path: a future anchor
// pushes the deadline forward, which can flip a settlement from permissionless back
// to authorized-signer-only. That hands one party a veto over a transition the
// protocol had already opened to everyone — an authorization failure even though
// the remainder destination is immutable.
func TestAFutureAnchorCannotAuthorizeFinalization(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)
	// Past the real deadline: without a corrupted anchor this is permissionless.
	pastDeadline(t, k, ctx, 0)
	clock, err := k.GetSettlementClock(ctx)
	require.NoError(t, err)

	anchor, _, err := k.GetSettlementEpochAnchor(ctx, 1)
	require.NoError(t, err)
	anchor.CreatedSettlementClock = clock + 5_000
	require.NoError(t, k.SettlementEpochAnchors.Set(ctx, 1, anchor))

	_, _, err = k.FinalizeSettlement(ctx, finalize(outsider))
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Contains(t, err.Error(), "ahead of the canonical clock")

	require.Zero(t, rewards.remainderCalls, "no operator remainder release")
	settlement := settlementRow(t, k, ctx)
	require.False(t, settlement.Finalized, "the settlement remains open")
	require.Zero(t, settlement.FinalizedHeight)
	require.Equal(t,
		types.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_UNSPECIFIED,
		settlement.FinalizationReason)
}

// TestFinalizationFailsClosedOnUnreadableCanonicalState covers the corruption set.
//
// Each of these is state no admitted transition could have produced, and each must
// halt rather than be interpreted.
func TestFinalizationFailsClosedOnUnreadableCanonicalState(t *testing.T) {
	t.Run("missing settlement", func(t *testing.T) {
		k, ctx, rewards := settlementFixture(t)
		msg := finalize(settlementSigner)
		msg.Epoch = 9
		_, _, err := k.FinalizeSettlement(ctx, msg)
		require.ErrorIs(t, err, types.ErrSettlementNotFound)
		require.Zero(t, rewards.remainderCalls)
	})

	t.Run("missing epoch anchor", func(t *testing.T) {
		k, ctx, rewards := settlementFixture(t)
		require.NoError(t, k.SettlementEpochAnchors.Remove(ctx, 1))
		_, _, err := k.FinalizeSettlement(ctx, finalize(settlementSigner))
		require.ErrorIs(t, err, types.ErrInvalidState)
		require.Contains(t, err.Error(), "no settlement epoch anchor")
		require.Zero(t, rewards.remainderCalls)
	})

	t.Run("missing entitlement", func(t *testing.T) {
		k, ctx, rewards := settlementFixture(t)
		rewards.entitlements[1] = nil
		_, _, err := k.FinalizeSettlement(ctx, finalize(settlementSigner))
		require.ErrorIs(t, err, types.ErrInvalidState)
		require.Contains(t, err.Error(), "has no entitlement")
		require.Zero(t, rewards.remainderCalls)
	})

	t.Run("entitlement identity mismatch", func(t *testing.T) {
		k, ctx, rewards := settlementFixture(t)
		mismatched := entitlement(1, 1, fixtureEntitlement)
		mismatched.Epoch = 4
		rewards.entitlements[1] = []rewardstypes.SlotEntitlement{mismatched}
		_, _, err := k.FinalizeSettlement(ctx, finalize(settlementSigner))
		require.ErrorIs(t, err, types.ErrInvalidState)
		require.Contains(t, err.Error(), "resolved an entitlement for slot 1 in epoch 4")
		require.Zero(t, rewards.remainderCalls)
	})

	t.Run("released above the entitlement", func(t *testing.T) {
		k, ctx, rewards := settlementFixture(t)
		over := entitlement(1, 1, fixtureEntitlement)
		over.ReleasedAmount = "2000000"
		rewards.entitlements[1] = []rewardstypes.SlotEntitlement{over}

		_, _, err := k.FinalizeSettlement(ctx, finalize(settlementSigner))
		require.ErrorIs(t, err, types.ErrInvalidState)
		require.Contains(t, err.Error(), "has released 2000000 of 1000000")
		require.Zero(t, rewards.remainderCalls,
			"a negative remainder must never read as nothing owed")
		require.False(t, settlementRow(t, k, ctx).Finalized)
	})

	t.Run("bound parameters disagree with the settlement", func(t *testing.T) {
		k, ctx, rewards := settlementFixture(t)
		settlement := settlementRow(t, k, ctx)
		settlement.SettlementParamsVersion = 7
		require.NoError(t, k.Settlements.Set(ctx, collections.Join(uint64(1), uint64(1)), settlement))

		_, _, err := k.FinalizeSettlement(ctx, finalize(settlementSigner))
		require.ErrorIs(t, err, types.ErrInvalidState)
		require.Contains(t, err.Error(), "binds version 1")
		require.Zero(t, rewards.remainderCalls)
	})

	t.Run("settlement has no canonical mode", func(t *testing.T) {
		k, ctx, rewards := settlementFixture(t)
		setSettlementMode(t, k, ctx, types.SettlementMode_SETTLEMENT_MODE_UNSPECIFIED)
		_, _, err := k.FinalizeSettlement(ctx, finalize(settlementSigner))
		require.Error(t, err)
		require.Zero(t, rewards.remainderCalls)
	})
}

// TestFinalizationRequiresAValidAccountSigner proves permissionless is not
// unvalidated.
//
// A transaction whose signer cannot be parsed has no authenticated caller. The rule
// is account validity, deliberately not the economic-destination rule: a signer is a
// control identity the protocol never pays, so a module account may trigger a
// finalization even though it could never receive one.
func TestFinalizationRequiresAValidAccountSigner(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)
	pastDeadline(t, k, ctx, 0)

	for name, signer := range map[string]string{
		"empty":        "",
		"not bech32":   "not-an-address",
		"wrong prefix": "cosmos1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq",
	} {
		t.Run(name, func(t *testing.T) {
			msg := finalize(outsider)
			msg.Signer = signer
			_, _, err := k.FinalizeSettlement(ctx, msg)
			require.ErrorIs(t, err, types.ErrInvalidAddress)
			require.Zero(t, rewards.remainderCalls)
		})
	}

	t.Run("a module account may trigger but could never receive", func(t *testing.T) {
		msg := finalize(outsider)
		msg.Signer = authtypes.NewModuleAddress(fixtureModuleAccount).String()
		reason, _, err := k.FinalizeSettlement(ctx, msg)
		require.NoError(t, err)
		require.Equal(t,
			types.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_PERMISSIONLESS_AFTER_DEADLINE,
			reason)
	})
}

// TestFinalizationIsAtomicWhenTheRemainderReleaseFails is the rollback proof in the
// direction the code can reach.
//
// The release boundary refuses after every authorization check has passed. Nothing
// may survive: no terminal metadata, no index change, and the settlement stays
// retryable.
func TestFinalizationIsAtomicWhenTheRemainderReleaseFails(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)
	settledTo(t, k, ctx, "400000")
	rewards.remainderErr = errors.New("the bank refused the operator transfer")

	_, _, err := k.FinalizeSettlement(ctx, finalize(settlementSigner))
	require.Error(t, err)
	require.Equal(t, 1, rewards.remainderCalls, "the release was attempted")

	settlement := settlementRow(t, k, ctx)
	require.False(t, settlement.Finalized, "no terminal metadata is written")
	require.Zero(t, settlement.FinalizedHeight)
	require.Equal(t,
		types.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_UNSPECIFIED,
		settlement.FinalizationReason)
	has, err := k.OpenSettlementsBySlot.Has(ctx, collections.Join(uint64(1), uint64(1)))
	require.NoError(t, err)
	require.True(t, has, "the settlement is still indexed as open")

	// Retryable once the cause is gone.
	rewards.remainderErr = nil
	_, _, err = k.FinalizeSettlement(ctx, finalize(settlementSigner))
	require.NoError(t, err)
	require.True(t, settlementRow(t, k, ctx).Finalized)
}

// TestFinalizationRefusesAnUnderDeliveredRelease is the post-condition, proven from
// state rather than assumed from the call returning nil.
//
// The release boundary reports success while leaving part of the entitlement
// unreleased. Closing the obligation anyway would strand value in escrow with
// nothing left able to claim it, so the whole transition fails instead.
func TestFinalizationRefusesAnUnderDeliveredRelease(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)
	rewards.remainderShortfall = "1"

	_, _, err := k.FinalizeSettlement(ctx, finalize(settlementSigner))
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Contains(t, err.Error(), "unreleased")
	require.False(t, settlementRow(t, k, ctx).Finalized,
		"an under-delivered release must not close the obligation")
}

// TestFinalizationRejectsAMalformedMessage covers the stateless pass.
func TestFinalizationRejectsAMalformedMessage(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)

	for name, mutate := range map[string]func(*types.MsgFinalizeSettlement){
		"no slot":   func(m *types.MsgFinalizeSettlement) { m.SlotId = 0 },
		"no epoch":  func(m *types.MsgFinalizeSettlement) { m.Epoch = 0 },
		"no signer": func(m *types.MsgFinalizeSettlement) { m.Signer = "" },
	} {
		t.Run(name, func(t *testing.T) {
			msg := finalize(settlementSigner)
			mutate(msg)
			_, _, err := k.FinalizeSettlement(ctx, msg)
			require.Error(t, err)
			require.Zero(t, rewards.remainderCalls)
		})
	}

	_, _, err := k.FinalizeSettlement(ctx, nil)
	require.ErrorIs(t, err, types.ErrInvalidState)
}

// TestTheRemainderIsReleasedBeforeTheSettlementIsClosed pins the ORDER of the two
// effects, not merely that both happen.
//
// With the handler's cache in place either ordering is safe, because a failure
// discards everything — so no ordinary test distinguishes them, and a later edit
// could reorder them with every test still green while the guarantee quietly came
// to rest on the cache alone.
//
// Driving the transition body without that cache shows why the order is the one it
// is. Releasing first and closing second fails SAFE: a refused release leaves the
// settlement open and retryable. The reverse fails DANGEROUS: the settlement would
// be terminally closed while the operator was never paid, and terminal is exactly
// the state from which no retry is possible.
func TestTheRemainderIsReleasedBeforeTheSettlementIsClosed(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)
	rewards.remainderErr = errors.New("the bank refused the operator transfer")

	// No enclosing cache: whatever the body writes before failing survives.
	_, err := keeper.FinalizeSettlementWithoutCache(k, ctx, finalize(settlementSigner))
	require.Error(t, err)
	require.Equal(t, 1, rewards.remainderCalls, "the release was attempted")

	settlement := settlementRow(t, k, ctx)
	require.False(t, settlement.Finalized,
		"the settlement must not be closed when the remainder was never released")
	require.Zero(t, settlement.FinalizedHeight)
	require.Equal(t,
		types.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_UNSPECIFIED,
		settlement.FinalizationReason)
	has, err := k.OpenSettlementsBySlot.Has(ctx, collections.Join(uint64(1), uint64(1)))
	require.NoError(t, err)
	require.True(t, has, "and it is still indexed as open, so it can be retried")
}

// TestTheFinalizationEventIsDiscardedWithAFailedTransition keeps the event log from
// announcing money that never moved.
//
// The event is emitted inside the handler's cache, and the SDK propagates a cache's
// events to its parent only when the cache is written. So a failed finalization
// leaves no trace — which is the property that matters, because an indexer treating
// a settlement as terminal on the strength of an event for a transition that was
// discarded would report a payout that never happened.
func TestTheFinalizationEventIsDiscardedWithAFailedTransition(t *testing.T) {
	k, ctx, rewards := settlementFixture(t)
	rewards.remainderErr = errors.New("the bank refused the operator transfer")

	_, _, err := k.FinalizeSettlement(ctx, finalize(settlementSigner))
	require.Error(t, err)
	require.Zero(t, countFinalizationEvents(ctx), "a discarded transition announces nothing")

	rewards.remainderErr = nil
	_, _, err = k.FinalizeSettlement(ctx, finalize(settlementSigner))
	require.NoError(t, err)
	require.Equal(t, 1, countFinalizationEvents(ctx), "a committed transition announces once")

	event := finalizationEvent(t, ctx)
	require.Equal(t,
		types.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_AUTHORIZED_EARLY.String(),
		eventAttr(t, event, types.AttributeKeyFinalizationReason))
	require.Equal(t, fixtureEntitlement, eventAttr(t, event, types.AttributeKeyReleasedRemainder))
}

func countFinalizationEvents(ctx sdk.Context) int {
	count := 0
	for _, event := range ctx.EventManager().Events() {
		if event.Type == types.EventTypeSettlementFinalized {
			count++
		}
	}
	return count
}

func finalizationEvent(t *testing.T, ctx sdk.Context) sdk.Event {
	t.Helper()
	for _, event := range ctx.EventManager().Events() {
		if event.Type == types.EventTypeSettlementFinalized {
			return event
		}
	}
	t.Fatalf("no %s event was emitted", types.EventTypeSettlementFinalized)
	return sdk.Event{}
}

func eventAttr(t *testing.T, event sdk.Event, key string) string {
	t.Helper()
	for _, attr := range event.Attributes {
		if attr.Key == key {
			return attr.Value
		}
	}
	t.Fatalf("event %s has no attribute %s", event.Type, key)
	return ""
}

// TestOperatorOnlyFinalizationRequiresItsEpochAnchor closes the gap the independent
// review found.
//
// The anchor is not consulted to CHOOSE the operator-only arm — no deadline enters
// into it — but it is mandatory companion state: a settlement exists only because
// its epoch materialized at least one, and the two are created in the same
// transition. Its absence means the pair came apart, so driving a terminal money
// movement out of that settlement would be finalizing against canonical state
// already known to be broken.
//
// Each subtest first proves the settlement finalizes cleanly WITH its anchor, so the
// refusal that follows is demonstrably caused by anchor integrity and not by some
// earlier admission condition.
func TestOperatorOnlyFinalizationRequiresItsEpochAnchor(t *testing.T) {
	operatorOnly := func(t *testing.T) (keeper.Keeper, sdk.Context, *rewardsKeeperMock) {
		t.Helper()
		k, ctx, rewards := settlementFixture(t)
		setSettlementMode(t, k, ctx, types.SettlementMode_SETTLEMENT_MODE_OPERATOR_ONLY)
		return k, ctx, rewards
	}

	t.Run("the same settlement finalizes with its anchor present", func(t *testing.T) {
		k, ctx, rewards := operatorOnly(t)
		reason, _, err := k.FinalizeSettlement(ctx, finalize(outsider))
		require.NoError(t, err, "every other admission condition is satisfied")
		require.Equal(t,
			types.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_PERMISSIONLESS_OPERATOR_ONLY,
			reason)
		require.Equal(t, 1, rewards.remainderCalls)
	})

	t.Run("absent anchor", func(t *testing.T) {
		k, ctx, rewards := operatorOnly(t)
		require.NoError(t, k.SettlementEpochAnchors.Remove(ctx, 1))

		_, _, err := k.FinalizeSettlement(ctx, finalize(outsider))
		require.ErrorIs(t, err, types.ErrInvalidState)
		require.Contains(t, err.Error(), "no settlement epoch anchor")
		assertOperatorOnlyUntouched(t, k, ctx, rewards)
	})

	t.Run("anchor disagreeing with the key it is filed under", func(t *testing.T) {
		k, ctx, rewards := operatorOnly(t)
		// Structurally present but not canonical: the row at epoch 1 declares another
		// epoch, so it is an anchor for a different obligation.
		require.NoError(t, k.SettlementEpochAnchors.Set(ctx, 1, types.SettlementEpochAnchor{
			Epoch: 5, CreatedSettlementClock: 0,
		}))

		_, _, err := k.FinalizeSettlement(ctx, finalize(outsider))
		require.ErrorIs(t, err, types.ErrInvalidState)
		require.Contains(t, err.Error(), "declares epoch 5")
		assertOperatorOnlyUntouched(t, k, ctx, rewards)
	})
}

// assertOperatorOnlyUntouched proves a refused finalization left nothing behind.
func assertOperatorOnlyUntouched(
	t *testing.T, k keeper.Keeper, ctx sdk.Context, rewards *rewardsKeeperMock,
) {
	t.Helper()
	require.Zero(t, rewards.remainderCalls, "no remainder release call")
	require.Equal(t, "0", releasedAmount(t, rewards, 1, 1), "released amount unchanged")

	settlement := settlementRow(t, k, ctx)
	require.False(t, settlement.Finalized, "settlement remains open")
	require.Zero(t, settlement.FinalizedHeight, "finalized height remains 0")
	require.Equal(t,
		types.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_UNSPECIFIED,
		settlement.FinalizationReason, "finalization reason remains unspecified")

	has, err := k.OpenSettlementsBySlot.Has(ctx, collections.Join(uint64(1), uint64(1)))
	require.NoError(t, err)
	require.True(t, has, "the OPEN index entry remains present")
}

// TestOperatorOnlyStillIgnoresTimeAndBoundParameters is the regression the anchor
// correction must not break.
//
// Requiring the anchor to EXIST must not smuggle in a dependency on what it
// CONTAINS, nor on the settlement parameters or the clock. Each condition below
// would refuse a participant-capable settlement, and each must be irrelevant here:
// the operator-only arm derives nothing from any of them.
func TestOperatorOnlyStillIgnoresTimeAndBoundParameters(t *testing.T) {
	corrupt := map[string]func(*testing.T, keeper.Keeper, sdk.Context){
		"bound parameters disagreeing with the settlement": func(
			t *testing.T, k keeper.Keeper, ctx sdk.Context,
		) {
			settlement := settlementRow(t, k, ctx)
			settlement.SettlementParamsVersion = 7
			require.NoError(t, k.Settlements.Set(ctx, collections.Join(uint64(1), uint64(1)), settlement))
		},
		"an anchor ahead of the canonical clock": func(
			t *testing.T, k keeper.Keeper, ctx sdk.Context,
		) {
			clock, err := k.GetSettlementClock(ctx)
			require.NoError(t, err)
			anchor, _, err := k.GetSettlementEpochAnchor(ctx, 1)
			require.NoError(t, err)
			anchor.CreatedSettlementClock = clock + 5_000
			require.NoError(t, k.SettlementEpochAnchors.Set(ctx, 1, anchor))
		},
		"a clock far inside the participant window": func(
			t *testing.T, k keeper.Keeper, ctx sdk.Context,
		) {
			require.NoError(t, k.SettlementClock.Set(ctx, 1))
		},
	}

	for name, breakIt := range corrupt {
		t.Run(name, func(t *testing.T) {
			// A participant-capable settlement refuses under this condition...
			k, ctx, _ := settlementFixture(t)
			breakIt(t, k, ctx)
			_, _, err := k.FinalizeSettlement(ctx, finalize(outsider))
			require.Error(t, err, "the condition really would refuse a deadline-bound settlement")

			// ...and an operator-only one does not even look.
			k, ctx, rewards := settlementFixture(t)
			setSettlementMode(t, k, ctx, types.SettlementMode_SETTLEMENT_MODE_OPERATOR_ONLY)
			breakIt(t, k, ctx)

			reason, remainder, err := k.FinalizeSettlement(ctx, finalize(outsider))
			require.NoError(t, err)
			require.Equal(t,
				types.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_PERMISSIONLESS_OPERATOR_ONLY,
				reason)
			require.Equal(t, fixtureEntitlement, remainder.String())
			require.Equal(t, 1, rewards.remainderCalls)
		})
	}
}
