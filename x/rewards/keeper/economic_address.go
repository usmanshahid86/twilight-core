package keeper

import (
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// Canonical economic-address enforcement for x/rewards (§25).
//
// Every rule below delegates to the single app-derived validator held by the
// keeper. Nothing here reimplements what an admissible address is; these
// functions only express WHERE the rule applies and WHEN a conditional field
// makes it mandatory.

// treasuryAddressRequired reports whether a treasury configuration obliges a
// treasury address to be present.
//
// The architecture makes the address conditional on the shares: a nonzero share
// requires a valid canonical economic treasury address, and the recommended
// initial share is zero. The default configuration therefore ships with no
// treasury address at all, and that is a valid state rather than an omission —
// turning an absent address into an unconditional error would break every
// default genesis.
func treasuryAddressRequired(emissionShareBps, feeShareBps uint64) bool {
	return emissionShareBps > 0 || feeShareBps > 0
}

// validateTreasuryAddress applies the canonical rule to a treasury destination,
// but only where the configuration actually directs value at it.
//
// When no share is positive the address is legitimately absent and is left
// alone. It is deliberately NOT validated-if-present in that case: this change
// is an admission-policy change, and deciding what a disabled-treasury
// configuration may carry is a separate architectural question that stays open.
func (k Keeper) validateTreasuryAddress(label, address string, emissionShareBps, feeShareBps uint64) error {
	if !treasuryAddressRequired(emissionShareBps, feeShareBps) {
		return nil
	}
	if _, err := k.economicAddresses.Validate(address); err != nil {
		return types.ErrInvalidAddress.Wrapf("%s treasury address: %v", label, err)
	}
	return nil
}

// validateParamsTreasury applies the conditional treasury rule to a Params value.
func (k Keeper) validateParamsTreasury(label string, params types.Params) error {
	return k.validateTreasuryAddress(
		label, params.TreasuryAddress, params.EmissionTreasuryShareBps, params.FeeTreasuryShareBps,
	)
}

// validateSnapshotTreasury applies the conditional treasury rule to a stored
// epoch configuration snapshot.
//
// A snapshot is validated in its own right rather than trusted because Params
// was valid once: snapshots arrive independently through genesis import, and a
// configuration that was admissible under one Params value says nothing about
// the one being imported.
func (k Keeper) validateSnapshotTreasury(label string, snapshot types.EpochConfigSnapshot) error {
	return k.validateTreasuryAddress(
		label, snapshot.TreasuryAddress, snapshot.EmissionTreasuryShareBps, snapshot.FeeTreasuryShareBps,
	)
}

// validateRewardAddresses applies the canonical rule to the operator and payout
// addresses carried by a reward record. Both are persisted economic state and
// the payout address is a value destination at claim time.
func (k Keeper) validateRewardAddresses(label string, reward *types.EligibleSlotReward) error {
	if reward == nil {
		return types.ErrInvalidState.Wrapf("%s: reward record is nil", label)
	}
	if _, err := k.economicAddresses.Validate(reward.OperatorAddress); err != nil {
		return types.ErrInvalidAddress.Wrapf("%s operator address: %v", label, err)
	}
	if _, err := k.economicAddresses.Validate(reward.PayoutAddress); err != nil {
		return types.ErrInvalidAddress.Wrapf("%s payout address: %v", label, err)
	}
	return nil
}

// validateFinalizedEpochAddresses applies the canonical rule to every economic
// address a finalized epoch persists: the treasury destination inside its
// embedded configuration, and the operator/payout pair of each embedded reward.
func (k Keeper) validateFinalizedEpochAddresses(label string, epoch types.EpochReward) error {
	if epoch.Config != nil {
		if err := k.validateSnapshotTreasury(label, *epoch.Config); err != nil {
			return err
		}
	}
	for _, reward := range epoch.Rewards {
		if err := k.validateRewardAddresses(label, reward); err != nil {
			return err
		}
	}
	return nil
}
