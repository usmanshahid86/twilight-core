package keeper

import (
	"context"
	"encoding/hex"

	"cosmossdk.io/collections"
	storetypes "cosmossdk.io/core/store"

	"github.com/cosmos/cosmos-sdk/codec"
	sdked25519 "github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	gogoproto "github.com/cosmos/gogoproto/proto"

	"github.com/twilight-project/twilight-core/internal/economicaddress"
	"github.com/twilight-project/twilight-core/x/coreslot/types"
)

type Keeper struct {
	cdc codec.Codec

	// economicAddresses is the app-derived canonical rule for addresses that
	// receive value (§25). It is a plain value, not a keeper: x/coreslot must
	// gain no dependency on bank, auth, x/rewards or a future x/mining, and the
	// keeper DAG is unchanged by holding it.
	economicAddresses economicaddress.Validator

	Schema        collections.Schema
	Params        collections.Item[types.Params]
	Slots         collections.Map[uint64, types.CoreSlot]
	ByOperator    collections.Map[string, uint64]
	ByConsensus   collections.Map[string, uint64]
	Reserved      collections.Map[string, types.ReservedConsensusAddress]
	Rotations     collections.Map[uint64, types.PendingKeyRotation]
	LastApplied   collections.Map[string, types.LastAppliedValidator]
	RewardWeights collections.Map[uint64, types.OperatorRewardWeight]
	NextSlotID    collections.Item[uint64]

	// SelectionPolicies is the immutable per-slot policy history keyed by
	// (slot_id, policy_version). PR4 writes version 1 only and never supersedes
	// it; runtime updates belong to a later change.
	SelectionPolicies collections.Map[collections.Pair[uint64, uint64], types.SelectionPolicyVersion]

	// ActiveSlots is a membership-only index of ACTIVE slot IDs. It is a key set
	// with no value payload on purpose: CoreSlot stays the single authority for
	// slot data, so there is no duplicated field that could silently diverge. Its
	// only job is to make enumerating the active set O(A) instead of O(every slot
	// ever registered), which is what the architecture's workload closure needs.
	//
	// collections.Uint64Key is big-endian, so iteration is ascending slot ID.
	ActiveSlots collections.KeySet[uint64]
}

// NewKeeper builds the CoreSlot keeper. economicAddresses is required: an
// unconfigured validator rejects every address, so a caller that omits it fails
// loudly at the first registration rather than silently admitting module
// accounts as payees.
func NewKeeper(cdc codec.Codec, storeService storetypes.KVStoreService, economicAddresses economicaddress.Validator) Keeper {
	sb := collections.NewSchemaBuilder(storeService)
	k := Keeper{
		cdc:               cdc,
		economicAddresses: economicAddresses,
		Params:            collections.NewItem(sb, collections.NewPrefix(types.ParamsKey), "params", codec.CollValue[types.Params](cdc)),
		Slots:             collections.NewMap(sb, collections.NewPrefix(types.SlotsPrefix), "slots", collections.Uint64Key, codec.CollValue[types.CoreSlot](cdc)),
		ByOperator:        collections.NewMap(sb, collections.NewPrefix(types.OperatorPrefix), "slot_by_operator", collections.StringKey, collections.Uint64Value),
		ByConsensus:       collections.NewMap(sb, collections.NewPrefix(types.ConsensusPrefix), "slot_by_consensus", collections.StringKey, collections.Uint64Value),
		Reserved:          collections.NewMap(sb, collections.NewPrefix(types.ReservedPrefix), "reserved_consensus", collections.StringKey, codec.CollValue[types.ReservedConsensusAddress](cdc)),
		Rotations:         collections.NewMap(sb, collections.NewPrefix(types.RotationsPrefix), "pending_rotations", collections.Uint64Key, codec.CollValue[types.PendingKeyRotation](cdc)),
		LastApplied:       collections.NewMap(sb, collections.NewPrefix(types.LastPrefix), "last_applied", collections.StringKey, codec.CollValue[types.LastAppliedValidator](cdc)),
		RewardWeights:     collections.NewMap(sb, collections.NewPrefix(types.RewardsPrefix), "reward_weights", collections.Uint64Key, codec.CollValue[types.OperatorRewardWeight](cdc)),
		NextSlotID:        collections.NewItem(sb, collections.NewPrefix(types.NextSlotIDKey), "next_slot_id", collections.Uint64Value),
		SelectionPolicies: collections.NewMap(sb, collections.NewPrefix(types.SelectionPoliciesPrefix), "selection_policies",
			collections.PairKeyCodec(collections.Uint64Key, collections.Uint64Key), codec.CollValue[types.SelectionPolicyVersion](cdc)),
		ActiveSlots: collections.NewKeySet(sb, collections.NewPrefix(types.ActiveSlotsPrefix), "active_slots", collections.Uint64Key),
	}
	schema, err := sb.Build()
	if err != nil {
		panic(err)
	}
	k.Schema = schema
	return k
}

