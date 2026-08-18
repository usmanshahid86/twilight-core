package app_test

import (
	"bytes"
	"sort"
	"testing"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"
	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/app"
	appparams "github.com/twilight-project/twilight-core/app/params"
	miningkeeper "github.com/twilight-project/twilight-core/x/mining/keeper"
	miningtypes "github.com/twilight-project/twilight-core/x/mining/types"
	rewardstypes "github.com/twilight-project/twilight-core/x/rewards/types"
)

// Settlement against the real application.
//
// This is the money proof for participant distribution. The x/mining keeper tests
// use a rewards double that records releases without moving balances, so the one
// property they cannot establish is the one that matters most: that a participant's
// bank balance actually rises, that escrow falls by exactly the same value, and
// that a failure unwinds both together.
//
// Everything below runs on a booted app with the real bank keeper, the real module
// account, the real rewards release boundary and real block transitions.

// initMining seeds the mining module on an app whose CoreSlot and rewards state is
// already initialized.
//
// The order is not incidental: mining's InitGenesis cross-checks every ACTIVE
// CoreSlot's Selection policy against the initial Selection parameters, so CoreSlot
// state must already be present — which is exactly why the app's InitGenesis order
// places mining last.
func initMining(t *testing.T, a *app.App, base sdk.Context) {
	t.Helper()
	require.NoError(t, a.MiningKeeper.InitGenesis(base, *miningtypes.DefaultGenesis()))
}

// driveSettlementBlock runs one block in the app's resolved dispatch order,
// including mining: BeginBlockers=[rewards], EndBlockers=[coreslot, rewards, mining].
//
// Mining runs last because materialization must observe the epoch x/rewards
// finalized in this same block.
func driveSettlementBlock(t *testing.T, a *app.App, base sdk.Context, height int64) sdk.Context {
	t.Helper()
	ctx := base.WithBlockHeight(height)
	require.NoError(t, a.RewardsKeeper.BeginBlock(ctx))
	_, err := a.CoreSlotKeeper.EndBlock(ctx)
	require.NoError(t, err)
	require.NoError(t, a.RewardsKeeper.EndBlock(ctx))
	require.NoError(t, a.MiningKeeper.EndBlock(ctx))
	return ctx
}

func driveSettlementBlocks(t *testing.T, a *app.App, base sdk.Context, from, count int64) sdk.Context {
	t.Helper()
	ctx := base.WithBlockHeight(from)
	for height := from; height < from+count; height++ {
		ctx = driveSettlementBlock(t, a, base, height)
	}
	return ctx
}

// settlementEnv is one finalized epoch with a materialized settlement, on a real app.
type settlementEnv struct {
	app        *app.App
	ctx        sdk.Context
	msgServer  miningtypes.MsgServer
	settlement string
	payout     string
	slotID     uint64
	epoch      uint64
}

func bootSettlement(t *testing.T) settlementEnv {
	t.Helper()
	a := bootApp(t)
	base := a.NewUncachedContext(false, cmtproto.Header{Height: 1})

	payout, credential := acc(12), acc(40)
	params, snapshot := rewardsParams(t, func(p *rewardstypes.Params) {
		p.InitialBlockSubsidy = "100000"
		p.EpochLengthBlocks = appparams.HardMinEpochLengthBlocks
	})
	initCoreSlotsAndRewards(t, a, base, []slotSpec{
		{id: 1, operator: acc(2), payout: payout, keyMarker: 1, settlement: credential},
	}, genesisState(params, snapshot))
	initMining(t, a, base)

	ctx := driveSettlementBlocks(t, a, base, 1, int64(appparams.HardMinEpochLengthBlocks))

	// Consensus materialized the settlement. No transaction can open one: there is
	// no such message.
	settlement, found, err := a.MiningKeeper.GetSettlement(ctx, 1, 1)
	require.NoError(t, err)
	require.True(t, found, "closing epoch 1 must materialize a settlement")
	require.Equal(t, miningtypes.SettlementMode_SETTLEMENT_MODE_TRUSTED_AS, settlement.SettlementMode)
	require.Zero(t, settlement.NextChunkIndex)
	require.False(t, settlement.Finalized)

	return settlementEnv{
		app: a, ctx: ctx, msgServer: miningkeeper.NewMsgServer(a.MiningKeeper),
		settlement: credential, payout: payout, slotID: 1, epoch: 1,
	}
}

