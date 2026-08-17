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
	k, ctx := setupIndexKeeper(t)

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

// TestVersionIndexRebuildRefusesGappedHistory is the precondition the index's
// whole contract rests on.
//
// The lookup reads an out-of-range version as absence and an in-range missing
// entry as corruption. Both readings are sound only if the canonical range is
// genuinely gapless. Rebuilding an index over versions 1 and 3 would quietly make
// version 2 permanently "in range and missing" — corrupt forever, from a document
// that was accepted.
//
// In-package because fresh genesis refuses a second reward configuration version
// outright, so no genesis document reaches this rule. It is the seam a continuation
// importer would reuse, and it must refuse rather than normalize: deciding what a
// gapped history was supposed to mean is not an import path's decision to make.
func TestVersionIndexRebuildRefusesGappedHistory(t *testing.T) {
	version := func(number, epoch uint64) *types.RewardConfigVersion {
		return &types.RewardConfigVersion{
			Version: number, EffectiveEpoch: epoch, InitialBlockSubsidy: "10",
		}
	}

	for _, tc := range []struct {
		name     string
		history  []*types.RewardConfigVersion
		accepted bool
	}{
		{
			name:     "the canonical anchor alone",
			history:  []*types.RewardConfigVersion{version(1, 1)},
			accepted: true,
		},
		{
			name:     "a contiguous history",
			history:  []*types.RewardConfigVersion{version(1, 1), version(2, 4), version(3, 9)},
			accepted: true,
		},
		{
			name:    "a gap",
			history: []*types.RewardConfigVersion{version(1, 1), version(3, 4)},
		},
		{
			name:    "no anchor",
			history: []*types.RewardConfigVersion{version(2, 1), version(3, 4)},
		},
		{
			name:    "a repeated version number",
			history: []*types.RewardConfigVersion{version(1, 1), version(1, 4)},
		},
		{
			name:    "a zero version",
			history: []*types.RewardConfigVersion{version(0, 1)},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx := setupIndexKeeper(t)
			err := k.rebuildRewardConfigVersionIndex(ctx, tc.history)
			if tc.accepted {
				require.NoError(t, err)
				for _, row := range tc.history {
					stored, getErr := k.RewardConfigVersionIndex.Get(ctx, row.Version)
					require.NoError(t, getErr)
					require.Equal(t, row.EffectiveEpoch, stored)
				}
				return
			}
			require.ErrorIs(t, err, types.ErrInvalidGenesis)

			// Nothing was written. A rebuild that refused halfway would leave exactly
			// the partial index it exists to prevent.
			for _, row := range tc.history {
				_, getErr := k.RewardConfigVersionIndex.Get(ctx, row.Version)
				require.Error(t, getErr, "a refused rebuild must write no entry at all")
			}
		})
	}
}

// setupIndexKeeper builds a bare keeper with a real store and no dependencies, so
// the derived-index rules can be driven directly.
func setupIndexKeeper(t *testing.T) (Keeper, sdk.Context) {
	t.Helper()
	registry := codectypes.NewInterfaceRegistry()
	types.RegisterInterfaces(registry)
	keys := storetypes.NewKVStoreKeys(types.StoreKey)
	cms := integration.CreateMultiStore(keys, log.NewNopLogger())
	ctx := sdk.NewContext(cms, cmtproto.Header{Height: 1}, false, log.NewNopLogger())
	return NewKeeper(codec.NewProtoCodec(registry), runtime.NewKVStoreService(keys[types.StoreKey]),
		nil, nil, nil, economicaddress.Validator{}), ctx
}

// TestAppendRefusesANonSuccessorVersion pins the contiguity guard at the write
// boundary itself.
//
// In-package, because the only production caller — promotion — derives the version
// as latest+1 and therefore can never hand this function a non-successor. A test
// driving it through EndBlock could only ever watch the guard not fire, and would
// keep passing if it were deleted.
//
// The guard is worth having anyway: appendRewardConfigVersion is where canonical
// history is created, and the rule that makes a version-number query answerable
// belongs at the point of creation rather than only at the caller that happens to
// respect it today.
func TestAppendRefusesANonSuccessorVersion(t *testing.T) {
	anchor := types.RewardConfigVersion{Version: 1, EffectiveEpoch: 1, InitialBlockSubsidy: "10"}

	for _, tc := range []struct {
		name     string
		version  types.RewardConfigVersion
		accepted bool
	}{
		{
			name:     "the immediate successor",
			version:  types.RewardConfigVersion{Version: 2, EffectiveEpoch: 4, InitialBlockSubsidy: "20"},
			accepted: true,
		},
		{
			name:    "a gap",
			version: types.RewardConfigVersion{Version: 3, EffectiveEpoch: 4, InitialBlockSubsidy: "20"},
		},
		{
			name:    "a large gap",
			version: types.RewardConfigVersion{Version: 99, EffectiveEpoch: 4, InitialBlockSubsidy: "20"},
		},
		{
			name:    "the same version again",
			version: types.RewardConfigVersion{Version: 1, EffectiveEpoch: 4, InitialBlockSubsidy: "20"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx := setupIndexKeeper(t)
			require.NoError(t, k.RewardConfigVersions.Set(ctx, 1, anchor))
			require.NoError(t, k.RewardConfigVersionIndex.Set(ctx, 1, 1))

			err := k.appendRewardConfigVersion(ctx, tc.version)
			if tc.accepted {
				require.NoError(t, err)
				stored, getErr := k.RewardConfigVersionIndex.Get(ctx, tc.version.Version)
				require.NoError(t, getErr)
				require.Equal(t, tc.version.EffectiveEpoch, stored)
				return
			}
			require.ErrorIs(t, err, types.ErrInvalidState)
			require.Contains(t, err.Error(), "contiguous")

			_, getErr := k.RewardConfigVersions.Get(ctx, tc.version.EffectiveEpoch)
			require.Error(t, getErr, "a refused append must write no history row")
		})
	}

	t.Run("an exhausted version space", func(t *testing.T) {
		k, ctx := setupIndexKeeper(t)
		exhausted := types.RewardConfigVersion{
			Version: ^uint64(0), EffectiveEpoch: 1, InitialBlockSubsidy: "10",
		}
		require.NoError(t, k.RewardConfigVersions.Set(ctx, 1, exhausted))
		require.NoError(t, k.RewardConfigVersionIndex.Set(ctx, exhausted.Version, 1))

		err := k.appendRewardConfigVersion(ctx, types.RewardConfigVersion{
			Version: 1, EffectiveEpoch: 4, InitialBlockSubsidy: "20",
		})
		require.ErrorIs(t, err, types.ErrInvalidState)
		require.Contains(t, err.Error(), "exhausted",
			"checked arithmetic must refuse rather than wrap to a number that looks like a fresh anchor")
	})
}
