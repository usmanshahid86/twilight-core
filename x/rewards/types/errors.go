package types

import errorsmod "cosmossdk.io/errors"

var (
	ErrInvalidParams      = errorsmod.Register(ModuleName, 2, "invalid rewards params")
	ErrInvalidGenesis     = errorsmod.Register(ModuleName, 3, "invalid rewards genesis")
	ErrImmutableParam     = errorsmod.Register(ModuleName, 4, "immutable rewards param")
	ErrUnsupportedFeature = errorsmod.Register(ModuleName, 5, "unsupported rewards feature")
	ErrInvalidState       = errorsmod.Register(ModuleName, 6, "invalid rewards state")
)
