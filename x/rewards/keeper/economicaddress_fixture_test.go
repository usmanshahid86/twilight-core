package keeper_test

import (
	"testing"

	"cosmossdk.io/log"
	storetypes "cosmossdk.io/store/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/cosmos/cosmos-sdk/codec"
	addresscodec "github.com/cosmos/cosmos-sdk/codec/address"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil/integration"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/stretchr/testify/require"

	"github.com/twilight-project/twilight-core/internal/economicaddress"
	"github.com/twilight-project/twilight-core/x/rewards/keeper"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// Test fixture for the canonical economic-address capability. The module-account
// names are synthetic; see the equivalent fixture in x/coreslot for why.
const (
	testModuleAccountName      = "test-module-account"
	testOtherModuleAccountName = "test-other-module-account"
)

func testModuleAddress(name string) string {
	return authtypes.NewModuleAddress(name).String()
}

func testAccount(marker byte) string {
	raw := make([]byte, 20)
	raw[0] = marker
	raw[19] = marker
	return sdk.AccAddress(raw).String()
}

// zeroAddress is the all-zero account address: a well-formed encoding that no
// key controls. §25 requires an economic address to be non-zero, so it must be
// refused as a destination.
func zeroAddress() string {
	return sdk.AccAddress(make([]byte, 20)).String()
}

func testEconomicAddresses(t *testing.T, blocked ...string) economicaddress.Validator {
	t.Helper()
	blockedSet := make(map[string]bool, len(blocked))
	for _, address := range blocked {
		blockedSet[address] = true
	}
	validator, err := economicaddress.New(
		addresscodec.NewBech32Codec(sdk.GetConfig().GetBech32AccountAddrPrefix()),
		[]string{testModuleAccountName, testOtherModuleAccountName},
		blockedSet,
	)
	require.NoError(t, err)
	return validator
}

// setupKeeperWithBlocked mirrors setupKeeper but configures the injected
// validator with a synthetic bank-blocked set, so the bank-blocked branch can be
// exercised independently of module-account exclusion.
func setupKeeperWithBlocked(
	t *testing.T, coreSlotKeeper keeper.CoreSlotKeeper, blocked ...string,
) (keeper.Keeper, sdk.Context, *bankKeeperMock) {
	t.Helper()
	registry := codectypes.NewInterfaceRegistry()
	types.RegisterInterfaces(registry)
	cdc := codec.NewProtoCodec(registry)
	keys := storetypes.NewKVStoreKeys(types.StoreKey)
	cms := integration.CreateMultiStore(keys, log.NewNopLogger())
	ctx := sdk.NewContext(cms, cmtproto.Header{Height: 1}, false, log.NewNopLogger())
	bank := &bankKeeperMock{}
	k := keeper.NewKeeper(
		cdc,
		runtime.NewKVStoreService(keys[types.StoreKey]),
		accountKeeperMock{},
		bank,
		coreSlotKeeper,
		testEconomicAddresses(t, blocked...),
	)
	return k, ctx, bank
}
