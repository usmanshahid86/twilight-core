package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	sdk "github.com/cosmos/cosmos-sdk/types"

	appparams "github.com/twilight-project/twilight-core/app/params"
	"github.com/twilight-project/twilight-core/x/rewards/keeper"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// The genesis-fixed monetary configuration, on the paths that act on it.
//
// Params is validated when it is written and was trusted on every read
// afterwards. For configuration that only describes things that is fine. For the
// three fields a monetary path CONSUMES it is not: a stored value no admission
// path could have produced is not a stale setting, it is an instruction the chain
// will carry out.
//
// native_denom is the sharpest of the three, so it is the one every case below
// corrupts. A wrong denom is not a cosmetic mislabel — the module would mint a
// token nobody can spend, measure its own solvency in that same fiction, and pay
// entitlements in it, while the real utwlt escrow sat untouched beside it.

// corruptNativeDenom writes a syntactically valid but non-canonical denom
// straight to the collection, bypassing SetParams.
//
// Bypassing the setter is the point: SetParams already refuses this, so the
// scenario under test is state that got there some other way. The denom chosen is
// a legitimate SDK denom, not a malformed string, so nothing downstream can reject
// it for being unparseable.
func corruptNativeDenom(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
	t.Helper()
	params, err := k.GetParams(ctx)
	require.NoError(t, err)
	params.NativeDenom = "uatom"
	params.FeeDenom = "uatom"
	require.NoError(t, k.Params.Set(ctx, params))
}

func TestCorruptAccountingDenomStopsFinalizationBeforeTheMint(t *testing.T) {
	k, ctx, bank, _ := setupFinalization(t, false)
	corruptNativeDenom(t, k, ctx)

	err := k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight))
	require.ErrorIs(t, err, types.ErrInvalidState)
	requireNoMonetaryEffect(t, k, ctx, bank, 1)
}

func TestCorruptAccountingDenomStopsReleaseBeforeTheSend(t *testing.T) {
	t.Run("a participant payout set", func(t *testing.T) {
		k, ctx, bank := setupRelease(t)
		corruptNativeDenom(t, k, ctx)

		err := k.PayEntitlement(ctx, 1, 1, []types.EntitlementPayout{
			payout(testAccount(7), "400"),
		})
		require.ErrorIs(t, err, types.ErrInvalidState)

		require.Zero(t, bank.sendCalls, "no transfer may be attempted")
		require.Empty(t, bank.sends)
		requireReleaseState(t, k, ctx, "0", "1000")
	})

	t.Run("the operator remainder", func(t *testing.T) {
		k, ctx, bank := setupRelease(t)
		corruptNativeDenom(t, k, ctx)

		err := k.PayEntitlementRemainderToOperator(ctx, 1, 1)
		require.ErrorIs(t, err, types.ErrInvalidState)

		require.Zero(t, bank.sendCalls, "no transfer may be attempted")
		require.Empty(t, bank.sends)
		requireReleaseState(t, k, ctx, "0", "1000")
	})
}

// TestMonetaryParamsRefusesEveryConsumedGenesisFixedField covers the other two
// fields directly, since neither has a corruption that a denom test would reach.
func TestMonetaryParamsRefusesEveryConsumedGenesisFixedField(t *testing.T) {
	for _, tc := range []struct {
		name    string
		corrupt func(*types.Params)
	}{
		{"a foreign accounting denom", func(p *types.Params) { p.NativeDenom = "uatom" }},
		{"the display denom", func(p *types.Params) { p.NativeDenom = "twlt" }},
		{"an unparseable supply cap", func(p *types.Params) { p.MaxSupply = "not-a-number" }},
		{"a zero supply cap", func(p *types.Params) { p.MaxSupply = "0" }},
		{"an unsupported halving schedule", func(p *types.Params) {
			p.HalvingMode = types.HalvingMode_HALVING_MODE_UNSPECIFIED
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx, _ := setupKeeper(t, &coreSlotKeeperMock{})
			params := rewardConfigParams()
			require.NoError(t, k.SetParams(ctx, params))

			tc.corrupt(&params)
			require.NoError(t, k.Params.Set(ctx, params))

			_, err := k.MonetaryParams(ctx)
			require.ErrorIs(t, err, types.ErrInvalidState)

			// The plain read still succeeds, which is what makes the boundary
			// load-bearing rather than incidental.
			_, err = k.GetParams(ctx)
			require.NoError(t, err)
		})
	}
}

