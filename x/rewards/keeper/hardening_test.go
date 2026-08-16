package keeper_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	_ func(keeper.Keeper, context.Context) error                                                = keeper.Keeper.BeginBlock
	_ func(types.RewardsState, types.EpochConfigSnapshot) (uint64, error)                       = keeper.ConfiguredEpochEndHeight
	// ShouldFinalizeAtHeight is now a keeper method: the epoch end is derived from
	// canonical EpochConfigVersion history rather than from a passed-in snapshot,
	// and it takes no pause argument because finalization is unconditional.
	_ func(keeper.Keeper, context.Context, uint64) (bool, error) = keeper.Keeper.ShouldFinalizeAtHeight
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

func TestFinalizeCapAssertionPrecedesMint(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	require.True(t, ok)

	source, err := os.ReadFile(filepath.Join(filepath.Dir(currentFile), "finalize.go"))
	require.NoError(t, err)
	text := string(source)
	capAssertion := strings.Index(text, "cumulativeAfter.GT(maxSupply)")
	mint := strings.Index(text, "MintCoins")
	require.NotEqual(t, -1, capAssertion)
	require.NotEqual(t, -1, mint)
	require.Less(t, capAssertion, mint)
	require.NotContains(t, text, "SupplyCapInvariant")
}

func TestEconomicsFilesRemainKeeperOnly(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	dir := filepath.Dir(currentFile)
	for _, name := range []string{"endblock.go", "finalize.go", "distribution.go", "claims.go", "msg_server.go"} {
		source, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err)
		require.NotContains(t, string(source), "ValidatorUpdate")
		require.NotContains(t, string(source), "ConsensusPower")
	}
}

func TestBeginBlockHasNoPhaseFiveBehavior(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	require.True(t, ok)

	source, err := os.ReadFile(filepath.Join(filepath.Dir(currentFile), "beginblock.go"))
	require.NoError(t, err)

	for _, forbidden := range []string{
		"MintCoins",
		"SendCoinsFromModuleToAccount",
		"FinalizeEpoch",
		"Distribute",
		"Allocate",
		"ClaimRewards",
		"SetSlot",
		"ConsensusPower",
		"ValidatorUpdate",
		"time.Now",
		"BlockTime",
	} {
		require.NotContains(t, string(source), forbidden)
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
