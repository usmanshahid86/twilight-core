package types

import (
	"encoding/hex"
	"fmt"
	"math"

	sdked25519 "github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"
	gogoproto "github.com/cosmos/gogoproto/proto"
	anypb "github.com/cosmos/gogoproto/types/any"
)

type expectedValidator struct {
	slotID uint64
	power  int64
}

func (g GenesisState) Validate() error {
	if g.Params == nil {
		return fmt.Errorf("params are required")
	}
	if err := g.Params.Validate(); err != nil {
		return err
	}
	ids := map[uint64]struct{}{}
	operators := map[string]struct{}{}
	keys := map[string]struct{}{}
	activeValidators := map[string]expectedValidator{}
	var active uint64
	for _, slot := range g.Slots {
		if slot == nil {
			return fmt.Errorf("nil slot")
		}
		if slot.SlotId == 0 {
			return fmt.Errorf("slot id must be nonzero")
		}
		if _, ok := ids[slot.SlotId]; ok {
			return fmt.Errorf("duplicate slot id %d", slot.SlotId)
		}
		ids[slot.SlotId] = struct{}{}
		if _, err := sdk.AccAddressFromBech32(slot.OperatorAddress); err != nil {
			return fmt.Errorf("slot %d operator: %w", slot.SlotId, err)
		}
		if _, err := sdk.AccAddressFromBech32(slot.PayoutAddress); err != nil {
			return fmt.Errorf("slot %d payout: %w", slot.SlotId, err)
		}
		if slot.Status != SlotStatus_SLOT_STATUS_REMOVED {
			if _, ok := operators[slot.OperatorAddress]; ok {
				return fmt.Errorf("duplicate operator %s", slot.OperatorAddress)
			}
			operators[slot.OperatorAddress] = struct{}{}
		}
		if slot.ConsensusPubkey == nil || len(slot.ConsensusPubkey.Value) == 0 {
			return fmt.Errorf("slot %d has empty consensus pubkey", slot.SlotId)
		}
		if err := validateConsensusPubKey(slot.ConsensusPubkey); err != nil {
			return fmt.Errorf("slot %d consensus pubkey: %w", slot.SlotId, err)
		}
		key := slot.ConsensusPubkey.TypeUrl + string(slot.ConsensusPubkey.Value)
		if _, ok := keys[key]; ok {
			return fmt.Errorf("duplicate consensus pubkey")
		}
		keys[key] = struct{}{}
		if err := ValidateWeight(slot.RewardWeight); err != nil {
			return fmt.Errorf("slot %d reward weight: %w", slot.SlotId, err)
		}
		if slot.Status == SlotStatus_SLOT_STATUS_ACTIVE {
			active++
			if slot.ConsensusPower != g.Params.SlotVotingPower {
				return fmt.Errorf("active slot %d has invalid power", slot.SlotId)
			}
			addr, err := consensusAddressHex(slot.ConsensusPubkey)
			if err != nil {
				return fmt.Errorf("slot %d consensus address: %w", slot.SlotId, err)
			}
			activeValidators[addr] = expectedValidator{slotID: slot.SlotId, power: slot.ConsensusPower}
		} else if slot.ConsensusPower != 0 {
			return fmt.Errorf("non-active slot %d has nonzero power", slot.SlotId)
		}
	}
	for _, rotation := range g.PendingKeyRotations {
		if rotation == nil || rotation.SlotId == 0 {
			return fmt.Errorf("invalid pending key rotation")
		}
		if rotation.EffectiveHeight <= rotation.RequestedHeight {
			return fmt.Errorf("slot %d pending rotation has invalid effective height", rotation.SlotId)
		}
		if err := validateConsensusPubKey(rotation.NewPubkey); err != nil {
			return fmt.Errorf("slot %d pending new pubkey: %w", rotation.SlotId, err)
		}
		key := rotation.NewPubkey.TypeUrl + string(rotation.NewPubkey.Value)
		if _, ok := keys[key]; ok {
			return fmt.Errorf("duplicate consensus pubkey in pending rotation")
		}
		keys[key] = struct{}{}
	}
	if active < g.Params.MinActiveSlots || active > g.Params.MaxActiveSlots {
		return fmt.Errorf("active slot count %d outside [%d,%d]", active, g.Params.MinActiveSlots, g.Params.MaxActiveSlots)
	}
	if active > uint64(math.MaxInt64/g.Params.SlotVotingPower) {
		return fmt.Errorf("total active power overflows int64")
	}
	// Reward weights must reference an existing slot (F7).
	for _, weight := range g.RewardWeights {
		if weight == nil {
			return ErrInvalidGenesis.Wrap("nil reward weight")
		}
		if _, ok := ids[weight.SlotId]; !ok {
			return ErrInvalidGenesis.Wrapf("reward weight references unknown slot %d", weight.SlotId)
		}
	}
	// Reserved consensus addresses must be unique (F7).
	reserved := map[string]struct{}{}
	for _, reservation := range g.ReservedConsensusAddresses {
		if reservation == nil {
			return ErrInvalidGenesis.Wrap("nil reserved consensus address")
		}
		addr := hex.EncodeToString(reservation.ConsAddress)
		if _, ok := reserved[addr]; ok {
			return ErrInvalidGenesis.Wrapf("duplicate reserved consensus address %s", addr)
		}
		reserved[addr] = struct{}{}
	}
	// If LastAppliedValidators is supplied (e.g. from an export) it must match the
	// active slot set exactly. InitGenesis recomputes the set deterministically
	// from active slots, so any mismatch is rejected before import (F7).
	if len(g.LastAppliedValidators) > 0 {
		seen := map[string]struct{}{}
		for _, validator := range g.LastAppliedValidators {
			if validator == nil || validator.ConsensusPubkey == nil {
				return ErrInvalidGenesis.Wrap("nil last-applied validator")
			}
			addr, err := consensusAddressHex(validator.ConsensusPubkey)
			if err != nil {
				return ErrInvalidGenesis.Wrapf("last-applied validator pubkey: %v", err)
			}
			expected, ok := activeValidators[addr]
			if !ok {
				return ErrInvalidGenesis.Wrapf("last-applied validator %s is not an active slot", addr)
			}
			if validator.SlotId != expected.slotID || validator.Power != expected.power {
				return ErrInvalidGenesis.Wrapf("last-applied validator %s does not match active slot", addr)
			}
			if _, dup := seen[addr]; dup {
				return ErrInvalidGenesis.Wrapf("duplicate last-applied validator %s", addr)
			}
			seen[addr] = struct{}{}
		}
		if len(seen) != len(activeValidators) {
			return ErrInvalidGenesis.Wrap("last-applied validators do not match active slots")
		}
	}
	return nil
}

