package app_test

// Branch-coverage drills.
//
// The C1 endurance soak and the existing app-level tests only ever drove the
// rewards "happy path": a single active slot, equal active-block counts, and
// default params (treasury 0 bps, no carry-in, no halving threshold crossed).
// Several core economic branches are unit-tested in x/rewards/keeper but were
// never driven through the assembled app with the full wiring (real bank/mint,
// real CoreSlot, runtime EndBlock dispatch order, invariants).
//
// These drills force each of those branches to fire and re-assert the accounting
// identity across the relevant boundary:
//
//	1. Halving / subsidy decay      - TestDrillHalvingSubsidyDecay (real FinalizeBlock)
//	2. Nonzero carry-forward        - TestDrillNonzeroCarryForward
//	3+5. Non-uniform blocks + churn - TestDrillNonUniformBlocksAndChurn
//	4. Treasury bps split           - TestDrillTreasuryBpsSplit (real FinalizeBlock)
//	6. max_claim_epochs_per_tx cap  - TestDrillMaxClaimEpochsCap
//	combined                        - TestDrillCombinedAllBranches
//
// Single-slot, no-claim, no-churn branches (halving, treasury) run through the
// real ABCI FinalizeBlock loop as a runtime anchor. Branches that need multiple
// slots, mid-epoch CoreSlot churn, or claims use the direct-keeper integration
// pattern established by TestRewardsShortEpochFinalizeSuspendClaim (real bank,
// real module accounts, real CoreSlot msg server, runtime EndBlock dispatch
// order coreslot -> rewards). Runtime FinalizeBlock dispatch itself is already
// proven by TestRewardsRuntimeDispatchFinalizeBlock; these drills target the
// economic branch logic, which is identical regardless of the caller.

import (
	"strconv"
	"testing"
	"time"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/app"
	appparams "github.com/twilight-project/twilight-core/app/params"
	coreslotkeeper "github.com/twilight-project/twilight-core/x/coreslot/keeper"
	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	rewardstypes "github.com/twilight-project/twilight-core/x/rewards/types"
)

// slotSpec describes one ACTIVE CoreSlot slot to bake into genesis.
type slotSpec struct {
	id        uint64
	operator  string
	payout    string
	keyMarker byte // distinct per slot: CoreSlot rejects duplicate consensus keys
}

// initCoreSlotsAndRewards seeds N active CoreSlot slots plus the supplied rewards
// genesis through the real keepers (direct InitGenesis on the booted app),
// generalizing the single-slot initChainWithRewards scaffold to the multi-slot
// scenarios the branch drills need.
// coreSlotRegisterMsg builds a registration message carrying the mandatory V2
// fields, so a test about authorization or key uniqueness observes the rejection
// it is about rather than a missing settlement address.
func coreSlotRegisterMsg(t *testing.T, authority, operator, payout string, keyMarker byte, moniker string) *coreslottypes.MsgRegisterCoreSlot {
	t.Helper()
	return &coreslottypes.MsgRegisterCoreSlot{
		Authority: authority, OperatorAddress: operator, PayoutAddress: payout,
		SettlementAddress: payout,
		ConsensusPubkey:   ed25519Any(t, keyMarker),
		Metadata:          &coreslottypes.OperatorMetadata{Moniker: moniker},
		InitialSelectionPolicy: &coreslottypes.InitialSelectionPolicy{
			SelectionRateBps: 2_500, MaxSelectedParticipants: 10,
		},
	}
}

func initCoreSlotsAndRewards(t *testing.T, a *app.App, base sdk.Context, slots []slotSpec, rGen rewardstypes.GenesisState) {
	t.Helper()
	csParams := coreslottypes.DefaultParams(app.AuthorityAddress(), app.EmergencyAuthorityAddress())
	csGen := &coreslottypes.GenesisState{Params: &csParams, NextSlotId: uint64(len(slots)) + 1}
	// Fresh-genesis ACTIVE normalization (§80): the first activation generation,
	// effective from the initial height, with a version-1 Selection policy the
	// slot's pointer names.
	initialHeight := base.BlockHeight()
	for _, s := range slots {
		csGen.Slots = append(csGen.Slots, &coreslottypes.CoreSlot{
			SlotId: s.id, OperatorAddress: s.operator, PayoutAddress: s.payout,
			SettlementAddress: s.payout,
			ConsensusPubkey:   ed25519Any(t, s.keyMarker),
			Status:            coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE,
			ConsensusPower:    1, RewardWeight: coreslottypes.DefaultRewardWeight,
			ActivationSequence: 1, ActivatedHeight: initialHeight, ActivationEffectiveHeight: initialHeight,
			CurrentSelectionPolicyVersion: 1,
		})
		csGen.SelectionPolicies = append(csGen.SelectionPolicies, &coreslottypes.SelectionPolicyVersion{
			SlotId: s.id, PolicyVersion: 1, SelectionRateBps: 2_500, MaxSelectedParticipants: 10,
			ValidFromHeight: initialHeight,
		})
		csGen.RewardWeights = append(csGen.RewardWeights, &coreslottypes.OperatorRewardWeight{
			SlotId: s.id, FinalWeight: coreslottypes.DefaultRewardWeight,
		})
	}
	_, err := a.CoreSlotKeeper.InitGenesis(base, csGen)
	require.NoError(t, err)
	require.NoError(t, a.RewardsKeeper.InitGenesis(base, rGen))
}