func (e settlementEnv) balance(t *testing.T, address string) sdkmath.Int {
	t.Helper()
	return e.app.BankKeeper.GetBalance(e.ctx, mustAddr(t, address), appparams.NativeBaseDenom).Amount
}

func (e settlementEnv) escrow(t *testing.T) sdkmath.Int {
	t.Helper()
	module := e.app.AccountKeeper.GetModuleAddress(rewardstypes.ModuleName)
	return e.app.BankKeeper.GetBalance(e.ctx, module, appparams.NativeBaseDenom).Amount
}

func (e settlementEnv) entitlement(t *testing.T) rewardstypes.SlotEntitlement {
	t.Helper()
	owed, found, err := e.app.RewardsKeeper.GetSlotEntitlement(e.ctx, e.slotID, e.epoch)
	require.NoError(t, err)
	require.True(t, found)
	return owed
}

func (e settlementEnv) chunk(index uint64, lines ...*miningtypes.SettlementChunkPayout) *miningtypes.MsgSubmitSettlementChunk {
	return &miningtypes.MsgSubmitSettlementChunk{
		SettlementAddress: e.settlement,
		SlotId:            e.slotID,
		Epoch:             e.epoch,
		ChunkIndex:        index,
		Payouts:           lines,
	}
}

func payoutLine(recipient, amount string) *miningtypes.SettlementChunkPayout {
	return &miningtypes.SettlementChunkPayout{Recipient: recipient, Amount: amount}
}

// ascendingAccounts orders addresses the way chunk admission does: by DECODED
// address bytes.
//
// Bech32 string order is not byte order — the encoding's checksum and charset make
// the two disagree — so a fixture that sorted the strings would produce chunks the
// chain rejects. That divergence is the reason the rule is stated in bytes.
func ascendingAccounts(t *testing.T, addresses ...string) []string {
	t.Helper()
	ordered := append([]string(nil), addresses...)
	sort.Slice(ordered, func(i, j int) bool {
		return bytes.Compare(mustAddr(t, ordered[i]), mustAddr(t, ordered[j])) < 0
	})
	return ordered
}

// TestSettlementChunkMovesRealParticipantBalances is the POC money-movement proof.
//
// A settlement materialized by consensus, a chunk submitted through the registered
// message service by the Slot's settlement credential, and real coins arriving in
// real participant accounts — with escrow falling and the outstanding liability
// falling by exactly the same value.
func TestSettlementChunkMovesRealParticipantBalances(t *testing.T) {
	e := bootSettlement(t)

	// Two participants, ordered by address bytes as admission requires.
	ordered := ascendingAccounts(t, acc(0x51), acc(0x52))
	first, second := ordered[0], ordered[1]
	require.True(t, e.balance(t, first).IsZero())
	require.True(t, e.balance(t, second).IsZero())

	escrowBefore := e.escrow(t)
	liabilityBefore, err := e.app.RewardsKeeper.GetOutstandingEntitlementLiability(e.ctx)
	require.NoError(t, err)

	response, err := e.msgServer.SubmitSettlementChunk(e.ctx, e.chunk(0,
		payoutLine(first, "400000"),
		payoutLine(second, "600000"),
	))
	require.NoError(t, err)
	require.Equal(t, uint64(1), response.NextChunkIndex)

	moved := sdkmath.NewInt(1_000_000)
	require.Equal(t, "400000", e.balance(t, first).String(), "a real participant balance rose")
	require.Equal(t, "600000", e.balance(t, second).String())
	require.Equal(t, escrowBefore.Sub(moved).String(), e.escrow(t).String(),
		"escrow fell by exactly what was released")

	liabilityAfter, err := e.app.RewardsKeeper.GetOutstandingEntitlementLiability(e.ctx)
	require.NoError(t, err)
	require.Equal(t, liabilityBefore.Sub(moved).String(), liabilityAfter.String(),
		"and the outstanding obligation fell by the same value")

	// The released amount is the ENTITLEMENT's. x/mining stores none.
	require.Equal(t, "1000000", e.entitlement(t).ReleasedAmount)
	settlement, _, err := e.app.MiningKeeper.GetSettlement(e.ctx, 1, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(1), settlement.NextChunkIndex)

	assertInvariants(t, e.app, e.ctx)
}

