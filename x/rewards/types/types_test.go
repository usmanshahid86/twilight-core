package types_test

import (
	"reflect"
	"testing"

	sdkmath "cosmossdk.io/math"
	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	appparams "github.com/twilight-project/twilight-core/app/params"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

func testAddress(marker byte) string {
	address := make([]byte, 20)
	address[0] = marker
	return sdk.AccAddress(address).String()
}

func TestDefaultParams(t *testing.T) {
	params := types.DefaultParams()
	require.NoError(t, params.Validate())
	require.True(t, params.EmissionsEnabled)
	require.True(t, params.EpochSettlementEnabled)
	require.True(t, params.ClaimsEnabled)
	require.Equal(t, appparams.NativeBaseDenom, params.NativeDenom)
	require.Equal(t, "21000000000000", params.MaxSupply)
	require.Equal(t, "416190", params.InitialBlockSubsidy)
	require.Equal(t, uint64(5), params.TargetBlockTimeSeconds)
	require.Equal(t, uint64(17280), params.EpochLengthBlocks)
	require.Equal(t, types.HalvingMode_HALVING_MODE_SUPPLY_THRESHOLD, params.HalvingMode)
	require.Equal(t, types.DistributionMethod_DISTRIBUTION_METHOD_UNIFORM_ACTIVE_BLOCKS, params.DistributionMethod)
	require.Equal(t, types.RemainderPolicy_REMAINDER_POLICY_CARRY_FORWARD, params.RemainderPolicy)
	require.Equal(t, uint64(100), params.MaxClaimEpochsPerTx)
	require.False(t, params.FeeCollectionEnabled)
	require.False(t, params.FeeDistributionEnabled)
	require.Equal(t, appparams.NativeBaseDenom, params.FeeDenom)
	require.Equal(t, types.FeeDistributionMode_FEE_DISTRIBUTION_MODE_NONE, params.FeeDistributionMode)
	require.Empty(t, params.TreasuryAddress)
	require.Zero(t, params.EmissionTreasuryShareBps)
	require.Zero(t, params.FeeTreasuryShareBps)
	require.False(t, params.WeightedRewardsEnabled)
	require.NotEqual(t, appparams.NativeDisplayDenom, params.NativeDenom)
	require.NotEqual(t, appparams.NativeSymbol, params.NativeDenom)
	require.NotEqual(t, appparams.NativeName, params.NativeDenom)
}

func TestParamsDoNotStoreAuthority(t *testing.T) {
	paramsType := reflect.TypeOf(types.Params{})
	_, authorityExists := paramsType.FieldByName("Authority")
	_, emergencyExists := paramsType.FieldByName("EmergencyAuthority")
	require.False(t, authorityExists)
	require.False(t, emergencyExists)
}

func TestEligibleSlotRewardDoesNotStoreClaimTxHash(t *testing.T) {
	rewardType := reflect.TypeOf(types.EligibleSlotReward{})
	_, exists := rewardType.FieldByName("ClaimTxHash")
	require.False(t, exists)
}

func TestEnumNumericValuesAreFrozen(t *testing.T) {
	require.Equal(t, int32(0), int32(types.HalvingMode_HALVING_MODE_UNSPECIFIED))
	require.Equal(t, int32(1), int32(types.HalvingMode_HALVING_MODE_SUPPLY_THRESHOLD))
	require.Equal(t, int32(1), int32(types.DistributionMethod_DISTRIBUTION_METHOD_UNIFORM_ACTIVE_BLOCKS))
	require.Equal(t, int32(2), int32(types.DistributionMethod_DISTRIBUTION_METHOD_WEIGHTED_ACTIVE_BLOCKS))
	require.Equal(t, int32(3), int32(types.DistributionMethod_DISTRIBUTION_METHOD_SNAPSHOT_UNIFORM))
	require.Equal(t, int32(1), int32(types.RemainderPolicy_REMAINDER_POLICY_CARRY_FORWARD))
	require.Equal(t, int32(2), int32(types.RemainderPolicy_REMAINDER_POLICY_BURN))
	require.Equal(t, int32(1), int32(types.FeeDistributionMode_FEE_DISTRIBUTION_MODE_NONE))
	require.Equal(t, int32(2), int32(types.FeeDistributionMode_FEE_DISTRIBUTION_MODE_ACTIVE_SET_POOL))
	require.Equal(t, int32(3), int32(types.FeeDistributionMode_FEE_DISTRIBUTION_MODE_TREASURY_ONLY))
	require.Equal(t, int32(4), int32(types.FeeDistributionMode_FEE_DISTRIBUTION_MODE_BURN))
}

func TestAmountStringParsing(t *testing.T) {
	amount, err := types.ParseAmountString("amount", "21000000000000")
	require.NoError(t, err)
	require.True(t, amount.Equal(sdkmath.NewInt(21_000_000_000_000)))

	for _, invalid := range []string{"", " ", "-1", "1.5", "not-an-amount"} {
		_, err := types.ParseAmountString("amount", invalid)
		require.Error(t, err, invalid)
	}
}

func TestDenomGuardRejectsDisplayMetadata(t *testing.T) {
	require.NoError(t, types.ValidateDenom(appparams.NativeBaseDenom))
	for _, invalid := range []string{appparams.NativeDisplayDenom, appparams.NativeSymbol, appparams.NativeName, ""} {
		require.Error(t, types.ValidateDenom(invalid), invalid)
	}
}

func TestParamsValidationRejectsUnsupportedV1FeaturesAndEnums(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*types.Params)
	}{
		{"fee collection", func(p *types.Params) { p.FeeCollectionEnabled = true }},
		{"fee distribution", func(p *types.Params) { p.FeeDistributionEnabled = true }},
		{"weighted rewards", func(p *types.Params) { p.WeightedRewardsEnabled = true }},
		{"weighted method", func(p *types.Params) {
			p.DistributionMethod = types.DistributionMethod_DISTRIBUTION_METHOD_WEIGHTED_ACTIVE_BLOCKS
		}},
		{"burn remainder", func(p *types.Params) { p.RemainderPolicy = types.RemainderPolicy_REMAINDER_POLICY_BURN }},
		{"fee pool mode", func(p *types.Params) {
			p.FeeDistributionMode = types.FeeDistributionMode_FEE_DISTRIBUTION_MODE_ACTIVE_SET_POOL
		}},
		{"unspecified halving", func(p *types.Params) { p.HalvingMode = types.HalvingMode_HALVING_MODE_UNSPECIFIED }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			params := types.DefaultParams()
			tc.mutate(&params)
			require.Error(t, params.Validate())
		})
	}
}

