package app_test

import (
	"testing"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/app"
	appparams "github.com/twilight-project/twilight-core/app/params"
	rewardstypes "github.com/twilight-project/twilight-core/x/rewards/types"
)

// The integrated economic lifecycle, end to end, against the real application.
//
// Everything below runs on a booted app with the real bank keeper, the real
// module account and real block transitions. That is the point: the keeper tests
// use a double that records calls without moving balances, so the property they
// cannot establish — that bank movement and rewards accounting commit or unwind
// together — is established here.
//
// The lifecycle proven is:
//
//	real epoch finalization
//	  -> canonical RewardConfig binding
//	  -> SlotEntitlement creation
//	  -> outstanding liability increase
//	  -> keeper-level release
//	  -> real participant bank balance movement
//	  -> released_amount increase
//	  -> liability decrease by the same value

// TestIntegratedFinalizationEntitlementAndRelease is Tranche 1's internal money
// proof.
//
// It is NOT the public POC money-movement proof. Release is keeper-only by
// design, so nothing here is reachable by transaction; the public proof arrives
// with Settlement, which calls the same boundary.
func TestIntegratedFinalizationEntitlementAndRelease(t *testing.T) {
	a := bootApp(t)
	base := a.NewUncachedContext(false, cmtproto.Header{Height: 1})

	pay1, pay2 := acc(12), acc(13)
	params, snapshot := rewardsParams(t, func(p *rewardstypes.Params) {
		p.InitialBlockSubsidy = "100"
		p.EpochLengthBlocks = appparams.HardMinEpochLengthBlocks
	})
	initCoreSlotsAndRewards(t, a, base, []slotSpec{
		{id: 1, operator: acc(2), payout: pay1, keyMarker: 1},
		{id: 2, operator: acc(3), payout: pay2, keyMarker: 2},
	}, genesisState(params, snapshot))

	epochLen := int64(appparams.HardMinEpochLengthBlocks)
	ctx := driveBlocks(t, a, base, 1, epochLen)

	// --- Finalization produced the canonical obligations, and only those. ---
	emission := epochLen * 100
	epoch, found, err := a.RewardsKeeper.GetFinalizedEpoch(ctx, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, intToString(emission), epoch.MintedEmission)
	require.Empty(t, epoch.Rewards, "the aggregate embeds no per-Slot rows")

	entitlements, err := a.RewardsKeeper.IterateEntitlementsForEpoch(ctx, 1)
	require.NoError(t, err)
	require.Len(t, entitlements, 2)
	require.Equal(t, []uint64{1, 2}, []uint64{entitlements[0].SlotId, entitlements[1].SlotId})
	for _, entitlement := range entitlements {
		require.Equal(t, uint64(1), entitlement.RewardConfigVersion,
			"epoch 1 bootstraps to the genesis reward configuration")
		require.Equal(t, "0", entitlement.ReleasedAmount)
	}
	perSlot := entitlements[0].EntitlementAmount

	// No claim record exists for the epoch that produced them.

	// --- Escrow solvency, against the real module account. ---
	escrow := a.AccountKeeper.GetModuleAddress(rewardstypes.ModuleName)
	liability, err := a.RewardsKeeper.GetOutstandingEntitlementLiability(ctx)
	require.NoError(t, err)
	state, err := a.RewardsKeeper.GetState(ctx)
	require.NoError(t, err)
	carry, ok := parseAmount(state.CarryForwardRemainder)
	require.True(t, ok)
	require.Equal(t,
		a.BankKeeper.GetBalance(ctx, escrow, app.BaseDenom).Amount.String(),
		liability.Add(carry).String(),
		"escrow must equal outstanding liability plus carry")

	// --- A participant payout set through the constrained boundary. ---
	participant := acc(80)
	before := snapshotBalances(t, a, ctx, escrow, mustAddr(t, participant))
	require.NoError(t, a.RewardsKeeper.PayEntitlement(ctx, 1, 1, []rewardstypes.EntitlementPayout{
		{Recipient: participant, Amount: "150"},
	}))

	after := snapshotBalances(t, a, ctx, escrow, mustAddr(t, participant))
	require.Equal(t, "150", after.recipient.Sub(before.recipient).String(),
		"the participant received exactly the payout")
	require.Equal(t, "150", before.escrow.Sub(after.escrow).String(),
		"escrow fell by exactly the payout")

	released, found, err := a.RewardsKeeper.GetSlotEntitlement(ctx, 1, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "150", released.ReleasedAmount)

	liabilityAfter, err := a.RewardsKeeper.GetOutstandingEntitlementLiability(ctx)
	require.NoError(t, err)
	require.Equal(t, "150", liability.Sub(liabilityAfter).String(),
		"the liability fell by exactly the payout")

	// --- The operator remainder, to the immutable snapshot. ---
	require.NoError(t, a.RewardsKeeper.PayEntitlementRemainderToOperator(ctx, 1, 1))
	final, found, err := a.RewardsKeeper.GetSlotEntitlement(ctx, 1, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, perSlot, final.ReleasedAmount, "the obligation is fully released")
	require.Equal(t, perSlot,
		addAmounts(t,
			a.BankKeeper.GetBalance(ctx, mustAddr(t, pay1), app.BaseDenom).Amount.String(),
			"150").String(),
		"the payout snapshot plus the participant account hold the whole entitlement")

	// Solvency still holds after release.
	liabilityFinal, err := a.RewardsKeeper.GetOutstandingEntitlementLiability(ctx)
	require.NoError(t, err)
	require.Equal(t,
		a.BankKeeper.GetBalance(ctx, escrow, app.BaseDenom).Amount.String(),
		liabilityFinal.Add(carry).String())
	assertInvariants(t, a, ctx)
}

