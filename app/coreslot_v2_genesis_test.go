package app_test

import (
	"encoding/json"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/testutil/sims"

	"github.com/twilight-project/twilight-core/app"
	appparams "github.com/twilight-project/twilight-core/app/params"
	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
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
	a := bootApp(t)
	cdc := genesisCodec()

	const initialHeight = int64(1)
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

	genMap := a.DefaultGenesis()
	genMap[coreslottypes.ModuleName] = cdc.MustMarshalJSON(csGen)
	appState, err := json.Marshal(genMap)
	require.NoError(t, err)

	_, err = a.InitChain(&abci.RequestInitChain{
		ChainId:         "",
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
