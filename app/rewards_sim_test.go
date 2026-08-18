package app_test

// Rewards economic simulation.
//
// A seeded, randomized state-machine test (deterministic property fuzzing) that
// generalizes the branch-coverage drills: each run picks randomized rewards
// params (subsidy, epoch length, treasury bps, max supply) for a
// multi-slot zero-premine chain, then drives a long random sequence of:
//
//   - advance block (crossing epoch boundaries -> mint / carry / treasury split)
//   - mid-epoch churn (suspend / reactivate a slot, floor-respecting) -> non-uniform blocks
//   - constrained releases (participant payout sets and operator remainders,
//     valid and invalid: over-release, missing obligation, paused) predicted from
//     chain state
//   - emergency pause / resume of the canonical global rewards state
//
// After every step it asserts the five rewards invariants AND the full accounting
// identity reconciled against every finalized epoch:
//
//   cumulative          == Σ MintedEmission
//   cumulative          == Σ TreasuryAmount + Σ AllocatedAmount + carry
//   supply              == cumulative                 (zero premine)
//   treasury balance    == Σ TreasuryAmount
//
// The seed is fixed per subtest so any failure reproduces deterministically.

import (
	"fmt"
	"math/rand"
	"testing"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/app"
	appparams "github.com/twilight-project/twilight-core/app/params"
	coreslotkeeper "github.com/twilight-project/twilight-core/x/coreslot/keeper"
	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	rewardskeeper "github.com/twilight-project/twilight-core/x/rewards/keeper"
	rewardstypes "github.com/twilight-project/twilight-core/x/rewards/types"
)

// rewardsCoverage accumulates, across seeds, that each claimed branch was really
// exercised — a standing guard against the sim silently regressing to vacuous.
type rewardsCoverage struct {
	releaseOk, releaseReject                             int
	overReleaseReject, missingReject, pauseReleaseReject int
	remainderPaid, remainderNoop                         int
	carry, treasury, halving                             int
}

func (c *rewardsCoverage) add(o rewardsCoverage) {
	c.releaseOk += o.releaseOk
	c.releaseReject += o.releaseReject
	c.overReleaseReject += o.overReleaseReject
	c.missingReject += o.missingReject
	c.pauseReleaseReject += o.pauseReleaseReject
	c.remainderPaid += o.remainderPaid
	c.remainderNoop += o.remainderNoop
	c.carry += o.carry
	c.treasury += o.treasury
	c.halving += o.halving
}

func TestRewardsEconomicSimulation(t *testing.T) {
	var cov rewardsCoverage
	for seed := int64(1); seed <= 8; seed++ {
		seed := seed
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			cov.add(runRewardsSim(t, seed, 220))
		})
	}
	// Across the 8 seeds, every branch the sim claims to cover must fire at least
	// once; otherwise the corresponding assurance is vacuous and should fail loudly.
	require.Positivef(t, cov.releaseOk, "no successful participant release was exercised: %+v", cov)
	require.Positivef(t, cov.releaseReject, "no rejected release was exercised: %+v", cov)
	require.Positivef(t, cov.overReleaseReject, "over-release rejection never exercised: %+v", cov)
	require.Positivef(t, cov.missingReject, "release against a missing entitlement never exercised: %+v", cov)
	require.Positivef(t, cov.pauseReleaseReject, "paused release rejection never exercised: %+v", cov)
	require.Positivef(t, cov.remainderPaid, "operator remainder never paid: %+v", cov)
	require.Positivef(t, cov.remainderNoop, "zero operator remainder never exercised: %+v", cov)
	require.Positivef(t, cov.carry, "nonzero carry-forward never produced: %+v", cov)
	require.Positivef(t, cov.treasury, "treasury split never exercised: %+v", cov)
	require.Positivef(t, cov.halving, "halving crossing never exercised: %+v", cov)
	t.Logf("rewards-sim coverage: %+v", cov)
}

