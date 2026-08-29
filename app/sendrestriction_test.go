package app_test

import (
	"testing"

	dbm "github.com/cosmos/cosmos-db"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/log"
	sdkmath "cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"

	"github.com/twilight-project/twilight-core/app"
	"github.com/twilight-project/twilight-core/app/params"
	miningtypes "github.com/twilight-project/twilight-core/x/mining/types"
	rewardstypes "github.com/twilight-project/twilight-core/x/rewards/types"
)

// The property that makes the restriction safe: no payout the chain is willing
// to settle may be too small to open the account it is paying into. Otherwise
// the chain owes money it cannot deliver, and that surfaces as a settlement
// halt rather than as anything pointing here.
//
// Asserted as a relation over every configuration the chain will ACCEPT, not
// against a copy of the number. A test that restated 10_000 would agree with a
// wrong implementation that also restated 10_000.
func TestNoAcceptableSettlementFloorIsUnpayable(t *testing.T) {
	hardMin := params.HardMinSettlementPayoutAmount()

	require.True(t, app.MinimumAccountFunding().LTE(hardMin),
		"the funding minimum must not exceed the hard settlement floor, or a "+
			"settlement at that floor could not open a recipient account")

	// ValidateMinRecipientPayoutAmount defines which configured floors are
	// acceptable. Every one of them must be able to open an account.
	for _, configured := range []sdkmath.Int{
		hardMin,
		hardMin.AddRaw(1),
		sdkmath.NewIntFromUint64(1_000_000),
		sdkmath.NewIntFromUint64(1_000_000_000_000),
	} {
		require.NoError(t, params.ValidateMinRecipientPayoutAmount(configured, hardMin),
			"test fixture error: %s is not an acceptable configured floor", configured)
		require.True(t, app.MinimumAccountFunding().LTE(configured),
			"a settlement at the configured floor %s could not open an account", configured)
	}

	// And the value the chain actually ships with.
	shipped, ok := sdkmath.NewIntFromString(miningtypes.DefaultMinRecipientPayoutAmount)
	require.True(t, ok, "the shipped default must parse")
	require.True(t, app.MinimumAccountFunding().LTE(shipped),
		"the default min_recipient_payout_amount could not open an account")
}

func fundingApp(t *testing.T) (*app.App, sdk.Context) {
	t.Helper()
	a := app.New(log.NewNopLogger(), dbm.NewMemDB(), nil, true, sims.EmptyAppOptions{})
	return a, a.NewUncachedContext(false, cmtproto.Header{Height: 1})
}

func coins(n int64) sdk.Coins { return sdk.NewCoins(sdk.NewCoin(app.BaseDenom, sdkmath.NewInt(n))) }

// minimum is the threshold as a plain int64, for arithmetic in the cases below.
func minimum() int64 { return app.MinimumAccountFunding().Int64() }

// addrOf returns a deterministic account address that does not exist yet.
func addrOf(marker byte) sdk.AccAddress {
	raw := make([]byte, 20)
	raw[0] = marker
	raw[19] = marker
	return sdk.AccAddress(raw)
}

// fundAccount puts a spendable balance behind an ordinary address. rewards is
// the only module account carrying minter permission on this chain, so it is
// the one that can put coins into circulation.
func fundAccount(t *testing.T, a *app.App, ctx sdk.Context, addr sdk.AccAddress, amount int64) {
	t.Helper()
	require.NoError(t, a.BankKeeper.MintCoins(ctx, rewardstypes.ModuleName, coins(amount)))
	require.NoError(t, a.BankKeeper.SendCoinsFromModuleToAccount(ctx, rewardstypes.ModuleName, addr, coins(amount)))
}

// The restriction runs inside bank, so it is exercised through the keeper
// rather than by calling the function directly: what matters is that the wiring
// puts it on the path a transaction actually takes.
func TestNewAccountBelowTheMinimumIsRefused(t *testing.T) {
	a, ctx := fundingApp(t)
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

			err := a.BankKeeper.SendCoins(ctx, funder, fresh, coins(tc.amount))
			require.Error(t, err, "an account below the minimum must not be created")
			require.Contains(t, err.Error(), "requires at least")
			require.False(t, a.AccountKeeper.HasAccount(ctx, fresh),
				"a refused send must leave no account behind")
		})
	}
}

// Exactly at the boundary must be ACCEPTED. A settlement pays exactly the
// configured floor to a first-time recipient, so a > comparison here would make
// the chain unable to deliver a payout it had already committed to.
func TestNewAccountAtExactlyTheMinimumIsAllowed(t *testing.T) {
	a, ctx := fundingApp(t)
	funder, fresh := addrOf(0x31), addrOf(0x32)
	fundAccount(t, a, ctx, funder, 1_000_000)

	require.NoError(t, a.BankKeeper.SendCoins(ctx, funder, fresh, coins(minimum())),
		"the settlement floor itself must be able to open an account")
	require.True(t, a.AccountKeeper.HasAccount(ctx, fresh))
}

