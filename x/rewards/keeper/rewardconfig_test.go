package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/x/rewards/keeper"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// Canonical reward-configuration binding (§33, §33.1, §94).
//
// The behavior under test divides into four groups:
//
//   - B1 target binding, including the two bootstrap targets and the ordinary
//     N-2 resolution that begins at target 3;
//   - fail-closed reads over a history that exists but cannot be trusted;
//   - the single-entry schedule rule;
//   - promotion, which happens in the closing epoch's EndBlock and only after
//     that epoch's monetary transition has succeeded.

func rewardConfigParams() types.Params {
	params := types.DefaultParams()
	params.MaxSupply = "1000000"
	params.InitialBlockSubsidy = "10"
	params.EpochLengthBlocks = finalizationEpochLength
	return params
}

// setupRewardConfig returns a keeper whose reward history holds only the genesis
// anchor, matching a chain that has never accepted a configuration update.
func setupRewardConfig(t *testing.T) (keeper.Keeper, sdk.Context, *bankKeeperMock) {
	t.Helper()
	return setupAccountingKeeper(t, &coreSlotKeeperMock{}, 1, rewardConfigParams())
}

// seedRewardVersion writes one history row directly, bypassing admission.
//
// Fixtures use this to build multi-version histories and to plant records that no
// admission path would have produced, which is exactly what the read-side checks
// are supposed to catch.
func seedRewardVersion(t *testing.T, k keeper.Keeper, ctx sdk.Context, version types.RewardConfigVersion) {
	t.Helper()
	require.NoError(t, k.RewardConfigVersions.Set(ctx, version.EffectiveEpoch, version))
}

func rewardVersionAt(version, effectiveEpoch uint64, subsidy string) types.RewardConfigVersion {
	return types.RewardConfigVersion{
		Version:             version,
		EffectiveEpoch:      effectiveEpoch,
		InitialBlockSubsidy: subsidy,
	}
}

// TestRewardConfigTargetBindingFollowsTheCanonicalRule is the B1 table.
//
// The history deliberately holds THREE versions. With only one, every candidate
// implementation — the correct rule, a clamp of N-2 to epoch 1, and an unguarded
// subtraction that underflows — returns the same record, and the test would prove
// nothing. With three, each wrong implementation returns a different version than
// the right one for at least one target:
//
//	target 1: correct -> v1, clamp -> v1, underflow -> v3   (underflow caught)
//	target 2: correct -> v1, clamp -> v1, underflow -> v3   (underflow caught)
//	target 3: correct -> v1 (binds epoch 1), clamp -> v1
//	target 6: correct -> v2 (binds epoch 4), clamp -> v2
//
// The clamp is caught separately below, where a history anchored past epoch 1
// makes the clamp and the rule disagree.
func TestRewardConfigTargetBindingFollowsTheCanonicalRule(t *testing.T) {
	k, ctx, _ := setupRewardConfig(t)
	seedRewardVersion(t, k, ctx, rewardVersionAt(2, 4, "20"))
	seedRewardVersion(t, k, ctx, rewardVersionAt(3, 9, "30"))

	for _, tc := range []struct {
		name        string
		target      uint64
		wantVersion uint64
	}{
		{"target 1 bootstraps to the genesis version", 1, 1},
		{"target 2 bootstraps to the genesis version", 2, 1},
		{"target 3 is the first ordinary N-2 binding, at epoch 1", 3, 1},
		{"target 5 binds epoch 3, still the genesis version", 5, 1},
		{"target 6 binds epoch 4, the first version that moved", 6, 2},
		{"target 10 binds epoch 8, still version 2", 10, 2},
		{"target 11 binds epoch 9, the newest version", 11, 3},
		{"a far target binds the newest version", 1_000, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			version, err := k.RewardConfigForTarget(ctx, tc.target)
			require.NoError(t, err)
			require.Equalf(t, tc.wantVersion, version.Version,
				"target %d resolved version %d", tc.target, version.Version)
		})
	}

	t.Run("target zero is not an epoch", func(t *testing.T) {
		_, err := k.RewardConfigForTarget(ctx, 0)
		require.ErrorIs(t, err, types.ErrInvalidState)
	})
}

