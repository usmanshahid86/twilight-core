package keeper_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/x/rewards/keeper"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// The derived version -> effective epoch index.
//
// The index exists for one reason: the canonical history is keyed by effective
// epoch, so answering "which record is version N" without it means walking the
// history, and that cost grows with every configuration change the chain has ever
// accepted.
//
// It is derived, and everything below is about keeping it that way. It holds no
// economics. It is rebuilt from the history at genesis and written with it at
// promotion. It never appears in a genesis document, so no operator can state it
// wrongly. And every lookup re-reads the canonical row and requires the two to
// agree in BOTH directions, so the index can make an answer fast and can never
// make it different.

// TestVersionLookupRefusesANonMonotonicHistory is the defect the previous
// implementation had, and the reason a walk was the wrong mechanism.
//
// A walk that stops once the rows pass the number asked for is assuming exactly
// the ordering it is supposed to be reading. Given v1, then v9, then v3, asking
// for version 3 sees v9, concludes it has gone past, and answers NotFound —
// reporting corruption as an ordinary "no such version".
func TestVersionLookupRefusesANonMonotonicHistory(t *testing.T) {
	k, ctx, _ := setupRewardConfig(t)
	server := keeper.NewQueryServer(k)

	// v1 @ 1 is the anchor from the fixture.
	seedRewardVersion(t, k, ctx, rewardVersionAt(9, 4, "20"))
	seedRewardVersion(t, k, ctx, rewardVersionAt(3, 9, "30"))

	_, err := server.RewardConfigVersion(ctx, &types.QueryRewardConfigVersionRequest{Version: 3})
	require.Equal(t, codes.Internal, status.Code(err),
		"a history whose versions do not advance with their epochs is corrupt, not short")
	require.Contains(t, err.Error(), "does not advance past")
}

// TestVersionLookupRefusesADivergentIndex covers every way the index and the
// history can disagree.
//
// Each is corruption rather than absence, and the distinction is the whole point:
// a client told NotFound concludes the version was never accepted, which is a
// different and much more comfortable belief than "this node's state is broken".
func TestVersionLookupRefusesADivergentIndex(t *testing.T) {
	for _, tc := range []struct {
		name    string
		corrupt func(*testing.T, keeper.Keeper, sdk.Context)
		reason  string
	}{
		{
			name:   "the index points at an epoch the history has no record for",
			reason: "the canonical history has no record",
			corrupt: func(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
				seedRewardVersion(t, k, ctx, rewardVersionAt(2, 4, "20"))
				require.NoError(t, k.RewardConfigVersionIndex.Set(ctx, 2, 77))
			},
		},
		{
			name:   "the index points at an epoch holding a different version",
			reason: "the canonical history holds version",
			corrupt: func(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
				seedRewardVersion(t, k, ctx, rewardVersionAt(2, 4, "20"))
				seedRewardVersion(t, k, ctx, rewardVersionAt(3, 9, "30"))
				// Version 2 now resolves to the row that is really version 3.
				require.NoError(t, k.RewardConfigVersionIndex.Set(ctx, 2, 9))
			},
		},
		{
			name:   "the history row at the derived epoch names the wrong version",
			reason: "the canonical history holds version",
			corrupt: func(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
				seedRewardVersion(t, k, ctx, rewardVersionAt(2, 4, "20"))
				// The row is replaced under the same key; the index still says 2 -> 4.
				require.NoError(t, k.RewardConfigVersions.Set(ctx, 4, rewardVersionAt(5, 4, "20")))
			},
		},
		{
			name:   "the history row at the derived epoch is itself malformed",
			reason: "has a leading zero",
			corrupt: func(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
				seedRewardVersion(t, k, ctx, rewardVersionAt(2, 4, "010"))
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx, _ := setupRewardConfig(t)
			server := keeper.NewQueryServer(k)
			tc.corrupt(t, k, ctx)

			_, err := server.RewardConfigVersion(ctx, &types.QueryRewardConfigVersionRequest{Version: 2})
			require.Equal(t, codes.Internal, status.Code(err))
			require.Contains(t, err.Error(), tc.reason)
		})
	}
}

// TestVersionLookupStillDistinguishesGenuineAbsence guards the other side. The
// divergence rules above must not turn "never accepted" into corruption.
func TestVersionLookupStillDistinguishesGenuineAbsence(t *testing.T) {
	k, ctx, _ := setupRewardConfig(t)
	server := keeper.NewQueryServer(k)
	seedRewardVersion(t, k, ctx, rewardVersionAt(2, 4, "20"))
	seedRewardVersion(t, k, ctx, rewardVersionAt(3, 9, "30"))

	for _, version := range []uint64{4, 5, 1_000, 1 << 40} {
		_, err := server.RewardConfigVersion(ctx, &types.QueryRewardConfigVersionRequest{Version: version})
		require.Equalf(t, codes.NotFound, status.Code(err), "version %d", version)
	}

	_, err := server.RewardConfigVersion(ctx, &types.QueryRewardConfigVersionRequest{Version: 0})
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	for version, epoch := range map[uint64]uint64{1: 1, 2: 4, 3: 9} {
		resp, err := server.RewardConfigVersion(ctx, &types.QueryRewardConfigVersionRequest{Version: version})
		require.NoError(t, err)
		require.Equal(t, version, resp.Version.Version)
		require.Equal(t, epoch, resp.Version.EffectiveEpoch)
	}
}

