package types_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	errorsmod "cosmossdk.io/errors"

	"github.com/twilight-project/twilight-core/internal/errorledger"

	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// Pins the registered error codes to their NUMBERS. See the equivalent test in
// x/coreslot/types for the full reasoning: an ABCI code is externally observed,
// and require.ErrorIs against a sentinel value is invariant under renumbering, so
// nothing else in the module would notice a swap.
//
// x/rewards carries the emergency pause path, so a client distinguishing "paused"
// from "malformed" by code is entirely plausible.
func TestErrorCodeLedgerIsPinned(t *testing.T) {
	ledger := map[uint32]*errorsmod.Error{
		2: types.ErrInvalidParams,
		3: types.ErrInvalidGenesis,
		4: types.ErrImmutableParam,
		5: types.ErrUnsupportedFeature,
		6: types.ErrInvalidState,
		7: types.ErrInvalidAddress,
		8: types.ErrEpochConfigNotFound,
		9: types.ErrRewardConfigNotFound,
		// Registered across two lines, which is exactly why a line-based
		// extraction missed it and left it unpinned.
		10: types.ErrEpochBeyondProjectionHorizon,
	}

	for code, sentinel := range ledger {
		require.NotNil(t, sentinel, "code %d has no sentinel", code)
		require.Equal(t, code, sentinel.ABCICode(),
			"error %q must keep ABCI code %d: the number is externally observed", sentinel.Error(), code)
	}

	// Completeness is derived from the SOURCE, not from the table.
	//
	// The previous version asserted a hard-coded length and a hard-coded range —
	// both facts about the table, checked against the table. That cannot notice a
	// sentinel that was registered and never listed, and it did not: x/rewards
	// code 10 was live and unpinned while the test passed, because the table and
	// its expected length came from one faulty extraction.
	registered, err := errorledger.Registered("errors.go")
	require.NoError(t, err)

	for code, reg := range registered {
		require.Contains(t, ledger, code,
			"%s is registered at code %d but is not pinned; add it to the ledger", reg.Sentinel, code)
	}
	require.Len(t, ledger, len(registered),
		"the ledger must pin exactly the registered set, no more and no less")

	// Contiguity, with the bounds taken from the registrations rather than
	// written down: a gap means a sentinel was removed, and a removed number must
	// stay retired rather than be filled by a later one shifting down into it.
	low, high := uint32(0), uint32(0)
	for code := range registered {
		if low == 0 || code < low {
			low = code
		}
		if code > high {
			high = code
		}
	}
	for code := low; code <= high; code++ {
		require.Contains(t, registered, code,
			"code %d is missing between %d and %d; a retired number must not be reused", code, low, high)
	}
}