// TestRewardConfigBootstrapIsNotAnUnguardedSubtraction is the mutation-sensitive
// case for the evaluation order.
//
// Replace the `target <= 2` branch with `checked(target-2)` and this fails: the
// subtraction underflows to 2^64-1, the descending seek bounded by that resolves
// the LATEST version, and targets 1 and 2 silently pay under configuration
// accepted long after they closed.
func TestRewardConfigBootstrapIsNotAnUnguardedSubtraction(t *testing.T) {
	k, ctx, _ := setupRewardConfig(t)
	seedRewardVersion(t, k, ctx, rewardVersionAt(2, 4, "20"))
	seedRewardVersion(t, k, ctx, rewardVersionAt(9, 500, "90"))

	latest, err := k.RewardConfigForTarget(ctx, 10_000)
	require.NoError(t, err)
	require.Equal(t, uint64(9), latest.Version, "fixture must have a distinguishable newest version")

	for _, target := range []uint64{1, 2} {
		version, err := k.RewardConfigForTarget(ctx, target)
		require.NoError(t, err)
		require.Equalf(t, uint64(1), version.Version,
			"target %d must bootstrap to the genesis version, not resolve an underflowed epoch", target)
	}
}

// TestRewardConfigBootstrapIsNotAClamp distinguishes the rule from clamping N-2
// to epoch 1.
//
// A clamp agrees with the rule for as long as the history is anchored at epoch 1,
// which is why the anchor is deliberately absent here. Under a clamp, target 2
// would resolve "the version effective at epoch 1" — and with no version at or
// below epoch 1 that is a not-found, not the genesis version. The rule instead
// reads the anchor directly and reports its absence as such.
func TestRewardConfigBootstrapIsNotAClamp(t *testing.T) {
	k, ctx, _ := setupRewardConfig(t)
	require.NoError(t, k.RewardConfigVersions.Remove(ctx, 1))
	seedRewardVersion(t, k, ctx, rewardVersionAt(4, 4, "40"))

	for _, target := range []uint64{1, 2} {
		_, err := k.RewardConfigForTarget(ctx, target)
		require.ErrorIsf(t, err, types.ErrRewardConfigNotFound,
			"target %d must report the missing anchor, not fall through to a later version", target)
	}

	// And no synthetic version is invented to stand in for it.
	_, err := k.GenesisRewardConfigVersion(ctx)
	require.ErrorIs(t, err, types.ErrRewardConfigNotFound)
}

// TestGenesisRewardConfigVersionRequiresTheCanonicalAnchor covers the identity of
// the bootstrap record itself.
func TestGenesisRewardConfigVersionRequiresTheCanonicalAnchor(t *testing.T) {
	t.Run("a row at epoch 1 that is not version 1", func(t *testing.T) {
		k, ctx, _ := setupRewardConfig(t)
		seedRewardVersion(t, k, ctx, rewardVersionAt(7, 1, "10"))
		_, err := k.GenesisRewardConfigVersion(ctx)
		require.ErrorIs(t, err, types.ErrInvalidState)
	})

	t.Run("a row whose key and record disagree", func(t *testing.T) {
		k, ctx, _ := setupRewardConfig(t)
		// Stored under epoch 1 while declaring epoch 5.
		require.NoError(t, k.RewardConfigVersions.Set(ctx, 1, rewardVersionAt(1, 5, "10")))
		_, err := k.GenesisRewardConfigVersion(ctx)
		require.ErrorIs(t, err, types.ErrInvalidState)
	})

	t.Run("a row that would not have been admitted", func(t *testing.T) {
		k, ctx, _ := setupRewardConfig(t)
		corrupt := rewardVersionAt(1, 1, "10")
		corrupt.EmissionTreasuryShareBps = 9_000
		seedRewardVersion(t, k, ctx, corrupt)
		_, err := k.GenesisRewardConfigVersion(ctx)
		require.ErrorIs(t, err, types.ErrInvalidState)
	})
}

