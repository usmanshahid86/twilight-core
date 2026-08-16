package app_test

import (
	"encoding/json"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/std"
	"github.com/cosmos/cosmos-sdk/testutil/sims"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/twilight-project/twilight-core/app"
	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	rewardstypes "github.com/twilight-project/twilight-core/x/rewards/types"
)

// initChainWithRewards builds a full app genesis (all module defaults) but
// overrides coreslot (one ACTIVE slot + reward weight, providing a CometBFT
// validator) and rewards (the supplied genesis) and runs the real app InitChain.
// It returns the slot's payout address.
// genesisCodec builds a JSON codec that can (un)marshal coreslot/rewards genesis,
// including the ed25519 consensus-key Any in coreslot slots.
func genesisCodec() *codec.ProtoCodec {
	registry := codectypes.NewInterfaceRegistry()
	std.RegisterInterfaces(registry) // crypto pubkeys (ed25519 Any) + base
	coreslottypes.RegisterInterfaces(registry)
	rewardstypes.RegisterInterfaces(registry)
	return codec.NewProtoCodec(registry)
}

func initChainWithRewards(t *testing.T, a *app.App, rewardsGen *rewardstypes.GenesisState) (operator, payout string) {
	t.Helper()
	cdc := genesisCodec()

	operator, payout = acc(2), acc(12)
	csParams := coreslottypes.DefaultParams(app.AuthorityAddress(), app.EmergencyAuthorityAddress())
	csGen := &coreslottypes.GenesisState{
		Params: &csParams,
		Slots: []*coreslottypes.CoreSlot{{
			SlotId: 1, OperatorAddress: operator, PayoutAddress: payout,
			SettlementAddress: payout,
			ConsensusPubkey:   ed25519Any(t, 7),
			Status:            coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE,
			ConsensusPower:    1, RewardWeight: coreslottypes.DefaultRewardWeight,
			// Fresh-genesis ACTIVE normalization at the chain's initial height (§80).
			ActivationSequence: 1, ActivatedHeight: 1, ActivationEffectiveHeight: 1,
			CurrentSelectionPolicyVersion: 1,
		}},
		SelectionPolicies: []*coreslottypes.SelectionPolicyVersion{{
			SlotId: 1, PolicyVersion: 1, SelectionRateBps: 2_500, MaxSelectedParticipants: 10,
			ValidFromHeight: 1,
		}},
		RewardWeights: []*coreslottypes.OperatorRewardWeight{{SlotId: 1, FinalWeight: coreslottypes.DefaultRewardWeight}},
		NextSlotId:    2,
	}

	genMap := a.DefaultGenesis()
	genMap[coreslottypes.ModuleName] = cdc.MustMarshalJSON(csGen)
	genMap[rewardstypes.ModuleName] = cdc.MustMarshalJSON(rewardsGen)
	appState, err := json.Marshal(genMap)
	require.NoError(t, err)

	_, err = a.InitChain(&abci.RequestInitChain{
		ChainId:         "", // matches the app's default (unset) chain id
		ConsensusParams: sims.DefaultConsensusParams,
		AppStateBytes:   appState,
	})
	require.NoError(t, err)
	return operator, payout
}

