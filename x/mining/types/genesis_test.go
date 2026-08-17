package types_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	appparams "github.com/twilight-project/twilight-core/app/params"
	"github.com/twilight-project/twilight-core/x/mining/types"
)

// Fresh-genesis rules for x/mining.
//
// Two groups. The histories must each begin with exactly one version effective at
// epoch 1, because a history that begins anywhere else cannot bind the chain's
// first targets. And nothing that a running chain's settlement workflow produces
// may appear at all, because a fresh chain has finalized no reward epoch — the
// clock, the cursor, the anchors and the settlements are four consequences of that
// one fact.

func TestDefaultGenesisIsConforming(t *testing.T) {
	genesis := types.DefaultGenesis()
	require.NoError(t, genesis.Validate())

	require.Len(t, genesis.DistributionModeVersions, 1)
	require.Len(t, genesis.SelectionParamsVersions, 1)
	require.Len(t, genesis.SettlementParamsVersions, 1)
	require.Empty(t, genesis.ScheduledDistributionModes)
	require.Empty(t, genesis.ScheduledSelectionParams)
	require.Empty(t, genesis.ScheduledSettlementParams)
	require.Empty(t, genesis.SettlementEpochAnchors)
	require.Empty(t, genesis.Settlements)
	require.Zero(t, genesis.SettlementClock)
	require.Zero(t, genesis.LastProcessedRewardEpoch)

	mode := genesis.DistributionModeVersions[0]
	require.Equal(t, types.MiningDistributionMode_MINING_DISTRIBUTION_MODE_TRUSTED_AS_DISTRIBUTION, mode.Mode)
	require.Equal(t, uint64(1), mode.ValidFromEpoch)
	require.Zero(t, mode.ValidUntilEpochExclusive, "the initial mode is open-ended")
}

// TestFreshGenesisCarriesNoSettlementState is the rule that keeps a running
// chain's workflow state out of a fresh document.
//
// Each mutation is refused rather than normalized. Accepting one and quietly
// zeroing it would be deciding what that state means across a restart, which is
// continuation import — deferred, and not a decision a fresh importer gets to make
// by accident.
func TestFreshGenesisCarriesNoSettlementState(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reason string
		mutate func(*types.GenesisState)
	}{
		{
			name:   "a settlement clock",
			reason: "has produced no settlement-enabled block",
			mutate: func(g *types.GenesisState) { g.SettlementClock = 1 },
		},
		{
			name:   "a processed epoch cursor",
			reason: "has finalized no epoch",
			mutate: func(g *types.GenesisState) { g.LastProcessedRewardEpoch = 1 },
		},
		{
			name:   "an epoch anchor",
			reason: "settlement epoch anchors",
			mutate: func(g *types.GenesisState) {
				g.SettlementEpochAnchors = []*types.SettlementEpochAnchor{{Epoch: 1, CreatedSettlementClock: 5}}
			},
		},
		{
			name:   "a settlement",
			reason: "settlements; a fresh chain has materialized none",
			mutate: func(g *types.GenesisState) {
				g.Settlements = []*types.Settlement{openSettlement(1, 1)}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			genesis := types.DefaultGenesis()
			tc.mutate(genesis)
			err := genesis.Validate()
			require.ErrorIs(t, err, types.ErrInvalidGenesis)
			require.Contains(t, err.Error(), tc.reason)
		})
	}
}

