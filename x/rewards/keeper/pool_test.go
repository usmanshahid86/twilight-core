package keeper_test

import (
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/twilight-project/twilight-core/x/rewards/keeper"
)

func TestComputeRewardPoolV2(t *testing.T) {
	cases := []struct {
		name           string
		mintedEmission string
		treasury       string
		carryIn        string
		want           string
	}{
		{"treasury and carry", "1000000", "100000", "7", "900007"},
		{"no treasury", "250", "0", "3", "253"},
		{"no carry", "250", "25", "0", "225"},
		{"nothing minted", "0", "0", "11", "11"},
		{"treasury takes everything", "1000", "1000", "5", "5"},
		// Amounts are arbitrary precision: a pool far above math.MaxUint64 is a
		// legitimate value, not an overflow.
		{
			"beyond uint64",
			"340282366920938463463374607431768211456",
			"40282366920938463463374607431768211456",
			"1",
			"300000000000000000000000000000000000001",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pool, err := keeper.ComputeRewardPoolV2(
				mustAmount(t, tc.mintedEmission),
				mustAmount(t, tc.treasury),
				mustAmount(t, tc.carryIn),
			)
			require.NoError(t, err)
			require.Equal(t, tc.want, pool.String())
		})
	}
}

func TestComputeRewardPoolV2FailsClosed(t *testing.T) {
	valid := math.NewInt(100)

	cases := []struct {
		name                              string
		mintedEmission, treasury, carryIn math.Int
	}{
		// The zero value of math.Int carries a nil big.Int; it must be rejected
		// before any sign query, which would panic on it.
		{"uninitialized emission", math.Int{}, valid, valid},
		{"uninitialized treasury", valid, math.Int{}, valid},
		{"uninitialized carry", valid, valid, math.Int{}},

		{"negative emission", math.NewInt(-1), math.ZeroInt(), math.ZeroInt()},
		{"negative treasury", valid, math.NewInt(-1), math.ZeroInt()},
		{"negative carry", valid, math.ZeroInt(), math.NewInt(-1)},

		// A treasury share larger than the emission it is taken from would make
		// the pool negative — a claim that the epoch owes more than it minted.
		{"treasury exceeds emission", math.NewInt(100), math.NewInt(101), math.ZeroInt()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.NotPanics(t, func() {
				_, err := keeper.ComputeRewardPoolV2(tc.mintedEmission, tc.treasury, tc.carryIn)
				require.Error(t, err)
			})
		})
	}
}

// TestComputeRewardPoolV2HasNoFeeTerm pins the difference from the V1 pool
// expression, which folds collected fees in.
//
// The expected value is written as a literal rather than recomputed from the
// arguments: restating the formula in the test would assert only that the
// function agrees with the test's own copy of it.
func TestComputeRewardPoolV2HasNoFeeTerm(t *testing.T) {
	pool, err := keeper.ComputeRewardPoolV2(math.NewInt(1_000), math.NewInt(100), math.NewInt(7))
	require.NoError(t, err)

	// 1000 minted, 100 to treasury, 7 carried in. A fee-inclusive implementation
	// would need a fourth input and would report more than this for the same epoch.
	require.Equal(t, "907", pool.String())
}
