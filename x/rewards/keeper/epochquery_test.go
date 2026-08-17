package keeper_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/x/rewards/keeper"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// The canonical epoch-timeline queries.
//
// These are read-only, but they are not consequence-free: a client deriving a
// boundary from them will act on the answer. So the contract they are held to is
// the same distinction the block path maintains — a modeled absence is an
// ordinary answer, and history that exists but cannot be trusted is a
// state-integrity failure that must not be flattened into "not found".

func epochQueryFixture(t *testing.T) (types.QueryServer, sdk.Context, keeper.Keeper) {
	t.Helper()
	params := types.DefaultParams()
	params.EpochLengthBlocks = shortEpoch
	k, ctx, _ := setupAccountingKeeper(t, &coreSlotKeeperMock{}, 1, params)
	return keeper.NewQueryServer(k), ctx, k
}

// TestEpochInfoClassifiesCanonicalStateFailures covers the error contract.
//
// None of these records is optional: genesis writes all of them and nothing
// removes them, so there is no "not configured yet" answer to give. Absence is
// corruption, and corruption is Internal — never NotFound, which would tell a
// client this chain has no rewards state, and never an unclassified Unknown,
// which is indistinguishable from a transport fault.
func TestEpochInfoClassifiesCanonicalStateFailures(t *testing.T) {
	t.Run("missing canonical state", func(t *testing.T) {
		k, ctx, _ := setupKeeper(t, &coreSlotKeeperMock{})
		_, err := keeper.NewQueryServer(k).EpochInfo(ctx, &types.QueryEpochInfoRequest{})
		require.Equal(t, codes.Internal, status.Code(err))
	})

	t.Run("missing epoch config mirror", func(t *testing.T) {
		k, ctx, _ := setupKeeper(t, &coreSlotKeeperMock{})
		require.NoError(t, k.SetParams(ctx, types.DefaultParams()))
		require.NoError(t, k.SetState(ctx, accountingState(1)))
		_, err := keeper.NewQueryServer(k).EpochInfo(ctx, &types.QueryEpochInfoRequest{})
		require.Equal(t, codes.Internal, status.Code(err))
	})

	t.Run("missing open reward-enabled counter", func(t *testing.T) {
		qs, ctx, k := epochQueryFixture(t)
		require.NoError(t, k.OpenRewardEnabledBlocks.Remove(ctx))
		_, err := qs.EpochInfo(ctx, &types.QueryEpochInfoRequest{})
		require.Equal(t, codes.Internal, status.Code(err))
	})

	t.Run("corrupt canonical geometry", func(t *testing.T) {
		qs, ctx, k := epochQueryFixture(t)
		// A stored length outside the ratified interval: the row exists and decodes,
		// but it is not admissible history.
		require.NoError(t, k.EpochConfigVersions.Set(ctx, 1, types.EpochConfigVersion{
			Version: 1, EffectiveEpoch: 1, EffectiveStartHeight: 1, EpochLengthBlocks: 5,
		}))
		_, err := qs.EpochInfo(ctx, &types.QueryEpochInfoRequest{})
		require.Equal(t, codes.Internal, status.Code(err))
	})

	t.Run("a healthy chain answers", func(t *testing.T) {
		qs, ctx, _ := epochQueryFixture(t)
		resp, err := qs.EpochInfo(ctx, &types.QueryEpochInfoRequest{})
		require.NoError(t, err)
		require.Equal(t, uint64(1), resp.CurrentEpochStartHeight)
		require.Equal(t, uint64(360), resp.CurrentEpochEndHeight)
		require.Equal(t, shortEpoch, resp.CurrentEpochLengthBlocks)
	})
}

