package app_test

import (
	"sort"
	"testing"

	dbm "github.com/cosmos/cosmos-db"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/log"
	sdkmath "cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	bankkeeper "github.com/cosmos/cosmos-sdk/x/bank/keeper"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"

	"github.com/twilight-project/twilight-core/app"
	"github.com/twilight-project/twilight-core/app/params"
	"github.com/twilight-project/twilight-core/internal/payoutledger"
	miningtypes "github.com/twilight-project/twilight-core/x/mining/types"
	rewardstypes "github.com/twilight-project/twilight-core/x/rewards/types"
)

// ---------------------------------------------------------------------------
// fixture
// ---------------------------------------------------------------------------

// fundingApp builds the app AND applies the shipped bank params.
//
// The params matter. IsSendEnabledCoins is enforced in bank's msgServer, not in
// SendCoins, and an app built without InitGenesis leaves DefaultSendEnabled at
// Go's zero value false. Tests that drove the keeper directly therefore passed
// while MsgSend would have been refused outright — proving the restriction only
// on a path no transaction takes. These tests go through the real MsgServer, so
// the fixture has to carry the real params.
func fundingApp(t *testing.T) (*app.App, sdk.Context, banktypes.MsgServer) {
	t.Helper()
	a := app.New(log.NewNopLogger(), dbm.NewMemDB(), nil, true, sims.EmptyAppOptions{})
	ctx := a.NewUncachedContext(false, cmtproto.Header{Height: 1})

	require.NoError(t, a.BankKeeper.SetParams(ctx, banktypes.DefaultParams()))
	// Asserted rather than assumed: if utwlt were not sendable, every refusal
	// below would pass for the wrong reason.
	require.True(t, a.BankKeeper.IsSendEnabledDenom(ctx, app.BaseDenom),
		"the fixture must permit utwlt sends, or these tests prove nothing")

	return a, ctx, bankkeeper.NewMsgServerImpl(a.BankKeeper)
}

func coins(n int64) sdk.Coins { return sdk.NewCoins(sdk.NewCoin(app.BaseDenom, sdkmath.NewInt(n))) }

// minimum is the threshold as a plain int64, for arithmetic in the cases below.
func minimum() int64 { return app.MinimumAccountFunding().Int64() }

// addrOf returns a deterministic account address that does not exist yet.
func addrOf(marker ...byte) sdk.AccAddress {
	raw := make([]byte, 20)
	copy(raw, marker)
	raw[19] = marker[len(marker)-1]
	return sdk.AccAddress(raw)
}

// fundAccount puts a spendable balance behind an ordinary address. rewards is
// the only module account carrying minter permission on this chain.
func fundAccount(t *testing.T, a *app.App, ctx sdk.Context, addr sdk.AccAddress, amount int64) {
	t.Helper()
	require.NoError(t, a.BankKeeper.MintCoins(ctx, rewardstypes.ModuleName, coins(amount)))
	require.NoError(t, a.BankKeeper.SendCoinsFromModuleToAccount(ctx, rewardstypes.ModuleName, addr, coins(amount)))
}

func send(msgServer banktypes.MsgServer, ctx sdk.Context, from, to sdk.AccAddress, amount int64) error {
	_, err := msgServer.Send(ctx, &banktypes.MsgSend{
		FromAddress: from.String(),
		ToAddress:   to.String(),
		Amount:      coins(amount),
	})
	return err
}

// ---------------------------------------------------------------------------
// the threshold relation
// ---------------------------------------------------------------------------

// EXACT equality, not a one-sided bound.
//
// A <= assertion would still hold if this floor were quietly halved, and no
// other test could see that: every other test derives its amounts from
// MinimumAccountFunding, so they all move with the mutation. Equality is the
// only statement here that a weakening cannot satisfy.
//
// Asserted between the two canonical functions rather than against a restated
// 10_000, so a literal copied into the test cannot agree with a literal copied
// into the implementation.
func TestTheFundingMinimumIsExactlyTheSettlementFloor(t *testing.T) {
	require.True(t, app.MinimumAccountFunding().Equal(params.HardMinSettlementPayoutAmount()),
		"the anti-spam floor and the hard settlement floor must be the same number, "+
			"got %s and %s",
		app.MinimumAccountFunding(), params.HardMinSettlementPayoutAmount())
}