// TestSettlementChunkRollsBackEveryTransferWhenTheBankRefuses is the atomicity
// proof that only a real bank can give.
//
// Escrow is deliberately drained below what the chunk requires, so the FIRST
// transfer succeeds and a later one fails. Both ceilings pass — this is not a
// ceiling test — and what must hold is that the succeeded transfer is unwound, the
// released amount does not move, and the chunk cursor does not advance.
func TestSettlementChunkRollsBackEveryTransferWhenTheBankRefuses(t *testing.T) {
	e := bootSettlement(t)
	ordered := ascendingAccounts(t, acc(0x61), acc(0x62))
	first, second := ordered[0], ordered[1]

	// Drain escrow so that it can cover the first line but not both. This is fault
	// injection: it deliberately breaks the escrow/liability equality that consensus
	// otherwise maintains, in order to reach a bank failure partway through a set.
	owed := e.entitlement(t)
	amount, err := owed.Amount()
	require.NoError(t, err)
	drain := e.escrow(t).Sub(amount.QuoRaw(2))
	require.NoError(t, e.app.BankKeeper.SendCoinsFromModuleToAccount(
		e.ctx, rewardstypes.ModuleName, mustAddr(t, acc(0x7f)),
		sdk.NewCoins(sdk.NewCoin(appparams.NativeBaseDenom, drain))))

	escrowBefore := e.escrow(t)
	half := amount.QuoRaw(2)

	_, err = e.msgServer.SubmitSettlementChunk(e.ctx, e.chunk(0,
		payoutLine(first, half.String()),
		payoutLine(second, half.String()),
	))
	require.Error(t, err, "the second transfer cannot be funded")

	require.True(t, e.balance(t, first).IsZero(), "the first transfer was unwound")
	require.True(t, e.balance(t, second).IsZero())
	require.Equal(t, escrowBefore.String(), e.escrow(t).String(), "escrow is untouched")
	require.Equal(t, "0", e.entitlement(t).ReleasedAmount)

	settlement, _, err := e.app.MiningKeeper.GetSettlement(e.ctx, 1, 1)
	require.NoError(t, err)
	require.Zero(t, settlement.NextChunkIndex, "and the cursor did not advance")
}

// TestSettlementChunkIsRefusedByTheRealReleaseBoundaryAboveTheEntitlement proves
// the two ceilings are layered rather than alternatives.
//
// x/mining refuses this chunk against the settlement's derived participant ceiling.
// x/rewards refuses the same shape independently against the escrow it owns, which
// is proven directly against the real boundary by TestPayEntitlementRefusesOverRelease
// — that separation is why a defect in x/mining could not widen what leaves escrow.
func TestSettlementChunkIsRefusedByTheRealReleaseBoundaryAboveTheEntitlement(t *testing.T) {
	e := bootSettlement(t)
	recipient := acc(0x71)

	owed := e.entitlement(t)
	amount, err := owed.Amount()
	require.NoError(t, err)

	escrowBefore := e.escrow(t)
	_, err = e.msgServer.SubmitSettlementChunk(e.ctx, e.chunk(0,
		payoutLine(recipient, amount.AddRaw(1).String())))
	require.ErrorIs(t, err, miningtypes.ErrInvalidState)

	require.True(t, e.balance(t, recipient).IsZero())
	require.Equal(t, escrowBefore.String(), e.escrow(t).String())
	assertInvariants(t, e.app, e.ctx)
}

