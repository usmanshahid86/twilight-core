package types

import (
	"fmt"
	"strings"

	sdkmath "cosmossdk.io/math"
	sdk "github.com/cosmos/cosmos-sdk/types"

	appparams "github.com/twilight-project/twilight-core/app/params"
)

const MaxBasisPoints = uint64(10_000)

func ParseAmountString(name, value string) (sdkmath.Int, error) {
	if strings.TrimSpace(value) == "" {
		return sdkmath.Int{}, ErrInvalidParams.Wrapf("%s is empty", name)
	}
	amount, ok := sdkmath.NewIntFromString(value)
	if !ok {
		return sdkmath.Int{}, ErrInvalidParams.Wrapf("%s is not an integer", name)
	}
	if amount.IsNegative() {
		return sdkmath.Int{}, ErrInvalidParams.Wrapf("%s cannot be negative", name)
	}
	return amount, nil
}

func ValidateWeightString(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return ErrInvalidGenesis.Wrapf("%s is empty", name)
	}
	weight, err := sdkmath.LegacyNewDecFromStr(value)
	if err != nil {
		return ErrInvalidGenesis.Wrapf("%s is not a decimal: %v", name, err)
	}
	if weight.IsNegative() {
		return ErrInvalidGenesis.Wrapf("%s cannot be negative", name)
	}
	return nil
}

func ValidateDenom(denom string) error {
	if denom != appparams.NativeBaseDenom {
		return ErrInvalidParams.Wrapf("accounting denom must be %s", appparams.NativeBaseDenom)
	}
	return nil
}

func ValidateBasisPoints(name string, value uint64) error {
	if value > MaxBasisPoints {
		return ErrInvalidParams.Wrapf("%s exceeds %d", name, MaxBasisPoints)
	}
	return nil
}

func (p Params) Validate() error {
	maxSupply, err := ParseAmountString("max supply", p.MaxSupply)
	if err != nil {
		return err
	}
	if !maxSupply.IsPositive() {
		return ErrInvalidParams.Wrap("max supply must be positive")
	}
	subsidy, err := ParseAmountString("initial block subsidy", p.InitialBlockSubsidy)
	if err != nil {
		return err
	}
	if !subsidy.IsPositive() {
		return ErrInvalidParams.Wrap("initial block subsidy must be positive")
	}
	if err := ValidateDenom(p.NativeDenom); err != nil {
		return err
	}
	if p.FeeDenom != p.NativeDenom {
		return ErrInvalidParams.Wrap("fee denom must equal native denom")
	}
	if err := ValidateDenom(p.FeeDenom); err != nil {
		return err
	}
	if p.TargetBlockTimeSeconds == 0 || p.EpochLengthBlocks == 0 || p.MaxClaimEpochsPerTx == 0 {
		return ErrInvalidParams.Wrap("block time, epoch length, and claim limit must be positive")
	}
	if p.HalvingMode != HalvingMode_HALVING_MODE_SUPPLY_THRESHOLD {
		return ErrInvalidParams.Wrap("unsupported halving mode")
	}
	if p.DistributionMethod != DistributionMethod_DISTRIBUTION_METHOD_UNIFORM_ACTIVE_BLOCKS {
		return ErrUnsupportedFeature.Wrap("only uniform active blocks is supported in v1")
	}
	if p.RemainderPolicy != RemainderPolicy_REMAINDER_POLICY_CARRY_FORWARD {
		return ErrUnsupportedFeature.Wrap("only carry-forward remainder is supported in v1")
	}
	if p.FeeDistributionMode != FeeDistributionMode_FEE_DISTRIBUTION_MODE_NONE {
		return ErrUnsupportedFeature.Wrap("fee distribution mode must be none in v1")
	}
	if p.FeeCollectionEnabled || p.FeeDistributionEnabled {
		return ErrUnsupportedFeature.Wrap("fee collection and distribution are disabled in v1")
	}
	if p.WeightedRewardsEnabled {
		return ErrUnsupportedFeature.Wrap("weighted rewards are disabled in v1")
	}
	if err := ValidateBasisPoints("emission treasury share", p.EmissionTreasuryShareBps); err != nil {
		return err
	}
	if err := ValidateBasisPoints("fee treasury share", p.FeeTreasuryShareBps); err != nil {
		return err
	}
	if p.EmissionTreasuryShareBps > 0 || p.FeeTreasuryShareBps > 0 {
		if _, err := sdk.AccAddressFromBech32(p.TreasuryAddress); err != nil {
			return ErrInvalidParams.Wrapf("treasury address: %v", err)
		}
	}
	return nil
}

