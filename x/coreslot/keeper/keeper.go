package keeper

import (
	"context"
	"encoding/hex"
	"errors"

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

	// upgrades is the only route from this module to x/upgrade, and the only
	// route to x/upgrade at all: the upgrade module's own authority is a module
	// address nobody holds a key for, so its messages are unreachable by design
	// and this proxy is the whole surface.
	//
	// Nil in unit tests that do not exercise the upgrade path. The handlers
	// refuse rather than dereference it, so a keeper built without one fails
	// loudly at the message instead of panicking mid-block.
	upgrades types.UpgradeScheduler

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
	// PendingAuthority holds at most one nomination per role, keyed by
	// AuthorityRole. The key IS the role, which is why the stored value does not
	// repeat it: two copies could disagree, and only one of them could be right.
	PendingAuthority collections.Map[int32, types.PendingAuthorityTransfer]

	// SelectionPolicies is the immutable per-slot policy history keyed by
	// (slot_id, policy_version). Registration and fresh genesis create version 1;
	// a runtime policy update closes the current version and appends the next.
	// Closing an open version's exclusive end is the only write ever made to an
	// existing row — a closed version is immutable from then on.
	SelectionPolicies collections.Map[collections.Pair[uint64, uint64], types.SelectionPolicyVersion]

	// ActiveSlots is a membership-only index of ACTIVE slot IDs. It is a key set
	// with no value payload on purpose: CoreSlot stays the single authority for
	// slot data, so there is no duplicated field that could silently diverge. Its
	// only job is to make enumerating the active set O(A) instead of O(every slot
	// ever registered), which is what the architecture's workload closure needs.
	//
	// collections.Uint64Key is big-endian, so iteration is ascending slot ID.
	ActiveSlots collections.KeySet[uint64]

	// PolicyStarts is the Selection-policy seek index: (slot_id,
	// valid_from_height) -> policy_version. It is derived, rebuildable state over
	// SelectionPolicies, written wherever a version is created, and read only to
	// resolve the version applicable at a height.
	//
	// Both key components use order-preserving encodings, so a reverse iteration
	// bounded above by (slot_id, H) lands on the greatest valid_from_height <= H
	// for that slot and cannot cross into another slot's range.
	PolicyStarts collections.Map[collections.Pair[uint64, int64], uint64]
}

