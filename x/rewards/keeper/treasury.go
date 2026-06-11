package keeper

import (
	"context"

	"cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/x/rewards/types"
)

func ComputeEmissionTreasuryAmount(emission math.Int, shareBps uint64) (math.Int, error) {
	if emission.IsNil() || emission.IsNegative() {
		return math.Int{}, types.ErrInvalidState.Wrap("emission must be nonnegative")
	}
	if shareBps > types.MaxBasisPoints {
		return math.Int{}, types.ErrInvalidParams.Wrap("emission treasury share exceeds basis-point maximum")
	}
	return emission.MulRaw(int64(shareBps)).QuoRaw(int64(types.MaxBasisPoints)), nil
}

func (k Keeper) PayTreasury(ctx context.Context, address string, amount math.Int, denom string) error {
	if amount.IsZero() {
		return nil
	}
	recipient, err := sdk.AccAddressFromBech32(address)
	if err != nil {
		return types.ErrInvalidParams.Wrapf("treasury address: %v", err)
	}
	return k.bankKeeper.SendCoinsFromModuleToAccount(
		ctx,
		types.ModuleName,
		recipient,
		sdk.NewCoins(sdk.NewCoin(denom, amount)),
	)
}
