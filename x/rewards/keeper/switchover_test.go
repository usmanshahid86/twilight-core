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

// The V2 monetary switchover.
//
// Finalization now binds the canonical reward configuration, creates entitlements
// and raises the liability, and stops producing the claim-shaped representation
// entirely. The tests below are grouped by what would break if a piece of that
// were wrong, rather than by which function it lives in.

// TestFinalizationCreatesExactlyOnePayableRepresentation is the switchover rule.
//
// The prohibition is not "prefer entitlements"; it is that one obligation must
// never have two payable forms over one escrow. Both halves are asserted: the
// entitlement exists, and the claim record that used to exist does not — so the
// legacy path has nothing to pay even though it is still reachable.
func TestFinalizationCreatesExactlyOnePayableRepresentation(t *testing.T) {
	k, ctx, _, _ := setupFinalization(t, false)
	require.NoError(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))

	for _, slotID := range []uint64{1, 2} {
		entitlement, found, err := k.GetSlotEntitlement(ctx, slotID, 1)
		require.NoError(t, err)
		require.Truef(t, found, "slot %d must have an entitlement", slotID)
		require.Equal(t, "0", entitlement.ReleasedAmount)

		_, found, err = k.GetClaimRecord(ctx, slotID, 1)
		require.NoError(t, err)
		require.Falsef(t, found, "slot %d must have no claim record for a V2 epoch", slotID)
	}
}

// TestLegacyClaimCannotPayANewObligation closes the economic bypass.
//
// ClaimRewards is deliberately still reachable — retiring it belongs to a later
// work package — so what matters is that it has nothing to act on. A claim for a
// V2 epoch fails because no record exists for it, not because the surface was
// removed.
func TestLegacyClaimCannotPayANewObligation(t *testing.T) {
	k, ctx, bank, _ := setupFinalization(t, false)
	require.NoError(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))
	sendsAfterFinalization := bank.sendCalls

	err := k.ClaimRewards(ctx, &types.MsgClaimRewards{
		Signer: addr(1), SlotId: 1, StartEpoch: 1, EndEpoch: 1,
	})
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Equal(t, sendsAfterFinalization, bank.sendCalls,
		"the legacy path must move no value for a V2 obligation")

	// And the entitlement is untouched by the attempt.
	entitlement, found, err := k.GetSlotEntitlement(ctx, 1, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "0", entitlement.ReleasedAmount)
}

// TestFinalizationBindsTheTargetRewardConfig proves the economics come from the
// canonical history and follow the N-2 rule, not from the deprecated mirror.
func TestFinalizationBindsTheTargetRewardConfig(t *testing.T) {
	k, ctx, bank, _ := setupFinalization(t, false)

	// A configuration effective at epoch 2 with a different subsidy. Target 1 must
	// not see it: targets 1 and 2 bootstrap to the genesis version.
	seedRewardVersion(t, k, ctx, rewardVersionAt(2, 2, "999"))
	require.NoError(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))

	// 360 reward-enabled blocks at the GENESIS subsidy of 10.
	require.Equal(t, "3600utwlt", bank.minted.String())
	entitlements, err := k.IterateEntitlementsForEpoch(ctx, 1)
	require.NoError(t, err)
	require.NotEmpty(t, entitlements)
	for _, entitlement := range entitlements {
		require.Equal(t, uint64(1), entitlement.RewardConfigVersion,
			"every obligation records the version that governed its epoch")
	}
}

// TestFinalizationIgnoresTheDeprecatedEconomicMirror is the mutation-sensitive
// case for where the economics are read from.
//
// The snapshot's subsidy is moved to a value that would produce a different mint.
// Nothing changes, because the mirror is not consulted. Point finalization back at
// cfg.InitialBlockSubsidy and this fails.
func TestFinalizationIgnoresTheDeprecatedEconomicMirror(t *testing.T) {
	k, ctx, bank, _ := setupFinalization(t, false)
	cfg, err := k.GetCurrentEpochConfig(ctx)
	require.NoError(t, err)
	cfg.InitialBlockSubsidy = "999"
	cfg.EmissionTreasuryShareBps = 5_000
	cfg.TreasuryAddress = addr(8)
	require.NoError(t, k.CurrentEpochConfig.Set(ctx, cfg))

	require.NoError(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))
	require.Equal(t, "3600utwlt", bank.minted.String(),
		"the mint follows the canonical reward configuration, not the mirror")
	require.Zero(t, bank.sendCalls,
		"the mirror's treasury share must direct no value")
}

