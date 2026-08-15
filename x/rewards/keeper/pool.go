package keeper

import (
	"cosmossdk.io/math"

	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// ComputeRewardPoolV2 returns the V2 epoch reward pool:
//
//	pool = minted_emission - treasury + carry_in
//
// # Why this is separate from the current finalization path
//
// The pool expression used by the current epoch-finalization path is written
// inline and takes a fee input, because V1 folds collected fees into the pool.
// The V2 relation has no fee term at all. The two therefore cannot share one
// function without one of them changing behavior, so this helper stands beside
// the existing path rather than replacing it.
//
// It is deliberately NOT wired into finalization. Rewiring the live path is a
// consensus change and belongs to the change that introduces V2 epoch
// finalization, not to the change that makes the arithmetic executable. Until
// then this function has exactly one caller: the reward conformance vectors.
//
// # Purity
//
// Arithmetic only. No keeper state, no bank call, no context, no ABCI effect.
// Amounts are arbitrary-precision base-denomination values and are never
// narrowed through a fixed-width type; a pool above math.MaxUint64 is a
// legitimate value rather than an overflow.
//
// # Failure
//
// The function fails closed rather than returning a wrong number. An
// uninitialized or negative input is rejected, and so is a treasury amount that
// exceeds the emission it is taken from: that would make the pool negative,
// which is not a small accounting error but a claim that the epoch owes more
// than it minted. The protocol keeps the treasury share strictly below 100%
// precisely so this cannot arise, so reaching it means an invariant upstream has
// already broken and continuing would commit the damage.
func ComputeRewardPoolV2(mintedEmission, treasuryAmount, carryIn math.Int) (math.Int, error) {
	for _, operand := range []struct {
		name  string
		value math.Int
	}{
		{"minted emission", mintedEmission},
		{"treasury amount", treasuryAmount},
		{"carry in", carryIn},
	} {
		// IsNil first: the zero value of math.Int carries a nil big.Int and
		// panics on a sign query.
		if operand.value.IsNil() {
			return math.Int{}, types.ErrInvalidState.Wrapf("%s must not be nil", operand.name)
		}
		if operand.value.IsNegative() {
			return math.Int{}, types.ErrInvalidState.Wrapf("%s must not be negative", operand.name)
		}
	}

	if treasuryAmount.GT(mintedEmission) {
		return math.Int{}, types.ErrInvalidState.Wrapf(
			"treasury amount %s exceeds minted emission %s", treasuryAmount, mintedEmission,
		)
	}

	distributable, err := mintedEmission.SafeSub(treasuryAmount)
	if err != nil {
		return math.Int{}, types.ErrInvalidState.Wrapf("emission less treasury: %v", err)
	}
	pool, err := distributable.SafeAdd(carryIn)
	if err != nil {
		return math.Int{}, types.ErrInvalidState.Wrapf("distributable plus carry in: %v", err)
	}

	return pool, nil
}
