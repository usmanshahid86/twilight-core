package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// The CoreSlot payout snapshot, which is where a stored address becomes money.
//
// Every entitlement carries an immutable destination copied out of CoreSlot at the
// moment its epoch closed. Two things therefore have to be true before the mint
// runs: the record x/rewards reads is the record it asked for, and the address in
// it is one the canonical rule admits.
//
// Neither is detectable afterwards. An entitlement built from the wrong Slot's
// record is internally consistent, correctly sized, and points at a perfectly
// valid address — just somebody else's.

// TestSnapshotRefusesARecordForADifferentSlot is B4.
//
// The dependency returns a structurally valid CoreSlot that declares a different
// slot_id than the one requested. Without an identity check the payout address of
// slot 2 is snapshotted into slot 1's entitlement, and the money for slot 1's
// participation is permanently redirected.
func TestSnapshotRefusesARecordForADifferentSlot(t *testing.T) {
	k, ctx, bank, core := setupFinalization(t, false)

	// Slot 1's key now holds a record declaring slot 2, with slot 2's destination.
	impostor := core.slots[2]
	core.slots[1] = impostor

	_, err := k.GetSlotRewardSnapshot(ctx, 1)
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Contains(t, err.Error(), "declaring slot 2")

	require.Error(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))
	requireNoMonetaryEffect(t, k, ctx, bank, 1)
}

// TestSnapshotFailuresAreDiscoveredBeforeAnyMint covers the ordering half.
//
// Each case is a snapshot that cannot be taken. All of them used to be found after
// the mint and the treasury send had run — rolled back correctly, but only because
// the whole transition is cached. The assertion is that the mint keeper is never
// invoked at all.
func TestSnapshotFailuresAreDiscoveredBeforeAnyMint(t *testing.T) {
	for _, tc := range []struct {
		name    string
		corrupt func(*coreSlotKeeperMock)
	}{
		{
			name: "the participating slot is absent from core slot state",
			corrupt: func(core *coreSlotKeeperMock) {
				delete(core.slots, 2)
			},
		},
		{
			name: "the record declares a different slot",
			corrupt: func(core *coreSlotKeeperMock) {
				core.slots[2] = core.slots[1]
			},
		},
		{
			name: "the payout destination is no longer admissible",
			corrupt: func(core *coreSlotKeeperMock) {
				slot := core.slots[2]
				slot.PayoutAddress = "not-an-address"
				core.slots[2] = slot
			},
		},
		{
			name: "the payout destination is the all-zero account",
			corrupt: func(core *coreSlotKeeperMock) {
				slot := core.slots[2]
				slot.PayoutAddress = zeroAddress()
				core.slots[2] = slot
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx, bank, core := setupFinalization(t, false)
			tc.corrupt(core)

			require.Error(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))
			requireNoMonetaryEffect(t, k, ctx, bank, 1)
		})
	}
}

// TestSnapshotDoesNotRequireTheSlotToStillBeActive is the guard on the other side.
//
// An entitlement is earned by participation in a closed epoch, and §64 keeps it
// payable through INACTIVE, SUSPENDED and REMOVED. The snapshot records the
// end-of-close lifecycle state as an audit field and nothing more, so tightening
// the integrity check above into a liveness requirement would confiscate money
// that was already earned.
func TestSnapshotDoesNotRequireTheSlotToStillBeActive(t *testing.T) {
	for _, status := range []coreslottypes.SlotStatus{
		coreslottypes.SlotStatus_SLOT_STATUS_INACTIVE,
		coreslottypes.SlotStatus_SLOT_STATUS_SUSPENDED,
		coreslottypes.SlotStatus_SLOT_STATUS_REMOVED,
	} {
		t.Run(status.String(), func(t *testing.T) {
			k, ctx, bank, core := setupFinalization(t, false)
			slot := core.slots[2]
			slot.Status = status
			core.slots[2] = slot

			require.NoError(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))
			require.Equal(t, 1, bank.mintCalls)

			entitlement, found, err := k.GetSlotEntitlement(ctx, 2, 1)
			require.NoError(t, err)
			require.True(t, found, "a slot that earned an epoch is owed it whatever happened afterwards")
			require.Equal(t, status, entitlement.SlotStatusAtEpochClose,
				"the lifecycle state is recorded as an audit field, not acted on")
		})
	}
}