// NewKeeper builds the CoreSlot keeper. economicAddresses is required: an
// unconfigured validator rejects every address, so a caller that omits it fails
// loudly at the first registration rather than silently admitting module
// accounts as payees.
func NewKeeper(
	cdc codec.Codec,
	storeService storetypes.KVStoreService,
	economicAddresses economicaddress.Validator,
	upgrades types.UpgradeScheduler,
) Keeper {
	sb := collections.NewSchemaBuilder(storeService)
	k := Keeper{
		cdc:               cdc,
		economicAddresses: economicAddresses,
		upgrades:          upgrades,
		Params:            collections.NewItem(sb, collections.NewPrefix(types.ParamsKey), "params", codec.CollValue[types.Params](cdc)),
		Slots:             collections.NewMap(sb, collections.NewPrefix(types.SlotsPrefix), "slots", collections.Uint64Key, codec.CollValue[types.CoreSlot](cdc)),
		ByOperator:        collections.NewMap(sb, collections.NewPrefix(types.OperatorPrefix), "slot_by_operator", collections.StringKey, collections.Uint64Value),
		ByConsensus:       collections.NewMap(sb, collections.NewPrefix(types.ConsensusPrefix), "slot_by_consensus", collections.StringKey, collections.Uint64Value),
		Reserved:          collections.NewMap(sb, collections.NewPrefix(types.ReservedPrefix), "reserved_consensus", collections.StringKey, codec.CollValue[types.ReservedConsensusAddress](cdc)),
		Rotations:         collections.NewMap(sb, collections.NewPrefix(types.RotationsPrefix), "pending_rotations", collections.Uint64Key, codec.CollValue[types.PendingKeyRotation](cdc)),
		LastApplied:       collections.NewMap(sb, collections.NewPrefix(types.LastPrefix), "last_applied", collections.StringKey, codec.CollValue[types.LastAppliedValidator](cdc)),
		RewardWeights:     collections.NewMap(sb, collections.NewPrefix(types.RewardsPrefix), "reward_weights", collections.Uint64Key, codec.CollValue[types.OperatorRewardWeight](cdc)),
		NextSlotID:        collections.NewItem(sb, collections.NewPrefix(types.NextSlotIDKey), "next_slot_id", collections.Uint64Value),
		PendingAuthority: collections.NewMap(sb, collections.NewPrefix(types.PendingAuthorityPrefix), "pending_authority",
			collections.Int32Key, codec.CollValue[types.PendingAuthorityTransfer](cdc)),
		SelectionPolicies: collections.NewMap(sb, collections.NewPrefix(types.SelectionPoliciesPrefix), "selection_policies",
			collections.PairKeyCodec(collections.Uint64Key, collections.Uint64Key), codec.CollValue[types.SelectionPolicyVersion](cdc)),
		ActiveSlots: collections.NewKeySet(sb, collections.NewPrefix(types.ActiveSlotsPrefix), "active_slots", collections.Uint64Key),
		PolicyStarts: collections.NewMap(sb, collections.NewPrefix(types.PolicyStartsPrefix), "policy_starts",
			collections.PairKeyCodec(collections.Uint64Key, collections.Int64Key), collections.Uint64Value),
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

// getSlot reads a slot record, reporting a genuinely absent key as
// ErrSlotNotFound and propagating everything else unchanged.
//
// The distinction is the point. Only collections.ErrNotFound means "no such
// slot"; a decode failure, or any other storage error, means the key IS there
// and the stored bytes could not be read. Relabelling that as absence would tell
// a caller — and, through the query surface, the outside world — that a slot
// does not exist when in fact the database holding it is broken.
func (k Keeper) getSlot(ctx context.Context, id uint64) (types.CoreSlot, error) {
	slot, err := k.Slots.Get(ctx, id)
	if err != nil {
		if errors.Is(err, collections.ErrNotFound) {
			return types.CoreSlot{}, types.ErrSlotNotFound.Wrapf("%d", id)
		}
		return types.CoreSlot{}, err
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

// nextSlotID resolves the identifier a new registration will take, refusing to
// proceed on any counter state it cannot trust.
//
// There is deliberately NO fallback. A registration writes the slot record, both
// address indexes, the reward-weight row and the slot's version-1 Selection
// policy with unconditional Sets, so an identifier that is already in use does
// not fail — it OVERWRITES, reassigning a live slot to a different operator and
// destroying policy history §26 makes immutable. Substituting a default value for
// a counter that could not be read is therefore not a lenient recovery; it is the
// most destructive thing this handler can do, and it happens precisely when state
// is already known to be damaged.
//
// Every rejection below is unreachable from a conforming chain. Genesis admission
// requires next_slot_id to exceed every assigned identifier, and each registration
// advances it with a checked increment, so a counter that is absent, zero,
// unreadable or already-taken means state has been corrupted by something outside
// the module's own transitions. The only safe answer is to stop.
func (k Keeper) nextSlotID(ctx context.Context) (uint64, error) {
	id, err := k.NextSlotID.Get(ctx)
	if err != nil {
		// Absence and corruption are separated because they are different faults,
		// not because one of them is tolerable. Genesis always writes the counter,
		// so an absent key on a chain able to process a registration is itself
		// broken state — "start from 1" would be the same overwrite hazard wearing
		// the disguise of a fresh chain.
		if errors.Is(err, collections.ErrNotFound) {
			return 0, types.ErrInvalidTransition.Wrap(
				"the slot id counter is not set; genesis must establish it before any registration")
		}
		return 0, types.ErrInvalidTransition.Wrapf("the slot id counter could not be read: %v", err)
	}
	if id == 0 {
		return 0, types.ErrInvalidTransition.Wrap("the slot id counter is zero")
	}
	// Independent of the counter's own consistency: whatever it names must not
	// already exist. This is what keeps the overwrite closed even if some future
	// path hands out an identifier a healthy-looking counter should not have.
	//
	// Has checks key presence without decoding, so a slot whose stored record is
	// itself corrupt still registers as taken rather than reading as free.
	if taken, err := k.Slots.Has(ctx, id); err != nil {
		return 0, types.ErrInvalidTransition.Wrapf("slot id %d availability could not be determined: %v", id, err)
	} else if taken {
		return 0, types.ErrInvalidTransition.Wrapf("the slot id counter names slot %d, which already exists", id)
	}
	return id, nil
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
// responsible for reserving it under the normal rules. Returns the canceled
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
