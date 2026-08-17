package keeper

import (
	"context"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/internal/checked"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

func (k Keeper) FinalizeEpoch(ctx context.Context) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	if err := k.finalizeEpoch(sdk.WrapSDKContext(cacheCtx)); err != nil {
		return err
	}
	write()
	return nil
}

// validateEpochParticipation checks the counters that drive emission and
// allocation against the geometry of the epoch they claim to describe.
//
// Two relations, both arithmetically impossible to violate through the block
// path, and therefore both meaningful as corruption detectors:
//
//   - an epoch cannot have counted more reward-enabled blocks than it has blocks.
//     BeginBlock increments the counter at most once per block of the open epoch,
//     so a count above the canonical length means either the counter was not reset
//     when the epoch opened or the length it is being measured against is not the
//     one the epoch ran under. Either way the multiplier feeding the mint is wrong.
//   - a slot cannot have been reward-active in more blocks than were
//     reward-enabled. Participation is credited only on blocks that also advance
//     the counter, so a row above it would claim a share of a pool computed from
//     fewer blocks than the row itself asserts.
//
// Zero rows is not an error: a fully paused epoch, or one with no ACTIVE slots,
// legitimately closes with none.
func (k Keeper) validateEpochParticipation(
	ctx context.Context, epoch, rewardEnabledBlocks uint64, rows []types.SlotActiveBlocks,
) error {
	length, err := k.EpochLengthForEpoch(ctx, epoch)
	if err != nil {
		return err
	}
	if rewardEnabledBlocks > length {
		return types.ErrInvalidState.Wrapf(
			"epoch %d counted %d reward-enabled blocks but is only %d blocks long",
			epoch, rewardEnabledBlocks, length)
	}
	for _, row := range rows {
		if row.SlotId == 0 {
			return types.ErrInvalidState.Wrapf(
				"epoch %d carries a participation row for slot 0", epoch)
		}
		if row.BlocksActive > rewardEnabledBlocks {
			return types.ErrInvalidState.Wrapf(
				"slot %d is recorded active for %d blocks of epoch %d, which counted %d reward-enabled blocks",
				row.SlotId, row.BlocksActive, epoch, rewardEnabledBlocks)
		}
	}
	return nil
}

