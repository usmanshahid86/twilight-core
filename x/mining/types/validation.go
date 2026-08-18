package types

import (
	"fmt"

	sdkmath "cosmossdk.io/math"

	appparams "github.com/twilight-project/twilight-core/app/params"
)

// Shape validation for the canonical mining histories.
//
// # Version numbering: unique and monotonic, NOT contiguous
//
// The three histories here require version numbers and effective epochs to be
// unique and monotonically increasing. They do NOT require a version to be its
// predecessor's successor.
//
// That is a deliberate divergence from the reward-configuration history, whose
// contiguity was separately ratified so that a version-number query could
// classify absence arithmetically. Nothing ratified contiguity for these
// families, and imposing it here would be stricter than the architecture: it
// would reject a history the protocol permits.
//
// The cost of that choice is that a version number alone cannot decide absence —
// with legitimate gaps, "no version 5" and "version 5 was never assigned" are the
// same observation. Lookup by version therefore goes through a derived index, and
// a missing index entry is reported as absence rather than as corruption. What
// keeps the index from becoming authority is that every entry it does resolve is
// cross-checked against the canonical row in both directions.

// ParseCanonicalAmount is the canonical base-denom decoding for monetary values
// this module admits from outside.
//
// It exists here rather than being borrowed from x/rewards because a module must
// own the decode of values it acts on: sharing a permissive parser across a module
// boundary is how one module's leniency becomes another's authorization. The rule
// is the same one x/rewards applies, and deliberately so — canonical base-10 only,
// because the arbitrary-precision decoder infers the radix and would read "010" as
// 8 and "0x10" as 16.
func ParseCanonicalAmount(name, value string) (sdkmath.Int, error) {
	if value == "" {
		return sdkmath.Int{}, ErrInvalidState.Wrapf("%s is empty", name)
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return sdkmath.Int{}, ErrInvalidState.Wrapf(
				"%s %q is not a canonical base-10 integer", name, value)
		}
	}
	if len(value) > 1 && value[0] == '0' {
		return sdkmath.Int{}, ErrInvalidState.Wrapf("%s %q has a leading zero", name, value)
	}
	amount, ok := sdkmath.NewIntFromString(value)
	if !ok {
		return sdkmath.Int{}, ErrInvalidState.Wrapf("%s %q is not an integer", name, value)
	}
	// Unreachable through the digit scan above, which admits no sign character.
	// Asserted anyway: a later relaxation of the scan would otherwise pass
	// silently on a value that authorizes a transfer.
	if amount.IsNegative() {
		return sdkmath.Int{}, ErrInvalidState.Wrapf("%s %q is negative", name, value)
	}
	return amount, nil
}

// Validate checks one distribution-mode history entry.
//
// The interval is checked as an interval: a version that ends before it begins
// describes no epoch at all, and one that ends exactly where it begins describes
// an empty span. Both are refused rather than treated as a closed version,
// because a mode history that resolves nothing for some epoch cannot bind a
// target.
func (v MiningDistributionModeVersion) Validate() error {
	if v.Version == 0 {
		return ErrInvalidState.Wrap("distribution mode version must be positive")
	}
	if v.ValidFromEpoch == 0 {
		return ErrInvalidState.Wrapf(
			"distribution mode version %d must be valid from a positive epoch", v.Version)
	}
	if err := validateDistributionMode(v.Mode); err != nil {
		return ErrInvalidState.Wrapf("distribution mode version %d: %v", v.Version, err)
	}
	// 0 is the canonical open-ended marker and is not a bound to compare against.
	if v.ValidUntilEpochExclusive != 0 && v.ValidUntilEpochExclusive <= v.ValidFromEpoch {
		return ErrInvalidState.Wrapf(
			"distribution mode version %d is valid from epoch %d until epoch %d exclusive, which is an empty span",
			v.Version, v.ValidFromEpoch, v.ValidUntilEpochExclusive)
	}
	return nil
}

