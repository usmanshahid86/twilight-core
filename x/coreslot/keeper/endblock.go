package keeper

import (
	"context"
	"sort"

	abci "github.com/cometbft/cometbft/abci/types"

	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/x/coreslot/types"
)

func (k Keeper) EndBlock(ctx context.Context) ([]abci.ValidatorUpdate, error) {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	cacheCtx, write := sdkCtx.CacheContext()
	updates, err := k.endBlock(cacheCtx)
	if err != nil {
		// On error the cache (store writes AND cached events) is discarded: write()
		// is never called, so a failed EndBlock surfaces no events on the parent.
		return nil, err
	}
	// write() commits the cached store and, per cosmos-sdk v0.53.7
	// Context.CacheContext (types/context.go), already propagates the cached
	// EventManager's events onto the parent context exactly once. Do NOT re-emit
	// them here — that double-counted every EndBlock event (F3).
	write()
	return updates, nil
}

func (k Keeper) endBlock(ctx context.Context) ([]abci.ValidatorUpdate, error) {
	height := sdk.UnwrapSDKContext(ctx).BlockHeight()
	params, err := k.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	var due []types.PendingKeyRotation
	if err := k.Rotations.Walk(ctx, nil, func(_ uint64, rotation types.PendingKeyRotation) (bool, error) {
		if rotation.EffectiveHeight <= height {
			due = append(due, rotation)
		}
		return false, nil
	}); err != nil {
		return nil, err
	}
	sort.Slice(due, func(i, j int) bool { return due[i].SlotId < due[j].SlotId })
	for _, rotation := range due {
		slot, err := k.getSlot(ctx, rotation.SlotId)
		if err != nil {
			// Slot disappeared; drop the stale rotation without mutating state (F1).
			if err := k.dropStaleRotation(ctx, rotation); err != nil {
				return nil, err
			}
			continue
		}
		if slot.Status != types.SlotStatus_SLOT_STATUS_ACTIVE {
			// Lifecycle change should have cancelled this already; never mutate a
			// non-active (in particular REMOVED) slot from a stale rotation (F1).
			if err := k.dropStaleRotation(ctx, rotation); err != nil {
				return nil, err
			}
			continue
		}
		oldKey, oldAddr, err := consensusKey(rotation.OldPubkey)
		if err != nil {
			return nil, err
		}
		newKey, _, err := consensusKey(rotation.NewPubkey)
		if err != nil {
			return nil, err
		}
		slot.ConsensusPubkey, slot.UpdatedHeight = rotation.NewPubkey, height
		if err := k.Slots.Set(ctx, slot.SlotId, slot); err != nil {
			return nil, err
		}
		_ = k.ByConsensus.Remove(ctx, oldKey)
		if err := k.Reserved.Set(ctx, oldKey, types.ReservedConsensusAddress{
			ConsAddress: oldAddr, SlotId: slot.SlotId, ReservedUntil: height + int64(params.ConsensusKeyReuseLockout), Reason: "key rotation",
		}); err != nil {
			return nil, err
		}
		if err := k.Rotations.Remove(ctx, rotation.SlotId); err != nil {
			return nil, err
		}
		emitKeyRotated(ctx, slot.SlotId, slot.OperatorAddress, oldKey, newKey, slot.ConsensusPower, rotation.EffectiveHeight)
	}
	return k.diffAndPersist(ctx)
}

// dropStaleRotation cancels a rotation whose slot is no longer eligible, freeing
// the staged new consensus key (which was never active) and emitting a cancel
// event (F1).
func (k Keeper) dropStaleRotation(ctx context.Context, rotation types.PendingKeyRotation) error {
	oldKey, _, err := consensusKey(rotation.OldPubkey)
	if err != nil {
		return err
	}
	newKey, _, err := consensusKey(rotation.NewPubkey)
	if err != nil {
		return err
	}
	if err := k.ByConsensus.Remove(ctx, newKey); err != nil {
		return err
	}
	if err := k.Rotations.Remove(ctx, rotation.SlotId); err != nil {
		return err
	}
	height := sdk.UnwrapSDKContext(ctx).BlockHeight()
	emitRotationCancelled(ctx, rotation.SlotId, k.operatorForSlot(ctx, rotation.SlotId), oldKey, newKey, types.RotationCancelReasonStale, height)
	return nil
}

// operatorForSlot resolves a slot's operator address for event enrichment,
// returning "" if the slot row is absent (slots are terminal, not deleted, so
// this is normally present even for REMOVED slots).
func (k Keeper) operatorForSlot(ctx context.Context, slotID uint64) string {
	slot, err := k.Slots.Get(ctx, slotID)
	if err != nil {
		return ""
	}
	return slot.OperatorAddress
}