// TestFreshGenesisRequiresOneAnchorPerHistory covers all three families together,
// since the rule and its reason are identical for each: a fresh chain has accepted
// no update, so each history holds exactly its genesis version and each schedule is
// empty.
func TestFreshGenesisRequiresOneAnchorPerHistory(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*types.GenesisState)
	}{
		{"no distribution mode", func(g *types.GenesisState) { g.DistributionModeVersions = nil }},
		{"two distribution modes", func(g *types.GenesisState) {
			second := *g.DistributionModeVersions[0]
			second.Version, second.ValidFromEpoch = 2, 4
			g.DistributionModeVersions = append(g.DistributionModeVersions, &second)
		}},
		{"a scheduled mode", func(g *types.GenesisState) {
			g.ScheduledDistributionModes = []*types.ScheduledMiningDistributionMode{{
				EffectiveEpoch: 2,
				Mode:           types.MiningDistributionMode_MINING_DISTRIBUTION_MODE_TRUSTED_AS_DISTRIBUTION,
			}}
		}},
		{"no selection params", func(g *types.GenesisState) { g.SelectionParamsVersions = nil }},
		{"a scheduled selection params", func(g *types.GenesisState) {
			g.ScheduledSelectionParams = []*types.ScheduledSelectionParams{{EffectiveEpoch: 2}}
		}},
		{"no settlement params", func(g *types.GenesisState) { g.SettlementParamsVersions = nil }},
		{"a scheduled settlement params", func(g *types.GenesisState) {
			g.ScheduledSettlementParams = []*types.ScheduledSettlementParams{{EffectiveEpoch: 2}}
		}},
		{"a mode anchored past epoch 1", func(g *types.GenesisState) {
			g.DistributionModeVersions[0].ValidFromEpoch = 2
		}},
		{"a selection params anchored past epoch 1", func(g *types.GenesisState) {
			g.SelectionParamsVersions[0].EffectiveEpoch = 2
		}},
		{"a settlement params anchored past epoch 1", func(g *types.GenesisState) {
			g.SettlementParamsVersions[0].EffectiveEpoch = 2
		}},
		{"a mode that is not version 1", func(g *types.GenesisState) {
			g.DistributionModeVersions[0].Version = 2
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			genesis := types.DefaultGenesis()
			tc.mutate(genesis)
			require.ErrorIs(t, genesis.Validate(), types.ErrInvalidGenesis)
		})
	}
}

// TestInitialDistributionModeMustBeOpenEndedTrusted covers the two things that
// distinguish the mode anchor from an ordinary history entry.
//
// A closed interval at genesis would leave every epoch past its end with no mode
// at all, and a target that cannot resolve a mode cannot be materialized. And
// PROTOCOL_SELECTION is a representable value with no producer or consumer in this
// profile, so a genesis selecting it would create targets nothing can ever settle.
func TestInitialDistributionModeMustBeOpenEndedTrusted(t *testing.T) {
	t.Run("a closed initial interval", func(t *testing.T) {
		genesis := types.DefaultGenesis()
		genesis.DistributionModeVersions[0].ValidUntilEpochExclusive = 10
		err := genesis.Validate()
		require.ErrorIs(t, err, types.ErrInvalidGenesis)
		require.Contains(t, err.Error(), "must be open-ended")
	})

	t.Run("protocol selection at genesis", func(t *testing.T) {
		genesis := types.DefaultGenesis()
		genesis.DistributionModeVersions[0].Mode =
			types.MiningDistributionMode_MINING_DISTRIBUTION_MODE_PROTOCOL_SELECTION
		err := genesis.Validate()
		require.ErrorIs(t, err, types.ErrInvalidGenesis)
		require.Contains(t, err.Error(), "only trusted distribution")
	})

	// The value itself remains representable. Refusing it as a genesis choice is
	// not the same as making the enum arm unrepresentable, and the difference is
	// what lets a later tranche add a producer rather than migrate state.
	t.Run("the value is still a valid record", func(t *testing.T) {
		version := types.MiningDistributionModeVersion{
			Version:        2,
			Mode:           types.MiningDistributionMode_MINING_DISTRIBUTION_MODE_PROTOCOL_SELECTION,
			ValidFromEpoch: 4,
		}
		require.NoError(t, version.Validate())
	})
}

