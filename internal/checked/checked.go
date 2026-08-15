// Package checked provides overflow-safe fixed-width integer arithmetic for
// consensus paths.
//
// Consensus code must never rely on Go's wrapping overflow. A wrapped value is
// committed identically by every node, so it is indistinguishable from correct
// execution: the chain does not halt, it silently agrees on the wrong number.
// Every helper here detects the condition before producing a result and returns
// a sentinel error instead.
//
// The helpers return bare sentinel errors rather than formatted ones. That keeps
// both the success and failure paths allocation-free and deterministic. Callers
// add context using their own module error:
//
//	end, err := checked.AddUint64(startHeight, epochLength-1)
//	if err != nil {
//	    return 0, types.ErrInvalidState.Wrapf("epoch end height: %v", err)
//	}
//
// Scope is deliberately narrow: the fixed-width operations consensus paths
// actually perform, and the conversions between the uint64 heights held in
// module state and the int64 heights supplied by the SDK context. It is not a
// general arithmetic framework.
//
// Arbitrary-precision amounts do not belong here. cosmossdk.io/math.Int already
// provides bounded safe arithmetic (SafeAdd, SafeSub, SafeMul, SafeQuo,
// SafeMod); use those directly rather than wrapping them.
package checked

import (
	"errors"
	"math"
)

// Sentinel errors. Compare with errors.Is, or directly: these values are
// returned unwrapped so no allocation occurs on the failure path either.
var (
	// ErrOverflow reports that a result exceeds the maximum of its type.
	ErrOverflow = errors.New("checked: integer overflow")
	// ErrUnderflow reports that a result falls below the minimum of its type.
	ErrUnderflow = errors.New("checked: integer underflow")
	// ErrRange reports that a value cannot be represented in the target type.
	ErrRange = errors.New("checked: value out of range for target type")
)

// AddUint64 returns a+b, or ErrOverflow if the sum exceeds math.MaxUint64.
func AddUint64(a, b uint64) (uint64, error) {
	if a > math.MaxUint64-b {
		return 0, ErrOverflow
	}
	return a + b, nil
}

// SubUint64 returns a-b, or ErrUnderflow if b exceeds a. uint64 has no negative
// range, so any borrow is an error rather than a wrap to a huge positive value.
func SubUint64(a, b uint64) (uint64, error) {
	if a < b {
		return 0, ErrUnderflow
	}
	return a - b, nil
}

// MulUint64 returns a*b, or ErrOverflow if the product exceeds math.MaxUint64.
func MulUint64(a, b uint64) (uint64, error) {
	if a == 0 || b == 0 {
		return 0, nil
	}
	if a > math.MaxUint64/b {
		return 0, ErrOverflow
	}
	return a * b, nil
}

// AddInt64 returns a+b, or ErrOverflow/ErrUnderflow if the sum leaves the int64
// range. The two guards are split so the caller learns which end was breached.
func AddInt64(a, b int64) (int64, error) {
	if b > 0 && a > math.MaxInt64-b {
		return 0, ErrOverflow
	}
	if b < 0 && a < math.MinInt64-b {
		return 0, ErrUnderflow
	}
	return a + b, nil
}

// SubInt64 returns a-b, or ErrOverflow/ErrUnderflow if the difference leaves the
// int64 range.
func SubInt64(a, b int64) (int64, error) {
	if b < 0 && a > math.MaxInt64+b {
		return 0, ErrOverflow
	}
	if b > 0 && a < math.MinInt64+b {
		return 0, ErrUnderflow
	}
	return a - b, nil
}

// MulInt64 returns a*b, or ErrOverflow/ErrUnderflow if the product leaves the
// int64 range.
func MulInt64(a, b int64) (int64, error) {
	if a == 0 || b == 0 {
		return 0, nil
	}

	// math.MinInt64 * -1 is the one case the division check below cannot catch:
	// the wrapped product is math.MinInt64, and Go defines math.MinInt64 / -1 as
	// math.MinInt64, so the round-trip appears to succeed.
	if (a == math.MinInt64 && b == -1) || (b == math.MinInt64 && a == -1) {
		return 0, ErrOverflow
	}

	product := a * b
	if product/b != a {
		if (a > 0) == (b > 0) {
			return 0, ErrOverflow
		}
		return 0, ErrUnderflow
	}
	return product, nil
}

// Uint64FromInt64 converts an SDK-supplied int64 height to the uint64 form held
// in module state, or returns ErrRange if the value is negative. An unchecked
// conversion would turn a negative height into a very large positive one.
func Uint64FromInt64(v int64) (uint64, error) {
	if v < 0 {
		return 0, ErrRange
	}
	return uint64(v), nil
}

// Int64FromUint64 converts a uint64 held in module state to the int64 form the
// SDK expects, or returns ErrRange if the value exceeds math.MaxInt64.
func Int64FromUint64(v uint64) (int64, error) {
	if v > math.MaxInt64 {
		return 0, ErrRange
	}
	return int64(v), nil
}
