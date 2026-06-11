package keeper

import (
	"context"
	"sort"

	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// BeginBlock credits block N to the CoreSlot active set observed before block N transactions.
func (k Keeper) BeginBlock(ctx context.Context) error {
	state, err := k.GetState(ctx)
	if err != nil {
		return err
	}
	if _, err := k.GetCurrentEpochConfig(ctx); err != nil {
		return err
	}

	slots, err := k.coreSlotKeeper.GetActiveSlots(ctx)
	if err != nil {
		return err
	}
	sort.Slice(slots, func(i, j int) bool {
		return slots[i].SlotId < slots[j].SlotId
	})

	for _, slot := range slots {
		if slot.SlotId == 0 {
			return types.ErrInvalidState.Wrap("CoreSlot active-set contract returned zero slot ID")
		}
		if slot.Status != coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE {
			return types.ErrInvalidState.Wrapf(
				"CoreSlot active-set contract returned non-active slot %d with status %s",
				slot.SlotId,
				slot.Status.String(),
			)
		}
	}

	for _, slot := range slots {
		if err := k.IncrementActiveBlocks(ctx, state.CurrentEpoch, slot.SlotId); err != nil {
			return err
		}
	}

	return nil
}
