package types

import "cosmossdk.io/collections"

const (
	ModuleName   = "rewards"
	StoreKey     = ModuleName
	RouterKey    = ModuleName
	QuerierRoute = ModuleName

	FeePoolName = "rewards_fee_pool"
)

var (
	ParamsKey             = collections.NewPrefix(0x01)
	PendingParamsKey      = collections.NewPrefix(0x02)
	StateKey              = collections.NewPrefix(0x03)
	CurrentEpochConfigKey = collections.NewPrefix(0x04)
	ActiveBlocksPrefix    = collections.NewPrefix(0x05)
	FinalizedEpochsPrefix = collections.NewPrefix(0x06)
	ClaimRecordsPrefix    = collections.NewPrefix(0x07)
)
