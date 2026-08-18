package keeper

import (
	"bytes"
	"context"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	appparams "github.com/twilight-project/twilight-core/app/params"
	"github.com/twilight-project/twilight-core/internal/checked"
	"github.com/twilight-project/twilight-core/x/mining/types"
	rewardstypes "github.com/twilight-project/twilight-core/x/rewards/types"
)

// Participant chunk admission.
//
// # What a chunk is, and what the trusted signer can and cannot do
//
// A chunk is one bounded batch of participant payouts against an open settlement.
// The signer may choose the recipients, may pay anywhere from nothing up to the
// full entitlement, and may spread that across the permitted number of chunks.
//
// It cannot exceed the entitlement, redirect the operator remainder, skip or
// reorder chunk progression, replay an accepted chunk, open a settlement, or send
// from escrow directly. The accepted POC1 threat model is stated plainly: a
// compromised settlement credential can direct participant releases up to the full
// entitlement of every settlement it controls. Nothing here narrows that, and
// nothing here may be presented as narrowing it — the operational response is
// credential rotation, or a chain-wide pause.
//
// # Two ceilings, layered
//
// This module proves the settlement's derived participant ceiling. x/rewards
// independently proves its own entitlement ceiling against the escrow it owns.
// The second is not a formality: x/mining does not hold the money, so a defect
// here must not be able to widen what actually leaves escrow.
//
// # Lifecycle is deliberately not consulted
//
// The handler never requires the Slot to be currently ACTIVE. An entitlement is
// earned by participation in a closed epoch, and suspension or removal must never
// rewrite the amount, the payout snapshot, the mode, the deadline, the released
// amount or the chunk cursor. Removal freezes the settlement address in x/coreslot,
// which is the containment that exists; a status check here would be confiscation.

// admittedChunk is a fully validated chunk, held between validation and effect.
//
// The whole chunk is proven before the first transfer, so the validated form is
// materialized once and then applied. A shape that revalidated per line while
// transferring would leave earlier recipients paid when a later line was refused.
type admittedChunk struct {
	settlement types.Settlement
	payouts    []rewardstypes.EntitlementPayout
	total      sdkmath.Int
}

// SubmitSettlementChunk admits and applies one participant chunk.
//
// The whole transition — validation, every participant transfer, the
// entitlement-side released-amount increase and the chunk-cursor advance — commits
// together or not at all. The cache is opened here rather than relied upon from
// baseapp, for the same reason the rewards release boundary opens its own: an
// atomicity guarantee that holds only because some outer layer happens to discard
// on error is not a guarantee.
//
// Returns the settlement's chunk cursor after the chunk committed.
func (k Keeper) SubmitSettlementChunk(
	ctx context.Context, msg *types.MsgSubmitSettlementChunk,
) (uint64, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	next, err := k.submitSettlementChunk(cacheCtx, msg)
	if err != nil {
		return 0, err
	}
	write()
	return next, nil
}

