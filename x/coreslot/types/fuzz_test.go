package types

import "testing"

// CoreSlot params validation runs on untrusted input at genesis and on authority
// param updates. Fuzz property: never panic — in particular the
// MaxInt64/SlotVotingPower division must never divide by zero, and huge bounds
// must not overflow into a panic.

// FuzzCoreSlotParamsValidate fuzzes the bech32 authority strings and the numeric
// slot bounds (including 0, negative, and MaxInt64 voting power).
func FuzzCoreSlotParamsValidate(f *testing.F) {
	f.Add("cosmos1auth", "cosmos1emer", int64(1), uint64(1), uint64(100))
	f.Add("", "", int64(0), uint64(0), uint64(0))
	f.Add("garbage", "x", int64(-5), uint64(5), uint64(3))
	f.Add("a", "b", int64(9223372036854775807), uint64(1), uint64(18446744073709551615))
	f.Fuzz(func(t *testing.T, auth, emer string, power int64, minSlots, maxSlots uint64) {
		p := Params{
			Authority:          auth,
			EmergencyAuthority: emer,
			SlotVotingPower:    power,
			MinActiveSlots:     minSlots,
			MaxActiveSlots:     maxSlots,
		}
		// Property: validation classifies input; it must never panic.
		_ = p.Validate()
	})
}

// FuzzCoreSlotValidateWeight fuzzes the decimal reward-weight validator.
func FuzzCoreSlotValidateWeight(f *testing.F) {
	for _, s := range []string{
		"", " ", "1.000000000000000000", "-1", "abc", "0",
		"99999999999999999999999999999.0", "1e18", "NaN",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, weight string) {
		_ = ValidateWeight(weight)
	})
}
