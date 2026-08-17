package keeper

import (
	"context"
	"fmt"
	"sort"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// The constrained release boundary.
//
// These are the only ways value leaves rewards escrow for mining distribution,
// and they are keeper-only: there is no message, no CLI, and no transaction that
// reaches them. A future x/mining will call them.
//
// # Mode-blind by construction
//
// Nothing here knows what a settlement is, which distribution mode a target was
// bound to, or who authorized the call. That is deliberate. x/mining will impose
// its own, stricter authorization ceiling; this boundary imposes the one that
// depends on nothing but rewards' own state, so a defect on the mining side
// cannot widen it. The two are layered, not alternatives.
//
// # What is never consulted
//
// Live CoreSlot lifecycle state. An entitlement is earned by participation in a
// closed epoch, and §64 keeps it payable through INACTIVE, SUSPENDED and REMOVED.
// Re-reading the Slot here would let a later lifecycle transition confiscate money
// that was already earned, which is exactly the confiscation the immutable payout
// snapshot exists to prevent.

// PayEntitlement releases a set of participant payouts against one entitlement.
//
// The whole SET is validated before the first transfer. That ordering is the
// contract, not an optimization: a per-line loop that validated and transferred
// together would leave earlier recipients paid when a later line was rejected.
// The cache would discard those transfers, but only because the error propagates
// — and an atomicity guarantee that depends on nobody swallowing an error is not
// a guarantee.
//
// The cache is opened here rather than left to the caller for the same reason.
// x/mining does not exist yet; when it does, its handler will have a cache of its
// own, and this one nests harmlessly inside it. What it must not do is make
// rewards' atomicity contingent on a module that has not been written.
func (k Keeper) PayEntitlement(
	ctx context.Context, slotID, epoch uint64, payouts []types.EntitlementPayout,
) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	if err := k.payEntitlement(cacheCtx, slotID, epoch, payouts); err != nil {
		return err
	}
	write()
	return nil
}

func (k Keeper) payEntitlement(
	ctx context.Context, slotID, epoch uint64, payouts []types.EntitlementPayout,
) error {
	if len(payouts) == 0 {
		return types.ErrInvalidState.Wrapf(
			"payout set for slot %d in epoch %d is empty", slotID, epoch)
	}

	// 1. Release must be permitted for this block at all, before any entitlement
	//    state is even read.
	entitlement, err := k.loadReleasableEntitlement(ctx, slotID, epoch)
	if err != nil {
		return err
	}

	// 2. Validate every line. Recipients are resolved to parsed addresses here and
	//    reused below rather than decoded a second time at transfer.
	params, err := k.GetParams(ctx)
	if err != nil {
		return err
	}
	grouped, total, err := k.resolvePayoutSet(slotID, epoch, payouts)
	if err != nil {
		return err
	}

	// 3. Prove the ceiling for the whole set before anything moves.
	if err := k.assertReleaseCeiling(entitlement, total); err != nil {
		return err
	}

	// 4. Transfer, then account. released_amount rises only after every send has
	//    succeeded (§34).
	for _, recipient := range sortedRecipients(grouped) {
		if err := k.bankKeeper.SendCoinsFromModuleToAccount(
			ctx,
			types.ModuleName,
			grouped[recipient].address,
			sdk.NewCoins(sdk.NewCoin(params.NativeDenom, grouped[recipient].amount)),
		); err != nil {
			return err
		}
	}
	return k.recordRelease(ctx, entitlement, total)
}

// PayEntitlementRemainderToOperator releases whatever is left of an entitlement to
// its immutable payout snapshot.
//
// There is no recipient parameter. Redirection is not rejected here, it is
// unrepresentable: the caller supplies only the obligation's identity, and the
// destination is derived from state that was fixed when the epoch closed. A
// rejection path could be relaxed by a later edit; a missing parameter cannot.
//
// A remainder of zero succeeds as a deterministic no-op with no bank send and no
// account side effect, so the settlement layer can mark a fully distributed
// settlement FINALIZED without creating an immortal zero-value payout obligation.
func (k Keeper) PayEntitlementRemainderToOperator(ctx context.Context, slotID, epoch uint64) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	if err := k.payEntitlementRemainder(cacheCtx, slotID, epoch); err != nil {
		return err
	}
	write()
	return nil
}