// TestSettlementChunkRequiresTheSlotsOwnSettlementCredential proves the credential
// is a distinct identity on the real app.
//
// The Slot's payout address is a different account from its settlement address here
// deliberately: an operator's earnings destination must not be able to authorize
// participant distribution, so that rotating one does not implicate the other.
func TestSettlementChunkRequiresTheSlotsOwnSettlementCredential(t *testing.T) {
	e := bootSettlement(t)
	require.NotEqual(t, e.payout, e.settlement, "the fixture must separate the two")

	impostor := e.chunk(0, payoutLine(acc(0x72), "50000"))
	impostor.SettlementAddress = e.payout

	_, err := e.msgServer.SubmitSettlementChunk(e.ctx, impostor)
	require.ErrorIs(t, err, miningtypes.ErrInvalidAddress)

	settlement, _, err := e.app.MiningKeeper.GetSettlement(e.ctx, 1, 1)
	require.NoError(t, err)
	require.Zero(t, settlement.NextChunkIndex)
	assertInvariants(t, e.app, e.ctx)
}

// TestSettlementChunksAcrossTheWholeEntitlementConserveValue is the conservation
// property over a full multi-chunk distribution.
//
// Participant releases sum to exactly what left escrow, and the entitlement's
// unreleased remainder is exactly what is still owed to the operator.
func TestSettlementChunksAcrossTheWholeEntitlementConserveValue(t *testing.T) {
	e := bootSettlement(t)
	owed := e.entitlement(t)
	amount, err := owed.Amount()
	require.NoError(t, err)

	escrowBefore := e.escrow(t)
	quarter := amount.QuoRaw(4)
	distributed := sdkmath.ZeroInt()

	for index := uint64(0); index < 3; index++ {
		recipient := acc(byte(0x80 + index))
		_, err := e.msgServer.SubmitSettlementChunk(e.ctx,
			e.chunk(index, payoutLine(recipient, quarter.String())))
		require.NoErrorf(t, err, "chunk %d", index)
		require.Equal(t, quarter.String(), e.balance(t, recipient).String())
		distributed = distributed.Add(quarter)
	}

	require.Equal(t, distributed.String(), e.entitlement(t).ReleasedAmount)
	require.Equal(t, escrowBefore.Sub(distributed).String(), e.escrow(t).String())

	remaining, err := e.entitlement(t).Remaining()
	require.NoError(t, err)
	require.Equal(t, amount.Sub(distributed).String(), remaining.String(),
		"what is left is the operator remainder, and no chunk can reach it")

	assertInvariants(t, e.app, e.ctx)
}

// TestAFutureAnchorBlocksReleaseOnTheRealBank is the real-bank half of the
// temporal-integrity rule.
//
// The keeper test proves the release boundary is never called. Only a real bank
// can prove what that means in money: escrow untouched and no participant balance
// created. A future anchor extends the participant window, so without the check
// this chunk would be admitted and paid.
func TestAFutureAnchorBlocksReleaseOnTheRealBank(t *testing.T) {
	e := bootSettlement(t)
	recipient := acc(0x73)

	clock, err := e.app.MiningKeeper.GetSettlementClock(e.ctx)
	require.NoError(t, err)
	anchor, found, err := e.app.MiningKeeper.GetSettlementEpochAnchor(e.ctx, e.epoch)
	require.NoError(t, err)
	require.True(t, found)
	require.LessOrEqual(t, anchor.CreatedSettlementClock, clock)

	anchor.CreatedSettlementClock = clock + 5_000
	require.NoError(t, e.app.MiningKeeper.SettlementEpochAnchors.Set(e.ctx, e.epoch, anchor))

	escrowBefore := e.escrow(t)
	_, err = e.msgServer.SubmitSettlementChunk(e.ctx, e.chunk(0, payoutLine(recipient, "50000")))
	require.ErrorIs(t, err, miningtypes.ErrInvalidState)
	require.Contains(t, err.Error(), "ahead of the canonical clock")

	require.True(t, e.balance(t, recipient).IsZero(), "no participant balance was created")
	require.Equal(t, escrowBefore.String(), e.escrow(t).String(), "escrow is untouched")
	require.Equal(t, "0", e.entitlement(t).ReleasedAmount)

	settlement, _, err := e.app.MiningKeeper.GetSettlement(e.ctx, e.slotID, e.epoch)
	require.NoError(t, err)
	require.Zero(t, settlement.NextChunkIndex)
	assertInvariants(t, e.app, e.ctx)
}

