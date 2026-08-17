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

// TestRewardConfigSubsidyAdmitsOneSpellingOnly closes the alternate-representation
// hazard on the value that scales every block's mint.
//
// The general amount parser infers the radix, so "010" decodes as 8 and "0x10" as
// 16. A genesis document or scheduled configuration written with either would
// therefore emit an amount its own text does not state, silently and forever —
// the subsidy is immutable protocol economics, not a value anyone re-reads.
//
// Every rejected spelling below is one the general parser ACCEPTS. That is what
// makes the table load-bearing: swap ParseCanonicalAmount back for
// ParseAmountString in validateRewardEconomics and each of these starts passing
// validation with the wrong number behind it.
func TestRewardConfigSubsidyAdmitsOneSpellingOnly(t *testing.T) {
	for _, tc := range []struct {
		name    string
		subsidy string
		accept  bool
	}{
		{name: "canonical decimal", subsidy: "10", accept: true},
		{name: "canonical large decimal", subsidy: "1000000000000000000000", accept: true},
		{name: "octal by leading zero", subsidy: "010"},
		{name: "hexadecimal", subsidy: "0x10"},
		{name: "binary", subsidy: "0b101"},
		{name: "explicit octal", subsidy: "0o17"},
		{name: "digit separator", subsidy: "1_0"},
		{name: "leading plus", subsidy: "+10"},
		{name: "leading minus", subsidy: "-10"},
		{name: "leading whitespace", subsidy: " 10"},
		{name: "trailing whitespace", subsidy: "10 "},
		{name: "internal whitespace", subsidy: "1 0"},
		{name: "decimal point", subsidy: "10.0"},
		{name: "exponent", subsidy: "1e1"},
		{name: "empty", subsidy: ""},
		{name: "zero is canonical but not positive", subsidy: "0"},
		{name: "padded zero", subsidy: "00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			version := rewardVersion(0)
			version.InitialBlockSubsidy = tc.subsidy
			scheduled := types.ScheduledRewardConfig{
				EffectiveEpoch:      2,
				InitialBlockSubsidy: tc.subsidy,
			}
			if tc.accept {
				require.NoError(t, version.Validate())
				require.NoError(t, scheduled.Validate())
				return
			}
			// Both records are held to the identical rule: a schedule entry becomes a
			// history version unchanged, so a spelling refused in one and admitted in
			// the other would be admitted in practice.
			require.ErrorIs(t, version.Validate(), types.ErrInvalidState)
			require.ErrorIs(t, scheduled.Validate(), types.ErrInvalidState)
		})
	}
}

// TestCanonicalAmountRejectsWhatTheGeneralParserAccepts states the difference
// between the two parsers as an explicit fact rather than leaving it implied.
//
// It also documents the values: these are not merely "unusual spellings of 10",
// they are different numbers.
func TestCanonicalAmountRejectsWhatTheGeneralParserAccepts(t *testing.T) {
	for _, tc := range []struct {
		value   string
		general string // what the general parser decodes it to
	}{
		{value: "010", general: "8"},
		{value: "0x10", general: "16"},
		{value: "0b101", general: "5"},
		{value: "0o17", general: "15"},
		{value: "1_0", general: "10"},
		{value: "+10", general: "10"},
	} {
		t.Run(tc.value, func(t *testing.T) {
			lenient, err := types.ParseAmountString("subsidy", tc.value)
			require.NoError(t, err, "the general parser is expected to accept this")
			require.Equal(t, tc.general, lenient.String(),
				"the general parser infers the radix, so this is a different number")

			_, err = types.ParseCanonicalAmount("subsidy", tc.value)
			require.ErrorIs(t, err, types.ErrInvalidState)
		})
	}
}