func decodePubKey(any interface {
	GetTypeUrl() string
	GetValue() []byte
}) (cryptotypes.PubKey, error) {
	if any == nil || any.GetTypeUrl() != "/cosmos.crypto.ed25519.PubKey" {
		return nil, types.ErrInvalidPubKey.Wrap("only CometBFT ed25519 keys are supported")
	}
	var pk sdked25519.PubKey
	if err := gogoproto.Unmarshal(any.GetValue(), &pk); err != nil {
		return nil, types.ErrInvalidPubKey.Wrap(err.Error())
	}
	if len(pk.Key) != sdked25519.PubKeySize {
		return nil, types.ErrInvalidPubKey.Wrapf("expected %d bytes", sdked25519.PubKeySize)
	}
	return &pk, nil
}

func DecodePubKey(any interface {
	GetTypeUrl() string
	GetValue() []byte
}) (cryptotypes.PubKey, error) {
	return decodePubKey(any)
}

func consensusKey(any interface {
	GetTypeUrl() string
	GetValue() []byte
}) (string, []byte, error) {
	pk, err := decodePubKey(any)
	if err != nil {
		return "", nil, err
	}
	addr := pk.Address().Bytes()
	return hex.EncodeToString(addr), addr, nil
}

// activeCount returns the number of ACTIVE slots.
//
// It walks the ActiveSlot membership index, so its cost is proportional to the
// active set and is bounded by HardMaxActiveCoreSlots. It must never be
// reimplemented as a scan over Slots: the registered population grows without
// bound over the chain's lifetime while the active set does not, and a consensus
// path whose cost tracks lifetime history breaks the architecture's workload
// closure. The same rule applies to GetActiveSlots and activeSlotIDs below.
func (k Keeper) activeCount(ctx context.Context) (uint64, error) {
	var count uint64
	err := k.ActiveSlots.Walk(ctx, nil, func(_ uint64) (bool, error) {
		count++
		return false, nil
	})
	if err != nil {
		return 0, err
	}
	return count, nil
}

// activeSlotIDs returns the ACTIVE slot IDs in ascending order. Ascending order
// is the pinned enumeration contract, not an incidental property: it comes from
// the index's big-endian key encoding rather than from sorting a set collected in
// some other order.
func (k Keeper) activeSlotIDs(ctx context.Context) ([]uint64, error) {
	ids := make([]uint64, 0)
	if err := k.ActiveSlots.Walk(ctx, nil, func(id uint64) (bool, error) {
		ids = append(ids, id)
		return false, nil
	}); err != nil {
		return nil, err
	}
	return ids, nil
}

// setSlotActive records ACTIVE membership for a slot. Callers must invoke it in
// the same state transition that writes the ACTIVE CoreSlot row.
func (k Keeper) setSlotActive(ctx context.Context, slotID uint64) error {
	return k.ActiveSlots.Set(ctx, slotID)
}

// clearSlotActive drops ACTIVE membership for a slot. Removing an absent key is
// not an error, so this is safe on a transition out of a non-active status.
func (k Keeper) clearSlotActive(ctx context.Context, slotID uint64) error {
	return k.ActiveSlots.Remove(ctx, slotID)
}

func (k Keeper) getSlot(ctx context.Context, id uint64) (types.CoreSlot, error) {
	slot, err := k.Slots.Get(ctx, id)
	if err != nil {
		return types.CoreSlot{}, types.ErrSlotNotFound.Wrapf("%d", id)
	}
	return slot, nil
}

