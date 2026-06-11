package keeper_test

import (
	"testing"

	"cosmossdk.io/collections"
	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	"github.com/twilight-project/twilight-core/x/rewards/keeper"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

func TestEndBlockFinalizesAndAllocatesEpoch(t *testing.T) {
	k, ctx, bank, _ := setupFinalization(t, false)
	ctx = ctx.WithBlockHeight(2)

	require.NoError(t, k.EndBlock(ctx))
	require.Equal(t, 1, bank.mintCalls)
	require.Equal(t, "20utwlt", bank.minted.String())

	epoch, found, err := k.GetFinalizedEpoch(ctx, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "20", epoch.MintedEmission)
	require.Equal(t, "20", epoch.RewardPool)
	require.Equal(t, "20", epoch.AllocatedAmount)
	require.Equal(t, "0", epoch.CarryOut)
	require.Len(t, epoch.Rewards, 2)
	require.Equal(t, []string{"5", "15"}, []string{epoch.Rewards[0].Amount, epoch.Rewards[1].Amount})
	require.True(t, hasEvent(ctx, types.EventTypeEpochFinalized))

	state, err := k.GetState(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), state.CurrentEpoch)
	require.Equal(t, uint64(3), state.CurrentEpochStartHeight)
	require.Equal(t, "20", state.CumulativeEmitted)
	_, err = k.GetActiveBlocks(ctx, 1, 1)
	require.ErrorIs(t, err, collections.ErrNotFound)
}

func TestFinalizeRejectsUnsupportedSnapshotModes(t *testing.T) {
	k, ctx, _, _ := setupFinalization(t, true)
	cfg, err := k.GetCurrentEpochConfig(ctx)
	require.NoError(t, err)
	cfg.WeightedRewardsEnabled = true
	require.NoError(t, k.CurrentEpochConfig.Set(ctx, cfg))

	require.ErrorIs(t, k.EndBlock(ctx.WithBlockHeight(2)), types.ErrUnsupportedFeature)
	_, found, err := k.GetFinalizedEpoch(ctx, 1)
	require.NoError(t, err)
	require.False(t, found)
}

func TestEndBlockReadinessAndSettlementPause(t *testing.T) {
	k, ctx, bank, _ := setupFinalization(t, false)
	require.NoError(t, k.EndBlock(ctx.WithBlockHeight(1)))
	require.Zero(t, bank.mintCalls)

	params, err := k.GetParams(ctx)
	require.NoError(t, err)
	params.EpochSettlementEnabled = false
	require.NoError(t, k.SetParams(ctx, params))
	require.NoError(t, k.EndBlock(ctx.WithBlockHeight(5)))
	require.Zero(t, bank.mintCalls)

	params.EpochSettlementEnabled = true
	require.NoError(t, k.SetParams(ctx, params))
	require.NoError(t, k.EndBlock(ctx.WithBlockHeight(5)))
	require.Equal(t, 1, bank.mintCalls)
	state, err := k.GetState(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(3), state.CurrentEpochStartHeight)
}

func TestFinalizeEmptyActiveSetMintsAndCarries(t *testing.T) {
	k, ctx, bank, _ := setupFinalization(t, true)
	require.NoError(t, k.EndBlock(ctx.WithBlockHeight(2)))
	require.Equal(t, "20utwlt", bank.minted.String())

	state, err := k.GetState(ctx)
	require.NoError(t, err)
	require.Equal(t, "20", state.CumulativeEmitted)
	require.Equal(t, "20", state.CarryForwardRemainder)
	epoch, found, err := k.GetFinalizedEpoch(ctx, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.Empty(t, epoch.Rewards)
}

func TestFinalizeEmissionsDisabledAndMintFailureAtomicity(t *testing.T) {
	t.Run("emissions disabled", func(t *testing.T) {
		k, ctx, bank, _ := setupFinalization(t, true)
		params, err := k.GetParams(ctx)
		require.NoError(t, err)
		params.EmissionsEnabled = false
		require.NoError(t, k.SetParams(ctx, params))
		require.NoError(t, k.EndBlock(ctx.WithBlockHeight(2)))
		require.Zero(t, bank.mintCalls)
		state, err := k.GetState(ctx)
		require.NoError(t, err)
		require.Equal(t, "0", state.CumulativeEmitted)
	})

	t.Run("mint failure", func(t *testing.T) {
		k, ctx, bank, _ := setupFinalization(t, false)
		bank.failMint()
		require.Error(t, k.EndBlock(ctx.WithBlockHeight(2)))
		_, found, err := k.GetFinalizedEpoch(ctx, 1)
		require.NoError(t, err)
		require.False(t, found)
		state, err := k.GetState(ctx)
		require.NoError(t, err)
		require.Equal(t, uint64(1), state.CurrentEpoch)
	})
}

func TestFinalizeActivatesPendingParamsAfterBoundary(t *testing.T) {
	k, ctx, _, _ := setupFinalization(t, true)
	current, err := k.GetParams(ctx)
	require.NoError(t, err)
	pending := current
	pending.EpochLengthBlocks = 7
	pending.InitialBlockSubsidy = "3"
	require.NoError(t, k.SetPendingParams(ctx, pending))

	require.NoError(t, k.EndBlock(ctx.WithBlockHeight(2)))
	active, err := k.GetParams(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(7), active.EpochLengthBlocks)
	cfg, err := k.GetCurrentEpochConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(7), cfg.EpochLengthBlocks)
	_, found, err := k.GetPendingParams(ctx)
	require.NoError(t, err)
	require.False(t, found)
}

