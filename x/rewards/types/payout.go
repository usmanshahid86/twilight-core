package types

import (
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

// ParseCanonicalPayoutAmount parses a payout amount under the canonical encoding
// (§34).
//
// The general amount parser in this package is deliberately not reused: it infers
// the radix, so several spellings decode to different numbers. See
// ParseCanonicalAmount, which is the shared rule this and the canonical reward
// configuration are both held to — one encoding, decided in one place, so the
// release boundary and the configuration that scales the mint cannot come to
// disagree about what a well-formed amount is.
//
// Zero parses. It is rejected as a PARTICIPANT payout by the caller, not here,
// because the canonical operator remainder of zero is a legitimate amount that
// the same encoding has to be able to express.
func ParseCanonicalPayoutAmount(name, value string) (sdkmath.Int, error) {
	return ParseCanonicalAmount(name, value)
}