func (k Keeper) diffAndPersist(ctx context.Context) ([]abci.ValidatorUpdate, error) {
	desired := map[string]types.LastAppliedValidator{}
	if err := k.Slots.Walk(ctx, nil, func(_ uint64, slot types.CoreSlot) (bool, error) {
		if slot.Status != types.SlotStatus_SLOT_STATUS_ACTIVE {
			return false, nil
		}
		key, _, err := consensusKey(slot.ConsensusPubkey)
		if err != nil {
			return true, err
		}
		if _, exists := desired[key]; exists {
			return true, types.ErrDuplicateConsensusKey
		}
		desired[key] = types.LastAppliedValidator{SlotId: slot.SlotId, ConsensusPubkey: slot.ConsensusPubkey, Power: slot.ConsensusPower}
		return false, nil
	}); err != nil {
		return nil, err
	}

	last := map[string]types.LastAppliedValidator{}
	if err := k.LastApplied.Walk(ctx, nil, func(key string, validator types.LastAppliedValidator) (bool, error) {
		last[key] = validator
		return false, nil
	}); err != nil {
		return nil, err
	}

	// One entry per consensus address (F4): the diff is computed over the union
	// of last-applied and desired keys so a given address yields at most one
	// update. When an address persists with a changed power we emit a single
	// update at the new power; we never emit old@0 + new@power for the same key.
	type keyedUpdate struct {
		key    string
		slotID uint64
		power  int64
		update abci.ValidatorUpdate
	}
	keys := make(map[string]struct{}, len(last)+len(desired))
	for key := range last {
		keys[key] = struct{}{}
	}
	for key := range desired {
		keys[key] = struct{}{}
	}
	var keyed []keyedUpdate
	for key := range keys {
		next, inDesired := desired[key]
		old, inLast := last[key]
		switch {
		case inDesired && inLast:
			if next.Power == old.Power {
				continue
			}
			update, err := validatorUpdate(next.ConsensusPubkey, next.Power)
			if err != nil {
				return nil, err
			}
			keyed = append(keyed, keyedUpdate{key: key, slotID: next.SlotId, power: next.Power, update: update})
		case inDesired:
			update, err := validatorUpdate(next.ConsensusPubkey, next.Power)
			if err != nil {
				return nil, err
			}
			keyed = append(keyed, keyedUpdate{key: key, slotID: next.SlotId, power: next.Power, update: update})
		default: // inLast only -> validator left the active set
			update, err := validatorUpdate(old.ConsensusPubkey, 0)
			if err != nil {
				return nil, err
			}
			keyed = append(keyed, keyedUpdate{key: key, slotID: old.SlotId, power: 0, update: update})
		}
	}
	sort.Slice(keyed, func(i, j int) bool { return keyed[i].key < keyed[j].key })

	// Explicit duplicate-address guard before returning (F4): defends against any
	// future change that could produce two updates for the same consensus address,
	// which CometBFT would reject as a duplicate changeset entry.
	for i := 1; i < len(keyed); i++ {
		if keyed[i].key == keyed[i-1].key {
			return nil, types.ErrDuplicateConsensusKey
		}
	}

	height := sdk.UnwrapSDKContext(ctx).BlockHeight()
	updates := make([]abci.ValidatorUpdate, len(keyed))
	for i := range keyed {
		updates[i] = keyed[i].update
		emitValidatorUpdateEmitted(ctx, keyed[i].slotID, k.operatorForSlot(ctx, keyed[i].slotID), keyed[i].key, keyed[i].power, height)
	}
	for key := range last {
		if err := k.LastApplied.Remove(ctx, key); err != nil {
			return nil, err
		}
	}
	for key, value := range desired {
		if err := k.LastApplied.Set(ctx, key, value); err != nil {
			return nil, err
		}
	}
	return updates, nil
}

func validatorUpdate(any interface {
	GetTypeUrl() string
	GetValue() []byte
}, power int64) (abci.ValidatorUpdate, error) {
	pk, err := decodePubKey(any)
	if err != nil {
		return abci.ValidatorUpdate{}, err
	}
	protoKey, err := cryptocodec.ToCmtProtoPublicKey(pk)
	if err != nil {
		return abci.ValidatorUpdate{}, err
	}
	return abci.ValidatorUpdate{PubKey: protoKey, Power: power}, nil
}