// Settlement finalization against the real application.
//
// The keeper tests establish the authorization model against a double that records
// releases without moving balances. What only a real bank can establish is where
// the operator remainder actually lands, that a permissionless caller receives
// nothing for triggering it, that a zero remainder performs no bank operation at
// all, and that a failure after a successful transfer unwinds the money.

func (e settlementEnv) finalize(signer string) *miningtypes.MsgFinalizeSettlement {
	return &miningtypes.MsgFinalizeSettlement{Signer: signer, SlotId: e.slotID, Epoch: e.epoch}
}

// atDeadline moves the canonical settlement clock to the settlement's derived
// deadline, through the same clock consensus maintains.
func atDeadline(t *testing.T, e settlementEnv) {
	t.Helper()
	settlement, found, err := e.app.MiningKeeper.GetSettlement(e.ctx, e.slotID, e.epoch)
	require.NoError(t, err)
	require.True(t, found)
	deadline, err := e.app.MiningKeeper.SettlementDeadlineClock(e.ctx, settlement)
	require.NoError(t, err)
	require.NoError(t, e.app.MiningKeeper.SettlementClock.Set(e.ctx, deadline))
}

// TestFinalizationPaysTheOperatorRemainderOnTheRealBank is §22A.
//
// Part of the entitlement went to a participant; the rest must reach the operator's
// immutable payout snapshot and nowhere else, with escrow falling by exactly the
// whole entitlement across both movements.
func TestFinalizationPaysTheOperatorRemainderOnTheRealBank(t *testing.T) {
	e := bootSettlement(t)
	participant := acc(0x81)

	owed := e.entitlement(t)
	amount, err := owed.Amount()
	require.NoError(t, err)
	require.Equal(t, e.payout, owed.PayoutAddress, "the operator destination is the payout snapshot")

	escrowBefore := e.escrow(t)
	operatorBefore := e.balance(t, e.payout)
	distributed := amount.QuoRaw(4)

	_, err = e.msgServer.SubmitSettlementChunk(e.ctx,
		e.chunk(0, payoutLine(participant, distributed.String())))
	require.NoError(t, err)

	response, err := e.msgServer.FinalizeSettlement(e.ctx, e.finalize(e.settlement))
	require.NoError(t, err)
	require.Equal(t,
		miningtypes.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_AUTHORIZED_EARLY,
		response.FinalizationReason)

	remainder := amount.Sub(distributed)
	require.Equal(t, remainder.String(), response.ReleasedRemainder)
	require.Equal(t, distributed.String(), e.balance(t, participant).String(),
		"the participant keeps what it was paid")
	require.Equal(t, operatorBefore.Add(remainder).String(), e.balance(t, e.payout).String(),
		"the operator receives exactly the remainder")
	require.Equal(t, escrowBefore.Sub(amount).String(), e.escrow(t).String(),
		"escrow fell by the whole entitlement across both movements")
	require.Equal(t, amount.String(), e.entitlement(t).ReleasedAmount)

	settlement, _, err := e.app.MiningKeeper.GetSettlement(e.ctx, e.slotID, e.epoch)
	require.NoError(t, err)
	require.True(t, settlement.Finalized)
	require.Equal(t, uint64(e.ctx.BlockHeight()), settlement.FinalizedHeight)
	assertInvariants(t, e.app, e.ctx)
}