// driveBlock runs one block through the rewards/coreslot keepers in the resolved
// runtime dispatch order: rewards.BeginBlock, then coreslot.EndBlock, then
// rewards.EndBlock (BeginBlockers=[rewards], EndBlockers=[coreslot,rewards]).
func driveBlock(t *testing.T, a *app.App, base sdk.Context, height int64) sdk.Context {
	t.Helper()
	ctx := base.WithBlockHeight(height)
	require.NoError(t, a.RewardsKeeper.BeginBlock(ctx))
	_, err := a.CoreSlotKeeper.EndBlock(ctx)
	require.NoError(t, err)
	require.NoError(t, a.RewardsKeeper.EndBlock(ctx))
	return ctx
}

// assertInvariants runs every rewards invariant against the real bank/module
// state and fails on any broken one.
func assertInvariants(t *testing.T, a *app.App, ctx sdk.Context) {
	t.Helper()
	// Stored under the plain func signature rather than the deprecated
	// sdk.Invariant named type (tied to x/crisis), which the linter rejects.
	invariants := map[string]func(sdk.Context) (string, bool){
		"supply-cap":                a.RewardsKeeper.SupplyCapInvariant(),
		"cumulative-emitted":        a.RewardsKeeper.CumulativeEmittedInvariant(),
		"module-balance-coverage":   a.RewardsKeeper.ModuleBalanceCoverageInvariant(),
		"denom-correctness":         a.RewardsKeeper.DenomCorrectnessInvariant(),
		"closed-epoch-immutability": a.RewardsKeeper.ClosedEpochImmutabilityInvariant(),
	}
	for name, inv := range invariants {
		msg, broken := inv(ctx)
		require.Falsef(t, broken, "invariant %s broken: %s", name, msg)
	}
}

func intStr(t *testing.T, s string) math.Int {
	t.Helper()
	v, ok := math.NewIntFromString(s)
	require.Truef(t, ok, "parse integer %q", s)
	return v
}

// rewardsParams builds rewards Params from defaults with the common drill knobs
// overridden, and the matching epoch-config snapshot (finalize reads the
// snapshot, not live params, so the two must agree).
func rewardsParams(t *testing.T, mutate func(p *rewardstypes.Params)) (rewardstypes.Params, rewardstypes.EpochConfigSnapshot) {
	t.Helper()
	p := rewardstypes.DefaultParams()
	mutate(&p)
	require.NoError(t, p.Validate(), "drill params must be valid")
	snap := rewardstypes.DefaultEpochConfigSnapshot(p)
	require.NoError(t, snap.Validate(), "drill snapshot must be valid")
	return p, snap
}

func genesisState(p rewardstypes.Params, snap rewardstypes.EpochConfigSnapshot) rewardstypes.GenesisState {
	gen := rewardstypes.GenesisState{
		Params:             &p,
		State:              &rewardstypes.RewardsState{CurrentEpoch: 1, CurrentEpochStartHeight: 1, CumulativeEmitted: "0", CarryForwardRemainder: "0"},
		CurrentEpochConfig: &snap,
	}
	return *canonicalRewardsTimeline(&gen, 1)
}

// canonicalRewardsTimeline fills the canonical timeline fields every fresh
// rewards genesis now requires: the original-genesis epoch anchor, the initial
// reward-configuration anchor, the empty reward schedule, the single pause state,
// and the open reward-enabled block counter.
//
// None of them has a runtime default — after genesis an absent one is corruption
// rather than a zero value — so a fixture that omitted them would be testing
// against state no chain is ever in.
func canonicalRewardsTimeline(gen *rewardstypes.GenesisState, initialHeight uint64) *rewardstypes.GenesisState {
	anchor := rewardstypes.DefaultEpochConfigVersion(*gen.Params, initialHeight)
	anchor.EffectiveEpoch = gen.State.CurrentEpoch
	anchor.EffectiveStartHeight = gen.State.CurrentEpochStartHeight
	gen.EpochConfigVersions = []*rewardstypes.EpochConfigVersion{&anchor}
	// The reward-configuration anchor is derived from the same Params the epoch
	// anchor is, which is what keeps the deprecated economic mirrors pinned to it.
	// A fixture that set the economics on Params alone would now be rejected at
	// genesis, and correctly so: the mirrors would name different numbers than the
	// version that governs every epoch this fixture finalizes.
	rewardAnchor := rewardstypes.DefaultRewardConfigVersion(*gen.Params)
	gen.RewardConfigVersions = []*rewardstypes.RewardConfigVersion{&rewardAnchor}
	gen.ScheduledRewardConfigs = nil
	gen.SlotEntitlements = nil
	gen.OutstandingEntitlementLiability = "0"
	gen.PauseState = &rewardstypes.RewardsPauseState{}
	gen.OpenRewardEnabledBlocks = 0
	return gen
}

