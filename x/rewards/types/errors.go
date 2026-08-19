package types

import errorsmod "cosmossdk.io/errors"

var (
	ErrInvalidParams      = errorsmod.Register(ModuleName, 2, "invalid rewards params")
	ErrInvalidGenesis     = errorsmod.Register(ModuleName, 3, "invalid rewards genesis")
	ErrImmutableParam     = errorsmod.Register(ModuleName, 4, "immutable rewards param")
	ErrUnsupportedFeature = errorsmod.Register(ModuleName, 5, "unsupported rewards feature")
	ErrInvalidState       = errorsmod.Register(ModuleName, 6, "invalid rewards state")
	// ErrInvalidAddress is returned when a treasury, operator or payout address
	// fails the canonical economic-address rule (§25). The specific cause —
	// malformed, empty, module account, bank-blocked — is carried in the wrapped
	// message.
	ErrInvalidAddress = errorsmod.Register(ModuleName, 7, "invalid economic address")
	// ErrEpochConfigNotFound is the ONLY ordinary absence in epoch-geometry
	// resolution: no configuration version is effective at or before the
	// requested epoch, or the epoch lies beyond the supported projection horizon.
	// Every other failure to resolve a boundary is state that exists and cannot
	// be trusted, and is reported as ErrInvalidState.
	ErrEpochConfigNotFound = errorsmod.Register(ModuleName, 8, "epoch configuration not found")
	// ErrRewardConfigNotFound is the ordinary absence in reward-configuration
	// resolution: no version is effective at or before the requested binding
	// epoch, or the genesis anchor is missing. It is separate from
	// ErrEpochConfigNotFound because the two histories fail for different reasons
	// and a caller resolving a target must be able to tell which one it lost.
	//
	// After genesis this is not a recoverable condition on the block path: the
	// anchor at epoch 1 governs every target the chain can finalize, so an absent
	// version means the history is not the history genesis wrote.
	ErrRewardConfigNotFound = errorsmod.Register(ModuleName, 9, "reward configuration not found")

	// ErrEpochBeyondProjectionHorizon reports an epoch the chain DECLINED to
	// project, not one it has no configuration for.
	//
	// The two are different answers and must not share a sentinel. A configuration
	// that is absent says something about the chain's history; a horizon says only
	// that the caller asked further ahead than a bounded query will walk, and the
	// same question inside the horizon would be answered. Collapsing them tells a
	// consumer a far-future epoch has no configuration, which it would reasonably
	// read as "nothing scheduled" and act on.
	ErrEpochBeyondProjectionHorizon = errorsmod.Register(
		ModuleName, 10, "epoch lies beyond the supported projection horizon")
)
