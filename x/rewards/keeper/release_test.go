package keeper_test

import (
	"context"
	"reflect"
	"testing"

	sdkmath "cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	"github.com/twilight-project/twilight-core/x/rewards/keeper"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// The constrained release boundary.
//
// Two properties are asserted throughout and are worth stating once: every
// rejection must leave bank state, released_amount and the liability all
// untouched, and every acceptance must move all three together. A test that only
// checked balances would pass against an implementation that forgot the
// accumulator, so each case checks the triple.

const releaseEntitlementAmount = "1000"

func setupRelease(t *testing.T, blocked ...string) (keeper.Keeper, sdk.Context, *bankKeeperMock) {
	t.Helper()
	k, ctx, bank := setupKeeperWithBlocked(t, &coreSlotKeeperMock{}, blocked...)
	params := rewardConfigParams()
	require.NoError(t, k.SetParams(ctx, params))
	require.NoError(t, k.SetState(ctx, types.RewardsState{
		CurrentEpoch: 1, CurrentEpochStartHeight: 1, CumulativeEmitted: "0", CarryForwardRemainder: "0",
	}))
	require.NoError(t, k.SetPauseState(ctx, types.RewardsPauseState{}))
	require.NoError(t, k.SetOpenRewardEnabledBlocks(ctx, 0))
	seedRewardConfigTimeline(t, k, ctx, params)
	require.NoError(t, k.SetOutstandingEntitlementLiability(ctx, sdkmath.ZeroInt()))
	require.NoError(t, k.CreateSlotEntitlement(ctx, entitlementFor(1, 1, releaseEntitlementAmount)))
	return k, ctx, bank
}

func payout(recipient, amount string) types.EntitlementPayout {
	return types.EntitlementPayout{Recipient: recipient, Amount: amount}
}

// requireReleaseState asserts the released amount and the liability together.
func requireReleaseState(t *testing.T, k keeper.Keeper, ctx sdk.Context, released, liability string) {
	t.Helper()
	entitlement, found, err := k.GetSlotEntitlement(ctx, 1, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, released, entitlement.ReleasedAmount)
	requireLiability(t, k, ctx, liability)
}

func TestPayEntitlementReleasesASingleRecipient(t *testing.T) {
	k, ctx, bank := setupRelease(t)
	require.NoError(t, k.PayEntitlement(ctx, 1, 1, []types.EntitlementPayout{
		payout(testAccount(9), "250"),
	}))

	require.Equal(t, 1, bank.sendCalls)
	require.Equal(t, "250", bank.sends[0].amounts[0].Amount.String())
	requireReleaseState(t, k, ctx, "250", "750")
}

func TestPayEntitlementReleasesAPayoutSetInOneTransition(t *testing.T) {
	k, ctx, bank := setupRelease(t)
	require.NoError(t, k.PayEntitlement(ctx, 1, 1, []types.EntitlementPayout{
		payout(testAccount(9), "100"),
		payout(testAccount(8), "200"),
		payout(testAccount(7), "300"),
	}))

	require.Equal(t, 3, bank.sendCalls)
	requireReleaseState(t, k, ctx, "600", "400")
}

// TestPayEntitlementAggregatesRepeatedRecipients proves a set that names one
// destination twice produces one transfer of the sum.
//
// Two sends would be two account touches for one authorization, and would make
// the observable effect of a payout set depend on how the caller chose to split
// it.
func TestPayEntitlementAggregatesRepeatedRecipients(t *testing.T) {
	k, ctx, bank := setupRelease(t)
	recipient := testAccount(9)
	require.NoError(t, k.PayEntitlement(ctx, 1, 1, []types.EntitlementPayout{
		payout(recipient, "100"),
		payout(testAccount(8), "50"),
		payout(recipient, "150"),
	}))

	require.Equal(t, 2, bank.sendCalls, "one send per distinct destination")
	requireReleaseState(t, k, ctx, "300", "700")
}

func TestPayEntitlementSupportsSequentialPartialReleases(t *testing.T) {
	k, ctx, _ := setupRelease(t)
	require.NoError(t, k.PayEntitlement(ctx, 1, 1, []types.EntitlementPayout{payout(testAccount(9), "400")}))
	requireReleaseState(t, k, ctx, "400", "600")

	require.NoError(t, k.PayEntitlement(ctx, 1, 1, []types.EntitlementPayout{payout(testAccount(8), "350")}))
	requireReleaseState(t, k, ctx, "750", "250")

	// Exactly to the ceiling.
	require.NoError(t, k.PayEntitlement(ctx, 1, 1, []types.EntitlementPayout{payout(testAccount(7), "250")}))
	requireReleaseState(t, k, ctx, releaseEntitlementAmount, "0")
}

// TestPayEntitlementRefusesOverRelease covers the ceiling from both directions:
// a single set that exceeds it, and a set that only exceeds it cumulatively.
func TestPayEntitlementRefusesOverRelease(t *testing.T) {
	t.Run("inside one payout set", func(t *testing.T) {
		k, ctx, bank := setupRelease(t)
		err := k.PayEntitlement(ctx, 1, 1, []types.EntitlementPayout{
			payout(testAccount(9), "600"),
			payout(testAccount(8), "600"),
		})
		require.ErrorIs(t, err, types.ErrInvalidState)

		// The ceiling is proven for the whole set BEFORE any transfer, so the first
		// line must not have been paid either.
		require.Zero(t, bank.sendCalls, "no line may be paid when the set exceeds the ceiling")
		requireReleaseState(t, k, ctx, "0", releaseEntitlementAmount)
	})

	t.Run("across separate calls", func(t *testing.T) {
		k, ctx, bank := setupRelease(t)
		require.NoError(t, k.PayEntitlement(ctx, 1, 1, []types.EntitlementPayout{payout(testAccount(9), "900")}))
		callsAfterFirst := bank.sendCalls

		err := k.PayEntitlement(ctx, 1, 1, []types.EntitlementPayout{payout(testAccount(8), "101")})
		require.ErrorIs(t, err, types.ErrInvalidState)
		require.Equal(t, callsAfterFirst, bank.sendCalls)
		requireReleaseState(t, k, ctx, "900", "100")
	})

	t.Run("an aggregated duplicate that only exceeds when summed", func(t *testing.T) {
		k, ctx, bank := setupRelease(t)
		recipient := testAccount(9)
		err := k.PayEntitlement(ctx, 1, 1, []types.EntitlementPayout{
			payout(recipient, "600"),
			payout(recipient, "600"),
		})
		require.ErrorIs(t, err, types.ErrInvalidState)
		require.Zero(t, bank.sendCalls)
		requireReleaseState(t, k, ctx, "0", releaseEntitlementAmount)
	})
}

// TestPayEntitlementRejectsMalformedLinesBeforeAnyTransfer is the ordering
// contract: the whole set is admitted before the first send.
func TestPayEntitlementRejectsMalformedLinesBeforeAnyTransfer(t *testing.T) {
	blocked := testAccount(77)
	for _, tc := range []struct {
		name   string
		set    []types.EntitlementPayout
		errIs  error
		reason string
	}{
		{
			name:  "an empty set",
			set:   nil,
			errIs: types.ErrInvalidState,
		},
		{
			name: "a zero participant amount",
			set: []types.EntitlementPayout{
				payout(testAccount(9), "100"),
				payout(testAccount(8), "0"),
			},
			errIs:  types.ErrInvalidState,
			reason: "a zero line authorizes an account touch without moving value",
		},
		{
			name: "a bank-blocked recipient on a later line",
			set: []types.EntitlementPayout{
				payout(testAccount(9), "100"),
				payout(blocked, "100"),
			},
			errIs: types.ErrInvalidAddress,
		},
		{
			name: "a module account recipient",
			set: []types.EntitlementPayout{
				payout(testAccount(9), "100"),
				payout(testModuleAddress(testModuleAccountName), "100"),
			},
			errIs: types.ErrInvalidAddress,
		},
		{
			name: "the zero address",
			set: []types.EntitlementPayout{
				payout(testAccount(9), "100"),
				payout(zeroAddress(), "100"),
			},
			errIs: types.ErrInvalidAddress,
		},
		{
			name: "a malformed recipient",
			set: []types.EntitlementPayout{
				payout(testAccount(9), "100"),
				payout("not-an-address", "100"),
			},
			errIs: types.ErrInvalidAddress,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx, bank := setupRelease(t, blocked)
			err := k.PayEntitlement(ctx, 1, 1, tc.set)
			require.ErrorIs(t, err, tc.errIs, tc.reason)
			require.Zero(t, bank.sendCalls, "validation of the whole set precedes the first transfer")
			requireReleaseState(t, k, ctx, "0", releaseEntitlementAmount)
		})
	}
}

