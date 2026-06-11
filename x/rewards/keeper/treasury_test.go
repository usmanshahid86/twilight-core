package keeper_test

import (
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/twilight-project/twilight-core/x/rewards/keeper"
)

func TestEmissionTreasuryAmount(t *testing.T) {
	amount, err := keeper.ComputeEmissionTreasuryAmount(math.NewInt(101), 1_000)
	require.NoError(t, err)
	require.Equal(t, "10", amount.String())
	amount, err = keeper.ComputeEmissionTreasuryAmount(math.NewInt(101), 0)
	require.NoError(t, err)
	require.True(t, amount.IsZero())
}

func TestTreasuryPaymentAndFailureAtomicity(t *testing.T) {
	t.Run("exact payment", func(t *testing.T) {
		k, ctx, bank, _ := setupFinalization(t, true)
		params, err := k.GetParams(ctx)
		require.NoError(t, err)
		params.EmissionTreasuryShareBps = 1_000
		params.TreasuryAddress = addr(8)
		require.NoError(t, k.SetParams(ctx, params))
		cfg, err := keeper.BuildEpochConfigSnapshot(params)
		require.NoError(t, err)
		require.NoError(t, k.SetCurrentEpochConfig(ctx, cfg))

		require.NoError(t, k.EndBlock(ctx.WithBlockHeight(2)))
		require.Len(t, bank.sends, 1)
		require.Equal(t, "2utwlt", bank.sends[0].amounts.String())
		epoch, found, err := k.GetFinalizedEpoch(ctx, 1)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, "2", epoch.TreasuryAmount)
	})

	t.Run("send failure", func(t *testing.T) {
		k, ctx, bank, _ := setupFinalization(t, true)
		params, err := k.GetParams(ctx)
		require.NoError(t, err)
		params.EmissionTreasuryShareBps = 1_000
		params.TreasuryAddress = addr(8)
		require.NoError(t, k.SetParams(ctx, params))
		cfg, err := keeper.BuildEpochConfigSnapshot(params)
		require.NoError(t, err)
		require.NoError(t, k.SetCurrentEpochConfig(ctx, cfg))
		bank.failSend()

		require.Error(t, k.EndBlock(ctx.WithBlockHeight(2)))
		_, found, err := k.GetFinalizedEpoch(ctx, 1)
		require.NoError(t, err)
		require.False(t, found)
	})
}
