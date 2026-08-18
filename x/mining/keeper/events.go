package keeper

import (
	"context"
	"strconv"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/x/mining/types"
)

// emitChunkSubmitted announces an accepted participant chunk.
//
// The chunk's position is reported BOTH as the index that was accepted and as the
// cursor that follows it. That is the pair a worker recovering from a lost response
// needs, and reporting only one would leave it inferring the other from a rule it
// would then be trusting an event to have applied.
//
// Individual payout lines are deliberately not enumerated. The recipients and
// amounts are in the transaction that produced this event, and copying them into
// the log would create a second account of a money movement that could drift from
// the first.
func emitChunkSubmitted(
	ctx context.Context,
	settlement types.Settlement,
	accepted uint64,
	recipients int,
	total sdkmath.Int,
) {
	// The accepted index is passed in rather than derived as cursor-1. The
	// subtraction would be correct only because of where this is called from, and an
	// unsigned underflow in an event is a silent one.
	sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(
		types.EventTypeSettlementChunkSubmitted,
		sdk.NewAttribute(types.AttributeKeySlotID, u64(settlement.SlotId)),
		sdk.NewAttribute(types.AttributeKeyEpoch, u64(settlement.Epoch)),
		sdk.NewAttribute(types.AttributeKeyChunkIndex, u64(accepted)),
		sdk.NewAttribute(types.AttributeKeyNextChunkIndex, u64(settlement.NextChunkIndex)),
		sdk.NewAttribute(types.AttributeKeyRecipientCount, strconv.Itoa(recipients)),
		sdk.NewAttribute(types.AttributeKeyChunkTotal, total.String()),
	))
}

func u64(v uint64) string { return strconv.FormatUint(v, 10) }

// emitSettlementFinalized announces a terminal settlement.
//
// The authorization arm is reported because it is the one thing an observer cannot
// infer from the transaction: the same signer can produce different arms depending
// on the settlement's mode and how far its clock has run.
func emitSettlementFinalized(
	ctx context.Context,
	settlement types.Settlement,
	reason types.SettlementFinalizationReason,
	remainder sdkmath.Int,
) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	sdkCtx.EventManager().EmitEvent(sdk.NewEvent(
		types.EventTypeSettlementFinalized,
		sdk.NewAttribute(types.AttributeKeySlotID, u64(settlement.SlotId)),
		sdk.NewAttribute(types.AttributeKeyEpoch, u64(settlement.Epoch)),
		sdk.NewAttribute(types.AttributeKeyFinalizationReason, reason.String()),
		sdk.NewAttribute(types.AttributeKeyReleasedRemainder, remainder.String()),
		sdk.NewAttribute(types.AttributeKeyFinalizedHeight, u64(uint64(sdkCtx.BlockHeight()))),
	))
}
