package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/twilight-project/twilight-core/x/coreslot/types"
)

// TestGenesisInitialValidatorSetCarryoverHeightsOneAndTwo ports the experiment's
// carryover check: the Core-Slot-defined genesis validator set must survive the
// first two EndBlocks unchanged. Validator-set bugs commonly surface not at
// height 0 but at heights 1-2 when EndBlock updates first apply.
func TestGenesisInitialValidatorSetCarryoverHeightsOneAndTwo(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	params := types.DefaultParams(authority, emergency) // SlotVotingPower=1, Min=1, Max=100
	ops := makeOps(10, 4)

	updates, err := initGenesis(t, k, ctx, &types.GenesisState{Params: &params, Slots: []*types.CoreSlot{
		slot(t, 1, ops[0], 1, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
		slot(t, 2, ops[1], 2, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
		slot(t, 3, ops[2], 3, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
		slot(t, 4, ops[3], 4, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
	}, NextSlotId: 5})
	require.NoError(t, err)

	// Initial validator updates match the active slots, all at SlotVotingPower.
	require.Len(t, updates, 4)
	for _, u := range updates {
		require.Equal(t, params.SlotVotingPower, u.Power)
	}

	initialLastApplied := lastAppliedBySlot(t, k, ctx)
	require.Len(t, initialLastApplied, 4)

	for _, height := range []int64{1, 2} {
		hctx := ctx.WithBlockHeight(height)
		upd, err := k.EndBlock(hctx)
		require.NoError(t, err)
		require.Emptyf(t, upd, "height %d must not emit unplanned validator updates", height)

		// LastApplied must be byte-for-byte stable across the carryover heights.
		require.Equalf(t, initialLastApplied, lastAppliedBySlot(t, k, hctx),
			"LastApplied must remain consistent at height %d", height)

		// Every genesis slot stays active at SlotVotingPower.
		for id := uint64(1); id <= 4; id++ {
			s, err := k.Slots.Get(hctx, id)
			require.NoError(t, err)
			require.Truef(t, s.Status == types.SlotStatus_SLOT_STATUS_ACTIVE, "slot %d must remain active at height %d", id, height)
			require.Equal(t, params.SlotVotingPower, s.ConsensusPower)
		}
	}
}