// And the consequence that equality exists to guarantee: no payout the chain is
// willing to settle can be too small to open the account it pays into.
func TestNoAcceptableSettlementFloorIsUnpayable(t *testing.T) {
	hardMin := params.HardMinSettlementPayoutAmount()

	for _, configured := range []sdkmath.Int{
		hardMin,
		hardMin.AddRaw(1),
		sdkmath.NewIntFromUint64(1_000_000),
	} {
		require.NoError(t, params.ValidateMinRecipientPayoutAmount(configured, hardMin),
			"test fixture error: %s is not an acceptable configured floor", configured)
		require.True(t, app.MinimumAccountFunding().LTE(configured),
			"a settlement at the configured floor %s could not open an account", configured)
	}

	shipped, ok := sdkmath.NewIntFromString(miningtypes.DefaultMinRecipientPayoutAmount)
	require.True(t, ok, "the shipped default must parse")
	require.True(t, app.MinimumAccountFunding().LTE(shipped),
		"the default min_recipient_payout_amount could not open an account")
}

// ---------------------------------------------------------------------------
// the rule, through real message admission
// ---------------------------------------------------------------------------

func TestNewAccountBelowTheMinimumIsRefused(t *testing.T) {
	a, ctx, msgServer := fundingApp(t)
	funder := addrOf(0x11)
	fundAccount(t, a, ctx, funder, 1_000_000)

	for _, tc := range []struct {
		name   string
		marker byte
		amount int64
	}{
		{"one below the minimum", 0x21, minimum() - 1},
		{"a single unit", 0x22, 1},
		{"dust", 0x23, 99},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fresh := addrOf(tc.marker)
			require.False(t, a.AccountKeeper.HasAccount(ctx, fresh))

			err := send(msgServer, ctx, funder, fresh, tc.amount)
			require.Error(t, err, "an account below the minimum must not be created")
			require.Contains(t, err.Error(), "requires at least")
			require.False(t, a.AccountKeeper.HasAccount(ctx, fresh),
				"a refused send must leave no account behind")
		})
	}
}

// Exactly at the boundary must be ACCEPTED: a settlement pays exactly the
// configured floor to a first-time recipient.
func TestNewAccountAtExactlyTheMinimumIsAllowed(t *testing.T) {
	a, ctx, msgServer := fundingApp(t)
	funder, fresh := addrOf(0x31), addrOf(0x32)
	fundAccount(t, a, ctx, funder, 1_000_000)

	require.NoError(t, send(msgServer, ctx, funder, fresh, minimum()),
		"the settlement floor itself must be able to open an account")
	require.True(t, a.AccountKeeper.HasAccount(ctx, fresh))
}

// The rule must not restrict ordinary trade: once an account exists, any amount.
func TestExistingAccountsAcceptAnyAmount(t *testing.T) {
	a, ctx, msgServer := fundingApp(t)
	funder, peer := addrOf(0x41), addrOf(0x42)
	fundAccount(t, a, ctx, funder, 1_000_000)
	fundAccount(t, a, ctx, peer, minimum())
	require.True(t, a.AccountKeeper.HasAccount(ctx, peer))

	require.NoError(t, send(msgServer, ctx, funder, peer, 1),
		"transfers between existing accounts must be unrestricted")
}

// ---------------------------------------------------------------------------
// what the rule DOES bound: transaction-local fan-out
// ---------------------------------------------------------------------------

// Within one transaction the capital cannot be reused between outputs, so N
// fresh recipients cost N times the minimum. This is the actual guarantee.
func TestFanOutWithinOneTransactionCostsProportionalBalance(t *testing.T) {
	a, ctx, msgServer := fundingApp(t)
	m := minimum()

	// 3M opens exactly three accounts.
	funder := addrOf(0x51)
	fundAccount(t, a, ctx, funder, 3*m)
	outputs := make([]banktypes.Output, 0, 3)
	for i := 0; i < 3; i++ {
		outputs = append(outputs, banktypes.Output{Address: addrOf(0x52, byte(i)).String(), Coins: coins(m)})
	}
	_, err := msgServer.MultiSend(ctx, &banktypes.MsgMultiSend{
		Inputs:  []banktypes.Input{{Address: funder.String(), Coins: coins(3 * m)}},
		Outputs: outputs,
	})
	require.NoError(t, err, "3M of balance must open exactly three accounts")

	// The same balance spread thinner is refused: it cannot be reused between
	// the outputs of one transaction.
	thin := addrOf(0x53)
	fundAccount(t, a, ctx, thin, 2*m)
	_, err = msgServer.MultiSend(ctx, &banktypes.MsgMultiSend{
		Inputs: []banktypes.Input{{Address: thin.String(), Coins: coins(2 * m)}},
		Outputs: []banktypes.Output{
			{Address: addrOf(0x54, 0x01).String(), Coins: coins(m)},
			{Address: addrOf(0x54, 0x02).String(), Coins: coins(m / 2)},
			{Address: addrOf(0x54, 0x03).String(), Coins: coins(m / 2)},
		},
	})
	require.Error(t, err, "a multi-send must not create an underfunded account")
	require.Contains(t, err.Error(), "requires at least",
		"the refusal must come from the funding rule, not an unrelated bank error")
	require.False(t, a.AccountKeeper.HasAccount(ctx, addrOf(0x54, 0x02)))
}

