package types_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// Fresh-genesis validation of the canonical epoch timeline.
//
// The anchor is the permanent origin of every later boundary, so these are not
// shape checks for their own sake: an anchor accepted at the wrong epoch, height
// or length misplaces every epoch the chain will ever finalize.

func freshGenesis(t *testing.T) *types.GenesisState {
	t.Helper()
	genesis := types.DefaultGenesis()
	require.NoError(t, genesis.Validate())
	return genesis
}

func TestFreshGenesisRequiresTheOriginalGenesisAnchor(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*types.GenesisState)
	}{
		{
			name:   "no history at all",
			mutate: func(g *types.GenesisState) { g.EpochConfigVersions = nil },
		},
		{
			name: "a second version at fresh genesis",
			mutate: func(g *types.GenesisState) {
				second := types.EpochConfigVersion{Version: 2, EffectiveEpoch: 2, EffectiveStartHeight: 100, EpochLengthBlocks: 10}
				g.EpochConfigVersions = append(g.EpochConfigVersions, &second)
			},
		},
		{
			name:   "the anchor is not version 1",
			mutate: func(g *types.GenesisState) { g.EpochConfigVersions[0].Version = 2 },
		},
		{
			name:   "the anchor is not effective at epoch 1",
			mutate: func(g *types.GenesisState) { g.EpochConfigVersions[0].EffectiveEpoch = 2 },
		},
		{
			name:   "the anchor has no length",
			mutate: func(g *types.GenesisState) { g.EpochConfigVersions[0].EpochLengthBlocks = 0 },
		},
		{
			name: "the open epoch does not start where the anchor says",
			mutate: func(g *types.GenesisState) {
				g.EpochConfigVersions[0].EffectiveStartHeight = 7
			},
		},
		{
			name:   "the open epoch is not epoch 1",
			mutate: func(g *types.GenesisState) { g.State.CurrentEpoch = 4 },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			genesis := freshGenesis(t)
			tc.mutate(genesis)
			require.Error(t, genesis.Validate())
		})
	}
}

// TestFreshGenesisPinsTheDeprecatedMirrors is what keeps the two retained copies
// of the epoch length from becoming a second apparent authority. They carry no
// authority, which is exactly why they must not be allowed to disagree.
func TestFreshGenesisPinsTheDeprecatedMirrors(t *testing.T) {
	t.Run("the epoch config snapshot mirror must agree", func(t *testing.T) {
		genesis := freshGenesis(t)
		genesis.CurrentEpochConfig.EpochLengthBlocks++
		require.Error(t, genesis.Validate())
	})

	t.Run("the params mirror must agree", func(t *testing.T) {
		genesis := freshGenesis(t)
		genesis.Params.EpochLengthBlocks++
		require.Error(t, genesis.Validate())
	})
}

// TestFreshGenesisRequiresAnEmptySchedule covers §80's explicit content rule.
//
// This is a rule about which entries may exist, not about how malformed
// collections are treated, so it is decidable today: there is no admissible
// scheduled entry at fresh genesis to have an opinion about.
func TestFreshGenesisRequiresAnEmptySchedule(t *testing.T) {
	genesis := freshGenesis(t)
	scheduled := types.ScheduledEpochConfig{EffectiveEpoch: 4, EpochLengthBlocks: 10}
	genesis.ScheduledEpochConfigs = []*types.ScheduledEpochConfig{&scheduled}
	require.Error(t, genesis.Validate())
}

func TestFreshGenesisRequiresAZeroOpenCounter(t *testing.T) {
	genesis := freshGenesis(t)
	genesis.OpenRewardEnabledBlocks = 3
	require.Error(t, genesis.Validate())
}

func TestFreshGenesisRequiresACanonicalPauseState(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		genesis := freshGenesis(t)
		genesis.PauseState = nil
		require.Error(t, genesis.Validate())
	})

	t.Run("a pending value with no pending transition", func(t *testing.T) {
		// has_pending is what separates a scheduled resume from no schedule at
		// all; without it a stale height could survive and be applied later.
		genesis := freshGenesis(t)
		genesis.PauseState = &types.RewardsPauseState{PendingValue: true}
		require.Error(t, genesis.Validate())
	})

	t.Run("a pending transition with no height", func(t *testing.T) {
		genesis := freshGenesis(t)
		genesis.PauseState = &types.RewardsPauseState{HasPending: true}
		require.Error(t, genesis.Validate())
	})
}