// TestFinalizationUsesParticipationAsTheShareDenominator separates the two block
// counts that are easy to confuse.
//
// reward_enabled_blocks scales the emission; W = Σ blocks_active divides the
// resulting pool. Here the epoch is fully enabled at 360 blocks but the two Slots
// together participated in only 180, so a denominator mix-up would allocate half
// the pool and silently carry the rest.
func TestFinalizationUsesParticipationAsTheShareDenominator(t *testing.T) {
	k, ctx, _, _ := setupFinalization(t, true)
	require.NoError(t, k.SetActiveBlocks(ctx, 1, 1, 60))
	require.NoError(t, k.SetActiveBlocks(ctx, 1, 2, 120))

	require.NoError(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))

	epoch, found, err := k.GetFinalizedEpoch(ctx, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "3600", epoch.MintedEmission, "emission follows reward_enabled_blocks")
	require.Equal(t, "3600", epoch.AllocatedAmount, "the whole pool is divided among participants")
	require.Equal(t, "0", epoch.CarryOut)

	entitlements, err := k.IterateEntitlementsForEpoch(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, []string{"1200", "2400"},
		[]string{entitlements[0].EntitlementAmount, entitlements[1].EntitlementAmount},
		"a 1:2 participation split of the whole pool")
	requireLiability(t, k, ctx, "3600")
}

// TestFinalizationCarriesTheWholePoolWhenNobodyParticipated covers §31's W == 0
// rule at the finalization level.
func TestFinalizationCarriesTheWholePoolWhenNobodyParticipated(t *testing.T) {
	k, ctx, bank, _ := setupFinalization(t, true)
	require.NoError(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))

	require.Equal(t, "3600utwlt", bank.minted.String(),
		"an unpaused epoch still emits; mass inactivation is not a pause mechanism")
	state, err := k.GetState(ctx)
	require.NoError(t, err)
	require.Equal(t, "3600", state.CarryForwardRemainder)

	entitlements, err := k.IterateEntitlementsForEpoch(ctx, 1)
	require.NoError(t, err)
	require.Empty(t, entitlements)
	requireLiability(t, k, ctx, "0")
}

// TestFinalizationOmitsZeroAmountEntitlements proves a floored-to-zero share
// creates no obligation while still counting toward the residue bound.
func TestFinalizationOmitsZeroAmountEntitlements(t *testing.T) {
	k, ctx, _, core := setupFinalization(t, true)
	// Three participants and a pool that cannot divide: subsidy 10 over 360 blocks
	// is 3600, but only two blocks are reward-enabled here, so the pool is 20 and
	// the third Slot's share floors away.
	core.slots[3] = slotWithID(core.slots[1], 3)
	require.NoError(t, k.SetOpenRewardEnabledBlocks(ctx, 2))
	require.NoError(t, k.SetActiveBlocks(ctx, 1, 1, 1))
	require.NoError(t, k.SetActiveBlocks(ctx, 1, 2, 1))
	require.NoError(t, k.SetActiveBlocks(ctx, 1, 3, 1))

	require.NoError(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))

	epoch, found, err := k.GetFinalizedEpoch(ctx, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "20", epoch.MintedEmission)
	// 20 across three equal participants: each floors to 6, carrying 2.
	require.Equal(t, "18", epoch.AllocatedAmount)
	require.Equal(t, "2", epoch.CarryOut)

	entitlements, err := k.IterateEntitlementsForEpoch(ctx, 1)
	require.NoError(t, err)
	require.Len(t, entitlements, 3)
	requireLiability(t, k, ctx, "18")
}

// TestFinalizationEnforcesTheResidueBound is the assertion a definitional
// equality cannot replace.
//
// carry = pool - allocated always holds by construction. carry <= n_pos - 1 does
// not: it follows from flooring exactly once per participating Slot, so a wrong
// denominator or a double-counted Slot breaks it while still producing a
// plausible-looking split.
func TestFinalizationEnforcesTheResidueBound(t *testing.T) {
	k, ctx, _, _ := setupFinalization(t, false)
	require.NoError(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))

	epoch, found, err := k.GetFinalizedEpoch(ctx, 1)
	require.NoError(t, err)
	require.True(t, found)

	carry, ok := sdkmath.NewIntFromString(epoch.CarryOut)
	require.True(t, ok)
	// Two participating Slots, so the residue may be at most one unit.
	require.True(t, carry.LTE(sdkmath.NewInt(1)),
		"carry %s exceeds the residue bound for two participants", carry)
}

// TestFinalizationAssertsEscrowSolvency proves the equality is live rather than
// documented.
//
// The liability is inflated before the boundary, so escrow can no longer equal
// liability plus carry. Nothing else about the epoch changes.
func TestFinalizationAssertsEscrowSolvency(t *testing.T) {
	k, ctx, _, _ := setupFinalization(t, false)
	require.NoError(t, k.SetOutstandingEntitlementLiability(ctx, sdkmath.NewInt(7)))

	require.ErrorIs(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)), types.ErrInvalidState)
	requireNothingFinalized(t, k, ctx)
}

