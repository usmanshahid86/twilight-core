package types_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	appparams "github.com/twilight-project/twilight-core/app/params"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

func rewardVersion(shareBps uint64) types.RewardConfigVersion {
	return types.RewardConfigVersion{
		Version:                  1,
		EffectiveEpoch:           1,
		InitialBlockSubsidy:      types.DefaultInitialBlockSubsidy,
		EmissionTreasuryShareBps: shareBps,
	}
}

// TestRewardConfigTreasuryShareAdmissionBoundary walks the ratified ceiling.
//
// The literals are written out rather than derived from the constant. A table
// built from HardMaxEmissionTreasuryShareBps would follow the constant wherever
// it moved and keep passing; this table exists so that moving it fails a test.
func TestRewardConfigTreasuryShareAdmissionBoundary(t *testing.T) {
	for _, tc := range []struct {
		name     string
		shareBps uint64
		accept   bool
	}{
		{name: "zero diverts nothing", shareBps: 0, accept: true},
		{name: "one basis point", shareBps: 1, accept: true},
		{name: "one below the ceiling", shareBps: 4_999, accept: true},
		{name: "at the ceiling", shareBps: 5_000, accept: true},
		{name: "one above the ceiling", shareBps: 5_001},
		{name: "one below the denominator", shareBps: 9_999},
		{name: "the whole emission", shareBps: 10_000},
		{name: "beyond the denominator", shareBps: 10_001},
	} {
		t.Run(tc.name, func(t *testing.T) {
			version := rewardVersion(tc.shareBps)
			scheduled := types.ScheduledRewardConfig{
				EffectiveEpoch:           2,
				InitialBlockSubsidy:      types.DefaultInitialBlockSubsidy,
				EmissionTreasuryShareBps: tc.shareBps,
			}
			if tc.accept {
				require.NoErrorf(t, version.Validate(), "share %d bps rejected", tc.shareBps)
				require.NoErrorf(t, scheduled.Validate(), "scheduled share %d bps rejected", tc.shareBps)
				return
			}
			// A schedule entry becomes history unchanged, so it is held to the same
			// ceiling. Admitting one that history would refuse would move the
			// rejection into a block that can only answer by halting.
			require.Errorf(t, version.Validate(), "share %d bps accepted", tc.shareBps)
			require.Errorf(t, scheduled.Validate(), "scheduled share %d bps accepted", tc.shareBps)
		})
	}
}

// TestRewardConfigCeilingTracksTheRatifiedConstant is the companion to the table
// above: the table pins the numbers, this pins the relation between the numbers
// and the constant the rest of the module reads.
func TestRewardConfigCeilingTracksTheRatifiedConstant(t *testing.T) {
	require.NoError(t, rewardVersion(appparams.HardMaxEmissionTreasuryShareBps).Validate())
	require.Error(t, rewardVersion(appparams.HardMaxEmissionTreasuryShareBps+1).Validate())
}

func TestRewardConfigVersionRejectsMalformedRecords(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*types.RewardConfigVersion)
	}{
		{"zero version", func(v *types.RewardConfigVersion) { v.Version = 0 }},
		{"zero effective epoch", func(v *types.RewardConfigVersion) { v.EffectiveEpoch = 0 }},
		{"empty subsidy", func(v *types.RewardConfigVersion) { v.InitialBlockSubsidy = "" }},
		{"non-numeric subsidy", func(v *types.RewardConfigVersion) { v.InitialBlockSubsidy = "many" }},
		{"negative subsidy", func(v *types.RewardConfigVersion) { v.InitialBlockSubsidy = "-1" }},
		// Zero is parseable and non-negative, and is still refused. A zero subsidy
		// makes every later block emit nothing regardless of supply or tier, which
		// is a permanent halt of emission expressed as configuration; the reversible
		// way to stop emission is the canonical pause state.
		{"zero subsidy", func(v *types.RewardConfigVersion) { v.InitialBlockSubsidy = "0" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			version := rewardVersion(0)
			tc.mutate(&version)
			require.Error(t, version.Validate())
		})
	}
}

func TestScheduledRewardConfigRejectsMalformedRecords(t *testing.T) {
	base := types.ScheduledRewardConfig{
		EffectiveEpoch:      2,
		InitialBlockSubsidy: types.DefaultInitialBlockSubsidy,
	}
	t.Run("zero effective epoch", func(t *testing.T) {
		scheduled := base
		scheduled.EffectiveEpoch = 0
		require.Error(t, scheduled.Validate())
	})
	t.Run("zero subsidy", func(t *testing.T) {
		scheduled := base
		scheduled.InitialBlockSubsidy = "0"
		require.Error(t, scheduled.Validate())
	})
	t.Run("well formed", func(t *testing.T) {
		require.NoError(t, base.Validate())
	})
}