func (k Keeper) finalizeEpoch(ctx context.Context) error {
	// The genesis-fixed monetary configuration this transition will act on, read
	// through the boundary that refuses a corrupted denom, cap, or schedule rather
	// than minting under it.
	params, err := k.MonetaryParams(ctx)
	if err != nil {
		return err
	}
	state, err := k.GetState(ctx)
	if err != nil {
		return err
	}
	// The epoch about to be closed must be anchored where the canonical history
	// puts it. The stored start height is written verbatim into the permanent
	// EpochReward record below, so a disagreement here would archive a span that
	// never existed alongside a real mint.
	if err := k.verifyCurrentEpochAnchor(ctx, state); err != nil {
		return err
	}
	cfg, err := k.GetCurrentEpochConfig(ctx)
	if err != nil {
		return err
	}
	if cfg.WeightedRewardsEnabled ||
		cfg.DistributionMethod != types.DistributionMethod_DISTRIBUTION_METHOD_UNIFORM_ACTIVE_BLOCKS {
		return types.ErrUnsupportedFeature.Wrap("only uniform active-block distribution is supported in v1")
	}
	if cfg.FeeCollectionEnabled || cfg.FeeDistributionEnabled ||
		cfg.FeeDistributionMode != types.FeeDistributionMode_FEE_DISTRIBUTION_MODE_NONE {
		return types.ErrUnsupportedFeature.Wrap("fee collection and distribution are disabled in v1")
	}
	// Canonical geometry. The deprecated snapshot mirror is never consulted here.
	endHeight, err := k.EpochEndHeight(ctx, state.CurrentEpoch)
	if err != nil {
		return err
	}

	// The block-count input to emission is the epoch's reward-enabled blocks, not
	// its configured length (§30). The two coincide only when the epoch was never
	// paused; a paused epoch counted fewer, and a fully paused epoch counted none.
	rewardEnabledBlocks, err := k.GetOpenRewardEnabledBlocks(ctx)
	if err != nil {
		return err
	}
	// Participation state is validated HERE, before the first monetary operation,
	// not where it happens to be consumed.
	//
	// These counters stopped being bookkeeping the moment emission started
	// following them: reward_enabled_blocks multiplies the block subsidy, and each
	// slot's active-block count is its share of the pool. Validating them after the
	// mint would still roll back — the whole finalization runs in a cache — but it
	// would mean the mint and the treasury send were computed from state already
	// known to be impossible, and it would put the check behind whichever
	// operation happened to fail first.
	rows, err := k.IterateActiveBlocksForEpoch(ctx, state.CurrentEpoch)
	if err != nil {
		return err
	}
	if err := k.validateEpochParticipation(ctx, state.CurrentEpoch, rewardEnabledBlocks, rows); err != nil {
		return err
	}

	// The canonical economics for THIS target (§33, §33.1). Resolved before any
	// monetary step, and never read from the deprecated snapshot: that mirror is
	// pinned at genesis and cannot express a configuration that changed since.
	rewardConfig, err := k.RewardConfigForTarget(ctx, state.CurrentEpoch)
	if err != nil {
		return err
	}

	cumulative, err := types.ParseAmountString("cumulative emitted", state.CumulativeEmitted)
	if err != nil {
		return err
	}
	maxSupply, err := types.ParseAmountString("max supply", params.MaxSupply)
	if err != nil {
		return err
	}
	initialSubsidy, err := types.ParseAmountString("initial block subsidy", rewardConfig.InitialBlockSubsidy)
	if err != nil {
		return err
	}
	// No emissions_enabled gate: that switch was one of three independent pause
	// authorities and is retired. Emission now follows the canonical counter, so a
	// paused epoch emits nothing because it counted no reward-enabled blocks —
	// not because a separate boolean suppressed the mint.
	// The halving mode is genesis-fixed protocol configuration (§33), not part of
	// the versioned reward configuration, so it comes from Params.
	emission, cumulativeAfter, err := ComputeEpochEmission(
		cumulative,
		rewardEnabledBlocks,
		maxSupply,
		initialSubsidy,
		params.HalvingMode,
	)
	if err != nil {
		return err
	}
	if cumulativeAfter.GT(maxSupply) {
		return types.ErrInvalidState.Wrap("cumulative emitted plus epoch emission exceeds max supply")
	}
	if emission.IsPositive() {
		if err := k.bankKeeper.MintCoins(
			ctx,
			types.ModuleName,
			sdk.NewCoins(sdk.NewCoin(params.NativeDenom, emission)),
		); err != nil {
			return err
		}
	}

	carryIn, err := types.ParseAmountString("carry forward remainder", state.CarryForwardRemainder)
	if err != nil {
		return err
	}
	treasuryAmount, err := ComputeEmissionTreasuryAmount(emission, rewardConfig.EmissionTreasuryShareBps)
	if err != nil {
		return err
	}
	// Zero-guarded inside PayTreasury: at zero there is no send and no
	// transfer-time revalidation of the destination (§33.2).
	if err := k.PayTreasury(ctx, rewardConfig.TreasuryAddress, treasuryAmount, params.NativeDenom); err != nil {
		return err
	}
	// The canonical V2 pool has no fee term. Fee distribution is disabled and
	// distributable_fees_E is zero by definition (§33), so the term is removed
	// rather than added as a zero — an input that is always zero is a place for a
	// future edit to reintroduce a value the architecture excludes.
	pool, err := ComputeRewardPoolV2(emission, treasuryAmount, carryIn)
	if err != nil {
		return err
	}

	// The payout snapshot is taken here: end of the closing block, after that
	// block's transactions have executed (§32). A payout address changed earlier in
	// this same block is therefore eligible to be snapshotted for this epoch.
	snapshots := make(map[uint64]SlotRewardSnapshot, len(rows))
	for _, row := range rows {
		if row.BlocksActive == 0 {
			continue
		}
		snapshot, err := k.GetSlotRewardSnapshot(ctx, row.SlotId)
		if err != nil {
			return err
		}
		snapshots[row.SlotId] = snapshot
	}
	height, err := checked.Uint64FromInt64(sdk.UnwrapSDKContext(ctx).BlockHeight())
	if err != nil {
		return types.ErrInvalidState.Wrapf("block height is not representable: %v", err)
	}
	entitlements, allocated, carryOut, nPos, err := AllocateSlotEntitlements(
		state.CurrentEpoch, pool, rows, snapshots, rewardConfig.Version, height)
	if err != nil {
		return err
	}
	if err := assertAllocationConservation(state.CurrentEpoch, pool, allocated, carryOut, nPos); err != nil {
		return err
	}

	// Authoritative obligation creation. Each write also raises the outstanding
	// liability by exactly its amount, so the two cannot commit apart.
	//
	// The already-resolved rewardConfig is passed in rather than looked up per row.
	// The governing configuration is a property of the EPOCH, so resolving it once
	// is not an optimization of a repeated lookup — it is the same lookup, and
	// repeating it would put a history seek that grows with the number of accepted
	// configuration changes inside a loop over participants. Materialization is
	// O(participants), independent of chain age.
	for _, entitlement := range entitlements {
		if err := k.createSlotEntitlement(ctx, entitlement, rewardConfig); err != nil {
			return err
		}
	}

	cfgCopy := cfg
	epoch := types.EpochReward{
		EpochNumber:    state.CurrentEpoch,
		StartHeight:    state.CurrentEpochStartHeight,
		EndHeight:      endHeight,
		MintedEmission: emission.String(),
		CarryIn:        carryIn.String(),
		// Permanently zero in V2: there is no fee input to the reward pool.
		DistributableFees:  "0",
		TreasuryAmount:     treasuryAmount.String(),
		RewardPool:         pool.String(),
		AllocatedAmount:    allocated.String(),
		CarryOut:           carryOut.String(),
		DistributionMethod: cfg.DistributionMethod,
		RemainderPolicy:    cfg.RemainderPolicy,
		// Rewards is deliberately left empty. The canonical per-Slot record is now
		// SlotEntitlement; populating this too would archive a second immutable copy
		// of the same obligation, free to drift from the one that can actually be
		// paid. The field stays on the wire and its number is never recycled.
		Rewards:                     nil,
		CumulativeEmittedAfterEpoch: cumulativeAfter.String(),
		Config:                      &cfgCopy,
		RewardEnabledBlocks:         rewardEnabledBlocks,
	}
	if err := k.SetFinalizedEpoch(ctx, epoch); err != nil {
		return err
	}
	if err := k.DeleteActiveBlocksForEpoch(ctx, state.CurrentEpoch); err != nil {
		return err
	}

	nextParams := params
	if pending, found, err := k.GetPendingParams(ctx); err != nil {
		return err
	} else if found {
		pending.EmissionsEnabled = params.EmissionsEnabled
		pending.EpochSettlementEnabled = params.EpochSettlementEnabled
		pending.ClaimsEnabled = params.ClaimsEnabled
		if err := k.SetParams(ctx, pending); err != nil {
			return err
		}
		if err := k.ClearPendingParams(ctx); err != nil {
			return err
		}
		nextParams = pending
		emitRewardsEvent(ctx, types.EventTypeParamsActivated)
	}
	nextCfg, err := k.buildEpochConfigSnapshot(ctx, nextParams)
	if err != nil {
		return err
	}
	if err := k.SetCurrentEpochConfig(ctx, nextCfg); err != nil {
		return err
	}
	// The epoch counter is NOT advanced here. It advances at the first BeginBlock
	// of the next epoch, which is also where a scheduled configuration is consumed
	// (§11, §95). Advancing it here would make every scheduled epoch-length change
	// unreachable, because the schedule is keyed to the epoch being opened.
	state.CumulativeEmitted = cumulativeAfter.String()
	state.CarryForwardRemainder = carryOut.String()
	if err := k.SetState(ctx, state); err != nil {
		return err
	}

	// Solvency, asserted after every transfer this epoch performs (§33). It is the
	// one check that spans the whole transition rather than any single step, and it
	// is what catches an obligation created without a matching liability, or a
	// carry that does not account for what stayed behind.
	if err := k.assertEscrowSolvency(ctx, params.NativeDenom, carryOut); err != nil {
		return err
	}

	emitEpochFinalized(ctx, epoch, uint64(len(snapshots)))
	if treasuryAmount.IsPositive() {
		emitTreasuryPaid(ctx, rewardConfig.TreasuryAddress, treasuryAmount.String())
	}
	return nil
}