// TestEpochBoundariesDistinguishesAbsenceFromCorruption keeps the one ordinary
// answer separate from every state-integrity failure.
func TestEpochBoundariesDistinguishesAbsenceFromCorruption(t *testing.T) {
	t.Run("an epoch predating the history is ordinary absence", func(t *testing.T) {
		k, ctx := historyKeeper(t,
			types.EpochConfigVersion{Version: 1, EffectiveEpoch: 5, EffectiveStartHeight: 1, EpochLengthBlocks: minLength},
		)
		_, err := keeper.NewQueryServer(k).EpochBoundaries(ctx, &types.QueryEpochBoundariesRequest{EpochNumber: 2})
		require.Equal(t, codes.NotFound, status.Code(err))
	})

	t.Run("a malformed governing row is corruption", func(t *testing.T) {
		k, ctx := historyKeeper(t)
		require.NoError(t, k.EpochConfigVersions.Set(ctx, 1, types.EpochConfigVersion{
			Version: 1, EffectiveEpoch: 1, EffectiveStartHeight: 1, EpochLengthBlocks: maxLength + 1,
		}))
		_, err := keeper.NewQueryServer(k).EpochBoundaries(ctx, &types.QueryEpochBoundariesRequest{EpochNumber: 1})
		require.Equal(t, codes.Internal, status.Code(err))
	})

	t.Run("epoch zero is rejected as an argument", func(t *testing.T) {
		qs, ctx, _ := epochQueryFixture(t)
		_, err := qs.EpochBoundaries(ctx, &types.QueryEpochBoundariesRequest{EpochNumber: 0})
		require.Equal(t, codes.InvalidArgument, status.Code(err))
	})
}

// TestEpochBoundariesChecksTheSuccessor covers the maximum epoch number.
//
// The end height is derived from the NEXT epoch's start, so the successor is a
// real arithmetic step. Unchecked it wraps to zero, and the query would then
// answer about epoch 0 — an epoch that cannot exist — instead of failing.
func TestEpochBoundariesChecksTheSuccessor(t *testing.T) {
	qs, ctx, _ := epochQueryFixture(t)
	_, err := qs.EpochBoundaries(ctx, &types.QueryEpochBoundariesRequest{EpochNumber: math.MaxUint64})
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
	require.Contains(t, err.Error(), "no representable successor")
}

// TestEpochBoundariesReturnsOneConsistentGeometry is the regression for a
// response whose own members contradicted each other.
//
// Start and end come from the schedule-aware projection. Resolving the length
// separately from immutable history alone answers a different question for any
// epoch a pending schedule entry has already changed, and publishes a triple that
// cannot be true of one epoch.
func TestEpochBoundariesReturnsOneConsistentGeometry(t *testing.T) {
	qs, ctx, k := epochQueryFixture(t)
	// A future length change that history has not absorbed yet.
	require.NoError(t, k.ScheduledEpochConfigs.Set(ctx, 3, types.ScheduledEpochConfig{
		EffectiveEpoch: 3, EpochLengthBlocks: longEpoch,
	}))

	for _, tc := range []struct {
		epoch, start, end, length uint64
	}{
		{epoch: 1, start: 1, end: 360, length: shortEpoch},
		{epoch: 2, start: 361, end: 720, length: shortEpoch},
		// Epoch 3 begins where epoch 2 ends — the schedule cannot move its own
		// start — but it RUNS at the scheduled length.
		{epoch: 3, start: 721, end: 1440, length: longEpoch},
		{epoch: 4, start: 1441, end: 2160, length: longEpoch},
	} {
		resp, err := qs.EpochBoundaries(ctx, &types.QueryEpochBoundariesRequest{EpochNumber: tc.epoch})
		require.NoErrorf(t, err, "epoch %d", tc.epoch)
		require.Equalf(t, tc.start, resp.StartHeight, "epoch %d start", tc.epoch)
		require.Equalf(t, tc.end, resp.EndHeight, "epoch %d end", tc.epoch)
		require.Equalf(t, tc.length, resp.EpochLengthBlocks, "epoch %d length", tc.epoch)
		require.Equalf(t, resp.EndHeight-resp.StartHeight+1, resp.EpochLengthBlocks,
			"epoch %d: length must describe the span the same response returned", tc.epoch)
	}
}

