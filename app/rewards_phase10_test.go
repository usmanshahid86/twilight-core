package app_test

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/log"

	"github.com/cosmos/cosmos-sdk/testutil/sims"

	"github.com/twilight-project/twilight-core/app"
	appparams "github.com/twilight-project/twilight-core/app/params"
	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	rewardstypes "github.com/twilight-project/twilight-core/x/rewards/types"
)

// renormalizeCoreSlotGenesis rewrites an exported CoreSlot genesis so its ACTIVE
// slots and Selection policies are normalized against initialHeight, which is
// what a fresh V2 genesis requires. It exists only so a rewards-focused
// export/import test can run; it is test scaffolding and encodes no claim about
// how a real continuation import should behave.
// renormalizeRewardsGenesis rewrites an exported rewards genesis's TIMELINE into
// the fresh-genesis shape: the original-genesis epoch anchor at the importing
// chain's initial height, the open epoch back at 1, an empty schedule, a clean
// pause state and a zero open counter.
//
// It deliberately does NOT touch the closed-epoch ledger the export carries — the
// finalized-epoch archive, the outstanding entitlements, the escrow balance behind
// them. That is the whole point of the test below: a live chain's ledger is not
// fresh-genesis state, and no amount of scaffolding makes it so. Reshaping it
// until a fresh importer accepted it would mean deciding, here in a test helper,
// what an outstanding obligation means across a restart — which is continuation
// work, deferred by design.
//
// Like its CoreSlot counterpart this is test scaffolding and encodes no claim
// about how a real continuation import should behave.
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

	rewardAnchor := rewardstypes.DefaultRewardConfigVersion(*rGen.Params)
	rGen.RewardConfigVersions = []*rewardstypes.RewardConfigVersion{&rewardAnchor}
	rGen.ScheduledRewardConfigs = nil

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

// TestRewardsLiveExportIsCompleteAndIsNotFreshGenesis replaces the export/import
// round trip this file used to run.
//
// The old test exported a chain that had closed an epoch, renormalized the
// document until a fresh importer accepted it, and then asserted what survived.
// Getting it accepted meant dropping the outstanding entitlements and zeroing the
// liability — while leaving the escrow that backed them, and the finalized-epoch
// archive that recorded them, in place. That is not a fresh genesis and it is not
// a continuation either; it is a third thing the architecture does not define, and
// the reconciled fresh-genesis contract now refuses it.
//
// What is actually true, and what this asserts:
//
//   - the export is COMPLETE. Every monetary fact of the closed epoch is in it:
//     the archive, the entitlements, the liability, the supply, the escrow.
//   - a fresh-genesis importer REFUSES it, naming the closed-epoch state as the
//     reason. Continuation import is deferred work, and until it exists the
//     refusal is the correct answer rather than a silent partial acceptance.
//
// The coverage the old test claimed on the import side — a chain booting and
// running a complete epoch boundary — is provided by
// TestFreshGenesisRunsItsFirstEpochBoundary against a genuinely conforming
// document.
func TestRewardsLiveExportIsCompleteAndIsNotFreshGenesis(t *testing.T) {
	a := bootApp(t)
	params := rewardstypes.DefaultParams()
	// The shortest admissible epoch length; toy values no longer boot a chain.
	params.EpochLengthBlocks = appparams.HardMinEpochLengthBlocks
	epochLen := int64(params.EpochLengthBlocks)
	snapshot := rewardstypes.DefaultEpochConfigSnapshot(params)
	initChainWithRewards(t, a, canonicalRewardsTimeline(&rewardstypes.GenesisState{
		Params:             &params,
		State:              &rewardstypes.RewardsState{CurrentEpoch: 1, CurrentEpochStartHeight: 1, CumulativeEmitted: "0", CarryForwardRemainder: "0"},
		CurrentEpochConfig: &snapshot,
	}, 1))

	blockTime := time.Unix(1_700_000_000, 0).UTC()
	for height := int64(1); height <= epochLen; height++ {
		_, err := a.FinalizeBlock(&abci.RequestFinalizeBlock{Height: height, Time: blockTime.Add(time.Duration(height) * time.Second)})
		require.NoError(t, err)
		_, err = a.Commit()
		require.NoError(t, err)
	}

	exported, err := a.ExportAppStateAndValidators(false, nil, []string{"auth", "bank", "consensus", "coreslot", "rewards"})
	require.NoError(t, err)
	require.Equal(t, epochLen+1, exported.Height)
	require.NotEmpty(t, exported.Validators)

	// --- the export is complete ------------------------------------------------

	epochEmission := strconv.FormatInt(epochLen*416190, 10)
	ctxA := a.NewUncachedContext(false, cmtproto.Header{Height: exported.Height})

	stateA, err := a.RewardsKeeper.GetState(ctxA)
	require.NoError(t, err)
	require.Equal(t, epochEmission, stateA.CumulativeEmitted)

	closed, found, err := a.RewardsKeeper.GetFinalizedEpoch(ctxA, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, epochEmission, closed.MintedEmission)

	entitlement, found, err := a.RewardsKeeper.GetSlotEntitlement(ctxA, 1, 1)
	require.NoError(t, err)
	require.True(t, found, "the closed epoch created an obligation")
	liabilityA, err := a.RewardsKeeper.GetOutstandingEntitlementLiability(ctxA)
	require.NoError(t, err)
	require.Equal(t, entitlement.EntitlementAmount, liabilityA.String())

	rewardsAddrA := a.AccountKeeper.GetModuleAddress(rewardstypes.ModuleName)
	require.Equal(t, epochEmission, a.BankKeeper.GetSupply(ctxA, app.BaseDenom).Amount.String())
	require.Equal(t, epochEmission, a.BankKeeper.GetBalance(ctxA, rewardsAddrA, app.BaseDenom).Amount.String())

	var exportedRewards rewardstypes.GenesisState
	var appStateMap map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(exported.AppState, &appStateMap))
	require.NoError(t, genesisCodec().UnmarshalJSON(appStateMap[rewardstypes.ModuleName], &exportedRewards))
	require.Len(t, exportedRewards.FinalizedEpochs, 1, "the archive is in the export")
	require.Len(t, exportedRewards.SlotEntitlements, 1, "the obligation is in the export")
	require.Equal(t, liabilityA.String(), exportedRewards.OutstandingEntitlementLiability)

	// --- and it is not a fresh genesis -----------------------------------------

	// CoreSlot lifecycle heights are renormalized first so the refusal below is
	// unambiguously about the rewards ledger and not about a slot activated at the
	// wrong height.
	appState := renormalizeCoreSlotGenesis(t, exported.AppState, exported.Height)
	appState = renormalizeRewardsGenesis(t, appState, exported.Height)

	b := app.New(log.NewNopLogger(), dbm.NewMemDB(), nil, true, sims.EmptyAppOptions{})
	failure := captureInitChainFailure(t, b, &abci.RequestInitChain{
		ChainId:         "",
		InitialHeight:   exported.Height,
		ConsensusParams: &exported.ConsensusParams,
		AppStateBytes:   appState,
	})
	require.ErrorIs(t, failure, rewardstypes.ErrInvalidGenesis,
		"a live chain's closed-epoch ledger is not fresh-genesis state, and importing it "+
			"is continuation work that does not exist yet")
	require.Contains(t, failure.Error(), "fresh genesis carries 1 finalized epochs")
}

