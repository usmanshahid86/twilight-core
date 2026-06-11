package keeper

import (
	"fmt"
	"strings"

	"cosmossdk.io/collections"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/x/rewards/types"
)

func (k Keeper) SupplyCapInvariant() sdk.Invariant {
	return func(ctx sdk.Context) (string, bool) {
		params, err := k.GetParams(ctx)
		if err != nil {
			return invariantError("supply-cap", err)
		}
		maxSupply, err := types.ParseAmountString("max supply", params.MaxSupply)
		if err != nil {
			return invariantError("supply-cap", err)
		}
		supply := k.bankKeeper.GetSupply(ctx, params.NativeDenom).Amount
		if supply.GT(maxSupply) {
			return sdk.FormatInvariant(types.ModuleName, "supply-cap", fmt.Sprintf("%s > %s", supply, maxSupply)), true
		}
		return "", false
	}
}

func (k Keeper) CumulativeEmittedInvariant() sdk.Invariant {
	return func(ctx sdk.Context) (string, bool) {
		params, err := k.GetParams(ctx)
		if err != nil {
			return invariantError("cumulative-emitted", err)
		}
		state, err := k.GetState(ctx)
		if err != nil {
			return invariantError("cumulative-emitted", err)
		}
		maxSupply, err := types.ParseAmountString("max supply", params.MaxSupply)
		if err != nil {
			return invariantError("cumulative-emitted", err)
		}
		cumulative, err := types.ParseAmountString("cumulative emitted", state.CumulativeEmitted)
		if err != nil {
			return invariantError("cumulative-emitted", err)
		}
		if cumulative.GT(maxSupply) {
			return sdk.FormatInvariant(types.ModuleName, "cumulative-emitted", fmt.Sprintf("%s > %s", cumulative, maxSupply)), true
		}
		return "", false
	}
}

func (k Keeper) ModuleBalanceCoverageInvariant() sdk.Invariant {
	return func(ctx sdk.Context) (string, bool) {
		params, err := k.GetParams(ctx)
		if err != nil {
			return invariantError("module-balance-coverage", err)
		}
		state, err := k.GetState(ctx)
		if err != nil {
			return invariantError("module-balance-coverage", err)
		}
		required, err := types.ParseAmountString("carry forward remainder", state.CarryForwardRemainder)
		if err != nil {
			return invariantError("module-balance-coverage", err)
		}
		err = k.ClaimRecords.Walk(ctx, nil, func(_ collections.Pair[uint64, uint64], reward types.EligibleSlotReward) (bool, error) {
			if reward.Claimed {
				return false, nil
			}
			amount, err := types.ParseAmountString("claim amount", reward.Amount)
			if err != nil {
				return true, err
			}
			required = required.Add(amount)
			return false, nil
		})
		if err != nil {
			return invariantError("module-balance-coverage", err)
		}
		address := k.accountKeeper.GetModuleAddress(types.ModuleName)
		if address == nil {
			return sdk.FormatInvariant(types.ModuleName, "module-balance-coverage", "module account missing"), true
		}
		balance := k.bankKeeper.GetBalance(ctx, address, params.NativeDenom).Amount
		if balance.LT(required) {
			return sdk.FormatInvariant(types.ModuleName, "module-balance-coverage", fmt.Sprintf("%s < %s", balance, required)), true
		}
		return "", false
	}
}

func (k Keeper) DenomCorrectnessInvariant() sdk.Invariant {
	return func(ctx sdk.Context) (string, bool) {
		params, err := k.GetParams(ctx)
		if err != nil {
			return invariantError("denom-correctness", err)
		}
		if err := types.ValidateDenom(params.NativeDenom); err != nil {
			return invariantError("denom-correctness", err)
		}
		if err := types.ValidateDenom(params.FeeDenom); err != nil {
			return invariantError("denom-correctness", err)
		}
		for _, value := range []string{params.MaxSupply, params.InitialBlockSubsidy} {
			if strings.ContainsAny(value, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ") {
				return sdk.FormatInvariant(types.ModuleName, "denom-correctness", "display metadata leaked into accounting amount"), true
			}
		}
		return "", false
	}
}

func (k Keeper) ClosedEpochImmutabilityInvariant() sdk.Invariant {
	return func(ctx sdk.Context) (string, bool) {
		err := k.FinalizedEpochs.Walk(ctx, nil, func(_ uint64, epoch types.EpochReward) (bool, error) {
			for _, reward := range epoch.Rewards {
				if reward.Claimed || reward.ClaimedAtHeight != 0 {
					return true, fmt.Errorf("finalized epoch %d aggregate contains mutable claim marker", epoch.EpochNumber)
				}
			}
			return false, nil
		})
		if err != nil {
			return invariantError("closed-epoch-immutability", err)
		}
		return "", false
	}
}

func invariantError(name string, err error) (string, bool) {
	return sdk.FormatInvariant(types.ModuleName, name, err.Error()), true
}
