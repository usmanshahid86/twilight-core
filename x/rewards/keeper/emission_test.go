package keeper_test

import (
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/twilight-project/twilight-core/x/rewards/keeper"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

func TestHalvingTierExactThresholds(t *testing.T) {
	maxSupply := emissionInt("21000000000000")

	testCases := []struct {
		name       string
		cumulative string
		expected   uint64
	}{
		{name: "genesis", cumulative: "0", expected: 0},
		{name: "before first threshold", cumulative: "10499999999999", expected: 0},
		{name: "first threshold", cumulative: "10500000000000", expected: 1},
		{name: "before second threshold", cumulative: "15749999999999", expected: 1},
		{name: "second threshold", cumulative: "15750000000000", expected: 2},
		{name: "third threshold", cumulative: "18375000000000", expected: 3},
		{name: "cap is terminal tier", cumulative: "21000000000000", expected: 44},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tier, err := keeper.HalvingTier(emissionInt(tc.cumulative), maxSupply)
			require.NoError(t, err)
			require.Equal(t, tc.expected, tier)
		})
	}
}

func TestNextHalvingThresholdExactValues(t *testing.T) {
	maxSupply := emissionInt("21000000000000")

	testCases := []struct {
		name       string
		cumulative string
		expected   string
		found      bool
	}{
		{name: "genesis", cumulative: "0", expected: "10500000000000", found: true},
		{name: "before first threshold", cumulative: "10499999999999", expected: "10500000000000", found: true},
		{name: "at first threshold", cumulative: "10500000000000", expected: "15750000000000", found: true},
		{name: "at second threshold", cumulative: "15750000000000", expected: "18375000000000", found: true},
		{name: "near cap", cumulative: "20999999999999", expected: "21000000000000", found: true},
		{name: "at cap", cumulative: "21000000000000", expected: "0", found: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			threshold, found, err := keeper.NextHalvingThreshold(emissionInt(tc.cumulative), maxSupply)
			require.NoError(t, err)
			require.Equal(t, tc.found, found)
			require.True(t, threshold.Equal(emissionInt(tc.expected)), "expected %s, got %s", tc.expected, threshold)
		})
	}
}

func TestNextBlockSubsidyExactValuesAndClipping(t *testing.T) {
	maxSupply := emissionInt("21000000000000")
	initialSubsidy := emissionInt("416190")

	testCases := []struct {
		name       string
		cumulative string
		expected   string
	}{
		{name: "tier zero", cumulative: "0", expected: "416190"},
		{name: "tier one", cumulative: "10500000000000", expected: "208095"},
		{name: "tier two", cumulative: "15750000000000", expected: "104047"},
		{name: "tier three", cumulative: "18375000000000", expected: "52023"},
		{name: "clip at first threshold", cumulative: "10499999999990", expected: "10"},
		{name: "terminal zero subsidy before cap", cumulative: "20999999999999", expected: "0"},
		{name: "zero at cap", cumulative: "21000000000000", expected: "0"},
	}

	subsidy, err := keeper.NextBlockSubsidy(math.NewInt(99), math.NewInt(100), math.NewInt(100))
	require.NoError(t, err)
	require.True(t, subsidy.Equal(math.OneInt()))

	subsidy, err = keeper.NextBlockSubsidy(math.ZeroInt(), math.NewInt(100), math.ZeroInt())
	require.NoError(t, err)
	require.True(t, subsidy.IsZero())

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			subsidy, err := keeper.NextBlockSubsidy(emissionInt(tc.cumulative), maxSupply, initialSubsidy)
			require.NoError(t, err)
			require.True(t, subsidy.Equal(emissionInt(tc.expected)), "expected %s, got %s", tc.expected, subsidy)
		})
	}
}

func TestComputeEpochEmissionExactValues(t *testing.T) {
	maxSupply := emissionInt("21000000000000")
	initialSubsidy := emissionInt("416190")
	mode := types.HalvingMode_HALVING_MODE_SUPPLY_THRESHOLD

	emission, cumulativeAfter, err := keeper.ComputeEpochEmission(math.ZeroInt(), 10, maxSupply, initialSubsidy, mode)
	require.NoError(t, err)
	require.True(t, emission.Equal(emissionInt("4161900")))
	require.True(t, cumulativeAfter.Equal(emissionInt("4161900")))

	emission, cumulativeAfter, err = keeper.ComputeEpochEmission(math.ZeroInt(), 17_280, maxSupply, initialSubsidy, mode)
	require.NoError(t, err)
	require.True(t, emission.Equal(emissionInt("7191763200")))
	require.True(t, cumulativeAfter.Equal(emissionInt("7191763200")))

	emission, cumulativeAfter, err = keeper.ComputeEpochEmission(emissionInt("10499999999990"), 2, maxSupply, initialSubsidy, mode)
	require.NoError(t, err)
	require.True(t, emission.Equal(emissionInt("208105")))
	require.True(t, cumulativeAfter.Equal(emissionInt("10500000208095")))

	emission, cumulativeAfter, err = keeper.ComputeEpochEmission(emissionInt("20999999999999"), 10, maxSupply, initialSubsidy, mode)
	require.NoError(t, err)
	require.True(t, emission.IsZero())
	require.True(t, cumulativeAfter.Equal(emissionInt("20999999999999")))

	emission, cumulativeAfter, err = keeper.ComputeEpochEmission(maxSupply, 17_280, maxSupply, initialSubsidy, mode)
	require.NoError(t, err)
	require.True(t, emission.IsZero())
	require.True(t, cumulativeAfter.Equal(maxSupply))

	emission, cumulativeAfter, err = keeper.ComputeEpochEmission(math.NewInt(93), 10, math.NewInt(100), math.NewInt(100), mode)
	require.NoError(t, err)
	require.True(t, emission.Equal(math.NewInt(7)))
	require.True(t, cumulativeAfter.Equal(math.NewInt(100)))
}

func TestComputeEpochEmissionCrossesMultipleThresholds(t *testing.T) {
	emission, cumulativeAfter, err := keeper.ComputeEpochEmission(
		math.ZeroInt(),
		3,
		math.NewInt(100),
		math.NewInt(80),
		types.HalvingMode_HALVING_MODE_SUPPLY_THRESHOLD,
	)
	require.NoError(t, err)
	require.True(t, emission.Equal(math.NewInt(88)))
	require.True(t, cumulativeAfter.Equal(math.NewInt(88)))
}

func TestEmissionMathRejectsInvalidInputs(t *testing.T) {
	validMax := math.NewInt(100)
	validSubsidy := math.NewInt(10)

	_, err := keeper.HalvingTier(math.NewInt(-1), validMax)
	require.Error(t, err)

	_, err = keeper.HalvingTier(math.ZeroInt(), math.NewInt(-1))
	require.Error(t, err)

	_, _, err = keeper.NextHalvingThreshold(math.NewInt(101), validMax)
	require.Error(t, err)

	_, err = keeper.NextBlockSubsidy(math.ZeroInt(), math.ZeroInt(), validSubsidy)
	require.Error(t, err)

	_, err = keeper.NextBlockSubsidy(math.ZeroInt(), validMax, math.NewInt(-1))
	require.Error(t, err)

	_, _, err = keeper.ComputeEpochEmission(
		math.ZeroInt(),
		1,
		validMax,
		validSubsidy,
		types.HalvingMode_HALVING_MODE_UNSPECIFIED,
	)
	require.Error(t, err)
}

func emissionInt(value string) math.Int {
	result, ok := math.NewIntFromString(value)
	if !ok {
		panic("invalid test integer: " + value)
	}
	return result
}
