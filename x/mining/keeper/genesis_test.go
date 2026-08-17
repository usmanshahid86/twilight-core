package keeper_test

import (
	"context"
	"fmt"
	"testing"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"
	"cosmossdk.io/log"
	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"

	addresscodec "github.com/cosmos/cosmos-sdk/codec/address"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil/integration"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/internal/economicaddress"
	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	"github.com/twilight-project/twilight-core/x/mining/keeper"
	"github.com/twilight-project/twilight-core/x/mining/types"
	rewardstypes "github.com/twilight-project/twilight-core/x/rewards/types"
)

// coreSlotKeeperMock supplies the CoreSlot state the mining genesis cross-check
// reads. Only the reads x/mining actually holds are implemented, which is the
// point of the narrow interface: a mock that had to grow a bank method would be
// telling us the dependency boundary had moved.
type coreSlotKeeperMock struct {
	active    []coreslottypes.CoreSlot
	policies  map[uint64]coreslottypes.SelectionPolicyVersion
	policyErr error
}

func (m *coreSlotKeeperMock) GetSlot(_ context.Context, slotID uint64) (coreslottypes.CoreSlot, error) {
	for _, slot := range m.active {
		if slot.SlotId == slotID {
			return slot, nil
		}
	}
	return coreslottypes.CoreSlot{}, coreslottypes.ErrSlotNotFound
}

func (m *coreSlotKeeperMock) GetActiveSlots(context.Context) ([]coreslottypes.CoreSlot, error) {
	return append([]coreslottypes.CoreSlot(nil), m.active...), nil
}

func (m *coreSlotKeeperMock) SelectionPolicyAtHeight(
	_ context.Context, slotID uint64, _ int64,
) (coreslottypes.SelectionPolicyVersion, error) {
	if m.policyErr != nil {
		return coreslottypes.SelectionPolicyVersion{}, m.policyErr
	}
	policy, ok := m.policies[slotID]
	if !ok {
		return coreslottypes.SelectionPolicyVersion{}, coreslottypes.ErrSlotNotFound
	}
	return policy, nil
}

// rewardsKeeperMock stands in for the only module x/mining reads money state from.
//
// Every method that moves value is present and records that it was called, because
// the most important assertions in this gate are about calls that must NOT happen:
// materialization performs no bank operation, and proving that means proving the
// release methods were never reached, which a balance comparison cannot show.
type rewardsKeeperMock struct {
	releaseEnabled bool
	finalized      map[uint64]bool
	entitlements   map[uint64][]rewardstypes.SlotEntitlement
	epochLength    uint64

	payCalls       int
	remainderCalls int

	// payErr makes the release boundary refuse. It models the two ways the real
	// boundary can reject a set x/mining has already admitted: its own entitlement
	// ceiling, and a bank send that fails partway through the set.
	payErr error
}

func newRewardsMock() *rewardsKeeperMock {
	return &rewardsKeeperMock{
		releaseEnabled: true,
		finalized:      map[uint64]bool{},
		entitlements:   map[uint64][]rewardstypes.SlotEntitlement{},
		epochLength:    360,
	}
}

// finalize marks an epoch closed and supplies the entitlements it produced.
func (m *rewardsKeeperMock) finalize(epoch uint64, entitlements ...rewardstypes.SlotEntitlement) {
	m.finalized[epoch] = true
	m.entitlements[epoch] = entitlements
}

func (m *rewardsKeeperMock) GetFinalizedEpoch(_ context.Context, epoch uint64) (rewardstypes.EpochReward, bool, error) {
	if !m.finalized[epoch] {
		return rewardstypes.EpochReward{}, false, nil
	}
	return rewardstypes.EpochReward{EpochNumber: epoch}, true, nil
}

func (m *rewardsKeeperMock) IterateEntitlementsForEpoch(_ context.Context, epoch uint64) ([]rewardstypes.SlotEntitlement, error) {
	return append([]rewardstypes.SlotEntitlement(nil), m.entitlements[epoch]...), nil
}

