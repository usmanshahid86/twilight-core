package app_test

import (
	"testing"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/app"
	appparams "github.com/twilight-project/twilight-core/app/params"
	miningkeeper "github.com/twilight-project/twilight-core/x/mining/keeper"
	miningtypes "github.com/twilight-project/twilight-core/x/mining/types"
	rewardstypes "github.com/twilight-project/twilight-core/x/rewards/types"
)

// The definitive POC1 payout proof.
//
// Everything before this establishes pieces. This establishes the deployed path, in
// one run, on a booted application with the real bank:
//
//	ACTIVE CoreSlot
//	  -> reward participation recorded by consensus
//	  -> rewards epoch finalized
//	  -> SlotEntitlement created
//	  -> mining Settlement materialized
//	  -> participant chunk by signed message
//	  -> real participant balance increase
//	  -> explicit finalization
//	  -> operator remainder to the immutable payout snapshot
//	  -> released_amount == entitlement_amount
//	  -> Settlement FINALIZED
//
// Two properties distinguish it from the gate tests it follows. It never injects a
// SlotEntitlement: the entitlement has to be produced by running real blocks through
// the real reward path, which is the only way to prove the deployed chain reaches
// it. And it reads state back through the A6 query surface at every stage rather
// than through keeper collections, which is what demonstrates a worker could recover
// from committed state alone.
//
// It runs at the minimum protocol-permitted epoch length so it stays entirely inside
// the frozen consensus bounds while closing an epoch as fast as the protocol allows.

