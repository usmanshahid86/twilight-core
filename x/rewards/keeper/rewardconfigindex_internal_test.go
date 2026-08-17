package keeper

import (
	"testing"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/log"
	storetypes "cosmossdk.io/store/types"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil/integration"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/internal/economicaddress"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// In-package, for the same reason the conservation assertions are.
//
// The duplicate rule below defends against a state no admitted path can produce:
// fresh genesis carries exactly one reward-configuration version, and promotion
// requires each new version to advance past the latest. A test driving it through
// a real path could therefore only ever observe it not firing, which would keep
// passing if the check were deleted.
//
// Calling it directly with the state it exists to refuse is what makes it
// load-bearing.
func TestRewardConfigVersionIndexIsWriteOnce(t *testing.T) {
	registry := codectypes.NewInterfaceRegistry()
	types.RegisterInterfaces(registry)
	keys := storetypes.NewKVStoreKeys(types.StoreKey)
	cms := integration.CreateMultiStore(keys, log.NewNopLogger())
	ctx := sdk.NewContext(cms, cmtproto.Header{Height: 1}, false, log.NewNopLogger())
	k := NewKeeper(codec.NewProtoCodec(registry), runtime.NewKVStoreService(keys[types.StoreKey]),
		nil, nil, nil, economicaddress.Validator{})

	first := types.RewardConfigVersion{Version: 2, EffectiveEpoch: 4, InitialBlockSubsidy: "20"}
	require.NoError(t, k.setRewardConfigVersionIndex(ctx, first))

	stored, err := k.RewardConfigVersionIndex.Get(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, uint64(4), stored)

	// A second row claiming the same version number, at a different epoch. Accepting
	// it would make "the record for version 2" ambiguous, which is not an answer the
	// lookup can give.
	second := types.RewardConfigVersion{Version: 2, EffectiveEpoch: 9, InitialBlockSubsidy: "30"}
	err = k.setRewardConfigVersionIndex(ctx, second)
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Contains(t, err.Error(), "already indexed")

	stored, err = k.RewardConfigVersionIndex.Get(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, uint64(4), stored, "the refused write must not have replaced anything")
}