func (k Keeper) payEntitlementRemainder(ctx context.Context, slotID, epoch uint64) error {
	entitlement, err := k.loadReleasableEntitlement(ctx, slotID, epoch)
	if err != nil {
		return err
	}
	remaining, err := entitlement.Remaining()
	if err != nil {
		return err
	}
	if remaining.IsZero() {
		// Nothing is owed. No send, no destination revalidation, and no account or
		// account-number side effect created solely by a zero transfer.
		return nil
	}

	params, err := k.GetParams(ctx)
	if err != nil {
		return err
	}
	// Revalidated immediately before the transfer. The snapshot was admitted when
	// it was taken; this is the last point at which a destination that has since
	// become unusable can still be refused rather than paid.
	recipient, err := k.economicAddresses.Validate(entitlement.PayoutAddress)
	if err != nil {
		return types.ErrInvalidAddress.Wrapf(
			"entitlement payout address for slot %d in epoch %d: %v", slotID, epoch, err)
	}
	if err := k.bankKeeper.SendCoinsFromModuleToAccount(
		ctx,
		types.ModuleName,
		recipient,
		sdk.NewCoins(sdk.NewCoin(params.NativeDenom, remaining)),
	); err != nil {
		return err
	}
	return k.recordRelease(ctx, entitlement, remaining)
}

// loadReleasableEntitlement performs the two checks every release shares: that
// release is permitted for this block, and that the obligation exists and is
// internally coherent.
//
// The pause is checked FIRST, before the entitlement is read. A paused chain
// refuses release for reasons that have nothing to do with which obligation was
// named, and checking in this order means a paused release cannot be distinguished
// from another paused release by whether the entitlement happened to exist.
func (k Keeper) loadReleasableEntitlement(
	ctx context.Context, slotID, epoch uint64,
) (types.SlotEntitlement, error) {
	// The already-effective value, never the pending one. A pause accepted earlier
	// in this same block is scheduled for H+1, and treating it as effective here
	// would make a release depend on where in the block the pause transaction
	// landed.
	releaseEnabled, err := k.SettlementReleaseEnabled(ctx)
	if err != nil {
		return types.SlotEntitlement{}, err
	}
	if !releaseEnabled {
		return types.SlotEntitlement{}, types.ErrUnsupportedFeature.Wrap("rewards release is paused")
	}

	entitlement, found, err := k.GetSlotEntitlement(ctx, slotID, epoch)
	if err != nil {
		return types.SlotEntitlement{}, err
	}
	if !found {
		return types.SlotEntitlement{}, types.ErrInvalidState.Wrapf(
			"no entitlement exists for slot %d in epoch %d", slotID, epoch)
	}
	return entitlement, nil
}

// resolvedPayout is one payout line after its recipient and amount have been
// admitted, aggregated across every line naming the same destination.
type resolvedPayout struct {
	address sdk.AccAddress
	amount  sdkmath.Int
}

// resolvePayoutSet admits every line and aggregates by destination.
//
// Aggregation matters beyond tidiness: a set that names one recipient twice must
// produce one transfer of the summed amount, not two. Two sends would be two
// account touches for one authorization, and would make the observable effect of
// a payout set depend on how the caller chose to split it.
func (k Keeper) resolvePayoutSet(
	slotID, epoch uint64, payouts []types.EntitlementPayout,
) (map[string]resolvedPayout, sdkmath.Int, error) {
	grouped := make(map[string]resolvedPayout, len(payouts))
	total := sdkmath.ZeroInt()

	for i, payout := range payouts {
		amount, err := types.ParseCanonicalPayoutAmount(
			payoutLabel(slotID, epoch, i)+" amount", payout.Amount)
		if err != nil {
			return nil, sdkmath.Int{}, err
		}
		// Participant payouts are strictly positive (§34). A zero line would be an
		// authorization to touch an account without moving value, which on a feeless
		// chain is a free account-creation primitive.
		if !amount.IsPositive() {
			return nil, sdkmath.Int{}, types.ErrInvalidState.Wrapf(
				"%s amount must be positive", payoutLabel(slotID, epoch, i))
		}
		address, err := k.economicAddresses.Validate(payout.Recipient)
		if err != nil {
			return nil, sdkmath.Int{}, types.ErrInvalidAddress.Wrapf(
				"%s recipient: %v", payoutLabel(slotID, epoch, i), err)
		}

		sum, err := total.SafeAdd(amount)
		if err != nil {
			return nil, sdkmath.Int{}, types.ErrInvalidState.Wrapf(
				"payout set for slot %d in epoch %d overflows: %v", slotID, epoch, err)
		}
		total = sum

		// Keyed by the CANONICAL re-encoding of the parsed address, not by the
		// caller's string. Grouping on the input would let two spellings of one
		// destination be treated as two recipients, which is the same class of
		// hazard the strict amount parser closes on the other field.
		key := address.String()
		existing, seen := grouped[key]
		if !seen {
			grouped[key] = resolvedPayout{address: address, amount: amount}
			continue
		}
		combined, err := existing.amount.SafeAdd(amount)
		if err != nil {
			return nil, sdkmath.Int{}, types.ErrInvalidState.Wrapf(
				"payout set for slot %d in epoch %d overflows for one recipient: %v", slotID, epoch, err)
		}
		grouped[key] = resolvedPayout{address: existing.address, amount: combined}
	}
	return grouped, total, nil
}