func (k Keeper) submitSettlementChunk(
	ctx context.Context, msg *types.MsgSubmitSettlementChunk,
) (uint64, error) {
	if msg == nil {
		return 0, types.ErrInvalidState.Wrap("a settlement chunk message is required")
	}
	if err := msg.Validate(); err != nil {
		return 0, err
	}

	// The pause is checked FIRST, before any settlement state is read. A paused
	// chain refuses every chunk for a reason that has nothing to do with which
	// settlement was named, and checking in this order means a paused rejection
	// cannot be distinguished from another paused rejection by whether the
	// settlement happened to exist.
	//
	// The value read is the one effective at the beginning of the block: a pause
	// accepted earlier in this same block takes effect at H+1, so admission does
	// not depend on where in the block that transaction landed.
	releaseEnabled, err := k.rewardsKeeper.SettlementReleaseEnabled(ctx)
	if err != nil {
		return 0, types.ErrInvalidState.Wrapf("settlement release state could not be read: %v", err)
	}
	if !releaseEnabled {
		return 0, types.ErrUnsupportedFeature.Wrap("settlement payout release is paused")
	}

	admitted, err := k.admitChunk(ctx, msg)
	if err != nil {
		return 0, err
	}

	// The independent ceiling, enforced by the module that owns the escrow. It
	// performs every transfer and raises the entitlement-side released amount.
	if err := k.rewardsKeeper.PayEntitlement(
		ctx, msg.SlotId, msg.Epoch, admitted.payouts,
	); err != nil {
		return 0, err
	}

	// The cursor advances only after the release succeeded. It never advances
	// partially, and an accepted chunk therefore cannot be replayed at its old
	// index: a caller that lost the response resolves its position by reading this
	// value back, where cursor == n+1 means chunk n committed and cursor == n means
	// retry n.
	next, err := checked.AddUint64(admitted.settlement.NextChunkIndex, 1)
	if err != nil {
		return 0, types.ErrInvalidState.Wrap("the settlement chunk cursor is exhausted")
	}
	updated := admitted.settlement
	updated.NextChunkIndex = next
	if err := updated.Validate(); err != nil {
		return 0, err
	}
	if err := k.Settlements.Set(ctx, settlementKey(updated.SlotId, updated.Epoch), updated); err != nil {
		return 0, err
	}

	emitChunkSubmitted(ctx, updated, msg.ChunkIndex, len(admitted.payouts), admitted.total)
	return next, nil
}

// admitChunk proves every admission rule and returns the payout set to release.
//
// Nothing here writes state. Every check that can refuse the chunk runs before the
// caller performs a single transfer.
func (k Keeper) admitChunk(
	ctx context.Context, msg *types.MsgSubmitSettlementChunk,
) (admittedChunk, error) {
	settlement, found, err := k.GetSettlement(ctx, msg.SlotId, msg.Epoch)
	if err != nil {
		return admittedChunk{}, err
	}
	if !found {
		return admittedChunk{}, types.ErrSettlementNotFound.Wrapf(
			"no settlement exists for slot %d in epoch %d", msg.SlotId, msg.Epoch)
	}
	// FINALIZED is terminal. A chunk against it would release value against an
	// obligation whose remainder has already been paid to the operator.
	if settlement.Finalized {
		return admittedChunk{}, types.ErrInvalidState.Wrapf(
			"the settlement for slot %d in epoch %d is finalized and admits no further chunks",
			msg.SlotId, msg.Epoch)
	}
	if err := requireParticipantChunksPermitted(settlement); err != nil {
		return admittedChunk{}, err
	}
	if err := k.requireSettlementSigner(ctx, settlement, msg.SettlementAddress); err != nil {
		return admittedChunk{}, err
	}

	params, err := k.SettlementParamsForTarget(ctx, settlement.Epoch)
	if err != nil {
		return admittedChunk{}, err
	}
	if params.Version != settlement.SettlementParamsVersion {
		return admittedChunk{}, types.ErrInvalidState.Wrapf(
			"the settlement for slot %d in epoch %d was created under settlement parameters version %d, "+
				"but epoch %d binds version %d",
			settlement.SlotId, settlement.Epoch, settlement.SettlementParamsVersion,
			settlement.Epoch, params.Version)
	}
	if err := k.requireBeforeDeadline(ctx, settlement, params); err != nil {
		return admittedChunk{}, err
	}
	if err := requireChunkPosition(settlement, params, msg.ChunkIndex); err != nil {
		return admittedChunk{}, err
	}

	payouts, total, err := k.admitPayoutSet(settlement, params, msg.Payouts)
	if err != nil {
		return admittedChunk{}, err
	}
	if err := k.requireWithinParticipantCeiling(ctx, settlement, total); err != nil {
		return admittedChunk{}, err
	}
	return admittedChunk{settlement: settlement, payouts: payouts, total: total}, nil
}

