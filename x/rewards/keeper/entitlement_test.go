package keeper_test

import (
	"testing"

	sdkmath "cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	"github.com/twilight-project/twilight-core/x/rewards/keeper"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// Canonical entitlement state and the O(1) outstanding-liability accumulator.
//
// Nothing here drives finalization: at this gate the creation primitive has no
// production caller. What is under test is the state itself — that an obligation
// can be created exactly once, that only its released amount can ever move, that
// the accumulator tracks it exactly, and that a store which disagrees with itself
// stops a payment rather than approximating one.

func entitlementFor(slotID, epoch uint64, amount string) types.SlotEntitlement {
	return types.SlotEntitlement{
		SlotId:                         slotID,
		Epoch:                          epoch,
		TotalBlocksActive:              10,
		EntitlementAmount:              amount,
		ReleasedAmount:                 "0",
		PayoutAddress:                  testAccount(byte(slotID + 40)),
		RewardConfigVersion:            1,
		SlotStatusAtEpochClose:         coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE,
		ActivationSequenceAtEpochClose: 1,
		CreatedHeight:                  100,
	}
}

func setupEntitlements(t *testing.T) (keeper.Keeper, sdk.Context, *bankKeeperMock) {
	t.Helper()
	k, ctx, bank := setupAccountingKeeper(t, &coreSlotKeeperMock{}, 1, rewardConfigParams())
	require.NoError(t, k.SetOutstandingEntitlementLiability(ctx, sdkmath.ZeroInt()))
	return k, ctx, bank
}

func requireLiability(t *testing.T, k keeper.Keeper, ctx sdk.Context, want string) {
	t.Helper()
	liability, err := k.GetOutstandingEntitlementLiability(ctx)
	require.NoError(t, err)
	require.Equal(t, want, liability.String())

	// The accumulator and the records it summarizes must agree. This is the full
	// scan the accumulator exists to avoid on the block path; running it here is
	// what would catch an accumulator that drifted.
	sum, err := k.SumOutstandingEntitlementLiability(ctx)
	require.NoError(t, err)
	require.Equal(t, liability.String(), sum.String(),
		"the O(1) accumulator must equal the definitional sum")
}

func TestEntitlementCreationIsWriteOnce(t *testing.T) {
	k, ctx, _ := setupEntitlements(t)
	require.NoError(t, k.CreateSlotEntitlement(ctx, entitlementFor(1, 1, "500")))
	requireLiability(t, k, ctx, "500")

	// A second creation for the same obligation is not an update. Accepting it
	// would put two obligations over one escrow, and the liability would carry
	// whichever amount was written last.
	second := entitlementFor(1, 1, "900")
	require.ErrorIs(t, k.CreateSlotEntitlement(ctx, second), types.ErrInvalidState)

	stored, found, err := k.GetSlotEntitlement(ctx, 1, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "500", stored.EntitlementAmount, "the refused creation must not have overwritten anything")
	requireLiability(t, k, ctx, "500")
}

func TestEntitlementCreationAccumulatesLiabilityExactly(t *testing.T) {
	k, ctx, _ := setupEntitlements(t)
	require.NoError(t, k.CreateSlotEntitlement(ctx, entitlementFor(1, 1, "500")))
	require.NoError(t, k.CreateSlotEntitlement(ctx, entitlementFor(2, 1, "1250")))
	require.NoError(t, k.CreateSlotEntitlement(ctx, entitlementFor(1, 2, "7")))
	requireLiability(t, k, ctx, "1757")
}

func TestEntitlementCreationRejectsMalformedObligations(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*types.SlotEntitlement)
	}{
		{"zero slot", func(e *types.SlotEntitlement) { e.SlotId = 0 }},
		{"zero epoch", func(e *types.SlotEntitlement) { e.Epoch = 0 }},
		{"no reward configuration version", func(e *types.SlotEntitlement) { e.RewardConfigVersion = 0 }},
		{"no creation height", func(e *types.SlotEntitlement) { e.CreatedHeight = 0 }},
		{"no participation", func(e *types.SlotEntitlement) { e.TotalBlocksActive = 0 }},
		{"unparseable amount", func(e *types.SlotEntitlement) { e.EntitlementAmount = "lots" }},
		{"negative amount", func(e *types.SlotEntitlement) { e.EntitlementAmount = "-1" }},
		// Zero-amount entitlements are not persisted. Refusing at the writer keeps
		// an obligation to pay nothing unrepresentable rather than merely unusual.
		{"zero amount", func(e *types.SlotEntitlement) { e.EntitlementAmount = "0" }},
		{"released above the amount", func(e *types.SlotEntitlement) { e.ReleasedAmount = "501" }},
		{"created already partly released", func(e *types.SlotEntitlement) { e.ReleasedAmount = "1" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx, _ := setupEntitlements(t)
			entitlement := entitlementFor(1, 1, "500")
			tc.mutate(&entitlement)
			require.Error(t, k.CreateSlotEntitlement(ctx, entitlement))
			requireLiability(t, k, ctx, "0")
		})
	}
}

