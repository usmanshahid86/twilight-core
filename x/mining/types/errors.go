package types

import errorsmod "cosmossdk.io/errors"

var (
	ErrInvalidGenesis = errorsmod.Register(ModuleName, 2, "invalid mining genesis")
	// ErrInvalidState covers stored mining state that exists and cannot be
	// trusted: a record that disagrees with the key it was found under, a derived
	// index that disagrees with the canonical row it points at, an arithmetic
	// relation that no admitted path could have produced.
	//
	// It is deliberately distinct from ErrNotFound. Absence is an ordinary answer
	// for most settlement lookups; state that contradicts itself never is, and
	// collapsing the two would let corruption present as "nothing here".
	ErrInvalidState = errorsmod.Register(ModuleName, 3, "invalid mining state")
	// ErrSettlementNotFound is modeled absence: most (slot, epoch) pairs have no
	// settlement, because most Slots earn nothing in most epochs.
	ErrSettlementNotFound = errorsmod.Register(ModuleName, 4, "settlement not found")
	// ErrParamsNotFound is modeled absence in a version history: no version is
	// effective at or before the epoch asked about.
	ErrParamsNotFound = errorsmod.Register(ModuleName, 5, "mining parameter version not found")
	// ErrInvalidAddress is a distinct code from ErrInvalidState because a rejected
	// recipient or signer is an admission failure against the canonical
	// economic-address rule, not a claim that stored state is broken.
	ErrInvalidAddress = errorsmod.Register(ModuleName, 6, "invalid economic address")
	// ErrUnsupportedFeature marks a canonical path that exists in the state model
	// but has no reachable producer in this profile.
	ErrUnsupportedFeature = errorsmod.Register(ModuleName, 7, "unsupported mining feature")
)
