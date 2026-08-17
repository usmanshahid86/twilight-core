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
	// A still-mutable operational field. The reward economics this test used to
	// move now belong to the canonical reward-configuration history and are
	// refused through this path; see
	// TestMsgUpdateRewardsParamsRejectsImmutableAndUnsupportedChanges.
	next.TargetBlockTimeSeconds = types.DefaultTargetBlockTimeSeconds + 1

	_, err := server.UpdateRewardsParams(ctx, &types.MsgUpdateRewardsParams{Authority: core.authority, Params: &next})
	require.NoError(t, err)
	pending, found, err := k.GetPendingParams(ctx)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, types.DefaultTargetBlockTimeSeconds+1, pending.TargetBlockTimeSeconds)
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

	for _, tc := range []struct {
		name   string
		mutate func(*types.Params)
	}{
		{"native denom", func(p *types.Params) { p.NativeDenom = "other" }},
		{"max supply", func(p *types.Params) { p.MaxSupply = "22" }},
		{"fee collection", func(p *types.Params) { p.FeeCollectionEnabled = true }},
		{"fee distribution", func(p *types.Params) { p.FeeDistributionEnabled = true }},
		{"weighted rewards", func(p *types.Params) { p.WeightedRewardsEnabled = true }},
		{"emissions flag", func(p *types.Params) { p.EmissionsEnabled = false }},
		// Epoch geometry belongs to the canonical epoch-configuration history; the
		// generic params path must not be a second, unversioned way to move it.
		{"epoch length", func(p *types.Params) { p.EpochLengthBlocks = 99 }},

		// The economics below belong to the canonical reward-configuration history,
		// which binds a target two epochs ahead. A change reaching money through
		// this path instead would take effect at the very next epoch and leave no
		// version behind to audit.
		//
		// Every proposed value here is one the params document would otherwise
		// accept, which is what makes these cases about OWNERSHIP rather than about
		// validity: the request is refused because this surface no longer governs
		// the field, not because the number is bad.
		{"initial block subsidy", func(p *types.Params) { p.InitialBlockSubsidy = "123" }},
		{"emission treasury share", func(p *types.Params) { p.EmissionTreasuryShareBps = 100 }},
		{"treasury address", func(p *types.Params) { p.TreasuryAddress = addr(7) }},
		{"fee treasury share", func(p *types.Params) { p.FeeTreasuryShareBps = 100 }},

		// Genesis-fixed protocol configuration. Params.Validate already rejects any
		// other value for these, but "already constant" is a weaker guarantee than
		// refusing the change, and only the refusal is visible to the authority.
		{"halving mode", func(p *types.Params) {
			p.HalvingMode = types.HalvingMode_HALVING_MODE_UNSPECIFIED
		}},
		{"remainder policy", func(p *types.Params) {
			p.RemainderPolicy = types.RemainderPolicy_REMAINDER_POLICY_BURN
		}},
		{"distribution method", func(p *types.Params) {
			p.DistributionMethod = types.DistributionMethod_DISTRIBUTION_METHOD_SNAPSHOT_UNIFORM
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			next := types.DefaultParams()
			tc.mutate(&next)
			_, err := server.UpdateRewardsParams(ctx,
				&types.MsgUpdateRewardsParams{Authority: core.authority, Params: &next})
			require.Error(t, err)

			// Rejected, not accepted-and-ignored: nothing is staged. An authority
			// that got a success response while the value silently stayed put would
			// be the exact failure this closure exists to prevent.
			_, found, stageErr := k.GetPendingParams(ctx)
			require.NoError(t, stageErr)
			require.False(t, found, "a refused update must stage nothing")
		})
	}
}

// TestEmergencyPauseAndResumeScheduleForNextBlock replaces the retired
// per-flag immediate-pause test.
//
// Two properties matter and both are asserted: the transition is scheduled for
// H+1 rather than applied in H, and it is global — the deprecated per-area
// selectors on the message are ignored rather than honored.
func TestEmergencyPauseAndResumeScheduleForNextBlock(t *testing.T) {
	core := &coreSlotKeeperMock{authority: addr(1), emergency: addr(2)}
	k, ctx, _ := setupAccountingKeeper(t, core, 1, types.DefaultParams())
	server := keeper.NewMsgServer(k)
	ctx = ctx.WithBlockHeight(10)

	// Only the emergency authority may schedule.
	_, err := server.PauseRewards(ctx, &types.MsgPauseRewards{EmergencyAuthority: addr(9)})
	require.Error(t, err)

	// Deliberately set only ONE deprecated selector: a global pause must result.
	_, err = server.PauseRewards(ctx, &types.MsgPauseRewards{
		EmergencyAuthority: core.emergency, PauseEmissions: true,
	})
	require.NoError(t, err)

	state, err := k.GetPauseState(ctx)
	require.NoError(t, err)
	require.False(t, state.CurrentPaused, "H must remain governed by the state effective at H")
	require.True(t, state.HasPending)
	require.True(t, state.PendingValue)
	require.Equal(t, uint64(11), state.PendingEffectiveHeight)

	// Release is still enabled during H: the pending value is not visible early.
	enabled, err := k.SettlementReleaseEnabled(ctx)
	require.NoError(t, err)
	require.True(t, enabled)

	// A second change in the same block replaces the pending value; the last
	// accepted transaction wins.
	_, err = server.ResumeRewards(ctx, &types.MsgResumeRewards{EmergencyAuthority: core.emergency})
	require.NoError(t, err)
	state, err = k.GetPauseState(ctx)
	require.NoError(t, err)
	require.True(t, state.HasPending)
	require.False(t, state.PendingValue)
	require.Equal(t, uint64(11), state.PendingEffectiveHeight)
}
