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

// ModuleBalanceCoverageInvariant proves the escrow covers what the module owes.
//
// Rewritten from the claim-shaped model that this chain no longer has. That model
// summed every unclaimed record through a full walk; the obligation is now the O(1)
// outstanding entitlement liability plus carry, which is the same quantity
// finalization asserts and costs two reads instead of a scan.
//
// Coverage (>=) rather than the equality finalization asserts. The two differ
// deliberately: finalization checks the stronger statement at the single instant
// it is entitled to, immediately after a complete monetary transition. This is a
// general backstop callable at any height, where what the chain owes is solvency
// rather than exactness — a surplus in escrow is not a solvency failure and must
// not be reported as one.
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
		carry, err := types.ParseAmountString("carry forward remainder", state.CarryForwardRemainder)
		if err != nil {
			return invariantError("module-balance-coverage", err)
		}
		liability, err := k.GetOutstandingEntitlementLiability(ctx)
		if err != nil {
			return invariantError("module-balance-coverage", err)
		}
		required, err := liability.SafeAdd(carry)
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

// EntitlementLiabilityInvariant proves the O(1) accumulator has not drifted from
// the records it summarizes.
//
// This is the full scan the accumulator exists to avoid on the block path, run
// where its cost is acceptable. Without it the accumulator is unfalsifiable:
// every consumer reads the same stored number, so a drift would be invisible to
// all of them and would show up only as a solvency failure much later, or not at
// all.
//
// sdk.Invariant is deprecated pending the removal of x/crisis. It is used anyway
// because the module's four existing invariants all use it and register through
// the same registry; an invariant with a different shape could not be registered
// beside them. The deprecation is a reason to migrate that whole surface in one
// change, not a reason to leave this backstop unwritten.
//
//nolint:staticcheck // SA1019: consistent with the module's existing invariant surface
func (k Keeper) EntitlementLiabilityInvariant() sdk.Invariant {
	return func(ctx sdk.Context) (string, bool) {
		stored, err := k.GetOutstandingEntitlementLiability(ctx)
		if err != nil {
			return invariantError("entitlement-liability", err)
		}
		summed, err := k.SumOutstandingEntitlementLiability(ctx)
		if err != nil {
			return invariantError("entitlement-liability", err)
		}
		if !stored.Equal(summed) {
			//nolint:staticcheck // SA1019: see the note on this function
			return sdk.FormatInvariant(types.ModuleName, "entitlement-liability",
				fmt.Sprintf("accumulator %s but live entitlements sum to %s", stored, summed)), true
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

// ClosedEpochImmutabilityInvariant proves a closed epoch's record cannot acquire
// mutable state.
//
// Rewritten around entitlements. Two properties now, where there used to be one:
//
//   - a finalized epoch carries no embedded per-Slot rows. After the switchover
//     the canonical per-Slot record is the entitlement, so an aggregate that
//     acquired rows would be a second immutable copy of the same obligation, free
//     to drift from the one that can actually be paid.
//   - every live entitlement stays inside its own bound. released_amount is the
//     only field permitted to move, and it may never pass the amount it is
//     bounded by.
//
// The first property is now ENFORCED rather than described. It used to be stated
// in this comment while the code below only rejected mutable claim markers inside
// the rows — so an aggregate that acquired unclaimed rows satisfied the invariant
// and contradicted its own documentation.
//
// Requiring the collection to be empty is correct for conforming POC1 state and
// costs nothing to a conforming chain: V2 finalization writes no rows, and fresh
// genesis refuses a document that carries a finalized epoch at all. An aggregate
// with rows can therefore only come from state written directly for explicitly
// legacy regression coverage, which is exactly the state this invariant should
// distinguish from a real chain's.
func (k Keeper) ClosedEpochImmutabilityInvariant() sdk.Invariant {
	return func(ctx sdk.Context) (string, bool) {
		err := k.FinalizedEpochs.Walk(ctx, nil, func(_ uint64, epoch types.EpochReward) (bool, error) {
			if len(epoch.Rewards) != 0 {
				return true, fmt.Errorf(
					"finalized epoch %d aggregate embeds %d per-slot rows; the canonical per-slot record is the slot entitlement",
					epoch.EpochNumber, len(epoch.Rewards))
			}
			return false, nil
		})
		if err != nil {
			return invariantError("closed-epoch-immutability", err)
		}

		err = k.SlotEntitlements.Walk(ctx, nil,
			func(key collections.Pair[uint64, uint64], entitlement types.SlotEntitlement) (bool, error) {
				if err := validateEntitlementRecord(key, entitlement); err != nil {
					return true, err
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