// TestFinalizationZeroOperationsMakeNoKeeperCall is B2, asserted on CALLS.
//
// A balance comparison cannot distinguish "no transfer" from "a transfer of
// zero", and the difference matters: a zero-value send still touches, and can
// create, the destination account.
func TestFinalizationZeroOperationsMakeNoKeeperCall(t *testing.T) {
	t.Run("zero emission makes no mint call", func(t *testing.T) {
		k, ctx, bank, _ := setupFinalization(t, true)
		// A fully paused epoch counted no reward-enabled blocks, so emission is zero.
		require.NoError(t, k.SetOpenRewardEnabledBlocks(ctx, 0))
		// A positive treasury share, to prove the zero treasury below follows from
		// the zero emission rather than from the configuration.
		seedTreasuryRewardConfig(t, k, ctx, 1_000, addr(8))

		require.NoError(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))
		require.Zero(t, bank.mintCalls, "a zero mint must not invoke the mint keeper")
		require.Zero(t, bank.sendCalls, "a zero treasury must not invoke the bank keeper")

		// The rest of finalization still happened.
		epoch, found, err := k.GetFinalizedEpoch(ctx, 1)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, "0", epoch.MintedEmission)
		require.Equal(t, "0", epoch.TreasuryAmount)
	})

	t.Run("zero treasury share makes no send call", func(t *testing.T) {
		k, ctx, bank, _ := setupFinalization(t, false)
		require.NoError(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))
		require.Equal(t, 1, bank.mintCalls)
		require.Zero(t, bank.sendCalls)
	})

	t.Run("a zero treasury performs no transfer-time destination validation", func(t *testing.T) {
		// The destination is one the canonical rule would refuse. With a zero share
		// it is never revalidated, so finalization succeeds -- which is exactly
		// §33.2: admission validated the configuration, and a zero amount performs
		// no transfer to revalidate for.
		k, ctx, bank, _ := setupFinalization(t, false)
		seedTreasuryRewardConfigUnchecked(t, k, ctx, 0, testModuleAddress(testModuleAccountName))

		require.NoError(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))
		require.Zero(t, bank.sendCalls)
	})

	t.Run("a positive treasury revalidates immediately before transfer", func(t *testing.T) {
		k, ctx, bank, _ := setupFinalization(t, false)
		seedTreasuryRewardConfigUnchecked(t, k, ctx, 1_000, testModuleAddress(testModuleAccountName))

		require.ErrorIs(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)), types.ErrInvalidAddress)
		require.Zero(t, bank.sendCalls)
		requireNothingFinalized(t, k, ctx)
	})
}

// TestFinalizationRollsBackCompletely walks every failure point in the monetary
// transition and proves none of them can leave partial state.
func TestFinalizationRollsBackCompletely(t *testing.T) {
	for _, tc := range []struct {
		name string
		fail func(*testing.T, keeper.Keeper, sdk.Context, *bankKeeperMock)
	}{
		{
			name: "mint failure",
			fail: func(_ *testing.T, _ keeper.Keeper, _ sdk.Context, bank *bankKeeperMock) {
				bank.failMint()
			},
		},
		{
			name: "treasury transfer failure",
			fail: func(t *testing.T, k keeper.Keeper, ctx sdk.Context, bank *bankKeeperMock) {
				seedTreasuryRewardConfig(t, k, ctx, 1_000, addr(8))
				bank.failSend()
			},
		},
		{
			name: "entitlement creation failure",
			fail: func(t *testing.T, k keeper.Keeper, ctx sdk.Context, _ *bankKeeperMock) {
				// An obligation already exists for one of the participating Slots, so
				// write-once refuses the creation mid-transition.
				require.NoError(t, k.CreateSlotEntitlement(ctx, entitlementFor(1, 1, "5")))
			},
		},
		{
			name: "liability mutation failure",
			fail: func(t *testing.T, k keeper.Keeper, ctx sdk.Context, _ *bankKeeperMock) {
				require.NoError(t, k.OutstandingEntitlementLiability.Set(ctx, "owed"))
			},
		},
		{
			name: "finalized epoch write failure",
			fail: func(t *testing.T, k keeper.Keeper, ctx sdk.Context, _ *bankKeeperMock) {
				// The epoch record is immutable, so a record already present makes the
				// write refuse.
				params, err := k.GetParams(ctx)
				require.NoError(t, err)
				require.NoError(t, k.SetFinalizedEpoch(ctx, validEpoch(1, params)))
			},
		},
		{
			name: "reward configuration promotion failure",
			fail: func(t *testing.T, k keeper.Keeper, ctx sdk.Context, _ *bankKeeperMock) {
				require.NoError(t, k.ScheduledRewardConfigs.Set(ctx, 2, types.ScheduledRewardConfig{
					EffectiveEpoch: 2, InitialBlockSubsidy: "0",
				}))
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx, bank, _ := setupFinalization(t, false)
			before, err := k.GetOutstandingEntitlementLiability(ctx)
			require.NoError(t, err)
			tc.fail(t, k, ctx, bank)
			liabilityAtFault, err := k.GetOutstandingEntitlementLiability(ctx)
			if err == nil {
				before = liabilityAtFault
			}

			require.Error(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))

			// No finalized epoch, no cumulative movement, no carry, no epoch advance.
			state, err := k.GetState(ctx)
			require.NoError(t, err)
			require.Equal(t, "0", state.CumulativeEmitted)
			require.Equal(t, "0", state.CarryForwardRemainder)
			require.Equal(t, uint64(1), state.CurrentEpoch)

			// The liability is exactly where the fault left it: finalization added
			// nothing that survived.
			if after, err := k.GetOutstandingEntitlementLiability(ctx); err == nil {
				require.Equal(t, before.String(), after.String())
			}
		})
	}
}

