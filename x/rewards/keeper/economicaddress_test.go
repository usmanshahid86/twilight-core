package keeper_test

import (
	"testing"

	"cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// Canonical economic-address admission in x/rewards (§25).
//
// The conditional treasury rule is the subtle half: the architecture makes a
// treasury address mandatory only when a treasury share is positive, and the
// default configuration ships with no address and zero shares. Both directions
// are pinned here, because tightening the optional case would break every
// default genesis and loosening the required case would let value be directed at
// an unspendable destination.

func paramsWithTreasury(address string, emissionBps, feeBps uint64) types.Params {
	params := types.DefaultParams()
	params.TreasuryAddress = address
	params.EmissionTreasuryShareBps = emissionBps
	params.FeeTreasuryShareBps = feeBps
	return params
}

// --- params admission ---------------------------------------------------------

// TestSetParamsKeepsEmptyTreasuryLegalWhenSharesAreZero is the default
// configuration. It must remain valid.
func TestSetParamsKeepsEmptyTreasuryLegalWhenSharesAreZero(t *testing.T) {
	k, ctx, _ := setupKeeper(t, &coreSlotKeeperMock{})

	params := types.DefaultParams()
	require.Empty(t, params.TreasuryAddress)
	require.Zero(t, params.EmissionTreasuryShareBps)
	require.Zero(t, params.FeeTreasuryShareBps)

	require.NoError(t, k.SetParams(ctx, params))

	stored, err := k.GetParams(ctx)
	require.NoError(t, err)
	require.Empty(t, stored.TreasuryAddress)
}

func TestSetParamsRejectsInadmissibleTreasuryWhenShareIsPositive(t *testing.T) {
	blocked := testAccount(77)

	cases := []struct {
		name     string
		address  string
		emission uint64
		fee      uint64
		reason   string
	}{
		{"module account, emission share", testModuleAddress(testModuleAccountName), 100, 0, "module account"},
		{"module account, fee share", testModuleAddress(testModuleAccountName), 0, 100, "module account"},
		{"bank-blocked, emission share", blocked, 100, 0, "blocked"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx, _ := setupKeeperWithBlocked(t, &coreSlotKeeperMock{}, blocked)

			err := k.SetParams(ctx, paramsWithTreasury(tc.address, tc.emission, tc.fee))
			require.ErrorIs(t, err, types.ErrInvalidAddress)
			require.Contains(t, err.Error(), tc.reason)
		})
	}
}

func TestSetParamsAcceptsOrdinaryTreasuryWithPositiveShare(t *testing.T) {
	k, ctx, _ := setupKeeper(t, &coreSlotKeeperMock{})

	treasury := testAccount(31)
	require.NoError(t, k.SetParams(ctx, paramsWithTreasury(treasury, 100, 0)))

	stored, err := k.GetParams(ctx)
	require.NoError(t, err)
	require.Equal(t, treasury, stored.TreasuryAddress)
}

// TestSetPendingParamsCannotMoveTheTreasuryAtAll replaces the older test that
// checked the pending path applied the treasury ADDRESS policy.
//
// That premise is gone. The treasury destination and share moved to the canonical
// reward-configuration history, so the pending-params path can no longer carry
// either one — well or badly. The stronger statement is what is asserted here: an
// attempt to move them is refused as immutable, whether the proposed destination
// would have been admissible or not.
//
// The inadmissible-destination rule still exists and is still reachable, through
// SetParams at genesis, through the epoch-configuration snapshot, and through
// reward-configuration admission. Those have their own tests.
func TestSetPendingParamsCannotMoveTheTreasuryAtAll(t *testing.T) {
	blocked := testAccount(77)
	k, ctx, _ := setupKeeperWithBlocked(t, &coreSlotKeeperMock{}, blocked)
	require.NoError(t, k.SetParams(ctx, types.DefaultParams()))

	for _, tc := range []struct {
		name    string
		address string
	}{
		{"a module account", testModuleAddress(testModuleAccountName)},
		{"a bank-blocked account", blocked},
		// The destination here is perfectly admissible. It is still refused, which
		// is the point: this path is closed by ownership, not by address quality.
		{"an otherwise valid account", testAccount(31)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := k.SetPendingParams(ctx, paramsWithTreasury(tc.address, 100, 0))
			require.ErrorIs(t, err, types.ErrImmutableParam)
		})
	}

	// A pending update that leaves the migrated economics alone remains admissible:
	// the legacy surface is closed to reward economics, not retired.
	next := types.DefaultParams()
	next.TargetBlockTimeSeconds = types.DefaultTargetBlockTimeSeconds + 1
	require.NoError(t, k.SetPendingParams(ctx, next))
}