// validateDistributionMode refuses the unspecified arm.
//
// PROTOCOL_SELECTION is admitted here as a value even though no POC 1 path can
// produce it. Refusing it at the record level would make the enum's second arm
// unrepresentable rather than merely unreachable, which is the difference between
// state a later tranche extends and state it has to migrate.
func validateDistributionMode(mode MiningDistributionMode) error {
	switch mode {
	case MiningDistributionMode_MINING_DISTRIBUTION_MODE_TRUSTED_AS_DISTRIBUTION,
		MiningDistributionMode_MINING_DISTRIBUTION_MODE_PROTOCOL_SELECTION:
		return nil
	default:
		return ErrInvalidState.Wrapf("distribution mode %s is not a canonical mode", mode)
	}
}

// Validate checks one Selection-parameter history entry.
//
// The selection rate is measured against the immutable absolute ceiling, which is
// the only bound this profile has ratified for these fields. The remaining
// numeric maxima are calibration inputs whose values are not yet ratified, so
// they are required positive — a zero would disable the path it governs — and are
// not measured against invented ceilings.
func (v SelectionParamsVersion) Validate() error {
	if v.Version == 0 {
		return ErrInvalidState.Wrap("selection params version must be positive")
	}
	if v.EffectiveEpoch == 0 {
		return ErrInvalidState.Wrapf(
			"selection params version %d must be effective at a positive epoch", v.Version)
	}
	if err := appparams.ValidateSelectionRateBps(v.MaxSelectionRateBps, appparams.AbsoluteMaxSelectionRateBps); err != nil {
		return ErrInvalidState.Wrapf("selection params version %d: %v", v.Version, err)
	}
	for _, field := range []struct {
		name  string
		value uint64
	}{
		{"max selected participants per selection", v.MaxSelectedParticipantsPerSelection},
		{"max candidates per selection", v.MaxCandidatesPerSelection},
		{"beacon window blocks", v.BeaconWindowBlocks},
		{"min external beacon blocks", v.MinExternalBeaconBlocks},
		{"min distinct external proposers", v.MinDistinctExternalProposers},
	} {
		if field.value == 0 {
			return ErrInvalidState.Wrapf(
				"selection params version %d %s must be positive", v.Version, field.name)
		}
	}
	if v.MinExternalBeaconBlocks > v.BeaconWindowBlocks {
		return ErrInvalidState.Wrapf(
			"selection params version %d requires %d external beacon blocks in a window of %d",
			v.Version, v.MinExternalBeaconBlocks, v.BeaconWindowBlocks)
	}
	return nil
}

// Validate checks one settlement-parameter history entry against the ratified
// immutable bounds.
//
// The payout floor is parsed under the canonical encoding and then measured
// against the immutable floor. Both steps matter: the first stops a configuration
// from meaning a different number than it appears to, and the second stops
// governance from lowering the dust defense beneath what was ratified.
func (v SettlementParamsVersion) Validate() error {
	if v.Version == 0 {
		return ErrInvalidState.Wrap("settlement params version must be positive")
	}
	if v.EffectiveEpoch == 0 {
		return ErrInvalidState.Wrapf(
			"settlement params version %d must be effective at a positive epoch", v.Version)
	}
	return validateSettlementParamValues(
		settlementParamsLabel(v.Version),
		v.SettlementWindowEpochs, v.MaxRecipientsPerChunk, v.MaxChunksPerSettlement,
		v.MinRecipientPayoutAmount,
	)
}

func validateSettlementParamValues(
	label string, windowEpochs, maxRecipients, maxChunks uint64, minPayout string,
) error {
	if err := appparams.ValidateSettlementWindowEpochs(windowEpochs); err != nil {
		return ErrInvalidState.Wrapf("%s: %v", label, err)
	}
	if err := appparams.ValidateMaxRecipientsPerChunk(maxRecipients); err != nil {
		return ErrInvalidState.Wrapf("%s: %v", label, err)
	}
	if err := appparams.ValidateMaxChunksPerSettlement(maxChunks); err != nil {
		return ErrInvalidState.Wrapf("%s: %v", label, err)
	}
	amount, err := ParseCanonicalAmount(label+" minimum recipient payout amount", minPayout)
	if err != nil {
		return err
	}
	if err := appparams.ValidateMinRecipientPayoutAmount(amount, appparams.HardMinSettlementPayoutAmount()); err != nil {
		return ErrInvalidState.Wrapf("%s: %v", label, err)
	}
	return nil
}

func settlementParamsLabel(version uint64) string {
	return fmt.Sprintf("settlement params version %d", version)
}