// TestSettlementParamsAdmissionWalksTheRatifiedBounds is the mutation-sensitive
// table for the six immutable settlement bounds.
//
// The literals are written out rather than derived from the constants. A table
// built from the constants would follow them wherever they moved and keep passing;
// this one exists so that moving a ratified bound fails a test.
func TestSettlementParamsAdmissionWalksTheRatifiedBounds(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*types.SettlementParamsVersion)
		accept bool
	}{
		{name: "the recommended configuration", mutate: func(*types.SettlementParamsVersion) {}, accept: true},
		{name: "the shortest admissible window", mutate: func(p *types.SettlementParamsVersion) {
			p.SettlementWindowEpochs = 1
		}, accept: true},
		{name: "a zero window", mutate: func(p *types.SettlementParamsVersion) { p.SettlementWindowEpochs = 0 }},

		{name: "one recipient", mutate: func(p *types.SettlementParamsVersion) { p.MaxRecipientsPerChunk = 1 }, accept: true},
		{name: "recipients at the ceiling", mutate: func(p *types.SettlementParamsVersion) { p.MaxRecipientsPerChunk = 32 }, accept: true},
		{name: "zero recipients", mutate: func(p *types.SettlementParamsVersion) { p.MaxRecipientsPerChunk = 0 }},
		{name: "recipients above the ceiling", mutate: func(p *types.SettlementParamsVersion) { p.MaxRecipientsPerChunk = 33 }},

		{name: "one chunk", mutate: func(p *types.SettlementParamsVersion) { p.MaxChunksPerSettlement = 1 }, accept: true},
		{name: "chunks at the ceiling", mutate: func(p *types.SettlementParamsVersion) { p.MaxChunksPerSettlement = 4 }, accept: true},
		{name: "zero chunks", mutate: func(p *types.SettlementParamsVersion) { p.MaxChunksPerSettlement = 0 }},
		{name: "chunks above the ceiling", mutate: func(p *types.SettlementParamsVersion) { p.MaxChunksPerSettlement = 5 }},

		{name: "the payout floor exactly", mutate: func(p *types.SettlementParamsVersion) {
			p.MinRecipientPayoutAmount = "10000"
		}, accept: true},
		{name: "above the payout floor", mutate: func(p *types.SettlementParamsVersion) {
			p.MinRecipientPayoutAmount = "1000000000000000000000000"
		}, accept: true},
		{name: "one below the payout floor", mutate: func(p *types.SettlementParamsVersion) {
			p.MinRecipientPayoutAmount = "9999"
		}},
		{name: "a zero payout floor", mutate: func(p *types.SettlementParamsVersion) {
			p.MinRecipientPayoutAmount = "0"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := *types.DefaultGenesis().SettlementParamsVersions[0]
			tc.mutate(&params)
			if tc.accept {
				require.NoError(t, params.Validate())
				return
			}
			require.ErrorIs(t, params.Validate(), types.ErrInvalidState)
		})
	}
}

// TestSettlementPayoutFloorRejectsNonCanonicalEncodings closes the alternate
// representation hazard on the one settlement parameter that is a monetary value.
//
// Every spelling below is accepted by the arbitrary-precision decoder and means a
// different number than it appears to: "010" is 8 and "0x10" is 16. A configured
// floor that silently meant 8 utwlt would defeat the dust defense entirely.
func TestSettlementPayoutFloorRejectsNonCanonicalEncodings(t *testing.T) {
	for _, spelling := range []string{"010", "0x10", "0b101", "1_0", "+10000", "-10000", " 10000", "10000 ", ""} {
		t.Run(spelling, func(t *testing.T) {
			params := *types.DefaultGenesis().SettlementParamsVersions[0]
			params.MinRecipientPayoutAmount = spelling
			require.ErrorIs(t, params.Validate(), types.ErrInvalidState)
		})
	}
}

