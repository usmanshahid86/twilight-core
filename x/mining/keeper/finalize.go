package keeper

import (
	"context"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/x/mining/types"
	rewardstypes "github.com/twilight-project/twilight-core/x/rewards/types"
)

// Settlement finalization: the terminal OPEN -> FINALIZED transition.
//
// # What finalization is for
//
// A settlement is an obligation to release one entitlement. Participant chunks
// release part of it, at the settlement signer's discretion, possibly none of it.
// Finalization releases whatever is LEFT to the operator and closes the obligation
// for good. Without it an entitlement that was only partly distributed would sit in
// escrow forever.
//
// # What the caller decides, and what it cannot
//
// The caller decides only WHETHER the transition happens now. It cannot decide
// where the remainder goes, how much it is, which authorization arm applied, or
// what the settlement records afterwards. Every one of those is derived from
// canonical state, and the message has no field for any of them.
//
// That matters most for the permissionless arms. "Permissionless" means
// permissionless TRIGGER: after the deadline anyone may push a stuck settlement to
// terminal, and they gain no monetary discretion by doing so — the destination is
// the immutable payout snapshot taken when the epoch closed.
//
// # No automatic finalization
//
// There is no queue, no EndBlock sweep, no retry scheduler and no attempt budget.
// An open settlement may remain open indefinitely past its deadline until some
// caller submits the transaction. That is accepted liveness behavior, and the
// alternative — consensus paying money on a timer — is the thing the architecture
// most deliberately does not do.

// finalizedSettlement is the outcome of a completed transition, returned so the
// caller can report what happened without reading state back.
type finalizedSettlement struct {
	reason    types.SettlementFinalizationReason
	remainder sdkmath.Int
}

// FinalizeSettlement performs the terminal transition.
//
// # Atomicity
//
// The whole transition — authorization, remainder derivation, the operator release
// and the terminal metadata write — commits together or not at all. The cache is
// opened here for the same reason the chunk handler and the rewards release
// boundary open theirs: an atomicity guarantee that holds only because some outer
// layer happens to discard on error is not a guarantee.
//
// Here, unlike the chunk path, the cache is genuinely load-bearing. The remainder
// release commits its own inner cache into this one and the terminal write happens
// AFTER it, so a failure between the two would otherwise leave money moved against
// a settlement still open — and an open settlement can be finalized again, which
// would pay the operator twice. The ordering is deliberate in the other direction
// too: writing terminal metadata before the release succeeded would close an
// obligation that was never paid.
func (k Keeper) FinalizeSettlement(
	ctx context.Context, msg *types.MsgFinalizeSettlement,
) (types.SettlementFinalizationReason, sdkmath.Int, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	outcome, err := k.finalizeSettlement(cacheCtx, msg)
	if err != nil {
		return types.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_UNSPECIFIED,
			sdkmath.Int{}, err
	}
	write()
	return outcome.reason, outcome.remainder, nil
}

func (k Keeper) finalizeSettlement(
	ctx context.Context, msg *types.MsgFinalizeSettlement,
) (finalizedSettlement, error) {
	var zero finalizedSettlement
	if msg == nil {
		return zero, types.ErrInvalidState.Wrap("a settlement finalization message is required")
	}
	if err := msg.Validate(); err != nil {
		return zero, err
	}

	// The pause is checked FIRST, before any settlement state is read, exactly as
	// chunk admission checks it. Finalization moves value, so a paused chain refuses
	// it for a reason that has nothing to do with which settlement was named.
	//
	// There is deliberately no separate finalization-pause state: the settlement
	// clock already stops while paused, so a pause freezes deadlines rather than
	// consuming them, and a settlement that was authorized-signer-only before a
	// pause is still authorized-signer-only after it.
	releaseEnabled, err := k.rewardsKeeper.SettlementReleaseEnabled(ctx)
	if err != nil {
		return zero, types.ErrInvalidState.Wrapf(
			"settlement release state could not be read: %v", err)
	}
	if !releaseEnabled {
		return zero, types.ErrUnsupportedFeature.Wrap("settlement payout release is paused")
	}

	settlement, found, err := k.GetSettlement(ctx, msg.SlotId, msg.Epoch)
	if err != nil {
		return zero, err
	}
	if !found {
		return zero, types.ErrSettlementNotFound.Wrapf(
			"no settlement exists for slot %d in epoch %d", msg.SlotId, msg.Epoch)
	}
	// FINALIZED is terminal. A second finalization would release a remainder against
	// an obligation that has already been closed and paid.
	if settlement.Finalized {
		return zero, types.ErrInvalidState.Wrapf(
			"the settlement for slot %d in epoch %d is already finalized", msg.SlotId, msg.Epoch)
	}

	reason, err := k.resolveFinalizationArm(ctx, settlement, msg.Signer)
	if err != nil {
		return zero, err
	}

	remainder, err := k.releaseOperatorRemainder(ctx, settlement)
	if err != nil {
		return zero, err
	}

	if err := k.writeFinalizedSettlement(ctx, settlement, reason); err != nil {
		return zero, err
	}
	emitSettlementFinalized(ctx, settlement, reason, remainder)
	return finalizedSettlement{reason: reason, remainder: remainder}, nil
}

