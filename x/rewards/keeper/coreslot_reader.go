package keeper

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"

	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// SlotRewardSnapshot is CoreSlot state as of the moment an entitlement is
// created: the end of the closing block, after that block's transactions have
// executed (§32).
//
// The payout address is the only field that becomes a value destination. Status
// and activation sequence are carried for the entitlement's audit fields and
// decide nothing monetary — V2.2 has no activation_participation[], and lifecycle
// generation cannot confiscate or enlarge an entitlement that was already earned.
type SlotRewardSnapshot struct {
	SlotID             uint64
	OperatorAddress    sdk.AccAddress
	PayoutAddress      sdk.AccAddress
	Status             coreslottypes.SlotStatus
	ActivationSequence uint64
}

func (k Keeper) GetActiveSlotSnapshots(ctx context.Context) ([]SlotRewardSnapshot, error) {
	slots, err := k.coreSlotKeeper.GetActiveSlots(ctx)
	if err != nil {
		return nil, err
	}
	snapshots := make([]SlotRewardSnapshot, 0, len(slots))
	for _, slot := range slots {
		if slot.Status != coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE {
			continue
		}
		snapshot, err := k.slotRewardSnapshot(ctx, slot)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

func (k Keeper) GetSlotRewardSnapshot(ctx context.Context, slotID uint64) (SlotRewardSnapshot, error) {
	slot, err := k.coreSlotKeeper.GetSlot(ctx, slotID)
	if err != nil {
		return SlotRewardSnapshot{}, err
	}
	// The record has to be the record that was asked for.
	//
	// This is not a redundant restatement of the lookup key. x/rewards asks for a
	// Slot by number and reads a payout address out of the answer; if the answer
	// declares a different Slot, the address that comes back belongs to somebody
	// else, and it is then written into an immutable entitlement for the Slot that
	// earned the money. That is a silent redirection of value, and no later check
	// can detect it — the entitlement is internally consistent, the amount is right,
	// and the destination is a perfectly valid address.
	//
	// Checked here rather than trusted to x/coreslot because this is the boundary
	// where a stored value becomes a payment destination, and the point of a
	// boundary is that it does not depend on the other side being correct.
	if slot.SlotId != slotID {
		return SlotRewardSnapshot{}, types.ErrInvalidState.Wrapf(
			"reward snapshot for slot %d received a core slot record declaring slot %d",
			slotID, slot.SlotId)
	}
	return k.slotRewardSnapshot(ctx, slot)
}

func (k Keeper) slotRewardSnapshot(ctx context.Context, slot coreslottypes.CoreSlot) (SlotRewardSnapshot, error) {
	// This is the §25 entitlement payout-snapshot boundary: the point at which a
	// stored CoreSlot address becomes a reward destination. CoreSlot admission
	// should already have guaranteed these values, so the check is defensive —
	// but a snapshot is exactly where a value that entered state before this rule
	// existed, or through some future path, would otherwise be laundered into a
	// payout.
	//
	// Only the payout address is a destination. The operator address is carried
	// on the snapshot as identity and is parsed, not economically validated; the
	// parsed forms are reused rather than decoded again.
	operator, err := k.economicAddresses.ParseAccountAddress(slot.OperatorAddress)
	if err != nil {
		return SlotRewardSnapshot{}, types.ErrInvalidAddress.Wrapf("slot %d operator address: %v", slot.SlotId, err)
	}
	payout, err := k.economicAddresses.Validate(slot.PayoutAddress)
	if err != nil {
		return SlotRewardSnapshot{}, types.ErrInvalidAddress.Wrapf("slot %d payout address: %v", slot.SlotId, err)
	}
	return SlotRewardSnapshot{
		SlotID:             slot.SlotId,
		OperatorAddress:    operator,
		PayoutAddress:      payout,
		Status:             slot.Status,
		ActivationSequence: slot.ActivationSequence,
	}, nil
}
