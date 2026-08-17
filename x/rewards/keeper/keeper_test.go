package keeper_test

import (
	"context"
	"errors"
	"testing"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/log"
	storetypes "cosmossdk.io/store/types"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil/integration"
	sdk "github.com/cosmos/cosmos-sdk/types"

	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	"github.com/twilight-project/twilight-core/x/rewards/keeper"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

type accountKeeperMock struct{}

func (accountKeeperMock) GetModuleAddress(string) sdk.AccAddress {
	return sdk.AccAddress(make([]byte, 20))
}

type bankKeeperMock struct {
	mintCalls int
	sendCalls int
	mintErr   error
	sendErr   error
	minted    sdk.Coins
	sends     []bankSend
	supply    sdk.Coin
	balances  map[string]sdk.Coin
}

type bankSend struct {
	recipient sdk.AccAddress
	amounts   sdk.Coins
}

func (m *bankKeeperMock) MintCoins(_ context.Context, _ string, amounts sdk.Coins) error {
	m.mintCalls++
	if m.mintErr != nil {
		return m.mintErr
	}
	m.minted = m.minted.Add(amounts...)
	return nil
}

func (m *bankKeeperMock) SendCoinsFromModuleToAccount(_ context.Context, _ string, recipient sdk.AccAddress, amounts sdk.Coins) error {
	m.sendCalls++
	if m.sendErr != nil {
		return m.sendErr
	}
	m.sends = append(m.sends, bankSend{recipient: recipient, amounts: amounts})
	return nil
}

func (m *bankKeeperMock) GetSupply(_ context.Context, denom string) sdk.Coin {
	if !m.supply.IsNil() {
		return m.supply
	}
	return sdk.NewInt64Coin(denom, 0)
}

func (m *bankKeeperMock) GetBalance(_ context.Context, address sdk.AccAddress, denom string) sdk.Coin {
	if coin, ok := m.balances[address.String()]; ok {
		return coin
	}
	return sdk.NewInt64Coin(denom, 0)
}

type coreSlotKeeperMock struct {
	slots     map[uint64]coreslottypes.CoreSlot
	weights   map[uint64]coreslottypes.OperatorRewardWeight
	active    []coreslottypes.CoreSlot
	authority string
	emergency string
}

func (m *coreSlotKeeperMock) GetActiveSlots(context.Context) ([]coreslottypes.CoreSlot, error) {
	return append([]coreslottypes.CoreSlot(nil), m.active...), nil
}

func (m *coreSlotKeeperMock) GetSlot(_ context.Context, slotID uint64) (coreslottypes.CoreSlot, error) {
	slot, ok := m.slots[slotID]
	if !ok {
		return coreslottypes.CoreSlot{}, coreslottypes.ErrSlotNotFound
	}
	return slot, nil
}

func (m *coreSlotKeeperMock) GetRewardWeight(_ context.Context, slotID uint64) (coreslottypes.OperatorRewardWeight, error) {
	weight, ok := m.weights[slotID]
	if !ok {
		return coreslottypes.OperatorRewardWeight{}, coreslottypes.ErrSlotNotFound
	}
	return weight, nil
}

func (m *coreSlotKeeperMock) GetAuthority(context.Context) (string, error) {
	return m.authority, nil
}

func (m *coreSlotKeeperMock) GetEmergencyAuthority(context.Context) (string, error) {
	return m.emergency, nil
}

func setupKeeper(t *testing.T, coreSlotKeeper keeper.CoreSlotKeeper) (keeper.Keeper, sdk.Context, *bankKeeperMock) {
	t.Helper()
	registry := codectypes.NewInterfaceRegistry()
	types.RegisterInterfaces(registry)
	cdc := codec.NewProtoCodec(registry)
	keys := storetypes.NewKVStoreKeys(types.StoreKey)
	cms := integration.CreateMultiStore(keys, log.NewNopLogger())
	ctx := sdk.NewContext(cms, cmtproto.Header{Height: 1}, false, log.NewNopLogger())
	bank := &bankKeeperMock{}
	k := keeper.NewKeeper(cdc, runtime.NewKVStoreService(keys[types.StoreKey]), accountKeeperMock{}, bank, coreSlotKeeper, testEconomicAddresses(t))
	// The canonical pause state and the open counter have no defaults: after
	// genesis an absent one is corruption, so every fixture must establish them
	// exactly as InitGenesis would.
	require.NoError(t, k.SetPauseState(ctx, types.RewardsPauseState{}))
	require.NoError(t, k.SetOpenRewardEnabledBlocks(ctx, 0))
	return k, ctx, bank
}

// seedEpochTimeline writes a canonical epoch-configuration history consistent
// with the fixture's open epoch, so geometry resolves the same way it would on a
// real chain. The anchor is version 1 effective at the fixture's own epoch, which
// is all the resolver needs; only fresh genesis additionally requires that epoch
// to be 1.
func seedEpochTimeline(
	t *testing.T, k keeper.Keeper, ctx sdk.Context, params types.Params, state types.RewardsState,
) {
	t.Helper()
	require.NoError(t, k.EpochConfigVersions.Set(ctx, state.CurrentEpoch, types.EpochConfigVersion{
		Version:              1,
		EffectiveEpoch:       state.CurrentEpoch,
		EffectiveStartHeight: state.CurrentEpochStartHeight,
		EpochLengthBlocks:    params.EpochLengthBlocks,
	}))
}

// seedRewardConfigTimeline writes the canonical reward-configuration anchor.
//
// Always version 1 at effective epoch 1, whatever epoch the fixture opens at.
// That differs from seedEpochTimeline, which anchors geometry at the fixture's
// own epoch: the reward anchor is the permanent bootstrap version targets 1 and 2
// resolve to, so a fixture that anchored it elsewhere would be testing against a
// history no chain can have.
func seedRewardConfigTimeline(t *testing.T, k keeper.Keeper, ctx sdk.Context, params types.Params) {
	t.Helper()
	require.NoError(t, k.RewardConfigVersions.Set(ctx, 1, types.DefaultRewardConfigVersion(params)))
}

func (m *bankKeeperMock) failMint() {
	m.mintErr = errors.New("mint failed")
}

func (m *bankKeeperMock) failSend() {
	m.sendErr = errors.New("send failed")
}

func addr(marker byte) string {
	address := make([]byte, 20)
	address[0] = marker
	return sdk.AccAddress(address).String()
}

func hasEvent(ctx sdk.Context, eventType string) bool {
	for _, event := range ctx.EventManager().Events() {
		if event.Type == eventType {
			return true
		}
	}
	return false
}

func validEpoch(number uint64, params types.Params) types.EpochReward {
	snapshot := types.DefaultEpochConfigSnapshot(params)
	return types.EpochReward{
		EpochNumber: number, StartHeight: 1, EndHeight: 10, MintedEmission: "1", CarryIn: "0",
		DistributableFees: "0", TreasuryAmount: "0", RewardPool: "1", AllocatedAmount: "1",
		CarryOut: "0", DistributionMethod: params.DistributionMethod, RemainderPolicy: params.RemainderPolicy,
		CumulativeEmittedAfterEpoch: "1", Config: &snapshot,
	}
}

func validClaim(slotID, epoch uint64) types.EligibleSlotReward {
	return types.EligibleSlotReward{
		SlotId: slotID, EpochNumber: epoch, OperatorAddress: addr(byte(slotID + 1)), PayoutAddress: addr(byte(slotID + 2)),
		BlocksActive: 10, RewardWeight: coreslottypes.DefaultRewardWeight, EffectiveWeight: "10", Amount: "1",
	}
}

func TestKeeperSchemaConstructs(t *testing.T) {
	k, _, _ := setupKeeper(t, &coreSlotKeeperMock{})
	require.NotNil(t, k.Schema)
}