// requireParticipantChunksPermitted is a total switch over every settlement mode.
//
// Totality is the point: admitting a chunk is a monetary authorization, so a
// default arm would silently permit participant releases for a mode nobody had
// considered. OPERATOR_ONLY admits none — its entire entitlement belongs to the
// operator remainder. The SELECTED_PARTICIPANTS arm is unreachable in this profile
// and is implemented anyway so a later tranche adds a producer rather than a
// branch; it deliberately performs NO Selection applicability check, because no
// Selection state exists here to check against.
func requireParticipantChunksPermitted(settlement types.Settlement) error {
	switch settlement.SettlementMode {
	case types.SettlementMode_SETTLEMENT_MODE_TRUSTED_AS,
		types.SettlementMode_SETTLEMENT_MODE_SELECTED_PARTICIPANTS:
		return nil
	case types.SettlementMode_SETTLEMENT_MODE_OPERATOR_ONLY:
		return types.ErrUnsupportedFeature.Wrapf(
			"the settlement for slot %d in epoch %d is operator-only and admits no participant chunks",
			settlement.SlotId, settlement.Epoch)
	default:
		return types.ErrInvalidState.Wrapf(
			"the settlement for slot %d in epoch %d has no canonical settlement mode",
			settlement.SlotId, settlement.Epoch)
	}
}

// requireSettlementSigner proves the caller is the Slot's settlement address as of
// this block.
//
// What this compares is the message's DECLARED settlement_address against the one
// x/coreslot records. That the declared field is the account which actually signed
// the transaction is guaranteed one layer up, by the cosmos.msg.v1.signer option on
// the message: the SDK derives the required signer from that field and verifies the
// signature against it before a handler runs. The two halves together are the
// authorization; neither is sufficient alone.
//
// The comparison is on DECODED address bytes rather than on the two strings. Two
// encodings of one account must authorize identically, and an encoding that
// differs from the stored spelling must not be refused for that reason alone.
//
// The Slot's lifecycle status is deliberately not consulted. What removal does is
// freeze this address in x/coreslot, so a removed Slot's settlements stay payable
// by whoever held the credential at removal — which is the point.
func (k Keeper) requireSettlementSigner(
	ctx context.Context, settlement types.Settlement, signer string,
) error {
	slot, err := k.coreSlotKeeper.GetSlot(ctx, settlement.SlotId)
	if err != nil {
		return types.ErrInvalidState.Wrapf(
			"the core slot %d backing this settlement could not be read: %v", settlement.SlotId, err)
	}
	expected, err := k.economicAddresses.Validate(slot.SettlementAddress)
	if err != nil {
		return types.ErrInvalidState.Wrapf(
			"the settlement address recorded for slot %d is not a usable destination: %v",
			settlement.SlotId, err)
	}
	supplied, err := k.economicAddresses.Validate(signer)
	if err != nil {
		return types.ErrInvalidAddress.Wrapf("settlement chunk signer: %v", err)
	}
	if !supplied.Equals(expected) {
		return types.ErrInvalidAddress.Wrapf(
			"only the settlement address of slot %d may submit its chunks", settlement.SlotId)
	}
	return nil
}

// requireBeforeDeadline refuses a chunk at or past the settlement's deadline.
//
// The clock compared against is the handler-visible one, which is exactly the value
// committed at the end of the previous block. This block's increment has not
// happened yet and must not be derived or pre-applied here: doing so would move
// every deadline forward by one block relative to what EndBlock will actually
// record.
//
// Reaching the deadline permanently closes chunks. It does not change who receives
// funds — only who may trigger finalization.
func (k Keeper) requireBeforeDeadline(
	ctx context.Context, settlement types.Settlement, params types.SettlementParamsVersion,
) error {
	anchor, err := k.requireEpochAnchor(ctx, settlement.Epoch)
	if err != nil {
		return err
	}
	clock, err := k.GetSettlementClock(ctx)
	if err != nil {
		return err
	}
	// The anchor must be a moment that has actually happened, proven BEFORE the
	// window it opens is measured.
	if err := requireAnchorHasElapsed(settlement, anchor, clock); err != nil {
		return err
	}
	deadline, err := k.DeadlineClock(ctx, settlement, anchor, params)
	if err != nil {
		return err
	}
	if clock >= deadline {
		return types.ErrUnsupportedFeature.Wrapf(
			"the participant window for slot %d in epoch %d closed at settlement clock %d, and the clock is %d",
			settlement.SlotId, settlement.Epoch, deadline, clock)
	}
	return nil
}

