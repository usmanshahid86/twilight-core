package types

import "cosmossdk.io/collections"

const (
	ModuleName   = "rewards"
	StoreKey     = ModuleName
	RouterKey    = ModuleName
	QuerierRoute = ModuleName

	FeePoolName = "rewards_fee_pool"
)

// Durable store-prefix ledger. A prefix byte is permanent: once assigned it is
// never recycled for different state, even if the collection it named is later
// removed or loses authority. Add new prefixes at the end and never renumber.
var (
	ParamsKey        = collections.NewPrefix(0x01)
	PendingParamsKey = collections.NewPrefix(0x02)
	StateKey         = collections.NewPrefix(0x03)
	// CurrentEpochConfigKey still holds the epoch configuration snapshot, but the
	// snapshot is no longer the epoch-geometry authority: EpochConfigVersions is.
	// It survives for the non-geometry economics the finalizer still reads.
	CurrentEpochConfigKey = collections.NewPrefix(0x04)
	ActiveBlocksPrefix    = collections.NewPrefix(0x05)
	FinalizedEpochsPrefix = collections.NewPrefix(0x06)
	ClaimRecordsPrefix    = collections.NewPrefix(0x07)

	// EpochConfigVersionsPrefix holds the immutable epoch-configuration history
	// keyed by effective epoch. This is the sole authority for epoch geometry.
	EpochConfigVersionsPrefix = collections.NewPrefix(0x08)
	// ScheduledEpochConfigsPrefix holds future epoch lengths keyed by the epoch
	// they become effective at. Values are lengths, never precomputed heights.
	ScheduledEpochConfigsPrefix = collections.NewPrefix(0x09)
	// RewardsPauseStateKey holds the single canonical rewards-pause state.
	RewardsPauseStateKey = collections.NewPrefix(0x0A)
	// OpenRewardEnabledBlocksKey holds the open epoch's reward-enabled block
	// count. Genesis writes it explicitly; afterwards an absent or unreadable
	// counter is corruption and must never be defaulted to zero.
	OpenRewardEnabledBlocksKey = collections.NewPrefix(0x0B)

	// RewardConfigVersionsPrefix holds the immutable reward-configuration history
	// keyed by effective epoch. This is the sole authority for the economics an
	// epoch's emission is computed under.
	RewardConfigVersionsPrefix = collections.NewPrefix(0x0C)
	// ScheduledRewardConfigsPrefix holds the single pending reward-configuration
	// change, keyed by the epoch it becomes effective at. At most one entry
	// exists and its key is exactly current_epoch + 1.
	ScheduledRewardConfigsPrefix = collections.NewPrefix(0x0D)

	// SlotEntitlementsPrefix holds canonical finalized entitlements keyed
	// (epoch, slot_id).
	//
	// The key order is (epoch, slot_id) and not the reverse. Both orders serve the
	// point read equally, but only this one makes "every entitlement of one epoch,
	// ascending by slot_id" a bounded prefix range — which is what epoch queries
	// and later settlement materialization both need. Ordering the key the other
	// way would require a second, derivable collection to answer the same
	// question, and with it a class of divergence between the two.
	SlotEntitlementsPrefix = collections.NewPrefix(0x0E)
	// OutstandingEntitlementLiabilityKey holds the O(1) accumulator of unreleased
	// entitlement value. Genesis writes it explicitly; afterwards an absent or
	// unreadable value is corruption and must never be defaulted to zero.
	OutstandingEntitlementLiabilityKey = collections.NewPrefix(0x0F)

	// RewardConfigVersionIndexPrefix maps a reward-configuration VERSION NUMBER to
	// the effective epoch its record is stored under.
	//
	// Purely derived state, and deliberately so. RewardConfigVersions remains the
	// sole economic authority; this holds no economics at all, only the key needed
	// to reach one. It exists because the history is keyed by effective epoch, so
	// answering "which record is version N" without it means walking the history —
	// a cost that grows with every configuration change the chain has ever
	// accepted.
	//
	// Being derived is what makes it safe to add: it is rebuilt from the canonical
	// history at genesis, written with it at promotion, absent from the genesis
	// document, and cross-checked against the row it points at on every read. A
	// divergence between the two is corruption, never an answer.
	RewardConfigVersionIndexPrefix = collections.NewPrefix(0x10)
)
