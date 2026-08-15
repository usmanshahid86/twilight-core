package app_test

import (
	"testing"

	dbm "github.com/cosmos/cosmos-db"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/log"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	sdked25519 "github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	"github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	gogoproto "github.com/cosmos/gogoproto/proto"
	anypb "github.com/cosmos/gogoproto/types/any"

	"github.com/twilight-project/twilight-core/app"
	coreslotkeeper "github.com/twilight-project/twilight-core/x/coreslot/keeper"
	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	rewardstypes "github.com/twilight-project/twilight-core/x/rewards/types"
)

// End-to-end wiring of the canonical economic-address rule against the REAL app:
// the real module-account declaration, the real bank blocked set, and the real
// keepers. The keeper-level tests use synthetic module accounts to prove
// routing; these prove the routing is fed by the production authorities.

func newApp(t *testing.T) *app.App {
	t.Helper()
	return app.New(log.NewNopLogger(), dbm.NewMemDB(), nil, true, sims.EmptyAppOptions{})
}

func appContext(t *testing.T, a *app.App) sdk.Context {
	t.Helper()
	return a.NewUncachedContext(false, cmtproto.Header{Height: 1})
}

func ordinaryAccount(marker byte) string {
	raw := make([]byte, 20)
	raw[0] = marker
	raw[19] = marker
	return sdk.AccAddress(raw).String()
}

// TestModuleAccountNamesMatchAuthConfiguration is the anti-drift guard. The
// validator's module-account authority and the auth module's configuration must
// be the same declaration, so a module account cannot be added to one without
// the other.
func TestModuleAccountNamesMatchAuthConfiguration(t *testing.T) {
	names := app.ModuleAccountNames()
	require.NotEmpty(t, names)

	// Every name the app declares must resolve to a real module account in the
	// running application.
	a := newApp(t)
	for _, name := range names {
		address := a.AccountKeeper.GetModuleAddress(name)
		require.NotNilf(t, address, "module account %q is declared but absent from the app", name)
		require.Equalf(t, authtypes.NewModuleAddress(name), address,
			"module account %q address must be derivable from its name", name)
	}

	// The accounts this chain actually relies on are present.
	require.Subset(t, names, []string{
		authtypes.FeeCollectorName,
		app.AuthorityModuleName,
		app.EmergencyAuthorityModuleName,
		rewardstypes.ModuleName,
		rewardstypes.FeePoolName,
	})
}

// TestRealModuleAccountsRejectedAsEconomicAddresses runs a real module account
// through an actual admission path in each module.
func TestRealModuleAccountsRejectedAsEconomicAddresses(t *testing.T) {
	for _, name := range app.ModuleAccountNames() {
		moduleAddress := authtypes.NewModuleAddress(name).String()

		t.Run("coreslot payout rejects "+name, func(t *testing.T) {
			a := newApp(t)
			ctx := appContext(t, a)
			seedCoreSlotParams(t, a, ctx)

			// The PAYOUT address is the value destination. The operator address is
			// an identity and is deliberately not subject to this exclusion.
			msgs := coreslotkeeper.NewMsgServer(a.CoreSlotKeeper)
			_, err := msgs.RegisterCoreSlot(ctx, &coreslottypes.MsgRegisterCoreSlot{
				Authority:       app.AuthorityAddress(),
				OperatorAddress: ordinaryAccount(9),
				PayoutAddress:   moduleAddress,
				ConsensusPubkey: appPubkey(t, 2),
			})
			require.ErrorIs(t, err, coreslottypes.ErrInvalidAddress)
		})

		t.Run("rewards treasury rejects "+name, func(t *testing.T) {
			a := newApp(t)
			ctx := appContext(t, a)

			params := rewardstypes.DefaultParams()
			params.TreasuryAddress = moduleAddress
			params.EmissionTreasuryShareBps = 100
			require.ErrorIs(t, a.RewardsKeeper.SetParams(ctx, params), rewardstypes.ErrInvalidAddress)
		})
	}
}

