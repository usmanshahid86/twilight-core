package types

// Validation for the canonical epoch-configuration history and its schedule.
//
// These are shape rules for one record. The relational rules — continuity
// between adjacent versions, agreement with the deprecated Params/snapshot
// mirrors, an empty fresh-genesis schedule — belong to genesis validation, which
// can see the whole collection at once.

// Validate checks one immutable history entry.
//
// Every field is required to be positive. Zero is not a legitimate value for any
// of them: epochs and versions are numbered from 1, the original genesis anchor
// starts at a real initial height, and a zero-length epoch would make the
// start-height recurrence stationary — every epoch would begin at the same block
// and no boundary would ever be reached.
func (v EpochConfigVersion) Validate() error {
	if v.Version == 0 {
		return ErrInvalidState.Wrap("epoch configuration version must be positive")
	}
	if v.EffectiveEpoch == 0 {
		return ErrInvalidState.Wrapf(
			"epoch configuration version %d must be effective at a positive epoch", v.Version)
	}
	if v.EffectiveStartHeight == 0 {
		return ErrInvalidState.Wrapf(
			"epoch configuration version %d must have a positive effective start height", v.Version)
	}
	if v.EpochLengthBlocks == 0 {
		return ErrInvalidState.Wrapf(
			"epoch configuration version %d must have a positive epoch length", v.Version)
	}
	return nil
}

// Validate checks one scheduled future epoch length.
//
// The record deliberately carries no start height: replacing an earlier
// scheduled value would leave a cached later boundary stale, so projected
// boundaries are always derived rather than stored.
func (s ScheduledEpochConfig) Validate() error {
	if s.EffectiveEpoch == 0 {
		return ErrInvalidState.Wrap("scheduled epoch configuration must be effective at a positive epoch")
	}
	if s.EpochLengthBlocks == 0 {
		return ErrInvalidState.Wrapf(
			"scheduled epoch configuration at epoch %d must have a positive epoch length", s.EffectiveEpoch)
	}
	return nil
}

// Validate checks the canonical pause state's internal consistency.
//
// has_pending is what separates a scheduled resume from no schedule at all, so
// the two dependent fields are only meaningful when it is set, and must be empty
// when it is not. Without that rule a cleared transition could leave a stale
// height behind that a later block would treat as due.
func (p RewardsPauseState) Validate() error {
	if p.HasPending {
		if p.PendingEffectiveHeight == 0 {
			return ErrInvalidState.Wrap("a pending pause transition requires a positive effective height")
		}
		return nil
	}
	if p.PendingValue {
		return ErrInvalidState.Wrap("a pause state with no pending transition must not carry a pending value")
	}
	if p.PendingEffectiveHeight != 0 {
		return ErrInvalidState.Wrap("a pause state with no pending transition must not carry an effective height")
	}
	return nil
}