func (m *rewardsKeeperMock) GetSlotEntitlement(_ context.Context, slotID, epoch uint64) (rewardstypes.SlotEntitlement, bool, error) {
	for _, entitlement := range m.entitlements[epoch] {
		if entitlement.SlotId == slotID {
			return entitlement, true, nil
		}
	}
	return rewardstypes.SlotEntitlement{}, false, nil
}

func (m *rewardsKeeperMock) EpochStartHeight(_ context.Context, epoch uint64) (uint64, error) {
	return (epoch-1)*m.epochLength + 1, nil
}

func (m *rewardsKeeperMock) EpochEndHeight(_ context.Context, epoch uint64) (uint64, error) {
	return epoch * m.epochLength, nil
}

func (m *rewardsKeeperMock) EpochLengthForEpoch(_ context.Context, _ uint64) (uint64, error) {
	return m.epochLength, nil
}

func (m *rewardsKeeperMock) SettlementReleaseEnabled(context.Context) (bool, error) {
	return m.releaseEnabled, nil
}

// PayEntitlement accumulates the released amount on the entitlement, so a later
// chunk sees what an earlier one released.
//
// It deliberately does NOT re-implement the rewards-side entitlement ceiling.
// That ceiling belongs to x/rewards and is proven against the real implementation
// by TestPayEntitlementRefusesOverRelease; enforcing it here as well would mask
// the removal of x/mining's own ceiling, which is the thing these tests exist to
// pin. Use payErr to model the boundary refusing.
func (m *rewardsKeeperMock) PayEntitlement(
	_ context.Context, slotID, epoch uint64, payouts []rewardstypes.EntitlementPayout,
) error {
	m.payCalls++
	if m.payErr != nil {
		return m.payErr
	}
	total := sdkmath.ZeroInt()
	for _, payout := range payouts {
		amount, ok := sdkmath.NewIntFromString(payout.Amount)
		if !ok {
			return fmt.Errorf("payout amount %q is not an integer", payout.Amount)
		}
		total = total.Add(amount)
	}
	return m.addReleased(slotID, epoch, total)
}

// addReleased raises the entitlement-side released amount, which is the
// authoritative one: x/mining stores no released amount of its own.
func (m *rewardsKeeperMock) addReleased(slotID, epoch uint64, amount sdkmath.Int) error {
	list := m.entitlements[epoch]
	for i := range list {
		if list[i].SlotId != slotID {
			continue
		}
		released, ok := sdkmath.NewIntFromString(list[i].ReleasedAmount)
		if !ok {
			return fmt.Errorf("released amount %q is not an integer", list[i].ReleasedAmount)
		}
		list[i].ReleasedAmount = released.Add(amount).String()
		return nil
	}
	return fmt.Errorf("no entitlement for slot %d in epoch %d", slotID, epoch)
}

func (m *rewardsKeeperMock) PayEntitlementRemainderToOperator(context.Context, uint64, uint64) error {
	m.remainderCalls++
	return nil
}

// settlementSlot is an admitted CoreSlot carrying the settlement credential that
// authorizes its chunks. The lifecycle status is ACTIVE only because a fixture
// needs some status; no chunk path reads it.
func settlementSlot(slotID uint64, settlementAddress string) coreslottypes.CoreSlot {
	slot := activeSlot(slotID)
	slot.SettlementAddress = settlementAddress
	return slot
}

// account builds a distinct, canonically encoded account address. The leading
// byte orders them, which is what the strictly-ascending recipient rule is
// measured against.
func account(marker byte) string {
	raw := make([]byte, 20)
	raw[0] = marker
	return sdk.AccAddress(raw).String()
}

// fixtureModuleAccount is the one module-account name the test economic-address
// rule knows about. A validator that knew of no module accounts could not refuse a
// payout to one, so the fixtures declare exactly one and the chunk tests use its
// address to prove the refusal.
const fixtureModuleAccount = "mining_test_module_account"

