package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/x/rewards/keeper"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// Canonical epoch geometry.
//
// The boundary of an epoch is DERIVED from immutable history, so these tests are
// about a recurrence rather than a stored number. What they mostly protect is
// that the derivation stays anchored: every start height traces back to the
// original genesis anchor through a chain of lengths, and a wrong predecessor or
// an unchecked subtraction moves every boundary after it rather than one.

// historyKeeper builds a keeper with an explicit epoch-configuration history and
// nothing else assumed.
func historyKeeper(t *testing.T, versions ...types.EpochConfigVersion) (keeper.Keeper, sdk.Context) {
	t.Helper()
	k, ctx, _ := setupKeeper(t, &coreSlotKeeperMock{})
	for _, version := range versions {
		require.NoError(t, k.EpochConfigVersions.Set(ctx, version.EffectiveEpoch, version))
	}
	return k, ctx
}

func anchor(startHeight, length uint64) types.EpochConfigVersion {
	return types.EpochConfigVersion{
		Version: 1, EffectiveEpoch: 1, EffectiveStartHeight: startHeight, EpochLengthBlocks: length,
	}
}

// TestEpochGeometryFromOriginalGenesisAnchor covers a chain whose first block is
// not height 1 — the case a clamped or assumed anchor gets wrong.
func TestEpochGeometryFromOriginalGenesisAnchor(t *testing.T) {
	const initialHeight, length = 500, 10
	k, ctx := historyKeeper(t, anchor(initialHeight, length))

	for _, tc := range []struct{ epoch, start, end uint64 }{
		{epoch: 1, start: 500, end: 509},
		{epoch: 2, start: 510, end: 519},
		{epoch: 7, start: 560, end: 569},
	} {
		start, err := k.EpochStartHeight(ctx, tc.epoch)
		require.NoError(t, err)
		require.Equalf(t, tc.start, start, "epoch %d start", tc.epoch)

		end, err := k.EpochEndHeight(ctx, tc.epoch)
		require.NoError(t, err)
		require.Equalf(t, tc.end, end, "epoch %d end", tc.epoch)

		gotLength, err := k.EpochLengthForEpoch(ctx, tc.epoch)
		require.NoError(t, err)
		require.Equal(t, uint64(length), gotLength)
	}
}

// TestEpochGeometryResolvesThePredecessorVersion pins that resolution takes the
// GREATEST version effective at or before the epoch, not the first or the latest.
func TestEpochGeometryResolvesThePredecessorVersion(t *testing.T) {
	k, ctx := historyKeeper(t,
		anchor(1, 10),
		types.EpochConfigVersion{Version: 2, EffectiveEpoch: 5, EffectiveStartHeight: 41, EpochLengthBlocks: 20},
		types.EpochConfigVersion{Version: 3, EffectiveEpoch: 9, EffectiveStartHeight: 121, EpochLengthBlocks: 5},
	)

	for _, tc := range []struct{ epoch, start uint64 }{
		{epoch: 1, start: 1},   // v1
		{epoch: 4, start: 31},  // v1, last epoch it governs
		{epoch: 5, start: 41},  // v2 begins
		{epoch: 8, start: 101}, // v2, last epoch it governs
		{epoch: 9, start: 121}, // v3 begins
		{epoch: 11, start: 131},
	} {
		start, err := k.EpochStartHeight(ctx, tc.epoch)
		require.NoError(t, err)
		require.Equalf(t, tc.start, start, "epoch %d", tc.epoch)
	}
}

// TestEpochGeometryAdjacentVersionsAreContinuous checks the §11 continuity
// equation across the history above: each version's anchor must be exactly where
// its predecessor's geometry would have put that epoch.
func TestEpochGeometryAdjacentVersionsAreContinuous(t *testing.T) {
	versions := []types.EpochConfigVersion{
		anchor(1, 10),
		{Version: 2, EffectiveEpoch: 5, EffectiveStartHeight: 41, EpochLengthBlocks: 20},
		{Version: 3, EffectiveEpoch: 9, EffectiveStartHeight: 121, EpochLengthBlocks: 5},
	}
	for i := 1; i < len(versions); i++ {
		previous, current := versions[i-1], versions[i]
		expected := previous.EffectiveStartHeight +
			(current.EffectiveEpoch-previous.EffectiveEpoch)*previous.EpochLengthBlocks
		require.Equalf(t, expected, current.EffectiveStartHeight,
			"version %d must begin where version %d's geometry ends", current.Version, previous.Version)
	}
}

