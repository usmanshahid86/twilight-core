package types_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	errorsmod "cosmossdk.io/errors"

	"github.com/twilight-project/twilight-core/internal/errorledger"

	"github.com/twilight-project/twilight-core/x/coreslot/types"
)

// The registered error codes are pinned to their NUMBERS, not merely to their
// identities.
//
// An ABCI code is externally observed: it appears in DeliverTx results, so
// clients, explorers, operator runbooks and the drills branch on the number
// rather than the message. The authority-rotation drill asserts on 2, 10 and 22
// directly, because a code is the only unambiguous signal that a transaction
// failed for the reason under test rather than for a stale sequence.
//
// Every other test in this module uses require.ErrorIs against the sentinel
// VALUE, which proves identity and is invariant under renumbering: all of them
// would still pass if two codes were swapped. Nothing else would notice, and the
// change would silently alter what every external consumer sees.
//
// So this table is the contract. Codes are append-only: a released number is
// never reused for different meaning, and never renumbered. Adding an error means
// adding a row here with the next number, which is the point — the test should be
// impossible to satisfy by accident.
func TestErrorCodeLedgerIsPinned(t *testing.T) {
	ledger := map[uint32]*errorsmod.Error{
		2:  types.ErrUnauthorized,
		3:  types.ErrSlotNotFound,
		4:  types.ErrInvalidTransition,
		5:  types.ErrDuplicateOperator,
		6:  types.ErrDuplicateConsensusKey,
		7:  types.ErrMinActiveSlots,
		8:  types.ErrMaxActiveSlots,
		9:  types.ErrInvalidPubKey,
		10: types.ErrInvalidParams,
		11: types.ErrPendingRotationExists,
		12: types.ErrCannotRemoveLastValidator,
		13: types.ErrInvalidGenesis,
		14: types.ErrInvalidAddress,
		15: types.ErrInvalidSelectionPolicy,
		16: types.ErrNoOpUpdate,
		17: types.ErrSelectionPolicyNotFound,
		18: types.ErrSelectionPolicyCooldown,
		19: types.ErrUpgradeUnavailable,
		20: types.ErrInvalidUpgrade,
		21: types.ErrInvalidAuthorityRole,
		22: types.ErrNoPendingNomination,
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
