package keeper

import (
	"context"
	"sort"
	"strconv"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/x/rewards/types"
)

func (k Keeper) ClaimRewards(ctx context.Context, msg *types.MsgClaimRewards) error {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	if err := k.claimRewards(sdk.WrapSDKContext(cacheCtx), msg); err != nil {
		return err
	}
	write()
	return nil
}

func (k Keeper) claimRewards(ctx context.Context, msg *types.MsgClaimRewards) error {
	if msg == nil || msg.SlotId == 0 || msg.StartEpoch == 0 || msg.EndEpoch < msg.StartEpoch {
		return types.ErrInvalidState.Wrap("invalid claim range")
	}
	// The signer is a CONTROL identity, not a value destination: rewards are sent
	// to each record's stored payout address regardless of who submits the claim.
	// It therefore keeps syntax-only validation. Applying the economic rule here
	// would refuse a perfectly legitimate claim submitted on an operator's behalf
	// by a relayer whose own address happened to be blocked, without protecting
	// anything — no value reaches the signer.
	if _, err := sdk.AccAddressFromBech32(msg.Signer); err != nil {
		return types.ErrInvalidState.Wrapf("claim signer: %v", err)
	}
	params, err := k.GetParams(ctx)
	if err != nil {
		return err
	}
	if !params.ClaimsEnabled {
		return types.ErrUnsupportedFeature.Wrap("claims are disabled")
	}
	rangeDelta := msg.EndEpoch - msg.StartEpoch
	if rangeDelta >= params.MaxClaimEpochsPerTx {
		return types.ErrInvalidState.Wrap("claim range exceeds maximum")
	}

	records := make([]types.EligibleSlotReward, 0, rangeDelta+1)
	groups := make(map[string]math.Int)
	total := math.ZeroInt()
	for epoch := msg.StartEpoch; epoch <= msg.EndEpoch; epoch++ {
		if _, found, err := k.GetFinalizedEpoch(ctx, epoch); err != nil {
			return err
		} else if !found {
			return types.ErrInvalidState.Wrapf("epoch %d is not finalized", epoch)
		}
		record, found, err := k.GetClaimRecord(ctx, msg.SlotId, epoch)
		if err != nil {
			return err
		}
		if !found {
			return types.ErrInvalidState.Wrapf("claim record missing for slot %d epoch %d", msg.SlotId, epoch)
		}
		if record.SlotId != msg.SlotId || record.EpochNumber != epoch {
			return types.ErrInvalidState.Wrapf("claim record identity mismatch for slot %d epoch %d", msg.SlotId, epoch)
		}
		if record.Claimed {
			return types.ErrInvalidState.Wrapf("reward already claimed for epoch %d", epoch)
		}
		amount, err := types.ParseAmountString("claim amount", record.Amount)
		if err != nil || !amount.IsPositive() {
			return types.ErrInvalidState.Wrapf("claim amount must be positive for epoch %d", epoch)
		}
		// Defensive revalidation immediately before money moves. The record was
		// admitted canonically when written, but this is the last point at which a
		// bad destination can still be refused.
		if _, err := k.economicAddresses.Validate(record.PayoutAddress); err != nil {
			return types.ErrInvalidAddress.Wrapf("claim payout address: %v", err)
		}
		groupAmount, found := groups[record.PayoutAddress]
		if !found {
			groupAmount = math.ZeroInt()
		}
		groups[record.PayoutAddress] = groupAmount.Add(amount)
		total = total.Add(amount)
		records = append(records, record)
		if epoch == ^uint64(0) {
			break
		}
	}

	moduleAddress := k.accountKeeper.GetModuleAddress(types.ModuleName)
	if moduleAddress == nil {
		return types.ErrInvalidState.Wrap("rewards module account is missing")
	}
	if k.bankKeeper.GetBalance(ctx, moduleAddress, params.NativeDenom).Amount.LT(total) {
		return types.ErrInvalidState.Wrap("insufficient rewards module balance")
	}
	payouts := make([]string, 0, len(groups))
	for payout := range groups {
		payouts = append(payouts, payout)
	}
	sort.Strings(payouts)
	for _, payout := range payouts {
		// Reuse the canonical parse instead of a second, error-ignoring one. The
		// discarded error in the previous form was the real hazard: a payout that
		// failed to parse became the zero address and the transfer proceeded.
		address, err := k.economicAddresses.Validate(payout)
		if err != nil {
			return types.ErrInvalidAddress.Wrapf("claim payout address: %v", err)
		}
		if err := k.bankKeeper.SendCoinsFromModuleToAccount(
			ctx,
			types.ModuleName,
			address,
			sdk.NewCoins(sdk.NewCoin(params.NativeDenom, groups[payout])),
		); err != nil {
			return err
		}
	}
	height := sdk.UnwrapSDKContext(ctx).BlockHeight()
	for _, record := range records {
		record.Claimed = true
		record.ClaimedAtHeight = height
		if err := k.SetClaimRecord(ctx, record); err != nil {
			return err
		}
	}
	emitRewardsEvent(ctx, types.EventTypeClaimRewards,
		sdk.NewAttribute(types.AttributeKeySigner, msg.Signer),
		sdk.NewAttribute(types.AttributeKeySlotID, strconv.FormatUint(msg.SlotId, 10)),
		sdk.NewAttribute(types.AttributeKeyStartEpoch, strconv.FormatUint(msg.StartEpoch, 10)),
		sdk.NewAttribute(types.AttributeKeyEndEpoch, strconv.FormatUint(msg.EndEpoch, 10)),
		sdk.NewAttribute(types.AttributeKeyAmount, total.String()),
		sdk.NewAttribute(types.AttributeKeyPayoutCount, strconv.Itoa(len(payouts))),
	)
	return nil
}