// TestEpochGeometryFailsClosedOnUnresolvableOrCorruptHistory separates the one
// ordinary answer from every state-integrity failure.
func TestEpochGeometryFailsClosedOnUnresolvableOrCorruptHistory(t *testing.T) {
	t.Run("no version at or before the epoch is ordinary absence", func(t *testing.T) {
		k, ctx := historyKeeper(t,
			types.EpochConfigVersion{Version: 1, EffectiveEpoch: 9, EffectiveStartHeight: 1, EpochLengthBlocks: 10},
		)
		_, err := k.EpochStartHeight(ctx, 3)
		require.ErrorIs(t, err, types.ErrEpochConfigNotFound)
	})

	t.Run("empty history is ordinary absence", func(t *testing.T) {
		k, ctx := historyKeeper(t)
		_, err := k.EpochStartHeight(ctx, 1)
		require.ErrorIs(t, err, types.ErrEpochConfigNotFound)
	})

	t.Run("epoch zero is refused", func(t *testing.T) {
		k, ctx := historyKeeper(t, anchor(1, 10))
		_, err := k.EpochStartHeight(ctx, 0)
		require.ErrorIs(t, err, types.ErrInvalidState)
	})

	t.Run("a row stored under the wrong key is corruption, not absence", func(t *testing.T) {
		k, ctx := historyKeeper(t)
		// Effective epoch 4 written under key 2: the row and the index disagree,
		// so resolution would silently apply the wrong geometry.
		require.NoError(t, k.EpochConfigVersions.Set(ctx, 2, types.EpochConfigVersion{
			Version: 1, EffectiveEpoch: 4, EffectiveStartHeight: 1, EpochLengthBlocks: 10,
		}))
		_, err := k.EpochStartHeight(ctx, 3)
		require.ErrorIs(t, err, types.ErrInvalidState)
		require.NotErrorIs(t, err, types.ErrEpochConfigNotFound)
	})

	t.Run("a zero-length version is corruption, not absence", func(t *testing.T) {
		k, ctx := historyKeeper(t)
		// A zero length makes the start-height recurrence stationary: every epoch
		// would begin at the same block and no boundary would ever be reached.
		require.NoError(t, k.EpochConfigVersions.Set(ctx, 1, types.EpochConfigVersion{
			Version: 1, EffectiveEpoch: 1, EffectiveStartHeight: 1, EpochLengthBlocks: 0,
		}))
		_, err := k.EpochStartHeight(ctx, 2)
		require.ErrorIs(t, err, types.ErrInvalidState)
	})
}

// TestEpochGeometryChecksArithmetic proves the recurrence refuses to wrap.
//
// Heights and epochs are unsigned, so an unchecked multiply or add would not
// fail here — it would name a plausible, wrong block.
func TestEpochGeometryChecksArithmetic(t *testing.T) {
	const maxUint64 = ^uint64(0)

	t.Run("start height multiplication overflows", func(t *testing.T) {
		k, ctx := historyKeeper(t, anchor(1, maxUint64/2))
		_, err := k.EpochStartHeight(ctx, 10)
		require.ErrorIs(t, err, types.ErrInvalidState)
	})

	t.Run("start height addition overflows", func(t *testing.T) {
		k, ctx := historyKeeper(t, anchor(maxUint64-5, 100))
		_, err := k.EpochStartHeight(ctx, 2)
		require.ErrorIs(t, err, types.ErrInvalidState)
	})

	t.Run("the epoch successor is not representable", func(t *testing.T) {
		k, ctx := historyKeeper(t, anchor(1, 10))
		_, err := k.EpochEndHeight(ctx, maxUint64)
		require.ErrorIs(t, err, types.ErrInvalidState)
	})
}

// TestScheduledEpochConfigProjection covers the query-side projection, which is
// the only place the ordered schedule affects a boundary.
func TestScheduledEpochConfigProjection(t *testing.T) {
	k, ctx := historyKeeper(t, anchor(1, 10))
	// Two distinct future effective epochs coexist.
	require.NoError(t, k.ScheduledEpochConfigs.Set(ctx, 3, types.ScheduledEpochConfig{EffectiveEpoch: 3, EpochLengthBlocks: 20}))
	require.NoError(t, k.ScheduledEpochConfigs.Set(ctx, 5, types.ScheduledEpochConfig{EffectiveEpoch: 5, EpochLengthBlocks: 4}))

	for _, tc := range []struct{ epoch, start uint64 }{
		{epoch: 1, start: 1},
		{epoch: 2, start: 11},
		{epoch: 3, start: 21}, // its own start is set by epoch 2's length, not by the schedule at 3
		{epoch: 4, start: 41}, // now 20-block epochs
		{epoch: 5, start: 61},
		{epoch: 6, start: 65}, // now 4-block epochs
	} {
		start, err := k.ProjectEpochStartHeight(ctx, tc.epoch, 100)
		require.NoError(t, err)
		require.Equalf(t, tc.start, start, "epoch %d", tc.epoch)
	}

	t.Run("replacing a scheduled value changes only later boundaries", func(t *testing.T) {
		require.NoError(t, k.ScheduledEpochConfigs.Set(ctx, 3, types.ScheduledEpochConfig{EffectiveEpoch: 3, EpochLengthBlocks: 2}))
		// Epoch 3's own start is unchanged because it is fixed by epoch 2.
		start, err := k.ProjectEpochStartHeight(ctx, 3, 100)
		require.NoError(t, err)
		require.Equal(t, uint64(21), start)
		// Epoch 4 moves: the replacement takes effect from epoch 3 onward. A cached
		// future start height would have kept the stale value here.
		start, err = k.ProjectEpochStartHeight(ctx, 4, 100)
		require.NoError(t, err)
		require.Equal(t, uint64(23), start)
	})

	t.Run("beyond the horizon is refused, never clamped", func(t *testing.T) {
		_, err := k.ProjectEpochStartHeight(ctx, 10_000, 5)
		require.ErrorIs(t, err, types.ErrEpochConfigNotFound)
	})
}
