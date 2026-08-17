package keeper

// Test-only access to the unexported history primitives.
//
// These two carry contracts this gate RATIFIES rather than merely uses, and the
// gate that decides a rule is the one that should pin it. Waiting for the query
// gate to consume them would leave the decisions unasserted in the commit that
// made them.
//
// Nothing here widens the production surface: the file builds only under test.
var (
	// LookupVersionEpochKey resolves a version number through a derived index.
	// Its contract is that a missing entry is ORDINARY ABSENCE — the divergence
	// from the reward-configuration index, where the same condition is corruption.
	LookupVersionEpochKey = lookupVersionEpochKey

	// BindingEpochForTarget maps a target epoch onto the epoch whose configuration
	// governs it. Its contract is that targets 1 and 2 bootstrap rather than
	// underflowing an unsigned subtraction into the newest version.
	BindingEpochForTarget = bindingEpochForTarget
)
