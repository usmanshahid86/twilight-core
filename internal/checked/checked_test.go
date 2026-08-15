package checked_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/twilight-project/twilight-core/internal/checked"
)

func TestAddUint64(t *testing.T) {
	cases := []struct {
		name    string
		a, b    uint64
		want    uint64
		wantErr error
	}{
		{name: "zero plus zero", a: 0, b: 0, want: 0},
		{name: "zero plus one", a: 0, b: 1, want: 1},
		{name: "max plus zero stays at max", a: math.MaxUint64, b: 0, want: math.MaxUint64},
		{name: "exact boundary reaches max", a: math.MaxUint64 - 1, b: 1, want: math.MaxUint64},
		{name: "one past boundary overflows", a: math.MaxUint64, b: 1, wantErr: checked.ErrOverflow},
		{name: "overflow is symmetric in operands", a: 1, b: math.MaxUint64, wantErr: checked.ErrOverflow},
		{name: "max plus max overflows", a: math.MaxUint64, b: math.MaxUint64, wantErr: checked.ErrOverflow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := checked.AddUint64(tc.a, tc.b)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Zero(t, got, "failed operations must not return a partial result")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestSubUint64(t *testing.T) {
	cases := []struct {
		name    string
		a, b    uint64
		want    uint64
		wantErr error
	}{
		{name: "zero minus zero", a: 0, b: 0, want: 0},
		{name: "one minus zero", a: 1, b: 0, want: 1},
		{name: "exact boundary reaches zero", a: 1, b: 1, want: 0},
		{name: "max minus max", a: math.MaxUint64, b: math.MaxUint64, want: 0},
		{name: "one past boundary underflows", a: 0, b: 1, wantErr: checked.ErrUnderflow},
		{name: "large borrow underflows rather than wrapping", a: 0, b: math.MaxUint64, wantErr: checked.ErrUnderflow},
		{name: "near-miss borrow underflows", a: math.MaxUint64 - 1, b: math.MaxUint64, wantErr: checked.ErrUnderflow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := checked.SubUint64(tc.a, tc.b)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Zero(t, got)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestMulUint64(t *testing.T) {
	const halfMax = math.MaxUint64 / 2 // 2^63 - 1

	cases := []struct {
		name    string
		a, b    uint64
		want    uint64
		wantErr error
	}{
		{name: "zero times max", a: 0, b: math.MaxUint64, want: 0},
		{name: "max times zero", a: math.MaxUint64, b: 0, want: 0},
		{name: "one times max", a: 1, b: math.MaxUint64, want: math.MaxUint64},
		{name: "max times one", a: math.MaxUint64, b: 1, want: math.MaxUint64},
		{name: "exact boundary", a: 2, b: halfMax, want: math.MaxUint64 - 1},
		{name: "one past boundary overflows", a: 2, b: halfMax + 1, wantErr: checked.ErrOverflow},
		{name: "max times two overflows", a: math.MaxUint64, b: 2, wantErr: checked.ErrOverflow},
		{name: "max times max overflows", a: math.MaxUint64, b: math.MaxUint64, wantErr: checked.ErrOverflow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := checked.MulUint64(tc.a, tc.b)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Zero(t, got)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestAddInt64(t *testing.T) {
	cases := []struct {
		name    string
		a, b    int64
		want    int64
		wantErr error
	}{
		{name: "zero plus zero", a: 0, b: 0, want: 0},
		{name: "max plus zero stays at max", a: math.MaxInt64, b: 0, want: math.MaxInt64},
		{name: "min plus zero stays at min", a: math.MinInt64, b: 0, want: math.MinInt64},
		{name: "exact upper boundary", a: math.MaxInt64 - 1, b: 1, want: math.MaxInt64},
		{name: "exact lower boundary", a: math.MinInt64 + 1, b: -1, want: math.MinInt64},
		{name: "opposite extremes cancel", a: math.MaxInt64, b: math.MinInt64, want: -1},
		{name: "one past upper boundary overflows", a: math.MaxInt64, b: 1, wantErr: checked.ErrOverflow},
		{name: "one past lower boundary underflows", a: math.MinInt64, b: -1, wantErr: checked.ErrUnderflow},
		{name: "min plus min underflows", a: math.MinInt64, b: math.MinInt64, wantErr: checked.ErrUnderflow},
		{name: "max plus max overflows", a: math.MaxInt64, b: math.MaxInt64, wantErr: checked.ErrOverflow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := checked.AddInt64(tc.a, tc.b)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Zero(t, got)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestSubInt64(t *testing.T) {
	cases := []struct {
		name    string
		a, b    int64
		want    int64
		wantErr error
	}{
		{name: "zero minus zero", a: 0, b: 0, want: 0},
		{name: "max minus zero stays at max", a: math.MaxInt64, b: 0, want: math.MaxInt64},
		{name: "min minus zero stays at min", a: math.MinInt64, b: 0, want: math.MinInt64},
		{name: "exact lower boundary", a: -1, b: math.MaxInt64, want: math.MinInt64},
		{name: "exact upper boundary", a: 0, b: -math.MaxInt64, want: math.MaxInt64},
		{name: "min minus negative one", a: math.MinInt64, b: -1, want: math.MinInt64 + 1},
		// Regression: subtracting the most negative value reaches the top of the
		// range exactly. Guards a future refactor of the b < 0 branch.
		{name: "negative one minus min reaches max exactly", a: -1, b: math.MinInt64, want: math.MaxInt64},
		{name: "one past lower boundary underflows", a: -2, b: math.MaxInt64, wantErr: checked.ErrUnderflow},
		{name: "one past upper boundary overflows", a: math.MaxInt64, b: -1, wantErr: checked.ErrOverflow},
		{name: "negating min overflows", a: 0, b: math.MinInt64, wantErr: checked.ErrOverflow},
		{name: "min minus one underflows", a: math.MinInt64, b: 1, wantErr: checked.ErrUnderflow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := checked.SubInt64(tc.a, tc.b)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Zero(t, got)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestMulInt64(t *testing.T) {
	const halfMax = math.MaxInt64 / 2 // 2^62 - 1

	cases := []struct {
		name    string
		a, b    int64
		want    int64
		wantErr error
	}{
		{name: "zero times min", a: 0, b: math.MinInt64, want: 0},
		{name: "min times zero", a: math.MinInt64, b: 0, want: 0},
		{name: "one times max", a: 1, b: math.MaxInt64, want: math.MaxInt64},
		{name: "one times min", a: 1, b: math.MinInt64, want: math.MinInt64},
		{name: "negative one times max", a: -1, b: math.MaxInt64, want: -math.MaxInt64},
		{name: "negative times negative is positive", a: -3, b: -3, want: 9},
		{name: "mixed signs", a: -3, b: 3, want: -9},
		{name: "exact upper boundary", a: 2, b: halfMax, want: math.MaxInt64 - 1},
		{name: "one past upper boundary overflows", a: 2, b: halfMax + 1, wantErr: checked.ErrOverflow},

		// Regression: the negative range reaches its limit exactly, in both
		// operand orders. Guards a future refactor of the sign-branch selection.
		{name: "exact lower boundary", a: 2, b: -(1 << 62), want: math.MinInt64},
		{name: "exact lower boundary with operands reversed", a: -(1 << 62), b: 2, want: math.MinInt64},

		// math.MinInt64 * -1 is the case a division round-trip cannot detect:
		// the wrapped product is math.MinInt64 and Go defines
		// math.MinInt64 / -1 as math.MinInt64, so the check appears to pass.
		{name: "min times negative one overflows", a: math.MinInt64, b: -1, wantErr: checked.ErrOverflow},
		{name: "negative one times min overflows", a: -1, b: math.MinInt64, wantErr: checked.ErrOverflow},

		{name: "max times two overflows", a: math.MaxInt64, b: 2, wantErr: checked.ErrOverflow},
		{name: "max times negative two underflows", a: math.MaxInt64, b: -2, wantErr: checked.ErrUnderflow},
		{name: "min times two underflows", a: math.MinInt64, b: 2, wantErr: checked.ErrUnderflow},
		{name: "min times min overflows", a: math.MinInt64, b: math.MinInt64, wantErr: checked.ErrOverflow},
		{name: "max times max overflows", a: math.MaxInt64, b: math.MaxInt64, wantErr: checked.ErrOverflow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := checked.MulInt64(tc.a, tc.b)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Zero(t, got)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestUint64FromInt64(t *testing.T) {
	cases := []struct {
		name    string
		in      int64
		want    uint64
		wantErr error
	}{
		{name: "zero", in: 0, want: 0},
		{name: "one", in: 1, want: 1},
		{name: "exact boundary at max int64", in: math.MaxInt64, want: math.MaxInt64},
		{name: "negative one is out of range", in: -1, wantErr: checked.ErrRange},
		{name: "min int64 is out of range", in: math.MinInt64, wantErr: checked.ErrRange},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := checked.Uint64FromInt64(tc.in)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Zero(t, got)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestInt64FromUint64(t *testing.T) {
	cases := []struct {
		name    string
		in      uint64
		want    int64
		wantErr error
	}{
		{name: "zero", in: 0, want: 0},
		{name: "one", in: 1, want: 1},
		{name: "exact boundary at max int64", in: math.MaxInt64, want: math.MaxInt64},
		{name: "one past boundary is out of range", in: math.MaxInt64 + 1, wantErr: checked.ErrRange},
		{name: "max uint64 is out of range", in: math.MaxUint64, wantErr: checked.ErrRange},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := checked.Int64FromUint64(tc.in)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Zero(t, got)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

// TestNegativeHeightDoesNotWrap pins the reason Uint64FromInt64 exists: the
// unchecked conversion an SDK block height invites turns a negative value into a
// very large positive one, silently corrupting height comparisons.
//
// The compiler rejects this conversion only when the operand is a constant, so
// negativeHeight is deliberately a variable: that is the shape a real block
// height takes, and the shape the compiler cannot protect.
func TestNegativeHeightDoesNotWrap(t *testing.T) {
	var negativeHeight int64 = -1

	require.Equal(t, uint64(math.MaxUint64), uint64(negativeHeight),
		"precondition: the unchecked conversion wraps")

	_, err := checked.Uint64FromInt64(negativeHeight)
	require.ErrorIs(t, err, checked.ErrRange)
}
