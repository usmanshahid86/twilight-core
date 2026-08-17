package types

import (
	"strings"

	sdkmath "cosmossdk.io/math"
)

// The canonical payout encoding for the constrained release boundary.
//
// Amounts crossing into x/rewards arrive as strings, and x/rewards is where they
// are parsed. That placement is the point: the module that owns the escrow owns
// the decode, so a future caller cannot launder a non-canonical encoding through
// a more permissive parser of its own and have the result treated as authorized.

// EntitlementPayout is one line of a payout set: a destination and the amount to
// send it.
//
// The amount stays a string until the rewards boundary parses it. Accepting an
// already-parsed integer would move the canonical-form decision to the caller,
// which is what this type exists to prevent.
type EntitlementPayout struct {
	Recipient string
	Amount    string
}

// ParseCanonicalPayoutAmount parses a payout amount under the canonical encoding.
//
// The general amount parser in this package is deliberately not reused. It
// delegates to the arbitrary-precision decoder, which accepts several spellings
// of the same number — a leading plus, leading zeroes, surrounding space. Those
// are harmless for a value the chain wrote itself and read back, and are not
// harmless for a value a caller supplies: two distinct byte strings that mean the
// same amount are two distinct messages, which is a replay and equivocation
// surface on a path that moves money.
//
// The rule (§34): canonical base-10 only. No leading '+', no '-', no whitespace
// anywhere, no decimal point, no exponent, and no leading zeroes other than the
// literal "0".
//
// Zero parses. It is rejected as a PARTICIPANT payout by the caller, not here,
// because the canonical operator remainder of zero is a legitimate amount that
// the same encoding has to be able to express.
func ParseCanonicalPayoutAmount(name, value string) (sdkmath.Int, error) {
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