// SCOPE CHARACTERISATION — not a security property this chain intends to keep.
//
// The minimum is transferred, not consumed or locked, so the same capital walks
// across transactions and leaves a permanent account at every hop. This records
// the accepted residual so no future reader mistakes the rule for a bound on
// cumulative growth.
//
// If a later fee or admission layer (TW-005) makes recycling economically
// costly, this characterization SHOULD change — deliberately, as a recorded
// decision. It is not a regression to be defended.
func TestRecyclingAcrossTransactionsIsPossible_ScopeCharacterization(t *testing.T) {
	a, ctx, msgServer := fundingApp(t)
	m := minimum()
	const hops = 25

	origin := addrOf(0xC0, 0x00)
	fundAccount(t, a, ctx, origin, m)
	supplyBefore := a.BankKeeper.GetSupply(ctx, app.BaseDenom).Amount

	chain := []sdk.AccAddress{origin}
	for hop := 1; hop <= hops; hop++ {
		next := addrOf(0xC0, byte(hop))
		require.False(t, a.AccountKeeper.HasAccount(ctx, next))
		require.NoError(t, send(msgServer, ctx, chain[hop-1], next, m),
			"hop %d: the rule does not prevent reuse of the same capital", hop)
		chain = append(chain, next)
	}

	var held int64
	for i, addr := range chain {
		require.True(t, a.AccountKeeper.HasAccount(ctx, addr),
			"account %d persists after its balance left", i)
		held += a.BankKeeper.GetBalance(ctx, addr, app.BaseDenom).Amount.Int64()
	}

	require.Len(t, chain, hops+1)
	require.Equal(t, m, held,
		"%d permanent accounts exist and the attacker still holds exactly M", len(chain))
	require.Equal(t, m, a.BankKeeper.GetBalance(ctx, chain[hops], app.BaseDenom).Amount.Int64(),
		"the whole original amount survives at the end of the chain")
	require.True(t, supplyBefore.Equal(a.BankKeeper.GetSupply(ctx, app.BaseDenom).Amount),
		"nothing was consumed: no economic cost is sunk per account")
}

// ---------------------------------------------------------------------------
// the protocol-payout exemption
// ---------------------------------------------------------------------------

// The exemption prevents a CONSENSUS HALT, not an inconvenience.
//
// PayTreasury is called from finalizeEpoch, which EndBlock runs in a cache
// context and whose error propagates out of EndBlock. A treasury share below
// the floor paid to a not-yet-funded treasury address is ordinary
// configuration — emission 1_000_000 at 50 bps is 5_000utwlt — so without the
// exemption a chain in that configuration halts at epoch finalization.
func TestSubFloorTreasuryPayoutDoesNotHaltFinalization(t *testing.T) {
	a, ctx, _ := fundingApp(t)
	treasury := addrOf(0x61)
	require.False(t, a.AccountKeeper.HasAccount(ctx, treasury))
	require.Less(t, int64(5_000), minimum(), "the fixture must produce a sub-floor payout")

	require.NoError(t, a.BankKeeper.MintCoins(ctx, rewardstypes.ModuleName, coins(1_000_000)))
	require.NoError(t,
		a.RewardsKeeper.PayTreasury(ctx, treasury.String(), sdkmath.NewInt(5_000), app.BaseDenom),
		"a sub-floor treasury payment must not fail; on the finalization path it halts the block")
	require.True(t, a.AccountKeeper.HasAccount(ctx, treasury))
}

// Remainder release pays whatever is left on an entitlement, possibly one base
// unit, to the slot's payout address.
func TestProtocolPayoutsReachFirstTimeRecipients(t *testing.T) {
	a, ctx, _ := fundingApp(t)
	fresh := addrOf(0x62)
	require.False(t, a.AccountKeeper.HasAccount(ctx, fresh))

	require.NoError(t, a.BankKeeper.MintCoins(ctx, rewardstypes.ModuleName, coins(1)))
	require.NoError(t,
		a.BankKeeper.SendCoinsFromModuleToAccount(ctx, rewardstypes.ModuleName, fresh, coins(1)),
		"a remainder release of one base unit must be able to open the payout account")
	require.Equal(t, int64(1), a.BankKeeper.GetBalance(ctx, fresh, app.BaseDenom).Amount.Int64())
}

