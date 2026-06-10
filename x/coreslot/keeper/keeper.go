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

	"github.com/nyks/nyks-core/x/coreslot/types"
)

type Keeper struct {
	cdc codec.Codec

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
}

func NewKeeper(cdc codec.Codec, storeService storetypes.KVStoreService) Keeper {
	sb := collections.NewSchemaBuilder(storeService)
	k := Keeper{
		cdc:           cdc,
		Params:        collections.NewItem(sb, collections.NewPrefix(types.ParamsKey), "params", codec.CollValue[types.Params](cdc)),
		Slots:         collections.NewMap(sb, collections.NewPrefix(types.SlotsPrefix), "slots", collections.Uint64Key, codec.CollValue[types.CoreSlot](cdc)),
		ByOperator:    collections.NewMap(sb, collections.NewPrefix(types.OperatorPrefix), "slot_by_operator", collections.StringKey, collections.Uint64Value),
		ByConsensus:   collections.NewMap(sb, collections.NewPrefix(types.ConsensusPrefix), "slot_by_consensus", collections.StringKey, collections.Uint64Value),
		Reserved:      collections.NewMap(sb, collections.NewPrefix(types.ReservedPrefix), "reserved_consensus", collections.StringKey, codec.CollValue[types.ReservedConsensusAddress](cdc)),
		Rotations:     collections.NewMap(sb, collections.NewPrefix(types.RotationsPrefix), "pending_rotations", collections.Uint64Key, codec.CollValue[types.PendingKeyRotation](cdc)),
		LastApplied:   collections.NewMap(sb, collections.NewPrefix(types.LastPrefix), "last_applied", collections.StringKey, codec.CollValue[types.LastAppliedValidator](cdc)),
		RewardWeights: collections.NewMap(sb, collections.NewPrefix(types.RewardsPrefix), "reward_weights", collections.Uint64Key, codec.CollValue[types.OperatorRewardWeight](cdc)),
		NextSlotID:    collections.NewItem(sb, collections.NewPrefix(types.NextSlotIDKey), "next_slot_id", collections.Uint64Value),
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

func (k Keeper) activeCount(ctx context.Context) (uint64, error) {
	var count uint64
	err := k.Slots.Walk(ctx, nil, func(_ uint64, slot types.CoreSlot) (bool, error) {
		if slot.Status == types.SlotStatus_SLOT_STATUS_ACTIVE {
			count++
		}
		return false, nil
	})
	return count, err
}

func (k Keeper) getSlot(ctx context.Context, id uint64) (types.CoreSlot, error) {
	slot, err := k.Slots.Get(ctx, id)
	if err != nil {
		return types.CoreSlot{}, types.ErrSlotNotFound.Wrapf("%d", id)
	}
	return slot, nil
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