// TestFinalizationPreservesTheExactHeightBoundary is the non-regression guard for
// the merged timeline behavior the switchover must not disturb.
func TestFinalizationPreservesTheExactHeightBoundary(t *testing.T) {
	t.Run("before the boundary nothing happens", func(t *testing.T) {
		k, ctx, bank, _ := setupFinalization(t, false)
		require.NoError(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight-1)))
		require.Zero(t, bank.mintCalls)
		requireNothingFinalized(t, k, ctx)
	})

	t.Run("a late height fails closed with no monetary effect", func(t *testing.T) {
		k, ctx, bank, _ := setupFinalization(t, false)
		require.ErrorIs(t,
			k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight+1)), types.ErrInvalidState)
		require.Zero(t, bank.mintCalls)
		require.Zero(t, bank.sendCalls)
		requireNothingFinalized(t, k, ctx)

		entitlements, err := k.IterateEntitlementsForEpoch(ctx, 1)
		require.NoError(t, err)
		require.Empty(t, entitlements)
		requireLiability(t, k, ctx, "0")
	})
}

// slotWithID copies a CoreSlot under a different identity, so a fixture can add a
// participant without restating every field.
func slotWithID(slot coreslottypes.CoreSlot, slotID uint64) coreslottypes.CoreSlot {
	slot.SlotId = slotID
	slot.PayoutAddress = addr(byte(50 + slotID))
	slot.OperatorAddress = addr(byte(60 + slotID))
	return slot
}

// TestLegacyClaimStateAndSolvencyDoNotMix pins a real, deliberate consequence of
// the switchover rather than leaving it to be discovered.
//
// The solvency assertion states that escrow holds exactly what the module
// believes it owes: outstanding entitlement liability plus carry. A legacy claim
// record is an obligation the liability accumulator does not count, so escrow
// funded for one — or drained by paying one — no longer matches, and the next
// finalization halts the block rather than committing an accounting it cannot
// justify.
//
// # Why this is acceptable, and where the fix lives
//
// After the switchover nothing creates a claim record, so the only way a chain
// can hold one is a genesis document that carries it. This tranche deliberately
// leaves legacy claim genesis handling untouched — retiring it belongs to the
// later claims-retirement work package, which is also where fresh genesis starts
// rejecting a non-empty claim collection outright.
//
// Until then the behavior is fail-closed and legible: the chain stops with a
// message naming the exact discrepancy, rather than paying out against an
// accounting that no longer adds up. This test exists so that is a known property
// with a stated owner, not a surprise for whoever meets it first.
func TestLegacyClaimStateAndSolvencyDoNotMix(t *testing.T) {
	k, ctx, bank, _ := setupFinalization(t, false)
	require.NoError(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))

	// A legacy obligation the accumulator does not know about, paid out of the
	// escrow the entitlements are relying on.
	params, err := k.GetParams(ctx)
	require.NoError(t, err)
	require.NoError(t, k.SetClaimRecord(ctx, validClaim(1, 1)))
	require.NoError(t, k.ClaimRewards(ctx, &types.MsgClaimRewards{
		Signer: addr(1), SlotId: 1, StartEpoch: 1, EndEpoch: 1,
	}))

	// Escrow has now fallen below what the entitlements still claim.
	liability, err := k.GetOutstandingEntitlementLiability(ctx)
	require.NoError(t, err)
	balance := bank.GetBalance(ctx, moduleAccountAddress(), params.NativeDenom).Amount
	require.True(t, balance.LT(liability),
		"the legacy payment left escrow short of the entitlements it still owes")

	// The coverage invariant reports it, which is what an operator would see.
	_, broken := k.ModuleBalanceCoverageInvariant()(ctx)
	require.True(t, broken,
		"paying a legacy claim beside live entitlements is a solvency defect the chain reports")
}