// resolveFinalizationArm decides which authorization arm applies, and therefore who
// may finalize and what the settlement records.
//
// # The reason follows the arm, never the caller
//
// This is the rule most easily got wrong. The recorded reason describes the
// authorization PATH the transition took, not the identity of whoever signed. So an
// authorized settlement signer finalizing after the deadline records
// PERMISSIONLESS_AFTER_DEADLINE — because the deadline had passed, the transition
// no longer needed that signer's authority, and recording otherwise would claim a
// permission was exercised that was not required. Likewise an authorized signer
// finalizing an operator-only settlement records PERMISSIONLESS_OPERATOR_ONLY.
//
// Nothing below inspects whether the caller happens to match the settlement
// credential except in the one arm where that authority is actually required.
//
// # Precedence
//
// Mode is evaluated before time. An operator-only settlement has no participant
// window to be inside or past, so it is finalizable immediately by anyone and the
// deadline never enters into it.
func (k Keeper) resolveFinalizationArm(
	ctx context.Context, settlement types.Settlement, signer string,
) (types.SettlementFinalizationReason, error) {
	unspecified := types.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_UNSPECIFIED

	// The epoch anchor is proven to EXIST and be canonical before any arm is
	// resolved, for every mode.
	//
	// It is mandatory companion state: a settlement exists only because its epoch
	// materialized at least one, and the two are created in the same transition. So
	// its absence means the pair came apart, and finalizing anyway would drive a
	// terminal money movement out of a settlement whose canonical state is already
	// known to be broken.
	//
	// This is deliberately a check on EXISTENCE and integrity, not on the anchor's
	// clock value. Whether the anchor describes an elapsed moment matters only where
	// something is derived from it, which is why that check lives on the path that
	// derives a deadline and not here.
	anchor, err := k.requireEpochAnchor(ctx, settlement.Epoch)
	if err != nil {
		return unspecified, err
	}

	// A total switch over every settlement mode. Admitting a finalization is a
	// monetary authorization, so a default arm would silently answer for a mode
	// nobody had considered. Two arms are unreachable in this profile and are
	// implemented anyway, so a later tranche adds a producer rather than a branch.
	switch settlement.SettlementMode {
	case types.SettlementMode_SETTLEMENT_MODE_OPERATOR_ONLY:
		// Arm A. The whole entitlement belongs to the operator, no participant window
		// exists, and there is nothing a deadline could protect. Any valid account may
		// trigger it, immediately.
		if err := k.requireValidSigner(signer); err != nil {
			return unspecified, err
		}
		return types.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_PERMISSIONLESS_OPERATOR_ONLY, nil
	case types.SettlementMode_SETTLEMENT_MODE_TRUSTED_AS,
		types.SettlementMode_SETTLEMENT_MODE_SELECTED_PARTICIPANTS:
	default:
		return unspecified, types.ErrInvalidState.Wrapf(
			"the settlement for slot %d in epoch %d has no canonical settlement mode",
			settlement.SlotId, settlement.Epoch)
	}

	deadline, clock, err := k.finalizationDeadline(ctx, settlement, anchor)
	if err != nil {
		return unspecified, err
	}

	if clock >= deadline {
		// Arm C. The participant window has closed, so no authority remains to
		// protect and anyone may push the settlement to terminal. Equality belongs
		// here and not to the early arm: at exactly the deadline chunks are already
		// permanently refused, so the window is over.
		if err := k.requireValidSigner(signer); err != nil {
			return unspecified, err
		}
		return types.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_PERMISSIONLESS_AFTER_DEADLINE, nil
	}

	// Arm B. The participant window is still open, so finalizing now forfeits the
	// remaining participant distribution. Only the settlement signer may decide
	// that, and it is allowed to do so having distributed nothing at all — releasing
	// the entire entitlement to the operator is within the authority model.
	if err := k.requireSettlementSigner(ctx, settlement, signer); err != nil {
		return unspecified, err
	}
	return types.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_AUTHORIZED_EARLY, nil
}