// ===========================================================================
// Branch 1: Halving / subsidy decay (supply-threshold tier change).
//
// Driven through the REAL ABCI FinalizeBlock loop. maxSupply=1000, subsidy=100,
// epoch length 5: epoch 1 emits 5*100=500 entirely in tier 0; crossing the first
// threshold (maxSupply/2 = 500) drops the per-block subsidy to 50, so epoch 2
// emits exactly 5*50=250 -- a clean, exact halving across the boundary. This is
// the headline economic branch C1 never exercised (default first halving is
// ~1,460 epochs away).
// ===========================================================================
// drillEpochLen is the epoch length every real-app drill boots with.
//
// The ratified admission interval is [HardMinEpochLengthBlocks,
// HardMaxEpochLengthBlocks], so a chain can no longer be started with the toy
// two- or five-block epochs these drills used to use. Each drill therefore keeps
// its economic branch but rescales its monetary parameters against this length,
// and drives whole epochs rather than a handful of blocks.
const drillEpochLen = int64(appparams.HardMinEpochLengthBlocks)

// drillOddEpochLen is an ODD admissible epoch length.
//
// A carry remainder needs a pool that does not divide evenly across equal slots.
// The floor is even, so an even length can never produce an odd emission from an
// integer subsidy; one block more is still inside the ratified interval and
// restores the odd case the carry drill depends on.
const drillOddEpochLen = drillEpochLen + 1

// driveBlocks runs count keeper-level blocks starting at height from, matching
// driveBlock's style. It exists because driveBlock and the ABCI-loop helper below
// advance different machinery and must not be mixed within one drill.
func driveBlocks(t *testing.T, a *app.App, base sdk.Context, from, count int64) sdk.Context {
	t.Helper()
	ctx := base.WithBlockHeight(from)
	for height := from; height < from+count; height++ {
		ctx = driveBlock(t, a, base, height)
	}
	return ctx
}

// driveEpochBlocks runs count blocks of the real ABCI loop starting at height
// from, and returns a context at the last height.
func driveEpochBlocks(t *testing.T, a *app.App, from, count int64) sdk.Context {
	t.Helper()
	blockTime := time.Unix(1_700_000_000, 0).UTC()
	last := from
	for height := from; height < from+count; height++ {
		_, err := a.FinalizeBlock(&abci.RequestFinalizeBlock{
			Height: height, Time: blockTime.Add(time.Duration(height) * time.Second),
		})
		require.NoError(t, err)
		_, err = a.Commit()
		require.NoError(t, err)
		last = height
	}
	return a.NewUncachedContext(false, cmtproto.Header{Height: last})
}

func TestDrillHalvingSubsidyDecay(t *testing.T) {
	a := bootApp(t)
	// Scaled so epoch 1 lands EXACTLY on the first supply threshold: one epoch
	// emits epochLen * subsidy, and max supply is twice that, so
	// threshold_1 = maxSupply/2 is reached precisely at the epoch-1 boundary.
	const subsidy = 100
	epochEmission := drillEpochLen * subsidy
	maxSupply := 2 * epochEmission

	p, snap := rewardsParams(t, func(p *rewardstypes.Params) {
		p.MaxSupply = strconv.FormatInt(maxSupply, 10)
		p.InitialBlockSubsidy = strconv.Itoa(subsidy)
		p.EpochLengthBlocks = appparams.HardMinEpochLengthBlocks
	})
	gen := genesisState(p, snap)
	initChainWithRewards(t, a, &gen)

	ctx := driveEpochBlocks(t, a, 1, 2*drillEpochLen)

	halved := epochEmission / 2

	epoch1, found, err := a.RewardsKeeper.GetFinalizedEpoch(ctx, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, strconv.FormatInt(epochEmission, 10), epoch1.MintedEmission,
		"epoch 1 emits one epoch of blocks at the tier-0 subsidy")
	require.Equal(t, strconv.FormatInt(epochEmission, 10), epoch1.CumulativeEmittedAfterEpoch)

	epoch2, found, err := a.RewardsKeeper.GetFinalizedEpoch(ctx, 2)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, strconv.FormatInt(halved, 10), epoch2.MintedEmission,
		"the subsidy halves once cumulative reaches the maxSupply/2 threshold")
	require.Equal(t, strconv.FormatInt(epochEmission+halved, 10), epoch2.CumulativeEmittedAfterEpoch)

	// The realized halving: epoch 2 emission is exactly half of epoch 1.
	require.Equal(t, intStr(t, epoch1.MintedEmission).QuoRaw(2), intStr(t, epoch2.MintedEmission),
		"emission must halve exactly across the supply threshold")

	// Single slot takes the whole pool each epoch; the per-slot reward halves too.
	rec1, found, err := a.RewardsKeeper.GetClaimRecord(ctx, 1, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, strconv.FormatInt(epochEmission, 10), rec1.Amount)
	rec2, found, err := a.RewardsKeeper.GetClaimRecord(ctx, 1, 2)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, strconv.FormatInt(halved, 10), rec2.Amount)

	// Identity (zero premine, zero treasury): supply == cumulative; both <= maxSupply.
	state, err := a.RewardsKeeper.GetState(ctx)
	require.NoError(t, err)
	require.Equal(t, strconv.FormatInt(epochEmission+halved, 10), state.CumulativeEmitted)
	require.Equal(t, strconv.FormatInt(epochEmission+halved, 10), a.BankKeeper.GetSupply(ctx, app.BaseDenom).Amount.String(),
		"supply must equal cumulative emitted under zero premine")
	assertInvariants(t, a, ctx)
}

