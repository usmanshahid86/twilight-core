package app_test

// Rewards economic simulation.
//
// A seeded, randomized state-machine test (deterministic property fuzzing) that
// generalizes the branch-coverage drills: each run picks randomized rewards
// params (subsidy, epoch length, treasury bps, max supply, claim cap) for a
// multi-slot zero-premine chain, then drives a long random sequence of:
//
//   - advance block (crossing epoch boundaries -> mint / carry / treasury split)
//   - mid-epoch churn (suspend / reactivate a slot, floor-respecting) -> non-uniform blocks
//   - claims (valid and invalid: double-claim, over-cap, unfinalized) predicted from chain state
//   - emergency pause / resume of emission / settlement / claims
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
	coreslotkeeper "github.com/twilight-project/twilight-core/x/coreslot/keeper"
	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	rewardskeeper "github.com/twilight-project/twilight-core/x/rewards/keeper"
	rewardstypes "github.com/twilight-project/twilight-core/x/rewards/types"
)

// rewardsCoverage accumulates, across seeds, that each claimed branch was really
// exercised — a standing guard against the sim silently regressing to vacuous.
type rewardsCoverage struct {
	claimOk, claimReject, capReject, replayReject, pauseClaimReject int
	carry, treasury, halving                                        int
}

func (c *rewardsCoverage) add(o rewardsCoverage) {
	c.claimOk += o.claimOk
	c.claimReject += o.claimReject
	c.capReject += o.capReject
	c.replayReject += o.replayReject
	c.pauseClaimReject += o.pauseClaimReject
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
	require.Positivef(t, cov.claimOk, "no successful claim was exercised: %+v", cov)
	require.Positivef(t, cov.claimReject, "no rejected claim was exercised: %+v", cov)
	require.Positivef(t, cov.capReject, "claim-cap rejection never exercised: %+v", cov)
	require.Positivef(t, cov.replayReject, "double-claim (replay) rejection never exercised: %+v", cov)
	require.Positivef(t, cov.pauseClaimReject, "claims-disabled rejection never exercised: %+v", cov)
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
	epochLen := uint64(1 + rng.Intn(4))   // 1..4
	cap_ := uint64(2 + rng.Intn(4))       // 2..5
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
		maxSupply = fmt.Sprintf("%d", subsidy*40) // half == subsidy*20 -> crossed within the drain
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
		for e := uint64(1); e < state.CurrentEpoch; e++ {
			epoch, found, err := a.RewardsKeeper.GetFinalizedEpoch(ctx, e)
			require.NoError(t, err)
			require.Truef(t, found, "seed %d: epoch %d must be finalized (< currentEpoch)", seed, e)
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

	for step := 0; step < steps; step++ {
		switch rng.Intn(6) {
		case 0, 1, 2: // advance a block (the dominant op)
			ctx = driveBlock(t, a, base, height)
			height++
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

		case 4: // claim: predict validity from chain state, then assert
			simClaim(t, a, ctx, rng, cap_, &cov, seed, step)

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
		drain = 60 // > 40 block-subsidies => past maxSupply (cap) => crossed the halving
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

// simClaim attempts a claim for a random slot+range and asserts the outcome
// matches what the chain state implies (finalized? already claimed? over cap?
// claims enabled?).
func simClaim(t *testing.T, a *app.App, ctx sdk.Context, rng *rand.Rand, cap_ uint64, cov *rewardsCoverage, seed int64, step int) {
	t.Helper()
	state, err := a.RewardsKeeper.GetState(ctx)
	require.NoError(t, err)
	if state.CurrentEpoch <= 1 {
		return // nothing finalized yet
	}
	params, err := a.RewardsKeeper.GetParams(ctx)
	require.NoError(t, err)
	slot := uint64(1 + rng.Intn(3))
	start := uint64(1 + rng.Intn(int(state.CurrentEpoch-1)))
	end := start + uint64(rng.Intn(6)) // delta 0..5 so the cap boundary is reachable for caps 2..5

	// Predict the FIRST rejection reason in claims.go precedence order
	// (claims-disabled -> over-cap -> per-epoch: unfinalized -> missing -> claimed
	// -> nonpositive). Empty reason == predicted valid.
	reason := ""
	switch {
	case !params.ClaimsEnabled:
		reason = "claims are disabled"
	case end-start >= cap_:
		reason = "claim range exceeds maximum"
	default:
		for e := start; e <= end; e++ {
			_, fin, ferr := a.RewardsKeeper.GetFinalizedEpoch(ctx, e)
			require.NoError(t, ferr) // a store error here is a real regression, not "unfinalized"
			if !fin {
				reason = "is not finalized"
				break
			}
			rec, found, gerr := a.RewardsKeeper.GetClaimRecord(ctx, slot, e)
			require.NoError(t, gerr)
			switch {
			case !found:
				reason = "claim record missing"
			case rec.Claimed:
				reason = "already claimed"
			case intStr(t, rec.Amount).IsZero():
				reason = "claim amount must be positive"
			}
			if reason != "" {
				break
			}
		}
	}

	// Snapshot funds state so a rejected claim can be proven to mutate nothing (B).
	modAddr := a.AccountKeeper.GetModuleAddress(rewardstypes.ModuleName)
	preMod := a.BankKeeper.GetBalance(ctx, modAddr, app.BaseDenom).Amount.String()
	preSupply := a.BankKeeper.GetSupply(ctx, app.BaseDenom).Amount.String()

	err = a.RewardsKeeper.ClaimRewards(ctx, &rewardstypes.MsgClaimRewards{
		Signer: acc(9), SlotId: slot, StartEpoch: start, EndEpoch: end,
	})
	if reason == "" {
		require.NoErrorf(t, err, "seed %d step %d: predicted-valid claim slot %d [%d,%d] must succeed", seed, step, slot, start, end)
		cov.claimOk++
	} else {
		require.Errorf(t, err, "seed %d step %d: predicted-invalid claim slot %d [%d,%d] must fail (%s)", seed, step, slot, start, end, reason)
		require.Containsf(t, err.Error(), reason, "seed %d step %d: claim rejected for a different reason than predicted (%s)", seed, step, reason)
		// Atomic rejection: no module-balance or supply change.
		require.Equalf(t, preMod, a.BankKeeper.GetBalance(ctx, modAddr, app.BaseDenom).Amount.String(),
			"seed %d step %d: rejected claim changed module balance", seed, step)
		require.Equalf(t, preSupply, a.BankKeeper.GetSupply(ctx, app.BaseDenom).Amount.String(),
			"seed %d step %d: rejected claim changed supply", seed, step)
		cov.claimReject++
		switch reason {
		case "claim range exceeds maximum":
			cov.capReject++
		case "already claimed":
			cov.replayReject++
		case "claims are disabled":
			cov.pauseClaimReject++
		}
	}
}

// pauseResume toggles a random subset of the three runtime flags via the
// emergency authority.
func pauseResume(t *testing.T, a *app.App, ctx sdk.Context, emer string, pause bool, rng *rand.Rand) error {
	t.Helper()
	em := rng.Intn(2) == 0
	se := rng.Intn(2) == 0
	cl := rng.Intn(2) == 0
	if !em && !se && !cl {
		em = true
	}
	srv := rewardskeeper.NewMsgServer(a.RewardsKeeper)
	if pause {
		_, err := srv.PauseRewards(ctx, &rewardstypes.MsgPauseRewards{
			EmergencyAuthority: emer, PauseEmissions: em, PauseEpochSettlement: se, PauseClaims: cl,
		})
		return err
	}
	_, err := srv.ResumeRewards(ctx, &rewardstypes.MsgResumeRewards{
		EmergencyAuthority: emer, ResumeEmissions: em, ResumeEpochSettlement: se, ResumeClaims: cl,
	})
	return err
}
