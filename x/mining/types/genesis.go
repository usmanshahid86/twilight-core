package types

import "fmt"

// Fresh-genesis validation for x/mining.
//
// Split the way x/coreslot and x/rewards split it: GenesisState.Validate covers
// what the document can decide about itself, and the keeper covers what needs
// chain height or cross-module state. Keeping the first pure means the keeper can
// preflight the whole document before its first write rather than discovering a
// bad history halfway through the import.

// Recommended initial Selection geometry. These are the architecture's suggested
// starting values, not protocol constants: governance may configure any admissible
// combination. They exist so a default genesis is bootable and so the ACTIVE-Slot
// policy cross-check has something real to check against.
const (
	DefaultMaxSelectionRateBps                 uint64 = 2_500
	DefaultMaxSelectedParticipantsPerSelection uint64 = 64
	DefaultMaxCandidatesPerSelection           uint64 = 1_024
	DefaultBeaconStartOffsetBlocks             uint64 = 48
	DefaultBeaconWindowBlocks                  uint64 = 24
	DefaultMinExternalBeaconBlocks             uint64 = 12
	DefaultMinDistinctExternalProposers        uint64 = 3

	// DefaultSettlementWindowEpochs is the architecture's recommended initial
	// window. At the minimum admissible epoch length this is two epochs of
	// settlement-enabled blocks, which is time measured in chain progress rather
	// than wall clock — a paused chain does not consume it.
	DefaultSettlementWindowEpochs uint64 = 2
	DefaultMaxRecipientsPerChunk  uint64 = 32
	DefaultMaxChunksPerSettlement uint64 = 4
	// DefaultMinRecipientPayoutAmount equals the ratified immutable floor. A
	// deployment may configure higher; it can never configure lower.
	DefaultMinRecipientPayoutAmount = "10000"
)

// DefaultGenesis returns the fresh-genesis document for a trusted-distribution
// chain whose first epoch is 1.
//
// All three histories are written explicitly with a single version effective at
// epoch 1. None of them has a usable runtime default: a history that begins
// anywhere else could not bind the chain's first targets, and an absent history
// is corruption rather than a zero value the module may assume.
func DefaultGenesis() *GenesisState {
	return &GenesisState{
		DistributionModeVersions: []*MiningDistributionModeVersion{{
			Version:        1,
			Mode:           MiningDistributionMode_MINING_DISTRIBUTION_MODE_TRUSTED_AS_DISTRIBUTION,
			ValidFromEpoch: 1,
			// Open-ended: this profile has no path that closes it.
			ValidUntilEpochExclusive: 0,
		}},
		SelectionParamsVersions: []*SelectionParamsVersion{{
			Version:                             1,
			EffectiveEpoch:                      1,
			MaxSelectionRateBps:                 DefaultMaxSelectionRateBps,
			MaxSelectedParticipantsPerSelection: DefaultMaxSelectedParticipantsPerSelection,
			MaxCandidatesPerSelection:           DefaultMaxCandidatesPerSelection,
			BeaconStartOffsetBlocks:             DefaultBeaconStartOffsetBlocks,
			BeaconWindowBlocks:                  DefaultBeaconWindowBlocks,
			MinExternalBeaconBlocks:             DefaultMinExternalBeaconBlocks,
			MinDistinctExternalProposers:        DefaultMinDistinctExternalProposers,
		}},
		SettlementParamsVersions: []*SettlementParamsVersion{{
			Version:                  1,
			EffectiveEpoch:           1,
			SettlementWindowEpochs:   DefaultSettlementWindowEpochs,
			MaxRecipientsPerChunk:    DefaultMaxRecipientsPerChunk,
			MaxChunksPerSettlement:   DefaultMaxChunksPerSettlement,
			MinRecipientPayoutAmount: DefaultMinRecipientPayoutAmount,
		}},
		SettlementClock:          0,
		LastProcessedRewardEpoch: 0,
	}
}

func ValidateGenesis(genesis GenesisState) error { return genesis.Validate() }

func (g GenesisState) Validate() error {
	if err := g.validateDistributionModeHistory(); err != nil {
		return err
	}
	if err := g.validateSelectionParamsHistory(); err != nil {
		return err
	}
	if err := g.validateSettlementParamsHistory(); err != nil {
		return err
	}
	return g.validateFreshSettlementState()
}

