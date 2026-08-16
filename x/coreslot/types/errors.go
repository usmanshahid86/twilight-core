package types

import errorsmod "cosmossdk.io/errors"

var (
	ErrUnauthorized          = errorsmod.Register(ModuleName, 2, "unauthorized")
	ErrSlotNotFound          = errorsmod.Register(ModuleName, 3, "core slot not found")
	ErrInvalidTransition     = errorsmod.Register(ModuleName, 4, "invalid core slot transition")
	ErrDuplicateOperator     = errorsmod.Register(ModuleName, 5, "operator already owns a core slot")
	ErrDuplicateConsensusKey = errorsmod.Register(ModuleName, 6, "consensus key is already assigned or reserved")
	ErrMinActiveSlots        = errorsmod.Register(ModuleName, 7, "minimum active core slots would be violated")
	ErrMaxActiveSlots        = errorsmod.Register(ModuleName, 8, "maximum active core slots would be violated")
	ErrInvalidPubKey         = errorsmod.Register(ModuleName, 9, "invalid consensus public key")
	ErrInvalidParams         = errorsmod.Register(ModuleName, 10, "invalid core slot params")
	// ErrPendingRotationExists is returned when a second consensus key rotation
	// is requested while one is already queued for the slot (F2).
	ErrPendingRotationExists = errorsmod.Register(ModuleName, 11, "a pending consensus key rotation already exists for this slot")
	// ErrCannotRemoveLastValidator guards against draining the active validator
	// set to zero, even in emergency mode (F6).
	ErrCannotRemoveLastValidator = errorsmod.Register(ModuleName, 12, "operation would remove the last active validator")
	// ErrInvalidGenesis is returned for genesis consistency violations (F7).
	ErrInvalidGenesis = errorsmod.Register(ModuleName, 13, "invalid core slot genesis")
	// ErrInvalidAddress is returned when an operator or payout address fails the
	// canonical economic-address rule (§25) — malformed, empty, a module account,
	// or a destination the bank module blocks. The specific cause is carried in
	// the wrapped message; it is a distinct code from ErrInvalidParams because a
	// rejected payee is an admission failure, not a parameter fault.
	ErrInvalidAddress = errorsmod.Register(ModuleName, 14, "invalid economic address")
	// ErrInvalidSelectionPolicy is returned when a Selection policy fails §27
	// local structural validity, or when a slot's stored policy history and
	// current-version pointer are inconsistent. It is distinct from
	// ErrInvalidParams because a policy is per-slot operator configuration rather
	// than a module parameter.
	ErrInvalidSelectionPolicy = errorsmod.Register(ModuleName, 15, "invalid selection policy")
	// ErrNoOpUpdate is returned when a mutation would replace a value with the
	// one already stored. §24 requires an identical settlement-address
	// replacement to be rejected rather than silently accepted.
	ErrNoOpUpdate = errorsmod.Register(ModuleName, 16, "update would not change stored state")
)