// --- epoch config snapshots ---------------------------------------------------

func TestSetCurrentEpochConfigRejectsInadmissibleTreasury(t *testing.T) {
	blocked := testAccount(77)
	k, ctx, _ := setupKeeperWithBlocked(t, &coreSlotKeeperMock{}, blocked)

	for _, address := range []string{testModuleAddress(testModuleAccountName), blocked} {
		snapshot := types.DefaultEpochConfigSnapshot(paramsWithTreasury(address, 100, 0))
		err := k.SetCurrentEpochConfig(ctx, snapshot)
		require.ErrorIsf(t, err, types.ErrInvalidAddress, "address %s", address)
	}

	// A snapshot with treasury disabled and no address remains storable.
	require.NoError(t, k.SetCurrentEpochConfig(ctx, types.DefaultEpochConfigSnapshot(types.DefaultParams())))
}

// --- claim records ------------------------------------------------------------

func reward(slotID, epoch uint64, operator, payout string) types.EligibleSlotReward {
	return types.EligibleSlotReward{
		SlotId:          slotID,
		EpochNumber:     epoch,
		OperatorAddress: operator,
		PayoutAddress:   payout,
		BlocksActive:    1,
		RewardWeight:    "1",
		EffectiveWeight: "1",
		Amount:          "1",
	}
}

// TestRewardRecordOperatorIsAnIdentityNotADestination is the rewards half of the
// operator/payout split. A bank-blocked address is legitimate as the persisted
// operator identity and inadmissible as the payout destination — the same
// address, two different answers, decided by which field it occupies.
func TestRewardRecordOperatorIsAnIdentityNotADestination(t *testing.T) {
	blocked := testAccount(77)

	t.Run("finalized epoch with a blocked embedded operator is accepted", func(t *testing.T) {
		k, ctx, _ := setupKeeperWithBlocked(t, &coreSlotKeeperMock{}, blocked)
		good := reward(1, 1, blocked, testAccount(10))
		config := types.DefaultEpochConfigSnapshot(types.DefaultParams())
		require.NoError(t, k.SetFinalizedEpoch(ctx, finalizedEpoch(1, &config, &good)))
	})

	t.Run("payout snapshot with a blocked operator is accepted", func(t *testing.T) {
		stored := coreslottypes.CoreSlot{
			SlotId:          1,
			OperatorAddress: blocked,
			PayoutAddress:   testAccount(10),
			Status:          coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE,
			RewardWeight:    coreslottypes.DefaultRewardWeight,
		}
		coreSlots := &coreSlotKeeperMock{
			active:  []coreslottypes.CoreSlot{stored},
			slots:   map[uint64]coreslottypes.CoreSlot{1: stored},
			weights: map[uint64]coreslottypes.OperatorRewardWeight{1: {SlotId: 1, FinalWeight: "1"}},
		}
		k, ctx, _ := setupKeeperWithBlocked(t, coreSlots, blocked)

		snapshots, err := k.GetActiveSlotSnapshots(ctx)
		require.NoError(t, err, "a bank-blocked operator identity must not block a payout snapshot")
		require.Len(t, snapshots, 1)
		require.Equal(t, testAccount(10), snapshots[0].PayoutAddress.String())
	})

	// A genesis-level case for the same split used to sit here, carrying a claim
	// record with a blocked operator. Fresh genesis now refuses closed-epoch state
	// of either representation, so no genesis document reaches the rule — and the
	// rule itself is unchanged, exercised above at the setter and below at the
	// snapshot, which are the two boundaries a conforming chain actually crosses.
}

