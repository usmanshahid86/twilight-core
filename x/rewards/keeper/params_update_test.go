package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/twilight-project/twilight-core/x/rewards/keeper"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

func TestMsgUpdateRewardsParamsQueuesAuthorityUpdate(t *testing.T) {
	core := &coreSlotKeeperMock{authority: addr(1), emergency: addr(2)}
	k, ctx, _ := setupAccountingKeeper(t, core, 1, types.DefaultParams())
	server := keeper.NewMsgServer(k)
	next := types.DefaultParams()
	next.EpochLengthBlocks = 99
	next.InitialBlockSubsidy = "123"

	_, err := server.UpdateRewardsParams(ctx, &types.MsgUpdateRewardsParams{Authority: core.authority, Params: &next})
	require.NoError(t, err)
	pending, found, err := k.GetPendingParams(ctx)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(99), pending.EpochLengthBlocks)
	cfg, err := k.GetCurrentEpochConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, types.DefaultEpochLengthBlocks, cfg.EpochLengthBlocks)

	_, err = server.UpdateRewardsParams(ctx, &types.MsgUpdateRewardsParams{Authority: addr(9), Params: &next})
	require.Error(t, err)
}

func TestMsgUpdateRewardsParamsRejectsImmutableAndUnsupportedChanges(t *testing.T) {
	core := &coreSlotKeeperMock{authority: addr(1)}
	k, ctx, _ := setupAccountingKeeper(t, core, 1, types.DefaultParams())
	server := keeper.NewMsgServer(k)

	for _, mutate := range []func(*types.Params){
		func(p *types.Params) { p.NativeDenom = "other" },
		func(p *types.Params) { p.MaxSupply = "22" },
		func(p *types.Params) { p.FeeCollectionEnabled = true },
		func(p *types.Params) { p.FeeDistributionEnabled = true },
		func(p *types.Params) { p.WeightedRewardsEnabled = true },
		func(p *types.Params) { p.EmissionTreasuryShareBps = 10_001 },
		func(p *types.Params) { p.EmissionsEnabled = false },
	} {
		next := types.DefaultParams()
		mutate(&next)
		_, err := server.UpdateRewardsParams(ctx, &types.MsgUpdateRewardsParams{Authority: core.authority, Params: &next})
		require.Error(t, err)
	}
}

func TestEmergencyPauseAndResumeOnlyImmediateFlags(t *testing.T) {
	core := &coreSlotKeeperMock{authority: addr(1), emergency: addr(2)}
	k, ctx, _ := setupAccountingKeeper(t, core, 1, types.DefaultParams())
	server := keeper.NewMsgServer(k)
	pending := types.DefaultParams()
	pending.EpochLengthBlocks = 99
	require.NoError(t, k.SetPendingParams(ctx, pending))

	_, err := server.PauseRewards(ctx, &types.MsgPauseRewards{
		EmergencyAuthority: core.emergency, PauseEmissions: true, PauseEpochSettlement: true, PauseClaims: true,
	})
	require.NoError(t, err)
	params, err := k.GetParams(ctx)
	require.NoError(t, err)
	require.False(t, params.EmissionsEnabled)
	require.False(t, params.EpochSettlementEnabled)
	require.False(t, params.ClaimsEnabled)
	stillPending, found, err := k.GetPendingParams(ctx)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(99), stillPending.EpochLengthBlocks)

	_, err = server.ResumeRewards(ctx, &types.MsgResumeRewards{
		EmergencyAuthority: core.emergency, ResumeEmissions: true, ResumeEpochSettlement: true, ResumeClaims: true,
	})
	require.NoError(t, err)
	params, err = k.GetParams(ctx)
	require.NoError(t, err)
	require.True(t, params.EmissionsEnabled)
	require.True(t, params.EpochSettlementEnabled)
	require.True(t, params.ClaimsEnabled)

	_, err = server.PauseRewards(ctx, &types.MsgPauseRewards{EmergencyAuthority: addr(9), PauseClaims: true})
	require.Error(t, err)
}