// requireAnchorHasElapsed proves an anchor describes a moment the chain has
// already reached.
//
// # Why the window check alone is not enough
//
// Admission proves current_clock < anchor_clock + window. That comparison treats
// the anchor as trusted input: an anchor carrying a clock the chain has not
// reached yet pushes the deadline forward by exactly its excess, so a corrupted
// future anchor does not merely survive the check — it EXTENDS the participant
// window, and can reopen one that had already closed. Everything downstream then
// authorizes a real release against impossible canonical state.
//
// The clock is monotonic and only ever advances, so an anchor ahead of it cannot
// have been created by any code path. Equality is fine, and so is zero: an epoch
// whose settlements were materialized before the clock had ever ticked carries a
// legitimate anchor of zero, and it must keep working forever.
//
// Stated as a standalone rule rather than inline because finalization needs the
// same guarantee about the same anchor — the deadline it consults decides which
// authorization arm applies, so it must not be derived from a moment that never
// happened either.
func requireAnchorHasElapsed(
	settlement types.Settlement, anchor types.SettlementEpochAnchor, clock uint64,
) error {
	if anchor.CreatedSettlementClock > clock {
		return types.ErrInvalidState.Wrapf(
			"the settlement epoch anchor for epoch %d records settlement clock %d, "+
				"which is ahead of the canonical clock %d; slot %d cannot be settled against "+
				"a moment that has not happened",
			settlement.Epoch, anchor.CreatedSettlementClock, clock, settlement.SlotId)
	}
	return nil
}

// requireChunkPosition proves the chunk is the next one and that there is room for
// it.
//
// The index is supplied by the caller and must match exactly. Inferring it would
// make a replayed submission look like the next chunk and pay the same recipients
// twice; an equality check turns a replay into a rejection.
func requireChunkPosition(
	settlement types.Settlement, params types.SettlementParamsVersion, chunkIndex uint64,
) error {
	if chunkIndex != settlement.NextChunkIndex {
		return types.ErrInvalidState.Wrapf(
			"the settlement for slot %d in epoch %d expects chunk %d, not chunk %d",
			settlement.SlotId, settlement.Epoch, settlement.NextChunkIndex, chunkIndex)
	}
	// Both bounds are applied. The configured maximum is the operative one and is
	// admitted at or below the immutable ceiling, so it normally dominates; the
	// immutable ceiling is checked anyway, because a stored parameter row above it
	// is corruption and must not be able to authorize a fifth chunk.
	if settlement.NextChunkIndex >= params.MaxChunksPerSettlement {
		return types.ErrInvalidState.Wrapf(
			"the settlement for slot %d in epoch %d permits %d chunks and has used them all",
			settlement.SlotId, settlement.Epoch, params.MaxChunksPerSettlement)
	}
	if settlement.NextChunkIndex >= appparams.HardMaxChunksPerSettlement {
		return types.ErrInvalidState.Wrapf(
			"the settlement for slot %d in epoch %d would exceed the immutable maximum of %d chunks",
			settlement.SlotId, settlement.Epoch, appparams.HardMaxChunksPerSettlement)
	}
	return nil
}

