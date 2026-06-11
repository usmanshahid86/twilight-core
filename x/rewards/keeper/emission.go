package keeper

import (
	"fmt"

	"cosmossdk.io/math"

	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// HalvingTier returns the supply-threshold tier reached by cumulativeEmitted.
func HalvingTier(cumulativeEmitted, maxSupply math.Int) (uint64, error) {
	if err := validateSupply(cumulativeEmitted, maxSupply); err != nil {
		return 0, err
	}

	var tier uint64
	for remaining := maxSupply.QuoRaw(2); remaining.IsPositive(); remaining = remaining.QuoRaw(2) {
		threshold := maxSupply.Sub(remaining)
		if cumulativeEmitted.LT(threshold) {
			break
		}
		tier++
	}

	return tier, nil
}

// NextHalvingThreshold returns the next supply threshold above cumulativeEmitted.
func NextHalvingThreshold(cumulativeEmitted, maxSupply math.Int) (math.Int, bool, error) {
	if err := validateSupply(cumulativeEmitted, maxSupply); err != nil {
		return math.Int{}, false, err
	}
	if cumulativeEmitted.Equal(maxSupply) {
		return math.ZeroInt(), false, nil
	}

	for remaining := maxSupply.QuoRaw(2); remaining.IsPositive(); remaining = remaining.QuoRaw(2) {
		threshold := maxSupply.Sub(remaining)
		if cumulativeEmitted.LT(threshold) {
			return threshold, true, nil
		}
	}

	return maxSupply, true, nil
}

// NextBlockSubsidy returns the tier subsidy clipped at the next threshold and max supply.
func NextBlockSubsidy(cumulativeEmitted, maxSupply, initialBlockSubsidy math.Int) (math.Int, error) {
	if err := validateEmissionInputs(cumulativeEmitted, maxSupply, initialBlockSubsidy); err != nil {
		return math.Int{}, err
	}
	if cumulativeEmitted.Equal(maxSupply) || initialBlockSubsidy.IsZero() {
		return math.ZeroInt(), nil
	}

	tier, err := HalvingTier(cumulativeEmitted, maxSupply)
	if err != nil {
		return math.Int{}, err
	}

	subsidy := initialBlockSubsidy
	for i := uint64(0); i < tier && subsidy.IsPositive(); i++ {
		subsidy = subsidy.QuoRaw(2)
	}
	if subsidy.IsZero() {
		return math.ZeroInt(), nil
	}

	nextThreshold, found, err := NextHalvingThreshold(cumulativeEmitted, maxSupply)
	if err != nil {
		return math.Int{}, err
	}

	remaining := maxSupply.Sub(cumulativeEmitted)
	if found {
		toThreshold := nextThreshold.Sub(cumulativeEmitted)
		if toThreshold.LT(remaining) {
			remaining = toThreshold
		}
	}
	if subsidy.GT(remaining) {
		return remaining, nil
	}

	return subsidy, nil
}

// ComputeEpochEmission applies NextBlockSubsidy once per block.
func ComputeEpochEmission(
	cumulativeEmitted math.Int,
	blockCount uint64,
	maxSupply math.Int,
	initialBlockSubsidy math.Int,
	halvingMode types.HalvingMode,
) (math.Int, math.Int, error) {
	if halvingMode != types.HalvingMode_HALVING_MODE_SUPPLY_THRESHOLD {
		return math.Int{}, math.Int{}, fmt.Errorf("unsupported halving mode: %s", halvingMode.String())
	}
	if err := validateEmissionInputs(cumulativeEmitted, maxSupply, initialBlockSubsidy); err != nil {
		return math.Int{}, math.Int{}, err
	}

	emission := math.ZeroInt()
	cumulativeAfter := cumulativeEmitted
	for i := uint64(0); i < blockCount; i++ {
		subsidy, err := NextBlockSubsidy(cumulativeAfter, maxSupply, initialBlockSubsidy)
		if err != nil {
			return math.Int{}, math.Int{}, err
		}
		if subsidy.IsZero() {
			break
		}

		emission = emission.Add(subsidy)
		cumulativeAfter = cumulativeAfter.Add(subsidy)
	}

	return emission, cumulativeAfter, nil
}

func validateEmissionInputs(cumulativeEmitted, maxSupply, initialBlockSubsidy math.Int) error {
	if err := validateSupply(cumulativeEmitted, maxSupply); err != nil {
		return err
	}
	if initialBlockSubsidy.IsNil() {
		return fmt.Errorf("initial block subsidy must not be nil")
	}
	if initialBlockSubsidy.IsNegative() {
		return fmt.Errorf("initial block subsidy must not be negative")
	}

	return nil
}

func validateSupply(cumulativeEmitted, maxSupply math.Int) error {
	if maxSupply.IsNil() {
		return fmt.Errorf("max supply must not be nil")
	}
	if !maxSupply.IsPositive() {
		return fmt.Errorf("max supply must be positive")
	}
	if cumulativeEmitted.IsNil() {
		return fmt.Errorf("cumulative emitted must not be nil")
	}
	if cumulativeEmitted.IsNegative() {
		return fmt.Errorf("cumulative emitted must not be negative")
	}
	if cumulativeEmitted.GT(maxSupply) {
		return fmt.Errorf("cumulative emitted must not exceed max supply")
	}

	return nil
}
