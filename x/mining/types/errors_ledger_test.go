package types_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	errorsmod "cosmossdk.io/errors"

	"github.com/twilight-project/twilight-core/x/mining/types"
)

// Pins the registered error codes to their NUMBERS. See the equivalent test in
// x/coreslot/types for the full reasoning.
//
// x/mining is the settlement state machine, so its codes are what an integrator
// watching for a refused or not-yet-finalized settlement would branch on.
func TestErrorCodeLedgerIsPinned(t *testing.T) {
	ledger := map[uint32]*errorsmod.Error{
		2: types.ErrInvalidGenesis,
		3: types.ErrInvalidState,
		4: types.ErrSettlementNotFound,
		5: types.ErrParamsNotFound,
		6: types.ErrInvalidAddress,
		7: types.ErrUnsupportedFeature,
	}

	for code, sentinel := range ledger {
		require.NotNil(t, sentinel, "code %d has no sentinel", code)
		require.Equal(t, code, sentinel.ABCICode(),
			"error %q must keep ABCI code %d: the number is externally observed", sentinel.Error(), code)
	}
	require.Len(t, ledger, 6, "every registered mining error must be pinned")
	for code := uint32(2); code <= 7; code++ {
		require.Contains(t, ledger, code, "code %d is missing from the ledger", code)
	}
}