// TestFreshGenesisRequiresTheRewardConfigAnchor covers the fresh-genesis content
// rules for the reward history and its schedule.
func TestFreshGenesisRequiresTheRewardConfigAnchor(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*types.GenesisState)
	}{
		{
			name:   "no reward history at all",
			mutate: func(g *types.GenesisState) { g.RewardConfigVersions = nil },
		},
		{
			name: "a second version at fresh genesis",
			mutate: func(g *types.GenesisState) {
				second := types.RewardConfigVersion{
					Version: 2, EffectiveEpoch: 2, InitialBlockSubsidy: "5",
				}
				g.RewardConfigVersions = append(g.RewardConfigVersions, &second)
			},
		},
		{
			name:   "the anchor is not version 1",
			mutate: func(g *types.GenesisState) { g.RewardConfigVersions[0].Version = 2 },
		},
		{
			name:   "the anchor is not effective at epoch 1",
			mutate: func(g *types.GenesisState) { g.RewardConfigVersions[0].EffectiveEpoch = 2 },
		},
		{
			name:   "the anchor is nil",
			mutate: func(g *types.GenesisState) { g.RewardConfigVersions = []*types.RewardConfigVersion{nil} },
		},
		{
			// The schedule is the residue of an authority transaction accepted in some
			// block. Fresh genesis has no pre-genesis block, so no such transaction can
			// have happened — and genesis already selects the initial economics
			// directly through the anchor.
			name: "a scheduled configuration at fresh genesis",
			mutate: func(g *types.GenesisState) {
				g.ScheduledRewardConfigs = []*types.ScheduledRewardConfig{{
					EffectiveEpoch: 2, InitialBlockSubsidy: "5",
				}}
			},
		},
		{
			// Even one that is otherwise perfectly well formed and keyed exactly where
			// a live chain would put it.
			name: "an otherwise valid schedule for epoch 2",
			mutate: func(g *types.GenesisState) {
				g.ScheduledRewardConfigs = []*types.ScheduledRewardConfig{{
					EffectiveEpoch:      2,
					InitialBlockSubsidy: types.DefaultInitialBlockSubsidy,
				}}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			genesis := freshGenesis(t)
			tc.mutate(genesis)
			require.Error(t, genesis.Validate())
		})
	}
}

// TestFreshGenesisPinsTheDeprecatedEconomicMirrors is the rule that keeps the
// compatibility copies from stating a different number than the authority.
//
// Both mirrors remain observable — Params through its query, the snapshot through
// the finalized epoch records it is embedded in — so an unpinned one is a second
// economic figure that looks authoritative and is archived permanently beside
// epochs it never governed.
func TestFreshGenesisPinsTheDeprecatedEconomicMirrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*types.GenesisState)
	}{
		{"params subsidy drifts", func(g *types.GenesisState) {
			g.Params.InitialBlockSubsidy = "12345"
		}},
		{"params treasury share drifts", func(g *types.GenesisState) {
			g.Params.EmissionTreasuryShareBps = 100
		}},
		{"params treasury address drifts", func(g *types.GenesisState) {
			g.Params.TreasuryAddress = "twilight1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq"
		}},
		{"snapshot subsidy drifts", func(g *types.GenesisState) {
			g.CurrentEpochConfig.InitialBlockSubsidy = "12345"
		}},
		{"snapshot treasury share drifts", func(g *types.GenesisState) {
			g.CurrentEpochConfig.EmissionTreasuryShareBps = 100
		}},
		{"reward anchor drifts away from both mirrors", func(g *types.GenesisState) {
			g.RewardConfigVersions[0].InitialBlockSubsidy = "12345"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			genesis := freshGenesis(t)
			tc.mutate(genesis)
			require.Error(t, genesis.Validate())
		})
	}

	t.Run("moving all three together is admissible", func(t *testing.T) {
		genesis := freshGenesis(t)
		genesis.Params.InitialBlockSubsidy = "12345"
		genesis.CurrentEpochConfig.InitialBlockSubsidy = "12345"
		genesis.RewardConfigVersions[0].InitialBlockSubsidy = "12345"
		require.NoError(t, genesis.Validate())
	})
}
