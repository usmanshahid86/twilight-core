package keeper_test

import (
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/twilight-project/twilight-core/x/rewards/keeper"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

func TestAllocateUniformActiveBlocks(t *testing.T) {
	snapshots := map[uint64]keeper.SlotRewardSnapshot{
		1: {SlotID: 1, OperatorAddress: mustAddress(1), PayoutAddress: mustAddress(2), RewardWeight: math.LegacyMustNewDecFromStr("7")},
		2: {SlotID: 2, OperatorAddress: mustAddress(3), PayoutAddress: mustAddress(4), RewardWeight: math.LegacyMustNewDecFromStr("9")},
	}
	rows := []types.SlotActiveBlocks{{SlotId: 2, BlocksActive: 3}, {SlotId: 1, BlocksActive: 1}}

	rewards, allocated, carry, err := keeper.AllocateUniformActiveBlocks(5, math.NewInt(10), rows, snapshots)
	require.NoError(t, err)
	require.Equal(t, []uint64{1, 2}, []uint64{rewards[0].SlotId, rewards[1].SlotId})
	require.Equal(t, []string{"2", "7"}, []string{rewards[0].Amount, rewards[1].Amount})
	require.Equal(t, "1", carry.String())
	require.Equal(t, "9", allocated.String())
	require.Equal(t, "1", rewards[0].EffectiveWeight)
	require.Equal(t, addr(1), rewards[0].OperatorAddress)
	require.Equal(t, addr(2), rewards[0].PayoutAddress)
}

func TestAllocateUniformActiveBlocksZeroWeightCarriesPool(t *testing.T) {
	rewards, allocated, carry, err := keeper.AllocateUniformActiveBlocks(
		1,
		math.NewInt(12),
		[]types.SlotActiveBlocks{{SlotId: 1}},
		nil,
	)
	require.NoError(t, err)
	require.Empty(t, rewards)
	require.True(t, allocated.IsZero())
	require.Equal(t, "12", carry.String())
}

func mustAddress(marker byte) []byte {
	address := make([]byte, 20)
	address[0] = marker
	return address
}
