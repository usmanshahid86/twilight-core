package types

import (
	sdkmath "cosmossdk.io/math"
)

// Shape validation for the canonical entitlement record.
//
// Relational rules live elsewhere: that the referenced reward-configuration
// version exists is a keeper check, because only the keeper can read the history;
// that the payout destination is admissible is also a keeper check, because only
// the keeper holds the canonical economic-address rule.
//
// What is checked here is the record's internal coherence — the part that is
// wrong on its own terms regardless of what else the store contains.

// Validate checks one entitlement against the invariants that must hold for its
// whole life.
//
// The amount is required POSITIVE, not merely non-negative. A zero-amount
// entitlement is never persisted (§31): it would be an obligation to pay nothing,
// and later settlement materialization would have to create a workflow row for it
// and then finalize it with no transfer. Refusing it at the writer is what keeps
// that state unrepresentable rather than merely unusual.
func (e SlotEntitlement) Validate() error {
	if e.SlotId == 0 {
		return ErrInvalidState.Wrap("entitlement requires a nonzero slot")
	}
	if e.Epoch == 0 {
		return ErrInvalidState.Wrapf("entitlement for slot %d requires a nonzero epoch", e.SlotId)
	}
	if e.RewardConfigVersion == 0 {
		return ErrInvalidState.Wrapf(
			"entitlement for slot %d in epoch %d must record the reward configuration version it was bound to",
			e.SlotId, e.Epoch)
	}
	if e.CreatedHeight == 0 {
		return ErrInvalidState.Wrapf(
			"entitlement for slot %d in epoch %d must record a positive creation height", e.SlotId, e.Epoch)
	}
	if e.TotalBlocksActive == 0 {
		return ErrInvalidState.Wrapf(
			"entitlement for slot %d in epoch %d must record positive participation", e.SlotId, e.Epoch)
	}

	amount, err := e.Amount()
	if err != nil {
		return err
	}
	if !amount.IsPositive() {
		return ErrInvalidState.Wrapf(
			"entitlement for slot %d in epoch %d must be positive; zero-amount entitlements are not persisted",
			e.SlotId, e.Epoch)
	}
	released, err := e.Released()
	if err != nil {
		return err
	}
	if released.GT(amount) {
		return ErrInvalidState.Wrapf(
			"entitlement for slot %d in epoch %d has released %s of %s",
			e.SlotId, e.Epoch, released, amount)
	}
	return nil
}

// Amount parses the immutable entitlement amount.
func (e SlotEntitlement) Amount() (sdkmath.Int, error) {
	amount, err := ParseAmountString("entitlement amount", e.EntitlementAmount)
	if err != nil {
		return sdkmath.Int{}, ErrInvalidState.Wrap(err.Error())
	}
	return amount, nil
}

// Released parses the mutable released amount.
func (e SlotEntitlement) Released() (sdkmath.Int, error) {
	released, err := ParseAmountString("released amount", e.ReleasedAmount)
	if err != nil {
		return sdkmath.Int{}, ErrInvalidState.Wrap(err.Error())
	}
	return released, nil
}

// Remaining returns the unreleased value of this entitlement.
//
// Computed rather than stored, so it cannot disagree with the two fields it is
// derived from. The subtraction is safe because Validate refuses a record whose
// released amount exceeds its entitlement, and every read path validates before
// reaching here.
func (e SlotEntitlement) Remaining() (sdkmath.Int, error) {
	amount, err := e.Amount()
	if err != nil {
		return sdkmath.Int{}, err
	}
	released, err := e.Released()
	if err != nil {
		return sdkmath.Int{}, err
	}
	remaining, err := amount.SafeSub(released)
	if err != nil {
		return sdkmath.Int{}, ErrInvalidState.Wrapf(
			"entitlement for slot %d in epoch %d has released more than it owes: %v", e.SlotId, e.Epoch, err)
	}
	return remaining, nil
}