// ===========================================================================
// Branch 4: Treasury basis-point split.
//
// Driven through the REAL ABCI FinalizeBlock loop. subsidy=1000, epoch length 2,
// EmissionTreasuryShareBps=1000 (10%): emission 2000 is minted in full, 200 is
// sent to the treasury address, and the remaining 1800 is the distributable pool
// for the single slot. C1 ran 0 bps, so PayTreasury always hit its zero-amount
// early return and the split path never fired.
// ===========================================================================
func TestDrillTreasuryBpsSplit(t *testing.T) {
	a := bootApp(t)
	treasury := acc(30)
	p, snap := rewardsParams(t, func(p *rewardstypes.Params) {
		p.InitialBlockSubsidy = "1000"
		p.EpochLengthBlocks = appparams.HardMinEpochLengthBlocks
		p.EmissionTreasuryShareBps = 1000 // 10%
		p.TreasuryAddress = treasury
	})
	gen := genesisState(p, snap)
	initChainWithRewards(t, a, &gen)

	ctx := driveEpochBlocks(t, a, 1, drillEpochLen)

	// One epoch of blocks at 1000 subsidy; 10% to treasury, the rest is the pool.
	emission := drillEpochLen * 1000
	treasuryCut := emission / 10
	pool := emission - treasuryCut

	epoch, found, err := a.RewardsKeeper.GetFinalizedEpoch(ctx, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, strconv.FormatInt(emission, 10), epoch.MintedEmission)
	require.Equal(t, strconv.FormatInt(treasuryCut, 10), epoch.TreasuryAmount, "10% of the emission")
	require.Equal(t, strconv.FormatInt(pool, 10), epoch.RewardPool, "pool is emission minus treasury")
	require.Equal(t, strconv.FormatInt(pool, 10), epoch.AllocatedAmount)
	require.Equal(t, "0", epoch.CarryOut)

	// Treasury address actually received the split out of the minted coins.
	require.Equal(t, strconv.FormatInt(treasuryCut, 10), a.BankKeeper.GetBalance(ctx, mustAddr(t, treasury), app.BaseDenom).Amount.String(),
		"treasury address must hold exactly the split amount")

	// Single slot's claimable reward is the post-treasury pool.
	rec, found, err := a.RewardsKeeper.GetClaimRecord(ctx, 1, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, strconv.FormatInt(pool, 10), rec.Amount)

	// Identity with treasury: emission is fully minted (counts in supply and
	// cumulative); the treasury portion left the module but stays in supply.
	state, err := a.RewardsKeeper.GetState(ctx)
	require.NoError(t, err)
	require.Equal(t, strconv.FormatInt(emission, 10), state.CumulativeEmitted)
	require.Equal(t, strconv.FormatInt(emission, 10), a.BankKeeper.GetSupply(ctx, app.BaseDenom).Amount.String(),
		"supply == cumulative; treasury coins are still in circulation")
	// Coverage: the module balance covers the single unclaimed reward; treasury
	// coins are excluded from both sides of the coverage invariant.
	require.Equal(t, strconv.FormatInt(pool, 10), a.BankKeeper.GetBalance(ctx, a.AccountKeeper.GetModuleAddress(rewardstypes.ModuleName), app.BaseDenom).Amount.String())
	assertInvariants(t, a, ctx)
}

