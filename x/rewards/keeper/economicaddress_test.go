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

func TestSetPendingParamsAppliesTheSameTreasuryPolicy(t *testing.T) {
	blocked := testAccount(77)
	k, ctx, _ := setupKeeperWithBlocked(t, &coreSlotKeeperMock{}, blocked)
	require.NoError(t, k.SetParams(ctx, types.DefaultParams()))

	err := k.SetPendingParams(ctx, paramsWithTreasury(testModuleAddress(testModuleAccountName), 100, 0))
	require.ErrorIs(t, err, types.ErrInvalidAddress)

	err = k.SetPendingParams(ctx, paramsWithTreasury(blocked, 100, 0))
	require.ErrorIs(t, err, types.ErrInvalidAddress)

	// Disabled treasury with no address stays admissible as a pending update.
	require.NoError(t, k.SetPendingParams(ctx, types.DefaultParams()))
	// And so does a valid one with a positive share.
	require.NoError(t, k.SetPendingParams(ctx, paramsWithTreasury(testAccount(31), 100, 0)))
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

func TestSetClaimRecordRejectsInadmissibleAddresses(t *testing.T) {
	blocked := testAccount(77)
	good := testAccount(9)
	moduleAccount := testModuleAddress(testModuleAccountName)

	cases := []struct {
		name     string
		operator string
		payout   string
	}{
		{"module-account operator", moduleAccount, good},
		{"module-account payout", good, moduleAccount},
		{"bank-blocked payout", good, blocked},
		{"bank-blocked operator", blocked, good},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx, _ := setupKeeperWithBlocked(t, &coreSlotKeeperMock{}, blocked)

			err := k.SetClaimRecord(ctx, reward(1, 1, tc.operator, tc.payout))
			require.ErrorIs(t, err, types.ErrInvalidAddress)

			_, found, err := k.GetClaimRecord(ctx, 1, 1)
			require.NoError(t, err)
			require.False(t, found, "a rejected claim record must not be persisted")
		})
	}
}

func TestSetClaimRecordAcceptsOrdinaryAddresses(t *testing.T) {
	k, ctx, _ := setupKeeper(t, &coreSlotKeeperMock{})
	require.NoError(t, k.SetClaimRecord(ctx, reward(1, 1, testAccount(9), testAccount(10))))

	stored, found, err := k.GetClaimRecord(ctx, 1, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, testAccount(10), stored.PayoutAddress)
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
		{"module-account operator", moduleAccount, testAccount(10)},
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

// genesisWith builds a STRUCTURALLY VALID genesis and then applies one mutation.
//
// Structural validity matters here: types.ValidateGenesis runs first, so a
// fixture that is malformed for some unrelated reason would be rejected before
// the economic preflight and the test would pass for the wrong reason. Every
// case below is therefore well-formed apart from the one address under test —
// a claim record has its finalized epoch, and every finalized epoch has its
// config.
func genesisWith(t *testing.T, mutate func(*types.GenesisState)) types.GenesisState {
	t.Helper()
	genesis := *types.DefaultGenesis()
	config := types.DefaultEpochConfigSnapshot(types.DefaultParams())
	epoch := finalizedEpoch(1, &config)
	genesis.FinalizedEpochs = []*types.EpochReward{&epoch}
	mutate(&genesis)
	return genesis
}

func TestInitGenesisRejectsInadmissibleEconomicAddresses(t *testing.T) {
	blocked := testAccount(77)
	moduleAccount := testModuleAddress(testModuleAccountName)

	cases := []struct {
		name   string
		mutate func(*types.GenesisState)
	}{
		{"params treasury", func(g *types.GenesisState) {
			params := paramsWithTreasury(moduleAccount, 100, 0)
			g.Params = &params
			config := types.DefaultEpochConfigSnapshot(params)
			g.CurrentEpochConfig = &config
		}},
		{"current epoch config treasury", func(g *types.GenesisState) {
			config := types.DefaultEpochConfigSnapshot(paramsWithTreasury(blocked, 100, 0))
			g.CurrentEpochConfig = &config
		}},
		{"claim record payout", func(g *types.GenesisState) {
			record := reward(1, 1, testAccount(9), blocked)
			g.ClaimRecords = []*types.EligibleSlotReward{&record}
		}},
		{"finalized epoch reward operator", func(g *types.GenesisState) {
			bad := reward(1, 1, moduleAccount, testAccount(10))
			config := types.DefaultEpochConfigSnapshot(types.DefaultParams())
			epoch := finalizedEpoch(1, &config, &bad)
			g.FinalizedEpochs = []*types.EpochReward{&epoch}
		}},
		{"finalized epoch config treasury", func(g *types.GenesisState) {
			config := types.DefaultEpochConfigSnapshot(paramsWithTreasury(blocked, 100, 0))
			epoch := finalizedEpoch(1, &config)
			g.FinalizedEpochs = []*types.EpochReward{&epoch}
		}},
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

// TestInitGenesisRejectsBeforeAnyWrite is the preflight property: an invalid
// address in the LAST record must leave no params and no earlier record behind.
func TestInitGenesisRejectsBeforeAnyWrite(t *testing.T) {
	k, ctx, _ := setupKeeper(t, &coreSlotKeeperMock{})

	genesis := genesisWith(t, func(g *types.GenesisState) {
		first := reward(1, 1, testAccount(9), testAccount(10))
		last := reward(2, 1, testAccount(9), testModuleAddress(testModuleAccountName))
		g.ClaimRecords = []*types.EligibleSlotReward{&first, &last}
	})

	err := k.InitGenesis(ctx, genesis)
	require.ErrorIs(t, err, types.ErrInvalidAddress)

	// Neither params nor the earlier, valid claim record was persisted.
	_, err = k.GetParams(ctx)
	require.Error(t, err, "params must not survive a rejected genesis")

	_, found, err := k.GetClaimRecord(ctx, 1, 1)
	require.NoError(t, err)
	require.False(t, found, "an earlier valid claim record must not survive a rejected genesis")
}
