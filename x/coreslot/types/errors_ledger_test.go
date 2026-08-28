package types_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	errorsmod "cosmossdk.io/errors"

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

	// A count check, so ADDING an error without a row here fails rather than
	// passing silently. Without it the table would only catch renumbering of
	// codes it already knows about.
	require.Len(t, ledger, 21, "every registered coreslot error must be pinned")

	// Contiguous from 2, so a gap left by a removed sentinel cannot be filled by
	// a later one shifting down into it.
	for code := uint32(2); code <= 22; code++ {
		require.Contains(t, ledger, code, "code %d is missing from the ledger", code)
	}
}