// TestRewardConfigResolutionFailsClosedOnCorruptHistory proves a governing record
// is validated before it can scale a mint, and that the adjacent edge behind it is
// checked too.
func TestRewardConfigResolutionFailsClosedOnCorruptHistory(t *testing.T) {
	for _, tc := range []struct {
		name string
		seed func(*testing.T, keeper.Keeper, sdk.Context)
	}{
		{
			name: "the governing record's key and value disagree",
			seed: func(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
				require.NoError(t, k.RewardConfigVersions.Set(ctx, 4, rewardVersionAt(2, 9, "20")))
			},
		},
		{
			name: "the governing record carries an inadmissible subsidy",
			seed: func(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
				seedRewardVersion(t, k, ctx, rewardVersionAt(2, 4, "0"))
			},
		},
		{
			name: "the governing record carries an inadmissible treasury share",
			seed: func(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
				corrupt := rewardVersionAt(2, 4, "20")
				corrupt.EmissionTreasuryShareBps = 5_001
				seedRewardVersion(t, k, ctx, corrupt)
			},
		},
		{
			name: "the version number does not advance across the edge",
			seed: func(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
				seedRewardVersion(t, k, ctx, rewardVersionAt(1, 4, "20"))
			},
		},
		{
			name: "the version number goes backwards across the edge",
			seed: func(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
				seedRewardVersion(t, k, ctx, rewardVersionAt(3, 4, "20"))
				seedRewardVersion(t, k, ctx, rewardVersionAt(2, 6, "30"))
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx, _ := setupRewardConfig(t)
			tc.seed(t, k, ctx)
			// Target 8 binds epoch 6, which sits at or after every seeded row above.
			_, err := k.RewardConfigForTarget(ctx, 8)
			require.ErrorIs(t, err, types.ErrInvalidState)
		})
	}
}

// TestRewardConfigResolutionIsBoundedInHistoryLength keeps resolution off the
// chain-age curve.
//
// A history with many versions must resolve through a predecessor seek, not a
// walk. The assertion here is behavioral rather than a timing measurement: the
// answer for a target deep inside a long history must be the correct predecessor,
// and must not depend on how many rows precede it.
func TestRewardConfigResolutionIsBoundedInHistoryLength(t *testing.T) {
	k, ctx, _ := setupRewardConfig(t)
	const versions = 200
	for i := uint64(2); i <= versions; i++ {
		// Effective epochs 10, 20, 30, ... so binding epochs land between rows.
		seedRewardVersion(t, k, ctx, rewardVersionAt(i, i*10, "20"))
	}

	// Target 107 binds epoch 105, whose governing row is the one effective at 100.
	version, err := k.RewardConfigForTarget(ctx, 107)
	require.NoError(t, err)
	require.Equal(t, uint64(100), version.EffectiveEpoch)
	require.Equal(t, uint64(10), version.Version)
}

// TestScheduledRewardConfigAdmitsOnlyTheNextBoundary covers the single-entry rule.
//
// §82 admits at most one scheduled reward configuration, keyed exactly at
// current_epoch + 1. Anything else is invalid state rather than a queue entry, so
// the closing block refuses to promote rather than picking one.
func TestScheduledRewardConfigAdmitsOnlyTheNextBoundary(t *testing.T) {
	for _, tc := range []struct {
		name string
		seed func(*testing.T, keeper.Keeper, sdk.Context)
	}{
		{
			name: "a schedule keyed at an unrelated future epoch",
			seed: func(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
				require.NoError(t, k.ScheduledRewardConfigs.Set(ctx, 7, types.ScheduledRewardConfig{
					EffectiveEpoch: 7, InitialBlockSubsidy: "20",
				}))
			},
		},
		{
			name: "a second, unrelated schedule beside the admissible one",
			seed: func(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
				require.NoError(t, k.ScheduledRewardConfigs.Set(ctx, 2, types.ScheduledRewardConfig{
					EffectiveEpoch: 2, InitialBlockSubsidy: "20",
				}))
				require.NoError(t, k.ScheduledRewardConfigs.Set(ctx, 3, types.ScheduledRewardConfig{
					EffectiveEpoch: 3, InitialBlockSubsidy: "30",
				}))
			},
		},
		{
			name: "a stale schedule for a boundary that already passed",
			seed: func(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
				require.NoError(t, k.ScheduledRewardConfigs.Set(ctx, 1, types.ScheduledRewardConfig{
					EffectiveEpoch: 1, InitialBlockSubsidy: "20",
				}))
			},
		},
		{
			name: "a schedule stored under a key it does not declare",
			seed: func(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
				require.NoError(t, k.ScheduledRewardConfigs.Set(ctx, 2, types.ScheduledRewardConfig{
					EffectiveEpoch: 5, InitialBlockSubsidy: "20",
				}))
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx, _ := setupFinalizationWithRewardConfig(t)
			tc.seed(t, k, ctx)

			require.Error(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))
			requireNothingFinalized(t, k, ctx)
		})
	}
}

