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
)
