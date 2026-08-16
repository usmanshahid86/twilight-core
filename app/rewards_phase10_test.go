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
	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	rewardstypes "github.com/twilight-project/twilight-core/x/rewards/types"
)

// renormalizeCoreSlotGenesis rewrites an exported CoreSlot genesis so its ACTIVE
// slots and Selection policies are normalized against initialHeight, which is
// what a fresh V2 genesis requires. It exists only so a rewards-focused
// export/import test can run; it is test scaffolding and encodes no claim about
// how a real continuation import should behave.
// renormalizeRewardsGenesis rewrites an exported rewards genesis into the
// fresh-genesis shape this chain now requires: the original-genesis epoch anchor
// at the importing chain's initial height, the open epoch back at 1, an empty
// schedule, a clean pause state and a zero open counter.
//
// Like its CoreSlot counterpart this is test scaffolding, not a continuation
// claim. Rewards genesis import is deliberately fresh-only — the epoch anchor is
// the permanent origin of every later boundary, so a live cursor cannot simply be
// re-imported — and deciding what a real continuation import means is separate,
// later work. The monetary state the surrounding test asserts (cumulative
// emission, finalized epochs, claim records, balances) is preserved untouched.
func renormalizeRewardsGenesis(t *testing.T, appState []byte, initialHeight int64) []byte {
	t.Helper()
	var state map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(appState, &state))

	cdc := genesisCodec()
	var rGen rewardstypes.GenesisState
	require.NoError(t, cdc.UnmarshalJSON(state[rewardstypes.ModuleName], &rGen))

	anchor := rewardstypes.DefaultEpochConfigVersion(*rGen.Params, uint64(initialHeight))
	rGen.EpochConfigVersions = []*rewardstypes.EpochConfigVersion{&anchor}
	rGen.ScheduledEpochConfigs = nil
	rGen.PauseState = &rewardstypes.RewardsPauseState{}
	rGen.OpenRewardEnabledBlocks = 0
	rGen.State.CurrentEpoch = 1
	rGen.State.CurrentEpochStartHeight = uint64(initialHeight)

	state[rewardstypes.ModuleName] = cdc.MustMarshalJSON(&rGen)
	out, err := json.Marshal(state)
	require.NoError(t, err)
	return out
}

func renormalizeCoreSlotGenesis(t *testing.T, appState []byte, initialHeight int64) []byte {
	t.Helper()
	var state map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(appState, &state))

	cdc := genesisCodec()
	var csGen coreslottypes.GenesisState
	require.NoError(t, cdc.UnmarshalJSON(state[coreslottypes.ModuleName], &csGen))
	for _, slot := range csGen.Slots {
		if slot.Status != coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE {
			continue
		}
		slot.ActivatedHeight = initialHeight
		slot.ActivationEffectiveHeight = initialHeight
	}
	for _, policy := range csGen.SelectionPolicies {
		policy.ValidFromHeight = initialHeight
	}
	state[coreslottypes.ModuleName] = cdc.MustMarshalJSON(&csGen)

	out, err := json.Marshal(state)
	require.NoError(t, err)
	return out
}

func TestRewardsPopulatedAppExportImportAndContinue(t *testing.T) {
	a := bootApp(t)
	params := rewardstypes.DefaultParams()
	params.EpochLengthBlocks = 2
	snapshot := rewardstypes.DefaultEpochConfigSnapshot(params)
	initChainWithRewards(t, a, canonicalRewardsTimeline(&rewardstypes.GenesisState{
		Params:             &params,
		State:              &rewardstypes.RewardsState{CurrentEpoch: 1, CurrentEpochStartHeight: 1, CumulativeEmitted: "0", CarryForwardRemainder: "0"},
		CurrentEpochConfig: &snapshot,
	}, 1))

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

	// x/coreslot genesis import is FRESH-genesis only: §80 requires an ACTIVE slot
	// to be normalized against the chain's initial height, and height-preserving
	// continuation for CoreSlot is a separate, deferred piece of work (H7). The
	// exported slots carry the heights of the chain they came from, so they are
	// renormalized to this chain's initial height before import.
	//
	// This test is about REWARDS continuity across an export/import, which the
	// assertions below check and which renormalizing CoreSlot lifecycle heights
	// does not affect. It is deliberately NOT a claim that CoreSlot continuation
	// works; when H7 lands it will decide what a continuation import means, and
	// this scaffolding should be revisited then rather than treated as precedent.
	appState := renormalizeCoreSlotGenesis(t, exported.AppState, exported.Height)
	appState = renormalizeRewardsGenesis(t, appState, exported.Height)

	b := app.New(log.NewNopLogger(), dbm.NewMemDB(), nil, true, sims.EmptyAppOptions{})
	_, err = b.InitChain(&abci.RequestInitChain{
		ChainId:         "",
		InitialHeight:   exported.Height,
		ConsensusParams: &exported.ConsensusParams,
		AppStateBytes:   appState,
	})
	require.NoError(t, err)

	_, err = b.FinalizeBlock(&abci.RequestFinalizeBlock{Height: 3, Time: blockTime.Add(3 * time.Second)})
	require.NoError(t, err)
	_, err = b.Commit()
	require.NoError(t, err)

	ctx := b.NewUncachedContext(false, cmtproto.Header{Height: 3})
	state, err := b.RewardsKeeper.GetState(ctx)
	require.NoError(t, err)
	// The imported chain re-anchors at epoch 1: rewards genesis import is
	// fresh-only, so an exported mid-life epoch cursor is renormalized rather
	// than continued. The monetary facts below are what this test actually
	// protects, and they survive intact.
	require.Equal(t, uint64(1), state.CurrentEpoch)
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
	active, err := b.RewardsKeeper.GetActiveBlocks(ctx, 1, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(1), active, "imported app must continue coherent active-block accounting")
}

func TestRewardsRuntimeFinalizeBlockFailClosed(t *testing.T) {
	a := bootApp(t)
	params := rewardstypes.DefaultParams()
	params.EpochLengthBlocks = 2
	snapshot := rewardstypes.DefaultEpochConfigSnapshot(params)
	initChainWithRewards(t, a, canonicalRewardsTimeline(&rewardstypes.GenesisState{
		Params:             &params,
		State:              &rewardstypes.RewardsState{CurrentEpoch: 1, CurrentEpochStartHeight: 1, CumulativeEmitted: "0", CarryForwardRemainder: "0"},
		CurrentEpochConfig: &snapshot,
	}, 1))

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