// consensusAddressHex returns the hex-encoded CometBFT consensus address for an
// ed25519 pubkey Any, matching keeper.consensusKey so genesis comparisons align
// with runtime indexing.
func consensusAddressHex(value *anypb.Any) (string, error) {
	if value == nil || value.TypeUrl != "/cosmos.crypto.ed25519.PubKey" {
		return "", fmt.Errorf("only ed25519 consensus keys are supported")
	}
	var pk sdked25519.PubKey
	if err := gogoproto.Unmarshal(value.Value, &pk); err != nil {
		return "", err
	}
	if len(pk.Key) != sdked25519.PubKeySize {
		return "", fmt.Errorf("expected %d-byte key", sdked25519.PubKeySize)
	}
	return hex.EncodeToString(pk.Address().Bytes()), nil
}

func validateConsensusPubKey(value *anypb.Any) error {
	if value == nil || value.TypeUrl != "/cosmos.crypto.ed25519.PubKey" {
		return fmt.Errorf("only ed25519 consensus keys are supported")
	}
	var pk sdked25519.PubKey
	if err := gogoproto.Unmarshal(value.Value, &pk); err != nil {
		return err
	}
	if len(pk.Key) != sdked25519.PubKeySize {
		return fmt.Errorf("expected %d-byte key", sdked25519.PubKeySize)
	}
	return nil
}
