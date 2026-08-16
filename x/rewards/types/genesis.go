package types

import sdkmath "cosmossdk.io/math"

// DefaultGenesis returns the fresh-genesis document for a chain whose first
// block is height 1.
//
// The epoch anchor, the canonical pause state and the open counter are all
// written explicitly. None of them has a usable default at runtime: the anchor
// fixes every later boundary, and an absent pause state or counter is corruption
// rather than a zero value the module may assume.
func DefaultGenesis() *GenesisState {
	params := DefaultParams()
	snapshot := DefaultEpochConfigSnapshot(params)
	anchor := DefaultEpochConfigVersion(params, 1)
	return &GenesisState{
		Params:                  &params,
		State:                   &RewardsState{CurrentEpoch: 1, CurrentEpochStartHeight: 1, CumulativeEmitted: "0", CarryForwardRemainder: "0"},
		CurrentEpochConfig:      &snapshot,
		EpochConfigVersions:     []*EpochConfigVersion{&anchor},
		PauseState:              &RewardsPauseState{},
		OpenRewardEnabledBlocks: 0,
	}
}

func ValidateGenesis(genesis GenesisState) error {
	return genesis.Validate()
}

func (g GenesisState) Validate() error {
	if g.Params == nil || g.State == nil || g.CurrentEpochConfig == nil {
		return ErrInvalidGenesis.Wrap("params, state, and current epoch config are required")
	}
	if err := g.Params.Validate(); err != nil {
		return ErrInvalidGenesis.Wrap(err.Error())
	}
	if g.State.CurrentEpoch == 0 || g.State.CurrentEpochStartHeight == 0 {
		return ErrInvalidGenesis.Wrap("current epoch and start height must be positive")
	}
	if err := validateStateAmount("cumulative emitted", g.State.CumulativeEmitted); err != nil {
		return err
	}
	if err := validateStateAmount("carry forward remainder", g.State.CarryForwardRemainder); err != nil {
		return err
	}
	cumulative, _ := sdkmath.NewIntFromString(g.State.CumulativeEmitted)
	maxSupply, _ := sdkmath.NewIntFromString(g.Params.MaxSupply)
	if cumulative.GT(maxSupply) {
		return ErrInvalidGenesis.Wrap("cumulative emitted exceeds max supply")
	}
	if err := g.CurrentEpochConfig.Validate(); err != nil {
		return err
	}
	if err := g.validateEpochTimeline(); err != nil {
		return err
	}
	if g.HasPendingParams {
		if g.PendingParams == nil {
			return ErrInvalidGenesis.Wrap("pending params flag requires pending params")
		}
		if err := ValidateUpdate(*g.Params, *g.PendingParams); err != nil {
			return ErrInvalidGenesis.Wrapf("pending params: %v", err)
		}
	} else if g.PendingParams != nil {
		return ErrInvalidGenesis.Wrap("pending params require explicit presence flag")
	}

	epochs := make(map[uint64]struct{}, len(g.FinalizedEpochs))
	for _, epoch := range g.FinalizedEpochs {
		if err := validateEpochReward(epoch); err != nil {
			return err
		}
		if _, exists := epochs[epoch.EpochNumber]; exists {
			return invalidDuplicate("finalized epoch", epoch.EpochNumber)
		}
		epochs[epoch.EpochNumber] = struct{}{}
	}

	claims := make(map[string]struct{}, len(g.ClaimRecords))
	for _, reward := range g.ClaimRecords {
		if err := validateEligibleReward(reward); err != nil {
			return err
		}
		key := claimKey(reward.SlotId, reward.EpochNumber)
		if _, exists := claims[key]; exists {
			return ErrInvalidGenesis.Wrapf("duplicate claim record %s", key)
		}
		if _, exists := epochs[reward.EpochNumber]; !exists {
			return ErrInvalidGenesis.Wrapf("claim record %s references non-finalized epoch", key)
		}
		claims[key] = struct{}{}
	}
	return nil
}

// Fresh-genesis validation for the canonical epoch timeline.
//
// Split the way x/coreslot splits it: GenesisState.Validate covers what the
// document can decide about itself, and ValidateFreshGenesisInitialHeight covers
// what only a caller who knows the chain's initial height can decide. Keeping the
// second pure means the keeper preflights the whole document before its first
// write rather than discovering an anchor mismatch halfway through the import.