// admitPayoutSet validates every line of the chunk and returns the release set.
//
// # Why strictly ascending, and not merely unique
//
// Ordering by decoded address bytes gives uniqueness for free, and it does more
// than that: it makes one chunk have exactly one canonical form. Two submissions
// naming the same recipients in different orders are the same chunk, so they
// cannot produce different observable effects, and a duplicate cannot hide behind
// a reordering. The rewards release boundary AGGREGATES repeated destinations,
// which is the right behavior for a boundary that must accept whatever it is
// handed; this layer is stricter and refuses them outright.
//
// The comparison is on decoded bytes rather than on the caller's strings, because
// the ordering must be a property of the accounts, not of how they were spelled.
//
// Across DIFFERENT chunks the same recipient may appear again. Nothing here tracks
// recipients between chunks.
func (k Keeper) admitPayoutSet(
	settlement types.Settlement,
	params types.SettlementParamsVersion,
	lines []*types.SettlementChunkPayout,
) ([]rewardstypes.EntitlementPayout, sdkmath.Int, error) {
	if uint64(len(lines)) > params.MaxRecipientsPerChunk {
		return nil, sdkmath.Int{}, types.ErrInvalidState.Wrapf(
			"a settlement chunk for slot %d in epoch %d names %d recipients, above the configured maximum of %d",
			settlement.SlotId, settlement.Epoch, len(lines), params.MaxRecipientsPerChunk)
	}
	floor, err := requirePayoutFloor(params)
	if err != nil {
		return nil, sdkmath.Int{}, err
	}

	payouts := make([]rewardstypes.EntitlementPayout, 0, len(lines))
	total := sdkmath.ZeroInt()
	var previous sdk.AccAddress

	for i, line := range lines {
		if line == nil {
			return nil, sdkmath.Int{}, types.ErrInvalidState.Wrapf(
				"%s is empty", chunkLineLabel(settlement, i))
		}
		recipient, err := k.economicAddresses.Validate(line.Recipient)
		if err != nil {
			return nil, sdkmath.Int{}, types.ErrInvalidAddress.Wrapf(
				"chunk payout line %d recipient: %v", i, err)
		}
		if previous != nil && bytes.Compare(previous, recipient) >= 0 {
			return nil, sdkmath.Int{}, types.ErrInvalidState.Wrapf(
				"chunk payout line %d is not strictly after line %d; "+
					"recipients must be unique and strictly ascending by address bytes", i, i-1)
		}
		previous = recipient

		amount, err := types.ParseCanonicalAmount(
			chunkLineLabel(settlement, i)+" amount", line.Amount)
		if err != nil {
			return nil, sdkmath.Int{}, err
		}
		// A zero participant payout is never valid. The protocol's zero-value
		// allowances cover legitimate zero substeps — a zero mint, a zero treasury
		// share, a zero operator remainder — never a participant line, which on a
		// feeless chain would be a free account-creation primitive.
		if amount.LT(floor) {
			return nil, sdkmath.Int{}, types.ErrInvalidState.Wrapf(
				"%s of %s is below the minimum participant payout of %s",
				chunkLineLabel(settlement, i), amount, floor)
		}

		sum, err := total.SafeAdd(amount)
		if err != nil {
			return nil, sdkmath.Int{}, types.ErrInvalidState.Wrapf(
				"a settlement chunk for slot %d in epoch %d overflows: %v",
				settlement.SlotId, settlement.Epoch, err)
		}
		total = sum
		payouts = append(payouts, rewardstypes.EntitlementPayout{
			Recipient: line.Recipient,
			Amount:    line.Amount,
		})
	}

	// Implied by a positive floor over a nonempty set, and asserted rather than
	// assumed: the floor is the only thing standing between here and a zero-total
	// chunk, and it is read from stored parameters.
	if !total.IsPositive() {
		return nil, sdkmath.Int{}, types.ErrInvalidState.Wrapf(
			"a settlement chunk for slot %d in epoch %d moves no value",
			settlement.SlotId, settlement.Epoch)
	}
	return payouts, total, nil
}