// finalizationDeadline resolves the deadline that decides the arm, and the clock it
// is measured against.
//
// The anchor is supplied already proven to exist and be canonical; what this adds is
// that it must describe a moment the chain has already reached, on the same terms as
// chunk admission and through the same rule. That distinction is the whole reason the
// two checks are separated: existence is mandatory for every mode, while the temporal
// property is consequential only where a deadline is derived. It matters here for a
// different reason: a future anchor pushes the deadline forward, which can flip a
// settlement from permissionless back to authorized-signer-only. That is an
// authorization failure even though the remainder destination is immutable — it
// hands one party a veto over a transition the protocol had already opened to
// everyone.
func (k Keeper) finalizationDeadline(
	ctx context.Context, settlement types.Settlement, anchor types.SettlementEpochAnchor,
) (deadline, clock uint64, err error) {
	params, err := k.SettlementParamsForTarget(ctx, settlement.Epoch)
	if err != nil {
		return 0, 0, err
	}
	if params.Version != settlement.SettlementParamsVersion {
		return 0, 0, types.ErrInvalidState.Wrapf(
			"the settlement for slot %d in epoch %d was created under settlement parameters version %d, "+
				"but epoch %d binds version %d",
			settlement.SlotId, settlement.Epoch, settlement.SettlementParamsVersion,
			settlement.Epoch, params.Version)
	}
	clock, err = k.GetSettlementClock(ctx)
	if err != nil {
		return 0, 0, err
	}
	if err := requireAnchorHasElapsed(settlement, anchor, clock); err != nil {
		return 0, 0, err
	}
	deadline, err = k.DeadlineClock(ctx, settlement, anchor, params)
	if err != nil {
		return 0, 0, err
	}
	return deadline, clock, nil
}

// requireValidSigner admits the permissionless arms.
//
// Account validity only, deliberately NOT the economic-destination rule that
// governs recipients and the settlement address. A finalization signer is a control
// identity: the protocol never sends anything to it, so refusing a module account
// or a bank-blocked account would deny a trigger the protocol permits while
// protecting nothing. The same distinction x/coreslot draws between an operator
// address and a payout address.
//
// "Permissionless" is not "unvalidated": an empty or malformed signer is still
// refused, because a transaction whose signer cannot be parsed has no authenticated
// caller at all.
func (k Keeper) requireValidSigner(signer string) error {
	if _, err := k.economicAddresses.ParseAccountAddress(signer); err != nil {
		return types.ErrInvalidAddress.Wrapf("settlement finalization signer: %v", err)
	}
	return nil
}

// releaseOperatorRemainder pays whatever is left of the entitlement to its
// immutable payout snapshot, and returns how much moved.
//
// # Canonical authority
//
// Every monetary value here comes from the entitlement in x/rewards. The settlement
// stores no released amount and the message carries none, so there is nothing a
// caller could have proposed. The destination is not passed either: the rewards
// boundary derives it from the snapshot taken when the epoch closed, so redirection
// is unrepresentable rather than rejected.
//
// Live CoreSlot lifecycle state is deliberately not consulted. An entitlement is
// earned by participation in a closed epoch, and letting a later suspension or
// removal redirect or withhold it would be confiscating value already earned.
func (k Keeper) releaseOperatorRemainder(
	ctx context.Context, settlement types.Settlement,
) (sdkmath.Int, error) {
	entitlement, err := k.requireEntitlementFor(ctx, settlement)
	if err != nil {
		return sdkmath.Int{}, err
	}
	remainder, err := remainingEntitlement(entitlement)
	if err != nil {
		return sdkmath.Int{}, err
	}

	if remainder.IsPositive() {
		if err := k.rewardsKeeper.PayEntitlementRemainderToOperator(
			ctx, settlement.SlotId, settlement.Epoch,
		); err != nil {
			return sdkmath.Int{}, err
		}
	}
	// A zero remainder finalizes as a monetary no-op: no release call, no bank
	// operation, and no account touched. That is a legitimate zero substep — the
	// entitlement was fully distributed to participants and nothing is owed. It is
	// NOT the participant-line rule, where zero is always invalid, because a
	// participant line with no value is a free account-creation primitive and an
	// absent operator payment is simply an absent payment.

	// The post-condition, proven from state rather than assumed from the call
	// succeeding: a finalized settlement's entitlement must be fully released. A
	// release that silently under-delivered would otherwise close the obligation
	// while leaving value stranded in escrow with nothing left able to claim it.
	if err := k.requireEntitlementFullyReleased(ctx, settlement); err != nil {
		return sdkmath.Int{}, err
	}
	return remainder, nil
}