// TestRewardsRuntimeDispatchFinalizeBlock is the runtime-dispatch proof: it drives
// the app through the real ABCI block lifecycle (InitChain + FinalizeBlock) and
// asserts rewards BeginBlock and EndBlock side effects occur WITHOUT the test ever
// calling a.RewardsKeeper.BeginBlock/EndBlock. ActiveBlocks incrementing proves
// runtime-dispatched BeginBlock; epoch finalization proves runtime-dispatched
// EndBlock.
func TestRewardsRuntimeDispatchFinalizeBlock(t *testing.T) {
	a := bootApp(t)

	// Resolved order is necessary but not sufficient; assert it, then prove dispatch.
	require.Equal(t, []string{"coreslot", "rewards"}, a.ModuleManager.OrderEndBlockers)
	require.Equal(t, []string{"rewards"}, a.ModuleManager.OrderBeginBlockers)

	rParams := rewardstypes.DefaultParams()
	rParams.EpochLengthBlocks = 2 // short epoch so it closes within the test
	rSnap := rewardstypes.DefaultEpochConfigSnapshot(rParams)
	rewardsGen := &rewardstypes.GenesisState{
		Params:             &rParams,
		State:              &rewardstypes.RewardsState{CurrentEpoch: 1, CurrentEpochStartHeight: 1, CumulativeEmitted: "0", CarryForwardRemainder: "0"},
		CurrentEpochConfig: &rSnap,
	}
	initChainWithRewards(t, a, rewardsGen)

	blockTime := time.Unix(1_700_000_000, 0).UTC()

	// === Block 1 via the runtime block loop. ===
	_, err := a.FinalizeBlock(&abci.RequestFinalizeBlock{Height: 1, Time: blockTime})
	require.NoError(t, err)
	ctx1 := a.NewContextLegacy(false, cmtproto.Header{Height: 1, Time: blockTime})

	// Runtime-dispatched BeginBlock must have credited the active slot.
	credited, err := a.RewardsKeeper.GetActiveBlocks(ctx1, 1, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(1), credited, "runtime-dispatched BeginBlock must increment ActiveBlocks")
	_, found, err := a.RewardsKeeper.GetFinalizedEpoch(ctx1, 1)
	require.NoError(t, err)
	require.False(t, found, "epoch must not finalize before its configured end height")
	supplyBefore := a.BankKeeper.GetSupply(ctx1, app.BaseDenom).Amount
	require.True(t, supplyBefore.IsZero(), "no premine; nothing minted before finalization")
	_, err = a.Commit()
	require.NoError(t, err)

	// === Block 2 via the runtime block loop (the configured epoch boundary). ===
	_, err = a.FinalizeBlock(&abci.RequestFinalizeBlock{Height: 2, Time: blockTime})
	require.NoError(t, err)
	ctx2 := a.NewContextLegacy(false, cmtproto.Header{Height: 2, Time: blockTime})

	// Runtime-dispatched EndBlock must have finalized the epoch exactly once.
	epoch, found, err := a.RewardsKeeper.GetFinalizedEpoch(ctx2, 1)
	require.NoError(t, err)
	require.True(t, found, "runtime-dispatched EndBlock must finalize the epoch at the boundary")
	state, err := a.RewardsKeeper.GetState(ctx2)
	require.NoError(t, err)
	require.Equal(t, uint64(2), state.CurrentEpoch, "CurrentEpoch must advance after finalization")

	// 1 slot × 2 active blocks × 416190 subsidy = 832380 minted into real supply.
	const mintedEmission = "832380"
	supplyAfter := a.BankKeeper.GetSupply(ctx2, app.BaseDenom).Amount
	require.Equal(t, mintedEmission, supplyAfter.Sub(supplyBefore).String(),
		"supply delta must equal the minted epoch emission")
	require.Equal(t, mintedEmission, epoch.MintedEmission)
	_, err = a.Commit()
	require.NoError(t, err)
}

// TestRewardsInitChainGenesisAccounts proves rewards genesis initializes through
// the real app genesis path and that the module accounts are created by genesis
// (auth InitGenesis), not lazily by a later MintCoins.
func TestRewardsInitChainGenesisAccounts(t *testing.T) {
	a := bootApp(t)
	// Default rewards genesis (epoch 17280, cap 21e12, cumulative 0).
	initChainWithRewards(t, a, rewardstypes.DefaultGenesis())
	ctx := a.NewContextLegacy(false, cmtproto.Header{Height: 1})

	// Module accounts exist after genesis with exactly the intended permissions.
	rewardsAcc := a.AccountKeeper.GetModuleAccount(ctx, rewardstypes.ModuleName)
	require.NotNil(t, rewardsAcc)
	require.Equal(t, []string{authtypes.Minter}, rewardsAcc.GetPermissions())
	feePoolAcc := a.AccountKeeper.GetModuleAccount(ctx, rewardstypes.FeePoolName)
	require.NotNil(t, feePoolAcc)
	require.Empty(t, feePoolAcc.GetPermissions())

	// Rewards state initialized through the real genesis path.
	params, err := a.RewardsKeeper.GetParams(ctx)
	require.NoError(t, err)
	require.Equal(t, app.BaseDenom, params.NativeDenom)
	require.Equal(t, "21000000000000", params.MaxSupply)
	state, err := a.RewardsKeeper.GetState(ctx)
	require.NoError(t, err)
	require.Equal(t, "0", state.CumulativeEmitted, "no premine in default genesis")
	require.Equal(t, uint64(1), state.CurrentEpoch)
	require.Equal(t, uint64(1), state.CurrentEpochStartHeight)

	// No premine: nothing minted at genesis.
	require.True(t, a.BankKeeper.GetSupply(ctx, app.BaseDenom).Amount.IsZero())

	// Default genesis round-trips through validation.
	require.NoError(t, rewardstypes.DefaultGenesis().Validate())
}

