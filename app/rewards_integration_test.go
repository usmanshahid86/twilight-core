package app_test

import (
	"testing"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	gogoproto "github.com/cosmos/gogoproto/proto"
	anypb "github.com/cosmos/gogoproto/types/any"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/core/appmodule"
	"cosmossdk.io/log"

	sdked25519 "github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	"github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/twilight-project/twilight-core/app"
	coreslotkeeper "github.com/twilight-project/twilight-core/x/coreslot/keeper"
	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	rewardstypes "github.com/twilight-project/twilight-core/x/rewards/types"
)

func bootApp(t *testing.T) *app.App {
	t.Helper()
	return app.New(log.NewNopLogger(), dbm.NewMemDB(), nil, true, sims.EmptyAppOptions{})
}

func acc(marker byte) string {
	b := make([]byte, 20)
	b[0] = marker
	return sdk.AccAddress(b).String()
}

func mustAddr(t *testing.T, bech string) sdk.AccAddress {
	t.Helper()
	a, err := sdk.AccAddressFromBech32(bech)
	require.NoError(t, err)
	return a
}

// ed25519Any builds a valid CometBFT ed25519 consensus pubkey Any, matching the
// only key type x/coreslot accepts.
func ed25519Any(t *testing.T, marker byte) *anypb.Any {
	t.Helper()
	key := make([]byte, sdked25519.PubKeySize)
	key[0] = marker
	bz, err := gogoproto.Marshal(&sdked25519.PubKey{Key: key})
	require.NoError(t, err)
	return &anypb.Any{TypeUrl: "/cosmos.crypto.ed25519.PubKey", Value: bz}
}

// TestRewardsModuleWiringAndOrdering proves the static wiring: the module is
// registered, the resolved runtime BeginBlock/EndBlock order is correct across
// the legacy/modern interface mix, rewards uses the modern (error-only)
// appmodule lifecycle interfaces and not the legacy validator-update interface,
// CoreSlot remains the sole legacy emitter, and the module accounts carry exactly
// the intended permissions.
func TestRewardsModuleWiringAndOrdering(t *testing.T) {
	a := bootApp(t)

	// Module is registered.
	require.Contains(t, a.ModuleManager.Modules, rewardstypes.ModuleName)

	// Resolved runtime ordering (Addendum 2 §A, option 1): this is the
	// post-resolution execution order the runtime will actually use, covering the
	// legacy(coreslot)/modern(rewards) mix — stronger than config-array reading.
	require.Equal(t, []string{"coreslot", "rewards"}, a.ModuleManager.OrderEndBlockers)
	require.Equal(t, []string{"rewards"}, a.ModuleManager.OrderBeginBlockers)

	rewModule := a.ModuleManager.Modules[rewardstypes.ModuleName]
	// Rewards uses the modern, error-only lifecycle interfaces...
	_, isBegin := rewModule.(appmodule.HasBeginBlocker)
	require.True(t, isBegin, "rewards must implement appmodule.HasBeginBlocker")
	_, isEnd := rewModule.(appmodule.HasEndBlocker)
	require.True(t, isEnd, "rewards must implement appmodule.HasEndBlocker")
	// ...and must NOT implement the legacy validator-update EndBlock interface.
	_, isLegacy := rewModule.(module.HasABCIEndBlock)
	require.False(t, isLegacy, "rewards must NOT implement module.HasABCIEndBlock")

	// CoreSlot remains the sole legacy validator-update emitter (regression).
	legacyEmitters := 0
	for _, m := range a.ModuleManager.Modules {
		if _, ok := m.(module.HasABCIEndBlock); ok {
			legacyEmitters++
		}
	}
	require.Equal(t, 1, legacyEmitters, "coreslot must remain the only validator-update EndBlock module")
	_, coreslotLegacy := a.ModuleManager.Modules["coreslot"].(module.HasABCIEndBlock)
	require.True(t, coreslotLegacy, "the single legacy emitter must be coreslot")

	// Module account permissions: rewards is the only minter; fee pool is dormant.
	ctx := a.NewUncachedContext(false, cmtproto.Header{Height: 1})
	rewardsAcc := a.AccountKeeper.GetModuleAccount(ctx, rewardstypes.ModuleName)
	require.NotNil(t, rewardsAcc)
	require.Equal(t, []string{authtypes.Minter}, rewardsAcc.GetPermissions())
	feePoolAcc := a.AccountKeeper.GetModuleAccount(ctx, rewardstypes.FeePoolName)
	require.NotNil(t, feePoolAcc)
	require.Empty(t, feePoolAcc.GetPermissions())
}

