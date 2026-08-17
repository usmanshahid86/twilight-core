package app_test

import (
	"encoding/json"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtcrypto "github.com/cometbft/cometbft/proto/tendermint/crypto"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/testutil/sims"
	anypb "github.com/cosmos/gogoproto/types/any"

	"github.com/twilight-project/twilight-core/app"
	appparams "github.com/twilight-project/twilight-core/app/params"
	coreslotkeeper "github.com/twilight-project/twilight-core/x/coreslot/keeper"
	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	rewardstypes "github.com/twilight-project/twilight-core/x/rewards/types"
)

// TestFreshV2GenesisBootsThroughTheRealApplication is the app-integration proof for
// the V2 structural state. Keeper-level tests can construct any context they like;
// this one goes through the real InitChain, where the initial height comes from the
// SDK rather than from the test.
//
// It matters because InitChain runs genesis with a block height of ZERO for a chain
// starting at height 1 — the SDK's own convention — so a module that naively read
// ctx.BlockHeight() as the initial height would normalize against the wrong value
// and either reject a conforming genesis or admit a non-conforming one.
func TestFreshV2GenesisBootsThroughTheRealApplication(t *testing.T) {
	// The requested initial height and the height genesis is normalized against
	// are not the same thing: InitChain reports 0 for a chain starting at 1, and
	// only carries a real header height above that. Both branches are exercised.
	for _, tc := range []struct {
		name            string
		requestedHeight int64
		initialHeight   int64
	}{
		{"default first block", 0, 1},
		{"explicit first block", 1, 1},
		{"explicit height above one", 5, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bootFreshV2Genesis(t, tc.requestedHeight, tc.initialHeight)
		})
	}
}

func bootFreshV2Genesis(t *testing.T, requestedHeight, initialHeight int64) {
	t.Helper()
	a := bootApp(t)
	cdc := genesisCodec()

	operator, payout, settlement := acc(2), acc(12), acc(22)
	csParams := coreslottypes.DefaultParams(app.AuthorityAddress(), app.EmergencyAuthorityAddress())
	csGen := &coreslottypes.GenesisState{
		Params: &csParams,
		Slots: []*coreslottypes.CoreSlot{{
			SlotId: 1, OperatorAddress: operator, PayoutAddress: payout,
			SettlementAddress: settlement,
			ConsensusPubkey:   ed25519Any(t, 7),
			Status:            coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE,
			ConsensusPower:    1, RewardWeight: coreslottypes.DefaultRewardWeight,
			ActivationSequence: 1, ActivatedHeight: initialHeight, ActivationEffectiveHeight: initialHeight,
			CurrentSelectionPolicyVersion: 1,
		}},
		SelectionPolicies: []*coreslottypes.SelectionPolicyVersion{{
			SlotId: 1, PolicyVersion: 1, SelectionRateBps: 2_500, MaxSelectedParticipants: 10,
			ValidFromHeight: initialHeight,
		}},
		RewardWeights: []*coreslottypes.OperatorRewardWeight{{SlotId: 1, FinalWeight: coreslottypes.DefaultRewardWeight}},
		NextSlotId:    2,
	}

	// The rewards genesis must be authored for THIS chain's initial height: the
	// original-genesis epoch anchor is the permanent origin of every later
	// boundary, so the module-default document (anchored at height 1) is not a
	// valid genesis for a chain that starts elsewhere. Rejecting the mismatch is
	// deliberate — synthesizing the anchor from the context would silently rewrite
	// canonical state the operator is supposed to declare.
	rParams := rewardstypes.DefaultParams()
	rSnap := rewardstypes.DefaultEpochConfigSnapshot(rParams)
	rAnchor := rewardstypes.DefaultEpochConfigVersion(rParams, uint64(initialHeight))
	rRewardAnchor := rewardstypes.DefaultRewardConfigVersion(rParams)
	rGen := &rewardstypes.GenesisState{
		Params: &rParams,
		State: &rewardstypes.RewardsState{
			CurrentEpoch: 1, CurrentEpochStartHeight: uint64(initialHeight),
			CumulativeEmitted: "0", CarryForwardRemainder: "0",
		},
		CurrentEpochConfig:              &rSnap,
		EpochConfigVersions:             []*rewardstypes.EpochConfigVersion{&rAnchor},
		RewardConfigVersions:            []*rewardstypes.RewardConfigVersion{&rRewardAnchor},
		PauseState:                      &rewardstypes.RewardsPauseState{},
		OutstandingEntitlementLiability: "0",
	}

	genMap := a.DefaultGenesis()
	genMap[coreslottypes.ModuleName] = cdc.MustMarshalJSON(csGen)
	genMap[rewardstypes.ModuleName] = cdc.MustMarshalJSON(rGen)
	appState, err := json.Marshal(genMap)
	require.NoError(t, err)

	_, err = a.InitChain(&abci.RequestInitChain{
		ChainId:         "",
		InitialHeight:   requestedHeight,
		ConsensusParams: sims.DefaultConsensusParams,
		AppStateBytes:   appState,
	})
	require.NoError(t, err)

	// The chain produces its first block from the genesis validator set; genesis
	// state is only readable through an uncached context once committed.
	_, err = a.FinalizeBlock(&abci.RequestFinalizeBlock{Height: initialHeight})
	require.NoError(t, err)
	_, err = a.Commit()
	require.NoError(t, err)

	ctx := a.NewUncachedContext(false, cmtproto.Header{Height: initialHeight})

	stored, err := a.CoreSlotKeeper.GetSlot(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, settlement, stored.SettlementAddress)
	require.Equal(t, uint64(1), stored.ActivationSequence)
	require.Equal(t, initialHeight, stored.ActivatedHeight)
	require.Equal(t, initialHeight, stored.ActivationEffectiveHeight,
		"fresh genesis is the explicit exception to the runtime H+1 rule")
	require.Equal(t, uint64(1), stored.CurrentSelectionPolicyVersion)
	require.Equal(t, int64(0), stored.LastSelectionPolicyUpdateHeight)

	// The ACTIVE membership index is populated and is the enumeration path.
	active, err := a.CoreSlotKeeper.GetActiveSlots(ctx)
	require.NoError(t, err)
	require.Len(t, active, 1)
	require.Equal(t, uint64(1), active[0].SlotId)

	// The immutable ceiling reached the app as a single named constant rather than
	// a scattered literal, and the shipped default sits within it.
	require.Equal(t, uint64(100), appparams.HardMaxActiveCoreSlots)
	storedParams, err := a.CoreSlotKeeper.Params.Get(ctx)
	require.NoError(t, err)
	require.LessOrEqual(t, storedParams.MaxActiveSlots, appparams.HardMaxActiveCoreSlots)
}

