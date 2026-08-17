package types

import (
	"strings"

	sdkmath "cosmossdk.io/math"
)

// The canonical decimal encoding for monetary values the protocol treats as
// identity rather than merely as numbers.
//
// # Why the general parser is not enough
//
// ParseAmountString delegates to the arbitrary-precision decoder, which parses
// with an INFERRED base. That is not a leniency about spelling, it is a different
// number:
//
//	"010"   parses as 8   (octal)
//	"0x10"  parses as 16  (hexadecimal)
//	"0b101" parses as 5   (binary)
//	"1_0"   parses as 10  (digit separator)
//	"+10"   parses as 10  (leading sign)
//
// For a value the chain wrote itself and read back, that never matters: it wrote
// canonical decimal, so it reads canonical decimal. It matters a great deal for a
// value that arrives from outside — a genesis document, a scheduled configuration,
// a payout line — where "010" silently means 8 utwlt per block rather than 10, and
// where two distinct byte strings meaning the same amount are two distinct
// messages over one authorization.
//
// The rule is therefore one spelling per value: canonical base-10 only. No sign,
// no whitespace, no radix prefix, no separator, no decimal point, no exponent, and
// no leading zeroes other than the literal "0".

// ParseCanonicalAmount parses a nonnegative amount under the canonical encoding.
//
// The digit scan is what decides admissibility; the arbitrary-precision decode
// afterwards only converts a string already known to be canonical decimal, so the
// inferred base can no longer reach a value.
func ParseCanonicalAmount(name, value string) (sdkmath.Int, error) {
	if value == "" {
		return sdkmath.Int{}, ErrInvalidState.Wrapf("%s is empty", name)
	}
	if strings.TrimSpace(value) != value || strings.ContainsAny(value, " \t\r\n") {
		return sdkmath.Int{}, ErrInvalidState.Wrapf("%s %q contains whitespace", name, value)
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return sdkmath.Int{}, ErrInvalidState.Wrapf(
				"%s %q is not a canonical base-10 integer", name, value)
		}
	}
	if len(value) > 1 && value[0] == '0' {
		return sdkmath.Int{}, ErrInvalidState.Wrapf("%s %q has a leading zero", name, value)
	}

	amount, ok := sdkmath.NewIntFromString(value)
	if !ok {
		return sdkmath.Int{}, ErrInvalidState.Wrapf("%s %q is not an integer", name, value)
	}
	// Unreachable through the digit scan above, which admits no sign character.
	// Asserted rather than assumed, because the guarantee is worth more than the
	// branch costs and a later relaxation of the scan would otherwise pass silently.
	if amount.IsNegative() {
		return sdkmath.Int{}, ErrInvalidState.Wrapf("%s %q is negative", name, value)
	}
	return amount, nil
}
