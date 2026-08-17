package types

import (
	"fmt"

	appparams "github.com/twilight-project/twilight-core/app/params"
)

// Validation for the canonical reward-configuration history and its schedule.
//
// These are shape rules for one record. The relational rules — monotonicity
// between adjacent versions, agreement with the deprecated Params/snapshot
// mirrors, an empty fresh-genesis schedule — belong to the keeper and to genesis
// validation, which can see neighboring records or the whole collection at once.
//
// Deliberately NOT checked here: that a treasury address is present whenever the
// share is positive. That rule needs the canonical EconomicAddressValidator,
// which only the keeper holds, so it is applied at every admission point in
// x/rewards rather than approximated with a syntax check that would disagree
// with the real rule.

// validateRewardEconomics checks the fields the history record and the schedule
// record have in common.
//
// The subsidy is parsed under the CANONICAL encoding rather than the general
// amount parser. The general parser infers the radix, so "010" decodes as 8 and
// "0x10" as 16 — a configuration that scales every block's mint would then emit a
// different amount than the one its own document appears to state, and would do so
// silently. One spelling per value is what makes the stored record and the genesis
// text that produced it the same number. See ParseCanonicalAmount.
//
// The subsidy is also required POSITIVE rather than merely parseable. A zero
// subsidy would make every later block emit nothing regardless of supply or
// halving tier, which is a permanent halt of emission expressed as a configuration
// value; the canonical way to stop emission is REWARDS_PAUSED, which is reversible
// and is recorded as pause state rather than buried in a reward version.
func validateRewardEconomics(label string, subsidy string, shareBps uint64) error {
	amount, err := ParseCanonicalAmount(label+" initial block subsidy", subsidy)
	if err != nil {
		return err
	}
	if !amount.IsPositive() {
		return ErrInvalidState.Wrapf("%s initial block subsidy must be positive", label)
	}
	// The ratified ceiling, read from the canonical constant. A share at or above
	// the basis-point denominator would divert an entire epoch's emission and
	// leave the reward pool with nothing to allocate; the ceiling sits well below
	// that and is not a value this module may choose.
	if err := appparams.ValidateEmissionTreasuryShareBps(shareBps); err != nil {
		return ErrInvalidState.Wrapf("%s: %v", label, err)
	}
	return nil
}

// Validate checks one immutable reward-configuration history entry.
//
// Versions and effective epochs are numbered from 1, so zero is not a legitimate
// value for either: a zero version cannot be ordered against its neighbors, and
// a zero effective epoch would claim to govern an epoch that never exists.
func (v RewardConfigVersion) Validate() error {
	if v.Version == 0 {
		return ErrInvalidState.Wrap("reward configuration version must be positive")
	}
	if v.EffectiveEpoch == 0 {
		return ErrInvalidState.Wrapf(
			"reward configuration version %d must be effective at a positive epoch", v.Version)
	}
	return validateRewardEconomics(
		rewardVersionLabel(v.Version, v.EffectiveEpoch), v.InitialBlockSubsidy, v.EmissionTreasuryShareBps)
}

// Validate checks one scheduled future reward configuration.
//
// The record deliberately carries no version number: the version is assigned
// from the history it extends at promotion time, so a schedule entry that was
// replaced cannot leave a stale identity behind for the replacement to collide
// with.
func (s ScheduledRewardConfig) Validate() error {
	if s.EffectiveEpoch == 0 {
		return ErrInvalidState.Wrap("scheduled reward configuration must be effective at a positive epoch")
	}
	return validateRewardEconomics(
		scheduledRewardLabel(s.EffectiveEpoch), s.InitialBlockSubsidy, s.EmissionTreasuryShareBps)
}

func rewardVersionLabel(version, effectiveEpoch uint64) string {
	return fmt.Sprintf("reward configuration version %d at effective epoch %d", version, effectiveEpoch)
}

func scheduledRewardLabel(effectiveEpoch uint64) string {
	return fmt.Sprintf("scheduled reward configuration at epoch %d", effectiveEpoch)
}