// TestInitChainComparesTheSuppliedValidatorSet closes the outer half of the §80
// validator-set contract.
//
// x/coreslot returns the validator set it derives from ACTIVE slots; BaseApp
// compares that against the set CometBFT supplies in RequestInitChain and refuses
// to start on any mismatch. The keeper tests prove the derivation; this proves the
// comparison actually runs, so the module's output is checked against the genesis
// document rather than silently trusted.
func TestInitChainComparesTheSuppliedValidatorSet(t *testing.T) {
	const initialHeight = int64(1)
	operator, payout, settlement := acc(2), acc(12), acc(22)
	consensusKey := ed25519Any(t, 7)

	buildState := func(t *testing.T, a *app.App) []byte {
		t.Helper()
		cdc := genesisCodec()
		csParams := coreslottypes.DefaultParams(app.AuthorityAddress(), app.EmergencyAuthorityAddress())
		csGen := &coreslottypes.GenesisState{
			Params: &csParams,
			Slots: []*coreslottypes.CoreSlot{{
				SlotId: 1, OperatorAddress: operator, PayoutAddress: payout,
				SettlementAddress: settlement,
				ConsensusPubkey:   consensusKey,
				Status:            coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE,
				ConsensusPower:    1, RewardWeight: coreslottypes.DefaultRewardWeight,
				ActivationSequence: 1, ActivatedHeight: initialHeight, ActivationEffectiveHeight: initialHeight,
				CurrentSelectionPolicyVersion: 1,
			}},
			SelectionPolicies: []*coreslottypes.SelectionPolicyVersion{{
				SlotId: 1, PolicyVersion: 1, SelectionRateBps: 2_500, MaxSelectedParticipants: 10,
				ValidFromHeight: initialHeight,
			}},
			RewardWeights: []*coreslottypes.OperatorRewardWeight{{SlotId: 1, FinalWeight: coreslottypes.DefaultRewardWeight}},
			NextSlotId:    2,
		}
		genMap := a.DefaultGenesis()
		genMap[coreslottypes.ModuleName] = cdc.MustMarshalJSON(csGen)
		appState, err := json.Marshal(genMap)
		require.NoError(t, err)
		return appState
	}

	cmtKey := func(t *testing.T, any *anypb.Any) cmtcrypto.PublicKey {
		t.Helper()
		pk, err := coreslotkeeper.DecodePubKey(any)
		require.NoError(t, err)
		proto, err := cryptocodec.ToCmtProtoPublicKey(pk)
		require.NoError(t, err)
		return proto
	}

	t.Run("matching validator set is accepted", func(t *testing.T) {
		a := bootApp(t)
		_, err := a.InitChain(&abci.RequestInitChain{
			ChainId:         "",
			ConsensusParams: sims.DefaultConsensusParams,
			AppStateBytes:   buildState(t, a),
			Validators: []abci.ValidatorUpdate{
				{PubKey: cmtKey(t, consensusKey), Power: 1},
			},
		})
		require.NoError(t, err)
	})

	t.Run("mismatching validator key is rejected", func(t *testing.T) {
		a := bootApp(t)
		_, err := a.InitChain(&abci.RequestInitChain{
			ChainId:         "",
			ConsensusParams: sims.DefaultConsensusParams,
			AppStateBytes:   buildState(t, a),
			Validators: []abci.ValidatorUpdate{
				// A key no CoreSlot holds.
				{PubKey: cmtKey(t, ed25519Any(t, 99)), Power: 1},
			},
		})
		require.Error(t, err, "a validator the module did not derive must not be accepted")
	})

	t.Run("mismatching validator power is rejected", func(t *testing.T) {
		a := bootApp(t)
		_, err := a.InitChain(&abci.RequestInitChain{
			ChainId:         "",
			ConsensusParams: sims.DefaultConsensusParams,
			AppStateBytes:   buildState(t, a),
			Validators: []abci.ValidatorUpdate{
				{PubKey: cmtKey(t, consensusKey), Power: 7},
			},
		})
		require.Error(t, err, "a power the module did not derive must not be accepted")
	})

	t.Run("extra supplied validator is rejected", func(t *testing.T) {
		a := bootApp(t)
		_, err := a.InitChain(&abci.RequestInitChain{
			ChainId:         "",
			ConsensusParams: sims.DefaultConsensusParams,
			AppStateBytes:   buildState(t, a),
			Validators: []abci.ValidatorUpdate{
				{PubKey: cmtKey(t, consensusKey), Power: 1},
				{PubKey: cmtKey(t, ed25519Any(t, 99)), Power: 1},
			},
		})
		require.Error(t, err, "nothing outside the ACTIVE set may appear in the initial validator set")
	})
}