// TestPayEntitlementRequiresCanonicalAmountEncoding is the strict parser.
//
// Two byte strings that mean the same number are two distinct messages on a path
// that moves money, which is a replay and equivocation surface. The general
// amount parser accepts several of these spellings, which is why release does not
// use it.
func TestPayEntitlementRequiresCanonicalAmountEncoding(t *testing.T) {
	for _, encoding := range []string{
		"+7",
		" 7",
		"7 ",
		"7.0",
		"1e3",
		"007",
		"-1",
		"",
		"0x10",
		"1_000",
	} {
		t.Run("rejects "+encoding, func(t *testing.T) {
			k, ctx, bank := setupRelease(t)
			err := k.PayEntitlement(ctx, 1, 1, []types.EntitlementPayout{payout(testAccount(9), encoding)})
			require.ErrorIs(t, err, types.ErrInvalidState)
			require.Zero(t, bank.sendCalls)
			requireReleaseState(t, k, ctx, "0", releaseEntitlementAmount)
		})
	}

	t.Run("accepts the canonical spelling", func(t *testing.T) {
		k, ctx, _ := setupRelease(t)
		require.NoError(t, k.PayEntitlement(ctx, 1, 1, []types.EntitlementPayout{payout(testAccount(9), "7")}))
		requireReleaseState(t, k, ctx, "7", "993")
	})
}

