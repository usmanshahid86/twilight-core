package types_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	errorsmod "cosmossdk.io/errors"

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
	}

	for code, sentinel := range ledger {
		require.NotNil(t, sentinel, "code %d has no sentinel", code)
		require.Equal(t, code, sentinel.ABCICode(),
			"error %q must keep ABCI code %d: the number is externally observed", sentinel.Error(), code)
	}
	require.Len(t, ledger, 8, "every registered rewards error must be pinned")
	for code := uint32(2); code <= 9; code++ {
		require.Contains(t, ledger, code, "code %d is missing from the ledger", code)
	}
}
