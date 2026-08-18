package keeper

import (
	"context"

	"cosmossdk.io/collections"
	storetypes "cosmossdk.io/core/store"

	"github.com/cosmos/cosmos-sdk/codec"

	"github.com/twilight-project/twilight-core/internal/economicaddress"
	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	"github.com/twilight-project/twilight-core/x/mining/types"
	rewardstypes "github.com/twilight-project/twilight-core/x/rewards/types"
)

// CoreSlotKeeper is the narrow read interface x/mining holds over x/coreslot.
//
// GetSlot supplies the settlement address that authorizes a Slot's settlement
// transactions; SelectionPolicyAtHeight and GetActiveSlots exist for the
// genesis cross-check that every ACTIVE Slot's policy satisfies the initial
// Selection parameters.
//
// The direction is fixed and one-way: x/mining reads x/coreslot, never the
// reverse.
type CoreSlotKeeper interface {
	GetSlot(ctx context.Context, slotID uint64) (coreslottypes.CoreSlot, error)
	GetActiveSlots(ctx context.Context) ([]coreslottypes.CoreSlot, error)
	SelectionPolicyAtHeight(ctx context.Context, slotID uint64, height int64) (coreslottypes.SelectionPolicyVersion, error)
}

// RewardsKeeper is the narrow interface x/mining holds over x/rewards.
//
// Every method that moves value lives here rather than in x/mining, and that is
// the point: this module owns the settlement workflow and owns no money. The
// entitlement amount, the released amount, the payout destination and the escrow
// are all x/rewards state, and the two release methods are the only ways value
// leaves it.
//
// Deliberately absent: any bank or account keeper. x/mining cannot mint, cannot
// send, and cannot read a module account, because it holds nothing that could.
// That is a structural guarantee rather than a rule to remember.
//
// This gate declares the interface and the read half is not yet consumed; the
// settlement clock, materialization and message gates supply the callers.
type RewardsKeeper interface {
	GetFinalizedEpoch(ctx context.Context, epoch uint64) (rewardstypes.EpochReward, bool, error)
	IterateEntitlementsForEpoch(ctx context.Context, epoch uint64) ([]rewardstypes.SlotEntitlement, error)
	GetSlotEntitlement(ctx context.Context, slotID, epoch uint64) (rewardstypes.SlotEntitlement, bool, error)
	EpochStartHeight(ctx context.Context, epoch uint64) (uint64, error)
	EpochEndHeight(ctx context.Context, epoch uint64) (uint64, error)
	EpochLengthForEpoch(ctx context.Context, epoch uint64) (uint64, error)
	SettlementReleaseEnabled(ctx context.Context) (bool, error)
	PayEntitlement(ctx context.Context, slotID, epoch uint64, payouts []rewardstypes.EntitlementPayout) error
	PayEntitlementRemainderToOperator(ctx context.Context, slotID, epoch uint64) error
}

type Keeper struct {
	cdc            codec.BinaryCodec
	coreSlotKeeper CoreSlotKeeper
	rewardsKeeper  RewardsKeeper

	// economicAddresses is the app-derived canonical rule for addresses that
	// receive value (§25). It is a plain value, not a keeper: x/mining gains no
	// dependency on bank or auth by holding it, and it is the same rule
	// x/coreslot and x/rewards apply, injected rather than rebuilt so the three
	// cannot come to disagree about what a payable address is.
	economicAddresses economicaddress.Validator

	Schema collections.Schema

	// DistributionModeVersions is the immutable chain-wide distribution-mode
	// history keyed by the epoch a version becomes valid from.
	//
	// Unlike the two parameter histories it stores a validity INTERVAL. That shape
	// is canonical and is not flattened into an effective-epoch record: resolution
	// helpers are generic over the key so each family keeps its own semantics.
	DistributionModeVersions collections.Map[uint64, types.MiningDistributionModeVersion]
	// ScheduledDistributionMode holds the single pending mode change, keyed by its
	// effective epoch. This profile exposes no update transaction, so ordinary
	// execution never writes it; the promotion primitive that consumes it is
	// production-shaped and fully implemented regardless.
	ScheduledDistributionMode collections.Map[uint64, types.ScheduledMiningDistributionMode]
	// DistributionModeVersionIndex is DERIVED: version -> valid_from_epoch.
	DistributionModeVersionIndex collections.Map[uint64, uint64]

	// SelectionParamsVersions is the immutable global Selection-parameter history
	// keyed by effective epoch.
	SelectionParamsVersions collections.Map[uint64, types.SelectionParamsVersion]
	// ScheduledSelectionParams holds the single pending change. No writer here.
	ScheduledSelectionParams collections.Map[uint64, types.ScheduledSelectionParams]
	// SelectionParamsVersionIndex is DERIVED: version -> effective_epoch.
	SelectionParamsVersionIndex collections.Map[uint64, uint64]

	// SettlementParamsVersions is the immutable settlement-validity parameter
	// history keyed by effective epoch, and the sole authority for what a target
	// may pay and how long it has to pay it.
	SettlementParamsVersions collections.Map[uint64, types.SettlementParamsVersion]
	// ScheduledSettlementParams holds the single pending change. No writer here.
	ScheduledSettlementParams collections.Map[uint64, types.ScheduledSettlementParams]
	// SettlementParamsVersionIndex is DERIVED: version -> effective_epoch.
	SettlementParamsVersionIndex collections.Map[uint64, uint64]

	// SettlementClock is the canonical monotonic settlement clock. It is not a
	// block height: it advances only on blocks whose beginning-of-block pause
	// state permits settlement release.
	SettlementClock collections.Item[uint64]
	// LastProcessedRewardEpoch is the materialization cursor.
	LastProcessedRewardEpoch collections.Item[uint64]

	// SettlementEpochAnchors holds one anchor per epoch that produced settlements.
	SettlementEpochAnchors collections.Map[uint64, types.SettlementEpochAnchor]

	// Settlements holds canonical settlement workflow rows keyed (slot_id, epoch).
	Settlements collections.Map[collections.Pair[uint64, uint64], types.Settlement]

	// OpenSettlementsBySlot is DERIVED indexing state over the OPEN subset.
	//
	// It carries no settlement content — the value is a presence marker — and it
	// never decides whether a settlement is open. It exists so that listing a
	// Slot's outstanding work costs the outstanding rows rather than that Slot's
	// entire finalized history.
	OpenSettlementsBySlot collections.Map[collections.Pair[uint64, uint64], uint64]
}

