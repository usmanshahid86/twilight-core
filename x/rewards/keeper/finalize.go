package keeper

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"

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
	params, err := k.GetParams(ctx)
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

	cumulative, err := types.ParseAmountString("cumulative emitted", state.CumulativeEmitted)
	if err != nil {
		return err
	}
	maxSupply, err := types.ParseAmountString("max supply", params.MaxSupply)
	if err != nil {
		return err
	}
	initialSubsidy, err := types.ParseAmountString("initial block subsidy", cfg.InitialBlockSubsidy)
	if err != nil {
		return err
	}
	// No emissions_enabled gate: that switch was one of three independent pause
	// authorities and is retired. Emission now follows the canonical counter, so a
	// paused epoch emits nothing because it counted no reward-enabled blocks —
	// not because a separate boolean suppressed the mint.
	emission, cumulativeAfter, err := ComputeEpochEmission(
		cumulative,
		rewardEnabledBlocks,
		maxSupply,
		initialSubsidy,
		cfg.HalvingMode,
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
	fees, err := k.GetDistributableFees(ctx, params)
	if err != nil {
		return err
	}
	treasuryAmount, err := ComputeEmissionTreasuryAmount(emission, cfg.EmissionTreasuryShareBps)
	if err != nil {
		return err
	}
	if err := k.PayTreasury(ctx, cfg.TreasuryAddress, treasuryAmount, params.NativeDenom); err != nil {
		return err
	}
	pool := emission.Add(carryIn).Add(fees).Sub(treasuryAmount)

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
	rewards, allocated, carryOut, err := AllocateUniformActiveBlocks(state.CurrentEpoch, pool, rows, snapshots)
	if err != nil {
		return err
	}

	epochRewards := make([]*types.EligibleSlotReward, 0, len(rewards))
	for i := range rewards {
		reward := rewards[i]
		if err := k.SetClaimRecord(ctx, reward); err != nil {
			return err
		}
		epochRewards = append(epochRewards, &reward)
	}
	cfgCopy := cfg
	epoch := types.EpochReward{
		EpochNumber:                 state.CurrentEpoch,
		StartHeight:                 state.CurrentEpochStartHeight,
		EndHeight:                   endHeight,
		MintedEmission:              emission.String(),
		CarryIn:                     carryIn.String(),
		DistributableFees:           fees.String(),
		TreasuryAmount:              treasuryAmount.String(),
		RewardPool:                  pool.String(),
		AllocatedAmount:             allocated.String(),
		CarryOut:                    carryOut.String(),
		DistributionMethod:          cfg.DistributionMethod,
		RemainderPolicy:             cfg.RemainderPolicy,
		Rewards:                     epochRewards,
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

	emitEpochFinalized(ctx, epoch, uint64(len(snapshots)))
	if treasuryAmount.IsPositive() {
		emitTreasuryPaid(ctx, cfg.TreasuryAddress, treasuryAmount.String())
	}
	return nil
}