// TestVersionLookupCostDoesNotTrackHistoryLength is regression E.
//
// Measured against a substantial history, on the three requests whose cost a walk
// would have made different: an early version (cheap for a walk), a late version
// (expensive), and a large absent one (a full scan). All three must cost the same,
// and none may walk a row.
func TestVersionLookupCostDoesNotTrackHistoryLength(t *testing.T) {
	const versions = 200

	k, ctx, access := setupCountingRewardConfig(t, versions)
	server := keeper.NewQueryServer(k)

	measure := func(t *testing.T, request *types.QueryRewardConfigVersionRequest, expectFound bool) storeAccess {
		t.Helper()
		*access = storeAccess{}
		_, err := server.RewardConfigVersion(ctx, request)
		if expectFound {
			require.NoError(t, err)
		} else {
			require.Equal(t, codes.NotFound, status.Code(err))
		}
		return *access
	}

	early := measure(t, &types.QueryRewardConfigVersionRequest{Version: 2}, true)
	late := measure(t, &types.QueryRewardConfigVersionRequest{Version: versions}, true)
	absent := measure(t, &types.QueryRewardConfigVersionRequest{Version: 1 << 40}, false)

	require.Zero(t, early.rows, "a point lookup must not walk the history")
	require.Zero(t, late.rows)
	require.Zero(t, absent.rows)

	require.Equal(t, early.reads, late.reads,
		"looking up the newest version must cost the same as the oldest")
	require.LessOrEqual(t, absent.reads, early.reads,
		"an absent version must not be more expensive than a present one")
	require.LessOrEqual(t, early.reads, 6,
		"a point lookup is a small constant number of reads, not a function of history length")
}

// TestVersionIndexIsWrittenWithTheHistoryItDescribes covers the two paths that
// create canonical history: genesis import and promotion.
//
// Neither is allowed to leave the index behind, because an index that lags the
// history reports a real version as absent — which is the failure mode hardest to
// notice, since it looks exactly like a version that was never accepted.
func TestVersionIndexIsWrittenWithTheHistoryItDescribes(t *testing.T) {
	t.Run("genesis rebuilds it from the imported history", func(t *testing.T) {
		k, ctx, _ := setupKeeper(t, &coreSlotKeeperMock{})
		require.NoError(t, k.InitGenesis(ctx, *types.DefaultGenesis()))

		epoch, err := k.RewardConfigVersionIndex.Get(ctx, 1)
		require.NoError(t, err)
		require.Equal(t, uint64(1), epoch)

		version, found, err := k.RewardConfigVersionByNumber(ctx, 1)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, uint64(1), version.Version)
	})

	t.Run("promotion writes it with the row", func(t *testing.T) {
		k, ctx, _ := setupFinalizationWithRewardConfig(t)
		require.NoError(t, k.ScheduledRewardConfigs.Set(ctx, 2, types.ScheduledRewardConfig{
			EffectiveEpoch:      2,
			InitialBlockSubsidy: "25",
		}))

		require.NoError(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))

		promoted, found, err := k.RewardConfigVersionByNumber(ctx, 2)
		require.NoError(t, err)
		require.True(t, found, "the promoted version must be reachable by number")
		require.Equal(t, uint64(2), promoted.EffectiveEpoch)
		require.Equal(t, "25", promoted.InitialBlockSubsidy)
	})

	t.Run("a failed promotion leaves neither behind", func(t *testing.T) {
		k, ctx, _ := setupFinalizationWithRewardConfig(t)
		// Scheduled at an epoch the promotion rule does not admit, so the whole
		// EndBlock cache is discarded.
		require.NoError(t, k.ScheduledRewardConfigs.Set(ctx, 7, types.ScheduledRewardConfig{
			EffectiveEpoch:      7,
			InitialBlockSubsidy: "25",
		}))

		require.Error(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))

		_, found, err := k.RewardConfigVersionByNumber(ctx, 2)
		require.NoError(t, err)
		require.False(t, found, "no index entry may survive a discarded promotion")
		_, err = k.RewardConfigVersionIndex.Get(ctx, 2)
		require.Error(t, err)
	})
}

// setupCountingRewardConfig builds a keeper whose reward-configuration history is
// long enough that any walk-based lookup would be visibly expensive, with every
// store operation under that prefix counted.
func setupCountingRewardConfig(t *testing.T, versions int) (keeper.Keeper, sdk.Context, *storeAccess) {
	t.Helper()
	k, ctx, access := setupCountingFinalization(t, 1, 1)
	for i := 7; i <= versions; i++ {
		seedRewardVersion(t, k, ctx, rewardVersionAt(uint64(i), uint64(i), fmt.Sprintf("%d", 10+i)))
	}
	return k, ctx, access
}