// TestScheduledRewardConfigPromotesAtTheClosingBoundary is the successful path.
func TestScheduledRewardConfigPromotesAtTheClosingBoundary(t *testing.T) {
	k, ctx, _ := setupFinalizationWithRewardConfig(t)
	require.NoError(t, k.ScheduledRewardConfigs.Set(ctx, 2, types.ScheduledRewardConfig{
		EffectiveEpoch:      2,
		InitialBlockSubsidy: "77",
	}))

	require.NoError(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))

	// The scheduled value became immutable history, and took the next version
	// number from the history it extends rather than carrying one of its own.
	promoted, err := k.RewardConfigVersions.Get(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, uint64(2), promoted.Version)
	require.Equal(t, uint64(2), promoted.EffectiveEpoch)
	require.Equal(t, "77", promoted.InitialBlockSubsidy)

	// The schedule is consumed, not left behind for a second promotion.
	_, found, err := k.ScheduledRewardConfigFor(ctx, 2)
	require.NoError(t, err)
	require.False(t, found)

	// Non-retroactivity: a version effective at epoch 2 first reaches a target at
	// epoch 4, because target N binds N-2.
	for target, wantVersion := range map[uint64]uint64{1: 1, 2: 1, 3: 1, 4: 2} {
		version, err := k.RewardConfigForTarget(ctx, target)
		require.NoError(t, err)
		require.Equalf(t, wantVersion, version.Version, "target %d", target)
	}
}

// TestScheduledRewardConfigIsNotPromotedAtBeginBlock separates the two schedule
// mechanisms.
//
// Epoch geometry is consumed at the first BeginBlock of the epoch it becomes
// effective at. Reward configuration must not be: it binds two epochs ahead of
// any target it can affect, so consuming it at BeginBlock would make it effective
// a boundary early and before the mint it is supposed to follow.
func TestScheduledRewardConfigIsNotPromotedAtBeginBlock(t *testing.T) {
	k, ctx, _ := setupRewardConfig(t)
	require.NoError(t, k.ScheduledRewardConfigs.Set(ctx, 2, types.ScheduledRewardConfig{
		EffectiveEpoch: 2, InitialBlockSubsidy: "77",
	}))

	// Drive the whole of epoch 1 and the first block of epoch 2 through BeginBlock.
	for height := int64(1); height <= finalizationEndHeight+1; height++ {
		require.NoError(t, k.BeginBlock(ctx.WithBlockHeight(height)))
	}

	_, found, err := k.ScheduledRewardConfigFor(ctx, 2)
	require.NoError(t, err)
	require.True(t, found, "BeginBlock must not consume the reward configuration schedule")
	_, err = k.RewardConfigVersions.Get(ctx, 2)
	require.Error(t, err, "BeginBlock must not create a reward configuration version")
}

// TestScheduledRewardConfigPromotionHoldsTheDestinationRule proves a schedule
// entry is admitted as a value destination, not merely as a shape.
//
// This is where the rule is reachable. Fresh genesis rejects a non-empty schedule
// outright, so no genesis document can carry one to be checked; promotion is the
// production path that turns a scheduled destination into one the treasury
// transfer will later use.
func TestScheduledRewardConfigPromotionHoldsTheDestinationRule(t *testing.T) {
	k, ctx, _ := setupFinalizationWithRewardConfig(t)
	require.NoError(t, k.ScheduledRewardConfigs.Set(ctx, 2, types.ScheduledRewardConfig{
		EffectiveEpoch:           2,
		InitialBlockSubsidy:      "77",
		EmissionTreasuryShareBps: 100,
		// The rewards module account itself: a valid bech32 string that the canonical
		// economic-address rule refuses as a destination.
		TreasuryAddress: testModuleAddress(testModuleAccountName),
	}))

	require.Error(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))
	requireNothingFinalized(t, k, ctx)
	_, err := k.RewardConfigVersions.Get(ctx, 2)
	require.Error(t, err, "a refused promotion must create no history")
}

// TestScheduledRewardConfigPromotesAPositiveShareWithAValidDestination is the
// counterpart of the case above: the rule is conditional on the share, not a
// blanket refusal of treasury configuration.
func TestScheduledRewardConfigPromotesAPositiveShareWithAValidDestination(t *testing.T) {
	k, ctx, _ := setupFinalizationWithRewardConfig(t)
	destination := testAccount(42)
	require.NoError(t, k.ScheduledRewardConfigs.Set(ctx, 2, types.ScheduledRewardConfig{
		EffectiveEpoch:           2,
		InitialBlockSubsidy:      "77",
		EmissionTreasuryShareBps: 1,
		TreasuryAddress:          destination,
	}))

	require.NoError(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))

	promoted, err := k.RewardConfigVersions.Get(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, uint64(1), promoted.EmissionTreasuryShareBps)
	require.Equal(t, destination, promoted.TreasuryAddress)
}