// GetActiveSlots returns the active CoreSlot rows in ascending slot ID order.
// It is a read-only module integration surface; validator-set ownership remains
// entirely inside x/coreslot.
//
// Rows come from the ActiveSlot index and are then read from Slots, which stays
// authoritative for slot data. There is deliberately no fallback scan: a missing
// row for an indexed ID means index and record have diverged, which is a broken
// invariant rather than a condition to paper over, so it fails closed.
func (k Keeper) GetActiveSlots(ctx context.Context) ([]types.CoreSlot, error) {
	ids, err := k.activeSlotIDs(ctx)
	if err != nil {
		return nil, err
	}
	slots := make([]types.CoreSlot, 0, len(ids))
	for _, id := range ids {
		slot, err := k.Slots.Get(ctx, id)
		if err != nil {
			return nil, types.ErrInvalidGenesis.Wrapf("active index references missing slot %d", id)
		}
		if slot.Status != types.SlotStatus_SLOT_STATUS_ACTIVE {
			return nil, types.ErrInvalidTransition.Wrapf("active index references slot %d with status %s", id, slot.Status)
		}
		slots = append(slots, slot)
	}
	return slots, nil
}

// GetSlot returns a CoreSlot row without exposing collection internals.
func (k Keeper) GetSlot(ctx context.Context, slotID uint64) (types.CoreSlot, error) {
	return k.getSlot(ctx, slotID)
}

// GetRewardWeight returns the stored reward-weight row for a slot. Absence is
// returned as an error rather than silently synthesizing economic state.
func (k Keeper) GetRewardWeight(ctx context.Context, slotID uint64) (types.OperatorRewardWeight, error) {
	return k.RewardWeights.Get(ctx, slotID)
}

// GetAuthority returns the current normal authority from CoreSlot params.
func (k Keeper) GetAuthority(ctx context.Context) (string, error) {
	params, err := k.Params.Get(ctx)
	return params.Authority, err
}

// GetEmergencyAuthority returns the current emergency authority from CoreSlot
// params.
func (k Keeper) GetEmergencyAuthority(ctx context.Context) (string, error) {
	params, err := k.Params.Get(ctx)
	return params.EmergencyAuthority, err
}

func (k Keeper) ensureConsensusAvailable(ctx context.Context, any interface {
	GetTypeUrl() string
	GetValue() []byte
}) (string, []byte, error) {
	key, address, err := consensusKey(any)
	if err != nil {
		return "", nil, err
	}
	if has, err := k.ByConsensus.Has(ctx, key); err != nil {
		return "", nil, err
	} else if has {
		return "", nil, types.ErrDuplicateConsensusKey
	}
	if has, err := k.Reserved.Has(ctx, key); err != nil {
		return "", nil, err
	} else if has {
		reservation, err := k.Reserved.Get(ctx, key)
		if err != nil {
			return "", nil, err
		}
		if reservation.ReservedUntil > sdk.UnwrapSDKContext(ctx).BlockHeight() {
			return "", nil, types.ErrDuplicateConsensusKey
		}
		if err := k.Reserved.Remove(ctx, key); err != nil {
			return "", nil, err
		}
	}
	return key, address, nil
}

// cancelPendingRotation removes any staged rotation for the slot (F1). The
// staged new consensus key was registered in ByConsensus at request time but
// was never active, so it is released (removed) rather than reserved. The
// slot's current/old key is left in place; the caller's lifecycle transition is
// responsible for reserving it under the normal rules. Returns the cancelled
// rotation and true when one existed.
func (k Keeper) cancelPendingRotation(ctx context.Context, slotID uint64) (types.PendingKeyRotation, bool, error) {
	rotation, err := k.Rotations.Get(ctx, slotID)
	if err != nil {
		// collections returns an error when the key is absent; treat as "none".
		return types.PendingKeyRotation{}, false, nil
	}
	newKey, _, err := consensusKey(rotation.NewPubkey)
	if err != nil {
		return types.PendingKeyRotation{}, false, err
	}
	if err := k.ByConsensus.Remove(ctx, newKey); err != nil {
		return types.PendingKeyRotation{}, false, err
	}
	if err := k.Rotations.Remove(ctx, slotID); err != nil {
		return types.PendingKeyRotation{}, false, err
	}
	return rotation, true, nil
}