func setupKeeper(t *testing.T, core keeper.CoreSlotKeeper) (keeper.Keeper, sdk.Context) {
	k, ctx, _ := setupKeeperWithRewards(t, core, newRewardsMock())
	return k, ctx
}

func setupKeeperWithRewards(
	t *testing.T, core keeper.CoreSlotKeeper, rewards *rewardsKeeperMock,
) (keeper.Keeper, sdk.Context, *rewardsKeeperMock) {
	t.Helper()
	registry := codectypes.NewInterfaceRegistry()
	types.RegisterInterfaces(registry)
	keys := storetypes.NewKVStoreKeys(types.StoreKey)
	cms := integration.CreateMultiStore(keys, log.NewNopLogger())
	ctx := sdk.NewContext(cms, cmtproto.Header{Height: 1}, false, log.NewNopLogger())

	// The canonical §25 rule, built exactly as the app builds it. It needs at
	// least one module account name: a validator that knew of no module accounts
	// could not refuse a payout to one.
	validator, err := economicaddress.New(
		addresscodec.NewBech32Codec(sdk.GetConfig().GetBech32AccountAddrPrefix()),
		[]string{fixtureModuleAccount},
		nil,
	)
	require.NoError(t, err)

	k := keeper.NewKeeper(
		codec.NewProtoCodec(registry),
		runtime.NewKVStoreService(keys[types.StoreKey]),
		core,
		rewards,
		validator,
	)
	return k, ctx, rewards
}

func activeSlot(slotID uint64) coreslottypes.CoreSlot {
	return coreslottypes.CoreSlot{SlotId: slotID, Status: coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE}
}

func policy(slotID, rateBps, maxSelected uint64) coreslottypes.SelectionPolicyVersion {
	return coreslottypes.SelectionPolicyVersion{
		SlotId: slotID, PolicyVersion: 1,
		SelectionRateBps: rateBps, MaxSelectedParticipants: maxSelected,
		ValidFromHeight: 1,
	}
}

func TestDefaultGenesisInitExportRoundTrip(t *testing.T) {
	k, ctx := setupKeeper(t, &coreSlotKeeperMock{})
	genesis := types.DefaultGenesis()
	require.NoError(t, k.InitGenesis(ctx, *genesis))

	exported, err := k.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Equal(t, genesis, exported)
}

// TestGenesisWritesTheClockAndCursorExplicitly proves both are written rather than
// defaulted.
//
// After genesis an absent value is corruption, and no read path may invent one: a
// settlement clock that defaulted to zero on a running chain would reopen every
// expired deadline at once.
func TestGenesisWritesTheClockAndCursorExplicitly(t *testing.T) {
	k, ctx := setupKeeper(t, &coreSlotKeeperMock{})
	require.NoError(t, k.InitGenesis(ctx, *types.DefaultGenesis()))

	clock, err := k.SettlementClock.Get(ctx)
	require.NoError(t, err)
	require.Zero(t, clock)

	cursor, err := k.LastProcessedRewardEpoch.Get(ctx)
	require.NoError(t, err)
	require.Zero(t, cursor)
}