// TestFreshGenesisInitialHeightAnchoring covers the height-bearing half, which
// only a caller that knows the chain's first block can decide.
func TestFreshGenesisInitialHeightAnchoring(t *testing.T) {
	build := func(t *testing.T, height uint64) *types.GenesisState {
		t.Helper()
		params := types.DefaultParams()
		snapshot := types.DefaultEpochConfigSnapshot(params)
		anchor := types.DefaultEpochConfigVersion(params, height)
		return &types.GenesisState{
			Params: &params,
			State: &types.RewardsState{
				CurrentEpoch: 1, CurrentEpochStartHeight: height,
				CumulativeEmitted: "0", CarryForwardRemainder: "0",
			},
			CurrentEpochConfig:  &snapshot,
			EpochConfigVersions: []*types.EpochConfigVersion{&anchor},
			PauseState:          &types.RewardsPauseState{},
		}
	}

	t.Run("a chain starting above height 1 anchors there", func(t *testing.T) {
		genesis := build(t, 500)
		require.NoError(t, genesis.Validate())
		require.NoError(t, types.ValidateFreshGenesisInitialHeight(genesis, 500))
	})

	t.Run("an anchor that disagrees with the chain is refused", func(t *testing.T) {
		genesis := build(t, 1)
		require.NoError(t, genesis.Validate())
		require.Error(t, types.ValidateFreshGenesisInitialHeight(genesis, 500),
			"the anchor is the origin of every later boundary and cannot be off by any amount")
	})

	// The pause rule is covered in full by TestFreshGenesisForbidsAnyPending-
	// PauseTransition below, which is where it belongs: it does not depend on the
	// initial height.
}

// TestFreshGenesisForbidsAnyPendingPauseTransition covers the ratified rule.
//
// A pending transition is the residue of an authority transaction accepted in
// block H and due at H+1. Fresh genesis has no pre-genesis block, so no such
// transaction can have happened, and genesis already selects the initial reward
// state directly through current_paused. The rule is therefore not "reject stale
// ones" — no pending transition is admissible at any height, and the height it
// names does not enter the decision.
//
// This is FRESH genesis only. Continuation import must preserve a pending H+1
// transition, because there it really was created by a transaction on the
// exporting chain.
func TestFreshGenesisForbidsAnyPendingPauseTransition(t *testing.T) {
	const initialHeight = 500

	build := func(t *testing.T, pause types.RewardsPauseState) *types.GenesisState {
		t.Helper()
		params := types.DefaultParams()
		snapshot := types.DefaultEpochConfigSnapshot(params)
		anchor := types.DefaultEpochConfigVersion(params, initialHeight)
		return &types.GenesisState{
			Params: &params,
			State: &types.RewardsState{
				CurrentEpoch: 1, CurrentEpochStartHeight: initialHeight,
				CumulativeEmitted: "0", CarryForwardRemainder: "0",
			},
			CurrentEpochConfig:  &snapshot,
			EpochConfigVersions: []*types.EpochConfigVersion{&anchor},
			PauseState:          &pause,
		}
	}

	for _, tc := range []struct {
		name   string
		pause  types.RewardsPauseState
		accept bool
	}{
		{
			name:   "no pending transition, starting unpaused",
			pause:  types.RewardsPauseState{CurrentPaused: false},
			accept: true,
		},
		{
			name:   "no pending transition, starting paused",
			pause:  types.RewardsPauseState{CurrentPaused: true},
			accept: true,
		},
		{
			name: "pending at the initial height",
			pause: types.RewardsPauseState{
				HasPending: true, PendingValue: true, PendingEffectiveHeight: initialHeight,
			},
		},
		{
			name: "pending at the initial height plus one",
			pause: types.RewardsPauseState{
				HasPending: true, PendingValue: true, PendingEffectiveHeight: initialHeight + 1,
			},
		},
		{
			name: "pending far in the future",
			pause: types.RewardsPauseState{
				HasPending: true, PendingValue: false, PendingEffectiveHeight: initialHeight + 10_000,
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			genesis := build(t, tc.pause)
			// Both entry points agree: the document-level rule and the last preflight
			// before the module's first write.
			if tc.accept {
				require.NoError(t, genesis.Validate())
				require.NoError(t, types.ValidateFreshGenesisInitialHeight(genesis, initialHeight))
				return
			}
			require.Error(t, genesis.Validate())
			require.Error(t, types.ValidateFreshGenesisInitialHeight(genesis, initialHeight))
		})
	}

	t.Run("a cleared flag over residual pending fields is still refused", func(t *testing.T) {
		// has_pending false with a height left behind is the inconsistency that
		// would later read back as a transition nobody scheduled.
		genesis := build(t, types.RewardsPauseState{PendingEffectiveHeight: initialHeight + 1})
		require.Error(t, genesis.Validate())
		require.Error(t, types.ValidateFreshGenesisInitialHeight(genesis, initialHeight))
	})
}
