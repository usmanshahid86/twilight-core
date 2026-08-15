package keeper_test

import (
	"encoding/base64"
	"testing"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/log"
	storetypes "cosmossdk.io/store/types"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	sdked25519 "github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil/integration"
	sdk "github.com/cosmos/cosmos-sdk/types"
	gogoproto "github.com/cosmos/gogoproto/proto"
	anypb "github.com/cosmos/gogoproto/types/any"

	"github.com/twilight-project/twilight-core/x/coreslot/keeper"
	"github.com/twilight-project/twilight-core/x/coreslot/types"
)

func setup(t *testing.T, blocked ...string) (keeper.Keeper, sdk.Context, string, string) {
	t.Helper()
	registry := codectypes.NewInterfaceRegistry()
	types.RegisterInterfaces(registry)
	cdc := codec.NewProtoCodec(registry)
	keys := storetypes.NewKVStoreKeys(types.StoreKey)
	key := keys[types.StoreKey]
	cms := integration.CreateMultiStore(keys, log.NewNopLogger())
	ctx := sdk.NewContext(cms, cmtproto.Header{Height: 1}, false, log.NewNopLogger())
	k := keeper.NewKeeper(cdc, runtime.NewKVStoreService(key), testEconomicAddresses(t, blocked...))
	authority := sdk.AccAddress(make([]byte, 20)).String()
	emergency := sdk.AccAddress(append([]byte{1}, make([]byte, 19)...)).String()
	return k, ctx, authority, emergency
}

func pubkey(t *testing.T, marker byte) *anypb.Any {
	t.Helper()
	key := make([]byte, sdked25519.PubKeySize)
	key[0] = marker
	bz, err := gogoproto.Marshal(&sdked25519.PubKey{Key: key})
	require.NoError(t, err)
	return &anypb.Any{TypeUrl: "/cosmos.crypto.ed25519.PubKey", Value: bz}
}

func slot(t *testing.T, id uint64, operator string, marker byte, status types.SlotStatus, power int64) *types.CoreSlot {
	t.Helper()
	return &types.CoreSlot{SlotId: id, OperatorAddress: operator, PayoutAddress: operator, ConsensusPubkey: pubkey(t, marker), Status: status, ConsensusPower: power, RewardWeight: types.DefaultRewardWeight}
}

func TestGenesisAndCanonicalUpdates(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	params := types.DefaultParams(authority, emergency)
	op1 := sdk.AccAddress(append([]byte{2}, make([]byte, 19)...)).String()
	op2 := sdk.AccAddress(append([]byte{3}, make([]byte, 19)...)).String()
	genesis := &types.GenesisState{Params: &params, Slots: []*types.CoreSlot{
		slot(t, 2, op2, 2, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
		slot(t, 1, op1, 1, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
	}, NextSlotId: 3}
	updates, err := k.InitGenesis(ctx, genesis)
	require.NoError(t, err)
	require.Len(t, updates, 2)
	first, err := cryptocodec.FromCmtProtoPublicKey(updates[0].PubKey)
	require.NoError(t, err)
	second, err := cryptocodec.FromCmtProtoPublicKey(updates[1].PubKey)
	require.NoError(t, err)
	require.Less(t, string(first.Address()), string(second.Address()))
}

func TestLifecycleAndActiveKeyRotation(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	params := types.DefaultParams(authority, emergency)
	params.MinActiveSlots = 1
	op1 := sdk.AccAddress(append([]byte{2}, make([]byte, 19)...)).String()
	op2 := sdk.AccAddress(append([]byte{3}, make([]byte, 19)...)).String()
	_, err := k.InitGenesis(ctx, &types.GenesisState{Params: &params, Slots: []*types.CoreSlot{
		slot(t, 1, op1, 1, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
		slot(t, 2, op2, 2, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
	}, NextSlotId: 3})
	require.NoError(t, err)

	msgs := keeper.NewMsgServer(k)
	_, err = msgs.InactivateCoreSlot(ctx, &types.MsgInactivateCoreSlot{AuthorityOrOperator: authority, SlotId: 2, Reason: "maintenance"})
	require.NoError(t, err)
	updates, err := k.EndBlock(ctx)
	require.NoError(t, err)
	require.Len(t, updates, 1)
	require.Zero(t, updates[0].Power)

	_, err = msgs.RotateConsensusKey(ctx, &types.MsgRotateConsensusKey{Authority: authority, SlotId: 1, NewConsensusPubkey: pubkey(t, 9)})
	require.NoError(t, err)
	ctx = ctx.WithBlockHeight(2)
	updates, err = k.EndBlock(ctx)
	require.NoError(t, err)
	require.Len(t, updates, 2)
	require.Equal(t, int64(0), updates[0].Power+updates[1].Power-1)
}

func TestDuplicateConsensusKeyRejected(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	params := types.DefaultParams(authority, emergency)
	op1 := sdk.AccAddress(append([]byte{2}, make([]byte, 19)...)).String()
	_, err := k.InitGenesis(ctx, &types.GenesisState{Params: &params, Slots: []*types.CoreSlot{slot(t, 1, op1, 1, types.SlotStatus_SLOT_STATUS_ACTIVE, 1)}, NextSlotId: 2})
	require.NoError(t, err)
	msgs := keeper.NewMsgServer(k)
	op2 := sdk.AccAddress(append([]byte{3}, make([]byte, 19)...)).String()
	_, err = msgs.RegisterCoreSlot(ctx, &types.MsgRegisterCoreSlot{Authority: authority, OperatorAddress: op2, PayoutAddress: op2, ConsensusPubkey: pubkey(t, 1)})
	require.ErrorIs(t, err, types.ErrDuplicateConsensusKey)
}

func TestPubKeyFixtureIsStable(t *testing.T) {
	require.NotEmpty(t, base64.StdEncoding.EncodeToString(pubkey(t, 1).Value))
}
