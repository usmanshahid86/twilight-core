package keeper_test

import (
	"testing"

	"cosmossdk.io/collections"
	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/x/rewards/keeper"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

func TestClaimRewardsAnyoneTriggeredAndGroupedByPayout(t *testing.T) {
	k, ctx, bank := setupClaims(t)
	server := keeper.NewMsgServer(k)
	signer := addr(9)

	_, err := server.ClaimRewards(ctx.WithBlockHeight(50), &types.MsgClaimRewards{
		Signer: signer, SlotId: 1, StartEpoch: 1, EndEpoch: 2,
	})
	require.NoError(t, err)
	require.Len(t, bank.sends, 2)
	require.NotEqual(t, signer, bank.sends[0].recipient.String())
	require.True(t, hasEvent(ctx, types.EventTypeClaimRewards))

	for epoch := uint64(1); epoch <= 2; epoch++ {
		record, found, err := k.GetClaimRecord(ctx, 1, epoch)
		require.NoError(t, err)
		require.True(t, found)
		require.True(t, record.Claimed)
		require.Equal(t, int64(50), record.ClaimedAtHeight)
	}
	finalized, found, err := k.GetFinalizedEpoch(ctx, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.False(t, finalized.Rewards[0].Claimed, "closed epoch aggregate must remain immutable")
}

func TestClaimRewardsFailuresDoNotMarkRecords(t *testing.T) {
	t.Run("bank failure", func(t *testing.T) {
		k, ctx, bank := setupClaims(t)
		bank.failSend()
		err := k.ClaimRewards(ctx, &types.MsgClaimRewards{Signer: addr(9), SlotId: 1, StartEpoch: 1, EndEpoch: 1})
		require.Error(t, err)
		record, _, err := k.GetClaimRecord(ctx, 1, 1)
		require.NoError(t, err)
		require.False(t, record.Claimed)
	})

	t.Run("claims disabled", func(t *testing.T) {
		k, ctx, _ := setupClaims(t)
		params, err := k.GetParams(ctx)
		require.NoError(t, err)
		params.ClaimsEnabled = false
		require.NoError(t, k.SetParams(ctx, params))
		require.Error(t, k.ClaimRewards(ctx, &types.MsgClaimRewards{Signer: addr(9), SlotId: 1, StartEpoch: 1, EndEpoch: 1}))
	})

	t.Run("already claimed and missing", func(t *testing.T) {
		k, ctx, _ := setupClaims(t)
		require.NoError(t, k.ClaimRewards(ctx, &types.MsgClaimRewards{Signer: addr(9), SlotId: 1, StartEpoch: 1, EndEpoch: 1}))
		require.Error(t, k.ClaimRewards(ctx, &types.MsgClaimRewards{Signer: addr(9), SlotId: 1, StartEpoch: 1, EndEpoch: 1}))
		require.Error(t, k.ClaimRewards(ctx, &types.MsgClaimRewards{Signer: addr(9), SlotId: 2, StartEpoch: 1, EndEpoch: 1}))
	})

	t.Run("range too large and epoch not finalized", func(t *testing.T) {
		k, ctx, _ := setupClaims(t)
		params, err := k.GetParams(ctx)
		require.NoError(t, err)
		params.MaxClaimEpochsPerTx = 1
		require.NoError(t, k.SetParams(ctx, params))
		require.Error(t, k.ClaimRewards(ctx, &types.MsgClaimRewards{Signer: addr(9), SlotId: 1, StartEpoch: 1, EndEpoch: 2}))
		require.Error(t, k.ClaimRewards(ctx, &types.MsgClaimRewards{Signer: addr(9), SlotId: 1, StartEpoch: 3, EndEpoch: 3}))
	})

	t.Run("insufficient balance and zero amount", func(t *testing.T) {
		k, ctx, bank := setupClaims(t)
		moduleAddress := sdk.AccAddress(make([]byte, 20))
		bank.balances[moduleAddress.String()] = sdk.NewInt64Coin(types.DefaultParams().NativeDenom, 0)
		require.Error(t, k.ClaimRewards(ctx, &types.MsgClaimRewards{Signer: addr(9), SlotId: 1, StartEpoch: 1, EndEpoch: 1}))

		record, _, err := k.GetClaimRecord(ctx, 1, 1)
		require.NoError(t, err)
		record.Amount = "0"
		require.NoError(t, k.ClaimRecords.Set(ctx, collections.Join(uint64(1), uint64(1)), record))
		bank.balances[moduleAddress.String()] = sdk.NewInt64Coin(types.DefaultParams().NativeDenom, 100)
		require.Error(t, k.ClaimRewards(ctx, &types.MsgClaimRewards{Signer: addr(9), SlotId: 1, StartEpoch: 1, EndEpoch: 1}))
	})
}

func setupClaims(t *testing.T) (keeper.Keeper, sdk.Context, *bankKeeperMock) {
	t.Helper()
	k, ctx, bank := setupAccountingKeeper(t, &coreSlotKeeperMock{}, 3, types.DefaultParams())
	for epoch := uint64(1); epoch <= 2; epoch++ {
		reward := validClaim(1, epoch)
		reward.Amount = "5"
		if epoch == 2 {
			reward.PayoutAddress = addr(7)
		}
		finalized := validEpoch(epoch, types.DefaultParams())
		rewardCopy := reward
		finalized.Rewards = []*types.EligibleSlotReward{&rewardCopy}
		require.NoError(t, k.SetFinalizedEpoch(ctx, finalized))
		require.NoError(t, k.SetClaimRecord(ctx, reward))
	}
	moduleAddress := sdk.AccAddress(make([]byte, 20))
	bank.balances = map[string]sdk.Coin{
		moduleAddress.String(): sdk.NewInt64Coin(types.DefaultParams().NativeDenom, 100),
	}
	return k, ctx, bank
}