func NewKeeper(
	cdc codec.BinaryCodec,
	storeService storetypes.KVStoreService,
	coreSlotKeeper CoreSlotKeeper,
	rewardsKeeper RewardsKeeper,
	economicAddresses economicaddress.Validator,
) Keeper {
	sb := collections.NewSchemaBuilder(storeService)
	pairKey := collections.PairKeyCodec(collections.Uint64Key, collections.Uint64Key)
	k := Keeper{
		cdc:               cdc,
		coreSlotKeeper:    coreSlotKeeper,
		rewardsKeeper:     rewardsKeeper,
		economicAddresses: economicAddresses,
		DistributionModeVersions: collections.NewMap(sb, types.DistributionModeVersionsPrefix,
			"distribution_mode_versions", collections.Uint64Key,
			codec.CollValue[types.MiningDistributionModeVersion](cdc)),
		ScheduledDistributionMode: collections.NewMap(sb, types.ScheduledDistributionModePrefix,
			"scheduled_distribution_mode", collections.Uint64Key,
			codec.CollValue[types.ScheduledMiningDistributionMode](cdc)),
		SelectionParamsVersions: collections.NewMap(sb, types.SelectionParamsVersionsPrefix,
			"selection_params_versions", collections.Uint64Key,
			codec.CollValue[types.SelectionParamsVersion](cdc)),
		DistributionModeVersionIndex: collections.NewMap(sb, types.DistributionModeVersionIndexPrefix,
			"distribution_mode_version_index", collections.Uint64Key, collections.Uint64Value),
		SelectionParamsVersionIndex: collections.NewMap(sb, types.SelectionParamsVersionIndexPrefix,
			"selection_params_version_index", collections.Uint64Key, collections.Uint64Value),
		SettlementParamsVersionIndex: collections.NewMap(sb, types.SettlementParamsVersionIndexPrefix,
			"settlement_params_version_index", collections.Uint64Key, collections.Uint64Value),
		ScheduledSelectionParams: collections.NewMap(sb, types.ScheduledSelectionParamsPrefix,
			"scheduled_selection_params", collections.Uint64Key,
			codec.CollValue[types.ScheduledSelectionParams](cdc)),
		SettlementParamsVersions: collections.NewMap(sb, types.SettlementParamsVersionsPrefix,
			"settlement_params_versions", collections.Uint64Key,
			codec.CollValue[types.SettlementParamsVersion](cdc)),
		ScheduledSettlementParams: collections.NewMap(sb, types.ScheduledSettlementParamsPrefix,
			"scheduled_settlement_params", collections.Uint64Key,
			codec.CollValue[types.ScheduledSettlementParams](cdc)),
		SettlementClock: collections.NewItem(sb, types.SettlementClockKey,
			"settlement_clock", collections.Uint64Value),
		LastProcessedRewardEpoch: collections.NewItem(sb, types.LastProcessedRewardEpochKey,
			"last_processed_reward_epoch", collections.Uint64Value),
		SettlementEpochAnchors: collections.NewMap(sb, types.SettlementEpochAnchorsPrefix,
			"settlement_epoch_anchors", collections.Uint64Key,
			codec.CollValue[types.SettlementEpochAnchor](cdc)),
		Settlements: collections.NewMap(sb, types.SettlementsPrefix, "settlements",
			pairKey, codec.CollValue[types.Settlement](cdc)),
		OpenSettlementsBySlot: collections.NewMap(sb, types.OpenSettlementsBySlotPrefix,
			"open_settlements_by_slot", pairKey, collections.Uint64Value),
	}
	schema, err := sb.Build()
	if err != nil {
		panic(err)
	}
	k.Schema = schema
	return k
}