// validateDistributionModeHistory requires exactly one open-ended genesis mode.
//
// A fresh chain has accepted no mode update, so it has exactly one version. That
// version must be open-ended: a closed interval at genesis would leave every
// epoch past its end with no mode at all, and a target that cannot resolve a mode
// cannot be materialized.
func (g GenesisState) validateDistributionModeHistory() error {
	if len(g.ScheduledDistributionModes) != 0 {
		return ErrInvalidGenesis.Wrapf(
			"fresh genesis carries %d scheduled distribution modes; the schedule must be empty",
			len(g.ScheduledDistributionModes))
	}
	if len(g.DistributionModeVersions) != 1 {
		return ErrInvalidGenesis.Wrapf(
			"fresh genesis requires exactly one distribution mode version, found %d",
			len(g.DistributionModeVersions))
	}
	anchor := g.DistributionModeVersions[0]
	if anchor == nil {
		return ErrInvalidGenesis.Wrap("distribution mode version is nil")
	}
	if err := anchor.Validate(); err != nil {
		return ErrInvalidGenesis.Wrap(err.Error())
	}
	if anchor.Version != 1 || anchor.ValidFromEpoch != 1 {
		return ErrInvalidGenesis.Wrapf(
			"the initial distribution mode must be version 1 valid from epoch 1, found version %d from epoch %d",
			anchor.Version, anchor.ValidFromEpoch)
	}
	if anchor.ValidUntilEpochExclusive != 0 {
		return ErrInvalidGenesis.Wrapf(
			"the initial distribution mode ends at epoch %d exclusive; a fresh genesis has accepted no later mode, so it must be open-ended",
			anchor.ValidUntilEpochExclusive)
	}
	// PROTOCOL_SELECTION is a representable mode but has no producer or consumer
	// in this profile: no candidate enrollment, no commitment, no beacon and no
	// result exist. A genesis that selected it would create targets nothing can
	// ever settle, so it is refused here rather than discovered at the first
	// epoch boundary.
	if anchor.Mode != MiningDistributionMode_MINING_DISTRIBUTION_MODE_TRUSTED_AS_DISTRIBUTION {
		return ErrInvalidGenesis.Wrapf(
			"the initial distribution mode is %s; this profile implements only trusted distribution",
			anchor.Mode)
	}
	return nil
}

// validateSelectionParamsHistory requires exactly one genesis version.
//
// The history exists because canonical fresh genesis requires it and because
// every ACTIVE CoreSlot policy is cross-checked against it. It enables nothing:
// there is no runtime update path and no Selection execution in this profile.
func (g GenesisState) validateSelectionParamsHistory() error {
	if len(g.ScheduledSelectionParams) != 0 {
		return ErrInvalidGenesis.Wrapf(
			"fresh genesis carries %d scheduled selection parameter versions; the schedule must be empty",
			len(g.ScheduledSelectionParams))
	}
	if len(g.SelectionParamsVersions) != 1 {
		return ErrInvalidGenesis.Wrapf(
			"fresh genesis requires exactly one selection parameter version, found %d",
			len(g.SelectionParamsVersions))
	}
	anchor := g.SelectionParamsVersions[0]
	if anchor == nil {
		return ErrInvalidGenesis.Wrap("selection parameter version is nil")
	}
	if err := anchor.Validate(); err != nil {
		return ErrInvalidGenesis.Wrap(err.Error())
	}
	if anchor.Version != 1 || anchor.EffectiveEpoch != 1 {
		return ErrInvalidGenesis.Wrapf(
			"the initial selection parameters must be version 1 effective at epoch 1, found version %d at epoch %d",
			anchor.Version, anchor.EffectiveEpoch)
	}
	return nil
}

// validateSettlementParamsHistory requires exactly one genesis version, admitted
// against the ratified immutable settlement bounds.
func (g GenesisState) validateSettlementParamsHistory() error {
	if len(g.ScheduledSettlementParams) != 0 {
		return ErrInvalidGenesis.Wrapf(
			"fresh genesis carries %d scheduled settlement parameter versions; the schedule must be empty",
			len(g.ScheduledSettlementParams))
	}
	if len(g.SettlementParamsVersions) != 1 {
		return ErrInvalidGenesis.Wrapf(
			"fresh genesis requires exactly one settlement parameter version, found %d",
			len(g.SettlementParamsVersions))
	}
	anchor := g.SettlementParamsVersions[0]
	if anchor == nil {
		return ErrInvalidGenesis.Wrap("settlement parameter version is nil")
	}
	if err := anchor.Validate(); err != nil {
		return ErrInvalidGenesis.Wrap(err.Error())
	}
	if anchor.Version != 1 || anchor.EffectiveEpoch != 1 {
		return ErrInvalidGenesis.Wrapf(
			"the initial settlement parameters must be version 1 effective at epoch 1, found version %d at epoch %d",
			anchor.Version, anchor.EffectiveEpoch)
	}
	return nil
}

