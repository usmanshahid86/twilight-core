package app_test

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	sdkmath "cosmossdk.io/math"

	"github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	"github.com/twilight-project/twilight-core/app"
	appparams "github.com/twilight-project/twilight-core/app/params"
	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	rewardstypes "github.com/twilight-project/twilight-core/x/rewards/types"
)

// Fresh genesis, against the real app.
//
// The document-level rules say a fresh chain owes nothing. What the document
// cannot see is the bank, and the bank holds the other half of the same equation:
// the escrow balance a genesis funds and the carry it declares are the same
// number, because after entitlements and claim records are excluded there is
// nothing else for escrow to hold.
//
// Getting that wrong used to be invisible until the first epoch closed, where
// finalization asserts escrow == liability + carry and halts the block. That is
// the right behavior at the wrong moment: the defect is in the genesis document,
// and a chain that produces epoch_length blocks and then stops is much harder to
// diagnose than one that refuses to start.

func freshRewardsGenesis(t *testing.T, carry string) *rewardstypes.GenesisState {
	t.Helper()
	params := rewardstypes.DefaultParams()
	params.EpochLengthBlocks = appparams.HardMinEpochLengthBlocks
	snapshot := rewardstypes.DefaultEpochConfigSnapshot(params)
	return canonicalRewardsTimeline(&rewardstypes.GenesisState{
		Params: &params,
		State: &rewardstypes.RewardsState{
			CurrentEpoch: 1, CurrentEpochStartHeight: 1,
			CumulativeEmitted: "0", CarryForwardRemainder: carry,
		},
		CurrentEpochConfig: &snapshot,
	}, 1)
}

// TestFreshGenesisEscrowMustEqualTheDeclaredCarry drives the mismatch in both
// directions through the real app InitChain.
//
// Both are defects, and for different reasons. Underfunded escrow cannot pay the
// carry it declares. Overfunded escrow holds value that no obligation and no carry
// accounts for — which is not harmless, because the solvency relation is an
// equality: a surplus is exactly as unexplainable as a shortfall.
func TestFreshGenesisEscrowMustEqualTheDeclaredCarry(t *testing.T) {
	for _, tc := range []struct {
		name    string
		carry   string
		funding int64
	}{
		{name: "a carry the escrow does not hold", carry: "250", funding: 0},
		{name: "escrow short of the carry", carry: "250", funding: 100},
		{name: "escrow beyond the carry", carry: "0", funding: 100},
		{name: "escrow beyond a positive carry", carry: "250", funding: 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := bootApp(t)
			failure := captureInitChainFailure(t, a, &abci.RequestInitChain{
				ChainId:         "",
				ConsensusParams: sims.DefaultConsensusParams,
				AppStateBytes:   freshAppState(t, a, freshRewardsGenesis(t, tc.carry), tc.funding),
			})
			require.ErrorIs(t, failure, rewardstypes.ErrInvalidGenesis)
			require.Contains(t, failure.Error(), "funds the rewards escrow with")
		})
	}

	t.Run("a matching pair is accepted", func(t *testing.T) {
		a := bootApp(t)
		_, err := a.InitChain(&abci.RequestInitChain{
			ChainId:         "",
			ConsensusParams: sims.DefaultConsensusParams,
			AppStateBytes:   freshAppState(t, a, freshRewardsGenesis(t, "250"), 250),
		})
		require.NoError(t, err)

		ctx := a.NewContextLegacy(false, cmtproto.Header{Height: 1})
		state, err := a.RewardsKeeper.GetState(ctx)
		require.NoError(t, err)
		require.Equal(t, "250", state.CarryForwardRemainder)
		escrow := a.AccountKeeper.GetModuleAddress(rewardstypes.ModuleName)
		require.Equal(t, "250", a.BankKeeper.GetBalance(ctx, escrow, app.BaseDenom).Amount.String())
	})
}

