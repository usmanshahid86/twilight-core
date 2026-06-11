package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/x/coreslot/keeper"
	"github.com/twilight-project/twilight-core/x/coreslot/types"
	rewardskeeper "github.com/twilight-project/twilight-core/x/rewards/keeper"
)

var _ rewardskeeper.CoreSlotKeeper = keeper.Keeper{}

func TestReadAPIActiveSlotsSortedAndFiltered(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	params := types.DefaultParams(authority, emergency)
	operators := []string{
		sdk.AccAddress(append([]byte{2}, make([]byte, 19)...)).String(),
		sdk.AccAddress(append([]byte{3}, make([]byte, 19)...)).String(),
		sdk.AccAddress(append([]byte{4}, make([]byte, 19)...)).String(),
	}
	_, err := k.InitGenesis(ctx, &types.GenesisState{
		Params: &params,
		Slots: []*types.CoreSlot{
			slot(t, 3, operators[2], 3, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
			slot(t, 2, operators[1], 2, types.SlotStatus_SLOT_STATUS_INACTIVE, 0),
			slot(t, 1, operators[0], 1, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
		},
		RewardWeights: []*types.OperatorRewardWeight{
			{SlotId: 1, FinalWeight: "1.000000000000000000"},
			{SlotId: 2, FinalWeight: "2.000000000000000000"},
			{SlotId: 3, FinalWeight: "3.000000000000000000"},
		},
		NextSlotId: 4,
	})
	require.NoError(t, err)
	slotOne, err := k.GetSlot(ctx, 1)
	require.NoError(t, err)
	slotOne.ConsensusPower = 202
	require.NoError(t, k.Slots.Set(ctx, 1, slotOne))

	before, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	active, err := k.GetActiveSlots(ctx)
	require.NoError(t, err)
	require.Equal(t, []uint64{1, 3}, []uint64{active[0].SlotId, active[1].SlotId})
	after, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Equal(t, before, after, "read methods must not mutate CoreSlot state")

	weight, err := k.GetRewardWeight(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, "1.000000000000000000", weight.FinalWeight)
	require.Equal(t, int64(202), active[0].ConsensusPower, "reward-weight reads must not alter consensus power")
}

func TestReadAPISlotAuthoritiesAndMissingWeight(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	params := types.DefaultParams(authority, emergency)
	operator := sdk.AccAddress(append([]byte{2}, make([]byte, 19)...)).String()
	_, err := k.InitGenesis(ctx, &types.GenesisState{
		Params:     &params,
		Slots:      []*types.CoreSlot{slot(t, 1, operator, 1, types.SlotStatus_SLOT_STATUS_ACTIVE, 1)},
		NextSlotId: 2,
	})
	require.NoError(t, err)

	gotAuthority, err := k.GetAuthority(ctx)
	require.NoError(t, err)
	require.Equal(t, authority, gotAuthority)
	gotEmergency, err := k.GetEmergencyAuthority(ctx)
	require.NoError(t, err)
	require.Equal(t, emergency, gotEmergency)

	gotSlot, err := k.GetSlot(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(1), gotSlot.SlotId)
	_, err = k.GetSlot(ctx, 99)
	require.ErrorIs(t, err, types.ErrSlotNotFound)

	_, err = k.GetRewardWeight(ctx, 1)
	require.ErrorIs(t, err, collections.ErrNotFound)
}
