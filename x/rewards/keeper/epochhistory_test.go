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

// Every length used through the keeper is inside the ratified [360, 720]
// interval, because the read path enforces it. The pure recurrence is exercised
// at small lengths through epochStartFrom's own test; anything that goes through
// the store is a consensus read and is held to the consensus bound.
const (
	minLength = 360
	maxLength = 720
)

// TestEpochGeometryFromOriginalGenesisAnchor covers a chain whose first block is
// not height 1 — the case a clamped or assumed anchor gets wrong.
func TestEpochGeometryFromOriginalGenesisAnchor(t *testing.T) {
	const initialHeight, length = 500, minLength
	k, ctx := historyKeeper(t, anchor(initialHeight, length))

	for _, tc := range []struct{ epoch, start, end uint64 }{
		{epoch: 1, start: 500, end: 859},
		{epoch: 2, start: 860, end: 1219},
		{epoch: 7, start: 2660, end: 3019},
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
	k, ctx := historyKeeper(t, continuousHistory()...)

	for _, tc := range []struct{ epoch, start uint64 }{
		{epoch: 1, start: 1},    // v1
		{epoch: 4, start: 1081}, // v1, last epoch it governs
		{epoch: 5, start: 1441}, // v2 begins
		{epoch: 8, start: 3601}, // v2, last epoch it governs
		{epoch: 9, start: 4321}, // v3 begins
		{epoch: 11, start: 5041},
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
	versions := continuousHistory()
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
			types.EpochConfigVersion{Version: 1, EffectiveEpoch: 9, EffectiveStartHeight: 1, EpochLengthBlocks: minLength},
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
		k, ctx := historyKeeper(t, anchor(1, minLength))
		_, err := k.EpochStartHeight(ctx, 0)
		require.ErrorIs(t, err, types.ErrInvalidState)
	})

	t.Run("a row stored under the wrong key is corruption, not absence", func(t *testing.T) {
		k, ctx := historyKeeper(t)
		// Effective epoch 4 written under key 2: the row and the index disagree,
		// so resolution would silently apply the wrong geometry.
		require.NoError(t, k.EpochConfigVersions.Set(ctx, 2, types.EpochConfigVersion{
			Version: 1, EffectiveEpoch: 4, EffectiveStartHeight: 1, EpochLengthBlocks: minLength,
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

	t.Run("a zero version number is corruption", func(t *testing.T) {
		k, ctx := historyKeeper(t)
		require.NoError(t, k.EpochConfigVersions.Set(ctx, 1, types.EpochConfigVersion{
			Version: 0, EffectiveEpoch: 1, EffectiveStartHeight: 1, EpochLengthBlocks: minLength,
		}))
		_, err := k.EpochStartHeight(ctx, 1)
		require.ErrorIs(t, err, types.ErrInvalidState)
	})

	t.Run("a zero effective start height is corruption", func(t *testing.T) {
		k, ctx := historyKeeper(t)
		require.NoError(t, k.EpochConfigVersions.Set(ctx, 1, types.EpochConfigVersion{
			Version: 1, EffectiveEpoch: 1, EffectiveStartHeight: 0, EpochLengthBlocks: minLength,
		}))
		_, err := k.EpochStartHeight(ctx, 1)
		require.ErrorIs(t, err, types.ErrInvalidState)
	})
}

// TestGoverningVersionIsHeldToTheRatifiedBound covers a stored length outside
// [360, 720].
//
// No current code path can admit one, which is exactly why finding one on read is
// corruption rather than history: the row is not the row that was written, and
// the geometry it describes was never ratified.
func TestGoverningVersionIsHeldToTheRatifiedBound(t *testing.T) {
	for _, tc := range []struct {
		name   string
		length uint64
	}{
		{name: "below the ratified minimum", length: minLength - 1},
		{name: "above the ratified maximum", length: maxLength + 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx := historyKeeper(t, anchor(1, tc.length))
			_, err := k.EpochStartHeight(ctx, 1)
			require.ErrorIs(t, err, types.ErrInvalidState)
			require.NotErrorIs(t, err, types.ErrEpochConfigNotFound)
		})
	}
}

// TestGoverningVersionValidatesItsAdjacentHistoryEdge covers the one relational
// property a single row cannot carry: that it starts where its predecessor's
// recurrence puts it, and that it advances the version number.
//
// The check is deliberately local. It reads the immediately preceding row, not
// the whole history, so its cost does not grow with chain age.
func TestGoverningVersionValidatesItsAdjacentHistoryEdge(t *testing.T) {
	t.Run("a discontinuous successor is corruption", func(t *testing.T) {
		k, ctx := historyKeeper(t,
			anchor(1, minLength),
			// v1 places epoch 5 at height 1441; this row claims 2000.
			types.EpochConfigVersion{Version: 2, EffectiveEpoch: 5, EffectiveStartHeight: 2000, EpochLengthBlocks: maxLength},
		)
		_, err := k.EpochStartHeight(ctx, 5)
		require.ErrorIs(t, err, types.ErrInvalidState)
		require.NotErrorIs(t, err, types.ErrEpochConfigNotFound)
	})

	t.Run("a non-advancing version number is corruption", func(t *testing.T) {
		k, ctx := historyKeeper(t,
			anchor(1, minLength),
			// Continuous, but reuses version 1: "latest" becomes ambiguous.
			types.EpochConfigVersion{Version: 1, EffectiveEpoch: 5, EffectiveStartHeight: 1441, EpochLengthBlocks: maxLength},
		)
		_, err := k.EpochStartHeight(ctx, 5)
		require.ErrorIs(t, err, types.ErrInvalidState)
	})

	t.Run("a malformed predecessor is corruption even when the governing row is clean", func(t *testing.T) {
		k, ctx := historyKeeper(t,
			types.EpochConfigVersion{Version: 2, EffectiveEpoch: 5, EffectiveStartHeight: 1441, EpochLengthBlocks: maxLength},
		)
		// The predecessor is written under a key that contradicts its own record.
		require.NoError(t, k.EpochConfigVersions.Set(ctx, 1, types.EpochConfigVersion{
			Version: 1, EffectiveEpoch: 2, EffectiveStartHeight: 1, EpochLengthBlocks: minLength,
		}))
		_, err := k.EpochStartHeight(ctx, 5)
		require.ErrorIs(t, err, types.ErrInvalidState)
	})

	t.Run("the earliest row has no edge to check", func(t *testing.T) {
		k, ctx := historyKeeper(t, anchor(1, minLength))
		start, err := k.EpochStartHeight(ctx, 1)
		require.NoError(t, err)
		require.Equal(t, uint64(1), start)
	})
}

// TestVerifyCurrentEpochAnchorRejectsAContradictoryState covers the coherence
// rule between stored state and derived geometry.
func TestVerifyCurrentEpochAnchorRejectsAContradictoryState(t *testing.T) {
	params := types.DefaultParams()
	params.EpochLengthBlocks = minLength
	k, ctx, _ := setupAccountingKeeper(t, &coreSlotKeeperMock{}, 1, params)

	// The history says epoch 2 begins at height 361; the state claims 999.
	state, err := k.GetState(ctx)
	require.NoError(t, err)
	state.CurrentEpoch = 2
	state.CurrentEpochStartHeight = 999
	require.NoError(t, k.SetState(ctx, state))

	// ShouldFinalizeAtHeight is the consequential caller: it decides whether a
	// monetary transition runs this block.
	_, err = k.ShouldFinalizeAtHeight(ctx, 720)
	require.ErrorIs(t, err, types.ErrInvalidState)
	require.Contains(t, err.Error(), "canonical configuration history places it at height 361")
}

// TestEpochGeometryChecksArithmetic proves the recurrence refuses to wrap.
//
// Heights and epochs are unsigned, so an unchecked multiply or add would not
// fail here — it would name a plausible, wrong block.
//
// The ratified length bound does not remove any of these: it bounds the
// multiplier, not the epoch number it is multiplied by, and it says nothing at
// all about the anchor height.
func TestEpochGeometryChecksArithmetic(t *testing.T) {
	const maxUint64 = ^uint64(0)

	t.Run("start height multiplication overflows", func(t *testing.T) {
		// An admissible length still overflows once the elapsed epoch count is
		// large enough.
		k, ctx := historyKeeper(t, anchor(1, maxLength))
		_, err := k.EpochStartHeight(ctx, maxUint64/2)
		require.ErrorIs(t, err, types.ErrInvalidState)
	})

	t.Run("start height addition overflows", func(t *testing.T) {
		k, ctx := historyKeeper(t, anchor(maxUint64-5, minLength))
		_, err := k.EpochStartHeight(ctx, 2)
		require.ErrorIs(t, err, types.ErrInvalidState)
	})

	t.Run("the epoch successor is not representable", func(t *testing.T) {
		k, ctx := historyKeeper(t, anchor(1, minLength))
		_, err := k.EpochEndHeight(ctx, maxUint64)
		require.ErrorIs(t, err, types.ErrInvalidState)
	})
}

// TestScheduledEpochConfigProjection covers the query-side projection, which is
// the only place the ordered schedule affects a boundary.
func TestScheduledEpochConfigProjection(t *testing.T) {
	k, ctx := historyKeeper(t, anchor(1, minLength))
	// Two distinct future effective epochs coexist.
	require.NoError(t, k.ScheduledEpochConfigs.Set(ctx, 3, types.ScheduledEpochConfig{EffectiveEpoch: 3, EpochLengthBlocks: maxLength}))
	require.NoError(t, k.ScheduledEpochConfigs.Set(ctx, 5, types.ScheduledEpochConfig{EffectiveEpoch: 5, EpochLengthBlocks: minLength}))

	for _, tc := range []struct{ epoch, start uint64 }{
		{epoch: 1, start: 1},
		{epoch: 2, start: 361},
		{epoch: 3, start: 721},  // its own start is set by epoch 2's length, not by the schedule at 3
		{epoch: 4, start: 1441}, // now 720-block epochs
		{epoch: 5, start: 2161},
		{epoch: 6, start: 2521}, // now 360-block epochs again
	} {
		start, err := k.ProjectEpochStartHeight(ctx, tc.epoch, 100)
		require.NoError(t, err)
		require.Equalf(t, tc.start, start, "epoch %d", tc.epoch)
	}

	t.Run("replacing a scheduled value changes only later boundaries", func(t *testing.T) {
		require.NoError(t, k.ScheduledEpochConfigs.Set(ctx, 3, types.ScheduledEpochConfig{EffectiveEpoch: 3, EpochLengthBlocks: 400}))
		// Epoch 3's own start is unchanged because it is fixed by epoch 2.
		start, err := k.ProjectEpochStartHeight(ctx, 3, 100)
		require.NoError(t, err)
		require.Equal(t, uint64(721), start)
		// Epoch 4 moves: the replacement takes effect from epoch 3 onward. A cached
		// future start height would have kept the stale value here.
		start, err = k.ProjectEpochStartHeight(ctx, 4, 100)
		require.NoError(t, err)
		require.Equal(t, uint64(1121), start)
	})

	t.Run("beyond the horizon is refused, never clamped", func(t *testing.T) {
		_, err := k.ProjectEpochStartHeight(ctx, 10_000, 5)
		require.ErrorIs(t, err, types.ErrEpochConfigNotFound)
	})

	t.Run("an inadmissible scheduled length is refused, not projected", func(t *testing.T) {
		k, ctx := historyKeeper(t, anchor(1, minLength))
		require.NoError(t, k.ScheduledEpochConfigs.Set(ctx, 2, types.ScheduledEpochConfig{
			EffectiveEpoch: 2, EpochLengthBlocks: minLength - 1,
		}))
		_, err := k.ProjectEpochStartHeight(ctx, 3, 100)
		require.ErrorIs(t, err, types.ErrInvalidState)
	})

	t.Run("a schedule row stored under the wrong key is corruption", func(t *testing.T) {
		k, ctx := historyKeeper(t, anchor(1, minLength))
		require.NoError(t, k.ScheduledEpochConfigs.Set(ctx, 2, types.ScheduledEpochConfig{
			EffectiveEpoch: 7, EpochLengthBlocks: maxLength,
		}))
		_, err := k.ProjectEpochStartHeight(ctx, 3, 100)
		require.ErrorIs(t, err, types.ErrInvalidState)
	})
}

// continuousHistory is a three-version history satisfying the §11 continuity
// equation at every edge, with every length inside the ratified interval.
//
//	v1 epoch 1  @ height 1     length 360   -> epochs 1-4
//	v2 epoch 5  @ height 1441  length 720   -> epochs 5-8
//	v3 epoch 9  @ height 4321  length 360
func continuousHistory() []types.EpochConfigVersion {
	return []types.EpochConfigVersion{
		anchor(1, minLength),
		{Version: 2, EffectiveEpoch: 5, EffectiveStartHeight: 1441, EpochLengthBlocks: maxLength},
		{Version: 3, EffectiveEpoch: 9, EffectiveStartHeight: 4321, EpochLengthBlocks: minLength},
	}
}