// TestSettlementLifecycleAndReasonMustAgree pins the one relation that binds the
// terminal flag to the audit metadata.
//
// A row that claims to be open while naming the arm it was finalized through, or
// one that claims to be terminal without naming one, is a row no admitted
// transition produced. Both directions are checked because both are reachable
// through a corrupted store and neither is detectable from the other fields.
func TestSettlementLifecycleAndReasonMustAgree(t *testing.T) {
	t.Run("an open settlement is unspecified", func(t *testing.T) {
		require.NoError(t, openSettlement(1, 1).Validate())
	})

	t.Run("open but carrying a reason", func(t *testing.T) {
		s := openSettlement(1, 1)
		s.FinalizationReason = types.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_AUTHORIZED_EARLY
		require.ErrorIs(t, s.Validate(), types.ErrInvalidState)
	})

	t.Run("open but carrying a height", func(t *testing.T) {
		s := openSettlement(1, 1)
		s.FinalizedHeight = 10
		require.ErrorIs(t, s.Validate(), types.ErrInvalidState)
	})

	t.Run("finalized without a reason", func(t *testing.T) {
		s := openSettlement(1, 1)
		s.Finalized, s.FinalizedHeight = true, 10
		require.ErrorIs(t, s.Validate(), types.ErrInvalidState)
	})

	t.Run("finalized without a height", func(t *testing.T) {
		s := openSettlement(1, 1)
		s.Finalized = true
		s.FinalizationReason = types.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_AUTHORIZED_EARLY
		require.ErrorIs(t, s.Validate(), types.ErrInvalidState)
	})

	t.Run("finalized with both", func(t *testing.T) {
		s := openSettlement(1, 1)
		s.Finalized, s.FinalizedHeight = true, 10
		s.FinalizationReason = types.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_PERMISSIONLESS_AFTER_DEADLINE
		require.NoError(t, s.Validate())
	})
}

// TestVersionNumbersAreNotRequiredContiguous states the deliberate divergence from
// the reward-configuration history.
//
// Reward configuration versions were separately ratified as contiguous so a
// version-number query could classify absence arithmetically. Nothing ratified
// that for these families, and imposing it would reject a history the architecture
// permits. This test exists so a later change that "fixes" the inconsistency has
// to argue with a named expectation rather than a silence.
func TestVersionNumbersAreNotRequiredContiguous(t *testing.T) {
	gapped := types.SettlementParamsVersion{
		Version:                  7,
		EffectiveEpoch:           12,
		SettlementWindowEpochs:   types.DefaultSettlementWindowEpochs,
		MaxRecipientsPerChunk:    types.DefaultMaxRecipientsPerChunk,
		MaxChunksPerSettlement:   types.DefaultMaxChunksPerSettlement,
		MinRecipientPayoutAmount: types.DefaultMinRecipientPayoutAmount,
	}
	require.NoError(t, gapped.Validate(),
		"version numbers are unique and monotonic, not contiguous")

	mode := types.MiningDistributionModeVersion{
		Version:        9,
		Mode:           types.MiningDistributionMode_MINING_DISTRIBUTION_MODE_TRUSTED_AS_DISTRIBUTION,
		ValidFromEpoch: 40,
	}
	require.NoError(t, mode.Validate())
}

// TestRatifiedSettlementBoundsAreTheExpectedValues pins the constants themselves.
//
// The bounds are consensus values: a chain configured against one set and a chain
// configured against another do not agree about which chunks are admissible.
// Changing one has to fail here first.
func TestRatifiedSettlementBoundsAreTheExpectedValues(t *testing.T) {
	require.Equal(t, uint64(1), appparams.HardMinSettlementWindowEpochs)
	require.Equal(t, uint64(1), appparams.HardMinRecipientsPerChunk)
	require.Equal(t, uint64(32), appparams.HardMaxRecipientsPerChunk)
	require.Equal(t, uint64(1), appparams.HardMinChunksPerSettlement)
	require.Equal(t, uint64(4), appparams.HardMaxChunksPerSettlement)
	require.Equal(t, "10000", appparams.HardMinSettlementPayoutAmount().String())
}

func openSettlement(slotID, epoch uint64) *types.Settlement {
	return &types.Settlement{
		SlotId:                  slotID,
		Epoch:                   epoch,
		DistributionModeVersion: 1,
		SettlementMode:          types.SettlementMode_SETTLEMENT_MODE_TRUSTED_AS,
		SettlementParamsVersion: 1,
	}
}