// TestReleaseKeepsAmountsArbitraryPrecision proves a payout above the fixed-width
// range is a legitimate value rather than an overflow.
func TestReleaseKeepsAmountsArbitraryPrecision(t *testing.T) {
	const huge = "340282366920938463463374607431768211456" // 2^128
	k, ctx, bank := setupKeeperWithBlocked(t, &coreSlotKeeperMock{})
	params := rewardConfigParams()
	require.NoError(t, k.SetParams(ctx, params))
	require.NoError(t, k.SetPauseState(ctx, types.RewardsPauseState{}))
	seedRewardConfigTimeline(t, k, ctx, params)
	require.NoError(t, k.SetOutstandingEntitlementLiability(ctx, sdkmath.ZeroInt()))
	require.NoError(t, k.CreateSlotEntitlement(ctx, entitlementFor(1, 1, huge)))

	require.NoError(t, k.PayEntitlement(ctx, 1, 1, []types.EntitlementPayout{payout(testAccount(9), huge)}))
	require.Equal(t, huge, bank.sends[0].amounts[0].Amount.String())

	entitlement, found, err := k.GetSlotEntitlement(ctx, 1, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, huge, entitlement.ReleasedAmount)
	requireLiability(t, k, ctx, "0")
}

// TestReleaseObeysTheCanonicalPause covers both halves of the H+1 rule.
func TestReleaseObeysTheCanonicalPause(t *testing.T) {
	t.Run("an effective pause blocks release", func(t *testing.T) {
		k, ctx, bank := setupRelease(t)
		require.NoError(t, k.SetPauseState(ctx, types.RewardsPauseState{CurrentPaused: true}))

		err := k.PayEntitlement(ctx, 1, 1, []types.EntitlementPayout{payout(testAccount(9), "100")})
		require.ErrorIs(t, err, types.ErrUnsupportedFeature)
		require.Zero(t, bank.sendCalls)
		requireReleaseState(t, k, ctx, "0", releaseEntitlementAmount)

		// And the remainder helper is bound by the same state.
		require.ErrorIs(t, k.PayEntitlementRemainderToOperator(ctx, 1, 1), types.ErrUnsupportedFeature)
		require.Zero(t, bank.sendCalls)
	})

	t.Run("a pause pending for H+1 does not block release in H", func(t *testing.T) {
		k, ctx, _ := setupRelease(t)
		ctx = ctx.WithBlockHeight(10)
		// Accepted in this block, effective at the next.
		require.NoError(t, k.SchedulePauseTransition(ctx, 10, true))

		require.NoError(t, k.PayEntitlement(ctx, 1, 1, []types.EntitlementPayout{payout(testAccount(9), "100")}),
			"a release must not depend on where in the block the pause transaction landed")
		requireReleaseState(t, k, ctx, "100", "900")
	})
}