// TestGenesisRejectsActivePolicyAboveInitialSelectionParams is the cross-module
// admission rule this module owns.
//
// It runs even though nothing in this profile executes Selection. A genesis that
// admitted a policy exceeding the parameters would produce Slots that become
// unusable the moment Selection is switched on, and finding that at the switch is
// finding it far too late.
func TestGenesisRejectsActivePolicyAboveInitialSelectionParams(t *testing.T) {
	initial := types.DefaultGenesis().SelectionParamsVersions[0]

	t.Run("a conforming policy is admitted", func(t *testing.T) {
		core := &coreSlotKeeperMock{
			active: []coreslottypes.CoreSlot{activeSlot(1)},
			policies: map[uint64]coreslottypes.SelectionPolicyVersion{
				1: policy(1, initial.MaxSelectionRateBps, initial.MaxSelectedParticipantsPerSelection),
			},
		}
		k, ctx := setupKeeper(t, core)
		require.NoError(t, k.InitGenesis(ctx, *types.DefaultGenesis()))
	})

	t.Run("a selection rate above the initial ceiling", func(t *testing.T) {
		core := &coreSlotKeeperMock{
			active: []coreslottypes.CoreSlot{activeSlot(1)},
			policies: map[uint64]coreslottypes.SelectionPolicyVersion{
				1: policy(1, initial.MaxSelectionRateBps+1, initial.MaxSelectedParticipantsPerSelection),
			},
		}
		k, ctx := setupKeeper(t, core)
		err := k.InitGenesis(ctx, *types.DefaultGenesis())
		require.ErrorIs(t, err, types.ErrInvalidGenesis)
		require.Contains(t, err.Error(), "selection policy")
	})

	t.Run("more selected participants than the initial maximum", func(t *testing.T) {
		core := &coreSlotKeeperMock{
			active: []coreslottypes.CoreSlot{activeSlot(1)},
			policies: map[uint64]coreslottypes.SelectionPolicyVersion{
				1: policy(1, initial.MaxSelectionRateBps, initial.MaxSelectedParticipantsPerSelection+1),
			},
		}
		k, ctx := setupKeeper(t, core)
		err := k.InitGenesis(ctx, *types.DefaultGenesis())
		require.ErrorIs(t, err, types.ErrInvalidGenesis)
		require.Contains(t, err.Error(), "participants")
	})

	t.Run("an unreadable policy fails closed", func(t *testing.T) {
		core := &coreSlotKeeperMock{
			active:    []coreslottypes.CoreSlot{activeSlot(1)},
			policyErr: coreslottypes.ErrSlotNotFound,
		}
		k, ctx := setupKeeper(t, core)
		require.ErrorIs(t, k.InitGenesis(ctx, *types.DefaultGenesis()), types.ErrInvalidGenesis)
	})
}

// TestGenesisRejectionIsTotal is the preflight property: a document refused for a
// reason discovered through cross-module state must leave nothing behind.
//
// The cross-module check runs before the first write precisely so this holds. A
// check interleaved with the writes would persist the mode history before
// discovering the policy violation, leaving a partially imported module behind a
// returned error.
func TestGenesisRejectionIsTotal(t *testing.T) {
	initial := types.DefaultGenesis().SelectionParamsVersions[0]
	core := &coreSlotKeeperMock{
		active: []coreslottypes.CoreSlot{activeSlot(1)},
		policies: map[uint64]coreslottypes.SelectionPolicyVersion{
			1: policy(1, initial.MaxSelectionRateBps+1, initial.MaxSelectedParticipantsPerSelection),
		},
	}
	k, ctx := setupKeeper(t, core)
	require.Error(t, k.InitGenesis(ctx, *types.DefaultGenesis()))

	_, err := k.SettlementClock.Get(ctx)
	require.Error(t, err, "the settlement clock must not survive a rejected genesis")
	_, err = k.LastProcessedRewardEpoch.Get(ctx)
	require.Error(t, err, "the cursor must not survive a rejected genesis")
	_, err = k.DistributionModeVersions.Get(ctx, 1)
	require.Error(t, err, "no history row may survive a rejected genesis")
}

// TestOpenIndexIsRebuiltRatherThanImported proves the derived index has no genesis
// field and is reconstructed from the canonical rows.
//
// That is what makes it impossible for a document to ship an index disagreeing
// with its own settlements: there is no second value to disagree with. At fresh
// genesis the settlement collection is empty, so the index is empty too.
func TestOpenIndexIsRebuiltRatherThanImported(t *testing.T) {
	k, ctx := setupKeeper(t, &coreSlotKeeperMock{})
	require.NoError(t, k.InitGenesis(ctx, *types.DefaultGenesis()))

	count := 0
	require.NoError(t, k.OpenSettlementsBySlot.Walk(ctx, nil, func(_ collections.Pair[uint64, uint64], _ uint64) (bool, error) {
		count++
		return false, nil
	}))
	require.Zero(t, count, "a fresh chain has materialized nothing to index")
}
