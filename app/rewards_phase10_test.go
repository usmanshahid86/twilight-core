package app_test

import (
	"encoding/json"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/log"

	"github.com/cosmos/cosmos-sdk/testutil/sims"

	"github.com/twilight-project/twilight-core/app"
	rewardstypes "github.com/twilight-project/twilight-core/x/rewards/types"
)

func TestRewardsPopulatedAppExportImportAndContinue(t *testing.T) {
	a := bootApp(t)
	params := rewardstypes.DefaultParams()
	params.EpochLengthBlocks = 2
	snapshot := rewardstypes.DefaultEpochConfigSnapshot(params)
	initChainWithRewards(t, a, &rewardstypes.GenesisState{
		Params:             &params,
		State:              &rewardstypes.RewardsState{CurrentEpoch: 1, CurrentEpochStartHeight: 1, CumulativeEmitted: "0", CarryForwardRemainder: "0"},
		CurrentEpochConfig: &snapshot,
	})

	blockTime := time.Unix(1_700_000_000, 0).UTC()
	for height := int64(1); height <= 2; height++ {
		_, err := a.FinalizeBlock(&abci.RequestFinalizeBlock{Height: height, Time: blockTime.Add(time.Duration(height) * time.Second)})
		require.NoError(t, err)
		_, err = a.Commit()
		require.NoError(t, err)
	}

	exported, err := a.ExportAppStateAndValidators(false, nil, []string{"auth", "bank", "consensus", "coreslot", "rewards"})
	require.NoError(t, err)
	require.Equal(t, int64(3), exported.Height)
	require.NotEmpty(t, exported.Validators)

	b := app.New(log.NewNopLogger(), dbm.NewMemDB(), nil, true, sims.EmptyAppOptions{})
	_, err = b.InitChain(&abci.RequestInitChain{
		ChainId:         "",
		InitialHeight:   exported.Height,
		ConsensusParams: &exported.ConsensusParams,
		AppStateBytes:   json.RawMessage(exported.AppState),
	})
	require.NoError(t, err)

	_, err = b.FinalizeBlock(&abci.RequestFinalizeBlock{Height: 3, Time: blockTime.Add(3 * time.Second)})
	require.NoError(t, err)
	_, err = b.Commit()
	require.NoError(t, err)

	ctx := b.NewUncachedContext(false, cmtproto.Header{Height: 3})
	state, err := b.RewardsKeeper.GetState(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), state.CurrentEpoch)
	require.Equal(t, "832380", state.CumulativeEmitted)
	importedParams, err := b.RewardsKeeper.GetParams(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), importedParams.EpochLengthBlocks)
	importedConfig, err := b.RewardsKeeper.GetCurrentEpochConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), importedConfig.EpochLengthBlocks)
	epoch, found, err := b.RewardsKeeper.GetFinalizedEpoch(ctx, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "832380", epoch.MintedEmission)
	claim, found, err := b.RewardsKeeper.GetClaimRecord(ctx, 1, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "832380", claim.Amount)
	require.False(t, claim.Claimed)
	require.Equal(t, "832380", b.BankKeeper.GetSupply(ctx, app.BaseDenom).Amount.String())
	rewardsAddr := b.AccountKeeper.GetModuleAddress(rewardstypes.ModuleName)
	require.Equal(t, "832380", b.BankKeeper.GetBalance(ctx, rewardsAddr, app.BaseDenom).Amount.String())
	active, err := b.RewardsKeeper.GetActiveBlocks(ctx, 2, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(1), active, "imported app must continue coherent active-block accounting")
}

func TestRewardsRuntimeFinalizeBlockFailClosed(t *testing.T) {
	a := bootApp(t)
	params := rewardstypes.DefaultParams()
	params.EpochLengthBlocks = 2
	snapshot := rewardstypes.DefaultEpochConfigSnapshot(params)
	initChainWithRewards(t, a, &rewardstypes.GenesisState{
		Params:             &params,
		State:              &rewardstypes.RewardsState{CurrentEpoch: 1, CurrentEpochStartHeight: 1, CumulativeEmitted: "0", CarryForwardRemainder: "0"},
		CurrentEpochConfig: &snapshot,
	})

	blockTime := time.Unix(1_700_000_000, 0).UTC()
	_, err := a.FinalizeBlock(&abci.RequestFinalizeBlock{Height: 1, Time: blockTime})
	require.NoError(t, err)
	_, err = a.Commit()
	require.NoError(t, err)

	// Corrupt only the test app's stored epoch snapshot to force the existing
	// unsupported-distribution error during runtime-dispatched rewards EndBlock.
	ctx := a.NewUncachedContext(false, cmtproto.Header{Height: 1})
	bad := snapshot
	bad.WeightedRewardsEnabled = true
	require.NoError(t, a.RewardsKeeper.CurrentEpochConfig.Set(ctx, bad))

	_, err = a.FinalizeBlock(&abci.RequestFinalizeBlock{Height: 2, Time: blockTime.Add(time.Second)})
	require.Error(t, err, "runtime must propagate rewards EndBlock errors and reject the block")
	require.Equal(t, int64(1), a.LastBlockHeight(), "failed FinalizeBlock must not advance committed height")

	committed := a.NewContextLegacy(false, cmtproto.Header{Height: 1})
	_, found, err := a.RewardsKeeper.GetFinalizedEpoch(committed, 1)
	require.NoError(t, err)
	require.False(t, found)
	state, err := a.RewardsKeeper.GetState(committed)
	require.NoError(t, err)
	require.Equal(t, uint64(1), state.CurrentEpoch)
	require.Equal(t, "0", state.CumulativeEmitted)
}