// TestReleaseIsIndependentOfSlotLifecycle covers §64.
//
// An entitlement is earned by participation in a closed epoch. Re-reading the
// Slot at release time would let a later lifecycle transition confiscate money
// that was already earned.
func TestReleaseIsIndependentOfSlotLifecycle(t *testing.T) {
	for _, status := range []coreslottypes.SlotStatus{
		coreslottypes.SlotStatus_SLOT_STATUS_INACTIVE,
		coreslottypes.SlotStatus_SLOT_STATUS_SUSPENDED,
		coreslottypes.SlotStatus_SLOT_STATUS_REMOVED,
	} {
		t.Run(status.String(), func(t *testing.T) {
			// The CoreSlot mock holds NO slots at all, which is stronger than holding
			// one in the given status: if release consulted live lifecycle state it
			// would fail to resolve the Slot and error, rather than paying.
			k, ctx, _ := setupRelease(t)
			require.NoError(t, k.PayEntitlement(ctx, 1, 1, []types.EntitlementPayout{payout(testAccount(9), "100")}))
			requireReleaseState(t, k, ctx, "100", "900")
			require.NoError(t, k.PayEntitlementRemainderToOperator(ctx, 1, 1))
			requireReleaseState(t, k, ctx, releaseEntitlementAmount, "0")
		})
	}
}

// TestReleaseRollsBackCompletelyOnBankFailure proves the atomicity of the triple.
func TestReleaseRollsBackCompletelyOnBankFailure(t *testing.T) {
	t.Run("a payout set", func(t *testing.T) {
		k, ctx, bank := setupRelease(t)
		bank.failSend()

		err := k.PayEntitlement(ctx, 1, 1, []types.EntitlementPayout{
			payout(testAccount(9), "100"),
			payout(testAccount(8), "200"),
		})
		require.Error(t, err)
		requireReleaseState(t, k, ctx, "0", releaseEntitlementAmount)
	})

	t.Run("the operator remainder", func(t *testing.T) {
		k, ctx, bank := setupRelease(t)
		bank.failSend()

		require.Error(t, k.PayEntitlementRemainderToOperator(ctx, 1, 1))
		requireReleaseState(t, k, ctx, "0", releaseEntitlementAmount)
	})
}