// TestOrdinaryAccountAcceptedThroughRealPath is the counterweight: the rule must
// not have made ordinary operation impossible.
func TestOrdinaryAccountAcceptedThroughRealPath(t *testing.T) {
	a := newApp(t)
	ctx := appContext(t, a)
	seedCoreSlotParams(t, a, ctx)

	operator := ordinaryAccount(9)
	msgs := coreslotkeeper.NewMsgServer(a.CoreSlotKeeper)
	res, err := msgs.RegisterCoreSlot(ctx, &coreslottypes.MsgRegisterCoreSlot{
		Authority:       app.AuthorityAddress(),
		OperatorAddress: operator,
		PayoutAddress:   ordinaryAccount(10),
		ConsensusPubkey: appPubkey(t, 2),
	})
	require.NoError(t, err)

	slot, err := a.CoreSlotKeeper.GetSlot(ctx, res.SlotId)
	require.NoError(t, err)
	require.Equal(t, operator, slot.OperatorAddress)

	// And an ordinary treasury address with a positive share is accepted.
	params := rewardstypes.DefaultParams()
	params.TreasuryAddress = ordinaryAccount(11)
	params.EmissionTreasuryShareBps = 100
	require.NoError(t, a.RewardsKeeper.SetParams(ctx, params))
}

// TestControlPlaneAuthoritiesRemainValid is the guard that matters most: both
// authorities are module accounts, and the economic rule must not be applied to
// them. If it were, the chain could not govern itself.
func TestControlPlaneAuthoritiesRemainValid(t *testing.T) {
	a := newApp(t)
	ctx := appContext(t, a)
	seedCoreSlotParams(t, a, ctx)

	// Both authorities really are module accounts — otherwise this test proves
	// nothing about the exemption.
	authority := app.AuthorityAddress()
	emergency := app.EmergencyAuthorityAddress()
	require.Contains(t, app.ModuleAccountNames(), app.AuthorityModuleName)
	require.Contains(t, app.ModuleAccountNames(), app.EmergencyAuthorityModuleName)

	stored, err := a.CoreSlotKeeper.Params.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, authority, stored.Authority)
	require.Equal(t, emergency, stored.EmergencyAuthority)

	// The module-account authority still authorizes a registration.
	msgs := coreslotkeeper.NewMsgServer(a.CoreSlotKeeper)
	_, err = msgs.RegisterCoreSlot(ctx, &coreslottypes.MsgRegisterCoreSlot{
		Authority:       authority,
		OperatorAddress: ordinaryAccount(12),
		PayoutAddress:   ordinaryAccount(13),
		ConsensusPubkey: appPubkey(t, 3),
	})
	require.NoError(t, err, "the module-account authority must remain a valid control-plane identity")
}

// TestDefaultGenesisStillInitialises pins that app startup is unaffected: the
// default rewards configuration carries no treasury address and zero shares.
func TestDefaultGenesisStillInitialises(t *testing.T) {
	a := newApp(t)
	ctx := appContext(t, a)

	require.NoError(t, a.RewardsKeeper.InitGenesis(ctx, *rewardstypes.DefaultGenesis()))

	params, err := a.RewardsKeeper.GetParams(ctx)
	require.NoError(t, err)
	require.Empty(t, params.TreasuryAddress)
	require.Zero(t, params.EmissionTreasuryShareBps)
}

func seedCoreSlotParams(t *testing.T, a *app.App, ctx sdk.Context) {
	t.Helper()
	params := coreslottypes.DefaultParams(app.AuthorityAddress(), app.EmergencyAuthorityAddress())
	require.NoError(t, a.CoreSlotKeeper.Params.Set(ctx, params))
}

func appPubkey(t *testing.T, marker byte) *anypb.Any {
	t.Helper()
	raw := make([]byte, sdked25519.PubKeySize)
	raw[0] = marker
	key := &sdked25519.PubKey{Key: raw}
	encoded, err := gogoproto.Marshal(key)
	require.NoError(t, err)
	return &anypb.Any{TypeUrl: "/cosmos.crypto.ed25519.PubKey", Value: encoded}
}
