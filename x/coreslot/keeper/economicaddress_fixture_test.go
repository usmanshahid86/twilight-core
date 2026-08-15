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
	"github.com/twilight-project/twilight-core/x/coreslot/keeper"
	"github.com/twilight-project/twilight-core/x/coreslot/types"
)

// Test fixture for the canonical economic-address capability.
//
// The module-account names here are SYNTHETIC on purpose. These tests prove that
// the keeper routes its admission decisions through the injected validator; which
// module accounts a real deployment has is the app's business, and asserting the
// production list here would create exactly the second copy §25 forbids. The
// app-level tests cover the real names.
const (
	testModuleAccountName      = "test-module-account"
	testOtherModuleAccountName = "test-other-module-account"
)

// testModuleAddress returns the address of a synthetic module account, derived
// the same deterministic way the validator derives them.
func testModuleAddress(name string) string {
	return authtypes.NewModuleAddress(name).String()
}

// testAccount returns an ordinary account address that is neither a module
// account nor blocked.
func testAccount(marker byte) string {
	raw := make([]byte, 20)
	raw[0] = marker
	raw[19] = marker
	return sdk.AccAddress(raw).String()
}

// setupWithoutEconomicAddresses builds a keeper holding the ZERO value of the
// capability, to prove an unwired keeper fails closed rather than admitting
// everything. It is the only place such a keeper is constructed.
func setupWithoutEconomicAddresses(t *testing.T) (keeper.Keeper, sdk.Context, string, string) {
	t.Helper()
	registry := codectypes.NewInterfaceRegistry()
	types.RegisterInterfaces(registry)
	cdc := codec.NewProtoCodec(registry)
	keys := storetypes.NewKVStoreKeys(types.StoreKey)
	cms := integration.CreateMultiStore(keys, log.NewNopLogger())
	ctx := sdk.NewContext(cms, cmtproto.Header{Height: 1}, false, log.NewNopLogger())
	k := keeper.NewKeeper(cdc, runtime.NewKVStoreService(keys[types.StoreKey]), economicaddress.Validator{})
	return k, ctx, testAccount(1), testAccount(2)
}

// testEconomicAddresses builds a real validator through the real constructor.
// The codec follows whatever account prefix the test environment is configured
// with, so the fixture cannot drift from the addresses the tests generate.
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
