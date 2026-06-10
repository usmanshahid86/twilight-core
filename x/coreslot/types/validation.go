package types

import (
	"fmt"
	"math"
	"strings"

	sdkmath "cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/types"
)

const (
	DefaultSlotVotingPower = int64(1)
	DefaultRewardWeight    = "1.000000000000000000"
)

func DefaultParams(authority, emergencyAuthority string) Params {
	return Params{
		Authority:                    authority,
		EmergencyAuthority:           emergencyAuthority,
		SlotVotingPower:              DefaultSlotVotingPower,
		MinActiveSlots:               1,
		MaxActiveSlots:               100,
		ActivationDelayBlocks:        0,
		KeyRotationDelayBlocks:       1,
		RemovalDelayBlocks:           0,
		ConsensusKeyReuseLockout:     100_000,
		AllowSelfRegistration:        false,
		AllowEmergencyBelowMinActive: false,
	}
}

func (p Params) Validate() error {
	if _, err := types.AccAddressFromBech32(p.Authority); err != nil {
		return ErrInvalidParams.Wrapf("authority: %v", err)
	}
	if _, err := types.AccAddressFromBech32(p.EmergencyAuthority); err != nil {
		return ErrInvalidParams.Wrapf("emergency authority: %v", err)
	}
	if p.SlotVotingPower <= 0 {
		return ErrInvalidParams.Wrap("slot voting power must be positive")
	}
	if p.MinActiveSlots == 0 || p.MaxActiveSlots < p.MinActiveSlots {
		return ErrInvalidParams.Wrap("invalid active slot bounds")
	}
	if p.MaxActiveSlots > uint64(math.MaxInt64/p.SlotVotingPower) {
		return ErrInvalidParams.Wrap("maximum total power overflows int64")
	}
	return nil
}

func ValidateMetadata(m *OperatorMetadata) error {
	if m == nil {
		return nil
	}
	for name, value := range map[string]string{
		"moniker": m.Moniker, "identity": m.Identity, "website": m.Website,
		"security_contact": m.SecurityContact, "details": m.Details,
	} {
		if len(value) > 512 {
			return fmt.Errorf("%s exceeds 512 bytes", name)
		}
	}
	return nil
}

func ValidateWeight(weight string) error {
	if strings.TrimSpace(weight) == "" {
		return fmt.Errorf("weight is empty")
	}
	v, err := sdkmath.LegacyNewDecFromStr(weight)
	if err != nil {
		return err
	}
	if v.IsNegative() {
		return fmt.Errorf("weight cannot be negative")
	}
	return nil
}

func DefaultGenesis(authority, emergencyAuthority string) *GenesisState {
	params := DefaultParams(authority, emergencyAuthority)
	return &GenesisState{
		Params:     &params,
		NextSlotId: 1,
	}
}
