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

type CoreSlotKeeper interface {
	GetActiveSlots(ctx context.Context) ([]coreslottypes.CoreSlot, error)
	GetSlot(ctx context.Context, slotID uint64) (coreslottypes.CoreSlot, error)
	GetRewardWeight(ctx context.Context, slotID uint64) (coreslottypes.OperatorRewardWeight, error)
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
	ClaimRecords       collections.Map[collections.Pair[uint64, uint64], types.EligibleSlotReward]
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
		ClaimRecords:       collections.NewMap(sb, types.ClaimRecordsPrefix, "claim_records", pairKey, codec.CollValue[types.EligibleSlotReward](cdc)),
	}
	schema, err := sb.Build()
	if err != nil {
		panic(err)
	}
	k.Schema = schema
	return k
}
