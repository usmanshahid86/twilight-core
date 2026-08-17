package keeper

import (
	"testing"

	sdkmath "cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// In-package, deliberately.
//
// The conservation assertions are defenses against the allocation arithmetic
// being WRONG. Correct arithmetic can never violate them, so a test that drives
// them through a real finalization can only ever observe them passing — it would
// keep passing if the assertions were deleted, which makes it worthless as
// evidence that they exist.
//
// Calling the assertion directly with a violating input is the only way to make
// it load-bearing: delete the check and these fail immediately.

func TestAssertAllocationConservationCatchesOverAllocation(t *testing.T) {
	pool := sdkmath.NewInt(100)

	t.Run("allocating more than the pool", func(t *testing.T) {
		err := assertAllocationConservation(1, pool, sdkmath.NewInt(101), sdkmath.NewInt(-1), 2)
		require.ErrorIs(t, err, types.ErrInvalidState)
	})

	t.Run("a negative allocation", func(t *testing.T) {
		err := assertAllocationConservation(1, pool, sdkmath.NewInt(-1), sdkmath.NewInt(101), 2)
		require.ErrorIs(t, err, types.ErrInvalidState)
	})

	t.Run("a negative carry", func(t *testing.T) {
		err := assertAllocationConservation(1, pool, sdkmath.NewInt(50), sdkmath.NewInt(-1), 2)
		require.ErrorIs(t, err, types.ErrInvalidState)
	})
}

// TestAssertAllocationConservationEnforcesTheResidueBound is the one that a
// definitional equality cannot replace.
//
// carry = pool - allocated always holds by construction, so it survives most
// defects. carry <= n_pos - 1 follows from flooring exactly once per
// participating Slot, so a wrong denominator, a Slot counted twice, or an
// iteration that revisited a Slot all break it while still producing a split
// that adds up.
func TestAssertAllocationConservationEnforcesTheResidueBound(t *testing.T) {
	pool := sdkmath.NewInt(100)

	for _, tc := range []struct {
		name      string
		allocated int64
		carry     int64
		nPos      uint64
		accept    bool
	}{
		// Three participants may leave at most two units behind.
		{name: "carry at the bound", allocated: 98, carry: 2, nPos: 3, accept: true},
		{name: "carry below the bound", allocated: 99, carry: 1, nPos: 3, accept: true},
		{name: "carry one past the bound", allocated: 97, carry: 3, nPos: 3},
		// One participant takes everything: a single floor discards nothing.
		{name: "one participant leaves nothing", allocated: 100, carry: 0, nPos: 1, accept: true},
		{name: "one participant leaving a unit is impossible", allocated: 99, carry: 1, nPos: 1},
		// No participants: the whole pool becomes carry and the bound does not apply.
		{name: "no participants carry the whole pool", allocated: 0, carry: 100, nPos: 0, accept: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := assertAllocationConservation(
				1, pool, sdkmath.NewInt(tc.allocated), sdkmath.NewInt(tc.carry), tc.nPos)
			if tc.accept {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, types.ErrInvalidState)
		})
	}
}

// TestAllocationSatisfiesTheResidueBoundForEveryParticipantCount ties the
// assertion back to the production arithmetic.
//
// The bound above is enforced; this is why it is satisfiable. A pool that
// divides evenly leaves nothing, and the worst case — a pool one unit short of
// dividing evenly for every Slot — leaves exactly n_pos - 1.
func TestAllocationSatisfiesTheResidueBoundForEveryParticipantCount(t *testing.T) {
	for participants := uint64(1); participants <= 8; participants++ {
		rows := make([]types.SlotActiveBlocks, 0, participants)
		snapshots := make(map[uint64]SlotRewardSnapshot, participants)
		for i := uint64(1); i <= participants; i++ {
			rows = append(rows, types.SlotActiveBlocks{SlotId: i, BlocksActive: 1})
			snapshots[i] = SlotRewardSnapshot{SlotID: i, PayoutAddress: make([]byte, 20)}
		}
		// One unit short of dividing evenly: every Slot floors, and the residue is
		// as large as the bound permits.
		pool := sdkmath.NewIntFromUint64(participants*10 - 1)

		_, allocated, carry, nPos, err := AllocateSlotEntitlements(1, pool, rows, snapshots, 1, 1)
		require.NoError(t, err)
		require.Equal(t, participants, nPos)
		require.NoError(t, assertAllocationConservation(1, pool, allocated, carry, nPos),
			"the production arithmetic must satisfy the bound it is checked against")
		require.Equal(t, sdkmath.NewIntFromUint64(participants-1).String(), carry.String(),
			"the worst case leaves exactly n_pos-1")
	}
}