func runRewardsSim(t *testing.T, seed int64, steps int) rewardsCoverage {
	var cov rewardsCoverage
	rng := rand.New(rand.NewSource(seed))
	a := bootApp(t)
	base := a.NewUncachedContext(false, cmtproto.Header{Height: 1})

	// Randomized but valid params. Even seeds use a small max supply tuned so the
	// run reliably crosses >=1 halving (maxSupply/2 == 20 block-subsidies; the
	// final drain emits well past that) and we ASSERT the decay happened at the
	// end; odd seeds run effectively uncapped (no halving). Treasury split on/off;
	// varied subsidy/epoch/cap.
	subsidy := int64(100 + rng.Intn(900)) // 100..999 (odd values exercise carry)
	// Epoch length is randomized inside the RATIFIED admission interval. Toy
	// lengths can no longer boot a chain, and a simulation that used one would be
	// exercising a configuration the protocol refuses.
	epochLen := appparams.HardMinEpochLengthBlocks + uint64(rng.Intn(4))
	cap_ := uint64(2 + rng.Intn(4)) // 2..5
	if seed <= 4 {
		cap_ = uint64(seed + 1) // seeds 1-4 deterministically cover caps 2,3,4,5
	}
	treasuryBps := uint64(0)
	treasuryAddr := ""
	if rng.Intn(2) == 0 {
		treasuryBps = uint64(500 + rng.Intn(2000)) // 5%..25%
		treasuryAddr = acc(210)
	}
	smallSupply := seed%2 == 0
	maxSupply := "21000000000000"
	if smallSupply {
		// Half of this is one epoch's emission, so the first supply threshold is
		// crossed at the epoch-1 boundary and the drain runs well past it.
		maxSupply = fmt.Sprintf("%d", subsidy*int64(epochLen)*2)
	}
	rp, snap := rewardsParams(t, func(p *rewardstypes.Params) {
		p.InitialBlockSubsidy = fmt.Sprintf("%d", subsidy)
		p.EpochLengthBlocks = epochLen
		p.MaxClaimEpochsPerTx = cap_
		p.MaxSupply = maxSupply
		p.EmissionTreasuryShareBps = treasuryBps
		p.TreasuryAddress = treasuryAddr
	})

	// 3 active slots.
	specs := []slotSpec{
		{id: 1, operator: acc(2), payout: acc(12), keyMarker: 1},
		{id: 2, operator: acc(3), payout: acc(13), keyMarker: 2},
		{id: 3, operator: acc(4), payout: acc(14), keyMarker: 3},
	}
	initCoreSlotsAndRewards(t, a, base, specs, genesisState(rp, snap))

	msgSrv := coreslotkeeper.NewMsgServer(a.CoreSlotKeeper)
	auth := app.AuthorityAddress()
	emer := app.EmergencyAuthorityAddress()
	slotStatus := map[uint64]coreslottypes.SlotStatus{
		1: coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE,
		2: coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE,
		3: coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE,
	}
	activeNow := func() int {
		n := 0
		for _, s := range slotStatus {
			if s == coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE {
				n++
			}
		}
		return n
	}

	height := int64(1)
	ctx := base.WithBlockHeight(height)

	reconcile := func(ctx sdk.Context) {
		state, err := a.RewardsKeeper.GetState(ctx)
		require.NoError(t, err)
		sumEmission, sumTreasury, sumAllocated := math.ZeroInt(), math.ZeroInt(), math.ZeroInt()
		// Walk the finalized records themselves rather than deriving the range from
		// CurrentEpoch. The epoch counter advances at BeginBlock, so an epoch
		// finalized at its boundary is still "current" until the next block opens;
		// a range of e < CurrentEpoch would silently omit it and under-count the
		// emission the conservation identity is checking.
		for e := uint64(1); e <= state.CurrentEpoch; e++ {
			epoch, found, err := a.RewardsKeeper.GetFinalizedEpoch(ctx, e)
			require.NoError(t, err)
			if !found {
				require.Equalf(t, state.CurrentEpoch, e,
					"seed %d: only the open epoch may be unfinalized", seed)
				continue
			}
			sumEmission = sumEmission.Add(intStr(t, epoch.MintedEmission))
			sumTreasury = sumTreasury.Add(intStr(t, epoch.TreasuryAmount))
			sumAllocated = sumAllocated.Add(intStr(t, epoch.AllocatedAmount))
		}
		carry := intStr(t, state.CarryForwardRemainder)
		require.Equalf(t, sumEmission.String(), state.CumulativeEmitted, "seed %d: cumulative == Σ minted", seed)
		require.Equalf(t, sumEmission.String(), sumTreasury.Add(sumAllocated).Add(carry).String(),
			"seed %d: cumulative == Σtreasury + Σallocated + carry", seed)
		require.Equalf(t, state.CumulativeEmitted, a.BankKeeper.GetSupply(ctx, app.BaseDenom).Amount.String(),
			"seed %d: supply == cumulative (zero premine)", seed)
		if treasuryAddr != "" {
			require.Equalf(t, sumTreasury.String(), a.BankKeeper.GetBalance(ctx, mustAddr(t, treasuryAddr), app.BaseDenom).Amount.String(),
				"seed %d: treasury balance == Σ treasury", seed)
		}
		assertInvariants(t, a, ctx)
	}

	reconcile(ctx)

	// Sized so a handful of advance steps closes an epoch, which is what makes
	// several epochs finalize within the step budget.
	advanceChunk := int64(epochLen) / 4
	for step := 0; step < steps; step++ {
		switch rng.Intn(6) {
		case 0, 1, 2: // advance the chain (the dominant op)
			// A CHUNK of blocks, not one. Epochs are now hundreds of blocks long,
			// so advancing singly would never close one and the release and
			// over-release branches below would never become reachable — the simulation
			// would still pass while exercising almost nothing.
			for i := int64(0); i < advanceChunk; i++ {
				driveBlock(t, a, base, height) // side effect only; ctx is refreshed below
				height++
			}
			ctx = base.WithBlockHeight(height) // subsequent ops act at the new current block height

		case 3: // churn: suspend an active slot or reactivate a suspended one (floor-respecting).
			// Pick the smallest eligible slot id deterministically (no map-order dependence).
			if rng.Intn(2) == 0 {
				if id := smallestSlotWithStatus(slotStatus, coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE); id != 0 && activeNow() > 1 {
					_, err := msgSrv.SuspendCoreSlot(ctx, &coreslottypes.MsgSuspendCoreSlot{Authority: emer, SlotId: id, Reason: "sim"})
					require.NoErrorf(t, err, "seed %d step %d: suspend", seed, step)
					slotStatus[id] = coreslottypes.SlotStatus_SLOT_STATUS_SUSPENDED
				}
			} else {
				if id := smallestSlotWithStatus(slotStatus, coreslottypes.SlotStatus_SLOT_STATUS_SUSPENDED); id != 0 {
					_, err := msgSrv.ActivateCoreSlot(ctx, &coreslottypes.MsgActivateCoreSlot{Authority: auth, SlotId: id})
					require.NoErrorf(t, err, "seed %d step %d: reactivate", seed, step)
					slotStatus[id] = coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE
				}
			}

		case 4: // release: predict validity from chain state, then assert
			simRelease(t, a, ctx, rng, &cov, seed, step)

		case 5: // emergency pause or resume a random subset of flags
			pause := rng.Intn(2) == 0
			msgErr := pauseResume(t, a, ctx, emer, pause, rng)
			require.NoErrorf(t, msgErr, "seed %d step %d: pause/resume", seed, step)
		}
		reconcile(ctx) // invariants + identity after EVERY operation, not just block advances
	}

	// Resume all flags for a clean final drain, then drive enough blocks that a
	// small-supply run is guaranteed to cross the maxSupply/2 halving threshold.
	_, _ = rewardskeeper.NewMsgServer(a.RewardsKeeper).ResumeRewards(ctx, &rewardstypes.MsgResumeRewards{
		EmergencyAuthority: emer, ResumeEmissions: true, ResumeEpochSettlement: true, ResumeClaims: true,
	})
	drain := int(epochLen)*2 + 2
	if smallSupply {
		// Three full epochs: past the maxSupply cap, so the halving is crossed.
		drain = int(epochLen) * 3
	}
	for i := 0; i < drain; i++ {
		ctx = driveBlock(t, a, base, height)
		height++
	}
	reconcile(ctx)

	// On small-supply seeds the run must have crossed a halving, i.e. the per-epoch
	// subsidy decayed. Assert >=2 distinct nonzero minted emissions across finalized
	// epochs (a single subsidy would yield exactly one value). This guarantees the
	// invariants/identity ARE checked across halving boundaries — it does NOT verify
	// the exact halving math (that a single subsidy halves by exactly 2); the exact
	// decay schedule is verified by TestDrillHalvingSubsidyDecay and the emission
	// unit tests. Here the point is that the accounting identity survives the crossing.
	state, err := a.RewardsKeeper.GetState(ctx)
	require.NoError(t, err)
	distinct := map[string]bool{}
	sumTreasury := math.ZeroInt()
	for e := uint64(1); e < state.CurrentEpoch; e++ {
		ep, found, err := a.RewardsKeeper.GetFinalizedEpoch(ctx, e)
		require.NoError(t, err)
		require.True(t, found)
		if ep.MintedEmission != "0" {
			distinct[ep.MintedEmission] = true
		}
		if intStr(t, ep.CarryOut).IsPositive() {
			cov.carry++
		}
		sumTreasury = sumTreasury.Add(intStr(t, ep.TreasuryAmount))
	}
	if treasuryAddr != "" && sumTreasury.IsPositive() {
		cov.treasury++
	}
	if smallSupply {
		require.GreaterOrEqualf(t, len(distinct), 2,
			"seed %d (maxSupply %s): run must cross a halving (>=2 distinct nonzero emissions); got %v", seed, maxSupply, distinct)
		cov.halving++
	}
	return cov
}

