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
	return k.writePolicyVersion(ctx, types.SelectionPolicyVersion{
		SlotId:                    slotID,
		PolicyVersion:             initialPolicyVersion,
		SelectionRateBps:          input.SelectionRateBps,
		MaxSelectedParticipants:   input.MaxSelectedParticipants,
		ValidFromHeight:           validFrom,
		ValidUntilHeightExclusive: 0,
	})
}

// policyStartKey is the canonical (slot_id, valid_from_height) seek-index key.
func policyStartKey(slotID uint64, validFrom int64) collections.Pair[uint64, int64] {
	return collections.Join(slotID, validFrom)
}

// writePolicyVersion stores a policy version and its seek-index entry together.
//
// Every path that creates a version goes through here — runtime registration,
// fresh genesis and policy updates — so the index cannot be complete for some
// creation paths and missing for others. A version written without its index
// entry would be invisible to height resolution while still being current, which
// is precisely the divergence the resolver refuses to paper over.
func (k Keeper) writePolicyVersion(ctx context.Context, policy types.SelectionPolicyVersion) error {
	if err := k.SelectionPolicies.Set(ctx, policyKey(policy.SlotId, policy.PolicyVersion), policy); err != nil {
		return err
	}
	return k.PolicyStarts.Set(ctx, policyStartKey(policy.SlotId, policy.ValidFromHeight), policy.PolicyVersion)
}

// SelectionPolicyAtHeight resolves the policy version whose half-open validity
// interval contains height, using the seek index rather than a history scan.
//
// The index gives the greatest valid_from_height <= height for the slot; the
// canonical row it names is then loaded and checked to actually contain the
// height. Both steps are required: the index answers "which version started most
// recently", and only the row itself can say whether that version was still
// current at the requested height.
//
// Absence of a predecessor is an ordinary not-found — a height before the slot's
// first version simply has no policy. Any DISAGREEMENT between index and history
// is different in kind and fails closed: the index is derived state, so a
// mismatch means the two have diverged, and silently repairing it or falling back
// to a full history walk would hide that while defeating the bound the index
// exists to provide.
func (k Keeper) SelectionPolicyAtHeight(ctx context.Context, slotID uint64, height int64) (types.SelectionPolicyVersion, error) {
	// Prefixed by slot, so the range can never cross into another slot's keys, and
	// bounded above by the requested height so the first descending entry is the
	// greatest valid_from_height <= height.
	rng := collections.NewPrefixedPairRange[uint64, int64](slotID).
		EndInclusive(height).
		Descending()

	iter, err := k.PolicyStarts.Iterate(ctx, rng)
	if err != nil {
		return types.SelectionPolicyVersion{}, err
	}
	defer iter.Close()

	if !iter.Valid() {
		return types.SelectionPolicyVersion{}, types.ErrSelectionPolicyNotFound.Wrapf(
			"slot %d has no selection policy at height %d", slotID, height)
	}
	key, err := iter.Key()
	if err != nil {
		return types.SelectionPolicyVersion{}, err
	}
	version, err := iter.Value()
	if err != nil {
		return types.SelectionPolicyVersion{}, err
	}

	policy, err := k.SelectionPolicies.Get(ctx, policyKey(slotID, version))
	if err != nil {
		return types.SelectionPolicyVersion{}, types.ErrInvalidSelectionPolicy.Wrapf(
			"slot %d policy index names missing version %d", slotID, version)
	}
	if policy.SlotId != slotID || policy.PolicyVersion != version || policy.ValidFromHeight != key.K2() {
		return types.SelectionPolicyVersion{}, types.ErrInvalidSelectionPolicy.Wrapf(
			"slot %d policy index entry disagrees with version %d", slotID, version)
	}
	if policy.ValidFromHeight > height {
		return types.SelectionPolicyVersion{}, types.ErrInvalidSelectionPolicy.Wrapf(
			"slot %d policy version %d starts after height %d", slotID, version, height)
	}
	if policy.ValidUntilHeightExclusive != 0 && height >= policy.ValidUntilHeightExclusive {
		return types.SelectionPolicyVersion{}, types.ErrInvalidSelectionPolicy.Wrapf(
			"slot %d policy version %d does not contain height %d", slotID, version, height)
	}
	return policy, nil
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
