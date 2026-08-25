package keeper_test

import (
	"encoding/base64"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
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
	k, ctx, authority, emergency, _ := setupWithRawStore(t, blocked...)
	return k, ctx, authority, emergency
}

// setupWithRawStore additionally returns the module's store key, so a test can
// reach the raw keyspace beneath the collections API. That is only appropriate
// for proving what a read path does NOT touch: a value written as raw bytes can
// be made undecodable, which the typed API cannot express.
func setupWithRawStore(t *testing.T, blocked ...string) (keeper.Keeper, sdk.Context, string, string, *storetypes.KVStoreKey) {
	t.Helper()
	registry := codectypes.NewInterfaceRegistry()
	types.RegisterInterfaces(registry)
	cdc := codec.NewProtoCodec(registry)
	keys := storetypes.NewKVStoreKeys(types.StoreKey)
	key := keys[types.StoreKey]
	cms := integration.CreateMultiStore(keys, log.NewNopLogger())
	ctx := sdk.NewContext(cms, cmtproto.Header{Height: 1}, false, log.NewNopLogger())
	k := keeper.NewKeeper(cdc, runtime.NewKVStoreService(key), testEconomicAddresses(t, blocked...), nil)
	authority := sdk.AccAddress(make([]byte, 20)).String()
	emergency := sdk.AccAddress(append([]byte{1}, make([]byte, 19)...)).String()
	return k, ctx, authority, emergency, key
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
	return &types.CoreSlot{
		SlotId: id, OperatorAddress: operator, PayoutAddress: operator, SettlementAddress: testSettlement(marker),
		ConsensusPubkey: pubkey(t, marker), Status: status, ConsensusPower: power, RewardWeight: types.DefaultRewardWeight,
	}
}

// testSettlement returns an ordinary account address for use as a settlement
// credential. It is deliberately independent of the operator and payout addresses
// so that a test which makes one of those inadmissible leaves the settlement
// address valid, and the rejection it observes names the field it is about.
func testSettlement(marker byte) string {
	raw := make([]byte, 20)
	raw[0] = marker
	raw[1] = 0xA5
	return sdk.AccAddress(raw).String()
}

// testInitialHeight is the block height every keeper test's context starts at, and
// therefore the initial height a fresh genesis in these tests normalizes against.
const testInitialHeight = int64(1)

// initGenesis imports a fixture after filling in the fresh-V2 fields it does not
// state explicitly. Tests that are ABOUT genesis rejection call k.InitGenesis
// directly with an input they built themselves, so nothing here can mask the
// condition under test.
func initGenesis(t *testing.T, k keeper.Keeper, ctx sdk.Context, genesis *types.GenesisState) ([]abci.ValidatorUpdate, error) {
	t.Helper()
	return k.InitGenesis(ctx, freshGenesis(t, genesis))
}

// freshGenesis fills in the fresh-V2 genesis fields a fixture does not care about,
// so a test about lifecycle or events does not have to restate the whole §80
// contract. It is a TEST FIXTURE HELPER, not a production normalizer: InitGenesis
// deliberately rejects a non-conforming genesis rather than repairing it, and any
// test that is about that rejection must build its input directly instead of
// going through here.
//
// Only absent values are supplied. A fixture that sets a field on purpose — to
// assert it is rejected, or to pin an exact value — keeps what it set.
func freshGenesis(t *testing.T, genesis *types.GenesisState) *types.GenesisState {
	t.Helper()
	var maxID uint64
	for _, s := range genesis.Slots {
		if s == nil {
			continue
		}
		if s.SlotId > maxID {
			maxID = s.SlotId
		}
		if s.SettlementAddress == "" {
			s.SettlementAddress = s.PayoutAddress
		}
		if s.CurrentSelectionPolicyVersion == 0 {
			s.CurrentSelectionPolicyVersion = 1
		}
		if s.Status == types.SlotStatus_SLOT_STATUS_ACTIVE {
			if s.ActivationSequence == 0 {
				s.ActivationSequence = 1
			}
			if s.ActivatedHeight == 0 {
				s.ActivatedHeight = testInitialHeight
			}
			if s.ActivationEffectiveHeight == 0 {
				s.ActivationEffectiveHeight = testInitialHeight
			}
		}
		if !hasPolicyFor(genesis, s.SlotId) {
			genesis.SelectionPolicies = append(genesis.SelectionPolicies, &types.SelectionPolicyVersion{
				SlotId: s.SlotId, PolicyVersion: 1, SelectionRateBps: 2_500, MaxSelectedParticipants: 10,
				ValidFromHeight: testInitialHeight,
			})
		}
	}
	if genesis.NextSlotId <= maxID {
		genesis.NextSlotId = maxID + 1
	}
	return genesis
}

func hasPolicyFor(genesis *types.GenesisState, slotID uint64) bool {
	for _, p := range genesis.SelectionPolicies {
		if p != nil && p.SlotId == slotID {
			return true
		}
	}
	return false
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
	updates, err := initGenesis(t, k, ctx, genesis)
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
	_, err := initGenesis(t, k, ctx, &types.GenesisState{Params: &params, Slots: []*types.CoreSlot{
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
	_, err := initGenesis(t, k, ctx, &types.GenesisState{Params: &params, Slots: []*types.CoreSlot{slot(t, 1, op1, 1, types.SlotStatus_SLOT_STATUS_ACTIVE, 1)}, NextSlotId: 2})
	require.NoError(t, err)
	msgs := keeper.NewMsgServer(k)
	op2 := sdk.AccAddress(append([]byte{3}, make([]byte, 19)...)).String()
	_, err = msgs.RegisterCoreSlot(ctx, registerMsg(t, authority, op2, op2, 1))
	require.ErrorIs(t, err, types.ErrDuplicateConsensusKey)
}

func TestPubKeyFixtureIsStable(t *testing.T) {
	require.NotEmpty(t, base64.StdEncoding.EncodeToString(pubkey(t, 1).Value))
}