// The objection this design has to answer: it must not restrict ordinary trade.
// Once an account exists, any amount may be sent to it.
func TestExistingAccountsAcceptAnyAmount(t *testing.T) {
	a, ctx := fundingApp(t)
	funder, peer := addrOf(0x41), addrOf(0x42)
	fundAccount(t, a, ctx, funder, 1_000_000)
	fundAccount(t, a, ctx, peer, minimum())
	require.True(t, a.AccountKeeper.HasAccount(ctx, peer))

	// A single base unit between existing accounts, which is the smallest
	// transfer the chain can express.
	require.NoError(t, a.BankKeeper.SendCoins(ctx, funder, peer, coins(1)),
		"transfers between existing accounts must be unrestricted")
}

// MsgMultiSend takes a different keeper path, and the restriction has to cover
// it too — bulk creation through InputOutputCoins is the cheaper half of TW-006.
func TestMultiSendCannotCreateUnderfundedAccounts(t *testing.T) {
	a, ctx := fundingApp(t)
	funder := addrOf(0x51)
	fundAccount(t, a, ctx, funder, 10_000_000)

	under, over := addrOf(0x52), addrOf(0x53)
	err := a.BankKeeper.InputOutputCoins(ctx,
		banktypes.Input{Address: funder.String(), Coins: coins(minimum() + 1)},
		[]banktypes.Output{
			{Address: over.String(), Coins: coins(minimum())},
			{Address: under.String(), Coins: coins(1)},
		})
	require.Error(t, err, "a multi-send must not create an underfunded account")
	require.Contains(t, err.Error(), "requires at least",
		"the refusal must come from the funding rule, not from an unrelated bank error")
	require.False(t, a.AccountKeeper.HasAccount(ctx, under))
}

// Protocol payouts must reach a first-time recipient at any amount. This is the
// shape of x/rewards payEntitlementRemainder, which pays whatever is left on an
// entitlement — possibly a single base unit — to the slot's payout address. If
// the funding rule gated it, the chain could not discharge a debt it had already
// recorded.
//
// The end-to-end proof is that the rewards release and settlement suites pass;
// this states the property directly so that removing the exemption fails HERE,
// naming the reason, rather than surfacing as an unexplained release error.
func TestProtocolPayoutsReachFirstTimeRecipients(t *testing.T) {
	a, ctx := fundingApp(t)
	fresh := addrOf(0x61)
	require.False(t, a.AccountKeeper.HasAccount(ctx, fresh))

	require.NoError(t, a.BankKeeper.MintCoins(ctx, rewardstypes.ModuleName, coins(1)))
	require.NoError(t,
		a.BankKeeper.SendCoinsFromModuleToAccount(ctx, rewardstypes.ModuleName, fresh, coins(1)),
		"a remainder release of one base unit must be able to open the payout account")
	require.True(t, a.AccountKeeper.HasAccount(ctx, fresh))
	require.Equal(t, int64(1), a.BankKeeper.GetBalance(ctx, fresh, app.BaseDenom).Amount.Int64())
}

// The exemption is keyed on the SENDER being a module account. A recipient that
// a module opened must not become a way to fund others below the minimum, and an
// ordinary account holding module-paid coins gets no exemption from them.
func TestTheProtocolExemptionDoesNotTransferToItsRecipients(t *testing.T) {
	a, ctx := fundingApp(t)
	paid, onward := addrOf(0x71), addrOf(0x72)

	require.NoError(t, a.BankKeeper.MintCoins(ctx, rewardstypes.ModuleName, coins(1_000_000)))
	require.NoError(t,
		a.BankKeeper.SendCoinsFromModuleToAccount(ctx, rewardstypes.ModuleName, paid, coins(1_000_000)))

	err := a.BankKeeper.SendCoins(ctx, paid, onward, coins(1))
	require.Error(t, err, "an ordinary sender is gated regardless of where its coins came from")
	require.Contains(t, err.Error(), "requires at least")
	require.False(t, a.AccountKeeper.HasAccount(ctx, onward))
}

// Module accounts must be unaffected. Rewards escrow and settlement move value
// between module accounts continuously, and a restriction that blocked those
// would halt the chain rather than merely inconvenience a user.
func TestModuleTransfersAreUnaffected(t *testing.T) {
	a, ctx := fundingApp(t)
	require.NoError(t, a.BankKeeper.MintCoins(ctx, rewardstypes.ModuleName, coins(1)))
	require.NoError(t, a.BankKeeper.SendCoinsFromModuleToModule(ctx, rewardstypes.ModuleName, rewardstypes.FeePoolName, coins(1)),
		"module-to-module movement must not be gated by the account-funding rule")
}