// TestReleaseRollsBackStateOnAccountingFailure is the converse of the bank-error
// case: a failure in the accounting step, after every transfer has succeeded,
// must leave no release standing.
//
// # What this level can and cannot prove
//
// The bank double is a plain Go object. Its call log records a send whether or
// not the surrounding cache commits, because it never writes to the context
// store — so it can show that the transfer was REACHED, but it cannot show that
// the transfer was rolled back. Asserting rollback against a call counter here
// would be asserting something the double is incapable of contradicting.
//
// What is provable at this level is that no rewards state survived, which is
// asserted below. That the bank movement itself unwinds is a property of running
// inside one cache context with a real bank keeper, and is proven at app level in
// the integrated release proof.
func TestReleaseRollsBackStateOnAccountingFailure(t *testing.T) {
	k, ctx, bank := setupRelease(t)

	require.NoError(t, k.OutstandingEntitlementLiability.Set(ctx, "owed"))
	err := k.PayEntitlement(ctx, 1, 1, []types.EntitlementPayout{payout(testAccount(9), "100")})
	require.ErrorIs(t, err, types.ErrInvalidState)

	require.Positive(t, bank.sendCalls,
		"the failure must occur after the transfer, or this proves nothing about ordering")

	entitlement, found, err := k.GetSlotEntitlement(ctx, 1, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "0", entitlement.ReleasedAmount, "no release may survive a failed accounting step")
}

// TestReleaseRefusesAnEntitlementThatDoesNotExist keeps absence from reading as a
// zero-value obligation.
func TestReleaseRefusesAnEntitlementThatDoesNotExist(t *testing.T) {
	k, ctx, bank := setupRelease(t)
	err := k.PayEntitlement(ctx, 4, 9, []types.EntitlementPayout{payout(testAccount(9), "100")})
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Zero(t, bank.sendCalls)

	require.Error(t, k.PayEntitlementRemainderToOperator(ctx, 4, 9))
	require.Zero(t, bank.sendCalls)
}

// TestReleaseFailsClosedOnACorruptEntitlement proves a corrupt record stops a
// payment rather than producing an approximate ceiling.
func TestReleaseFailsClosedOnACorruptEntitlement(t *testing.T) {
	k, ctx, bank := setupRelease(t)
	corrupt := entitlementFor(1, 1, releaseEntitlementAmount)
	corrupt.ReleasedAmount = "1001"
	require.NoError(t, k.SlotEntitlements.Set(ctx, entitlementTestKey(1, 1), corrupt))

	err := k.PayEntitlement(ctx, 1, 1, []types.EntitlementPayout{payout(testAccount(9), "1")})
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Zero(t, bank.sendCalls)

	require.ErrorIs(t, k.PayEntitlementRemainderToOperator(ctx, 1, 1), types.ErrInvalidState)
	require.Zero(t, bank.sendCalls)
}

// TestOperatorRemainderPaysTheImmutableSnapshot is the destination rule.
func TestOperatorRemainderPaysTheImmutableSnapshot(t *testing.T) {
	k, ctx, bank := setupRelease(t)
	entitlement, found, err := k.GetSlotEntitlement(ctx, 1, 1)
	require.NoError(t, err)
	require.True(t, found)

	require.NoError(t, k.PayEntitlement(ctx, 1, 1, []types.EntitlementPayout{payout(testAccount(9), "400")}))
	require.NoError(t, k.PayEntitlementRemainderToOperator(ctx, 1, 1))

	last := bank.sends[len(bank.sends)-1]
	require.Equal(t, entitlement.PayoutAddress, last.recipient.String(),
		"the remainder goes to the entitlement's immutable snapshot")
	require.Equal(t, "600", last.amounts[0].Amount.String())
	requireReleaseState(t, k, ctx, releaseEntitlementAmount, "0")
}

// TestOperatorRemainderOfZeroPerformsNoBankCall is §34's deterministic no-op.
//
// Asserted on the CALL count rather than on balances: a zero-value send would be
// invisible in a balance comparison while still touching, and potentially
// creating, the destination account.
func TestOperatorRemainderOfZeroPerformsNoBankCall(t *testing.T) {
	k, ctx, bank := setupRelease(t)
	require.NoError(t, k.PayEntitlement(ctx, 1, 1, []types.EntitlementPayout{
		payout(testAccount(9), releaseEntitlementAmount),
	}))
	callsAfterFull := bank.sendCalls
	requireReleaseState(t, k, ctx, releaseEntitlementAmount, "0")

	// Fully released: the remainder is zero.
	require.NoError(t, k.PayEntitlementRemainderToOperator(ctx, 1, 1))
	require.Equal(t, callsAfterFull, bank.sendCalls, "a zero remainder must issue no bank send")
	requireReleaseState(t, k, ctx, releaseEntitlementAmount, "0")

	// And it stays a no-op however many times it is called, so a settlement can be
	// finalized without creating an immortal zero-value obligation.
	require.NoError(t, k.PayEntitlementRemainderToOperator(ctx, 1, 1))
	require.Equal(t, callsAfterFull, bank.sendCalls)
	requireReleaseState(t, k, ctx, releaseEntitlementAmount, "0")
}