// TestEntitlementCreationRequiresAnAdmissibleDestination admits the payout
// snapshot as a value destination at the moment it becomes immutable.
func TestEntitlementCreationRequiresAnAdmissibleDestination(t *testing.T) {
	blocked := testAccount(77)
	for _, tc := range []struct {
		name    string
		address string
	}{
		{"a module account", testModuleAddress(testModuleAccountName)},
		{"a bank-blocked account", blocked},
		{"the zero address", zeroAddress()},
		{"a malformed address", "not-an-address"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx, _ := setupKeeperWithBlocked(t, &coreSlotKeeperMock{}, blocked)
			require.NoError(t, k.SetParams(ctx, rewardConfigParams()))
			seedRewardConfigTimeline(t, k, ctx, rewardConfigParams())
			require.NoError(t, k.SetOutstandingEntitlementLiability(ctx, sdkmath.ZeroInt()))

			entitlement := entitlementFor(1, 1, "500")
			entitlement.PayoutAddress = tc.address
			require.ErrorIs(t, k.CreateSlotEntitlement(ctx, entitlement), types.ErrInvalidAddress)
			requireLiability(t, k, ctx, "0")
		})
	}
}

// TestEntitlementCreationRequiresTheGoverningRewardConfigVersion is the
// referential-integrity rule.
//
// An entitlement records which configuration governed the epoch that created it.
// The check is not "a version with this number exists" but "this is the version
// that governs this epoch" — the weaker form would admit a record naming some
// unrelated version, which is exactly the record that makes a payout unauditable
// afterwards.
func TestEntitlementCreationRequiresTheGoverningRewardConfigVersion(t *testing.T) {
	k, ctx, _ := setupEntitlements(t)
	// A second version that genuinely exists, and does NOT govern epoch 1.
	seedRewardVersion(t, k, ctx, rewardVersionAt(9, 40, "20"))

	t.Run("a version that exists but does not govern this epoch", func(t *testing.T) {
		entitlement := entitlementFor(1, 1, "500")
		entitlement.RewardConfigVersion = 9
		require.ErrorIs(t, k.CreateSlotEntitlement(ctx, entitlement), types.ErrInvalidState)
		requireLiability(t, k, ctx, "0")
	})

	t.Run("a version that does not exist at all", func(t *testing.T) {
		entitlement := entitlementFor(2, 1, "500")
		entitlement.RewardConfigVersion = 77
		require.Error(t, k.CreateSlotEntitlement(ctx, entitlement))
		requireLiability(t, k, ctx, "0")
	})

	t.Run("the governing version is admitted", func(t *testing.T) {
		// Epoch 1 bootstraps to the genesis anchor, version 1.
		require.NoError(t, k.CreateSlotEntitlement(ctx, entitlementFor(1, 1, "500")))
		requireLiability(t, k, ctx, "500")
	})

	t.Run("an epoch whose binding resolves the later version", func(t *testing.T) {
		// Target 42 binds epoch 40, which version 9 governs.
		entitlement := entitlementFor(1, 42, "500")
		entitlement.RewardConfigVersion = 1
		require.ErrorIs(t, k.CreateSlotEntitlement(ctx, entitlement), types.ErrInvalidState)

		entitlement.RewardConfigVersion = 9
		require.NoError(t, k.CreateSlotEntitlement(ctx, entitlement))
	})
}