// captureInitChainFailure runs InitChain and returns the failure it reports.
//
// The SDK surfaces a module InitGenesis error by panicking out of InitChain, so a
// test that wants to assert on the refusal has to catch it. Returning the error
// rather than matching a formatted string keeps the assertion on the sentinel,
// which is what the contract is written in terms of.
func captureInitChainFailure(t *testing.T, a *app.App, req *abci.RequestInitChain) (failure error) {
	t.Helper()
	defer func() {
		recovered := recover()
		require.NotNil(t, recovered, "InitChain was expected to refuse this genesis")
		err, ok := recovered.(error)
		require.Truef(t, ok, "InitChain panicked with a non-error value: %v", recovered)
		failure = err
	}()
	_, err := a.InitChain(req)
	require.NoError(t, err)
	return nil
}

func TestRewardsRuntimeFinalizeBlockFailClosed(t *testing.T) {
	a := bootApp(t)
	params := rewardstypes.DefaultParams()
	// The shortest admissible epoch length; toy values no longer boot a chain.
	params.EpochLengthBlocks = appparams.HardMinEpochLengthBlocks
	epochLen := int64(params.EpochLengthBlocks)
	snapshot := rewardstypes.DefaultEpochConfigSnapshot(params)
	initChainWithRewards(t, a, canonicalRewardsTimeline(&rewardstypes.GenesisState{
		Params:             &params,
		State:              &rewardstypes.RewardsState{CurrentEpoch: 1, CurrentEpochStartHeight: 1, CumulativeEmitted: "0", CarryForwardRemainder: "0"},
		CurrentEpochConfig: &snapshot,
	}, 1))

	// Drive to the block before the epoch boundary, committing each one.
	blockTime := time.Unix(1_700_000_000, 0).UTC()
	var err error
	for height := int64(1); height < epochLen; height++ {
		_, err = a.FinalizeBlock(&abci.RequestFinalizeBlock{
			Height: height, Time: blockTime.Add(time.Duration(height) * time.Second),
		})
		require.NoError(t, err)
		_, err = a.Commit()
		require.NoError(t, err)
	}

	// Corrupt only the test app's stored epoch snapshot to force the existing
	// unsupported-distribution error during runtime-dispatched rewards EndBlock.
	ctx := a.NewUncachedContext(false, cmtproto.Header{Height: epochLen - 1})
	bad := snapshot
	bad.WeightedRewardsEnabled = true
	require.NoError(t, a.RewardsKeeper.CurrentEpochConfig.Set(ctx, bad))

	// The boundary block is the one that finalizes, so it is the one that fails.
	_, err = a.FinalizeBlock(&abci.RequestFinalizeBlock{
		Height: epochLen, Time: blockTime.Add(time.Duration(epochLen) * time.Second),
	})
	require.Error(t, err, "runtime must propagate rewards EndBlock errors and reject the block")
	require.Equal(t, epochLen-1, a.LastBlockHeight(), "failed FinalizeBlock must not advance committed height")

	committed := a.NewContextLegacy(false, cmtproto.Header{Height: epochLen - 1})
	_, found, err := a.RewardsKeeper.GetFinalizedEpoch(committed, 1)
	require.NoError(t, err)
	require.False(t, found)
	state, err := a.RewardsKeeper.GetState(committed)
	require.NoError(t, err)
	require.Equal(t, uint64(1), state.CurrentEpoch)
	require.Equal(t, "0", state.CumulativeEmitted)
}
