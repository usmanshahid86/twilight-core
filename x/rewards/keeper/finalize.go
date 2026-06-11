package keeper

import (
	"context"

	"cosmossdk.io/math"
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

func (k Keeper) finalizeEpoch(ctx context.Context) error {
	params, err := k.GetParams(ctx)
	if err != nil {
		return err
	}
	state, err := k.GetState(ctx)
	if err != nil {
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
	endHeight, err := ConfiguredEpochEndHeight(state, cfg)
	if err != nil {
		return err
	}
	if endHeight == ^uint64(0) {
		return types.ErrInvalidState.Wrap("cannot advance epoch beyond maximum height")
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
	emission := math.ZeroInt()
	cumulativeAfter := cumulative
	if params.EmissionsEnabled {
		emission, cumulativeAfter, err = ComputeEpochEmission(
			cumulative,
			cfg.EpochLengthBlocks,
			maxSupply,
			initialSubsidy,
			cfg.HalvingMode,
		)
		if err != nil {
			return err
		}
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

	rows, err := k.IterateActiveBlocksForEpoch(ctx, state.CurrentEpoch)
	if err != nil {
		return err
	}
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
	nextCfg, err := BuildEpochConfigSnapshot(nextParams)
	if err != nil {
		return err
	}
	if err := k.SetCurrentEpochConfig(ctx, nextCfg); err != nil {
		return err
	}
	state.CurrentEpoch++
	state.CurrentEpochStartHeight = endHeight + 1
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
