package keeper

import "github.com/twilight-project/twilight-core/x/rewards/types"

// ConfiguredEpochEndHeight derives the open epoch end from its immutable snapshot.
func ConfiguredEpochEndHeight(state types.RewardsState, cfg types.EpochConfigSnapshot) (uint64, error) {
	if state.CurrentEpoch == 0 || state.CurrentEpochStartHeight == 0 {
		return 0, types.ErrInvalidState.Wrap("current epoch and start height must be positive")
	}
	if cfg.EpochLengthBlocks == 0 {
		return 0, types.ErrInvalidState.Wrap("current epoch config length must be positive")
	}

	delta := cfg.EpochLengthBlocks - 1
	if state.CurrentEpochStartHeight > ^uint64(0)-delta {
		return 0, types.ErrInvalidState.Wrap("configured epoch end height overflows uint64")
	}

	return state.CurrentEpochStartHeight + delta, nil
}

// ShouldFinalizeAtHeight reports whether the open epoch has reached its configured end.
func ShouldFinalizeAtHeight(
	height uint64,
	state types.RewardsState,
	cfg types.EpochConfigSnapshot,
	settlementEnabled bool,
) (bool, error) {
	endHeight, err := ConfiguredEpochEndHeight(state, cfg)
	if err != nil {
		return false, err
	}
	if !settlementEnabled {
		return false, nil
	}

	return height >= endHeight, nil
}