// TestFreshV2GenesisRejectsNonNormalizedInput is the counterweight through the real
// application: a genesis that is not already normalized is refused rather than
// repaired. §80 says what a conforming fresh genesis looks like; it does not
// authorize manufacturing one.
func TestFreshV2GenesisRejectsNonNormalizedInput(t *testing.T) {
	cdc := genesisCodec()
	operator, payout, settlement := acc(2), acc(12), acc(22)

	conforming := func() *coreslottypes.GenesisState {
		csParams := coreslottypes.DefaultParams(app.AuthorityAddress(), app.EmergencyAuthorityAddress())
		return &coreslottypes.GenesisState{
			Params: &csParams,
			Slots: []*coreslottypes.CoreSlot{{
				SlotId: 1, OperatorAddress: operator, PayoutAddress: payout,
				SettlementAddress: settlement,
				ConsensusPubkey:   ed25519Any(t, 7),
				Status:            coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE,
				ConsensusPower:    1, RewardWeight: coreslottypes.DefaultRewardWeight,
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
	}

	for _, tc := range []struct {
		name   string
		mutate func(*coreslottypes.GenesisState)
	}{
		{"settlement address missing", func(g *coreslottypes.GenesisState) { g.Slots[0].SettlementAddress = "" }},
		{"settlement address is a module account", func(g *coreslottypes.GenesisState) {
			g.Slots[0].SettlementAddress = app.AuthorityAddress()
		}},
		{"activation generation not normalized", func(g *coreslottypes.GenesisState) { g.Slots[0].ActivationSequence = 0 }},
		{"activation heights not at the initial height", func(g *coreslottypes.GenesisState) {
			g.Slots[0].ActivatedHeight, g.Slots[0].ActivationEffectiveHeight = 5, 5
		}},
		{"selection policy missing", func(g *coreslottypes.GenesisState) { g.SelectionPolicies = nil }},
		{"inactive slot", func(g *coreslottypes.GenesisState) {
			g.Slots[0].Status = coreslottypes.SlotStatus_SLOT_STATUS_INACTIVE
			g.Slots[0].ConsensusPower = 0
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := bootApp(t)
			csGen := conforming()
			tc.mutate(csGen)

			genMap := a.DefaultGenesis()
			genMap[coreslottypes.ModuleName] = cdc.MustMarshalJSON(csGen)
			appState, err := json.Marshal(genMap)
			require.NoError(t, err)

			// The module panics on a genesis error, which is how a chain refuses to
			// start rather than booting into a state it cannot justify.
			require.Panics(t, func() {
				_, _ = a.InitChain(&abci.RequestInitChain{
					ChainId:         "",
					ConsensusParams: sims.DefaultConsensusParams,
					AppStateBytes:   appState,
				})
			})
		})
	}
}