// TestMonetaryParamsLeavesTheDeprecatedMirrorsAlone guards the boundary from the
// other side.
//
// The economic mirrors on Params carry no authority and reach no computation.
// Validating them here would be the first step back toward treating them as a
// second monetary authority, which is the thing the canonical RewardConfigVersion
// history exists to prevent.
func TestMonetaryParamsLeavesTheDeprecatedMirrorsAlone(t *testing.T) {
	k, ctx, _ := setupKeeper(t, &coreSlotKeeperMock{})
	params := rewardConfigParams()
	require.NoError(t, k.SetParams(ctx, params))

	params.InitialBlockSubsidy = "010"
	params.EmissionTreasuryShareBps = 9_999
	params.TreasuryAddress = "not-an-address"
	require.NoError(t, k.Params.Set(ctx, params))

	_, err := k.MonetaryParams(ctx)
	require.NoError(t, err, "the mirrors are not monetary authority and are not this boundary's business")
}

// TestCorruptAccountingDenomStopsTheLegacyClaimPath closes the last path that
// reached the accounting denom without validating it.
//
// ClaimRewards is retained compatibility, not exempt compatibility. It compares a
// balance against params.NativeDenom and then transfers in it, so a denom
// corrupted to another syntactically valid one would have been checked and paid in
// a token the chain does not account in — and the escrow holding real utwlt would
// have been left untouched beside the whole transaction.
//
// The fixture funds the foreign denom deliberately. Without that, an
// implementation that still read the raw denom would fail anyway on the balance
// check, and the test would pass while proving nothing about which denom was
// consulted.
func TestCorruptAccountingDenomStopsTheLegacyClaimPath(t *testing.T) {
	// Explicitly legacy-only state: no conforming POC1 chain can hold a claim
	// record, so this is written straight to the store.
	k, ctx, bank, _ := setupFinalization(t, false)
	require.NoError(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))
	require.NoError(t, k.SetClaimRecord(ctx, validClaim(1, 1)))

	corruptNativeDenom(t, k, ctx)
	// Fund the foreign denom so a wrong implementation could actually pay.
	bank.credit(moduleAccountAddress(), sdk.NewCoins(sdk.NewInt64Coin("uatom", 1_000_000)))

	sendsBefore := bank.sendCalls
	before := bank.GetBalance(ctx, moduleAccountAddress(), "uatom").Amount

	err := k.ClaimRewards(ctx, &types.MsgClaimRewards{
		Signer: addr(1), SlotId: 1, StartEpoch: 1, EndEpoch: 1,
	})
	require.ErrorIs(t, err, types.ErrInvalidState)

	require.Equal(t, sendsBefore, bank.sendCalls, "no transfer may be attempted")
	require.Equal(t, before.String(),
		bank.GetBalance(ctx, moduleAccountAddress(), "uatom").Amount.String(),
		"no balance may move in any denom")

	record, found, err := k.GetClaimRecord(ctx, 1, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.False(t, record.Claimed, "the claim must remain unclaimed")
	require.Zero(t, record.ClaimedAtHeight, "the claimed height must not move")
}

// TestModuleBalancesRefusesACorruptMonetaryConfiguration keeps the query and the
// block path on one definition of a usable monetary configuration.
//
// The response publishes the escrow balance beside the two quantities it must
// equal, so it is meant to be believed. Selecting the denom from an unvalidated
// Params would let a chain whose configuration execution refuses to act on publish
// a confident, internally consistent tuple denominated in a token the protocol
// does not account in.
func TestModuleBalancesRefusesACorruptMonetaryConfiguration(t *testing.T) {
	k, ctx, _ := setupEntitlements(t)
	server := keeper.NewQueryServer(k)

	resp, err := server.ModuleBalances(ctx, &types.QueryModuleBalancesRequest{})
	require.NoError(t, err)
	require.Equal(t, appparams.NativeBaseDenom, resp.Denom)

	corruptNativeDenom(t, k, ctx)

	resp, err = server.ModuleBalances(ctx, &types.QueryModuleBalancesRequest{})
	require.Equal(t, codes.Internal, status.Code(err))
	require.Nil(t, resp, "no balance tuple may be returned in a denom the chain does not account in")
}