// ===========================================================================
// Branch 2: Nonzero carry-forward remainder (produced AND consumed).
//
// A single slot always takes the whole pool, so a remainder requires >=2 slots
// with a non-divisible pool. Two equal slots with an ODD pool (epoch length 1,
// subsidy 416191) leave carryOut=1 in epoch 1; epoch 2 then folds that carry-in
// back into the pool (416191 + 1 = 416192), proving carry is both produced and
// consumed across a real epoch boundary. C1 ran an even subsidy that divided
// evenly across 4 uniform slots, so carryOut was always 0.
// ===========================================================================
func TestDrillNonzeroCarryForward(t *testing.T) {
	a := bootApp(t)
	base := a.NewUncachedContext(false, cmtproto.Header{Height: 1})

	// An ODD subsidy over an ODD epoch length gives an odd emission, which two
	// equal slots cannot divide.
	const subsidy = 416191
	emission := drillOddEpochLen * subsidy
	require.Equal(t, int64(1), emission%2, "the drill needs an odd pool to produce a carry")

	p, snap := rewardsParams(t, func(p *rewardstypes.Params) {
		p.InitialBlockSubsidy = strconv.Itoa(subsidy)
		p.EpochLengthBlocks = uint64(drillOddEpochLen)
	})
	initCoreSlotsAndRewards(t, a, base, []slotSpec{
		{id: 1, operator: acc(2), payout: acc(12), keyMarker: 1},
		{id: 2, operator: acc(3), payout: acc(13), keyMarker: 2},
	}, genesisState(p, snap))

	// Epoch 1: the odd pool leaves a remainder of exactly 1.
	ctx1 := driveBlocks(t, a, base, 1, drillOddEpochLen)
	epoch1, found, err := a.RewardsKeeper.GetFinalizedEpoch(ctx1, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, strconv.FormatInt(emission, 10), epoch1.MintedEmission)
	require.Equal(t, "0", epoch1.CarryIn)
	require.Equal(t, strconv.FormatInt(emission, 10), epoch1.RewardPool)
	require.Equal(t, strconv.FormatInt(emission-1, 10), epoch1.AllocatedAmount)
	require.Equal(t, "1", epoch1.CarryOut, "odd pool split across 2 equal slots leaves a remainder of 1")

	state1, err := a.RewardsKeeper.GetState(ctx1)
	require.NoError(t, err)
	require.Equal(t, "1", state1.CarryForwardRemainder, "carry persists into the next epoch's state")
	assertInvariants(t, a, ctx1)

	// Epoch 2: the carry-in is folded into the pool, which now divides cleanly.
	ctx2 := driveBlocks(t, a, base, drillOddEpochLen+1, drillOddEpochLen)
	epoch2, found, err := a.RewardsKeeper.GetFinalizedEpoch(ctx2, 2)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, strconv.FormatInt(emission, 10), epoch2.MintedEmission)
	require.Equal(t, "1", epoch2.CarryIn, "epoch 1's carryOut is epoch 2's carryIn")
	require.Equal(t, strconv.FormatInt(emission+1, 10), epoch2.RewardPool, "pool = emission + carryIn")
	require.Equal(t, strconv.FormatInt(emission+1, 10), epoch2.AllocatedAmount, "even pool now divides cleanly across 2 slots")
	require.Equal(t, "0", epoch2.CarryOut)

	// Identity: cumulative = sum of emissions; allocated + carry == cumulative.
	state2, err := a.RewardsKeeper.GetState(ctx2)
	require.NoError(t, err)
	require.Equal(t, strconv.FormatInt(2*emission, 10), state2.CumulativeEmitted)
	require.Equal(t, "0", state2.CarryForwardRemainder)
	allocated := intStr(t, epoch1.AllocatedAmount).Add(intStr(t, epoch2.AllocatedAmount))
	require.Equal(t, state2.CumulativeEmitted, allocated.String(),
		"with carry fully consumed, total allocated equals cumulative emitted")
	require.Equal(t, state2.CumulativeEmitted, a.BankKeeper.GetSupply(ctx2, app.BaseDenom).Amount.String())
	assertInvariants(t, a, ctx2)
}