// smallestSlotWithStatus returns the lowest slot id with the given status, or 0
// if none — deterministic, avoiding Go map-iteration order.
func smallestSlotWithStatus(m map[uint64]coreslottypes.SlotStatus, st coreslottypes.SlotStatus) uint64 {
	var found uint64
	for id, s := range m {
		if s == st && (found == 0 || id < found) {
			found = id
		}
	}
	return found
}

// simRelease exercises the constrained release boundary against a random
// obligation and asserts the outcome matches what chain state implies.
//
// The oracle is derived from the chain, not from a parallel model: the
// entitlement is read each time, so accumulated releases from earlier steps are
// accounted for without the simulation having to track them. What it predicts is
// only the FIRST reason the boundary would refuse, in the order the boundary
// applies them (paused -> no obligation -> over the ceiling).
func simRelease(
	t *testing.T, a *app.App, ctx sdk.Context, rng *rand.Rand,
	cov *rewardsCoverage, seed int64, step int,
) {
	t.Helper()
	state, err := a.RewardsKeeper.GetState(ctx)
	require.NoError(t, err)
	if state.CurrentEpoch <= 1 {
		return // nothing finalized yet
	}
	slot := uint64(1 + rng.Intn(3))
	epoch := uint64(1 + rng.Intn(int(state.CurrentEpoch-1)))

	entitlement, found, err := a.RewardsKeeper.GetSlotEntitlement(ctx, slot, epoch)
	require.NoError(t, err)

	paused := !releaseEnabled(t, a, ctx)
	modAddr := a.AccountKeeper.GetModuleAddress(rewardstypes.ModuleName)
	preMod := a.BankKeeper.GetBalance(ctx, modAddr, app.BaseDenom).Amount.String()
	preSupply := a.BankKeeper.GetSupply(ctx, app.BaseDenom).Amount.String()
	preLiability, err := a.RewardsKeeper.GetOutstandingEntitlementLiability(ctx)
	require.NoError(t, err)

	// Half the steps take the operator remainder, half a participant payout set.
	if rng.Intn(2) == 0 {
		simRemainder(t, a, ctx, slot, epoch, entitlement, found, paused, cov, seed, step,
			preMod, preSupply, preLiability)
		return
	}

	var remaining math.Int
	if found {
		remaining, err = entitlement.Remaining()
		require.NoError(t, err)
	}
	// Deliberately allowed to exceed the remainder, so the ceiling is exercised.
	amount := math.NewInt(int64(1 + rng.Intn(400)))

	reason := ""
	switch {
	case paused:
		reason = "rewards release is paused"
	case !found:
		reason = "no entitlement exists"
	case amount.GT(remaining):
		reason = "above the entitlement of"
	}

	err = a.RewardsKeeper.PayEntitlement(ctx, slot, epoch, []rewardstypes.EntitlementPayout{
		{Recipient: acc(200), Amount: amount.String()},
	})
	if reason == "" {
		require.NoErrorf(t, err, "seed %d step %d: predicted-valid release slot %d epoch %d must succeed",
			seed, step, slot, epoch)
		cov.releaseOk++

		after, ok, gerr := a.RewardsKeeper.GetSlotEntitlement(ctx, slot, epoch)
		require.NoError(t, gerr)
		require.True(t, ok)
		before, perr := entitlement.Released()
		require.NoError(t, perr)
		got, perr := after.Released()
		require.NoError(t, perr)
		require.Equalf(t, amount.String(), got.Sub(before).String(),
			"seed %d step %d: released amount must rise by exactly the payout", seed, step)

		postLiability, lerr := a.RewardsKeeper.GetOutstandingEntitlementLiability(ctx)
		require.NoError(t, lerr)
		require.Equalf(t, amount.String(), preLiability.Sub(postLiability).String(),
			"seed %d step %d: liability must fall by exactly the payout", seed, step)
		require.Equalf(t, preSupply, a.BankKeeper.GetSupply(ctx, app.BaseDenom).Amount.String(),
			"seed %d step %d: a release moves value, it does not mint", seed, step)
		return
	}

	require.Errorf(t, err, "seed %d step %d: predicted-invalid release slot %d epoch %d must fail (%s)",
		seed, step, slot, epoch, reason)
	require.Containsf(t, err.Error(), reason,
		"seed %d step %d: release rejected for a different reason than predicted (%s)", seed, step, reason)
	requireReleaseChangedNothing(t, a, ctx, preMod, preSupply, preLiability, seed, step)

	cov.releaseReject++
	switch reason {
	case "above the entitlement of":
		cov.overReleaseReject++
	case "no entitlement exists":
		cov.missingReject++
	case "rewards release is paused":
		cov.pauseReleaseReject++
	}
}