func (s EpochConfigSnapshot) Validate() error {
	if s.SnapshotVersion == 0 || s.EpochLengthBlocks == 0 {
		return ErrInvalidGenesis.Wrap("epoch snapshot version and length must be positive")
	}
	if _, err := ParseAmountString("snapshot initial block subsidy", s.InitialBlockSubsidy); err != nil {
		return ErrInvalidGenesis.Wrap(err.Error())
	}
	params := DefaultParams()
	params.EpochLengthBlocks = s.EpochLengthBlocks
	params.DistributionMethod = s.DistributionMethod
	params.RemainderPolicy = s.RemainderPolicy
	params.InitialBlockSubsidy = s.InitialBlockSubsidy
	params.HalvingMode = s.HalvingMode
	params.WeightedRewardsEnabled = s.WeightedRewardsEnabled
	params.FeeCollectionEnabled = s.FeeCollectionEnabled
	params.FeeDistributionEnabled = s.FeeDistributionEnabled
	params.FeeDenom = s.FeeDenom
	params.FeeDistributionMode = s.FeeDistributionMode
	params.TreasuryAddress = s.TreasuryAddress
	params.EmissionTreasuryShareBps = s.EmissionTreasuryShareBps
	params.FeeTreasuryShareBps = s.FeeTreasuryShareBps
	if err := params.Validate(); err != nil {
		return ErrInvalidGenesis.Wrapf("epoch snapshot: %v", err)
	}
	return nil
}

func ValidateUpdate(current, next Params) error {
	if current.NativeDenom != next.NativeDenom {
		return ErrImmutableParam.Wrap("native denom")
	}
	if current.MaxSupply != next.MaxSupply {
		return ErrImmutableParam.Wrap("max supply")
	}
	// Epoch geometry belongs to the canonical EpochConfigVersion history, which
	// has its own effective-epoch scheduling rules. Allowing the generic params
	// path to move this mirror would be a second, unversioned way to change epoch
	// length — one that takes effect immediately and leaves no history entry, so
	// every already-derived boundary would silently disagree with it.
	if current.EpochLengthBlocks != next.EpochLengthBlocks {
		return ErrImmutableParam.Wrap(
			"epoch length is governed by the canonical epoch configuration history and cannot be changed through a params update")
	}
	if err := validateRewardEconomicsUnchanged(current, next); err != nil {
		return err
	}
	return next.Validate()
}

// validateRewardEconomicsUnchanged freezes every economic field that stopped
// being runtime-mutable when the canonical reward-configuration history became
// the authority.
//
// Two groups, frozen for different reasons.
//
// The first three moved to RewardConfigVersion. That history binds a target two
// epochs ahead, so a change reaching money through this path instead would take
// effect at the very next epoch, leave no version behind, and be invisible to
// anything auditing which configuration governed a payout.
//
// The rest are genesis-fixed protocol configuration (§33): the halving mode and
// the remainder policy are not versioned at all, and the distribution method is
// fixed by the V2 allocation rule. They are frozen here because "already
// constant" is enforced by Params.Validate rejecting other values, which is a
// weaker and less legible guarantee than refusing the change outright.
//
// Rejection is the point. An accepted transaction that silently did nothing would
// report success to an authority that believed it had changed the economics, and
// the deprecated mirrors would then disagree with the version that actually
// governs. There is no runtime RewardConfig update path in this profile, so the
// correct answer to every such request is a refusal.
func validateRewardEconomicsUnchanged(current, next Params) error {
	if current.InitialBlockSubsidy != next.InitialBlockSubsidy {
		return ErrImmutableParam.Wrap(
			"the initial block subsidy is governed by the canonical reward configuration history and cannot be changed through a params update")
	}
	if current.EmissionTreasuryShareBps != next.EmissionTreasuryShareBps {
		return ErrImmutableParam.Wrap(
			"the emission treasury share is governed by the canonical reward configuration history and cannot be changed through a params update")
	}
	if current.TreasuryAddress != next.TreasuryAddress {
		return ErrImmutableParam.Wrap(
			"the treasury address is governed by the canonical reward configuration history and cannot be changed through a params update")
	}
	if current.HalvingMode != next.HalvingMode {
		return ErrImmutableParam.Wrap("the halving mode is genesis-fixed protocol configuration")
	}
	if current.RemainderPolicy != next.RemainderPolicy {
		return ErrImmutableParam.Wrap("the remainder policy is genesis-fixed protocol configuration")
	}
	if current.DistributionMethod != next.DistributionMethod {
		return ErrImmutableParam.Wrap("the distribution method is genesis-fixed protocol configuration")
	}
	// Fee distribution is disabled in V2 and the fee share reaches no computation,
	// but it is a basis-point economic field on a live authority message. Freezing
	// it keeps the params document from acquiring a value that a future reader
	// would have to work out is inert.
	if current.FeeTreasuryShareBps != next.FeeTreasuryShareBps {
		return ErrImmutableParam.Wrap("the fee treasury share is genesis-fixed protocol configuration")
	}
	return nil
}