// TestRewardsAuthorityMsgRoutedThroughApp proves the rewards Msg service is wired
// into the app's MsgServiceRouter and that authority/emergency-authority are read
// through the wired CoreSlot keeper.
func TestRewardsAuthorityMsgRoutedThroughApp(t *testing.T) {
	a := bootApp(t)
	ctx := a.NewUncachedContext(false, cmtproto.Header{Height: 1})

	// Seed CoreSlot authority/emergency and rewards params/state through the app keepers.
	csParams := coreslottypes.DefaultParams(app.AuthorityAddress(), app.EmergencyAuthorityAddress())
	require.NoError(t, a.CoreSlotKeeper.Params.Set(ctx, csParams))
	rParams := rewardstypes.DefaultParams()
	require.NoError(t, a.RewardsKeeper.SetParams(ctx, rParams))
	require.NoError(t, a.RewardsKeeper.SetState(ctx, rewardstypes.RewardsState{CurrentEpoch: 1, CurrentEpochStartHeight: 1, CumulativeEmitted: "0", CarryForwardRemainder: "0"}))
	snap := rewardstypes.DefaultEpochConfigSnapshot(rParams)
	require.NoError(t, a.RewardsKeeper.SetCurrentEpochConfig(ctx, snap))

	// --- MsgUpdateRewardsParams routed through the app MsgServiceRouter. ---
	queued := rewardstypes.DefaultParams()
	queued.EpochLengthBlocks = 3 // mutable economic change
	updateMsg := &rewardstypes.MsgUpdateRewardsParams{Authority: app.AuthorityAddress(), Params: &queued}
	handler := a.MsgServiceRouter().Handler(updateMsg)
	require.NotNil(t, handler, "rewards MsgUpdateRewardsParams must be registered on the app router")

	_, err := handler(ctx, updateMsg)
	require.NoError(t, err, "CoreSlot authority must be able to queue params via the app router")

	pending, found, err := a.RewardsKeeper.GetPendingParams(ctx)
	require.NoError(t, err)
	require.True(t, found, "update must queue PendingParams")
	require.Equal(t, uint64(3), pending.EpochLengthBlocks)
	// Current epoch config is NOT mutated immediately.
	cfg, err := a.RewardsKeeper.GetCurrentEpochConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, rParams.EpochLengthBlocks, cfg.EpochLengthBlocks, "current epoch config must not change on a queued update")

	// Non-authority rejected.
	badUpdate := &rewardstypes.MsgUpdateRewardsParams{Authority: acc(99), Params: &queued}
	_, err = a.MsgServiceRouter().Handler(badUpdate)(ctx, badUpdate)
	require.Error(t, err, "non-authority must not update rewards params")

	// --- MsgPauseRewards routed through the app, gated by emergency authority. ---
	pauseMsg := &rewardstypes.MsgPauseRewards{EmergencyAuthority: app.EmergencyAuthorityAddress(), PauseEmissions: true}
	_, err = a.MsgServiceRouter().Handler(pauseMsg)(ctx, pauseMsg)
	require.NoError(t, err, "CoreSlot emergency authority must be able to pause via the app router")
	paused, err := a.RewardsKeeper.GetParams(ctx)
	require.NoError(t, err)
	require.False(t, paused.EmissionsEnabled, "pause must toggle only the requested flag")
	require.True(t, paused.EpochSettlementEnabled, "settlement flag must be untouched")
	require.True(t, paused.ClaimsEnabled, "claims flag must be untouched")

	// Non-emergency-authority rejected (the normal authority cannot pause).
	badPause := &rewardstypes.MsgPauseRewards{EmergencyAuthority: app.AuthorityAddress(), PauseClaims: true}
	_, err = a.MsgServiceRouter().Handler(badPause)(ctx, badPause)
	require.Error(t, err, "normal authority must not be able to pause; emergency authority is separate")
}

// TestRewardsQueryServiceRegistered proves the rewards gRPC query service is wired
// into the app's query router (Phase 9), reachable by its fully-qualified method.
func TestRewardsQueryServiceRegistered(t *testing.T) {
	a := bootApp(t)
	for _, method := range []string{
		"/twilight.rewards.v1.Query/Params",
		"/twilight.rewards.v1.Query/EpochInfo",
		"/twilight.rewards.v1.Query/ModuleBalances",
	} {
		require.NotNil(t, a.GRPCQueryRouter().Route(method), "query route %s must be registered", method)
	}
}