// assertAllocationConservation applies the independent checks §31 requires before
// carry is treated as final.
//
// The residue bound is the substantive one. allocated <= pool and carry >= 0 both
// follow from the arithmetic and would survive most defects; carry <= n_pos - 1
// does not. It follows from flooring exactly once per participating Slot, so a
// wrong denominator, a double-counted Slot, or an iteration that visited a Slot
// twice all break it while still producing a superficially plausible split.
func assertAllocationConservation(epoch uint64, pool, allocated, carryOut sdkmath.Int, nPos uint64) error {
	if allocated.IsNegative() {
		return types.ErrInvalidState.Wrapf("epoch %d allocated a negative amount %s", epoch, allocated)
	}
	if allocated.GT(pool) {
		return types.ErrInvalidState.Wrapf(
			"epoch %d allocated %s from a pool of %s", epoch, allocated, pool)
	}
	if carryOut.IsNegative() {
		return types.ErrInvalidState.Wrapf("epoch %d carries a negative remainder %s", epoch, carryOut)
	}
	if nPos == 0 {
		return nil
	}
	bound := sdkmath.NewIntFromUint64(nPos).SubRaw(1)
	if carryOut.GT(bound) {
		return types.ErrInvalidState.Wrapf(
			"epoch %d carries %s across %d participating slots, above the residue bound of %s",
			epoch, carryOut, nPos, bound)
	}
	return nil
}