func TestParamsValidationRejectsInvalidAmountsAndLimits(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*types.Params)
	}{
		{"zero max supply", func(p *types.Params) { p.MaxSupply = "0" }},
		{"negative subsidy", func(p *types.Params) { p.InitialBlockSubsidy = "-1" }},
		{"invalid fee denom", func(p *types.Params) { p.FeeDenom = appparams.NativeDisplayDenom }},
		{"zero block time", func(p *types.Params) { p.TargetBlockTimeSeconds = 0 }},
		{"zero epoch length", func(p *types.Params) { p.EpochLengthBlocks = 0 }},
		{"zero claim limit", func(p *types.Params) { p.MaxClaimEpochsPerTx = 0 }},
		{"emission bps", func(p *types.Params) { p.EmissionTreasuryShareBps = 10_001 }},
		{"fee bps", func(p *types.Params) { p.FeeTreasuryShareBps = 10_001 }},
		{"missing treasury", func(p *types.Params) { p.EmissionTreasuryShareBps = 1 }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			params := types.DefaultParams()
			tc.mutate(&params)
			require.Error(t, params.Validate())
		})
	}
}

func TestTreasuryAddressValidation(t *testing.T) {
	params := types.DefaultParams()
	params.TreasuryAddress = testAddress(1)
	params.EmissionTreasuryShareBps = 1
	params.FeeTreasuryShareBps = 10_000
	require.NoError(t, params.Validate())
}

