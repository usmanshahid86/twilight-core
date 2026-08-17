package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// Canonical entitlement state and the outstanding-liability accumulator.
//
// An entitlement is the chain's record that it owes a Slot a specific amount for
// a specific closed epoch. Two properties make it different from ordinary module
// state and shape everything below:
//
//   - it is created once and never recreated. A second creation for the same
//     (epoch, slot_id) is not an update, it is a second obligation over the same
//     escrow, so it is refused rather than overwritten.
//   - exactly one field moves after creation. released_amount is monotonic and
//     bounded by entitlement_amount; every other field, including the payout
//     destination, is fixed at the moment the epoch closed.
//
// The liability accumulator mirrors the sum of what is unreleased. It is stored
// because solvency is asserted on the block path and the definitional sum is a
// full scan, and it is maintained in the same cached transitions that create and
// release entitlements so the two cannot commit apart.

// entitlementKey builds the canonical (epoch, slot_id) key.
func entitlementKey(epoch, slotID uint64) collections.Pair[uint64, uint64] {
	return collections.Join(epoch, slotID)
}

// validateEntitlementRecord checks a stored record against the key it was found
// under and against its own invariants.
func validateEntitlementRecord(key collections.Pair[uint64, uint64], entitlement types.SlotEntitlement) error {
	if entitlement.Epoch != key.K1() || entitlement.SlotId != key.K2() {
		return types.ErrInvalidState.Wrapf(
			"entitlement stored at epoch %d slot %d declares epoch %d slot %d",
			key.K1(), key.K2(), entitlement.Epoch, entitlement.SlotId)
	}
	return entitlement.Validate()
}

// GetSlotEntitlement returns the canonical entitlement for one Slot and epoch,
// reporting whether one exists.
//
// Absence is ordinary: most (epoch, slot) pairs have none, because a Slot that
// did not participate, or whose share floored to zero, is not owed anything.
// Anything else is corruption and never presents as absence — a release that read
// a decode failure as "no entitlement" would refuse a legitimate payout, and one
// that read it as an empty record would compute a ceiling of zero.
func (k Keeper) GetSlotEntitlement(
	ctx context.Context, slotID, epoch uint64,
) (types.SlotEntitlement, bool, error) {
	if slotID == 0 || epoch == 0 {
		return types.SlotEntitlement{}, false, types.ErrInvalidState.Wrap(
			"entitlement lookup requires a nonzero slot and epoch")
	}
	key := entitlementKey(epoch, slotID)
	entitlement, err := k.SlotEntitlements.Get(ctx, key)
	if errors.Is(err, collections.ErrNotFound) {
		return types.SlotEntitlement{}, false, nil
	}
	if err != nil {
		return types.SlotEntitlement{}, false, types.ErrInvalidState.Wrapf(
			"entitlement for slot %d in epoch %d could not be read: %v", slotID, epoch, err)
	}
	if err := validateEntitlementRecord(key, entitlement); err != nil {
		return types.SlotEntitlement{}, false, err
	}
	return entitlement, true, nil
}