func validateStateAmount(name, value string) error {
	if _, err := ParseAmountString(name, value); err != nil {
		return ErrInvalidGenesis.Wrap(err.Error())
	}
	return nil
}

func validateAddress(name, address string) error {
	if _, err := sdk.AccAddressFromBech32(address); err != nil {
		return ErrInvalidGenesis.Wrapf("%s: %v", name, err)
	}
	return nil
}

func validateEligibleReward(reward *EligibleSlotReward) error {
	if reward == nil || reward.SlotId == 0 || reward.EpochNumber == 0 {
		return ErrInvalidGenesis.Wrap("claim record requires nonzero slot and epoch")
	}
	if err := validateAddress("operator address", reward.OperatorAddress); err != nil {
		return err
	}
	if err := validateAddress("payout address", reward.PayoutAddress); err != nil {
		return err
	}
	if err := ValidateWeightString("reward weight", reward.RewardWeight); err != nil {
		return err
	}
	if err := ValidateWeightString("effective weight", reward.EffectiveWeight); err != nil {
		return err
	}
	if err := validateStateAmount("reward amount", reward.Amount); err != nil {
		return err
	}
	if !reward.Claimed && reward.ClaimedAtHeight != 0 {
		return ErrInvalidGenesis.Wrap("unclaimed reward has claimed height")
	}
	return nil
}

func validateEpochReward(epoch *EpochReward) error {
	if epoch == nil || epoch.EpochNumber == 0 || epoch.StartHeight == 0 || epoch.EndHeight < epoch.StartHeight {
		return ErrInvalidGenesis.Wrap("invalid finalized epoch identity or heights")
	}
	for name, value := range map[string]string{
		"minted emission":                epoch.MintedEmission,
		"carry in":                       epoch.CarryIn,
		"distributable fees":             epoch.DistributableFees,
		"treasury amount":                epoch.TreasuryAmount,
		"reward pool":                    epoch.RewardPool,
		"allocated amount":               epoch.AllocatedAmount,
		"carry out":                      epoch.CarryOut,
		"cumulative emitted after epoch": epoch.CumulativeEmittedAfterEpoch,
	} {
		if err := validateStateAmount(name, value); err != nil {
			return err
		}
	}
	if epoch.Config == nil {
		return ErrInvalidGenesis.Wrap("finalized epoch config is required")
	}
	if err := epoch.Config.Validate(); err != nil {
		return err
	}
	for _, reward := range epoch.Rewards {
		if err := validateEligibleReward(reward); err != nil {
			return err
		}
		if reward.EpochNumber != epoch.EpochNumber {
			return ErrInvalidGenesis.Wrap("epoch reward contains mismatched claim epoch")
		}
	}
	return nil
}

func invalidDuplicate(kind string, values ...uint64) error {
	return ErrInvalidGenesis.Wrapf("duplicate %s %v", kind, values)
}

func claimKey(slotID, epoch uint64) string {
	return fmt.Sprintf("%d/%d", slotID, epoch)
}