// ===========================================================================
// Branches 3 + 5: Non-uniform BlocksActive via active-set churn mid-emission.
//
// Two slots active at the start of a 3-block epoch. Slot 1 is suspended through
// the real CoreSlot msg server after block 1, so BeginBlock at blocks 2 and 3
// observes only slot 2. Slot 1 ends the epoch with BlocksActive=1, slot 2 with
// BlocksActive=3 -> non-uniform allocation (1:3) AND a real active-set change
// mid-emission. The suspended-but-earned slot still finalizes and stays
// claimable. C1 kept a fixed, uniformly-active set, so neither branch fired.
// ===========================================================================
func TestDrillNonUniformBlocksAndChurn(t *testing.T) {
	a := bootApp(t)
	base := a.NewUncachedContext(false, cmtproto.Header{Height: 1})

	op1, pay1 := acc(2), acc(12)
	p, snap := rewardsParams(t, func(p *rewardstypes.Params) {
		p.InitialBlockSubsidy = "100"
		p.EpochLengthBlocks = appparams.HardMinEpochLengthBlocks
	})
	initCoreSlotsAndRewards(t, a, base, []slotSpec{
		{id: 1, operator: op1, payout: pay1, keyMarker: 1},
		{id: 2, operator: acc(3), payout: acc(13), keyMarker: 2},
	}, genesisState(p, snap))

	// Block 1: both slots active, both credited.
	driveBlock(t, a, base, 1)

	// Suspend slot 1 mid-epoch through the real CoreSlot path, BEFORE block 2's
	// BeginBlock, so every remaining block of the epoch observes only slot 2.
	csMsg := coreslotkeeper.NewMsgServer(a.CoreSlotKeeper)
	_, err := csMsg.SuspendCoreSlot(base.WithBlockHeight(2), &coreslottypes.MsgSuspendCoreSlot{
		Authority: app.AuthorityAddress(), SlotId: 1, Reason: "branch-coverage-churn-drill",
	})
	require.NoError(t, err)

	// The rest of the epoch: only slot 2 is active. The last block closes it.
	ctx3 := driveBlocks(t, a, base, 2, drillEpochLen-1)

	// Slot 1 earned exactly one block; slot 2 earned the whole epoch.
	emission := drillEpochLen * 100
	slot1Blocks, slot2Blocks := int64(1), drillEpochLen
	totalWeight := slot1Blocks + slot2Blocks
	slot1Amount := emission * slot1Blocks / totalWeight
	slot2Amount := emission * slot2Blocks / totalWeight
	allocated := slot1Amount + slot2Amount

	// Allocation is proportional to active blocks (1:3), not uniform. The
	// per-slot active-block counts are preserved on the finalized claim records
	// (the live ActiveBlocks counters are deleted once the epoch finalizes).
	epoch, found, err := a.RewardsKeeper.GetFinalizedEpoch(ctx3, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, strconv.FormatInt(emission, 10), epoch.MintedEmission)
	require.Equal(t, strconv.FormatInt(allocated, 10), epoch.AllocatedAmount)
	require.Equal(t, strconv.FormatInt(emission-allocated, 10), epoch.CarryOut,
		"whatever the floors leave is carry, and it is bounded by one per positive slot")
	require.LessOrEqual(t, emission-allocated, int64(1), "two positive slots leave at most one unit")

	rec1, found, err := a.RewardsKeeper.GetClaimRecord(ctx3, 1, 1)
	require.NoError(t, err)
	require.True(t, found, "suspended-but-earned slot 1 still has a claim record")
	require.Equal(t, uint64(1), rec1.BlocksActive, "slot 1 was active only for block 1 before suspension")
	require.Equal(t, strconv.FormatInt(slot1Amount, 10), rec1.Amount, "one block out of the epoch's total weight")
	require.Equal(t, pay1, rec1.PayoutAddress)
	rec2, found, err := a.RewardsKeeper.GetClaimRecord(ctx3, 2, 1)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, uint64(slot2Blocks), rec2.BlocksActive, "slot 2 stayed active for the whole epoch")
	require.Equal(t, strconv.FormatInt(slot2Amount, 10), rec2.Amount)

	// CoreSlot reflects the churn: slot 1 is suspended but its row/payout survive.
	s1, err := a.CoreSlotKeeper.GetSlot(ctx3, 1)
	require.NoError(t, err)
	require.Equal(t, coreslottypes.SlotStatus_SLOT_STATUS_SUSPENDED, s1.Status)
	require.Equal(t, pay1, s1.PayoutAddress)

	// The suspended slot's reward is still claimable to its snapshotted payout.
	require.NoError(t, a.RewardsKeeper.ClaimRewards(ctx3, &rewardstypes.MsgClaimRewards{
		Signer: acc(9), SlotId: 1, StartEpoch: 1, EndEpoch: 1,
	}))
	require.Equal(t, strconv.FormatInt(slot1Amount, 10), a.BankKeeper.GetBalance(ctx3, mustAddr(t, pay1), app.BaseDenom).Amount.String())

	// Identity holds across the churn.
	require.Equal(t, strconv.FormatInt(emission, 10), a.BankKeeper.GetSupply(ctx3, app.BaseDenom).Amount.String())
	assertInvariants(t, a, ctx3)
}

// ===========================================================================
// Branch 6: max_claim_epochs_per_tx cap.
//
// MaxClaimEpochsPerTx=3 with 4 finalized epochs (epoch length 1, single slot).
// The cap rejects when EndEpoch-StartEpoch >= cap, so a [1,4] claim (delta 3) is
// rejected while a [1,3] claim (delta 2, three inclusive epochs) is the maximum
// allowed span. C1 used the default cap of 100 and never claimed near it.
// ===========================================================================
func TestDrillMaxClaimEpochsCap(t *testing.T) {
	a := bootApp(t)
	base := a.NewUncachedContext(false, cmtproto.Header{Height: 1})

	pay := acc(12)
	p, snap := rewardsParams(t, func(p *rewardstypes.Params) {
		p.InitialBlockSubsidy = "100"
		p.EpochLengthBlocks = appparams.HardMinEpochLengthBlocks
		p.MaxClaimEpochsPerTx = 3
	})
	initCoreSlotsAndRewards(t, a, base, []slotSpec{
		{id: 1, operator: acc(2), payout: pay, keyMarker: 1},
	}, genesisState(p, snap))

	// Four full epochs -> four finalized epochs, each crediting the single slot
	// one epoch's worth of subsidy.
	perEpoch := drillEpochLen * 100
	ctx := driveBlocks(t, a, base, 1, 4*drillEpochLen)
	for epoch := uint64(1); epoch <= 4; epoch++ {
		rec, found, err := a.RewardsKeeper.GetClaimRecord(ctx, 1, epoch)
		require.NoError(t, err)
		require.Truef(t, found, "epoch %d must be finalized with a claim record", epoch)
		require.Equal(t, strconv.FormatInt(perEpoch, 10), rec.Amount)
	}

	// A span exceeding the cap is rejected (delta 3 >= cap 3).
	err := a.RewardsKeeper.ClaimRewards(ctx, &rewardstypes.MsgClaimRewards{
		Signer: acc(9), SlotId: 1, StartEpoch: 1, EndEpoch: 4,
	})
	require.Error(t, err, "a 4-epoch span must exceed MaxClaimEpochsPerTx=3")
	require.Contains(t, err.Error(), "claim range exceeds maximum")

	// Nothing was paid by the rejected claim.
	require.True(t, a.BankKeeper.GetBalance(ctx, mustAddr(t, pay), app.BaseDenom).Amount.IsZero(),
		"a rejected over-cap claim must pay nothing")

	// The maximum allowed span (delta 2, epochs 1..3) succeeds and pays three
	// epochs' worth.
	require.NoError(t, a.RewardsKeeper.ClaimRewards(ctx, &rewardstypes.MsgClaimRewards{
		Signer: acc(9), SlotId: 1, StartEpoch: 1, EndEpoch: 3,
	}))
	require.Equal(t, strconv.FormatInt(3*perEpoch, 10), a.BankKeeper.GetBalance(ctx, mustAddr(t, pay), app.BaseDenom).Amount.String())

	// Epoch 4 is still unclaimed and claimable on its own.
	rec4, _, err := a.RewardsKeeper.GetClaimRecord(ctx, 1, 4)
	require.NoError(t, err)
	require.False(t, rec4.Claimed, "epoch 4 was outside the allowed span and remains unclaimed")
	require.NoError(t, a.RewardsKeeper.ClaimRewards(ctx, &rewardstypes.MsgClaimRewards{
		Signer: acc(9), SlotId: 1, StartEpoch: 4, EndEpoch: 4,
	}))
	require.Equal(t, strconv.FormatInt(4*perEpoch, 10), a.BankKeeper.GetBalance(ctx, mustAddr(t, pay), app.BaseDenom).Amount.String())

	assertInvariants(t, a, ctx)
}