// TestFreshGenesisRunsItsFirstEpochBoundary is the positive half of the contract.
//
// Every rule added above refuses something. This proves the document that
// satisfies all of them is not merely acceptable but usable: a chain booted from
// it runs a complete epoch, mints, materializes obligations, and stays solvent
// under the equality finalization asserts.
//
// It also pins that V2 creates no claim records. The legacy surface is still
// reachable, and a chain that produced one anyway would have two payable forms of
// one obligation over one escrow — so "the collection is empty after a real epoch
// close" is the assertion, not "the message was removed".
func TestFreshGenesisRunsItsFirstEpochBoundary(t *testing.T) {
	a := bootApp(t)
	genesis := freshRewardsGenesis(t, "0")
	epochLen := int64(genesis.Params.EpochLengthBlocks)
	initChainWithRewards(t, a, genesis)

	blockTime := time.Unix(1_700_000_000, 0).UTC()
	for height := int64(1); height <= epochLen; height++ {
		_, err := a.FinalizeBlock(&abci.RequestFinalizeBlock{
			Height: height, Time: blockTime.Add(time.Duration(height) * time.Second),
		})
		require.NoError(t, err)
		_, err = a.Commit()
		require.NoError(t, err)
	}

	ctx := a.NewUncachedContext(false, cmtproto.Header{Height: epochLen})

	epochEmission := strconv.FormatInt(epochLen*416190, 10)
	closed, found, err := a.RewardsKeeper.GetFinalizedEpoch(ctx, 1)
	require.NoError(t, err)
	require.True(t, found, "the first epoch must close")
	require.Equal(t, epochEmission, closed.MintedEmission)
	require.Empty(t, closed.Rewards, "a V2 finalized epoch embeds no per-slot rows")

	entitlements, err := a.RewardsKeeper.IterateEntitlementsForEpoch(ctx, 1)
	require.NoError(t, err)
	require.Len(t, entitlements, 1, "the single active slot is owed the epoch")

	// The legacy representation was not created beside it.
	_, found, err = a.RewardsKeeper.GetClaimRecord(ctx, 1, 1)
	require.NoError(t, err)
	require.False(t, found, "V2 finalization creates no claim record")

	// Solvency, from the outside: escrow holds exactly liability plus carry.
	liability, err := a.RewardsKeeper.GetOutstandingEntitlementLiability(ctx)
	require.NoError(t, err)
	state, err := a.RewardsKeeper.GetState(ctx)
	require.NoError(t, err)
	carry, ok := sdkmath.NewIntFromString(state.CarryForwardRemainder)
	require.True(t, ok)
	escrow := a.AccountKeeper.GetModuleAddress(rewardstypes.ModuleName)
	require.Equal(t,
		liability.Add(carry).String(),
		a.BankKeeper.GetBalance(ctx, escrow, app.BaseDenom).Amount.String(),
		"escrow must hold exactly what the module owes")
	require.Equal(t, epochEmission, a.BankKeeper.GetSupply(ctx, app.BaseDenom).Amount.String())
}

// freshAppState assembles an app genesis with the rewards escrow funded to an
// arbitrary amount, so the escrow/carry relation can be violated on purpose.
//
// initChainWithRewards deliberately cannot do this: it funds escrow to exactly the
// declared carry, which is the correct pairing and the one every other fixture
// wants.
func freshAppState(t *testing.T, a *app.App, rewardsGen *rewardstypes.GenesisState, escrowFunding int64) []byte {
	t.Helper()
	cdc := genesisCodec()
	genMap := a.DefaultGenesis()
	// The same one-ACTIVE-slot CoreSlot genesis every other fixture uses. Without
	// it the chain has no validator and InitChain fails for a reason that has
	// nothing to do with the escrow relation under test.
	genMap[coreslottypes.ModuleName] = cdc.MustMarshalJSON(defaultCoreSlotGenesis(t, acc(2), acc(12)))
	genMap[rewardstypes.ModuleName] = cdc.MustMarshalJSON(rewardsGen)

	if escrowFunding > 0 {
		var bankGen banktypes.GenesisState
		require.NoError(t, cdc.UnmarshalJSON(genMap[banktypes.ModuleName], &bankGen))
		funds := sdk.NewCoins(sdk.NewInt64Coin(appparams.NativeBaseDenom, escrowFunding))
		bankGen.Balances = append(bankGen.Balances, banktypes.Balance{
			Address: authtypes.NewModuleAddress(rewardstypes.ModuleName).String(),
			Coins:   funds,
		})
		bankGen.Supply = bankGen.Supply.Add(funds...)
		genMap[banktypes.ModuleName] = cdc.MustMarshalJSON(&bankGen)
	}

	appState, err := json.Marshal(genMap)
	require.NoError(t, err)
	return appState
}
