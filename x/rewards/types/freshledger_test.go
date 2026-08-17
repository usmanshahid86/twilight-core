package types_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// Fresh genesis carries no closed-epoch state.
//
// A fresh chain has finalized nothing, so it has no archive, no obligations in
// either representation, and owes nothing. Four rules, one fact.
//
// The claim-record rule is the one worth being explicit about. It is NOT the
// legacy retirement work package: ClaimRewards, its queries and its CLI all
// remain, and continue to serve state seeded directly for explicitly legacy
// regression coverage. What it closes is the only remaining way a conforming POC1
// chain could come to hold a claim record at all — V2 finalization creates
// entitlements and nothing else, so genesis was the last source. With it closed, a
// payable claim and a payable entitlement can no longer coexist over one escrow.

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
			name:   "a legacy claim record",
			reason: "fresh genesis carries 1 legacy claim records",
			mutate: func(_ *testing.T, g *types.GenesisState) {
				// Deliberately NOT paired with a finalized epoch. Pairing it would be
				// well-formed under the old rules and would now be refused for the
				// archive instead, so the claim rule would never be reached.
				g.ClaimRecords = []*types.EligibleSlotReward{{
					SlotId: 1, EpochNumber: 1,
					OperatorAddress: testAddress(2), PayoutAddress: testAddress(3),
					RewardWeight: "1.000000000000000000", EffectiveWeight: "10", Amount: "1",
				}}
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
	require.Empty(t, genesis.ClaimRecords)
	require.Empty(t, genesis.SlotEntitlements)
	require.Equal(t, "0", genesis.OutstandingEntitlementLiability)
	require.Equal(t, "0", genesis.State.CarryForwardRemainder)
}