// IterateEntitlementsForEpoch returns every entitlement of one epoch in ascending
// slot_id order.
//
// The ordering is a property of the key, not of a sort applied afterwards: the
// pair codec is big-endian, so a prefix range over the epoch yields slots in
// ascending numeric order. Callers that allocate money across this sequence
// depend on that, and so will settlement materialization.
func (k Keeper) IterateEntitlementsForEpoch(ctx context.Context, epoch uint64) ([]types.SlotEntitlement, error) {
	if epoch == 0 {
		return nil, types.ErrInvalidState.Wrap("epoch numbers start at 1")
	}
	rows := make([]types.SlotEntitlement, 0)
	err := k.SlotEntitlements.Walk(ctx, collections.NewPrefixedPairRange[uint64, uint64](epoch),
		func(key collections.Pair[uint64, uint64], entitlement types.SlotEntitlement) (bool, error) {
			if err := validateEntitlementRecord(key, entitlement); err != nil {
				return true, err
			}
			rows = append(rows, entitlement)
			return false, nil
		})
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// CreateSlotEntitlement writes one new obligation and increases the outstanding
// liability by exactly its amount.
//
// # Atomicity is this boundary's own property
//
// The contract is that the row and the liability move together or not at all, and
// that has to hold for a caller who did not think to open a cache. Without one,
// the Set below can succeed and the accounting step after it can fail, leaving an
// obligation the module does not know it owes — which is precisely the state the
// solvency assertion exists to catch, and which it would then be unable to catch
// because the recorded liability looks consistent with itself.
//
// So the cache is opened here. The finalization loop already runs inside one and
// nests harmlessly; what it must not do is make this guarantee contingent on
// every future caller remembering it.
//
// # Why the governing configuration is resolved here and passed down
//
// Referential integrity is checked through the EPOCH's binding rather than by
// looking a bare version number up. Resolving a version number means walking a
// history keyed by effective epoch, and that walk grows with the number of
// accepted configuration changes. It also happens to be the weaker claim: "some
// version with this number exists" is not what makes the record auditable — naming
// the version that actually governed its epoch is.
//
// One resolution per call is correct for a standalone caller and wrong for the
// finalizer, which creates many entitlements for ONE epoch and would repeat the
// identical lookup for each. See createSlotEntitlement.
func (k Keeper) CreateSlotEntitlement(ctx context.Context, entitlement types.SlotEntitlement) error {
	governing, err := k.RewardConfigForTarget(ctx, entitlement.Epoch)
	if err != nil {
		return err
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	if err := k.createSlotEntitlement(cacheCtx, entitlement, governing); err != nil {
		return err
	}
	write()
	return nil
}

// createSlotEntitlement is the raw creation step, against an ALREADY-RESOLVED
// governing configuration and inside a cache the caller owns.
//
// Splitting it out is what keeps epoch close O(participants) rather than
// O(participants x versions): the governing configuration is a property of the
// epoch, not of the row, so the finalizer resolves it once and every entitlement
// of that epoch is proven against the same value. Nothing here reads the
// reward-configuration history, so no amount of chain age changes what this costs.
//
// Referential integrity is not weakened by moving the read out. The stored record
// must still name the version that governed its epoch; the only change is who
// establishes what that version is.
func (k Keeper) createSlotEntitlement(
	ctx context.Context, entitlement types.SlotEntitlement, governing types.RewardConfigVersion,
) error {
	if err := entitlement.Validate(); err != nil {
		return err
	}
	released, err := entitlement.Released()
	if err != nil {
		return err
	}
	if !released.IsZero() {
		return types.ErrInvalidState.Wrapf(
			"entitlement for slot %d in epoch %d is created with %s already released",
			entitlement.SlotId, entitlement.Epoch, released)
	}
	// The destination is admitted as a value destination at creation, which is the
	// moment the snapshot becomes permanent. Later releases revalidate immediately
	// before transfer; this is the check that stops an inadmissible address from
	// being written down as immutable in the first place.
	if err := k.validatePayoutDestination("entitlement", entitlement.PayoutAddress); err != nil {
		return err
	}
	// The binding the caller resolved has to be the binding for THIS record's epoch.
	// Passing a configuration in makes the argument a place a mismatch could enter,
	// so the argument is checked rather than trusted.
	if governing.EffectiveEpoch > entitlement.Epoch {
		return types.ErrInvalidState.Wrapf(
			"entitlement for slot %d in epoch %d was offered a reward configuration effective at epoch %d",
			entitlement.SlotId, entitlement.Epoch, governing.EffectiveEpoch)
	}
	if governing.Version != entitlement.RewardConfigVersion {
		return types.ErrInvalidState.Wrapf(
			"entitlement for slot %d in epoch %d records reward configuration version %d, but epoch %d is governed by version %d",
			entitlement.SlotId, entitlement.Epoch, entitlement.RewardConfigVersion,
			entitlement.Epoch, governing.Version)
	}

	// Write-once is enforced by an explicit existence check rather than by relying
	// on the caller. Overwriting would silently replace one obligation with another
	// over the same escrow, and the difference between the two amounts would never
	// appear in the liability — the accumulator would still be carrying the old
	// figure.
	key := entitlementKey(entitlement.Epoch, entitlement.SlotId)
	exists, err := k.SlotEntitlements.Has(ctx, key)
	if err != nil {
		return types.ErrInvalidState.Wrapf(
			"entitlement for slot %d in epoch %d could not be checked: %v",
			entitlement.SlotId, entitlement.Epoch, err)
	}
	if exists {
		return types.ErrInvalidState.Wrapf(
			"entitlement for slot %d in epoch %d already exists and is immutable",
			entitlement.SlotId, entitlement.Epoch)
	}

	amount, err := entitlement.Amount()
	if err != nil {
		return err
	}
	if err := k.SlotEntitlements.Set(ctx, key, entitlement); err != nil {
		return err
	}
	return k.addOutstandingLiability(ctx, amount)
}

// GetOutstandingEntitlementLiability returns the unreleased value the module owes.
//
// There is deliberately no default. Genesis writes it explicitly, so after
// initialization an absent or unparseable value is corruption. Defaulting it to
// zero would report the module as owing nothing while entitlements sit unpaid in
// the store, and every solvency assertion built on it would then pass.
func (k Keeper) GetOutstandingEntitlementLiability(ctx context.Context) (sdkmath.Int, error) {
	raw, err := k.OutstandingEntitlementLiability.Get(ctx)
	if err != nil {
		return sdkmath.Int{}, types.ErrInvalidState.Wrapf(
			"outstanding entitlement liability could not be read: %v", err)
	}
	amount, err := types.ParseAmountString("outstanding entitlement liability", raw)
	if err != nil {
		return sdkmath.Int{}, types.ErrInvalidState.Wrap(err.Error())
	}
	return amount, nil
}

// SetOutstandingEntitlementLiability writes the accumulator.
func (k Keeper) SetOutstandingEntitlementLiability(ctx context.Context, amount sdkmath.Int) error {
	if amount.IsNil() {
		return types.ErrInvalidState.Wrap("outstanding entitlement liability must not be nil")
	}
	if amount.IsNegative() {
		return types.ErrInvalidState.Wrapf(
			"outstanding entitlement liability would become negative (%s)", amount)
	}
	return k.OutstandingEntitlementLiability.Set(ctx, amount.String())
}

// addOutstandingLiability increases the accumulator by a newly created obligation.
func (k Keeper) addOutstandingLiability(ctx context.Context, amount sdkmath.Int) error {
	current, err := k.GetOutstandingEntitlementLiability(ctx)
	if err != nil {
		return err
	}
	next, err := current.SafeAdd(amount)
	if err != nil {
		return types.ErrInvalidState.Wrapf("outstanding entitlement liability overflows: %v", err)
	}
	return k.SetOutstandingEntitlementLiability(ctx, next)
}

// SumOutstandingEntitlementLiability recomputes the liability definitionally.
//
// This is the full scan the O(1) accumulator exists to avoid, kept as the
// backstop that can prove the accumulator has not drifted. It is exported for
// tests and invariants; NOTHING on a block path or a release path may call it,
// because its cost grows with the number of live entitlements.
func (k Keeper) SumOutstandingEntitlementLiability(ctx context.Context) (sdkmath.Int, error) {
	total := sdkmath.ZeroInt()
	err := k.SlotEntitlements.Walk(ctx, nil,
		func(key collections.Pair[uint64, uint64], entitlement types.SlotEntitlement) (bool, error) {
			if err := validateEntitlementRecord(key, entitlement); err != nil {
				return true, err
			}
			remaining, err := entitlement.Remaining()
			if err != nil {
				return true, err
			}
			sum, err := total.SafeAdd(remaining)
			if err != nil {
				return true, types.ErrInvalidState.Wrapf("entitlement liability sum overflows: %v", err)
			}
			total = sum
			return false, nil
		})
	if err != nil {
		return sdkmath.Int{}, err
	}
	return total, nil
}
