package keeper_test

import (
	"testing"

	sdkmath "cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	"github.com/twilight-project/twilight-core/x/rewards/keeper"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// Consequential integrity of the reward configuration.
//
// Every case below plants a governing configuration that no admission path could
// have produced, and then drives a real epoch close against it. The assertion is
// always the same shape and it is deliberately not "the block failed": it is that
// the mint keeper was never invoked.
//
// That distinction is the whole point. Finalization runs in a cache, so a
// configuration rejected AFTER the mint still rolls back and still halts the
// block. But the mint is the step that creates the value everything downstream
// moves, and a check that runs behind it is a check whose ordering nobody is
// asserting — it would keep passing if it moved further back still, until one day
// it sat behind a step that does not roll back.

// requireNoMonetaryEffect asserts a finalization attempt left nothing behind:
// no keeper call that moves value, and no committed state.
func requireNoMonetaryEffect(t *testing.T, k keeper.Keeper, ctx sdk.Context, bank *bankKeeperMock, epoch uint64) {
	t.Helper()
	require.Zero(t, bank.mintCalls, "no mint may be attempted")
	require.Zero(t, bank.sendCalls, "no bank send may be attempted")

	_, found, err := k.GetFinalizedEpoch(ctx, epoch)
	require.NoError(t, err)
	require.False(t, found, "no finalized epoch may be committed")

	entitlements, err := k.IterateEntitlementsForEpoch(ctx, epoch)
	require.NoError(t, err)
	require.Empty(t, entitlements, "no entitlement may be committed")

	liability, err := k.GetOutstandingEntitlementLiability(ctx)
	require.NoError(t, err)
	require.Equal(t, "0", liability.String(), "the liability may not move")

	state, err := k.GetState(ctx)
	require.NoError(t, err)
	require.Equal(t, "0", state.CumulativeEmitted, "no cumulative emission may be committed")
	require.Equal(t, "0", state.CarryForwardRemainder, "no carry may be committed")
	require.Equal(t, epoch, state.CurrentEpoch)
}

// TestGoverningRewardConfigSubsidyMustBeCanonical is the stored half of the
// canonical-encoding rule.
//
// Admission refuses these spellings, so a stored one means the row is not the row
// that was written. Resolution must refuse it too: "010" would otherwise scale the
// mint by 8 rather than by the 10 the record appears to state.
func TestGoverningRewardConfigSubsidyMustBeCanonical(t *testing.T) {
	for _, subsidy := range []string{"010", "0x10", "0b101", "1_0", "+10", " 10", "10 "} {
		t.Run(subsidy, func(t *testing.T) {
			k, ctx, bank, _ := setupFinalization(t, false)
			anchor, err := k.GenesisRewardConfigVersion(ctx)
			require.NoError(t, err)
			anchor.InitialBlockSubsidy = subsidy
			require.NoError(t, k.RewardConfigVersions.Set(ctx, anchor.EffectiveEpoch, anchor))

			// The consequential read itself refuses it.
			_, err = k.RewardConfigForTarget(ctx, 1)
			require.ErrorIs(t, err, types.ErrInvalidState)

			require.Error(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))
			requireNoMonetaryEffect(t, k, ctx, bank, 1)
		})
	}
}

// TestGoverningRewardConfigWithAPositiveShareMustNameItsDestination is the
// pre-mint destination rule.
//
// A configuration that diverts part of every epoch's emission has to say where.
// Before this check the mint ran first and the destination was refused at the
// transfer, which rolled back correctly but put the ordering the wrong way round.
func TestGoverningRewardConfigWithAPositiveShareMustNameItsDestination(t *testing.T) {
	for _, tc := range []struct {
		name        string
		destination string
	}{
		{name: "absent", destination: ""},
		{name: "malformed", destination: "not-an-address"},
		{name: "a module account", destination: testModuleAddress(testModuleAccountName)},
		{name: "the all-zero account", destination: zeroAddress()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx, bank, _ := setupFinalization(t, false)
			seedTreasuryRewardConfigUnchecked(t, k, ctx, 1_000, tc.destination)

			_, err := k.RewardConfigForTarget(ctx, 1)
			require.Error(t, err, "a positive share with no usable destination is not resolvable")

			require.Error(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))
			requireNoMonetaryEffect(t, k, ctx, bank, 1)
		})
	}
}

// TestZeroShareStillPerformsNoDestinationValidation guards the boundary from the
// other side.
//
// The pre-mint rule above must not creep into a configuration that directs no
// value. §33.2 is explicit: at a zero share there is no transfer, no revalidation,
// and therefore nothing for an inadmissible address to be inadmissible FOR.
// Widening the check to "validate if present" would reject configurations the
// architecture admits.
func TestZeroShareStillPerformsNoDestinationValidation(t *testing.T) {
	k, ctx, bank, _ := setupFinalization(t, false)
	seedTreasuryRewardConfigUnchecked(t, k, ctx, 0, testModuleAddress(testModuleAccountName))

	version, err := k.RewardConfigForTarget(ctx, 1)
	require.NoError(t, err, "a zero share resolves regardless of what the address field holds")
	require.Equal(t, uint64(0), version.EmissionTreasuryShareBps)

	require.NoError(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))
	require.Zero(t, bank.sendCalls)
}