// TestRewardsShortEpochFinalizeSuspendClaim drives a short epoch through the
// booted app's real keepers (real bank, real module accounts, real CoreSlot). It
// proves: active-block credit accrues; a slot suspended mid-epoch (through the
// real CoreSlot path) after earning credit still finalizes and stays claimable;
// CoreSlot retains the slot row, addresses, and reward-weight row; finalization
// mints exactly the clipped emission into the real rewards module account; a
// claim transfers exactly that reward to the snapshotted payout address and
// nothing to a different signer; and every rewards invariant holds against the
// real bank.
func TestRewardsShortEpochFinalizeSuspendClaim(t *testing.T) {
	a := bootApp(t)
	base := a.NewUncachedContext(false, cmtproto.Header{Height: 1})

	// --- CoreSlot: two ACTIVE slots with distinct operator/payout + reward weights.
	op1, pay1 := acc(2), acc(12)
	op2, pay2 := acc(3), acc(13)
	csParams := coreslottypes.DefaultParams(app.AuthorityAddress(), app.EmergencyAuthorityAddress())
	_, err := a.CoreSlotKeeper.InitGenesis(base, &coreslottypes.GenesisState{
		Params: &csParams,
		// Fresh-genesis ACTIVE normalization at the chain's initial height (§80),
		// with the version-1 Selection policy each slot's pointer names.
		Slots: []*coreslottypes.CoreSlot{
			{SlotId: 1, OperatorAddress: op1, PayoutAddress: pay1, SettlementAddress: pay1, ConsensusPubkey: ed25519Any(t, 1), Status: coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE, ConsensusPower: 1, RewardWeight: coreslottypes.DefaultRewardWeight, ActivationSequence: 1, ActivatedHeight: 1, ActivationEffectiveHeight: 1, CurrentSelectionPolicyVersion: 1},
			{SlotId: 2, OperatorAddress: op2, PayoutAddress: pay2, SettlementAddress: pay2, ConsensusPubkey: ed25519Any(t, 2), Status: coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE, ConsensusPower: 1, RewardWeight: coreslottypes.DefaultRewardWeight, ActivationSequence: 1, ActivatedHeight: 1, ActivationEffectiveHeight: 1, CurrentSelectionPolicyVersion: 1},
		},
		SelectionPolicies: []*coreslottypes.SelectionPolicyVersion{
			{SlotId: 1, PolicyVersion: 1, SelectionRateBps: 2_500, MaxSelectedParticipants: 10, ValidFromHeight: 1},
			{SlotId: 2, PolicyVersion: 1, SelectionRateBps: 2_500, MaxSelectedParticipants: 10, ValidFromHeight: 1},
		},
		RewardWeights: []*coreslottypes.OperatorRewardWeight{
			{SlotId: 1, FinalWeight: coreslottypes.DefaultRewardWeight},
			{SlotId: 2, FinalWeight: coreslottypes.DefaultRewardWeight},
		},
		NextSlotId: 3,
	})
	require.NoError(t, err)

	// --- Rewards: short epoch (2 blocks), default subsidy, all flags enabled.
	rParams := rewardstypes.DefaultParams()
	rParams.EpochLengthBlocks = 2
	snapshot := rewardstypes.DefaultEpochConfigSnapshot(rParams)
	require.NoError(t, a.RewardsKeeper.InitGenesis(base, rewardstypes.GenesisState{
		Params:             &rParams,
		State:              &rewardstypes.RewardsState{CurrentEpoch: 1, CurrentEpochStartHeight: 1, CumulativeEmitted: "0", CarryForwardRemainder: "0"},
		CurrentEpochConfig: &snapshot,
	}))

	// === Block 1: credit both active slots; not yet at the epoch boundary. ===
	ctx1 := base.WithBlockHeight(1)
	require.NoError(t, a.RewardsKeeper.BeginBlock(ctx1))
	_, err = a.CoreSlotKeeper.EndBlock(ctx1)
	require.NoError(t, err)
	require.NoError(t, a.RewardsKeeper.EndBlock(ctx1)) // height 1 < end (2): no finalize
	_, found, err := a.RewardsKeeper.GetFinalizedEpoch(ctx1, 1)
	require.NoError(t, err)
	require.False(t, found, "epoch must not finalize before its configured end height")

	// === Block 2: credit again, then suspend slot 1 mid-epoch (real CoreSlot path). ===
	ctx2 := base.WithBlockHeight(2)
	require.NoError(t, a.RewardsKeeper.BeginBlock(ctx2))

	csMsg := coreslotkeeper.NewMsgServer(a.CoreSlotKeeper)
	_, err = csMsg.SuspendCoreSlot(ctx2, &coreslottypes.MsgSuspendCoreSlot{
		Authority: app.AuthorityAddress(), SlotId: 1, Reason: "integration-test",
	})
	require.NoError(t, err)

	// Addendum 2 §B: CoreSlot retains every finalization snapshot dependency for
	// the now-suspended slot.
	s1, err := a.CoreSlotKeeper.GetSlot(ctx2, 1)
	require.NoError(t, err)
	require.Equal(t, coreslottypes.SlotStatus_SLOT_STATUS_SUSPENDED, s1.Status)
	require.Equal(t, op1, s1.OperatorAddress, "operator address must survive suspend")
	require.Equal(t, pay1, s1.PayoutAddress, "payout address must survive suspend")
	w1, err := a.CoreSlotKeeper.GetRewardWeight(ctx2, 1)
	require.NoError(t, err, "reward-weight row must survive suspend")
	require.Equal(t, coreslottypes.DefaultRewardWeight, w1.FinalWeight)

	// CoreSlot EndBlock runs before rewards EndBlock (resolved order above).
	_, err = a.CoreSlotKeeper.EndBlock(ctx2)
	require.NoError(t, err)

	supplyBefore := a.BankKeeper.GetSupply(ctx2, app.BaseDenom).Amount
	require.NoError(t, a.RewardsKeeper.EndBlock(ctx2)) // height 2 == end: finalize epoch 1
	supplyAfter := a.BankKeeper.GetSupply(ctx2, app.BaseDenom).Amount

	// Two slots × 2 active blocks, subsidy 416190/block, 2 blocks => mint 832380.
	const mintedEmission = "832380"
	const perSlotReward = "416190"
	require.Equal(t, mintedEmission, supplyAfter.Sub(supplyBefore).String(),
		"supply must rise by exactly the clipped epoch emission (no double mint)")

	// Finalized aggregate + separate claim records exist; suspended slot 1 is in.
	epoch, found, err := a.RewardsKeeper.GetFinalizedEpoch(ctx2, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, mintedEmission, epoch.MintedEmission)
	rec1, found, err := a.RewardsKeeper.GetClaimRecord(ctx2, 1, 1)
	require.NoError(t, err)
	require.True(t, found, "suspended slot 1 must still have a claim record for the epoch it earned")
	require.Equal(t, perSlotReward, rec1.Amount)
	require.Equal(t, pay1, rec1.PayoutAddress)
	require.False(t, rec1.Claimed)

	// === Claim slot 1 (the suspended slot) via an arbitrary signer != payout. ===
	signer := acc(9)
	payAddr1 := mustAddr(t, pay1)
	require.True(t, a.BankKeeper.GetBalance(ctx2, payAddr1, app.BaseDenom).Amount.IsZero())

	require.NoError(t, a.RewardsKeeper.ClaimRewards(ctx2, &rewardstypes.MsgClaimRewards{
		Signer: signer, SlotId: 1, StartEpoch: 1, EndEpoch: 1,
	}))

	// Addendum 2 §B.6 + §13: real bank balance of the snapshotted payout rose by
	// exactly the reward; a non-payout signer received nothing.
	require.Equal(t, perSlotReward, a.BankKeeper.GetBalance(ctx2, payAddr1, app.BaseDenom).Amount.String())
	require.True(t, a.BankKeeper.GetBalance(ctx2, mustAddr(t, signer), app.BaseDenom).Amount.IsZero(),
		"signer must not receive funds unless signer == snapshotted payout")

	// Double claim is impossible.
	require.Error(t, a.RewardsKeeper.ClaimRewards(ctx2, &rewardstypes.MsgClaimRewards{
		Signer: signer, SlotId: 1, StartEpoch: 1, EndEpoch: 1,
	}))

	// Closed-epoch aggregate is not mutated by the claim (separate claim row only).
	epochAfter, _, err := a.RewardsKeeper.GetFinalizedEpoch(ctx2, 1)
	require.NoError(t, err)
	for _, r := range epochAfter.Rewards {
		require.False(t, r.Claimed, "finalized epoch aggregate must stay immutable after claims")
	}

	// === Every rewards invariant holds against the real bank/module accounts. ===
	invariants := map[string]sdk.Invariant{
		"supply-cap":                a.RewardsKeeper.SupplyCapInvariant(),
		"cumulative-emitted":        a.RewardsKeeper.CumulativeEmittedInvariant(),
		"module-balance-coverage":   a.RewardsKeeper.ModuleBalanceCoverageInvariant(),
		"denom-correctness":         a.RewardsKeeper.DenomCorrectnessInvariant(),
		"closed-epoch-immutability": a.RewardsKeeper.ClosedEpochImmutabilityInvariant(),
	}
	for name, inv := range invariants {
		msg, broken := inv(ctx2)
		require.Falsef(t, broken, "invariant %s broken: %s", name, msg)
	}
}