// TestAllZeroTreasuryAddressRejected exercises §25's non-zero requirement
// through a real economic admission path rather than only the helper.
func TestAllZeroTreasuryAddressRejected(t *testing.T) {
	k, ctx, _ := setupKeeper(t, &coreSlotKeeperMock{})

	err := k.SetParams(ctx, paramsWithTreasury(zeroAddress(), 100, 0))
	require.ErrorIs(t, err, types.ErrInvalidAddress)
	require.Contains(t, err.Error(), "all zero")

	// And at transfer time.
	require.ErrorIs(t, k.PayTreasury(ctx, zeroAddress(), math.NewInt(10), "utwlt"), types.ErrInvalidAddress)
}

// --- finalized epochs ---------------------------------------------------------

func finalizedEpoch(epoch uint64, config *types.EpochConfigSnapshot, rewards ...*types.EligibleSlotReward) types.EpochReward {
	return types.EpochReward{
		EpochNumber:                 epoch,
		StartHeight:                 1,
		EndHeight:                   2,
		MintedEmission:              "0",
		CarryIn:                     "0",
		DistributableFees:           "0",
		TreasuryAmount:              "0",
		RewardPool:                  "0",
		AllocatedAmount:             "0",
		CarryOut:                    "0",
		CumulativeEmittedAfterEpoch: "0",
		Rewards:                     rewards,
		Config:                      config,
	}
}

func TestSetFinalizedEpochRejectsInadmissibleEmbeddedAddresses(t *testing.T) {
	blocked := testAccount(77)
	moduleAccount := testModuleAddress(testModuleAccountName)

	t.Run("embedded reward payout", func(t *testing.T) {
		k, ctx, _ := setupKeeperWithBlocked(t, &coreSlotKeeperMock{}, blocked)
		bad := reward(1, 1, testAccount(9), blocked)
		err := k.SetFinalizedEpoch(ctx, finalizedEpoch(1, nil, &bad))
		require.ErrorIs(t, err, types.ErrInvalidAddress)

		_, found, err := k.GetFinalizedEpoch(ctx, 1)
		require.NoError(t, err)
		require.False(t, found)
	})

	t.Run("embedded config treasury", func(t *testing.T) {
		k, ctx, _ := setupKeeper(t, &coreSlotKeeperMock{})
		config := types.DefaultEpochConfigSnapshot(paramsWithTreasury(moduleAccount, 100, 0))
		err := k.SetFinalizedEpoch(ctx, finalizedEpoch(1, &config))
		require.ErrorIs(t, err, types.ErrInvalidAddress)
	})

	t.Run("ordinary epoch is accepted", func(t *testing.T) {
		k, ctx, _ := setupKeeper(t, &coreSlotKeeperMock{})
		good := reward(1, 1, testAccount(9), testAccount(10))
		config := types.DefaultEpochConfigSnapshot(types.DefaultParams())
		require.NoError(t, k.SetFinalizedEpoch(ctx, finalizedEpoch(1, &config, &good)))
	})
}

// --- payout snapshot ----------------------------------------------------------

