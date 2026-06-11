package keeper_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/twilight-project/twilight-core/x/rewards/keeper"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

var (
	_ func(math.Int, math.Int) (uint64, error)                                                  = keeper.HalvingTier
	_ func(math.Int, math.Int) (math.Int, bool, error)                                          = keeper.NextHalvingThreshold
	_ func(math.Int, math.Int, math.Int) (math.Int, error)                                      = keeper.NextBlockSubsidy
	_ func(math.Int, uint64, math.Int, math.Int, types.HalvingMode) (math.Int, math.Int, error) = keeper.ComputeEpochEmission
)

func TestEmissionMathAlwaysRespectsThresholdAndSupplyCaps(t *testing.T) {
	maxSupply := math.NewInt(100)
	initialSubsidy := math.NewInt(80)

	for cumulative := int64(0); cumulative <= 100; cumulative++ {
		current := math.NewInt(cumulative)
		subsidy, err := keeper.NextBlockSubsidy(current, maxSupply, initialSubsidy)
		require.NoError(t, err)
		require.False(t, subsidy.IsNegative())
		require.True(t, current.Add(subsidy).LTE(maxSupply))

		nextThreshold, found, err := keeper.NextHalvingThreshold(current, maxSupply)
		require.NoError(t, err)
		if found {
			require.True(t, current.Add(subsidy).LTE(nextThreshold))
		}
	}
}

func TestEmissionMathIsIsolatedPureCode(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	require.True(t, ok)

	source, err := os.ReadFile(filepath.Join(filepath.Dir(currentFile), "emission.go"))
	require.NoError(t, err)

	for _, forbidden := range []string{
		"context.Context",
		"time.Now",
		"BlockTime",
		"os.Getenv",
		"rand.",
		"Keeper)",
	} {
		require.NotContains(t, string(source), forbidden)
	}
}