// simRemainder exercises the operator-remainder helper, whose contract differs
// from a participant payout in one important way: a remainder of zero is a
// deterministic no-op that SUCCEEDS rather than an error.
func simRemainder(
	t *testing.T, a *app.App, ctx sdk.Context, slot, epoch uint64,
	entitlement rewardstypes.SlotEntitlement, found, paused bool,
	cov *rewardsCoverage, seed int64, step int,
	preMod, preSupply string, preLiability math.Int,
) {
	t.Helper()
	err := a.RewardsKeeper.PayEntitlementRemainderToOperator(ctx, slot, epoch)

	switch {
	case paused:
		require.Errorf(t, err, "seed %d step %d: a paused chain must refuse the remainder", seed, step)
		require.Contains(t, err.Error(), "rewards release is paused")
		requireReleaseChangedNothing(t, a, ctx, preMod, preSupply, preLiability, seed, step)
		cov.releaseReject++
		cov.pauseReleaseReject++
		return
	case !found:
		require.Errorf(t, err, "seed %d step %d: no obligation exists to pay a remainder against", seed, step)
		require.Contains(t, err.Error(), "no entitlement exists")
		requireReleaseChangedNothing(t, a, ctx, preMod, preSupply, preLiability, seed, step)
		cov.releaseReject++
		cov.missingReject++
		return
	}

	require.NoErrorf(t, err, "seed %d step %d: the remainder helper must succeed", seed, step)
	remaining, perr := entitlement.Remaining()
	require.NoError(t, perr)

	after, ok, gerr := a.RewardsKeeper.GetSlotEntitlement(ctx, slot, epoch)
	require.NoError(t, gerr)
	require.True(t, ok)
	require.Equalf(t, after.EntitlementAmount, after.ReleasedAmount,
		"seed %d step %d: a paid remainder leaves the obligation fully released", seed, step)

	if remaining.IsZero() {
		// The deterministic no-op: nothing moved, and nothing was sent.
		requireReleaseChangedNothing(t, a, ctx, preMod, preSupply, preLiability, seed, step)
		cov.remainderNoop++
		return
	}
	postLiability, lerr := a.RewardsKeeper.GetOutstandingEntitlementLiability(ctx)
	require.NoError(t, lerr)
	require.Equalf(t, remaining.String(), preLiability.Sub(postLiability).String(),
		"seed %d step %d: liability must fall by exactly the remainder", seed, step)
	cov.remainderPaid++
}