// TestRewardsAppGenesisExportImportRoundTrip proves rewards genesis round-trips
// through the real app genesis path (the module wrapper's InitGenesis/ExportGenesis
// invoked by the app ModuleManager), not just the keeper. It seeds a populated
// genesis (non-default state + pending params), exports it via
// ModuleManager.ExportGenesisForModules, asserts byte-equality, then re-imports
// the exported genesis into a fresh app and confirms the state is preserved.
func TestRewardsAppGenesisExportImportRoundTrip(t *testing.T) {
	cdc := genesisCodec()

	params := rewardstypes.DefaultParams()
	pending := rewardstypes.DefaultParams()
	pending.EpochLengthBlocks = 9 // a queued, mutable economic change
	snap := rewardstypes.DefaultEpochConfigSnapshot(params)
	rGen := &rewardstypes.GenesisState{
		Params:             &params,
		State:              &rewardstypes.RewardsState{CurrentEpoch: 5, CurrentEpochStartHeight: 100, CumulativeEmitted: "1000000", CarryForwardRemainder: "250"},
		CurrentEpochConfig: &snap,
		HasPendingParams:   true,
		PendingParams:      &pending,
	}

	// Import into app A through the real app genesis path.
	a := bootApp(t)
	initChainWithRewards(t, a, rGen)
	ctxA := a.NewContextLegacy(false, cmtproto.Header{Height: 1})

	// Export via the app ModuleManager (the same path app.ExportAppStateAndValidators uses).
	exported, err := a.ModuleManager.ExportGenesisForModules(ctxA, cdc, []string{rewardstypes.ModuleName})
	require.NoError(t, err)
	require.JSONEq(t, string(cdc.MustMarshalJSON(rGen)), string(exported[rewardstypes.ModuleName]),
		"app-exported rewards genesis must equal the imported genesis")

	// Re-import the exported genesis into a fresh app and confirm state is preserved.
	var roundTripped rewardstypes.GenesisState
	require.NoError(t, cdc.UnmarshalJSON(exported[rewardstypes.ModuleName], &roundTripped))
	b := bootApp(t)
	initChainWithRewards(t, b, &roundTripped)
	ctxB := b.NewContextLegacy(false, cmtproto.Header{Height: 1})

	stateB, err := b.RewardsKeeper.GetState(ctxB)
	require.NoError(t, err)
	require.Equal(t, uint64(5), stateB.CurrentEpoch)
	require.Equal(t, "1000000", stateB.CumulativeEmitted)
	require.Equal(t, "250", stateB.CarryForwardRemainder)
	pendingB, found, err := b.RewardsKeeper.GetPendingParams(ctxB)
	require.NoError(t, err)
	require.True(t, found, "pending params must survive the app export/import round trip")
	require.Equal(t, uint64(9), pendingB.EpochLengthBlocks)
}

// TestRewardsEndBlockFailClosedNoHalfCommit proves the fail-closed failure mode
// against the booted app's real stores: a finalization fault makes EndBlock return
// an error and commit no partial state (no finalized epoch, cumulative unchanged,
// epoch not advanced). Under the runtime, baseapp.FinalizeBlock surfaces a module
// EndBlocker error and discards the block's deliverState, so the same fault halts
// the block rather than half-committing — the intended accounting-safety posture.
func TestRewardsEndBlockFailClosedNoHalfCommit(t *testing.T) {
	a := bootApp(t)
	ctx := a.NewUncachedContext(false, cmtproto.Header{Height: 1})

	params := rewardstypes.DefaultParams()
	params.EpochLengthBlocks = 2
	require.NoError(t, a.RewardsKeeper.SetParams(ctx, params))
	require.NoError(t, a.RewardsKeeper.SetState(ctx, rewardstypes.RewardsState{CurrentEpoch: 1, CurrentEpochStartHeight: 1, CumulativeEmitted: "0", CarryForwardRemainder: "0"}))

	// Inject an unsupported (weighted) epoch config directly into the collection,
	// bypassing the validating setter, to force a finalization fault at the boundary.
	badCfg := rewardstypes.DefaultEpochConfigSnapshot(params)
	badCfg.WeightedRewardsEnabled = true
	require.NoError(t, a.RewardsKeeper.CurrentEpochConfig.Set(ctx, badCfg))

	// At the configured boundary, EndBlock must fail closed.
	boundaryCtx := ctx.WithBlockHeight(2)
	require.Error(t, a.RewardsKeeper.EndBlock(boundaryCtx), "unsupported config must abort finalization")

	// No partial commit: no finalized epoch, cumulative unchanged, epoch not advanced.
	_, found, err := a.RewardsKeeper.GetFinalizedEpoch(ctx, 1)
	require.NoError(t, err)
	require.False(t, found, "fail-closed finalization must not write a finalized epoch")
	state, err := a.RewardsKeeper.GetState(ctx)
	require.NoError(t, err)
	require.Equal(t, "0", state.CumulativeEmitted, "cumulative emitted must not advance on a finalization fault")
	require.Equal(t, uint64(1), state.CurrentEpoch, "epoch must not advance on a finalization fault")
}
