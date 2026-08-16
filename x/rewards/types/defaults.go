package types

import appparams "github.com/twilight-project/twilight-core/app/params"

const (
	DefaultMaxSupply              = "21000000000000"
	DefaultInitialBlockSubsidy    = "416190"
	DefaultTargetBlockTimeSeconds = uint64(5)
	DefaultEpochLengthBlocks      = uint64(17280)
	DefaultMaxClaimEpochsPerTx    = uint64(100)
	DefaultEpochSnapshotVersion   = uint64(1)
)

func DefaultParams() Params {
	return Params{
		EmissionsEnabled:         true,
		EpochSettlementEnabled:   true,
		ClaimsEnabled:            true,
		NativeDenom:              appparams.NativeBaseDenom,
		MaxSupply:                DefaultMaxSupply,
		InitialBlockSubsidy:      DefaultInitialBlockSubsidy,
		TargetBlockTimeSeconds:   DefaultTargetBlockTimeSeconds,
		EpochLengthBlocks:        DefaultEpochLengthBlocks,
		HalvingMode:              HalvingMode_HALVING_MODE_SUPPLY_THRESHOLD,
		DistributionMethod:       DistributionMethod_DISTRIBUTION_METHOD_UNIFORM_ACTIVE_BLOCKS,
		RemainderPolicy:          RemainderPolicy_REMAINDER_POLICY_CARRY_FORWARD,
		MaxClaimEpochsPerTx:      DefaultMaxClaimEpochsPerTx,
		FeeCollectionEnabled:     false,
		FeeDistributionEnabled:   false,
		FeeDenom:                 appparams.NativeBaseDenom,
		FeeDistributionMode:      FeeDistributionMode_FEE_DISTRIBUTION_MODE_NONE,
		TreasuryAddress:          "",
		EmissionTreasuryShareBps: 0,
		FeeTreasuryShareBps:      0,
		WeightedRewardsEnabled:   false,
	}
}

// DefaultEpochConfigVersion returns the original-genesis epoch anchor for a
// chain starting at initialHeight: version 1, effective at epoch 1 (§11).
func DefaultEpochConfigVersion(params Params, initialHeight uint64) EpochConfigVersion {
	return EpochConfigVersion{
		Version:              1,
		EffectiveEpoch:       1,
		EffectiveStartHeight: initialHeight,
		EpochLengthBlocks:    params.EpochLengthBlocks,
	}
}

func DefaultEpochConfigSnapshot(params Params) EpochConfigSnapshot {
	return EpochConfigSnapshot{
		SnapshotVersion:          DefaultEpochSnapshotVersion,
		EpochLengthBlocks:        params.EpochLengthBlocks,
		DistributionMethod:       params.DistributionMethod,
		RemainderPolicy:          params.RemainderPolicy,
		InitialBlockSubsidy:      params.InitialBlockSubsidy,
		HalvingMode:              params.HalvingMode,
		WeightedRewardsEnabled:   params.WeightedRewardsEnabled,
		FeeCollectionEnabled:     params.FeeCollectionEnabled,
		FeeDistributionEnabled:   params.FeeDistributionEnabled,
		FeeDenom:                 params.FeeDenom,
		FeeDistributionMode:      params.FeeDistributionMode,
		TreasuryAddress:          params.TreasuryAddress,
		EmissionTreasuryShareBps: params.EmissionTreasuryShareBps,
		FeeTreasuryShareBps:      params.FeeTreasuryShareBps,
	}
}