// validateEpochTimeline checks the internally decidable shape of the canonical
// epoch history, its schedule, the pause state and the open counter.
func (g GenesisState) validateEpochTimeline() error {
	if g.PauseState == nil {
		return ErrInvalidGenesis.Wrap("canonical rewards pause state is required")
	}
	if err := g.PauseState.Validate(); err != nil {
		return ErrInvalidGenesis.Wrap(err.Error())
	}

	// §80 requires an empty schedule at fresh genesis. This is a canonical
	// CONTENT rule about which entries may exist, and is independent of the open
	// question about how malformed collections are treated: there is no admissible
	// entry here to have an opinion about.
	if len(g.ScheduledEpochConfigs) != 0 {
		return ErrInvalidGenesis.Wrapf(
			"fresh genesis carries %d scheduled epoch configurations; the schedule must be empty",
			len(g.ScheduledEpochConfigs))
	}
	if g.OpenRewardEnabledBlocks != 0 {
		return ErrInvalidGenesis.Wrapf(
			"fresh genesis carries %d open reward-enabled blocks; the open epoch has accrued none",
			g.OpenRewardEnabledBlocks)
	}

	if len(g.EpochConfigVersions) != 1 {
		return ErrInvalidGenesis.Wrapf(
			"fresh genesis requires exactly one epoch configuration version, found %d",
			len(g.EpochConfigVersions))
	}
	anchor := g.EpochConfigVersions[0]
	if anchor == nil {
		return ErrInvalidGenesis.Wrap("epoch configuration version is nil")
	}
	if err := anchor.Validate(); err != nil {
		return ErrInvalidGenesis.Wrap(err.Error())
	}
	if anchor.Version != 1 || anchor.EffectiveEpoch != 1 {
		return ErrInvalidGenesis.Wrapf(
			"the original-genesis epoch anchor must be version 1 effective at epoch 1, found version %d at epoch %d",
			anchor.Version, anchor.EffectiveEpoch)
	}
	if g.State.CurrentEpoch != 1 {
		return ErrInvalidGenesis.Wrapf(
			"fresh genesis must open at epoch 1, found epoch %d", g.State.CurrentEpoch)
	}
	if g.State.CurrentEpochStartHeight != anchor.EffectiveStartHeight {
		return ErrInvalidGenesis.Wrapf(
			"current epoch starts at height %d but the epoch anchor is effective at height %d",
			g.State.CurrentEpochStartHeight, anchor.EffectiveStartHeight)
	}

	// The two deprecated mirrors must agree with the authority. They carry no
	// authority themselves, which is exactly why they are pinned here: an
	// unpinned mirror is a second number that looks authoritative to a reader.
	if g.CurrentEpochConfig.EpochLengthBlocks != anchor.EpochLengthBlocks {
		return ErrInvalidGenesis.Wrapf(
			"current epoch config mirrors epoch length %d but the canonical version is %d",
			g.CurrentEpochConfig.EpochLengthBlocks, anchor.EpochLengthBlocks)
	}
	if g.Params.EpochLengthBlocks != anchor.EpochLengthBlocks {
		return ErrInvalidGenesis.Wrapf(
			"params mirror epoch length %d but the canonical version is %d",
			g.Params.EpochLengthBlocks, anchor.EpochLengthBlocks)
	}
	return nil
}

// ValidateFreshGenesisInitialHeight pins the height-bearing portions of the
// canonical epoch timeline to the chain's effective first-block height.
//
// Two rules, and only two:
//
//   - the original-genesis anchor starts at the initial height. §11 makes this
//     the permanent anchor of every later boundary, so an anchor that disagrees
//     with the chain's own first block misplaces the entire history.
//   - a pending pause transition that is already due is rejected. Transitions are
//     consumed exactly once at the height they name, so one naming a height at or
//     below the first block could never be applied by any block.
//
// It deliberately takes NO position on a pending transition scheduled for a
// future height. Whether fresh genesis may seed one is unresolved, and both
// accepting and rejecting it here would settle that question by implementation.
func ValidateFreshGenesisInitialHeight(genesis *GenesisState, initialHeight int64) error {
	if genesis == nil {
		return ErrInvalidGenesis.Wrap("genesis state is nil")
	}
	if initialHeight < 1 {
		return ErrInvalidGenesis.Wrapf("initial height %d must be positive", initialHeight)
	}
	height := uint64(initialHeight)

	if len(genesis.EpochConfigVersions) != 1 || genesis.EpochConfigVersions[0] == nil {
		return ErrInvalidGenesis.Wrap("fresh genesis requires exactly one epoch configuration version")
	}
	if got := genesis.EpochConfigVersions[0].EffectiveStartHeight; got != height {
		return ErrInvalidGenesis.Wrapf(
			"the original-genesis epoch anchor starts at height %d but the chain starts at height %d",
			got, height)
	}
	if genesis.State != nil && genesis.State.CurrentEpochStartHeight != height {
		return ErrInvalidGenesis.Wrapf(
			"the open epoch starts at height %d but the chain starts at height %d",
			genesis.State.CurrentEpochStartHeight, height)
	}
	if genesis.PauseState != nil && genesis.PauseState.HasPending &&
		genesis.PauseState.PendingEffectiveHeight <= height {
		return ErrInvalidGenesis.Wrapf(
			"a rewards pause transition is pending for height %d, which the chain has already reached at height %d",
			genesis.PauseState.PendingEffectiveHeight, height)
	}
	return nil
}