// TestFinalizingAFullyDistributedEntitlementMovesNothing is §22B.
//
// The entitlement was distributed in full to participants, so the operator is owed
// nothing. Finalization still happens, and it must perform no bank operation — a
// zero-value transfer would touch an account for no reason.
func TestFinalizingAFullyDistributedEntitlementMovesNothing(t *testing.T) {
	e := bootSettlement(t)
	participant := acc(0x82)

	owed := e.entitlement(t)
	amount, err := owed.Amount()
	require.NoError(t, err)
	_, err = e.msgServer.SubmitSettlementChunk(e.ctx,
		e.chunk(0, payoutLine(participant, amount.String())))
	require.NoError(t, err)

	escrowBefore := e.escrow(t)
	operatorBefore := e.balance(t, e.payout)
	operatorAccountBefore := e.app.AccountKeeper.GetAccount(e.ctx, mustAddr(t, e.payout))

	response, err := e.msgServer.FinalizeSettlement(e.ctx, e.finalize(e.settlement))
	require.NoError(t, err)
	require.Equal(t, "0", response.ReleasedRemainder)

	require.Equal(t, escrowBefore.String(), e.escrow(t).String(), "escrow is untouched")
	require.Equal(t, operatorBefore.String(), e.balance(t, e.payout).String())
	require.Equal(t, operatorAccountBefore, e.app.AccountKeeper.GetAccount(e.ctx, mustAddr(t, e.payout)),
		"no account side effect from a zero remainder")

	settlement, _, err := e.app.MiningKeeper.GetSettlement(e.ctx, e.slotID, e.epoch)
	require.NoError(t, err)
	require.True(t, settlement.Finalized)
	assertInvariants(t, e.app, e.ctx)
}

// TestPermissionlessFinalizationPaysTheOperatorAndNotTheCaller is §22C.
//
// Past the deadline anyone may trigger the transition. The point of the test is
// what the trigger does NOT buy: the caller's balance is unchanged and the money
// goes only to the immutable payout snapshot.
func TestPermissionlessFinalizationPaysTheOperatorAndNotTheCaller(t *testing.T) {
	e := bootSettlement(t)
	caller := acc(0x83)
	atDeadline(t, e)

	owed := e.entitlement(t)
	amount, err := owed.Amount()
	require.NoError(t, err)
	operatorBefore := e.balance(t, e.payout)
	require.True(t, e.balance(t, caller).IsZero())

	response, err := e.msgServer.FinalizeSettlement(e.ctx, e.finalize(caller))
	require.NoError(t, err)
	require.Equal(t,
		miningtypes.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_PERMISSIONLESS_AFTER_DEADLINE,
		response.FinalizationReason)

	require.Equal(t, operatorBefore.Add(amount).String(), e.balance(t, e.payout).String(),
		"the whole remainder reached the operator")
	require.True(t, e.balance(t, caller).IsZero(),
		"triggering finalization pays the caller nothing")
	assertInvariants(t, e.app, e.ctx)
}

// TestOperatorOnlyFinalizationPaysTheOperatorInFull is §22D.
//
// The mode is reached through the keeper-level fixture seam rather than by adding a
// message that could produce it, and no Selection state is involved.
func TestOperatorOnlyFinalizationPaysTheOperatorInFull(t *testing.T) {
	e := bootSettlement(t)
	caller := acc(0x84)

	settlement, found, err := e.app.MiningKeeper.GetSettlement(e.ctx, e.slotID, e.epoch)
	require.NoError(t, err)
	require.True(t, found)
	settlement.SettlementMode = miningtypes.SettlementMode_SETTLEMENT_MODE_OPERATOR_ONLY
	require.NoError(t, e.app.MiningKeeper.Settlements.Set(
		e.ctx, collections.Join(e.slotID, e.epoch), settlement))

	owed := e.entitlement(t)
	amount, err := owed.Amount()
	require.NoError(t, err)
	operatorBefore := e.balance(t, e.payout)

	response, err := e.msgServer.FinalizeSettlement(e.ctx, e.finalize(caller))
	require.NoError(t, err)
	require.Equal(t,
		miningtypes.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_PERMISSIONLESS_OPERATOR_ONLY,
		response.FinalizationReason)
	require.Equal(t, amount.String(), response.ReleasedRemainder)
	require.Equal(t, operatorBefore.Add(amount).String(), e.balance(t, e.payout).String())
	require.True(t, e.balance(t, caller).IsZero())
	assertInvariants(t, e.app, e.ctx)
}