// assertEscrowSolvency proves the module holds exactly what it owes.
//
//	rewards escrow balance == outstanding entitlement liability + carry
//
// Equality rather than coverage, because a surplus is as much a defect as a
// shortfall: it means value entered escrow that no obligation and no carry
// accounts for. The equality is safe to assert because the rewards module account
// is a blocked destination for ordinary transfers, so nobody can push funds in
// from outside and turn a correct chain into a halted one. That property is
// asserted at app level, where the bank configuration that provides it lives.
func (k Keeper) assertEscrowSolvency(ctx context.Context, denom string, carryOut sdkmath.Int) error {
	address := k.accountKeeper.GetModuleAddress(types.ModuleName)
	if address == nil {
		return types.ErrInvalidState.Wrap("rewards module account is missing")
	}
	liability, err := k.GetOutstandingEntitlementLiability(ctx)
	if err != nil {
		return err
	}
	owed, err := liability.SafeAdd(carryOut)
	if err != nil {
		return types.ErrInvalidState.Wrapf("outstanding liability plus carry overflows: %v", err)
	}
	balance := k.bankKeeper.GetBalance(ctx, address, denom).Amount
	if !balance.Equal(owed) {
		return types.ErrInvalidState.Wrapf(
			"rewards escrow holds %s but owes %s (liability %s + carry %s)",
			balance, owed, liability, carryOut)
	}
	return nil
}
