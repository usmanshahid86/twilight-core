package app_test

import (
	"strconv"
	"testing"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	gogoproto "github.com/cosmos/gogoproto/proto"
	anypb "github.com/cosmos/gogoproto/types/any"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/core/appmodule"
	"cosmossdk.io/log"
	sdkmath "cosmossdk.io/math"

	sdked25519 "github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	"github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"

	"github.com/twilight-project/twilight-core/app"
	coreslotkeeper "github.com/twilight-project/twilight-core/x/coreslot/keeper"
	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"

	appparams "github.com/twilight-project/twilight-core/app/params"
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
	require.Equal(t, []string{"coreslot", "rewards", "mining"}, a.ModuleManager.OrderEndBlockers)
	require.Equal(t, []string{"rewards"}, a.ModuleManager.OrderBeginBlockers)
	// mining materializes settlements for whatever epoch rewards just closed, so
	// it must initialize and end-block after rewards. Its genesis additionally
	// cross-checks already-imported CoreSlot policies, which is why it is last.
	require.Equal(t,
		[]string{"auth", "bank", "consensus", "coreslot", "rewards", "mining"},
		a.ModuleManager.OrderInitGenesis)

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
func TestRewardsEpochFinalizeSuspendAndRelease(t *testing.T) {
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
	// The shortest admissible epoch; toy lengths can no longer boot a chain.
	rParams.EpochLengthBlocks = appparams.HardMinEpochLengthBlocks
	epochLen := int64(rParams.EpochLengthBlocks)
	snapshot := rewardstypes.DefaultEpochConfigSnapshot(rParams)
	require.NoError(t, a.RewardsKeeper.InitGenesis(base, *canonicalRewardsTimeline(&rewardstypes.GenesisState{
		Params:             &rParams,
		State:              &rewardstypes.RewardsState{CurrentEpoch: 1, CurrentEpochStartHeight: 1, CumulativeEmitted: "0", CarryForwardRemainder: "0"},
		CurrentEpochConfig: &snapshot,
	}, 1)))

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
	require.NoError(t, a.RewardsKeeper.EndBlock(ctx2)) // still before the boundary

	// === Blocks 3..epochLen: only slot 2 is active; the last one closes the epoch. ===
	ctx2 = base.WithBlockHeight(epochLen)
	var supplyBefore, supplyAfter sdkmath.Int
	for height := int64(3); height <= epochLen; height++ {
		ctx := base.WithBlockHeight(height)
		require.NoError(t, a.RewardsKeeper.BeginBlock(ctx))
		_, err = a.CoreSlotKeeper.EndBlock(ctx)
		require.NoError(t, err)
		if height == epochLen {
			supplyBefore = a.BankKeeper.GetSupply(ctx, app.BaseDenom).Amount
		}
		require.NoError(t, a.RewardsKeeper.EndBlock(ctx))
		if height == epochLen {
			supplyAfter = a.BankKeeper.GetSupply(ctx, app.BaseDenom).Amount
			ctx2 = ctx
		}
	}

	// Slot 1 earned two blocks before suspension; slot 2 earned the whole epoch.
	const subsidy = 416190
	emission := epochLen * subsidy
	slot1Blocks, slot2Blocks := int64(2), epochLen
	weight := slot1Blocks + slot2Blocks
	mintedEmission := strconv.FormatInt(emission, 10)
	perSlotReward := strconv.FormatInt(emission*slot1Blocks/weight, 10)
	slot2Reward := strconv.FormatInt(emission*slot2Blocks/weight, 10)
	require.Equal(t, mintedEmission, supplyAfter.Sub(supplyBefore).String(),
		"supply must rise by exactly the clipped epoch emission (no double mint)")

	// Finalized aggregate plus canonical entitlements; suspended slot 1 is in.
	epoch, found, err := a.RewardsKeeper.GetFinalizedEpoch(ctx2, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, mintedEmission, epoch.MintedEmission)
	require.Empty(t, epoch.Rewards, "the aggregate embeds no per-Slot rows after the switchover")

	ent1, found, err := a.RewardsKeeper.GetSlotEntitlement(ctx2, 1, 1)
	require.NoError(t, err)
	require.True(t, found, "suspended slot 1 must still hold an entitlement for the epoch it earned")
	require.Equal(t, perSlotReward, ent1.EntitlementAmount)
	require.Equal(t, pay1, ent1.PayoutAddress)
	require.Equal(t, "0", ent1.ReleasedAmount)

	// The counterweight: allocation is proportional to earned blocks, so the slot
	// that stayed active takes the larger share.
	ent2, found, err := a.RewardsKeeper.GetSlotEntitlement(ctx2, 2, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, slot2Reward, ent2.EntitlementAmount)
	require.Equal(t, uint64(slot2Blocks), ent2.TotalBlocksActive)
	require.Equal(t, uint64(slot1Blocks), ent1.TotalBlocksActive,
		"slot 1 earned only the blocks before its suspension took effect")

	// The legacy path owns nothing here.
	_, found, err = a.RewardsKeeper.GetClaimRecord(ctx2, 1, 1)
	require.NoError(t, err)
	require.False(t, found, "V2 finalization creates no claim record")
	require.Error(t, a.RewardsKeeper.ClaimRewards(ctx2, &rewardstypes.MsgClaimRewards{
		Signer: acc(9), SlotId: 1, StartEpoch: 1, EndEpoch: 1,
	}), "the legacy claim path must be unable to pay a V2 obligation")

	// === Release slot 1 (the suspended slot) through the constrained boundary. ===
	//
	// Against the REAL bank keeper and the real module account, so this is where
	// the bank half of the atomicity story is actually proven rather than modeled.
	payAddr1 := mustAddr(t, pay1)
	require.True(t, a.BankKeeper.GetBalance(ctx2, payAddr1, app.BaseDenom).Amount.IsZero())
	liabilityBefore, err := a.RewardsKeeper.GetOutstandingEntitlementLiability(ctx2)
	require.NoError(t, err)

	require.NoError(t, a.RewardsKeeper.PayEntitlementRemainderToOperator(ctx2, 1, 1))

	// The snapshotted payout received exactly the entitlement, the released amount
	// moved with it, and the liability fell by the same value. All three or none.
	require.Equal(t, perSlotReward, a.BankKeeper.GetBalance(ctx2, payAddr1, app.BaseDenom).Amount.String())
	released, found, err := a.RewardsKeeper.GetSlotEntitlement(ctx2, 1, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, perSlotReward, released.ReleasedAmount)
	liabilityAfter, err := a.RewardsKeeper.GetOutstandingEntitlementLiability(ctx2)
	require.NoError(t, err)
	require.Equal(t, perSlotReward, liabilityBefore.Sub(liabilityAfter).String())

	// A second remainder release is a deterministic no-op, not a double payment.
	require.NoError(t, a.RewardsKeeper.PayEntitlementRemainderToOperator(ctx2, 1, 1))
	require.Equal(t, perSlotReward, a.BankKeeper.GetBalance(ctx2, payAddr1, app.BaseDenom).Amount.String())

	// Suspension did not confiscate: §64 keeps an earned obligation payable.
	suspended, err := a.CoreSlotKeeper.GetSlot(ctx2, 1)
	require.NoError(t, err)
	require.Equal(t, coreslottypes.SlotStatus_SLOT_STATUS_SUSPENDED, suspended.Status)

	// === Every rewards invariant holds against the real bank/module accounts. ===
	invariants := map[string]sdk.Invariant{
		"supply-cap":                a.RewardsKeeper.SupplyCapInvariant(),
		"cumulative-emitted":        a.RewardsKeeper.CumulativeEmittedInvariant(),
		"module-balance-coverage":   a.RewardsKeeper.ModuleBalanceCoverageInvariant(),
		"denom-correctness":         a.RewardsKeeper.DenomCorrectnessInvariant(),
		"closed-epoch-immutability": a.RewardsKeeper.ClosedEpochImmutabilityInvariant(),
		"entitlement-liability":     a.RewardsKeeper.EntitlementLiabilityInvariant(),
	}
	for name, inv := range invariants {
		msg, broken := inv(ctx2)
		require.Falsef(t, broken, "invariant %s broken: %s", name, msg)
	}
}
