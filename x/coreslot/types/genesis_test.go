package types_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	sdked25519 "github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"
	gogoproto "github.com/cosmos/gogoproto/proto"
	anypb "github.com/cosmos/gogoproto/types/any"

	"github.com/nyks/nyks-core/x/coreslot/types"
)

func testPubKey(t *testing.T, marker byte) *anypb.Any {
	t.Helper()
	key := make([]byte, sdked25519.PubKeySize)
	key[0] = marker
	bz, err := gogoproto.Marshal(&sdked25519.PubKey{Key: key})
	require.NoError(t, err)
	return &anypb.Any{TypeUrl: "/cosmos.crypto.ed25519.PubKey", Value: bz}
}

// activeGenesis builds a single-active-slot genesis that passes Validate, so
// each negative test can perturb exactly one field.
func activeGenesis(t *testing.T) (*types.GenesisState, *types.CoreSlot) {
	t.Helper()
	a := sdk.AccAddress(make([]byte, 20)).String()
	b := sdk.AccAddress(append([]byte{1}, make([]byte, 19)...)).String()
	op := sdk.AccAddress(append([]byte{2}, make([]byte, 19)...)).String()
	params := types.DefaultParams(a, b)
	slot := &types.CoreSlot{
		SlotId: 1, OperatorAddress: op, PayoutAddress: op, ConsensusPubkey: testPubKey(t, 1),
		Status: types.SlotStatus_SLOT_STATUS_ACTIVE, ConsensusPower: params.SlotVotingPower, RewardWeight: types.DefaultRewardWeight,
	}
	genesis := &types.GenesisState{Params: &params, Slots: []*types.CoreSlot{slot}, NextSlotId: 2}
	require.NoError(t, genesis.Validate())
	return genesis, slot
}

func TestParamsValidation(t *testing.T) {
	a := sdk.AccAddress(make([]byte, 20)).String()
	b := sdk.AccAddress(append([]byte{1}, make([]byte, 19)...)).String()
	params := types.DefaultParams(a, b)
	require.NoError(t, params.Validate())

	params.SlotVotingPower = 0
	require.Error(t, params.Validate())
}

func TestGenesisRejectsMissingActiveSlots(t *testing.T) {
	a := sdk.AccAddress(make([]byte, 20)).String()
	b := sdk.AccAddress(append([]byte{1}, make([]byte, 19)...)).String()
	genesis := types.DefaultGenesis(a, b)
	require.Error(t, genesis.Validate())
}

func TestGenesisRejectsMismatchedLastAppliedValidators(t *testing.T) {
	genesis, slot := activeGenesis(t)
	// Correct address but wrong power must be rejected (F7).
	genesis.LastAppliedValidators = []*types.LastAppliedValidator{{
		SlotId: slot.SlotId, ConsensusPubkey: slot.ConsensusPubkey, Power: slot.ConsensusPower + 1,
	}}
	require.ErrorIs(t, genesis.Validate(), types.ErrInvalidGenesis)

	// A last-applied entry that is not an active slot must also be rejected.
	genesis.LastAppliedValidators = []*types.LastAppliedValidator{{
		SlotId: 99, ConsensusPubkey: testPubKey(t, 7), Power: slot.ConsensusPower,
	}}
	require.ErrorIs(t, genesis.Validate(), types.ErrInvalidGenesis)
}

func TestGenesisAcceptsMatchingLastAppliedValidators(t *testing.T) {
	genesis, slot := activeGenesis(t)
	genesis.LastAppliedValidators = []*types.LastAppliedValidator{{
		SlotId: slot.SlotId, ConsensusPubkey: slot.ConsensusPubkey, Power: slot.ConsensusPower,
	}}
	require.NoError(t, genesis.Validate())
}

func TestGenesisRejectsInvalidRewardWeightSlotReference(t *testing.T) {
	genesis, _ := activeGenesis(t)
	genesis.RewardWeights = []*types.OperatorRewardWeight{{SlotId: 999, FinalWeight: types.DefaultRewardWeight}}
	require.ErrorIs(t, genesis.Validate(), types.ErrInvalidGenesis)
}

func TestGenesisRejectsDuplicateReservedConsensusAddress(t *testing.T) {
	genesis, _ := activeGenesis(t)
	addr := []byte{0xaa, 0xbb, 0xcc}
	genesis.ReservedConsensusAddresses = []*types.ReservedConsensusAddress{
		{ConsAddress: addr, SlotId: 1, ReservedUntil: 10},
		{ConsAddress: addr, SlotId: 1, ReservedUntil: 20},
	}
	require.ErrorIs(t, genesis.Validate(), types.ErrInvalidGenesis)
}