// TestOperatorRemainderRevalidatesTheDestinationBeforeTransfer covers the
// fail-closed rule for a snapshot that has since become unusable.
//
// The destination was admissible when it was snapshotted. If it stops being so,
// the release fails rather than redirecting: §32 forbids silently sending the
// money somewhere else, and INV-07 forbids a caller substituting a recipient.
func TestOperatorRemainderRevalidatesTheDestinationBeforeTransfer(t *testing.T) {
	entitlement := entitlementFor(1, 1, releaseEntitlementAmount)

	// The same address is blocked only in the second keeper, so the entitlement is
	// created under a validator that admits it and released under one that does not.
	k, ctx, bank := setupKeeperWithBlocked(t, &coreSlotKeeperMock{}, entitlement.PayoutAddress)
	params := rewardConfigParams()
	require.NoError(t, k.SetParams(ctx, params))
	require.NoError(t, k.SetPauseState(ctx, types.RewardsPauseState{}))
	seedRewardConfigTimeline(t, k, ctx, params)
	require.NoError(t, k.SetOutstandingEntitlementLiability(ctx, sdkmath.ZeroInt()))
	// Written straight to the store: creation would refuse the destination, which
	// is the correct behavior and not what this test is about.
	require.NoError(t, k.SlotEntitlements.Set(ctx, entitlementTestKey(1, 1), entitlement))
	require.NoError(t, k.SetOutstandingEntitlementLiability(ctx, sdkmath.NewInt(1000)))

	require.ErrorIs(t, k.PayEntitlementRemainderToOperator(ctx, 1, 1), types.ErrInvalidAddress)
	require.Zero(t, bank.sendCalls, "an unusable snapshot fails closed rather than redirecting")
	requireReleaseState(t, k, ctx, "0", releaseEntitlementAmount)
}

// TestReleaseHasNoPublicMessageOrCLISurface is the structural half of "keeper
// only".
//
// The signature check is the substantive one: PayEntitlementRemainderToOperator
// takes only an obligation identity, so a caller-chosen remainder destination is
// not merely rejected, it cannot be expressed. A rejection path could be relaxed
// by a later edit; a missing parameter cannot.
func TestReleaseHasNoPublicMessageOrCLISurface(t *testing.T) {
	// Compile-time: the remainder helper takes an obligation identity and nothing
	// else. Adding a recipient parameter would fail this assignment rather than
	// merely failing a runtime rejection test.
	var remainder func(context.Context, uint64, uint64) error
	k, _, _ := setupRelease(t)
	remainder = k.PayEntitlementRemainderToOperator
	require.NotNil(t, remainder)

	// The rewards Msg service exposes exactly three methods. No payout message was
	// ever added beside them, and the claim message that used to sit here was
	// retired once Settlement became the only way entitlement value leaves escrow —
	// so this now proves both that nothing was added and that the legacy path is
	// genuinely gone rather than merely unused.
	msgServer := reflect.TypeOf((*types.MsgServer)(nil)).Elem()
	methods := make([]string, 0, msgServer.NumMethod())
	for i := range msgServer.NumMethod() {
		methods = append(methods, msgServer.Method(i).Name)
	}
	require.ElementsMatch(t,
		[]string{"UpdateRewardsParams", "PauseRewards", "ResumeRewards"},
		methods,
		"release is keeper-only: no payout message may exist, and the retired claim "+
			"message must not have survived")
}
