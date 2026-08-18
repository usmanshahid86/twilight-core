package types_test

import (
	"testing"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/stretchr/testify/require"

	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// Fresh genesis carries no closed-epoch state.
//
// A fresh chain has finalized nothing, so it has no archive, no obligations, and
// owes nothing. Three rules, one fact.
//
// The claim ledger that used to need a fourth rule no longer exists: the message,
// its queries, its CLI, its event and its store prefix were removed, and the
// genesis field number is reserved. What remains to prove is that its absence is
// enforced at the decode boundary rather than merely undocumented, which is what
// TestARetiredClaimLedgerCannotEnterThroughGenesis covers.

func closedEpochRecord(t *testing.T, genesis *types.GenesisState) *types.EpochReward {
	t.Helper()
	snapshot := types.DefaultEpochConfigSnapshot(*genesis.Params)
	return &types.EpochReward{
		EpochNumber: 1, StartHeight: 1, EndHeight: 10,
		MintedEmission: "1", CarryIn: "0", DistributableFees: "0", TreasuryAmount: "0",
		RewardPool: "1", AllocatedAmount: "1", CarryOut: "0",
		DistributionMethod:          genesis.Params.DistributionMethod,
		RemainderPolicy:             genesis.Params.RemainderPolicy,
		CumulativeEmittedAfterEpoch: "1", Config: &snapshot,
	}
}

func TestFreshGenesisCarriesNoClosedEpochState(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reason string
		mutate func(*testing.T, *types.GenesisState)
	}{
		{
			name:   "a finalized epoch",
			reason: "fresh genesis carries 1 finalized epochs",
			mutate: func(t *testing.T, g *types.GenesisState) {
				g.FinalizedEpochs = []*types.EpochReward{closedEpochRecord(t, g)}
			},
		},
		{
			name:   "a slot entitlement",
			reason: "fresh genesis carries 1 slot entitlements",
			mutate: func(_ *testing.T, g *types.GenesisState) {
				g.SlotEntitlements = []*types.SlotEntitlement{{
					SlotId: 1, Epoch: 1, TotalBlocksActive: 1,
					EntitlementAmount: "1", ReleasedAmount: "0",
					PayoutAddress: testAddress(3), RewardConfigVersion: 1, CreatedHeight: 1,
				}}
				g.OutstandingEntitlementLiability = "1"
			},
		},
		{
			name:   "an outstanding liability",
			reason: "a fresh chain owes nothing",
			mutate: func(_ *testing.T, g *types.GenesisState) {
				g.OutstandingEntitlementLiability = "1"
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			genesis := freshGenesis(t)
			tc.mutate(t, genesis)
			err := genesis.Validate()
			require.ErrorIs(t, err, types.ErrInvalidGenesis)
			require.Contains(t, err.Error(), tc.reason)
		})
	}
}

// TestFreshGenesisLiabilityMustBeCanonicalZero is S6.
//
// Every spelling below parses as zero, so a rule written as "must parse to zero"
// would admit all of them. The value is written and exported verbatim, so
// admitting one would put a string into canonical state that the module itself
// never produces — and every later comparison against it would then be comparing
// against something the chain cannot write.
func TestFreshGenesisLiabilityMustBeCanonicalZero(t *testing.T) {
	t.Run("the canonical zero is accepted", func(t *testing.T) {
		genesis := freshGenesis(t)
		genesis.OutstandingEntitlementLiability = "0"
		require.NoError(t, genesis.Validate())
	})

	for _, spelling := range []string{"+0", "00", "000", "0x0", "-0", " 0", "0 ", "", "0.0"} {
		t.Run(spelling, func(t *testing.T) {
			genesis := freshGenesis(t)
			genesis.OutstandingEntitlementLiability = spelling
			require.ErrorIs(t, genesis.Validate(), types.ErrInvalidGenesis)
		})
	}
}

// TestDefaultGenesisIsAConformingFreshDocument keeps the shipped default inside
// the rules above rather than merely alongside them.
func TestDefaultGenesisIsAConformingFreshDocument(t *testing.T) {
	genesis := types.DefaultGenesis()
	require.NoError(t, genesis.Validate())
	require.Empty(t, genesis.FinalizedEpochs)
	require.Empty(t, genesis.SlotEntitlements)
	require.Equal(t, "0", genesis.OutstandingEntitlementLiability)
	require.Equal(t, "0", genesis.State.CarryForwardRemainder)
}

// TestARetiredClaimLedgerCannotEnterThroughGenesis closes the last door the
// retired claim path could have come back through.
//
// Removing a proto field does not by itself remove the state: an operator holding
// a pre-retirement export still has a document with a `claim_records` array in it,
// describing obligations this chain can no longer pay. The dangerous outcome is
// not a refusal — it is a SILENT one, where the array is skipped as unknown and a
// chain starts having quietly discarded a ledger somebody was owed under.
//
// So the property under test is that the decode fails, and fails naming the field.
// Genesis import goes through cdc.UnmarshalJSON (x/rewards/module.go), which is
// strict about unknown fields; this pins that strictness as a retirement guarantee
// rather than leaving it as an incidental property of the codec.
//
// The reserved field number is the other half: it stops a future field from
// inheriting the wire bytes an old binary document would decode into it.
func TestARetiredClaimLedgerCannotEnterThroughGenesis(t *testing.T) {
	registry := codectypes.NewInterfaceRegistry()
	types.RegisterInterfaces(registry)
	cdc := codec.NewProtoCodec(registry)

	for _, tc := range []struct {
		name string
		doc  string
	}{
		{
			name: "a populated legacy ledger",
			doc:  `{"claim_records":[{"slot_id":"1","epoch_number":"1","amount":"5"}]}`,
		},
		{
			// An empty array is the shape a pre-retirement export of a chain that
			// never wrote a claim record produces. It carries no value, so skipping
			// it would look harmless — but accepting the field at all would mean the
			// populated case above is rejected only by luck of its contents.
			name: "an empty legacy ledger",
			doc:  `{"claim_records":[]}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var genesis types.GenesisState
			err := cdc.UnmarshalJSON([]byte(tc.doc), &genesis)
			require.Error(t, err, "a genesis document carrying the retired claim ledger must be refused, not silently stripped")
			require.Contains(t, err.Error(), "claim_records",
				"the refusal must name the retired field so an operator can see what was rejected")
		})
	}

	// Control. Without this the cases above would also pass against a codec that
	// rejects every document, which would prove nothing about the retired field.
	t.Run("a conforming document still decodes", func(t *testing.T) {
		encoded, err := cdc.MarshalJSON(types.DefaultGenesis())
		require.NoError(t, err)

		var genesis types.GenesisState
		require.NoError(t, cdc.UnmarshalJSON(encoded, &genesis))
	})
}