// TestRewardConfigHistoryRequiresItsPermanentAnchor is B1-C.
//
// The predecessor seek cannot detect a missing anchor on its own: it asks for the
// greatest effective epoch at or before a bound, and any later version answers
// that perfectly well. So a history whose epoch-1/version-1 row has gone still
// resolves — to a version that was never the bootstrap, on a chain whose first two
// targets were paid under one that no longer exists.
func TestRewardConfigHistoryRequiresItsPermanentAnchor(t *testing.T) {
	t.Run("an ordinary target refuses to resolve without it", func(t *testing.T) {
		k, ctx, _ := setupRewardConfig(t)
		// A later, entirely well-formed version. Without the anchor rule, target 5
		// binds epoch 3 and resolves this row.
		seedRewardVersion(t, k, ctx, rewardVersionAt(2, 3, "20"))
		resolved, err := k.RewardConfigForTarget(ctx, 5)
		require.NoError(t, err, "the fixture must resolve while the anchor is present")
		require.Equal(t, uint64(2), resolved.Version)

		require.NoError(t, k.RewardConfigVersions.Remove(ctx, 1))

		_, err = k.RewardConfigForTarget(ctx, 5)
		require.ErrorIs(t, err, types.ErrRewardConfigNotFound)
		// And the bootstrap targets, which read the anchor directly.
		_, err = k.RewardConfigForTarget(ctx, 1)
		require.ErrorIs(t, err, types.ErrRewardConfigNotFound)
		_, err = k.RewardConfigForTarget(ctx, 2)
		require.ErrorIs(t, err, types.ErrRewardConfigNotFound)
	})

	t.Run("an anchor that is not version 1 is not an anchor", func(t *testing.T) {
		k, ctx, _ := setupRewardConfig(t)
		seedRewardVersion(t, k, ctx, rewardVersionAt(2, 3, "20"))
		// Replace the anchor with a row that is well-formed but carries the wrong
		// identity. This is the case a "the earliest row is the anchor" rule would
		// accept.
		seedRewardVersion(t, k, ctx, rewardVersionAt(7, 1, "10"))

		_, err := k.RewardConfigForTarget(ctx, 5)
		require.ErrorIs(t, err, types.ErrInvalidState)
	})

	t.Run("finalization at an ordinary target makes no monetary call", func(t *testing.T) {
		k, ctx, bank, _ := setupFinalizationAtEpoch(t, 3)
		require.NoError(t, k.RewardConfigVersions.Remove(ctx, 1))

		require.Error(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))
		requireNoMonetaryEffect(t, k, ctx, bank, 3)
	})
}

// setupFinalizationAtEpoch builds the finalization fixture at an arbitrary open
// epoch, so the ordinary N-2 binding branch can be driven rather than only the two
// bootstrap targets.
//
// Geometry is anchored at the fixture's own epoch, exactly as setupFinalization
// does; the reward anchor stays at epoch 1, because that one is permanent.
func setupFinalizationAtEpoch(t *testing.T, epoch uint64) (keeper.Keeper, sdk.Context, *bankKeeperMock, *coreSlotKeeperMock) {
	t.Helper()
	params := rewardConfigParams()
	core := &coreSlotKeeperMock{
		slots: map[uint64]coreslottypes.CoreSlot{
			1: {SlotId: 1, OperatorAddress: addr(1), PayoutAddress: addr(2), Status: coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE},
			2: {SlotId: 2, OperatorAddress: addr(3), PayoutAddress: addr(4), Status: coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE},
		},
	}
	k, ctx, bank := setupAccountingKeeper(t, core, epoch, params)
	require.NoError(t, k.SetOpenRewardEnabledBlocks(ctx, params.EpochLengthBlocks))
	require.NoError(t, k.SetActiveBlocks(ctx, epoch, 1, 90))
	require.NoError(t, k.SetActiveBlocks(ctx, epoch, 2, 270))
	return k, ctx, bank, core
}

// TestFinalizationAtAnOrdinaryTargetBindsThePredecessor proves setupFinalizationAtEpoch
// exercises the branch it claims to, so the anchor case above is not passing for
// want of a target that ever reaches the seek.
func TestFinalizationAtAnOrdinaryTargetBindsThePredecessor(t *testing.T) {
	k, ctx, bank, _ := setupFinalizationAtEpoch(t, 3)
	// Effective at epoch 1 is the anchor; a version effective at epoch 2 is what
	// target 4 would bind, and must NOT govern target 3.
	seedRewardVersion(t, k, ctx, rewardVersionAt(2, 2, "999"))

	governing, err := k.RewardConfigForTarget(ctx, 3)
	require.NoError(t, err)
	require.Equal(t, uint64(1), governing.Version, "target 3 binds epoch 1")

	require.NoError(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))
	require.Equal(t, "3600utwlt", bank.minted.String())

	entitlements, err := k.IterateEntitlementsForEpoch(ctx, 3)
	require.NoError(t, err)
	require.Len(t, entitlements, 2)
	for _, entitlement := range entitlements {
		require.Equal(t, uint64(1), entitlement.RewardConfigVersion)
	}
	requireLiability(t, k, ctx, sdkmath.NewInt(3600).String())
}