// TestReleaseRollsBackRealBankMovement is the property the keeper double cannot
// establish.
//
// A release whose accounting step fails after every transfer has succeeded must
// leave the real bank balances exactly where they were. The keeper tests can only
// show that no rewards state survived; only a real bank keeper writing into the
// context store can show that the transfer itself unwound.
func TestReleaseRollsBackRealBankMovement(t *testing.T) {
	a := bootApp(t)
	base := a.NewUncachedContext(false, cmtproto.Header{Height: 1})

	params, snapshot := rewardsParams(t, func(p *rewardstypes.Params) {
		p.InitialBlockSubsidy = "100"
		p.EpochLengthBlocks = appparams.HardMinEpochLengthBlocks
	})
	initCoreSlotsAndRewards(t, a, base, []slotSpec{
		{id: 1, operator: acc(2), payout: acc(12), keyMarker: 1},
	}, genesisState(params, snapshot))
	ctx := driveBlocks(t, a, base, 1, int64(appparams.HardMinEpochLengthBlocks))

	escrow := a.AccountKeeper.GetModuleAddress(rewardstypes.ModuleName)
	participant := mustAddr(t, acc(80))
	before := snapshotBalances(t, a, ctx, escrow, participant)

	// Corrupt the accumulator so the accounting step fails AFTER the transfer has
	// already been made inside the cache.
	require.NoError(t, a.RewardsKeeper.OutstandingEntitlementLiability.Set(ctx, "owed"))

	err := a.RewardsKeeper.PayEntitlement(ctx, 1, 1, []rewardstypes.EntitlementPayout{
		{Recipient: acc(80), Amount: "150"},
	})
	require.Error(t, err)

	after := snapshotBalances(t, a, ctx, escrow, participant)
	require.Equal(t, before.escrow.String(), after.escrow.String(),
		"a failed release must leave escrow untouched")
	require.Equal(t, before.recipient.String(), after.recipient.String(),
		"a failed release must pay nobody")

	entitlement, found, err := a.RewardsKeeper.GetSlotEntitlement(ctx, 1, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "0", entitlement.ReleasedAmount)
}

// TestRewardsEscrowIsABlockedTransferDestination protects the assumption behind
// the solvency equality.
//
// Finalization asserts escrow == liability + carry exactly. That is only safe
// while nobody can push funds into the escrow from outside: an external deposit
// would make a correct chain fail the assertion and halt. The bank module blocks
// module accounts as ordinary transfer destinations by default, and this pins
// that the rewards account is in fact among them.
func TestRewardsEscrowIsABlockedTransferDestination(t *testing.T) {
	a := bootApp(t)
	escrow := a.AccountKeeper.GetModuleAddress(rewardstypes.ModuleName)
	require.NotNil(t, escrow)
	require.True(t, a.BankKeeper.BlockedAddr(escrow),
		"the rewards escrow must be unreachable by ordinary transfers, or the "+
			"finalization solvency equality becomes a liveness trap")
}

// parseAmount decodes a canonical base-denom amount.
func parseAmount(value string) (math.Int, bool) {
	return math.NewIntFromString(value)
}

func addAmounts(t *testing.T, a, b string) math.Int {
	t.Helper()
	left, ok := math.NewIntFromString(a)
	require.True(t, ok)
	right, ok := math.NewIntFromString(b)
	require.True(t, ok)
	return left.Add(right)
}

func intToString(v int64) string {
	return math.NewInt(v).String()
}

type balancePair struct {
	escrow    math.Int
	recipient math.Int
}

func snapshotBalances(t *testing.T, a *app.App, ctx sdk.Context, escrow, recipient sdk.AccAddress) balancePair {
	t.Helper()
	return balancePair{
		escrow:    a.BankKeeper.GetBalance(ctx, escrow, app.BaseDenom).Amount,
		recipient: a.BankKeeper.GetBalance(ctx, recipient, app.BaseDenom).Amount,
	}
}