// validateFreshSettlementState refuses any trace of a running chain's settlement
// workflow.
//
// A fresh chain has finalized no reward epoch, so it has materialized nothing,
// anchored nothing, and advanced neither the clock nor the cursor. These are four
// consequences of one fact and are enforced as one rule.
//
// They are validation rules rather than defaults. Silently normalizing a document
// that carries settlement state would be deciding what that state means across a
// restart, which is continuation import — deferred, and not a decision a fresh
// importer gets to make by accident.
func (g GenesisState) validateFreshSettlementState() error {
	if g.SettlementClock != 0 {
		return ErrInvalidGenesis.Wrapf(
			"fresh genesis carries a settlement clock of %d; a fresh chain has produced no settlement-enabled block",
			g.SettlementClock)
	}
	if g.LastProcessedRewardEpoch != 0 {
		return ErrInvalidGenesis.Wrapf(
			"fresh genesis carries a processed reward epoch cursor of %d; a fresh chain has finalized no epoch",
			g.LastProcessedRewardEpoch)
	}
	if len(g.SettlementEpochAnchors) != 0 {
		return ErrInvalidGenesis.Wrapf(
			"fresh genesis carries %d settlement epoch anchors; a fresh chain has materialized none",
			len(g.SettlementEpochAnchors))
	}
	if len(g.Settlements) != 0 {
		return ErrInvalidGenesis.Wrapf(
			"fresh genesis carries %d settlements; a fresh chain has materialized none",
			len(g.Settlements))
	}
	return nil
}

// Validate checks one settlement record's internal coherence.
//
// Exported because the fail-closed read paths use it too: a settlement is read
// back before every authorization decision, and a row that disagrees with itself
// must stop that decision rather than inform it.
func (s Settlement) Validate() error {
	if s.SlotId == 0 {
		return ErrInvalidState.Wrap("settlement requires a nonzero slot")
	}
	if s.Epoch == 0 {
		return ErrInvalidState.Wrapf("settlement for slot %d requires a nonzero epoch", s.SlotId)
	}
	if s.DistributionModeVersion == 0 || s.SettlementParamsVersion == 0 {
		return ErrInvalidState.Wrapf(
			"%s must record the mode and settlement parameter versions it was bound to", settlementLabel(s))
	}
	switch s.SettlementMode {
	case SettlementMode_SETTLEMENT_MODE_TRUSTED_AS,
		SettlementMode_SETTLEMENT_MODE_SELECTED_PARTICIPANTS,
		SettlementMode_SETTLEMENT_MODE_OPERATOR_ONLY:
	default:
		return ErrInvalidState.Wrapf("%s has no canonical settlement mode", settlementLabel(s))
	}
	// Lifecycle and terminal metadata must agree. A row that claims to be open
	// while naming the arm it was finalized through, or one that claims to be
	// terminal without naming one, is a row no admitted transition produced.
	if s.Finalized {
		if s.FinalizationReason == SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_UNSPECIFIED {
			return ErrInvalidState.Wrapf("%s is finalized without recording an authorization arm", settlementLabel(s))
		}
		if s.FinalizedHeight == 0 {
			return ErrInvalidState.Wrapf("%s is finalized without a finalization height", settlementLabel(s))
		}
		return nil
	}
	if s.FinalizationReason != SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_UNSPECIFIED {
		return ErrInvalidState.Wrapf(
			"%s is open but records finalization reason %s", settlementLabel(s), s.FinalizationReason)
	}
	if s.FinalizedHeight != 0 {
		return ErrInvalidState.Wrapf(
			"%s is open but records finalization height %d", settlementLabel(s), s.FinalizedHeight)
	}
	return nil
}

// Validate checks one settlement epoch anchor.
func (a SettlementEpochAnchor) Validate() error {
	if a.Epoch == 0 {
		return ErrInvalidState.Wrap("settlement epoch anchor requires a nonzero epoch")
	}
	return nil
}

func settlementLabel(s Settlement) string {
	return fmt.Sprintf("settlement for slot %d in epoch %d", s.SlotId, s.Epoch)
}
