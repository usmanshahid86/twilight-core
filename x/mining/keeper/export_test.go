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

// FinalizeSettlementWithoutCache is the finalization body with the handler's own
// cache stripped off.
//
// It exists to make the ORDER of the two effects observable. With the cache in
// place both orderings are safe, so no ordinary test can tell them apart — which
// is exactly the condition under which a later edit could reorder them unnoticed
// and leave the guarantee resting on the cache alone. Driving the body directly
// shows which ordering fails safe when that cache is not there.
var FinalizeSettlementWithoutCache = Keeper.finalizeSettlement

// EncodeEpochCursor exposes the OpenSettlements continuation cursor so a test can
// construct one rather than only echoing one back, which is the only way to reach
// the "cursor past the end" branch deliberately.
var EncodeEpochCursor = encodeEpochCursor
