package types

func DefaultGenesis() *GenesisState {
	params := DefaultParams()
	snapshot := DefaultEpochConfigSnapshot(params)
	return &GenesisState{
		Params:             &params,
		State:              &RewardsState{CurrentEpoch: 1, CurrentEpochStartHeight: 1, CumulativeEmitted: "0", CarryForwardRemainder: "0"},
		CurrentEpochConfig: &snapshot,
	}
}

func (g GenesisState) Validate() error {
	if g.Params == nil || g.State == nil || g.CurrentEpochConfig == nil {
		return ErrInvalidGenesis.Wrap("params, state, and current epoch config are required")
	}
	if err := g.Params.Validate(); err != nil {
		return ErrInvalidGenesis.Wrap(err.Error())
	}
	if g.State.CurrentEpoch == 0 || g.State.CurrentEpochStartHeight == 0 {
		return ErrInvalidGenesis.Wrap("current epoch and start height must be positive")
	}
	if err := validateStateAmount("cumulative emitted", g.State.CumulativeEmitted); err != nil {
		return err
	}
	if err := validateStateAmount("carry forward remainder", g.State.CarryForwardRemainder); err != nil {
		return err
	}
	if err := g.CurrentEpochConfig.Validate(); err != nil {
		return err
	}
	if g.HasPendingParams {
		if g.PendingParams == nil {
			return ErrInvalidGenesis.Wrap("pending params flag requires pending params")
		}
		if err := ValidateUpdate(*g.Params, *g.PendingParams); err != nil {
			return ErrInvalidGenesis.Wrapf("pending params: %v", err)
		}
	} else if g.PendingParams != nil {
		return ErrInvalidGenesis.Wrap("pending params require explicit presence flag")
	}

	epochs := make(map[uint64]struct{}, len(g.FinalizedEpochs))
	for _, epoch := range g.FinalizedEpochs {
		if err := validateEpochReward(epoch); err != nil {
			return err
		}
		if _, exists := epochs[epoch.EpochNumber]; exists {
			return invalidDuplicate("finalized epoch", epoch.EpochNumber)
		}
		epochs[epoch.EpochNumber] = struct{}{}
	}

	claims := make(map[string]struct{}, len(g.ClaimRecords))
	for _, reward := range g.ClaimRecords {
		if err := validateEligibleReward(reward); err != nil {
			return err
		}
		key := claimKey(reward.SlotId, reward.EpochNumber)
		if _, exists := claims[key]; exists {
			return ErrInvalidGenesis.Wrapf("duplicate claim record %s", key)
		}
		claims[key] = struct{}{}
	}
	return nil
}
