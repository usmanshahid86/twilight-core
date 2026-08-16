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
)
