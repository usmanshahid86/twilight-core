package types

import (
	sdkmath "cosmossdk.io/math"

	appparams "github.com/twilight-project/twilight-core/app/params"
)

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
	rewardAnchor := DefaultRewardConfigVersion(params)
	return &GenesisState{
		Params:                  &params,
		State:                   &RewardsState{CurrentEpoch: 1, CurrentEpochStartHeight: 1, CumulativeEmitted: "0", CarryForwardRemainder: "0"},
		CurrentEpochConfig:      &snapshot,
		EpochConfigVersions:     []*EpochConfigVersion{&anchor},
		RewardConfigVersions:    []*RewardConfigVersion{&rewardAnchor},
		PauseState:              &RewardsPauseState{},
		OpenRewardEnabledBlocks: 0,
		// Written explicitly rather than left to the zero string. After genesis an
		// absent liability is corruption, and an empty string does not parse.
		OutstandingEntitlementLiability: "0",
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
	if err := g.validateRewardConfigTimeline(); err != nil {
		return err
	}
	if err := g.validateFreshLedgerState(); err != nil {
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

	// The two loops below are reached only by a document that carries closed-epoch
	// state, which validateFreshLedgerState above now refuses. They are kept rather
	// than deleted: they are the RECORD-level rules for these types, and a
	// continuation importer — which is deferred, not cancelled — needs exactly them.
	// Removing the shape rules for a collection because the current importer will
	// not see one would be retiring the legacy surface, which belongs elsewhere.
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
	// Fresh genesis carries NO pending pause transition, at any height.
	//
	// A pending transition is not a configuration choice; it is the residue of an
	// authority transaction accepted in block H and due at H+1. Fresh genesis has
	// no pre-genesis block and therefore no transaction that could have created
	// one. Genesis already selects the initial reward state directly through
	// current_paused, so a pending entry is never the only way to express an
	// intent — it is either meaningless or a second, contradictory answer to the
	// question current_paused already answers.
	//
	// This is a FRESH-genesis rule only. Continuation import, when it exists, must
	// preserve a pending H+1 transition: there the transition really was created
	// by a transaction on the exporting chain, and dropping it would silently
	// discard an accepted authority decision.
	if err := validateFreshGenesisPauseState(*g.PauseState); err != nil {
		return err
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
	// The canonical bound is enforced on the AUTHORITY, not on either deprecated
	// mirror. The mirrors are pinned to the anchor immediately below, so checking
	// the anchor is what makes all three admissible; checking a mirror instead
	// would leave the value that actually governs geometry unchecked.
	if err := appparams.ValidateEpochLengthBlocks(anchor.EpochLengthBlocks); err != nil {
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

// validateRewardConfigTimeline checks the fresh-genesis shape of the canonical
// reward-configuration history and its schedule.
//
// Three rules, mirroring what validateEpochTimeline does for geometry: exactly
// one anchor with a fixed identity, an empty schedule, and deprecated mirrors
// pinned to the anchor.
func (g GenesisState) validateRewardConfigTimeline() error {
	// §80 requires an empty schedule at fresh genesis, for the same reason the
	// pause state carries no pending transition: a scheduled configuration is the
	// residue of an authority transaction accepted in some block, and fresh genesis
	// has no pre-genesis block that could have produced one. Genesis selects the
	// initial economics directly through the anchor, so a scheduled entry is never
	// the only way to express an intent — it is a second, contradictory answer.
	//
	// This is a canonical CONTENT rule about which entries may exist, and is
	// independent of the open question about how malformed collections are
	// treated: there is no admissible entry here to have an opinion about.
	if len(g.ScheduledRewardConfigs) != 0 {
		return ErrInvalidGenesis.Wrapf(
			"fresh genesis carries %d scheduled reward configurations; the schedule must be empty",
			len(g.ScheduledRewardConfigs))
	}

	if len(g.RewardConfigVersions) != 1 {
		return ErrInvalidGenesis.Wrapf(
			"fresh genesis requires exactly one reward configuration version, found %d",
			len(g.RewardConfigVersions))
	}
	anchor := g.RewardConfigVersions[0]
	if anchor == nil {
		return ErrInvalidGenesis.Wrap("reward configuration version is nil")
	}
	if err := anchor.Validate(); err != nil {
		return ErrInvalidGenesis.Wrap(err.Error())
	}
	if anchor.Version != 1 || anchor.EffectiveEpoch != 1 {
		return ErrInvalidGenesis.Wrapf(
			"the initial reward configuration must be version 1 effective at epoch 1, found version %d at epoch %d",
			anchor.Version, anchor.EffectiveEpoch)
	}

	return g.validateRewardConfigMirrors(*anchor)
}

// validateFreshLedgerState checks the fresh-genesis shape of everything a closed
// epoch leaves behind: the finalized-epoch archive, the legacy claim collection,
// canonical entitlements, and the liability accumulator.
//
// A fresh chain has finalized no epoch. It therefore has no archive, no
// obligations in either representation, and owes nothing — and this is a single
// rule rather than four, because they are four consequences of the same fact.
//
// Each is a canonical CONTENT rule, about which entries may exist at all rather
// than about how a malformed collection is treated, so all four are enforceable
// without settling the open duplicate/ordering question.
//
// # Legacy claim records
//
// Refusing them here is not the legacy retirement work package. ClaimRewards, its
// query surface and its CLI all remain, and continue to serve state seeded
// directly for explicitly legacy regression coverage. What this closes is the one
// path by which a conforming POC1 chain could ever hold a claim record at all:
// V2 finalization creates entitlements and nothing else, so genesis was the only
// remaining source. With it closed, a payable claim record and a payable
// entitlement can no longer coexist over one escrow on a conforming chain.
//
// # Why the liability must be the exact string
//
// It is required to be the literal "0" rather than merely to parse as zero. The
// value is written, not defaulted, and it is written back out on export; accepting
// "+0" or "00" here would put a spelling into canonical state that the chain never
// produces, and every later comparison against it would be comparing something the
// module cannot itself write.
func (g GenesisState) validateFreshLedgerState() error {
	if len(g.FinalizedEpochs) != 0 {
		return ErrInvalidGenesis.Wrapf(
			"fresh genesis carries %d finalized epochs; a fresh chain has closed none",
			len(g.FinalizedEpochs))
	}
	if len(g.ClaimRecords) != 0 {
		return ErrInvalidGenesis.Wrapf(
			"fresh genesis carries %d legacy claim records; canonical obligations are slot entitlements, "+
				"created only by finalization",
			len(g.ClaimRecords))
	}
	if len(g.SlotEntitlements) != 0 {
		return ErrInvalidGenesis.Wrapf(
			"fresh genesis carries %d slot entitlements; a fresh chain has finalized no epoch",
			len(g.SlotEntitlements))
	}
	if g.OutstandingEntitlementLiability != "0" {
		return ErrInvalidGenesis.Wrapf(
			"fresh genesis outstanding entitlement liability is %q; a fresh chain owes nothing, "+
				"written canonically as \"0\"",
			g.OutstandingEntitlementLiability)
	}
	return nil
}

// validateRewardConfigMirrors pins the deprecated economic mirrors to the
// canonical anchor.
//
// The same treatment epoch length already receives, and for the same reason. The
// three values below moved to RewardConfigVersion, which is now the only thing
// finalization reads; the copies on Params and on the epoch-configuration
// snapshot survive as compatibility data and carry no authority.
//
// Carrying no authority is exactly why they must not be free to state a different
// number. Both remain observable — Params through its query, the snapshot through
// the finalized epoch records it is embedded in — so an unpinned mirror is a
// second economic figure that looks authoritative to a reader and is permanently
// archived beside the epochs it did not govern.
//
// The treasury address is compared even when the share is zero. A disabled
// treasury legitimately carries no address, and "" == "" satisfies this; what it
// refuses is a genesis that names different destinations in two places while
// disagreeing about whether either can receive anything.
func (g GenesisState) validateRewardConfigMirrors(anchor RewardConfigVersion) error {
	for _, mirror := range []struct {
		label    string
		subsidy  string
		shareBps uint64
		treasury string
	}{
		{"params", g.Params.InitialBlockSubsidy, g.Params.EmissionTreasuryShareBps, g.Params.TreasuryAddress},
		{
			"current epoch config",
			g.CurrentEpochConfig.InitialBlockSubsidy,
			g.CurrentEpochConfig.EmissionTreasuryShareBps,
			g.CurrentEpochConfig.TreasuryAddress,
		},
	} {
		if mirror.subsidy != anchor.InitialBlockSubsidy {
			return ErrInvalidGenesis.Wrapf(
				"%s mirrors initial block subsidy %q but the canonical reward configuration is %q",
				mirror.label, mirror.subsidy, anchor.InitialBlockSubsidy)
		}
		if mirror.shareBps != anchor.EmissionTreasuryShareBps {
			return ErrInvalidGenesis.Wrapf(
				"%s mirrors emission treasury share %d bps but the canonical reward configuration is %d bps",
				mirror.label, mirror.shareBps, anchor.EmissionTreasuryShareBps)
		}
		if mirror.treasury != anchor.TreasuryAddress {
			return ErrInvalidGenesis.Wrapf(
				"%s mirrors treasury address %q but the canonical reward configuration is %q",
				mirror.label, mirror.treasury, anchor.TreasuryAddress)
		}
	}
	return nil
}

// validateFreshGenesisPauseState requires the normalized fresh-genesis shape of
// the canonical pause state: an explicit current value and no pending
// transition of any kind.
//
// The three fields are checked individually rather than through the has-pending
// flag alone, because a cleared flag over a non-zero pending value or height is
// exactly the inconsistency that would later be read back as a transition nobody
// scheduled.
func validateFreshGenesisPauseState(state RewardsPauseState) error {
	if state.HasPending {
		return ErrInvalidGenesis.Wrapf(
			"fresh genesis carries a pending rewards pause transition to %t at height %d; "+
				"fresh genesis has no pre-genesis transaction able to create one, and selects "+
				"the initial state through current_paused",
			state.PendingValue, state.PendingEffectiveHeight)
	}
	if state.PendingValue || state.PendingEffectiveHeight != 0 {
		return ErrInvalidGenesis.Wrapf(
			"fresh genesis rewards pause state declares no pending transition but carries "+
				"pending value %t at height %d",
			state.PendingValue, state.PendingEffectiveHeight)
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
//   - the pause state carries no pending transition. This restates the rule the
//     document-level validation already applies, because this function is the
//     last preflight before the module's first write and the rule is a
//     fresh-genesis rule: a continuation importer must NOT reuse it.
//
// The pending rule is deliberately not expressed relative to initialHeight. It
// is not that an already-due transition is stale and a future one might be
// admissible — no pending transition is admissible at fresh genesis at all, so
// the initial height is irrelevant to the decision.
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
	if genesis.PauseState != nil {
		if err := validateFreshGenesisPauseState(*genesis.PauseState); err != nil {
			return err
		}
	}
	return nil
}