// ===========================================================================
// Combined: all branches composing in one chain.
//
// 3 slots, epoch length 3, subsidy=100, maxSupply=1000 (forces a halving as
// cumulative crosses 500), EmissionTreasuryShareBps=1000 (treasury split every
// epoch), MaxClaimEpochsPerTx=2 (cap fires on a 3-epoch span). Slot 3 is
// suspended mid-epoch to churn the active set and produce non-uniform blocks,
// which in turn yields non-divisible pools and nonzero carry-forward. The
// branches are NOT isolated here -- the point is that they compose and the full
// accounting identity survives every boundary:
//
//	cumulative = Sum(treasury) + Sum(allocated) + carry
//	supply     = cumulative                       (zero premine)
//
// ===========================================================================
func TestDrillCombinedAllBranches(t *testing.T) {
	a := bootApp(t)
	base := a.NewUncachedContext(false, cmtproto.Header{Height: 1})

	treasury := acc(40)
	pay1 := acc(12)
	// Max supply is twice a single epoch's emission, so the first supply
	// threshold is crossed at the epoch-1 boundary and later epochs emit at a
	// decayed tier — the same shape as the original two-epoch-per-halving setup,
	// rescaled to an admissible epoch length.
	const combinedSubsidy = 100
	combinedEpochEmission := drillEpochLen * combinedSubsidy
	combinedMaxSupply := 2 * combinedEpochEmission

	p, snap := rewardsParams(t, func(p *rewardstypes.Params) {
		p.MaxSupply = strconv.FormatInt(combinedMaxSupply, 10)
		p.InitialBlockSubsidy = strconv.Itoa(combinedSubsidy)
		p.EpochLengthBlocks = appparams.HardMinEpochLengthBlocks
		p.EmissionTreasuryShareBps = 1000 // 10% to treasury every epoch
		p.TreasuryAddress = treasury
		p.MaxClaimEpochsPerTx = 2
	})
	initCoreSlotsAndRewards(t, a, base, []slotSpec{
		{id: 1, operator: acc(2), payout: pay1, keyMarker: 1},
		{id: 2, operator: acc(3), payout: acc(13), keyMarker: 2},
		{id: 3, operator: acc(4), payout: acc(14), keyMarker: 3},
	}, genesisState(p, snap))

	// reconcile asserts the full identity against every finalized epoch so far.
	reconcile := func(ctx sdk.Context, throughEpoch uint64) {
		state, err := a.RewardsKeeper.GetState(ctx)
		require.NoError(t, err)
		sumEmission := math.ZeroInt()
		sumTreasury := math.ZeroInt()
		sumAllocated := math.ZeroInt()
		for e := uint64(1); e <= throughEpoch; e++ {
			epoch, found, err := a.RewardsKeeper.GetFinalizedEpoch(ctx, e)
			require.NoError(t, err)
			require.Truef(t, found, "epoch %d must be finalized", e)
			sumEmission = sumEmission.Add(intStr(t, epoch.MintedEmission))
			sumTreasury = sumTreasury.Add(intStr(t, epoch.TreasuryAmount))
			sumAllocated = sumAllocated.Add(intStr(t, epoch.AllocatedAmount))
		}
		carry := intStr(t, state.CarryForwardRemainder)

		// cumulative == sum of minted emissions.
		require.Equal(t, sumEmission.String(), state.CumulativeEmitted,
			"cumulative emitted must equal the sum of per-epoch minted emissions")
		// Full identity with treasury: emission telescopes to treasury + allocated + carry.
		require.Equal(t, sumEmission.String(), sumTreasury.Add(sumAllocated).Add(carry).String(),
			"cumulative = Sum(treasury) + Sum(allocated) + carry")
		// Zero premine: supply == cumulative.
		require.Equal(t, state.CumulativeEmitted, a.BankKeeper.GetSupply(ctx, app.BaseDenom).Amount.String(),
			"supply must equal cumulative emitted under zero premine")
		// Treasury address holds exactly the cumulative treasury split.
		require.Equal(t, sumTreasury.String(), a.BankKeeper.GetBalance(ctx, mustAddr(t, treasury), app.BaseDenom).Amount.String(),
			"treasury balance must equal the cumulative treasury split")
		assertInvariants(t, a, ctx)
	}

	// --- Epoch 1: all 3 slots uniformly active. ---
	ctx := driveBlocks(t, a, base, 1, drillEpochLen)
	reconcile(ctx, 1)

	// --- Epoch 2: churn -- suspend slot 3 after its first block. ---
	epoch2Start := drillEpochLen + 1
	driveBlock(t, a, base, epoch2Start)
	csMsg := coreslotkeeper.NewMsgServer(a.CoreSlotKeeper)
	_, err := csMsg.SuspendCoreSlot(base.WithBlockHeight(epoch2Start+1), &coreslottypes.MsgSuspendCoreSlot{
		Authority: app.AuthorityAddress(), SlotId: 3, Reason: "combined-drill-churn",
	})
	require.NoError(t, err)
	ctx = driveBlocks(t, a, base, epoch2Start+1, drillEpochLen-1)
	// Slot 3 was active only for the first block of epoch 2; slots 1 and 2 for all
	// of it. Read the preserved count off the finalized claim record, since the
	// live counters are deleted at finalization.
	rec3, found, err := a.RewardsKeeper.GetClaimRecord(ctx, 3, 2)
	require.NoError(t, err)
	require.True(t, found, "suspended-but-earned slot 3 still has a claim record for epoch 2")
	require.Equal(t, uint64(1), rec3.BlocksActive, "suspended slot 3 earned only the first block of epoch 2")
	reconcile(ctx, 2)

	// --- Epochs 3 and 4: only slots 1,2 active; cross the halving. ---
	ctx = driveBlocks(t, a, base, 2*drillEpochLen+1, 2*drillEpochLen)
	reconcile(ctx, 4)

	// Confirm the headline branches actually fired over the run.
	state, err := a.RewardsKeeper.GetState(ctx)
	require.NoError(t, err)
	require.True(t, intStr(t, state.CumulativeEmitted).GT(math.NewInt(combinedMaxSupply/2)),
		"run must cross the maxSupply/2 halving threshold")
	// Some epoch must show subsidy decay (emission dropped vs an earlier epoch).
	e1, _, err := a.RewardsKeeper.GetFinalizedEpoch(ctx, 1)
	require.NoError(t, err)
	e4, _, err := a.RewardsKeeper.GetFinalizedEpoch(ctx, 4)
	require.NoError(t, err)
	require.True(t, intStr(t, e4.MintedEmission).LT(intStr(t, e1.MintedEmission)),
		"later epoch emission must be lower than epoch 1 (halving-driven tier decay)")
	// Treasury split fired every epoch.
	require.True(t, intStr(t, e1.TreasuryAmount).IsPositive(), "treasury split must be nonzero")
	// Nonzero carry must have appeared at some boundary (non-divisible pools).
	carrySeen := false
	for e := uint64(1); e <= 4; e++ {
		epoch, _, err := a.RewardsKeeper.GetFinalizedEpoch(ctx, e)
		require.NoError(t, err)
		if intStr(t, epoch.CarryOut).IsPositive() {
			carrySeen = true
		}
	}
	require.True(t, carrySeen, "non-divisible pools must have produced a nonzero carry-forward at least once")

	// Claim-cap branch: a 3-epoch span exceeds MaxClaimEpochsPerTx=2.
	err = a.RewardsKeeper.ClaimRewards(ctx, &rewardstypes.MsgClaimRewards{
		Signer: acc(9), SlotId: 1, StartEpoch: 1, EndEpoch: 3,
	})
	require.Error(t, err, "3-epoch span must exceed the cap of 2")
	require.Contains(t, err.Error(), "claim range exceeds maximum")
	// The maximum allowed span (epochs 1..2) succeeds.
	require.NoError(t, a.RewardsKeeper.ClaimRewards(ctx, &rewardstypes.MsgClaimRewards{
		Signer: acc(9), SlotId: 1, StartEpoch: 1, EndEpoch: 2,
	}))

	// Identity still holds after claims (claims move coins module -> payout but
	// do not change cumulative, treasury, or allocated totals).
	reconcile(ctx, 4)
}
