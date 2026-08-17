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
		// The treasury share and destination are set on the canonical reward
		// configuration, which is what finalization reads. Setting them on Params
		// would change nothing: that mirror carries no authority.
		seedTreasuryRewardConfig(t, k, ctx, 1_000, addr(8))

		// 10% of a 3600 emission.
		require.NoError(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))
		require.Len(t, bank.sends, 1)
		require.Equal(t, "360utwlt", bank.sends[0].amounts.String())
		epoch, found, err := k.GetFinalizedEpoch(ctx, 1)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, "360", epoch.TreasuryAmount)
	})

	t.Run("send failure", func(t *testing.T) {
		k, ctx, bank, _ := setupFinalization(t, true)
		seedTreasuryRewardConfig(t, k, ctx, 1_000, addr(8))
		bank.failSend()

		require.Error(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))
		_, found, err := k.GetFinalizedEpoch(ctx, 1)
		require.NoError(t, err)
		require.False(t, found)
	})
}
