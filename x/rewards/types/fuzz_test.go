package types

import (
	"testing"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
)

// The rewards params/genesis validation runs on untrusted input at genesis
// (InitChain) and on every MsgUpdateRewardsParams. A panic there halts the
// chain, so the fuzz property for all of these is simply: never panic. Where a
// function documents a stronger contract, we assert it too.

// FuzzParseAmountString fuzzes the integer-amount parser used for max supply and
// initial block subsidy. Contract: on success the amount is non-negative.
func FuzzParseAmountString(f *testing.F) {
	for _, s := range []string{
		"", " ", "0", "-1", "21000000000000", "abc", "+5", "0x10", "1.5",
		"  42  ", "999999999999999999999999999999999999999999", "١٢٣",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, value string) {
		amount, err := ParseAmountString("fuzz", value)
		if err == nil && amount.IsNegative() {
			t.Fatalf("ParseAmountString(%q) returned a negative amount with no error", value)
		}
	})
}

// FuzzValidateWeightString fuzzes the decimal-weight validator.
func FuzzValidateWeightString(f *testing.F) {
	for _, s := range []string{
		"", " ", "1.0", "-1", "abc", "1.000000000000000000", "0",
		"99999999999999999999999999999.0", "1e9", "NaN",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, value string) {
		_ = ValidateWeightString("fuzz", value)
	})
}

// FuzzRewardsParamsValidate fuzzes the amount/denom/treasury-address inputs of
// Params.Validate with the v1 enum/flag fields fixed to valid values, so the
// parse-heavy paths (integer amounts, denom check, and — when a treasury share
// is set — bech32 address parsing) are all exercised.
func FuzzRewardsParamsValidate(f *testing.F) {
	f.Add("21000000000000", "416190", "utwlt", "", uint64(0))
	f.Add("", "", "", "cosmos1xyz", uint64(500))
	f.Add("-1", "0", "twlt", "not-an-address", uint64(20000))
	f.Add("abc", "9999999999999999999999999", "utwlt", "cosmos1abc", uint64(10000))
	f.Fuzz(func(t *testing.T, maxSupply, subsidy, denom, treasury string, bps uint64) {
		p := Params{
			MaxSupply:                maxSupply,
			InitialBlockSubsidy:      subsidy,
			NativeDenom:              denom,
			FeeDenom:                 denom,
			TargetBlockTimeSeconds:   5,
			EpochLengthBlocks:        17280,
			MaxClaimEpochsPerTx:      100,
			HalvingMode:              HalvingMode_HALVING_MODE_SUPPLY_THRESHOLD,
			DistributionMethod:       DistributionMethod_DISTRIBUTION_METHOD_UNIFORM_ACTIVE_BLOCKS,
			RemainderPolicy:          RemainderPolicy_REMAINDER_POLICY_CARRY_FORWARD,
			FeeDistributionMode:      FeeDistributionMode_FEE_DISTRIBUTION_MODE_NONE,
			EmissionTreasuryShareBps: bps,
			TreasuryAddress:          treasury,
		}
		// Property: validation classifies input; it must never panic.
		_ = p.Validate()
	})
}

// FuzzRewardsGenesisJSON fuzzes the real genesis deserialization surface used by
// InitGenesis (rewards/module.go): cdc.UnmarshalJSON(raw, &GenesisState) followed
// by Validate. Property: neither the JSON decode nor Validate panics on arbitrary
// bytes. Seeded with a marshaled DefaultGenesis so the fuzzer mutates near-valid
// documents, not just random noise.
func FuzzRewardsGenesisJSON(f *testing.F) {
	registry := codectypes.NewInterfaceRegistry()
	RegisterInterfaces(registry)
	cdc := codec.NewProtoCodec(registry)

	if seed, err := cdc.MarshalJSON(DefaultGenesis()); err == nil {
		f.Add(seed)
	}
	f.Add([]byte("{}"))
	f.Add([]byte(""))
	f.Add([]byte(`{"params":null}`))
	f.Add([]byte(`{"params":{"native_denom":"utwlt","max_supply":"-1"}}`))

	f.Fuzz(func(t *testing.T, raw []byte) {
		var gs GenesisState
		if err := cdc.UnmarshalJSON(raw, &gs); err != nil {
			return // malformed JSON is expected; we only care that it doesn't panic
		}
		_ = gs.Validate()
	})
}
