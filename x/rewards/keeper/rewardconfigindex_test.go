package keeper_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"

	"cosmossdk.io/log"
	storetypes "cosmossdk.io/store/types"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil/integration"
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
	require.Contains(t, err.Error(), "does not immediately follow")
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
			// A swapped row is caught by the contiguity edge before the identity
			// check reaches it. Both are corruption and both are Internal; the edge
			// simply notices first, because under the ratified sequence a row that
			// is not the one the index expects is almost always also a row that does
			// not follow its predecessor.
			name:   "the history row at the derived epoch was replaced",
			reason: "does not immediately follow",
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

// TestVersionLookupCostDoesNotTrackHistoryLength measures the WHOLE lookup path.
//
// The path spans two collections — the canonical history and the derived index —
// so a measurement of only one is not evidence about the cost of the lookup. Both
// prefixes are counted separately here, because a claim about one collection is
// not a claim about the other.
//
// Three requests, chosen because a walk-based implementation would price them
// differently: an early version (cheap for a walk), the newest version
// (expensive), and a large absent one (a full scan). All three must cost the same,
// and none may advance an iterator over either collection.
func TestVersionLookupCostDoesNotTrackHistoryLength(t *testing.T) {
	const versions = 200

	k, ctx, history, index := setupCountingRewardConfig(t, versions)
	server := keeper.NewQueryServer(k)

	type cost struct{ history, index storeAccess }
	measure := func(t *testing.T, request *types.QueryRewardConfigVersionRequest, expectFound bool) cost {
		t.Helper()
		*history, *index = storeAccess{}, storeAccess{}
		_, err := server.RewardConfigVersion(ctx, request)
		if expectFound {
			require.NoError(t, err)
		} else {
			require.Equal(t, codes.NotFound, status.Code(err))
		}
		return cost{history: *history, index: *index}
	}

	early := measure(t, &types.QueryRewardConfigVersionRequest{Version: 2}, true)
	latest := measure(t, &types.QueryRewardConfigVersionRequest{Version: versions}, true)
	absent := measure(t, &types.QueryRewardConfigVersionRequest{Version: 1 << 40}, false)

	for name, c := range map[string]cost{"early": early, "latest": latest, "absent": absent} {
		require.Zerof(t, c.history.rows, "%s: a point lookup must not walk the canonical history", name)
		require.Zerof(t, c.index.rows, "%s: a point lookup must not walk the derived index", name)
	}

	require.Equal(t, early.history.reads, latest.history.reads,
		"looking up the newest version must read the history exactly as much as the oldest")
	require.Equal(t, early.index.reads, latest.index.reads,
		"and must read the index exactly as much")
	require.Positive(t, early.index.reads, "the index must actually be on the path being measured")

	// An absent version is decided by the canonical range alone, so it never
	// consults the index at all — strictly cheaper, never more expensive.
	require.LessOrEqual(t, absent.history.reads, early.history.reads)
	require.Zero(t, absent.index.reads,
		"a version above the canonical range is answered without touching derived state")

	// A small constant, not a function of the two hundred versions behind it.
	require.LessOrEqual(t, early.history.reads+early.index.reads, 10,
		"the lookup is a bounded number of canonical and index reads plus constant edge validation")
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

	// TestPromotionRollsBackTheHistoryRowWhenTheIndexWriteFails is the seam the
	// shared-cache claim actually rests on.
	//
	// The earlier version of this case failed BEFORE the history row was written,
	// so it could not distinguish "both writes rolled back" from "neither was
	// attempted". The failure is now forced at the one point that matters: the
	// history row is written and the derived index write is then refused.
	//
	// The refusal is arranged through the index's own write-once rule, by planting
	// an entry for the version promotion is about to assign. That is state a
	// conforming chain cannot hold, which is exactly why it is the narrowest
	// available way to fail after the first write without test-only machinery
	// inside the production path.
	t.Run("a failed index write rolls back the history row", func(t *testing.T) {
		k, ctx, _ := setupFinalizationWithRewardConfig(t)
		require.NoError(t, k.ScheduledRewardConfigs.Set(ctx, 2, types.ScheduledRewardConfig{
			EffectiveEpoch:      2,
			InitialBlockSubsidy: "25",
		}))
		// Promotion will derive version 2. An entry already claiming it makes the
		// index write fail after the history row has been set.
		require.NoError(t, k.RewardConfigVersionIndex.Set(ctx, 2, 99))

		require.Error(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))

		// The history row did not survive.
		_, err := k.RewardConfigVersions.Get(ctx, 2)
		require.Error(t, err, "no canonical history row may survive a discarded promotion")
		latest, err := k.RewardConfigForTarget(ctx, 1)
		require.NoError(t, err)
		require.Equal(t, uint64(1), latest.Version, "the history is still the anchor alone")

		// The planted index entry is exactly as it was: unchanged, not overwritten.
		planted, err := k.RewardConfigVersionIndex.Get(ctx, 2)
		require.NoError(t, err)
		require.Equal(t, uint64(99), planted)

		// The schedule was not consumed.
		scheduled, found, err := k.ScheduledRewardConfigFor(ctx, 2)
		require.NoError(t, err)
		require.True(t, found, "an unconsumed schedule must remain for the next attempt")
		require.Equal(t, "25", scheduled.InitialBlockSubsidy)

		// And the monetary transition did not commit separately from the promotion
		// that failed. Both halves share one cache, so neither is present.
		_, found, err = k.GetFinalizedEpoch(ctx, 1)
		require.NoError(t, err)
		require.False(t, found, "finalization must not commit beside a failed promotion")
		entitlements, err := k.IterateEntitlementsForEpoch(ctx, 1)
		require.NoError(t, err)
		require.Empty(t, entitlements)
		requireLiability(t, k, ctx, "0")
	})

	t.Run("a schedule the promotion rule refuses leaves nothing behind", func(t *testing.T) {
		k, ctx, _ := setupFinalizationWithRewardConfig(t)
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
// long enough that any walk-based lookup would be visibly expensive, counting the
// canonical history and the derived index separately.
func setupCountingRewardConfig(
	t *testing.T, versions int,
) (keeper.Keeper, sdk.Context, *storeAccess, *storeAccess) {
	t.Helper()
	registry := codectypes.NewInterfaceRegistry()
	types.RegisterInterfaces(registry)
	keys := storetypes.NewKVStoreKeys(types.StoreKey)
	cms := integration.CreateMultiStore(keys, log.NewNopLogger())
	ctx := sdk.NewContext(cms, cmtproto.Header{Height: 1}, false, log.NewNopLogger())

	history, index := &storeAccess{}, &storeAccess{}
	service := countingKVStoreService{
		inner: runtime.NewKVStoreService(keys[types.StoreKey]),
		watched: []watchedPrefix{
			{prefix: types.RewardConfigVersionsPrefix.Bytes(), access: history},
			{prefix: types.RewardConfigVersionIndexPrefix.Bytes(), access: index},
		},
	}
	k := keeper.NewKeeper(codec.NewProtoCodec(registry), service,
		accountKeeperMock{}, &bankKeeperMock{}, &coreSlotKeeperMock{}, testEconomicAddresses(t))

	params := rewardConfigParams()
	require.NoError(t, k.SetParams(ctx, params))
	require.NoError(t, k.SetState(ctx, types.RewardsState{
		CurrentEpoch: 1, CurrentEpochStartHeight: 1, CumulativeEmitted: "0", CarryForwardRemainder: "0",
	}))
	require.NoError(t, k.SetPauseState(ctx, types.RewardsPauseState{}))
	seedRewardConfigTimeline(t, k, ctx, params)
	for i := 2; i <= versions; i++ {
		seedRewardVersion(t, k, ctx, rewardVersionAt(uint64(i), uint64(i), fmt.Sprintf("%d", 10+i)))
	}
	return k, ctx, history, index
}

// TestCanonicalVersionsAreContiguous is the ratified sequence rule at the runtime
// admission point.
//
// version is a protocol sequence number, not merely an increasing label. The
// distinction is what makes a version-number query answerable: with a contiguous
// history the numbers present are exactly 1..latest, so "above latest" is absence
// and "at or below latest but unreachable" is corruption. Under a merely
// increasing rule a history could hold 1 then 3, and version 2 would be
// indistinguishable from both.
func TestCanonicalVersionsAreContiguous(t *testing.T) {
	promote := func(t *testing.T, k keeper.Keeper, ctx sdk.Context, effectiveEpoch uint64) error {
		t.Helper()
		require.NoError(t, k.ScheduledRewardConfigs.Set(ctx, effectiveEpoch, types.ScheduledRewardConfig{
			EffectiveEpoch:      effectiveEpoch,
			InitialBlockSubsidy: "25",
		}))
		return k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight))
	}

	t.Run("promotion derives exactly the successor", func(t *testing.T) {
		k, ctx, _ := setupFinalizationWithRewardConfig(t)
		require.NoError(t, promote(t, k, ctx, 2))

		latest, found, err := k.RewardConfigVersionByNumber(ctx, 2)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, uint64(2), latest.Version, "v1 -> v2 is the only version promotion may assign")
	})

	for _, tc := range []struct {
		name     string
		planted  types.RewardConfigVersion
		accepted bool
	}{
		{name: "the successor is accepted", planted: rewardVersionAt(2, 4, "20"), accepted: true},
		{name: "a gap is rejected", planted: rewardVersionAt(3, 4, "20")},
		{name: "a larger gap is rejected", planted: rewardVersionAt(9, 4, "20")},
		{name: "a non-advancing version is rejected", planted: rewardVersionAt(1, 4, "20")},
		{name: "a decreasing version is rejected", planted: types.RewardConfigVersion{
			Version: 1, EffectiveEpoch: 4, InitialBlockSubsidy: "20",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx, _ := setupRewardConfig(t)
			seedRewardVersion(t, k, ctx, tc.planted)

			// The read side enforces the same rule the write side does, so a planted
			// row is refused wherever the history is resolved.
			_, err := k.RewardConfigForTarget(ctx, 6)
			if tc.accepted {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, types.ErrInvalidState)
			require.Contains(t, err.Error(), "contiguous")
		})
	}

	t.Run("an exhausted version space fails closed", func(t *testing.T) {
		k, ctx, _ := setupFinalizationWithRewardConfig(t)
		// The newest version is the largest representable number, so no successor
		// exists. Checked arithmetic must refuse rather than wrap to zero, which
		// would look like a fresh anchor.
		require.NoError(t, k.RewardConfigVersions.Set(ctx, 1, types.RewardConfigVersion{
			Version: ^uint64(0), EffectiveEpoch: 1, InitialBlockSubsidy: "10",
		}))
		require.Error(t, promote(t, k, ctx, 2))
	})

	t.Run("a long contiguous history remains acceptable", func(t *testing.T) {
		k, ctx, _ := setupRewardConfig(t)
		for i := uint64(2); i <= 40; i++ {
			seedRewardVersion(t, k, ctx, rewardVersionAt(i, i*3, "20"))
		}
		// Target 100 binds epoch 98; the newest row at or before it is the one
		// effective at 96, which is version 32.
		version, err := k.RewardConfigForTarget(ctx, 100)
		require.NoError(t, err)
		require.Equal(t, uint64(96), version.EffectiveEpoch)
		require.Equal(t, uint64(32), version.Version)
	})
}