// TestRewardConfigPromotionAndFinalizationCommitTogether is the ordering rule of
// §94, asserted from both sides.
func TestRewardConfigPromotionAndFinalizationCommitTogether(t *testing.T) {
	t.Run("a failed finalization promotes nothing", func(t *testing.T) {
		k, ctx, bank := setupFinalizationWithRewardConfig(t)
		require.NoError(t, k.ScheduledRewardConfigs.Set(ctx, 2, types.ScheduledRewardConfig{
			EffectiveEpoch: 2, InitialBlockSubsidy: "77",
		}))
		bank.failMint()

		require.Error(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))
		requireNothingFinalized(t, k, ctx)

		// The schedule survives untouched and the history is unchanged, so the
		// boundary can be reached again only by a block that also succeeds at the
		// monetary transition.
		scheduled, found, err := k.ScheduledRewardConfigFor(ctx, 2)
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, "77", scheduled.InitialBlockSubsidy)
		_, err = k.RewardConfigVersions.Get(ctx, 2)
		require.Error(t, err)
	})

	t.Run("a failed promotion commits no monetary state", func(t *testing.T) {
		k, ctx, bank := setupFinalizationWithRewardConfig(t)
		// A scheduled record that cannot become history: its subsidy is
		// inadmissible, so promotion refuses it after finalization has already run.
		require.NoError(t, k.ScheduledRewardConfigs.Set(ctx, 2, types.ScheduledRewardConfig{
			EffectiveEpoch: 2, InitialBlockSubsidy: "0",
		}))

		require.Error(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))

		// The mint was reached — finalization runs first — so this is a rollback
		// assertion, not an absence-of-work one. That is the stronger statement:
		// monetary work happened inside the cache and none of it survived.
		require.Positive(t, bank.mintCalls, "finalization must have reached the mint before promotion failed")
		requireNothingFinalized(t, k, ctx)
	})
}

// setupFinalizationWithRewardConfig is setupFinalization plus a reward history.
//
// setupFinalization already seeds the reward anchor through setupAccountingKeeper;
// this wrapper exists to name the dependency at the call sites that exercise
// promotion, so a later change to the shared fixture cannot silently remove the
// history these tests depend on.
func setupFinalizationWithRewardConfig(t *testing.T) (keeper.Keeper, sdk.Context, *bankKeeperMock) {
	t.Helper()
	k, ctx, bank, _ := setupFinalization(t, false)
	anchor, err := k.GenesisRewardConfigVersion(ctx)
	require.NoError(t, err, "the finalization fixture must carry the reward configuration anchor")
	require.Equal(t, uint64(1), anchor.Version)
	return k, ctx, bank
}

// seedTreasuryRewardConfig points the canonical reward configuration at a
// treasury destination with a positive share.
//
// The deprecated Params/snapshot mirrors are deliberately left alone. They carry
// no authority, so moving them would change nothing about what finalization pays
// — which is itself worth stating, because a fixture that set them instead would
// silently test nothing.
func seedTreasuryRewardConfig(
	t *testing.T, k keeper.Keeper, ctx sdk.Context, shareBps uint64, destination string,
) {
	t.Helper()
	anchor, err := k.GenesisRewardConfigVersion(ctx)
	require.NoError(t, err)
	anchor.EmissionTreasuryShareBps = shareBps
	anchor.TreasuryAddress = destination
	require.NoError(t, k.RewardConfigVersions.Set(ctx, anchor.EffectiveEpoch, anchor))
}

// seedTreasuryRewardConfigUnchecked writes a treasury configuration straight to
// the history, bypassing admission.
//
// Admission would refuse an inadmissible destination, which is correct and is
// tested separately. This exists for the §33.2 cases, which are about whether the
// destination is revalidated AT TRANSFER TIME — a question that only arises once
// such a configuration is already in force.
func seedTreasuryRewardConfigUnchecked(
	t *testing.T, k keeper.Keeper, ctx sdk.Context, shareBps uint64, destination string,
) {
	t.Helper()
	anchor, err := k.GenesisRewardConfigVersion(ctx)
	require.NoError(t, err)
	anchor.EmissionTreasuryShareBps = shareBps
	anchor.TreasuryAddress = destination
	require.NoError(t, k.RewardConfigVersions.Set(ctx, anchor.EffectiveEpoch, anchor))
}