// TestEpochConfigVersionsRejectsMalformedRows keeps the query from presenting a
// record consensus itself would refuse as though it were history.
func TestEpochConfigVersionsRejectsMalformedRows(t *testing.T) {
	t.Run("a history row stored under the wrong key", func(t *testing.T) {
		qs, ctx, k := epochQueryFixture(t)
		require.NoError(t, k.EpochConfigVersions.Set(ctx, 9, types.EpochConfigVersion{
			Version: 2, EffectiveEpoch: 4, EffectiveStartHeight: 1081, EpochLengthBlocks: minLength,
		}))
		_, err := qs.EpochConfigVersions(ctx, &types.QueryEpochConfigVersionsRequest{})
		require.Equal(t, codes.Internal, status.Code(err))
	})

	t.Run("a schedule row with an inadmissible length", func(t *testing.T) {
		qs, ctx, k := epochQueryFixture(t)
		require.NoError(t, k.ScheduledEpochConfigs.Set(ctx, 4, types.ScheduledEpochConfig{
			EffectiveEpoch: 4, EpochLengthBlocks: minLength - 1,
		}))
		_, err := qs.EpochConfigVersions(ctx, &types.QueryEpochConfigVersionsRequest{})
		require.Equal(t, codes.Internal, status.Code(err))
	})

	t.Run("a schedule row stored under the wrong key", func(t *testing.T) {
		qs, ctx, k := epochQueryFixture(t)
		require.NoError(t, k.ScheduledEpochConfigs.Set(ctx, 4, types.ScheduledEpochConfig{
			EffectiveEpoch: 6, EpochLengthBlocks: maxLength,
		}))
		_, err := qs.EpochConfigVersions(ctx, &types.QueryEpochConfigVersionsRequest{})
		require.Equal(t, codes.Internal, status.Code(err))
	})
}

// TestEpochConfigVersionsBoundsTheSchedule proves the schedule is windowed rather
// than walked whole.
//
// The schedule is the more exposed of the two collections: unlike append-only
// history its size is not bounded by chain age, so an unbounded walk would let a
// single query force the node to materialize all of it.
func TestEpochConfigVersionsBoundsTheSchedule(t *testing.T) {
	qs, ctx, k := epochQueryFixture(t)
	const total = 150
	for epoch := uint64(2); epoch < 2+total; epoch++ {
		require.NoError(t, k.ScheduledEpochConfigs.Set(ctx, epoch, types.ScheduledEpochConfig{
			EffectiveEpoch: epoch, EpochLengthBlocks: minLength,
		}))
	}

	// The default window is the server maximum, not the whole collection.
	first, err := qs.EpochConfigVersions(ctx, &types.QueryEpochConfigVersionsRequest{})
	require.NoError(t, err)
	require.Len(t, first.Scheduled, 100)
	require.Equal(t, uint64(2), first.Scheduled[0].EffectiveEpoch)
	require.Equal(t, uint64(102), first.ScheduledNextEpoch,
		"the cursor names the first entry the window did not return")

	// The cursor continues exactly where the window stopped, and the final page
	// reports no successor.
	second, err := qs.EpochConfigVersions(ctx, &types.QueryEpochConfigVersionsRequest{
		ScheduledStartEpoch: first.ScheduledNextEpoch,
	})
	require.NoError(t, err)
	require.Len(t, second.Scheduled, total-100)
	require.Equal(t, uint64(102), second.Scheduled[0].EffectiveEpoch)
	require.Zero(t, second.ScheduledNextEpoch)

	// A caller-supplied limit is honored below the maximum.
	small, err := qs.EpochConfigVersions(ctx, &types.QueryEpochConfigVersionsRequest{ScheduledLimit: 3})
	require.NoError(t, err)
	require.Len(t, small.Scheduled, 3)
	require.Equal(t, uint64(5), small.ScheduledNextEpoch)

	// And one above it is reduced to the maximum rather than honored.
	huge, err := qs.EpochConfigVersions(ctx, &types.QueryEpochConfigVersionsRequest{ScheduledLimit: 10_000})
	require.NoError(t, err)
	require.Len(t, huge.Scheduled, 100)

	// History still paginates independently.
	require.Len(t, first.Versions, 1)
	require.Equal(t, uint64(1), first.Versions[0].EffectiveEpoch)
}