// TestInRangeVersionWithNoIndexEntryIsCorruption is the classification the
// contiguity rule makes possible, and the case closure review found missing.
//
// The canonical history is intact and says version 2 exists. Only the derived
// index entry is gone. Reporting NotFound would tell a client the chain never
// accepted a configuration it is in fact governed by — an answer that is not
// merely unhelpful but false, and one the client has no way to question.
func TestInRangeVersionWithNoIndexEntryIsCorruption(t *testing.T) {
	k, ctx, _ := setupRewardConfig(t)
	server := keeper.NewQueryServer(k)
	seedRewardVersion(t, k, ctx, rewardVersionAt(2, 4, "20"))

	// Both selectors agree while the index is intact.
	byEpoch, err := server.RewardConfigVersion(ctx, &types.QueryRewardConfigVersionRequest{EffectiveEpoch: 4})
	require.NoError(t, err)
	require.Equal(t, uint64(2), byEpoch.Version.Version)
	byNumber, err := server.RewardConfigVersion(ctx, &types.QueryRewardConfigVersionRequest{Version: 2})
	require.NoError(t, err)
	require.Equal(t, uint64(4), byNumber.Version.EffectiveEpoch)

	// Remove ONLY the derived entry. Canonical history is untouched.
	require.NoError(t, k.RewardConfigVersionIndex.Remove(ctx, 2))

	// The authority still answers, because it never needed the index.
	byEpoch, err = server.RewardConfigVersion(ctx, &types.QueryRewardConfigVersionRequest{EffectiveEpoch: 4})
	require.NoError(t, err)
	require.Equal(t, uint64(2), byEpoch.Version.Version)

	_, err = server.RewardConfigVersion(ctx, &types.QueryRewardConfigVersionRequest{Version: 2})
	require.Equal(t, codes.Internal, status.Code(err),
		"a version canonical history says exists is not absent because derived state lost it")
	require.Contains(t, err.Error(), "no index entry")

	// And a version above the canonical range is still ordinary absence.
	_, err = server.RewardConfigVersion(ctx, &types.QueryRewardConfigVersionRequest{Version: 3})
	require.Equal(t, codes.NotFound, status.Code(err))
}