// TestSlotRewardSnapshotRejectsInadmissibleStoredAddresses covers the §25
// entitlement payout-snapshot boundary defensively: even though CoreSlot
// admission should already have guaranteed these values, the snapshot is where a
// stored address becomes a reward destination.
func TestSlotRewardSnapshotRejectsInadmissibleStoredAddresses(t *testing.T) {
	blocked := testAccount(77)
	moduleAccount := testModuleAddress(testModuleAccountName)

	for _, tc := range []struct {
		name     string
		operator string
		payout   string
	}{
		{"module-account payout", testAccount(9), moduleAccount},
		{"bank-blocked payout", testAccount(9), blocked},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stored := coreslottypes.CoreSlot{
				SlotId:          1,
				OperatorAddress: tc.operator,
				PayoutAddress:   tc.payout,
				Status:          coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE,
				RewardWeight:    coreslottypes.DefaultRewardWeight,
			}
			coreSlots := &coreSlotKeeperMock{
				active: []coreslottypes.CoreSlot{stored},
				slots:  map[uint64]coreslottypes.CoreSlot{1: stored},
				weights: map[uint64]coreslottypes.OperatorRewardWeight{
					1: {SlotId: 1, FinalWeight: "1"},
				},
			}
			k, ctx, _ := setupKeeperWithBlocked(t, coreSlots, blocked)

			_, err := k.GetActiveSlotSnapshots(ctx)
			require.ErrorIs(t, err, types.ErrInvalidAddress)
		})
	}
}

// --- treasury execution -------------------------------------------------------

func TestPayTreasuryRejectsInadmissibleRecipient(t *testing.T) {
	blocked := testAccount(77)

	for _, address := range []string{testModuleAddress(testModuleAccountName), blocked, "not-an-address", ""} {
		k, ctx, bank := setupKeeperWithBlocked(t, &coreSlotKeeperMock{}, blocked)

		err := k.PayTreasury(ctx, address, math.NewInt(10), "utwlt")
		require.ErrorIsf(t, err, types.ErrInvalidAddress, "address %q", address)
		require.Zerof(t, bank.sendCalls, "no transfer may be attempted for %q", address)
	}
}

// TestPayTreasuryZeroAmountKeepsExistingSemantics pins the early return. A zero
// treasury payment is a no-op and this change must not redefine it.
func TestPayTreasuryZeroAmountKeepsExistingSemantics(t *testing.T) {
	k, ctx, bank := setupKeeper(t, &coreSlotKeeperMock{})

	// Even an address that would be refused is not reached, because nothing is
	// transferred at all.
	require.NoError(t, k.PayTreasury(ctx, testModuleAddress(testModuleAccountName), math.ZeroInt(), "utwlt"))
	require.NoError(t, k.PayTreasury(ctx, "", math.ZeroInt(), "utwlt"))
	require.Zero(t, bank.sendCalls)
}

func TestPayTreasuryAcceptsOrdinaryRecipient(t *testing.T) {
	k, ctx, bank := setupKeeper(t, &coreSlotKeeperMock{})
	require.NoError(t, k.PayTreasury(ctx, testAccount(31), math.NewInt(10), "utwlt"))
	require.Equal(t, 1, bank.sendCalls)
}

// --- genesis ------------------------------------------------------------------

// genesisWith builds a STRUCTURALLY VALID fresh genesis and then applies one
// mutation.
//
// Structural validity matters here: types.ValidateGenesis runs first, so a
// fixture that is malformed for some unrelated reason would be rejected before
// the economic preflight and the test would pass for the wrong reason. The
// fixture is therefore the canonical fresh document, well-formed apart from the
// one address under test.
//
// It used to seed a finalized epoch. Fresh genesis now refuses closed-epoch
// state, so the seed would itself be the reason for rejection and every case
// built on it would prove nothing about addresses.
func genesisWith(t *testing.T, mutate func(*types.GenesisState)) types.GenesisState {
	t.Helper()
	genesis := *types.DefaultGenesis()
	mutate(&genesis)
	return genesis
}

// withTreasuryEverywhere points the canonical reward configuration and both
// deprecated mirrors at the same treasury destination and share.
//
// Fresh genesis requires the three to agree, so a fixture that wants to exercise
// the ADDRESS rule has to move them together; moving one produces a mirror
// mismatch and never reaches the destination check.
func withTreasuryEverywhere(g *types.GenesisState, address string, shareBps uint64) {
	params := paramsWithTreasury(address, shareBps, 0)
	g.Params = &params
	config := types.DefaultEpochConfigSnapshot(params)
	g.CurrentEpochConfig = &config
	rewardAnchor := types.DefaultRewardConfigVersion(params)
	g.RewardConfigVersions = []*types.RewardConfigVersion{&rewardAnchor}
}