func TestPendingActivationFailureDoesNotHalfCommitFinalization(t *testing.T) {
	k, ctx, _, _ := setupFinalization(t, true)
	invalid := types.DefaultParams()
	invalid.EpochLengthBlocks = 0
	require.NoError(t, k.PendingParams.Set(ctx, invalid))

	require.Error(t, k.EndBlock(ctx.WithBlockHeight(2)))
	_, found, err := k.GetFinalizedEpoch(ctx, 1)
	require.NoError(t, err)
	require.False(t, found)
	state, err := k.GetState(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), state.CurrentEpoch)
}

func TestFinalizeClipsEmissionAndPreservesTerminalDust(t *testing.T) {
	t.Run("threshold clipping", func(t *testing.T) {
		k, ctx, bank, _ := setupFinalization(t, true)
		state, err := k.GetState(ctx)
		require.NoError(t, err)
		state.CumulativeEmitted = "499"
		require.NoError(t, k.SetState(ctx, state))

		require.NoError(t, k.EndBlock(ctx.WithBlockHeight(2)))
		require.Equal(t, "6utwlt", bank.minted.String())
		state, err = k.GetState(ctx)
		require.NoError(t, err)
		require.Equal(t, "505", state.CumulativeEmitted)
	})

	t.Run("terminal zero subsidy", func(t *testing.T) {
		k, ctx, bank, _ := setupFinalization(t, true)
		state, err := k.GetState(ctx)
		require.NoError(t, err)
		state.CumulativeEmitted = "999"
		require.NoError(t, k.SetState(ctx, state))

		require.NoError(t, k.EndBlock(ctx.WithBlockHeight(2)))
		require.Zero(t, bank.mintCalls)
		state, err = k.GetState(ctx)
		require.NoError(t, err)
		require.Equal(t, "999", state.CumulativeEmitted)
	})
}

func setupFinalization(t *testing.T, empty bool) (keeper.Keeper, sdk.Context, *bankKeeperMock, *coreSlotKeeperMock) {
	t.Helper()
	params := types.DefaultParams()
	params.MaxSupply = "1000"
	params.InitialBlockSubsidy = "10"
	params.EpochLengthBlocks = 2
	core := &coreSlotKeeperMock{
		slots: map[uint64]coreslottypes.CoreSlot{
			1: {SlotId: 1, OperatorAddress: addr(1), PayoutAddress: addr(2), Status: coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE, ConsensusPower: 999},
			2: {SlotId: 2, OperatorAddress: addr(3), PayoutAddress: addr(4), Status: coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE, ConsensusPower: 1},
		},
		weights: map[uint64]coreslottypes.OperatorRewardWeight{
			1: {SlotId: 1, FinalWeight: "100.000000000000000000"},
			2: {SlotId: 2, FinalWeight: "1.000000000000000000"},
		},
	}
	k, ctx, bank := setupAccountingKeeper(t, core, 1, params)
	if !empty {
		require.NoError(t, k.SetActiveBlocks(ctx, 1, 1, 1))
		require.NoError(t, k.SetActiveBlocks(ctx, 1, 2, 3))
	}
	return k, ctx, bank, core
}