// requirePayoutFloor resolves the effective per-recipient minimum.
//
// The larger of the target-bound minimum and the immutable floor. Governance can
// only ever raise it: a stored row BELOW the floor is refused when the parameter
// record is read, so this function is never actually reached with one, and no test
// asserts otherwise. The maximum is kept as a second line of defense — the read
// validation and this bound are independent, and a later weakening of the first
// must not silently authorize a payment beneath the floor.
func requirePayoutFloor(params types.SettlementParamsVersion) (sdkmath.Int, error) {
	configured, err := types.ParseCanonicalAmount(
		"minimum recipient payout amount", params.MinRecipientPayoutAmount)
	if err != nil {
		return sdkmath.Int{}, err
	}
	hard := appparams.HardMinSettlementPayoutAmount()
	if configured.LT(hard) {
		return hard, nil
	}
	return configured, nil
}

// requireWithinParticipantCeiling proves the chunk against the settlement's derived
// ceiling, measured from the AUTHORITATIVE released amount.
//
// That released amount lives on the entitlement in x/rewards, not on the
// settlement. A second copy inside Settlement would be a duplicate monetary
// authority that could diverge from the value the release boundary actually
// enforces, and this check would then be proving the wrong number.
func (k Keeper) requireWithinParticipantCeiling(
	ctx context.Context, settlement types.Settlement, requested sdkmath.Int,
) error {
	entitlement, err := k.requireEntitlementFor(ctx, settlement)
	if err != nil {
		return err
	}
	ceiling, err := ParticipantDistributionCeiling(settlement, entitlement)
	if err != nil {
		return err
	}
	released, err := entitlement.Released()
	if err != nil {
		return types.ErrInvalidState.Wrap(err.Error())
	}
	next, err := released.SafeAdd(requested)
	if err != nil {
		return types.ErrInvalidState.Wrapf(
			"the released total for slot %d in epoch %d overflows: %v",
			settlement.SlotId, settlement.Epoch, err)
	}
	if next.GT(ceiling) {
		return types.ErrInvalidState.Wrapf(
			"releasing %s against the settlement for slot %d in epoch %d would take the released total to %s, "+
				"above the participant distribution ceiling of %s",
			requested, settlement.SlotId, settlement.Epoch, next, ceiling)
	}
	return nil
}

// requireEntitlementFor loads the obligation a settlement was materialized from.
//
// Absence is corruption, not an ordinary answer. A settlement exists only because
// a nonzero entitlement existed when its epoch closed, and the two are created in
// the same transition — so a missing one means the pair came apart, and every
// amount derived from it would be invented.
func (k Keeper) requireEntitlementFor(
	ctx context.Context, settlement types.Settlement,
) (rewardstypes.SlotEntitlement, error) {
	entitlement, found, err := k.rewardsKeeper.GetSlotEntitlement(ctx, settlement.SlotId, settlement.Epoch)
	if err != nil {
		return rewardstypes.SlotEntitlement{}, types.ErrInvalidState.Wrapf(
			"the entitlement for slot %d in epoch %d could not be read: %v",
			settlement.SlotId, settlement.Epoch, err)
	}
	if !found {
		return rewardstypes.SlotEntitlement{}, types.ErrInvalidState.Wrapf(
			"the settlement for slot %d in epoch %d has no entitlement", settlement.SlotId, settlement.Epoch)
	}
	if entitlement.SlotId != settlement.SlotId || entitlement.Epoch != settlement.Epoch {
		return rewardstypes.SlotEntitlement{}, types.ErrInvalidState.Wrapf(
			"the settlement for slot %d in epoch %d resolved an entitlement for slot %d in epoch %d",
			settlement.SlotId, settlement.Epoch, entitlement.SlotId, entitlement.Epoch)
	}
	return entitlement, nil
}

func chunkLineLabel(settlement types.Settlement, index int) string {
	return types.ChunkPayoutLabel(settlement.SlotId, settlement.Epoch, index)
}
