package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"

	"github.com/twilight-project/twilight-core/x/rewards/types"
)

func (k Keeper) GetParams(ctx context.Context) (types.Params, error) {
	return k.Params.Get(ctx)
}

// MonetaryParams returns Params for a path that is about to mint or transfer, and
// refuses the ones that path cannot safely act on.
//
// # Why reading is not enough
//
// Params is written through SetParams, which validates. Every read afterwards then
// trusts the stored value — reasonably, for configuration that only describes
// things. It stops being reasonable for the three fields a monetary path CONSUMES,
// because a stored value that no admission path could have produced is not a stale
// setting, it is a wrong instruction that will be carried out:
//
//   - native_denom decides which denom is minted, which denom escrow solvency is
//     measured in, and which denom a release transfers. Corrupted to another
//     syntactically valid denom, the module mints something nobody can spend,
//     checks its own solvency in that same fiction, and pays entitlements in it —
//     while the real utwlt escrow sits untouched. §5 admits exactly one accounting
//     denom, and this is where that stops being a documentation statement.
//   - max_supply is the cap the emission is checked against.
//   - halving_mode selects the emission schedule itself.
//
// The three are genesis-fixed and immutable, so requiring them to still be
// admissible costs nothing a correct chain will ever notice.
//
// # What is deliberately NOT checked here
//
// The deprecated economic mirrors — initial_block_subsidy, emission_treasury_share
// and treasury_address on Params — are not consulted by any monetary path and are
// not validated here. Validating them would be the first step back toward treating
// them as authority, and the canonical RewardConfigVersion history is the authority
// precisely so they are not.
func (k Keeper) MonetaryParams(ctx context.Context) (types.Params, error) {
	params, err := k.GetParams(ctx)
	if err != nil {
		return types.Params{}, err
	}
	if err := types.ValidateDenom(params.NativeDenom); err != nil {
		return types.Params{}, types.ErrInvalidState.Wrapf(
			"the canonical accounting denom is not usable: %v", err)
	}
	maxSupply, err := types.ParseAmountString("max supply", params.MaxSupply)
	if err != nil {
		return types.Params{}, types.ErrInvalidState.Wrap(err.Error())
	}
	if !maxSupply.IsPositive() {
		return types.Params{}, types.ErrInvalidState.Wrapf(
			"max supply %s is not a usable emission cap", params.MaxSupply)
	}
	if params.HalvingMode != types.HalvingMode_HALVING_MODE_SUPPLY_THRESHOLD {
		return types.Params{}, types.ErrInvalidState.Wrapf(
			"halving mode %s is not a supported emission schedule", params.HalvingMode)
	}
	return params, nil
}

func (k Keeper) SetParams(ctx context.Context, params types.Params) error {
	if err := params.Validate(); err != nil {
		return err
	}
	if err := k.validateParamsTreasury("params", params); err != nil {
		return err
	}
	return k.Params.Set(ctx, params)
}

func (k Keeper) GetPendingParams(ctx context.Context) (types.Params, bool, error) {
	params, err := k.PendingParams.Get(ctx)
	if errors.Is(err, collections.ErrNotFound) {
		return types.Params{}, false, nil
	}
	return params, err == nil, err
}

func (k Keeper) SetPendingParams(ctx context.Context, params types.Params) error {
	current, err := k.GetParams(ctx)
	if err != nil {
		return err
	}
	if err := types.ValidateUpdate(current, params); err != nil {
		return err
	}
	if err := k.validateParamsTreasury("pending params", params); err != nil {
		return err
	}
	return k.PendingParams.Set(ctx, params)
}

func (k Keeper) ClearPendingParams(ctx context.Context) error {
	err := k.PendingParams.Remove(ctx)
	if errors.Is(err, collections.ErrNotFound) {
		return nil
	}
	return err
}

func BuildEpochConfigSnapshot(params types.Params) (types.EpochConfigSnapshot, error) {
	if err := params.Validate(); err != nil {
		return types.EpochConfigSnapshot{}, err
	}
	snapshot := types.DefaultEpochConfigSnapshot(params)
	if err := snapshot.Validate(); err != nil {
		return types.EpochConfigSnapshot{}, err
	}
	return snapshot, nil
}

// buildEpochConfigSnapshot rebuilds the epoch configuration snapshot for the
// non-geometry economics the finalizer still reads, and repopulates its
// deprecated epoch-length mirror from canonical history.
//
// The mirror is taken from EpochConfigVersion rather than from Params on
// purpose. Params.epoch_length_blocks is frozen genesis/compatibility data that
// no runtime path may change, so copying it forward would republish a value that
// is only correct because nothing is allowed to move it. Reading the authority
// instead means the mirror cannot drift even if that ever changes.
//
// It remains a mirror: nothing derives a boundary from it.
func (k Keeper) buildEpochConfigSnapshot(ctx context.Context, params types.Params) (types.EpochConfigSnapshot, error) {
	snapshot, err := BuildEpochConfigSnapshot(params)
	if err != nil {
		return types.EpochConfigSnapshot{}, err
	}
	state, err := k.GetState(ctx)
	if err != nil {
		return types.EpochConfigSnapshot{}, err
	}
	length, err := k.EpochLengthForEpoch(ctx, state.CurrentEpoch)
	if err != nil {
		return types.EpochConfigSnapshot{}, err
	}
	snapshot.EpochLengthBlocks = length
	if err := snapshot.Validate(); err != nil {
		return types.EpochConfigSnapshot{}, err
	}
	return snapshot, nil
}