// TestDefinitivePOC1SettlementEndToEnd is the money-movement proof for the tranche.
func TestDefinitivePOC1SettlementEndToEnd(t *testing.T) {
	a := bootApp(t)
	base := a.NewUncachedContext(false, cmtproto.Header{Height: 1})

	// The minimum protocol-permitted epoch length. Deliberately the floor, not a
	// test-only value: HARD_MIN_EPOCH_LENGTH_BLOCKS is the bound, and using it keeps
	// the run inside the ratified envelope rather than inventing a faster mode.
	const epochLength = appparams.HardMinEpochLengthBlocks
	require.Equal(t, uint64(360), epochLength,
		"the definitive run uses the ratified minimum epoch length")

	operator, payout, credential := acc(0x02), acc(0x0c), acc(0x28)
	params, snapshot := rewardsParams(t, func(p *rewardstypes.Params) {
		p.InitialBlockSubsidy = "100000"
		p.EpochLengthBlocks = epochLength
	})
	initCoreSlotsAndRewards(t, a, base, []slotSpec{
		{id: 1, operator: operator, payout: payout, keyMarker: 1, settlement: credential},
	}, genesisState(params, snapshot))
	initMining(t, a, base)

	// --- 1. an ACTIVE CoreSlot participates for a whole epoch -----------------
	ctx := driveSettlementBlocks(t, a, base, 1, int64(epochLength))

	epoch, found, err := a.RewardsKeeper.GetFinalizedEpoch(ctx, 1)
	require.NoError(t, err)
	require.True(t, found, "the epoch closed at the 360-block boundary")
	require.Equal(t, uint64(1), epoch.EpochNumber)

	// --- 2. participation became a canonical entitlement ----------------------
	owed, found, err := a.RewardsKeeper.GetSlotEntitlement(ctx, 1, 1)
	require.NoError(t, err)
	require.True(t, found, "the deployed reward path produced the entitlement; nothing injected it")
	entitlementAmount, err := owed.Amount()
	require.NoError(t, err)
	// The frozen scenario's exact value, not merely a positive one. A
	// positive-but-wrong emission — a changed subsidy, a changed epoch length, a
	// changed allocation rule — would otherwise leave the definitive proof green
	// while proving the wrong economics.
	require.Equal(t, "36000000", entitlementAmount.String(),
		"360 blocks at a subsidy of 100000, allocated to the single participating Slot")
	require.Equal(t, payout, owed.PayoutAddress, "the payout snapshot is the operator destination")

	msgServer := miningkeeper.NewMsgServer(a.MiningKeeper)
	querier := miningkeeper.NewQueryServer(a.MiningKeeper)
	settlementQuery := func() *miningtypes.QuerySettlementResponse {
		res, err := querier.Settlement(ctx, &miningtypes.QuerySettlementRequest{SlotId: 1, Epoch: 1})
		require.NoError(t, err)
		return res
	}

	// --- 3. consensus materialized the Settlement -----------------------------
	//
	// Read through the query surface, because that is what a worker has.
	afterMaterialization := settlementQuery()
	require.Equal(t, miningtypes.SettlementMode_SETTLEMENT_MODE_TRUSTED_AS,
		afterMaterialization.Settlement.SettlementMode)
	require.False(t, afterMaterialization.Settlement.Finalized)
	require.Zero(t, afterMaterialization.Settlement.NextChunkIndex, "chunk 0 is next")
	require.Equal(t, "0", afterMaterialization.ReleasedAmount)
	require.Equal(t, entitlementAmount.String(), afterMaterialization.RemainingAmount)
	require.Equal(t, entitlementAmount.String(), afterMaterialization.ParticipantDistributionCeiling)
	require.False(t, afterMaterialization.PermissionlessFinalizationNow,
		"inside the participant window, only the settlement signer may finalize")

	open, err := querier.OpenSettlements(ctx, &miningtypes.QueryOpenSettlementsRequest{SlotId: 1})
	require.NoError(t, err)
	require.Len(t, open.Settlements, 1, "the Slot has exactly one outstanding settlement")

	// --- 4. the trusted worker distributes part of it -------------------------
	participant := acc(0x55)
	distributed := entitlementAmount.QuoRaw(4)
	require.Equal(t, "9000000", distributed.String(), "the frozen participant payout")
	require.True(t, distributed.GTE(appparams.HardMinSettlementPayoutAmount()),
		"the participant line is above the immutable floor")

	escrowBefore := e2eEscrow(t, a, ctx)
	liabilityBefore, err := a.RewardsKeeper.GetOutstandingEntitlementLiability(ctx)
	require.NoError(t, err)
	require.True(t, e2eBalance(t, a, ctx, participant).IsZero())
	require.True(t, e2eBalance(t, a, ctx, payout).IsZero())

	chunkRes, err := msgServer.SubmitSettlementChunk(ctx, &miningtypes.MsgSubmitSettlementChunk{
		SettlementAddress: credential, SlotId: 1, Epoch: 1, ChunkIndex: 0,
		Payouts: []*miningtypes.SettlementChunkPayout{
			{Recipient: participant, Amount: distributed.String()},
		},
	})
	require.NoError(t, err)
	require.Equal(t, uint64(1), chunkRes.NextChunkIndex)

	require.Equal(t, distributed.String(), e2eBalance(t, a, ctx, participant).String(),
		"a real participant balance rose")

	// Replay, asserted HERE rather than after finalization. Once a settlement is
	// terminal the finalized check refuses every chunk, so a replay attempted then
	// proves nothing about replay protection at all.
	_, err = msgServer.SubmitSettlementChunk(ctx, &miningtypes.MsgSubmitSettlementChunk{
		SettlementAddress: credential, SlotId: 1, Epoch: 1, ChunkIndex: 0,
		Payouts: []*miningtypes.SettlementChunkPayout{
			{Recipient: participant, Amount: distributed.String()},
		},
	})
	require.Error(t, err, "an accepted chunk index cannot be replayed")
	require.Equal(t, distributed.String(), e2eBalance(t, a, ctx, participant).String(),
		"the refused replay paid nothing a second time")

	afterChunk := settlementQuery()
	require.Equal(t, uint64(1), afterChunk.Settlement.NextChunkIndex,
		"the cursor says chunk 0 committed and chunk 1 is next")
	require.Equal(t, distributed.String(), afterChunk.ReleasedAmount)
	require.Equal(t, entitlementAmount.Sub(distributed).String(), afterChunk.RemainingAmount)
	require.False(t, afterChunk.Settlement.Finalized)

	// --- 5. explicit finalization pays the operator remainder -----------------
	finalizeRes, err := msgServer.FinalizeSettlement(ctx, &miningtypes.MsgFinalizeSettlement{
		Signer: credential, SlotId: 1, Epoch: 1,
	})
	require.NoError(t, err)
	remainder := entitlementAmount.Sub(distributed)
	require.Equal(t, "27000000", remainder.String(), "the frozen operator remainder")
	require.Equal(t, remainder.String(), finalizeRes.ReleasedRemainder)
	require.Equal(t, entitlementAmount, distributed.Add(remainder),
		"participant plus operator is the whole entitlement")
	require.Equal(t,
		miningtypes.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_AUTHORIZED_EARLY,
		finalizeRes.FinalizationReason)

	// --- 6. exact conservation ------------------------------------------------
	require.Equal(t, distributed.String(), e2eBalance(t, a, ctx, participant).String(),
		"the participant keeps exactly what it was paid")
	require.Equal(t, remainder.String(), e2eBalance(t, a, ctx, payout).String(),
		"the operator receives exactly the remainder, at the immutable payout snapshot")
	require.True(t, e2eBalance(t, a, ctx, credential).IsZero(),
		"the settlement credential that triggered both operations receives nothing")
	require.Equal(t, escrowBefore.Sub(entitlementAmount).String(), e2eEscrow(t, a, ctx).String(),
		"escrow fell by exactly the entitlement across both movements")

	liabilityAfter, err := a.RewardsKeeper.GetOutstandingEntitlementLiability(ctx)
	require.NoError(t, err)
	require.Equal(t, liabilityBefore.Sub(entitlementAmount).String(), liabilityAfter.String(),
		"the outstanding obligation fell by the same value")

	// --- 7. terminal state, read back through the recovery surface ------------
	afterFinalization := settlementQuery()
	require.True(t, afterFinalization.Settlement.Finalized)
	require.Equal(t, entitlementAmount.String(), afterFinalization.ReleasedAmount,
		"released_amount reached entitlement_amount")
	require.Equal(t, "0", afterFinalization.RemainingAmount)
	require.Equal(t,
		miningtypes.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_AUTHORIZED_EARLY,
		afterFinalization.Settlement.FinalizationReason)
	require.Positive(t, afterFinalization.Settlement.FinalizedHeight)
	require.False(t, afterFinalization.PermissionlessFinalizationNow,
		"nothing remains to finalize")

	open, err = querier.OpenSettlements(ctx, &miningtypes.QueryOpenSettlementsRequest{SlotId: 1})
	require.NoError(t, err)
	require.Empty(t, open.Settlements, "the Slot has no outstanding work left")

	// --- 8. the lifecycle is terminal and replay-safe -------------------------
	// Chunk index 1 is the settlement's genuine next index, so the only thing that
	// can refuse this is terminality. Reusing index 0 would be refused by the
	// cursor check instead and would prove nothing about the terminal state.
	_, err = msgServer.SubmitSettlementChunk(ctx, &miningtypes.MsgSubmitSettlementChunk{
		SettlementAddress: credential, SlotId: 1, Epoch: 1, ChunkIndex: 1,
		Payouts: []*miningtypes.SettlementChunkPayout{
			{Recipient: acc(0x56), Amount: appparams.HardMinSettlementPayoutAmount().String()},
		},
	})
	require.Error(t, err, "a terminal settlement admits no further chunk")
	require.True(t, e2eBalance(t, a, ctx, acc(0x56)).IsZero())

	_, err = msgServer.FinalizeSettlement(ctx, &miningtypes.MsgFinalizeSettlement{
		Signer: credential, SlotId: 1, Epoch: 1,
	})
	require.Error(t, err, "a terminal settlement cannot be finalized twice")

	require.Equal(t, remainder.String(), e2eBalance(t, a, ctx, payout).String(),
		"no refused operation moved value")
	require.Equal(t, distributed.String(), e2eBalance(t, a, ctx, participant).String())

	// --- 9. no legacy claim path was involved anywhere ------------------------
	//
	// This used to assert that no ClaimRecord had been created. That assertion is
	// now unwritable, and its absence is the stronger statement: the claim store,
	// its message, its queries and its CLI no longer exist, so the lifecycle proven
	// above is the only way entitlement value can leave escrow. What was once a
	// runtime check is now a property of the type system.

	assertInvariants(t, a, ctx)

	// The exact economics of the run, recorded so the proof is legible without
	// re-deriving it from the parameters.
	t.Logf("definitive POC1 settlement: epoch_length=%d minted=%s entitlement=%s "+
		"participant=%s operator_remainder=%s released=%s reason=%s finalized_height=%d",
		epochLength, epoch.MintedEmission, entitlementAmount, distributed, remainder,
		afterFinalization.ReleasedAmount, afterFinalization.Settlement.FinalizationReason,
		afterFinalization.Settlement.FinalizedHeight)
}

func e2eBalance(t *testing.T, a *app.App, ctx sdk.Context, address string) sdkmath.Int {
	t.Helper()
	return a.BankKeeper.GetBalance(ctx, mustAddr(t, address), appparams.NativeBaseDenom).Amount
}

func e2eEscrow(t *testing.T, a *app.App, ctx sdk.Context) sdkmath.Int {
	t.Helper()
	module := a.AccountKeeper.GetModuleAddress(rewardstypes.ModuleName)
	return a.BankKeeper.GetBalance(ctx, module, appparams.NativeBaseDenom).Amount
}
