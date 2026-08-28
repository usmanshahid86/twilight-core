package types

import (
	"encoding/hex"
	"fmt"
	"math"

	sdked25519 "github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"
	gogoproto "github.com/cosmos/gogoproto/proto"
	anypb "github.com/cosmos/gogoproto/types/any"

	appparams "github.com/twilight-project/twilight-core/app/params"
	"github.com/twilight-project/twilight-core/internal/checked"
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
	if err := g.validatePendingAuthorityTransfers(); err != nil {
		return err
	}
	policies, err := indexGenesisPolicies(g.SelectionPolicies)
	if err != nil {
		return err
	}
	ids := map[uint64]struct{}{}
	operators := map[string]struct{}{}
	keys := map[string]struct{}{}
	activeValidators := map[string]expectedValidator{}
	var active uint64
	var maxSlotID uint64
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
		if slot.SlotId > maxSlotID {
			maxSlotID = slot.SlotId
		}
		if err := validateFreshGenesisSlot(slot, policies); err != nil {
			return err
		}
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
	// A pending key rotation is lifecycle history: it can only exist because a
	// runtime rotation request staged one, which a fresh chain has not had. Fresh
	// genesis therefore admits none at all.
	//
	// This is a rejection rather than an import path on purpose. Accepting a
	// staged rotation would require rebuilding the state that made it safe — the
	// new key's entry in the consensus-key index, and the uniqueness guarantee
	// that entry carries — from a genesis file that cannot be checked against the
	// history it came from. Reconstructing that is continuation work (H7), and
	// doing it here would decide continuation semantics as a side effect of
	// authoring a fresh chain.
	//
	// The check is one condition on the whole collection rather than a per-row
	// validation for the same reason: there is no such thing as a well-formed
	// pending rotation at fresh genesis, so inspecting the rows would imply some
	// of them could be admissible.
	if len(g.PendingKeyRotations) != 0 {
		return ErrInvalidGenesis.Wrapf(
			"fresh genesis admits no pending key rotations, has %d; importing staged rotations is continuation work",
			len(g.PendingKeyRotations))
	}
	// The pre-existing operational relation, unchanged: the configured window
	// binds first and its lower end is the established min-active semantic.
	if active < g.Params.MinActiveSlots || active > g.Params.MaxActiveSlots {
		return fmt.Errorf("active slot count %d outside [%d,%d]", active, g.Params.MinActiveSlots, g.Params.MaxActiveSlots)
	}
	// The immutable ceiling, asserted separately. Params.Validate already caps
	// MaxActiveSlots at the same bound, so reaching this is only possible if that
	// validation ever changes — which is exactly the case a resource-closure
	// guarantee must survive. Genesis bypasses the activation handler, so without
	// this check a genesis file could seed an ACTIVE set that runtime activation
	// is forbidden to create.
	if active > appparams.HardMaxActiveCoreSlots {
		return fmt.Errorf("active slot count %d exceeds hard maximum %d", active, appparams.HardMaxActiveCoreSlots)
	}
	if active > uint64(math.MaxInt64/g.Params.SlotVotingPower) {
		return fmt.Errorf("total active power overflows int64")
	}
	// Every policy row must belong to a slot present in this genesis. The
	// converse — every slot has a version-1 row — is enforced per slot above.
	for slotID := range policies {
		if _, ok := ids[slotID]; !ok {
			return ErrInvalidGenesis.Wrapf("selection policy references unknown slot %d", slotID)
		}
	}
	// NextSlotID must be beyond every assigned identifier so no future
	// registration can collide with a genesis slot. Checked so a genesis at the
	// top of the uint64 range is rejected rather than wrapping to zero.
	nextFromMax, err := checked.AddUint64(maxSlotID, 1)
	if err != nil {
		return ErrInvalidGenesis.Wrapf("slot id %d leaves no room for next slot id: %v", maxSlotID, err)
	}
	if g.NextSlotId < nextFromMax {
		return ErrInvalidGenesis.Wrapf("next slot id %d must exceed maximum slot id %d", g.NextSlotId, maxSlotID)
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

// EffectiveInitialHeight applies the SDK/BaseApp convention to the height a
// genesis document or InitChain context supplies. Keeping this normalization in
// one shared helper prevents the CLI and keeper from becoming independent
// authorities on which block is the first block.
func EffectiveInitialHeight(height int64) (int64, error) {
	if height < 0 {
		return 0, ErrInvalidGenesis.Wrapf("initial height %d is negative", height)
	}
	if height == 0 {
		return 1, nil
	}
	return height, nil
}

// ValidateFreshGenesisInitialHeight pins the height-bearing portions of fresh
// CoreSlot genesis to the effective first-block height. GenesisState.Validate
// checks the internally decidable shape; callers that know the document/context
// height use this second pure preflight before accepting or writing the state.
func ValidateFreshGenesisInitialHeight(genesis *GenesisState, initialHeight int64) error {
	if genesis == nil {
		return ErrInvalidGenesis.Wrap("genesis state is nil")
	}
	if initialHeight < 1 {
		return ErrInvalidGenesis.Wrapf("initial height must be at least 1, is %d", initialHeight)
	}
	for _, slot := range genesis.Slots {
		if slot.Status != SlotStatus_SLOT_STATUS_ACTIVE {
			continue
		}
		if slot.ActivatedHeight != initialHeight || slot.ActivationEffectiveHeight != initialHeight {
			return ErrInvalidGenesis.Wrapf(
				"active slot %d must have activated and activation-effective heights equal to the initial height %d",
				slot.SlotId, initialHeight)
		}
	}
	for _, policy := range genesis.SelectionPolicies {
		if policy.ValidFromHeight != initialHeight {
			return ErrInvalidGenesis.Wrapf(
				"slot %d policy must be valid from the initial height %d, is %d",
				policy.SlotId, initialHeight, policy.ValidFromHeight)
		}
	}
	return nil
}

// indexGenesisPolicies groups the genesis Selection policy rows by slot.
//
// This is the single seam through which the genesis Selection-policy collection
// is read, and it is deliberately narrow. It enforces only rules the architecture
// states explicitly: §26 requires exactly one current version per slot, so two rows for
// the same slot cannot both be current and are rejected. It decides nothing about
// the unresolved general question (B4/Y-3) of how a genesis collection with
// duplicate keys or non-canonical ordering is treated across the module — when
// that is ratified, this function is the one place that changes.
//
// Ordering of the input list does not affect the result, and no caller may rely
// on that: it is an artifact of grouping by key, not a decided acceptance rule.
func indexGenesisPolicies(rows []*SelectionPolicyVersion) (map[uint64]*SelectionPolicyVersion, error) {
	policies := make(map[uint64]*SelectionPolicyVersion, len(rows))
	for _, row := range rows {
		if row == nil {
			return nil, ErrInvalidGenesis.Wrap("nil selection policy")
		}
		if _, exists := policies[row.SlotId]; exists {
			return nil, ErrInvalidGenesis.Wrapf("slot %d has more than one selection policy version", row.SlotId)
		}
		policies[row.SlotId] = row
	}
	return policies, nil
}

// validateFreshGenesisSlot applies the §80 fresh-genesis contract to one slot, to
// the extent it can be decided without the chain's initial height or the
// app-derived economic-address rule. Those two legs are enforced by the keeper
// before any write; everything checkable from the record alone is checked here.
//
// This is validation, not normalization: a record that does not already carry
// these values is rejected rather than rewritten. §80 says what a conforming
// fresh genesis looks like; it does not authorize manufacturing one.
func validateFreshGenesisSlot(slot *CoreSlot, policies map[uint64]*SelectionPolicyVersion) error {
	switch slot.Status {
	case SlotStatus_SLOT_STATUS_PENDING:
		// Never activated: the whole activation generation is the zero sentinel.
		if slot.ActivationSequence != 0 || slot.ActivatedHeight != 0 || slot.ActivationEffectiveHeight != 0 {
			return ErrInvalidGenesis.Wrapf("pending slot %d must have zero activation sequence and heights", slot.SlotId)
		}
	case SlotStatus_SLOT_STATUS_ACTIVE:
		// The first generation. The exact height is pinned by the keeper against
		// the chain's initial height; what is decidable here is that the two
		// heights agree and are a real height rather than the never-activated
		// sentinel.
		if slot.ActivationSequence != 1 {
			return ErrInvalidGenesis.Wrapf("active slot %d must have activation sequence 1, has %d", slot.SlotId, slot.ActivationSequence)
		}
		if slot.ActivatedHeight < 1 || slot.ActivationEffectiveHeight != slot.ActivatedHeight {
			return ErrInvalidGenesis.Wrapf("active slot %d must have equal positive activated and activation-effective heights", slot.SlotId)
		}
	default:
		// Fresh genesis admits no other status. INACTIVE, SUSPENDED and REMOVED
		// describe history, and fresh genesis has none.
		return ErrInvalidGenesis.Wrapf("slot %d has status %s; fresh genesis admits only PENDING and ACTIVE", slot.SlotId, slot.Status)
	}
	// Settlement address is mandatory from normal V2.2 registration onward (§24),
	// so it is required for PENDING as well as ACTIVE. Syntax only here; the
	// economic rule needs app-derived state and runs in the keeper.
	if _, err := sdk.AccAddressFromBech32(slot.SettlementAddress); err != nil {
		return ErrInvalidGenesis.Wrapf("slot %d settlement address: %v", slot.SlotId, err)
	}
	if slot.CurrentSelectionPolicyVersion != 1 {
		return ErrInvalidGenesis.Wrapf("slot %d must point at policy version 1, points at %d", slot.SlotId, slot.CurrentSelectionPolicyVersion)
	}
	if slot.LastSelectionPolicyUpdateHeight != 0 {
		return ErrInvalidGenesis.Wrapf("slot %d must have no prior policy update, has height %d", slot.SlotId, slot.LastSelectionPolicyUpdateHeight)
	}
	policy, ok := policies[slot.SlotId]
	if !ok {
		return ErrInvalidGenesis.Wrapf("slot %d has no selection policy", slot.SlotId)
	}
	if policy.PolicyVersion != 1 {
		return ErrInvalidGenesis.Wrapf("slot %d policy must be version 1, is %d", slot.SlotId, policy.PolicyVersion)
	}
	if policy.ValidUntilHeightExclusive != 0 {
		return ErrInvalidGenesis.Wrapf("slot %d policy version 1 must be current, has validity end %d", slot.SlotId, policy.ValidUntilHeightExclusive)
	}
	if err := ValidateSelectionPolicyValues(policy.SelectionRateBps, policy.MaxSelectedParticipants); err != nil {
		return ErrInvalidGenesis.Wrapf("slot %d policy: %v", slot.SlotId, err)
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

// validatePendingAuthorityTransfers checks everything about an imported
// nomination that is decidable WITHOUT app-derived context.
//
// It exists because genesis tooling has to be able to reject a bad document
// before a node ever starts. `coreslot-genesis validate` and the module's own
// ValidateGenesis both run this; without it a document naming an unknown role, a
// malformed nominee or a negative height was reported as "a valid genesis file"
// and only refused later, during keeper import, after params had already been
// written to the in-memory InitChain cache.
//
// Module-account and bank-blocked admissibility is deliberately NOT checked here.
// Those sets are derived from the running application, and a types-level
// validator has no access to them; the keeper applies that rule in its genesis
// preflight, before any store write.
func (g GenesisState) validatePendingAuthorityTransfers() error {
	seen := make(map[AuthorityRole]struct{}, len(g.PendingAuthorityTransfers))
	for i, entry := range g.PendingAuthorityTransfers {
		if entry == nil {
			return fmt.Errorf("pending authority transfer %d is nil", i)
		}
		if entry.Transfer == nil {
			return fmt.Errorf("pending authority transfer %d for role %s has no transfer", i, entry.Role)
		}
		switch entry.Role {
		case AuthorityRole_AUTHORITY_ROLE_PRIMARY, AuthorityRole_AUTHORITY_ROLE_EMERGENCY:
		default:
			// Includes the unspecified zero value, which must never be defaulted:
			// an entry that omitted the field would otherwise become a pending
			// handover of the primary authority.
			return fmt.Errorf("pending authority transfer %d has invalid role %s", i, entry.Role)
		}
		// At most one per role. Two entries for the same role would make which
		// survives depend on iteration order, and only one can be the intended
		// successor.
		if _, duplicate := seen[entry.Role]; duplicate {
			return fmt.Errorf("duplicate pending authority transfer for role %s", entry.Role)
		}
		seen[entry.Role] = struct{}{}

		if _, err := sdk.AccAddressFromBech32(entry.Transfer.Nominee); err != nil {
			return fmt.Errorf("pending authority transfer for role %s has an invalid nominee: %w", entry.Role, err)
		}
		if entry.Transfer.NominatedHeight < 0 {
			return fmt.Errorf(
				"pending authority transfer for role %s has a negative nominated height %d",
				entry.Role, entry.Transfer.NominatedHeight)
		}
		// Runtime nomination refuses naming the incumbent, so a document produced
		// by a real chain cannot contain one. Rejecting it here keeps imported
		// state canonical with state the running chain can actually reach, rather
		// than admitting a pending handover that would change nothing.
		if entry.Transfer.Nominee == currentRoleHolder(*g.Params, entry.Role) {
			return fmt.Errorf(
				"pending authority transfer for role %s names the current holder", entry.Role)
		}
	}
	return nil
}

// currentRoleHolder returns the address holding a role in the given params.
func currentRoleHolder(params Params, role AuthorityRole) string {
	if role == AuthorityRole_AUTHORITY_ROLE_EMERGENCY {
		return params.EmergencyAuthority
	}
	return params.Authority
}