// TestAFailureAfterTheRemainderTransferUnwindsTheMoney is the §24 atomicity proof,
// at the one seam that is genuinely reachable with a real bank.
//
// The remainder release commits into the handler's cache, and the terminal write
// that follows then fails on its block-height guard. That guard is a real
// fail-closed check — a terminal settlement must record a positive finalization
// height, and a nonpositive height converted to unsigned would record an enormous
// one — not an error path added for this test.
//
// What must hold is that the transfer does not survive the failure. If it did, the
// operator would have been paid against a settlement still OPEN, and an OPEN
// settlement can be finalized again — paying the operator twice.
func TestAFailureAfterTheRemainderTransferUnwindsTheMoney(t *testing.T) {
	e := bootSettlement(t)
	escrowBefore := e.escrow(t)
	operatorBefore := e.balance(t, e.payout)

	// Height zero: every authorization check still passes and the remainder release
	// succeeds, then the terminal write refuses.
	broken := e.ctx.WithBlockHeight(0)
	_, err := e.msgServer.FinalizeSettlement(broken, e.finalize(e.settlement))
	require.ErrorIs(t, err, miningtypes.ErrInvalidState)
	require.Contains(t, err.Error(), "requires a positive block height")

	require.Equal(t, operatorBefore.String(), e.balance(t, e.payout).String(),
		"the operator transfer was unwound")
	require.Equal(t, escrowBefore.String(), e.escrow(t).String(), "escrow is untouched")
	require.Equal(t, "0", e.entitlement(t).ReleasedAmount,
		"the entitlement-side released amount was unwound with it")

	settlement, _, err := e.app.MiningKeeper.GetSettlement(e.ctx, e.slotID, e.epoch)
	require.NoError(t, err)
	require.False(t, settlement.Finalized, "the settlement remains open and retryable")
	require.Zero(t, settlement.FinalizedHeight)

	// And it finalizes cleanly once the cause is gone, exactly once.
	response, err := e.msgServer.FinalizeSettlement(e.ctx, e.finalize(e.settlement))
	require.NoError(t, err)
	amount, err := e.entitlement(t).Amount()
	require.NoError(t, err)
	require.Equal(t, amount.String(), response.ReleasedRemainder)
	require.Equal(t, operatorBefore.Add(amount).String(), e.balance(t, e.payout).String())
	assertInvariants(t, e.app, e.ctx)
}

// TestFinalizationIsTerminalOnTheRealBank proves a second attempt pays nothing.
func TestFinalizationIsTerminalOnTheRealBank(t *testing.T) {
	e := bootSettlement(t)
	_, err := e.msgServer.FinalizeSettlement(e.ctx, e.finalize(e.settlement))
	require.NoError(t, err)
	operatorAfterFirst := e.balance(t, e.payout)
	escrowAfterFirst := e.escrow(t)

	_, err = e.msgServer.FinalizeSettlement(e.ctx, e.finalize(e.settlement))
	require.ErrorIs(t, err, miningtypes.ErrInvalidState)
	require.Contains(t, err.Error(), "already finalized")

	require.Equal(t, operatorAfterFirst.String(), e.balance(t, e.payout).String(),
		"no second payout")
	require.Equal(t, escrowAfterFirst.String(), e.escrow(t).String())
	assertInvariants(t, e.app, e.ctx)
}
