package keeper_test

import (
	"context"
	"testing"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"
	"cosmossdk.io/log"
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

// rewardsKeeperMock is deliberately inert. This gate consumes none of the rewards
// reads; the interface exists so the dependency is declared where review can see
// it, and every method panics so a premature caller is loud rather than silent.
type rewardsKeeperMock struct{}

func (rewardsKeeperMock) GetFinalizedEpoch(context.Context, uint64) (rewardstypes.EpochReward, bool, error) {
	panic("mining does not read finalized epochs in this gate")
}

func (rewardsKeeperMock) IterateEntitlementsForEpoch(context.Context, uint64) ([]rewardstypes.SlotEntitlement, error) {
	panic("mining does not enumerate entitlements in this gate")
}

func (rewardsKeeperMock) GetSlotEntitlement(context.Context, uint64, uint64) (rewardstypes.SlotEntitlement, bool, error) {
	panic("mining does not read entitlements in this gate")
}

func (rewardsKeeperMock) EpochStartHeight(context.Context, uint64) (uint64, error) {
	panic("mining does not derive epoch geometry in this gate")
}

func (rewardsKeeperMock) EpochEndHeight(context.Context, uint64) (uint64, error) {
	panic("mining does not derive epoch geometry in this gate")
}

func (rewardsKeeperMock) EpochLengthForEpoch(context.Context, uint64) (uint64, error) {
	panic("mining does not derive epoch geometry in this gate")
}

func (rewardsKeeperMock) SettlementReleaseEnabled(context.Context) (bool, error) {
	panic("mining does not read the pause state in this gate")
}

func (rewardsKeeperMock) PayEntitlement(context.Context, uint64, uint64, []rewardstypes.EntitlementPayout) error {
	panic("mining moves no value in this gate")
}

func (rewardsKeeperMock) PayEntitlementRemainderToOperator(context.Context, uint64, uint64) error {
	panic("mining moves no value in this gate")
}

func setupKeeper(t *testing.T, core keeper.CoreSlotKeeper) (keeper.Keeper, sdk.Context) {
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
		[]string{"mining_test_module_account"},
		nil,
	)
	require.NoError(t, err)

	k := keeper.NewKeeper(
		codec.NewProtoCodec(registry),
		runtime.NewKVStoreService(keys[types.StoreKey]),
		core,
		rewardsKeeperMock{},
		validator,
	)
	return k, ctx
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

// TestMiningEndBlockIsAConsensusNoOpInThisGate pins that the hook is wired and
// does nothing yet, so a later gate adds behavior to a live seam rather than
// discovering the module was never end-blocked.
func TestMiningEndBlockIsAConsensusNoOpInThisGate(t *testing.T) {
	k, ctx := setupKeeper(t, &coreSlotKeeperMock{})
	require.NoError(t, k.InitGenesis(ctx, *types.DefaultGenesis()))
	require.NoError(t, k.EndBlock(ctx))

	clock, err := k.SettlementClock.Get(ctx)
	require.NoError(t, err)
	require.Zero(t, clock, "the clock does not advance until the settlement gate lands")
}