// remainingEntitlement derives what is still owed, proving the entitlement is
// coherent before subtracting.
//
// The released <= amount check is not redundant with the subtraction. Arbitrary
// precision subtraction reports overflow, not sign: released > amount would produce
// a NEGATIVE remainder and no error at all, and a negative remainder would read as
// "nothing owed" at the zero test below it. The check is what turns that into a
// halt.
//
// x/rewards validates the same relation when it reads an entitlement, so this is
// unreachable through the real implementation. It is applied anyway because
// x/mining reaches entitlements through a narrow interface: this module must not
// depend on its dependency's internal validation to keep its own money arithmetic
// sound.
func remainingEntitlement(entitlement rewardstypes.SlotEntitlement) (sdkmath.Int, error) {
	amount, err := entitlement.Amount()
	if err != nil {
		return sdkmath.Int{}, types.ErrInvalidState.Wrap(err.Error())
	}
	released, err := entitlement.Released()
	if err != nil {
		return sdkmath.Int{}, types.ErrInvalidState.Wrap(err.Error())
	}
	if released.GT(amount) {
		return sdkmath.Int{}, types.ErrInvalidState.Wrapf(
			"the entitlement for slot %d in epoch %d has released %s of %s",
			entitlement.SlotId, entitlement.Epoch, released, amount)
	}
	remainder, err := amount.SafeSub(released)
	if err != nil {
		return sdkmath.Int{}, types.ErrInvalidState.Wrapf(
			"the remainder for slot %d in epoch %d could not be derived: %v",
			entitlement.SlotId, entitlement.Epoch, err)
	}
	return remainder, nil
}

// requireEntitlementFullyReleased re-reads the entitlement and proves the invariant
// a finalized settlement asserts.
//
// Re-read rather than reasoned about: the release wrote through the same cache, so
// this observes what actually committed instead of what the caller intended.
func (k Keeper) requireEntitlementFullyReleased(
	ctx context.Context, settlement types.Settlement,
) error {
	entitlement, err := k.requireEntitlementFor(ctx, settlement)
	if err != nil {
		return err
	}
	amount, err := entitlement.Amount()
	if err != nil {
		return types.ErrInvalidState.Wrap(err.Error())
	}
	released, err := entitlement.Released()
	if err != nil {
		return types.ErrInvalidState.Wrap(err.Error())
	}
	if !released.Equal(amount) {
		return types.ErrInvalidState.Wrapf(
			"finalizing slot %d in epoch %d would leave %s of %s unreleased",
			settlement.SlotId, settlement.Epoch, amount.Sub(released), amount)
	}
	return nil
}

// writeFinalizedSettlement stamps the terminal metadata and retires the OPEN index
// entry.
//
// The index removal is not bookkeeping. The OPEN index is rebuilt from canonical
// rows on import and deliberately skips finalized ones, so leaving an entry behind
// would make the derived index disagree with the settlements it describes — and a
// derived index that disagrees with canonical state is corruption, not a stale
// convenience.
//
// Only the three terminal fields change. Everything else is carried through from
// the row that was read and authorized: a finalization must not be able to restate
// the mode, the bound configuration versions or the chunk cursor that shaped the
// authorization it just passed.
func (k Keeper) writeFinalizedSettlement(
	ctx context.Context, settlement types.Settlement, reason types.SettlementFinalizationReason,
) error {
	height := sdk.UnwrapSDKContext(ctx).BlockHeight()
	if height <= 0 {
		return types.ErrInvalidState.Wrapf(
			"finalizing slot %d in epoch %d requires a positive block height, found %d",
			settlement.SlotId, settlement.Epoch, height)
	}

	finalized := settlement
	finalized.Finalized = true
	finalized.FinalizedHeight = uint64(height)
	finalized.FinalizationReason = reason
	// Validate covers the lifecycle/metadata agreement this transition must produce:
	// terminal rows carry both an arm and a height, open rows carry neither.
	if err := finalized.Validate(); err != nil {
		return err
	}

	key := settlementKey(finalized.SlotId, finalized.Epoch)
	if err := k.Settlements.Set(ctx, key, finalized); err != nil {
		return err
	}
	return k.OpenSettlementsBySlot.Remove(ctx, key)
}