// The exemption is keyed on the SENDER. An account a module funded gets no
// exemption from it.
func TestTheProtocolExemptionDoesNotTransferToItsRecipients(t *testing.T) {
	a, ctx, msgServer := fundingApp(t)
	paid, onward := addrOf(0x71), addrOf(0x72)
	fundAccount(t, a, ctx, paid, 1_000_000)

	err := send(msgServer, ctx, paid, onward, 1)
	require.Error(t, err, "an ordinary sender is gated regardless of where its coins came from")
	require.Contains(t, err.Error(), "requires at least")
	require.False(t, a.AccountKeeper.HasAccount(ctx, onward))
}

// A module account that is NOT on the allow-list gets no exemption. This is the
// difference between the narrow rule and "every sdk.ModuleAccountI sender": a
// future module cannot inherit the bypass simply by being a module.
func TestUnlistedModuleAccountsAreNotExempt(t *testing.T) {
	a, ctx, _ := fundingApp(t)
	fresh := addrOf(0x73)

	// The fee collector is a real module account on this chain that is
	// deliberately absent from the protocol-payout list.
	feeCollector := authtypes.NewModuleAddress(authtypes.FeeCollectorName)
	require.NoError(t, a.BankKeeper.MintCoins(ctx, rewardstypes.ModuleName, coins(1_000)))
	require.NoError(t, a.BankKeeper.SendCoinsFromModuleToModule(
		ctx, rewardstypes.ModuleName, authtypes.FeeCollectorName, coins(1_000)))

	err := a.BankKeeper.SendCoins(ctx, feeCollector, fresh, coins(1))
	require.Error(t, err,
		"a module account absent from the allow-list must not bypass the funding rule")
	require.Contains(t, err.Error(), "requires at least")
	require.False(t, a.AccountKeeper.HasAccount(ctx, fresh))
}

// Module-to-module movement is unaffected.
func TestModuleTransfersAreUnaffected(t *testing.T) {
	a, ctx, _ := fundingApp(t)
	require.NoError(t, a.BankKeeper.MintCoins(ctx, rewardstypes.ModuleName, coins(1)))
	require.NoError(t, a.BankKeeper.SendCoinsFromModuleToModule(
		ctx, rewardstypes.ModuleName, rewardstypes.FeePoolName, coins(1)),
		"module-to-module movement must not be gated by the account-funding rule")
}

// The allow-list and the source-derived inventory must be the SAME SET.
//
// Containment in one direction is not enough, and the gap is not theoretical.
// A test that only proved
//
//	source-derived senders ⊆ allow-list
//
// catches a payout path added without an exemption, but passes a speculative
// exemption added before any payout path exists. That is the more dangerous
// drift: once a module sits on the list unnecessarily, the day someone gives it
// a SendCoinsFromModuleToAccount path, the new path inherits the bypass and the
// inventory notices nothing — defeating the whole purpose of deriving the set
// from source.
//
// So both directions are proven at once, by comparing canonical sets.
func TestTheExemptionListMatchesTheSendingModulesExactly(t *testing.T) {
	sending, sites, err := payoutledger.SendingModules("../x", "../app")
	require.NoError(t, err,
		"every module payout call site must be resolvable; an unreadable one is a "+
			"reason to widen the parser deliberately, not to skip it")
	require.NotEmpty(t, sites, "the inventory found no payout call sites at all")

	// Copied before sorting. ProtocolPayoutModulesForTest hands back the
	// production slice itself, and a test must not reorder the list the running
	// application reads.
	allowed := append([]string(nil), app.ProtocolPayoutModulesForTest()...)
	sort.Strings(allowed)

	// A duplicate would make the sets differ, so exact equality already catches
	// it — but it would fail with a confusing length mismatch rather than saying
	// what is wrong.
	seen := make(map[string]struct{}, len(allowed))
	for _, module := range allowed {
		_, duplicate := seen[module]
		require.False(t, duplicate, "module %q appears twice in the exemption list", module)
		seen[module] = struct{}{}
	}

	// SendingModules returns its distinct set sorted; sorted again here so the
	// comparison does not depend silently on that.
	expected := append([]string(nil), sending...)
	sort.Strings(expected)

	require.Equal(t, expected, allowed,
		"protocol payout exemptions must exactly match the source-derived "+
			"module-to-account senders.\n"+
			"  a module in SOURCE but not ALLOWED halts the block on a sub-floor payout;\n"+
			"  a module ALLOWED but not in SOURCE silently pre-exempts a payout path "+
			"that does not exist yet, so the day it is added it inherits the bypass unnoticed")
}