// TestEntitlementReadsFailClosedOnCorruptRecords proves a stored record that
// disagrees with itself or with its key stops a read rather than presenting as
// absent or as an empty obligation.
func TestEntitlementReadsFailClosedOnCorruptRecords(t *testing.T) {
	for _, tc := range []struct {
		name  string
		store func(*testing.T, keeper.Keeper, sdk.Context)
	}{
		{
			name: "the record declares a different slot than its key",
			store: func(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
				require.NoError(t, k.SlotEntitlements.Set(ctx, collections.Join(uint64(1), uint64(1)),
					entitlementFor(5, 1, "500")))
			},
		},
		{
			name: "the record declares a different epoch than its key",
			store: func(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
				require.NoError(t, k.SlotEntitlements.Set(ctx, collections.Join(uint64(1), uint64(1)),
					entitlementFor(1, 9, "500")))
			},
		},
		{
			name: "the record has released more than it owes",
			store: func(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
				corrupt := entitlementFor(1, 1, "500")
				corrupt.ReleasedAmount = "501"
				require.NoError(t, k.SlotEntitlements.Set(ctx, collections.Join(uint64(1), uint64(1)), corrupt))
			},
		},
		{
			name: "the record carries an unparseable amount",
			store: func(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
				corrupt := entitlementFor(1, 1, "500")
				corrupt.EntitlementAmount = "lots"
				require.NoError(t, k.SlotEntitlements.Set(ctx, collections.Join(uint64(1), uint64(1)), corrupt))
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx, _ := setupEntitlements(t)
			tc.store(t, k, ctx)

			_, _, err := k.GetSlotEntitlement(ctx, 1, 1)
			require.ErrorIs(t, err, types.ErrInvalidState)

			// The per-epoch enumeration is held to the same rule: a corrupt row must
			// not be skipped, because settlement would then materialize a set that is
			// missing an obligation the chain still owes.
			_, err = k.IterateEntitlementsForEpoch(ctx, 1)
			require.ErrorIs(t, err, types.ErrInvalidState)
		})
	}
}

// TestEntitlementAbsenceIsOrdinary separates genuine absence from corruption.
func TestEntitlementAbsenceIsOrdinary(t *testing.T) {
	k, ctx, _ := setupEntitlements(t)
	_, found, err := k.GetSlotEntitlement(ctx, 4, 1)
	require.NoError(t, err)
	require.False(t, found, "a Slot that earned nothing simply has no entitlement")

	rows, err := k.IterateEntitlementsForEpoch(ctx, 7)
	require.NoError(t, err)
	require.Empty(t, rows)
}

// TestEntitlementEpochEnumerationIsDeterministicallyAscending is the property the
// key order exists to provide.
//
// Allocation and, later, settlement materialization both walk one epoch in
// ascending slot_id order. The ordering must come from the key rather than from a
// sort applied afterwards, so it cannot be lost by a caller that forgets to sort.
func TestEntitlementEpochEnumerationIsDeterministicallyAscending(t *testing.T) {
	k, ctx, _ := setupEntitlements(t)
	// Written out of order, and interleaved with a neighboring epoch.
	for _, slotID := range []uint64{9, 2, 40, 1, 17} {
		require.NoError(t, k.CreateSlotEntitlement(ctx, entitlementFor(slotID, 4, "10")))
		require.NoError(t, k.CreateSlotEntitlement(ctx, entitlementFor(slotID, 5, "10")))
	}

	rows, err := k.IterateEntitlementsForEpoch(ctx, 4)
	require.NoError(t, err)
	require.Len(t, rows, 5, "the range must not leak into a neighboring epoch")

	got := make([]uint64, 0, len(rows))
	for _, row := range rows {
		require.Equal(t, uint64(4), row.Epoch)
		got = append(got, row.SlotId)
	}
	require.Equal(t, []uint64{1, 2, 9, 17, 40}, got)
}

