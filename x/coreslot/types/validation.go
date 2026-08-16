package types

import (
	"fmt"
	"math"
	"strings"

	sdkmath "cosmossdk.io/math"
	"github.com/cosmos/cosmos-sdk/types"

	appparams "github.com/twilight-project/twilight-core/app/params"
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
	// The configured operational ceiling must sit within the immutable one. These
	// are two layers, not one: MaxActiveSlots may be lowered by governance and
	// binds first, while HardMaxActiveCoreSlots is the outer guarantee that no
	// state exceeds it whatever governance does.
	if err := appparams.ValidateMaxActiveSlots(p.MaxActiveSlots); err != nil {
		return ErrInvalidParams.Wrapf("%v", err)
	}
	if p.MaxActiveSlots > uint64(math.MaxInt64/p.SlotVotingPower) {
		return ErrInvalidParams.Wrap("maximum total power overflows int64")
	}
	// Deprecated V1 parameters. The fields keep their numbers for wire
	// compatibility under the unchanged v1 package, but V2 admits only their
	// zero values so a stored parameter set cannot describe behavior the chain
	// no longer implements.
	if p.ActivationDelayBlocks != 0 {
		return ErrInvalidParams.Wrap("activation_delay_blocks is deprecated and must be 0")
	}
	if p.RemovalDelayBlocks != 0 {
		return ErrInvalidParams.Wrap("removal_delay_blocks is deprecated and must be 0")
	}
	if p.AllowSelfRegistration {
		return ErrInvalidParams.Wrap("allow_self_registration is deprecated and must be false")
	}
	return nil
}

// ValidateSelectionPolicyValues enforces the §27 LOCAL structural rule on the two
// operator-selectable values of a Selection policy:
//
//	0 < selection_rate_bps <= AbsoluteMaxSelectionRateBps
//	max_selected_participants > 0
//
// There is deliberately no upper bound on max_selected_participants. A-SEL-01
// removed the independent HARD_MAX_SELECTED_PARTICIPANTS outright rather than
// recalibrating it, so no replacement ceiling is invented here and the superseded
// app/params helper that took one is not called. The absolute selected-population
// bound is derived later inside x/mining from the candidate ceiling and
// K <= floor(C/2); x/coreslot cannot see those and must not import x/mining.
//
// Whether a policy must additionally conform to a global operational
// SelectionParams envelope is unresolved (B5/Y-4) and is not decided here.
func ValidateSelectionPolicyValues(selectionRateBps, maxSelectedParticipants uint64) error {
	if err := appparams.ValidateSelectionRateBps(selectionRateBps, appparams.AbsoluteMaxSelectionRateBps); err != nil {
		return ErrInvalidSelectionPolicy.Wrapf("%v", err)
	}
	if maxSelectedParticipants == 0 {
		return ErrInvalidSelectionPolicy.Wrap("max selected participants must be positive")
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