func TestInitGenesisRejectsInadmissibleEconomicAddresses(t *testing.T) {
	blocked := testAccount(77)
	moduleAccount := testModuleAddress(testModuleAccountName)

	cases := []struct {
		name   string
		mutate func(*types.GenesisState)
	}{
		// The economic mirrors are moved together with the canonical reward
		// configuration. Setting a bad treasury on one alone would now be rejected by
		// the genesis mirror-pinning rule before the address rule was ever consulted,
		// which would leave these cases silently proving the wrong thing.
		{"params treasury", func(g *types.GenesisState) {
			withTreasuryEverywhere(g, moduleAccount, 100)
		}},
		{"current epoch config treasury", func(g *types.GenesisState) {
			withTreasuryEverywhere(g, blocked, 100)
		}},
		{"reward configuration treasury", func(g *types.GenesisState) {
			withTreasuryEverywhere(g, blocked, 250)
		}},
		// Three collections are deliberately absent from this table, all for the
		// same reason: fresh genesis rejects a non-empty schedule, a non-empty
		// finalized-epoch archive and a non-empty claim collection outright, as
		// content rules, so no genesis document can reach the destination check for
		// any of them. Each rule is exercised where it IS reachable — the schedule at
		// promotion, in TestScheduledRewardConfigPromotionHoldsTheDestinationRule,
		// and the closed-epoch representation at its setter, in
		// TestSetFinalizedEpochRejectsInadmissibleEmbeddedAddresses.
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx, _ := setupKeeperWithBlocked(t, &coreSlotKeeperMock{}, blocked)
			err := k.InitGenesis(ctx, genesisWith(t, tc.mutate))
			require.ErrorIs(t, err, types.ErrInvalidAddress)
		})
	}
}

func TestInitGenesisAcceptsDefaultAndOrdinaryState(t *testing.T) {
	k, ctx, _ := setupKeeper(t, &coreSlotKeeperMock{})

	// The default genesis carries no treasury address and zero shares.
	require.NoError(t, k.InitGenesis(ctx, *types.DefaultGenesis()))

	stored, err := k.GetParams(ctx)
	require.NoError(t, err)
	require.Empty(t, stored.TreasuryAddress)
}

// TestInitGenesisRejectsBeforeAnyWrite is the preflight property: rejection is
// TOTAL, leaving no partially initialized module behind.
//
// The address under test is on the reward configuration, which the preflight
// reaches after params. Params is written first by the import, so a preflight that
// merely validated each record as it wrote would leave params persisted and the
// module half-initialized behind a returned error.
//
// This used to make the point with two claim records, an earlier valid one and a
// later invalid one. Fresh genesis now carries no repeated address-bearing
// collection at all — the reward history holds exactly one anchor and every
// closed-epoch collection is empty — so the multi-record form is no longer
// expressible, and the property it existed to prove is stated directly instead.
func TestInitGenesisRejectsBeforeAnyWrite(t *testing.T) {
	k, ctx, _ := setupKeeperWithBlocked(t, &coreSlotKeeperMock{}, testAccount(77))

	genesis := genesisWith(t, func(g *types.GenesisState) {
		withTreasuryEverywhere(g, testAccount(77), 100)
	})

	err := k.InitGenesis(ctx, genesis)
	require.ErrorIs(t, err, types.ErrInvalidAddress)

	_, err = k.GetParams(ctx)
	require.Error(t, err, "params must not survive a rejected genesis")

	_, err = k.GetState(ctx)
	require.Error(t, err, "rewards state must not survive a rejected genesis")

	_, err = k.GenesisRewardConfigVersion(ctx)
	require.Error(t, err, "no reward configuration version may survive a rejected genesis")
}
