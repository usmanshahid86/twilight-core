package keeper

import (
	"sort"

	"cosmossdk.io/math"

	"github.com/twilight-project/twilight-core/x/rewards/types"
)

func AllocateUniformActiveBlocks(
	epoch uint64,
	pool math.Int,
	rows []types.SlotActiveBlocks,
	snapshots map[uint64]SlotRewardSnapshot,
) ([]types.EligibleSlotReward, math.Int, math.Int, error) {
	if pool.IsNil() || pool.IsNegative() {
		return nil, math.Int{}, math.Int{}, types.ErrInvalidState.Wrap("reward pool must be nonnegative")
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].SlotId < rows[j].SlotId })

	totalWeight := math.ZeroInt()
	for _, row := range rows {
		if row.BlocksActive > 0 {
			totalWeight = totalWeight.Add(math.NewIntFromUint64(row.BlocksActive))
		}
	}
	if totalWeight.IsZero() {
		return nil, math.ZeroInt(), pool, nil
	}

	rewards := make([]types.EligibleSlotReward, 0, len(rows))
	allocated := math.ZeroInt()
	for _, row := range rows {
		if row.BlocksActive == 0 {
			continue
		}
		snapshot, ok := snapshots[row.SlotId]
		if !ok {
			return nil, math.Int{}, math.Int{}, types.ErrInvalidState.Wrapf("missing reward snapshot for slot %d", row.SlotId)
		}
		weight := math.NewIntFromUint64(row.BlocksActive)
		amount := pool.Mul(weight).Quo(totalWeight)
		if amount.IsZero() {
			continue
		}
		rewards = append(rewards, types.EligibleSlotReward{
			SlotId:          row.SlotId,
			OperatorAddress: snapshot.OperatorAddress.String(),
			PayoutAddress:   snapshot.PayoutAddress.String(),
			BlocksActive:    row.BlocksActive,
			RewardWeight:    snapshot.RewardWeight.String(),
			EffectiveWeight: weight.String(),
			Amount:          amount.String(),
			EpochNumber:     epoch,
		})
		allocated = allocated.Add(amount)
	}

	return rewards, allocated, pool.Sub(allocated), nil
}
