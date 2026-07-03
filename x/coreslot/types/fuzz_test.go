package types

import (
	"testing"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
)

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

// FuzzCoreSlotGenesisJSON fuzzes the real genesis deserialization surface used by
// InitGenesis (coreslot/module.go): cdc.UnmarshalJSON(raw, &GenesisState) followed
// by Validate. The codec registers the crypto interfaces, so the consensus-key
// Any inside a slot is resolved during decode. Property: neither the JSON decode
// nor Validate panics on arbitrary bytes.
func FuzzCoreSlotGenesisJSON(f *testing.F) {
	registry := codectypes.NewInterfaceRegistry()
	RegisterInterfaces(registry)
	cdc := codec.NewProtoCodec(registry)

	a := sdk.AccAddress(make([]byte, 20)).String()
	b := sdk.AccAddress(append([]byte{1}, make([]byte, 19)...)).String()
	if seed, err := cdc.MarshalJSON(DefaultGenesis(a, b)); err == nil {
		f.Add(seed)
	}
	f.Add([]byte("{}"))
	f.Add([]byte(""))
	f.Add([]byte(`{"next_slot_id":"1"}`))
	f.Add([]byte(`{"params":{"min_active_slots":"0","max_active_slots":"0"}}`))

	f.Fuzz(func(t *testing.T, raw []byte) {
		var gs GenesisState
		if err := cdc.UnmarshalJSON(raw, &gs); err != nil {
			return // malformed JSON is expected; we only care that it doesn't panic
		}
		_ = gs.Validate()
	})
}