func TestImmutableUpdateGuard(t *testing.T) {
	current := types.DefaultParams()
	next := current
	next.MaxClaimEpochsPerTx++
	require.NoError(t, types.ValidateUpdate(current, next))

	// Epoch geometry is owned by the canonical epoch-configuration history, so
	// this path must refuse to move it. Without the guard a params update would
	// be a second way to change epoch length — immediate, unversioned, and
	// invisible to every boundary already derived from the history.
	next = current
	next.EpochLengthBlocks++
	require.ErrorIs(t, types.ValidateUpdate(current, next), types.ErrImmutableParam)

	next = current
	next.NativeDenom = "other"
	require.ErrorIs(t, types.ValidateUpdate(current, next), types.ErrImmutableParam)

	next = current
	next.MaxSupply = "1"
	require.ErrorIs(t, types.ValidateUpdate(current, next), types.ErrImmutableParam)
}

func TestDefaultGenesis(t *testing.T) {
	genesis := types.DefaultGenesis()
	require.NoError(t, genesis.Validate())
	require.Equal(t, uint64(1), genesis.State.CurrentEpoch)
	require.Equal(t, uint64(1), genesis.State.CurrentEpochStartHeight)
	require.Equal(t, "0", genesis.State.CumulativeEmitted)
	require.Equal(t, "0", genesis.State.CarryForwardRemainder)
	require.Empty(t, genesis.FinalizedEpochs)
	require.Empty(t, genesis.ClaimRecords)
	require.False(t, genesis.HasPendingParams)
	require.Nil(t, genesis.PendingParams)
}

func TestGenesisPendingParamsPresence(t *testing.T) {
	genesis := types.DefaultGenesis()
	pending := types.DefaultParams()
	pending.MaxClaimEpochsPerTx++
	genesis.PendingParams = &pending
	require.Error(t, genesis.Validate())

	genesis.HasPendingParams = true
	require.NoError(t, genesis.Validate())

	genesis.PendingParams = nil
	require.Error(t, genesis.Validate())
}

func TestGenesisRejectsDuplicateEpochsAndClaims(t *testing.T) {
	genesis := types.DefaultGenesis()
	snapshot := types.DefaultEpochConfigSnapshot(*genesis.Params)
	epoch := &types.EpochReward{
		EpochNumber: 1, StartHeight: 1, EndHeight: 10, MintedEmission: "1", CarryIn: "0",
		DistributableFees: "0", TreasuryAmount: "0", RewardPool: "1", AllocatedAmount: "1",
		CarryOut: "0", DistributionMethod: genesis.Params.DistributionMethod,
		RemainderPolicy: genesis.Params.RemainderPolicy, CumulativeEmittedAfterEpoch: "1", Config: &snapshot,
	}
	genesis.FinalizedEpochs = []*types.EpochReward{epoch, epoch}
	require.Error(t, genesis.Validate())

	genesis.FinalizedEpochs = []*types.EpochReward{epoch}
	claim := &types.EligibleSlotReward{
		SlotId: 1, EpochNumber: 1, OperatorAddress: testAddress(2), PayoutAddress: testAddress(3),
		RewardWeight: "1.000000000000000000", EffectiveWeight: "10", Amount: "1",
	}
	genesis.ClaimRecords = []*types.EligibleSlotReward{claim, claim}
	require.Error(t, genesis.Validate())
}

func TestGenesisRejectsInvalidClaimMarkers(t *testing.T) {
	genesis := types.DefaultGenesis()
	genesis.ClaimRecords = []*types.EligibleSlotReward{{
		SlotId: 1, EpochNumber: 1, OperatorAddress: testAddress(2), PayoutAddress: testAddress(3),
		RewardWeight: "1.000000000000000000", EffectiveWeight: "10", Amount: "1", ClaimedAtHeight: 5,
	}}
	require.Error(t, genesis.Validate())
}
