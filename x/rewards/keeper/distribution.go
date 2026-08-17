package keeper

import (
	"sort"

	"cosmossdk.io/math"

	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// Canonical V2 entitlement allocation (§31).
//
//	W        = Σ blocks_active_s over Slots with positive participation
//	share(s) = floor(pool * blocks_active_s / W)
//
// W is the participation total, NOT reward_enabled_blocks. The two are easy to
// confuse and mean different things: reward_enabled_blocks scales the epoch's
// emission, while W is the denominator that divides the resulting pool among the
// Slots that earned it. Using the block count here would pay out less than the
// pool whenever any Slot was inactive for part of the epoch, and silently move
// the difference into carry.
//
// The arithmetic below is unchanged from the version certified by the reward
// conformance vectors. Only the record it produces changed: entitlements rather
// than claim-shaped rows.

// AllocateSlotEntitlements divides the pool across participating Slots and
// returns the entitlements to persist, the total allocated, the carry remainder,
// and the number of participating Slots.
//
// n_pos is returned rather than recomputed by the caller because it is the
// denominator of the residue bound, and a caller that derived it from the
// returned entitlements would count wrong: a Slot whose share floors to zero
// participated, and is not persisted.
func AllocateSlotEntitlements(
	epoch uint64,
	pool math.Int,
	rows []types.SlotActiveBlocks,
	snapshots map[uint64]SlotRewardSnapshot,
	rewardConfigVersion uint64,
	createdHeight uint64,
) ([]types.SlotEntitlement, math.Int, math.Int, uint64, error) {
	if pool.IsNil() || pool.IsNegative() {
		return nil, math.Int{}, math.Int{}, 0, types.ErrInvalidState.Wrap("reward pool must be nonnegative")
	}
	// Ascending slot_id. The order decides which Slot absorbs nothing when the
	// pool does not divide evenly, so it is part of the consensus result and not a
	// presentation choice.
	sort.Slice(rows, func(i, j int) bool { return rows[i].SlotId < rows[j].SlotId })

	totalWeight := math.ZeroInt()
	nPos := uint64(0)
	for _, row := range rows {
		if row.BlocksActive > 0 {
			totalWeight = totalWeight.Add(math.NewIntFromUint64(row.BlocksActive))
			nPos++
		}
	}
	if totalWeight.IsZero() {
		// An unpaused epoch with no participating Slot still consumes its emission
		// into carry (§31). Mass inactivation is not an emission-pause mechanism;
		// REWARDS_PAUSED is.
		return nil, math.ZeroInt(), pool, 0, nil
	}

	entitlements := make([]types.SlotEntitlement, 0, len(rows))
	allocated := math.ZeroInt()
	for _, row := range rows {
		if row.BlocksActive == 0 {
			continue
		}
		snapshot, ok := snapshots[row.SlotId]
		if !ok {
			return nil, math.Int{}, math.Int{}, 0, types.ErrInvalidState.Wrapf(
				"missing reward snapshot for slot %d", row.SlotId)
		}
		weight := math.NewIntFromUint64(row.BlocksActive)
		amount := pool.Mul(weight).Quo(totalWeight)
		// Counted before the zero check: a Slot whose share floors to zero still
		// contributed to the division, and allocated is the sum of every share.
		allocated = allocated.Add(amount)
		if amount.IsZero() {
			// Zero-amount entitlements are not persisted (§31).
			continue
		}
		entitlements = append(entitlements, types.SlotEntitlement{
			SlotId:                         row.SlotId,
			Epoch:                          epoch,
			TotalBlocksActive:              row.BlocksActive,
			EntitlementAmount:              amount.String(),
			ReleasedAmount:                 "0",
			PayoutAddress:                  snapshot.PayoutAddress.String(),
			RewardConfigVersion:            rewardConfigVersion,
			SlotStatusAtEpochClose:         snapshot.Status,
			ActivationSequenceAtEpochClose: snapshot.ActivationSequence,
			CreatedHeight:                  createdHeight,
		})
	}

	return entitlements, allocated, pool.Sub(allocated), nPos, nil
}
