package keeper

import (
	"context"

	"cosmossdk.io/collections"

	"github.com/twilight-project/twilight-core/x/coreslot/types"
)

// initialPolicyVersion is the version number of the policy every slot is
// registered with. Later versions are created by policy updates, which this
// change does not implement.
const initialPolicyVersion uint64 = 1

// policyKey is the canonical (slot_id, policy_version) key for the immutable
// policy history.
func policyKey(slotID, version uint64) collections.Pair[uint64, uint64] {
	return collections.Join(slotID, version)
}

// writeInitialPolicy validates a caller-supplied initial policy and stores it as
// version 1, effective from validFrom.
//
// The caller supplies only the two operator-selectable values. slot_id,
// policy_version and the validity window are assigned here from consensus state,
// so a registration transaction cannot choose its own version numbering or
// backdate a validity window. Version 1 is current, so its exclusive end is the
// zero sentinel.
func (k Keeper) writeInitialPolicy(ctx context.Context, slotID uint64, validFrom int64, input *types.InitialSelectionPolicy) error {
	if input == nil {
		return types.ErrInvalidSelectionPolicy.Wrap("an initial selection policy is required")
	}
	if err := types.ValidateSelectionPolicyValues(input.SelectionRateBps, input.MaxSelectedParticipants); err != nil {
		return err
	}
	return k.SelectionPolicies.Set(ctx, policyKey(slotID, initialPolicyVersion), types.SelectionPolicyVersion{
		SlotId:                    slotID,
		PolicyVersion:             initialPolicyVersion,
		SelectionRateBps:          input.SelectionRateBps,
		MaxSelectedParticipants:   input.MaxSelectedParticipants,
		ValidFromHeight:           validFrom,
		ValidUntilHeightExclusive: 0,
	})
}

// currentPolicy returns the policy version a slot currently points at, failing
// closed if the pointer names a version that does not exist or a row whose stored
// identity disagrees with its key.
func (k Keeper) currentPolicy(ctx context.Context, slot types.CoreSlot) (types.SelectionPolicyVersion, error) {
	if slot.CurrentSelectionPolicyVersion == 0 {
		return types.SelectionPolicyVersion{}, types.ErrInvalidSelectionPolicy.Wrapf("slot %d has no current policy version", slot.SlotId)
	}
	policy, err := k.SelectionPolicies.Get(ctx, policyKey(slot.SlotId, slot.CurrentSelectionPolicyVersion))
	if err != nil {
		return types.SelectionPolicyVersion{}, types.ErrInvalidSelectionPolicy.Wrapf(
			"slot %d points at missing policy version %d", slot.SlotId, slot.CurrentSelectionPolicyVersion)
	}
	if policy.SlotId != slot.SlotId || policy.PolicyVersion != slot.CurrentSelectionPolicyVersion {
		return types.SelectionPolicyVersion{}, types.ErrInvalidSelectionPolicy.Wrapf(
			"slot %d policy row identity does not match its key", slot.SlotId)
	}
	return policy, nil
}

// validateActiveSlotInvariant checks the §18 requirements that must hold of an
// ACTIVE slot record.
//
// It is applied to the CANDIDATE POST-TRANSITION record, not to the row as it
// stands before activation. A first activation is performed on a PENDING row
// whose activation sequence is still the never-activated sentinel, so validating
// the pre-transition record against an ACTIVE-only invariant would reject every
// first activation.
//
// Address roles are not interchangeable here. The payout and settlement
// addresses are value destinations and take the full economic rule; the operator
// address is a control identity and is only required to be a valid account
// address, so a bank-blocked operator remains able to run a slot the protocol
// never sends value to.
func (k Keeper) validateActiveSlotInvariant(ctx context.Context, slot types.CoreSlot) error {
	if slot.Status != types.SlotStatus_SLOT_STATUS_ACTIVE {
		return types.ErrInvalidTransition.Wrapf("slot %d is not active", slot.SlotId)
	}
	if _, err := k.economicAddresses.ParseAccountAddress(slot.OperatorAddress); err != nil {
		return types.ErrInvalidAddress.Wrapf("slot %d operator address: %v", slot.SlotId, err)
	}
	if _, err := k.economicAddresses.Validate(slot.PayoutAddress); err != nil {
		return types.ErrInvalidAddress.Wrapf("slot %d payout address: %v", slot.SlotId, err)
	}
	if _, err := k.economicAddresses.Validate(slot.SettlementAddress); err != nil {
		return types.ErrInvalidAddress.Wrapf("slot %d settlement address: %v", slot.SlotId, err)
	}
	if _, _, err := consensusKey(slot.ConsensusPubkey); err != nil {
		return err
	}
	if slot.ActivationSequence == 0 {
		return types.ErrInvalidTransition.Wrapf("active slot %d must have a nonzero activation sequence", slot.SlotId)
	}
	if slot.ActivationEffectiveHeight == 0 {
		return types.ErrInvalidTransition.Wrapf("active slot %d must have a nonzero activation effective height", slot.SlotId)
	}
	policy, err := k.currentPolicy(ctx, slot)
	if err != nil {
		return err
	}
	// §27 LOCAL validity only. Whether the policy must also sit inside a global
	// operational SelectionParams envelope is unresolved (B5/Y-4) and is not
	// decided here; x/coreslot cannot see those parameters and must not read
	// x/mining to find them.
	if err := types.ValidateSelectionPolicyValues(policy.SelectionRateBps, policy.MaxSelectedParticipants); err != nil {
		return err
	}
	return nil
}