// requireReleaseChangedNothing is the atomicity assertion every rejected release
// shares: no escrow movement, no supply movement, no accounting movement.
func requireReleaseChangedNothing(
	t *testing.T, a *app.App, ctx sdk.Context,
	preMod, preSupply string, preLiability math.Int, seed int64, step int,
) {
	t.Helper()
	modAddr := a.AccountKeeper.GetModuleAddress(rewardstypes.ModuleName)
	require.Equalf(t, preMod, a.BankKeeper.GetBalance(ctx, modAddr, app.BaseDenom).Amount.String(),
		"seed %d step %d: a refused release changed the module balance", seed, step)
	require.Equalf(t, preSupply, a.BankKeeper.GetSupply(ctx, app.BaseDenom).Amount.String(),
		"seed %d step %d: a refused release changed supply", seed, step)
	postLiability, err := a.RewardsKeeper.GetOutstandingEntitlementLiability(ctx)
	require.NoError(t, err)
	require.Equalf(t, preLiability.String(), postLiability.String(),
		"seed %d step %d: a refused release changed the outstanding liability", seed, step)
}

// pauseResume drives the canonical global pause through the emergency authority.
//
// The message schedules the value for H+1. This simulation does not step a block
// between the transaction and the steps that observe it, so it then settles the
// transition directly: the simulation is about what a paused or resumed chain
// does, and the H+1 timing itself is pinned by dedicated keeper and app tests
// rather than sampled randomly here.
//
// The deprecated per-area selectors on the messages are left at their zero values
// deliberately — a pause is global, and passing them would suggest otherwise.
func pauseResume(t *testing.T, a *app.App, ctx sdk.Context, emer string, pause bool, _ *rand.Rand) error {
	t.Helper()
	srv := rewardskeeper.NewMsgServer(a.RewardsKeeper)
	if pause {
		if _, err := srv.PauseRewards(ctx, &rewardstypes.MsgPauseRewards{EmergencyAuthority: emer}); err != nil {
			return err
		}
	} else {
		if _, err := srv.ResumeRewards(ctx, &rewardstypes.MsgResumeRewards{EmergencyAuthority: emer}); err != nil {
			return err
		}
	}
	return a.RewardsKeeper.SetPauseState(ctx, rewardstypes.RewardsPauseState{CurrentPaused: pause})
}

// releaseEnabled reports the canonical release state, which is what governs
// whether entitlement value may move. The retired claims_enabled param carries no
// authority and must not be used to predict a rejection.
func releaseEnabled(t *testing.T, a *app.App, ctx sdk.Context) bool {
	t.Helper()
	enabled, err := a.RewardsKeeper.SettlementReleaseEnabled(ctx)
	require.NoError(t, err)
	return enabled
}