// TestOutstandingLiabilityHasNoDefault is the fail-closed rule for the
// accumulator.
//
// Genesis writes it explicitly. Afterwards an absent value is corruption, not
// zero: reporting zero would say the module owes nothing while entitlements sit
// unpaid in the store, and every solvency assertion built on it would then pass.
func TestOutstandingLiabilityHasNoDefault(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		k, ctx, _ := setupEntitlements(t)
		// Removed explicitly rather than relying on a fixture that never wrote it:
		// the fixtures establish it exactly as InitGenesis would, so absence has to
		// be created on purpose to be tested.
		require.NoError(t, k.OutstandingEntitlementLiability.Remove(ctx))
		_, err := k.GetOutstandingEntitlementLiability(ctx)
		require.ErrorIs(t, err, types.ErrInvalidState)
	})

	t.Run("unparseable", func(t *testing.T) {
		k, ctx, _ := setupEntitlements(t)
		require.NoError(t, k.OutstandingEntitlementLiability.Set(ctx, "owed"))
		_, err := k.GetOutstandingEntitlementLiability(ctx)
		require.ErrorIs(t, err, types.ErrInvalidState)
	})

	t.Run("a corrupt accumulator blocks creation rather than being rebuilt", func(t *testing.T) {
		k, ctx, _ := setupEntitlements(t)
		require.NoError(t, k.OutstandingEntitlementLiability.Set(ctx, "owed"))
		require.Error(t, k.CreateSlotEntitlement(ctx, entitlementFor(1, 1, "500")))
	})
}

// TestOutstandingLiabilityRefusesNegativeAndUninitializedValues covers the
// accumulator's own write guard.
func TestOutstandingLiabilityRefusesNegativeAndUninitializedValues(t *testing.T) {
	k, ctx, _ := setupEntitlements(t)
	require.Error(t, k.SetOutstandingEntitlementLiability(ctx, sdkmath.NewInt(-1)))
	require.Error(t, k.SetOutstandingEntitlementLiability(ctx, sdkmath.Int{}))
}

// TestEntitlementAmountsRemainArbitraryPrecision keeps monetary values off
// fixed-width arithmetic.
//
// A base-denom amount above math.MaxUint64 is a legitimate value, not an
// overflow, so both the record and the accumulator must carry it exactly.
func TestEntitlementAmountsRemainArbitraryPrecision(t *testing.T) {
	k, ctx, _ := setupEntitlements(t)
	const huge = "340282366920938463463374607431768211456" // 2^128
	require.NoError(t, k.CreateSlotEntitlement(ctx, entitlementFor(1, 1, huge)))
	require.NoError(t, k.CreateSlotEntitlement(ctx, entitlementFor(2, 1, huge)))

	want, ok := sdkmath.NewIntFromString("680564733841876926926749214863536422912") // 2^129
	require.True(t, ok)
	requireLiability(t, k, ctx, want.String())
}

// TestFreshGenesisCarriesNoObligations is the content rule at the module boundary.
func TestFreshGenesisCarriesNoObligations(t *testing.T) {
	t.Run("a non-empty entitlement collection is refused", func(t *testing.T) {
		k, ctx, _ := setupKeeper(t, &coreSlotKeeperMock{})
		genesis := *types.DefaultGenesis()
		entitlement := entitlementFor(1, 1, "500")
		genesis.SlotEntitlements = []*types.SlotEntitlement{&entitlement}
		require.ErrorIs(t, k.InitGenesis(ctx, genesis), types.ErrInvalidGenesis)
	})

	t.Run("a nonzero liability is refused", func(t *testing.T) {
		k, ctx, _ := setupKeeper(t, &coreSlotKeeperMock{})
		genesis := *types.DefaultGenesis()
		genesis.OutstandingEntitlementLiability = "1"
		require.ErrorIs(t, k.InitGenesis(ctx, genesis), types.ErrInvalidGenesis)
	})

	t.Run("an absent liability is refused rather than defaulted", func(t *testing.T) {
		k, ctx, _ := setupKeeper(t, &coreSlotKeeperMock{})
		genesis := *types.DefaultGenesis()
		genesis.OutstandingEntitlementLiability = ""
		require.ErrorIs(t, k.InitGenesis(ctx, genesis), types.ErrInvalidGenesis)
	})

	t.Run("the default document initializes the accumulator explicitly", func(t *testing.T) {
		k, ctx, _ := setupKeeper(t, &coreSlotKeeperMock{})
		require.NoError(t, k.InitGenesis(ctx, *types.DefaultGenesis()))
		requireLiability(t, k, ctx, "0")
	})
}

// entitlementTestKey mirrors the keeper's canonical (epoch, slot_id) key so
// fixtures can write straight to the store when they need to plant a record that
// the admission path would refuse.
func entitlementTestKey(slotID, epoch uint64) collections.Pair[uint64, uint64] {
	return collections.Join(epoch, slotID)
}