// assertReleaseCeiling proves the entitlement bound for a whole payout set.
//
// This is rewards' own ceiling and is enforced on rewards' own state. A future
// x/mining will additionally bound a chunk by the settlement's derived
// participant-distribution ceiling; that is a narrower limit layered on top and
// never a substitute, because it lives in a module that does not own the escrow.
func (k Keeper) assertReleaseCeiling(entitlement types.SlotEntitlement, requested sdkmath.Int) error {
	amount, err := entitlement.Amount()
	if err != nil {
		return err
	}
	released, err := entitlement.Released()
	if err != nil {
		return err
	}
	next, err := released.SafeAdd(requested)
	if err != nil {
		return types.ErrInvalidState.Wrapf(
			"released amount for slot %d in epoch %d overflows: %v",
			entitlement.SlotId, entitlement.Epoch, err)
	}
	if next.GT(amount) {
		return types.ErrInvalidState.Wrapf(
			"releasing %s against slot %d in epoch %d would take the released total to %s, above the entitlement of %s",
			requested, entitlement.SlotId, entitlement.Epoch, next, amount)
	}
	return nil
}

// recordRelease advances released_amount and lowers the outstanding liability by
// exactly the value transferred.
//
// Only the released amount is rewritten. The record is re-read from the store
// rather than reusing the caller's copy so the write is applied to what is
// actually there, and every other field is carried through unchanged — a release
// must not be able to restate the amount that bounds it, or the destination it
// was bounded toward.
func (k Keeper) recordRelease(ctx context.Context, entitlement types.SlotEntitlement, released sdkmath.Int) error {
	current, err := entitlement.Released()
	if err != nil {
		return err
	}
	next, err := current.SafeAdd(released)
	if err != nil {
		return types.ErrInvalidState.Wrapf(
			"released amount for slot %d in epoch %d overflows: %v",
			entitlement.SlotId, entitlement.Epoch, err)
	}
	updated := entitlement
	updated.ReleasedAmount = next.String()
	if err := updated.Validate(); err != nil {
		return err
	}
	if err := k.SlotEntitlements.Set(
		ctx, entitlementKey(updated.Epoch, updated.SlotId), updated,
	); err != nil {
		return err
	}
	return k.subOutstandingLiability(ctx, released)
}

// subOutstandingLiability decreases the accumulator by a released amount.
//
// An underflow here is not an arithmetic edge case: it means the module has just
// paid out more than it believed it owed, so the accumulator and the entitlements
// had already diverged before this call. It fails closed rather than clamping at
// zero, which would hide the divergence behind a plausible number.
func (k Keeper) subOutstandingLiability(ctx context.Context, amount sdkmath.Int) error {
	current, err := k.GetOutstandingEntitlementLiability(ctx)
	if err != nil {
		return err
	}
	next, err := current.SafeSub(amount)
	if err != nil {
		return types.ErrInvalidState.Wrapf(
			"releasing %s would drop the outstanding entitlement liability of %s below zero: %v",
			amount, current, err)
	}
	return k.SetOutstandingEntitlementLiability(ctx, next)
}

// sortedRecipients returns the grouped destinations in deterministic order.
//
// Go map iteration is randomized, and the order transfers execute in is consensus
// state: it decides which send fails first when one of them must, and therefore
// what every node concludes about the block.
func sortedRecipients(grouped map[string]resolvedPayout) []string {
	recipients := make([]string, 0, len(grouped))
	for recipient := range grouped {
		recipients = append(recipients, recipient)
	}
	sort.Strings(recipients)
	return recipients
}

func payoutLabel(slotID, epoch uint64, index int) string {
	return fmt.Sprintf("payout line %d for slot %d in epoch %d", index, slotID, epoch)
}
