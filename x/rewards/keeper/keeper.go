package keeper

import (
	"context"

	"cosmossdk.io/collections"
	storetypes "cosmossdk.io/core/store"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/internal/economicaddress"
	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

type AccountKeeper interface {
	GetModuleAddress(moduleName string) sdk.AccAddress
}

type BankKeeper interface {
	MintCoins(ctx context.Context, moduleName string, amounts sdk.Coins) error
	SendCoinsFromModuleToAccount(ctx context.Context, senderModule string, recipient sdk.AccAddress, amounts sdk.Coins) error
	GetSupply(ctx context.Context, denom string) sdk.Coin
	GetBalance(ctx context.Context, addr sdk.AccAddress, denom string) sdk.Coin
}

// CoreSlotKeeper is the narrow read interface x/rewards holds over x/coreslot.
//
// GetRewardWeight is deliberately absent. Weighted rewards are not part of V2:
// entitlement shares are participation-relative, so the weight had no remaining
// consumer once allocation stopped producing claim-shaped rows. Removing it
// narrows the cross-module surface rather than leaving a read that nothing acts
// on — the weight remains CoreSlot's own metadata.
type CoreSlotKeeper interface {
	GetActiveSlots(ctx context.Context) ([]coreslottypes.CoreSlot, error)
	GetSlot(ctx context.Context, slotID uint64) (coreslottypes.CoreSlot, error)
	GetAuthority(ctx context.Context) (string, error)
	GetEmergencyAuthority(ctx context.Context) (string, error)
}

type Keeper struct {
	cdc            codec.BinaryCodec
	accountKeeper  AccountKeeper
	bankKeeper     BankKeeper
	coreSlotKeeper CoreSlotKeeper

	// economicAddresses is the app-derived canonical rule for addresses that
	// receive value (§25). It is the same value x/coreslot holds, injected rather
	// than rebuilt, so the two modules cannot come to disagree about what a
	// payable address is.
	economicAddresses economicaddress.Validator

	Schema             collections.Schema
	Params             collections.Item[types.Params]
	PendingParams      collections.Item[types.Params]
	State              collections.Item[types.RewardsState]
	CurrentEpochConfig collections.Item[types.EpochConfigSnapshot]
	ActiveBlocks       collections.Map[collections.Pair[uint64, uint64], uint64]
	FinalizedEpochs    collections.Map[uint64, types.EpochReward]

	// EpochConfigVersions is the immutable epoch-configuration history keyed by
	// effective epoch, and the sole authority for epoch geometry.
	//
	// collections.Uint64Key is big-endian, so iteration is ascending by effective
	// epoch and a reverse iteration bounded above by N lands on the greatest
	// effective_epoch <= N. That predecessor seek is how a boundary is resolved:
	// history is never scanned in full, so resolution cost does not track chain
	// age.
	EpochConfigVersions collections.Map[uint64, types.EpochConfigVersion]

	// ScheduledEpochConfigs holds future epoch lengths keyed by the epoch at
	// which they become effective. A schedule entry is consumed at the first
	// BeginBlock of that epoch, which is where it becomes an immutable history
	// version. Fresh genesis carries none.
	ScheduledEpochConfigs collections.Map[uint64, types.ScheduledEpochConfig]

	// RewardConfigVersions is the immutable reward-configuration history keyed by
	// effective epoch, and the sole authority for the economics an epoch's
	// emission is computed under.
	//
	// Resolution is the same predecessor seek EpochConfigVersions uses, but bounded
	// by the TARGET's N-2 binding epoch rather than by the epoch itself: a target
	// binds the configuration that was already effective two epochs earlier, so
	// nothing accepted after that boundary can change what the target pays.
	RewardConfigVersions collections.Map[uint64, types.RewardConfigVersion]

	// RewardConfigVersionIndex maps a version number to the effective epoch its
	// history record is stored under.
	//
	// DERIVED, not authoritative. It carries no economics — only the key needed to
	// reach the record that does. Every lookup through it re-reads the canonical row
	// and requires the two to agree in both directions, so the index can make a
	// query fast but can never make it answer differently.
	RewardConfigVersionIndex collections.Map[uint64, uint64]

	// ScheduledRewardConfigs holds the single pending reward-configuration change,
	// keyed by the epoch it becomes effective at.
	//
	// Unlike ScheduledEpochConfigs this is not a queue: at most one entry exists
	// and its key is exactly current_epoch + 1. It is consumed in the closing
	// epoch's EndBlock, after that epoch's monetary finalization has succeeded —
	// NOT at BeginBlock, which is where the epoch-geometry schedule is consumed.
	ScheduledRewardConfigs collections.Map[uint64, types.ScheduledRewardConfig]

	// SlotEntitlements holds canonical finalized entitlements keyed
	// (epoch, slot_id).
	//
	// One collection, no secondary index. The key order makes the point read O(1)
	// and "one epoch, ascending slot_id" a bounded prefix range, which is every
	// access the architecture asks for. A by-epoch index would add a second write
	// per entitlement and a divergence class to detect, in exchange for no
	// reachable query.
	SlotEntitlements collections.Map[collections.Pair[uint64, uint64], types.SlotEntitlement]

	// OutstandingEntitlementLiability is the O(1) accumulator of unreleased
	// entitlement value, held as a canonical base-10 amount string.
	//
	// It exists because the definitional form — summing every live entitlement —
	// is a full scan, and solvency is asserted on a block path. The scan survives
	// as a test backstop, which is where an accumulator that drifted from the
	// records it summarizes would be caught.
	OutstandingEntitlementLiability collections.Item[string]

	// PauseState is the single canonical rewards-pause state.
	PauseState collections.Item[types.RewardsPauseState]

	// OpenRewardEnabledBlocks counts the reward-enabled blocks of the open epoch
	// and is the block-count input to epoch emission. It is reset when an epoch
	// opens and read at that epoch's finalization.
	OpenRewardEnabledBlocks collections.Item[uint64]
}

func NewKeeper(
	cdc codec.BinaryCodec,
	storeService storetypes.KVStoreService,
	accountKeeper AccountKeeper,
	bankKeeper BankKeeper,
	coreSlotKeeper CoreSlotKeeper,
	economicAddresses economicaddress.Validator,
) Keeper {
	sb := collections.NewSchemaBuilder(storeService)
	pairKey := collections.PairKeyCodec(collections.Uint64Key, collections.Uint64Key)
	k := Keeper{
		cdc:                cdc,
		accountKeeper:      accountKeeper,
		bankKeeper:         bankKeeper,
		coreSlotKeeper:     coreSlotKeeper,
		economicAddresses:  economicAddresses,
		Params:             collections.NewItem(sb, types.ParamsKey, "params", codec.CollValue[types.Params](cdc)),
		PendingParams:      collections.NewItem(sb, types.PendingParamsKey, "pending_params", codec.CollValue[types.Params](cdc)),
		State:              collections.NewItem(sb, types.StateKey, "state", codec.CollValue[types.RewardsState](cdc)),
		CurrentEpochConfig: collections.NewItem(sb, types.CurrentEpochConfigKey, "current_epoch_config", codec.CollValue[types.EpochConfigSnapshot](cdc)),
		ActiveBlocks:       collections.NewMap(sb, types.ActiveBlocksPrefix, "active_blocks", pairKey, collections.Uint64Value),
		FinalizedEpochs:    collections.NewMap(sb, types.FinalizedEpochsPrefix, "finalized_epochs", collections.Uint64Key, codec.CollValue[types.EpochReward](cdc)),
		EpochConfigVersions: collections.NewMap(sb, types.EpochConfigVersionsPrefix, "epoch_config_versions",
			collections.Uint64Key, codec.CollValue[types.EpochConfigVersion](cdc)),
		ScheduledEpochConfigs: collections.NewMap(sb, types.ScheduledEpochConfigsPrefix, "scheduled_epoch_configs",
			collections.Uint64Key, codec.CollValue[types.ScheduledEpochConfig](cdc)),
		RewardConfigVersions: collections.NewMap(sb, types.RewardConfigVersionsPrefix, "reward_config_versions",
			collections.Uint64Key, codec.CollValue[types.RewardConfigVersion](cdc)),
		RewardConfigVersionIndex: collections.NewMap(sb, types.RewardConfigVersionIndexPrefix,
			"reward_config_version_index", collections.Uint64Key, collections.Uint64Value),
		ScheduledRewardConfigs: collections.NewMap(sb, types.ScheduledRewardConfigsPrefix, "scheduled_reward_configs",
			collections.Uint64Key, codec.CollValue[types.ScheduledRewardConfig](cdc)),
		SlotEntitlements: collections.NewMap(sb, types.SlotEntitlementsPrefix, "slot_entitlements",
			pairKey, codec.CollValue[types.SlotEntitlement](cdc)),
		OutstandingEntitlementLiability: collections.NewItem(sb, types.OutstandingEntitlementLiabilityKey,
			"outstanding_entitlement_liability", collections.StringValue),
		PauseState:              collections.NewItem(sb, types.RewardsPauseStateKey, "pause_state", codec.CollValue[types.RewardsPauseState](cdc)),
		OpenRewardEnabledBlocks: collections.NewItem(sb, types.OpenRewardEnabledBlocksKey, "open_reward_enabled_blocks", collections.Uint64Value),
	}
	schema, err := sb.Build()
	if err != nil {
		panic(err)
	}
	k.Schema = schema
	return k
}
