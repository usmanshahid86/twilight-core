package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"

	"github.com/twilight-project/twilight-core/x/mining/keeper"
	"github.com/twilight-project/twilight-core/x/mining/types"
)

// Exact lookup of a configuration version.
//
// # The distinction everything here defends
//
// Version numbers are unique and strictly increasing but NOT contiguous, and the
// canonical history is keyed by effective epoch. A derived version -> epoch index
// makes lookup by version cheap, and is an accelerator only.
//
// So two situations present identically from the index alone:
//
//	an intentional gap        no record was ever assigned that number
//	a lost index entry        a canonical record exists and its entry is missing
//
// The first is NotFound. The second is corruption. Collapsing them would let one
// corrupted entry make an existing record publicly invisible — and invisible is
// precisely what a caller reconciling its own state cannot distinguish from
// never-existed. Only canonical adjacency can tell them apart.

// settlementParamsAt writes a canonical settlement-parameter record and its index
// entry, the way promotion would.
func settlementParamsAt(t *testing.T, k keeper.Keeper, ctx sdk.Context, version, epoch uint64) {
	t.Helper()
	require.NoError(t, k.SettlementParamsVersions.Set(ctx, epoch, types.SettlementParamsVersion{
		Version:                  version,
		EffectiveEpoch:           epoch,
		SettlementWindowEpochs:   types.DefaultSettlementWindowEpochs,
		MaxRecipientsPerChunk:    types.DefaultMaxRecipientsPerChunk,
		MaxChunksPerSettlement:   types.DefaultMaxChunksPerSettlement,
		MinRecipientPayoutAmount: types.DefaultMinRecipientPayoutAmount,
	}))
	require.NoError(t, k.SettlementParamsVersionIndex.Set(ctx, version, epoch))
}

// TestExactVersionLookupServesAnIndexedRecord is the ordinary hit.
func TestExactVersionLookupServesAnIndexedRecord(t *testing.T) {
	q, _, ctx, _ := queryFixture(t)

	res, err := q.SettlementParamsVersion(ctx, &types.QuerySettlementParamsVersionRequest{Version: 1})
	require.NoError(t, err)
	require.Equal(t, uint64(1), res.Version.Version)
	require.Equal(t, uint64(1), res.Version.EffectiveEpoch)
}

// TestExactVersionLookupClassifiesTheEdges pins every non-corrupt answer.
func TestExactVersionLookupClassifiesTheEdges(t *testing.T) {
	q, _, ctx, _ := queryFixture(t)

	t.Run("version zero is InvalidArgument", func(t *testing.T) {
		_, err := q.SettlementParamsVersion(ctx, &types.QuerySettlementParamsVersionRequest{Version: 0})
		require.Equal(t, codes.InvalidArgument, grpcCode(t, err))
	})

	t.Run("above the latest assigned version is NotFound", func(t *testing.T) {
		_, err := q.SettlementParamsVersion(ctx, &types.QuerySettlementParamsVersionRequest{Version: 99})
		require.Equal(t, codes.NotFound, grpcCode(t, err))
	})
}

// TestAProvenIntentionalGapIsNotFound is the positive half of the distinction.
//
// A history of v1 and v5 with nothing between them: canonical adjacency proves v5
// immediately follows v1, so no record can carry v3 and the absence is real.
func TestAProvenIntentionalGapIsNotFound(t *testing.T) {
	q, k, ctx, _ := queryFixture(t)
	settlementParamsAt(t, k, ctx, 5, 10)

	for _, missing := range []uint64{2, 3, 4} {
		_, err := q.SettlementParamsVersion(ctx,
			&types.QuerySettlementParamsVersionRequest{Version: missing})
		require.Equalf(t, codes.NotFound, grpcCode(t, err),
			"version %d falls in a proven gap", missing)
	}

	// The endpoints themselves still resolve.
	res, err := q.SettlementParamsVersion(ctx, &types.QuerySettlementParamsVersionRequest{Version: 5})
	require.NoError(t, err)
	require.Equal(t, uint64(10), res.Version.EffectiveEpoch)
}

// TestALostIndexEntryIsCorruptionNotAGap is the negative half, and the reason the
// canonical adjacency step exists.
//
// The history is v1, v3, v5 — v3 genuinely exists — but its index entry has been
// lost. From the index alone this is indistinguishable from the gap above: the
// neighbors are v1 and v5 either way. Stepping once through the canonical history
// reveals a record between them, so the absence cannot be proven and the answer is
// Internal.
func TestALostIndexEntryIsCorruptionNotAGap(t *testing.T) {
	q, k, ctx, _ := queryFixture(t)
	settlementParamsAt(t, k, ctx, 3, 5)
	settlementParamsAt(t, k, ctx, 5, 10)
	// Lose only the derived entry. The canonical record stays.
	require.NoError(t, k.SettlementParamsVersionIndex.Remove(ctx, 3))

	_, err := q.SettlementParamsVersion(ctx, &types.QuerySettlementParamsVersionRequest{Version: 3})
	require.Equal(t, codes.Internal, grpcCode(t, err),
		"an existing record must never be reported absent because its index entry is gone")

	// The damage is contained but it is not narrow: v2 and v4 are genuinely absent,
	// yet neither absence is PROVABLE any more. Both proofs would have to bracket
	// against v3, and v3 is exactly the record the index can no longer locate — so
	// the indexed neighbors v1 and v5 are not canonically adjacent and the step
	// refuses. Reporting NotFound for either would be asserting an absence the chain
	// can no longer demonstrate.
	for _, unprovable := range []uint64{2, 4} {
		_, err = q.SettlementParamsVersion(ctx,
			&types.QuerySettlementParamsVersionRequest{Version: unprovable})
		require.Equalf(t, codes.Internal, grpcCode(t, err),
			"v%d cannot be proven absent while v3's index entry is lost", unprovable)
	}

	// Classification that does not depend on the damaged region still works, so the
	// fix is not simply "answer Internal whenever unsure".
	_, err = q.SettlementParamsVersion(ctx, &types.QuerySettlementParamsVersionRequest{Version: 99})
	require.Equal(t, codes.NotFound, grpcCode(t, err),
		"above the latest assigned version needs no gap proof")
	res, err := q.SettlementParamsVersion(ctx, &types.QuerySettlementParamsVersionRequest{Version: 5})
	require.NoError(t, err, "an intact indexed record still resolves")
	require.Equal(t, uint64(5), res.Version.Version)
}

// TestAMispointedIndexEntryIsCorruption covers the exact-hit cross-checks.
func TestAMispointedIndexEntryIsCorruption(t *testing.T) {
	t.Run("index points at an epoch holding a different version", func(t *testing.T) {
		q, k, ctx, _ := queryFixture(t)
		settlementParamsAt(t, k, ctx, 5, 10)
		// v5's entry now locates v1's record.
		require.NoError(t, k.SettlementParamsVersionIndex.Set(ctx, 5, 1))

		_, err := q.SettlementParamsVersion(ctx, &types.QuerySettlementParamsVersionRequest{Version: 5})
		require.Equal(t, codes.Internal, grpcCode(t, err))
	})

	t.Run("index points at an epoch holding no record", func(t *testing.T) {
		q, k, ctx, _ := queryFixture(t)
		settlementParamsAt(t, k, ctx, 5, 10)
		require.NoError(t, k.SettlementParamsVersionIndex.Set(ctx, 5, 77))

		_, err := q.SettlementParamsVersion(ctx, &types.QuerySettlementParamsVersionRequest{Version: 5})
		require.Equal(t, codes.Internal, grpcCode(t, err))
	})

	t.Run("record disagreeing with the epoch it is filed under", func(t *testing.T) {
		q, k, ctx, _ := queryFixture(t)
		require.NoError(t, k.SettlementParamsVersions.Set(ctx, 10, types.SettlementParamsVersion{
			Version: 5, EffectiveEpoch: 11,
			SettlementWindowEpochs:   types.DefaultSettlementWindowEpochs,
			MaxRecipientsPerChunk:    types.DefaultMaxRecipientsPerChunk,
			MaxChunksPerSettlement:   types.DefaultMaxChunksPerSettlement,
			MinRecipientPayoutAmount: types.DefaultMinRecipientPayoutAmount,
		}))
		require.NoError(t, k.SettlementParamsVersionIndex.Set(ctx, 5, 10))

		_, err := q.SettlementParamsVersion(ctx, &types.QuerySettlementParamsVersionRequest{Version: 5})
		require.Equal(t, codes.Internal, grpcCode(t, err))
	})
}

// TestALostOriginIndexEntryIsCorruption covers the prefix case.
//
// Every history begins at version 1, so a query below the smallest indexed version
// has no predecessor to prove anything against. That is a broken index rather than a
// prefix gap, and §14 forbids inventing prefix-gap semantics.
func TestALostOriginIndexEntryIsCorruption(t *testing.T) {
	q, k, ctx, _ := queryFixture(t)
	settlementParamsAt(t, k, ctx, 5, 10)
	require.NoError(t, k.SettlementParamsVersionIndex.Remove(ctx, 1))

	_, err := q.SettlementParamsVersion(ctx, &types.QuerySettlementParamsVersionRequest{Version: 3})
	require.Equal(t, codes.Internal, grpcCode(t, err))
}

// TestVersionLookupAppliesToEveryFamily keeps the three histories from drifting.
func TestVersionLookupAppliesToEveryFamily(t *testing.T) {
	q, _, ctx, _ := queryFixture(t)

	t.Run("distribution mode", func(t *testing.T) {
		res, err := q.DistributionModeVersion(ctx,
			&types.QueryDistributionModeVersionRequest{Version: 1})
		require.NoError(t, err)
		require.Equal(t, uint64(1), res.Version.Version)
		_, err = q.DistributionModeVersion(ctx, &types.QueryDistributionModeVersionRequest{Version: 0})
		require.Equal(t, codes.InvalidArgument, grpcCode(t, err))
		_, err = q.DistributionModeVersion(ctx, &types.QueryDistributionModeVersionRequest{Version: 99})
		require.Equal(t, codes.NotFound, grpcCode(t, err))
	})

	t.Run("selection params", func(t *testing.T) {
		res, err := q.SelectionParamsVersion(ctx,
			&types.QuerySelectionParamsVersionRequest{Version: 1})
		require.NoError(t, err)
		require.Equal(t, uint64(1), res.Version.Version)
		_, err = q.SelectionParamsVersion(ctx, &types.QuerySelectionParamsVersionRequest{Version: 0})
		require.Equal(t, codes.InvalidArgument, grpcCode(t, err))
		_, err = q.SelectionParamsVersion(ctx, &types.QuerySelectionParamsVersionRequest{Version: 99})
		require.Equal(t, codes.NotFound, grpcCode(t, err))
	})
}

// TestHistoryListingsAreOrderedAndFailClosed pins the listing contract.
func TestHistoryListingsAreOrderedAndFailClosed(t *testing.T) {
	t.Run("canonical ascending order", func(t *testing.T) {
		q, k, ctx, _ := queryFixture(t)
		settlementParamsAt(t, k, ctx, 5, 10)
		settlementParamsAt(t, k, ctx, 3, 5)

		res, err := q.SettlementParamsVersions(ctx, &types.QuerySettlementParamsVersionsRequest{})
		require.NoError(t, err)
		require.Len(t, res.Versions, 3)
		require.Equal(t, []uint64{1, 3, 5},
			[]uint64{res.Versions[0].Version, res.Versions[1].Version, res.Versions[2].Version})
	})

	t.Run("a malformed record fails the page rather than being skipped", func(t *testing.T) {
		q, k, ctx, _ := queryFixture(t)
		require.NoError(t, k.SettlementParamsVersions.Set(ctx, 5, types.SettlementParamsVersion{
			Version: 3, EffectiveEpoch: 5,
			SettlementWindowEpochs:   types.DefaultSettlementWindowEpochs,
			MaxRecipientsPerChunk:    types.DefaultMaxRecipientsPerChunk,
			MaxChunksPerSettlement:   0, // outside the ratified bound
			MinRecipientPayoutAmount: types.DefaultMinRecipientPayoutAmount,
		}))
		_, err := q.SettlementParamsVersions(ctx, &types.QuerySettlementParamsVersionsRequest{})
		require.Equal(t, codes.Internal, grpcCode(t, err),
			"a history returned with a row quietly omitted cannot be reconciled against")
	})
}

// TestAHistoryWithoutItsOriginIsCorrupt is A6-S1.
//
// Every family begins at version 1, effective from epoch 1, and that origin is
// canonical identity rather than convention. A decapitated history still answers
// perfectly well from its later rows — a seek finds the greatest key at or below
// what was asked for, and a self-valid row validates — so without an explicit anchor
// proof the surface would report it as healthy.
//
// Each subtest first shows the query succeeding with the anchor intact, so the
// refusal is demonstrably caused by the anchor and not by the damage incidentally
// breaking something else.
func TestAHistoryWithoutItsOriginIsCorrupt(t *testing.T) {
	// A self-valid later row, so the history can answer from it if nothing checks.
	seedLater := func(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
		t.Helper()
		settlementParamsAt(t, k, ctx, 5, 10)
	}

	t.Run("intact origin answers normally", func(t *testing.T) {
		q, k, ctx, _ := queryFixture(t)
		seedLater(t, k, ctx)
		res, err := q.SettlementParamsVersion(ctx, &types.QuerySettlementParamsVersionRequest{Version: 5})
		require.NoError(t, err)
		require.Equal(t, uint64(5), res.Version.Version)
		list, err := q.SettlementParamsVersions(ctx, &types.QuerySettlementParamsVersionsRequest{})
		require.NoError(t, err)
		require.Len(t, list.Versions, 2)
	})

	t.Run("a removed origin fails exact lookup and listing", func(t *testing.T) {
		q, k, ctx, _ := queryFixture(t)
		seedLater(t, k, ctx)
		require.NoError(t, k.SettlementParamsVersions.Remove(ctx, 1))

		_, err := q.SettlementParamsVersion(ctx, &types.QuerySettlementParamsVersionRequest{Version: 5})
		require.Equal(t, codes.Internal, grpcCode(t, err))
		_, err = q.SettlementParamsVersions(ctx, &types.QuerySettlementParamsVersionsRequest{})
		require.Equal(t, codes.Internal, grpcCode(t, err))
	})

	t.Run("a moved origin fails too", func(t *testing.T) {
		q, k, ctx, _ := queryFixture(t)
		// Version 1 relocated to epoch 3: self-consistent, but no longer the origin.
		require.NoError(t, k.SettlementParamsVersions.Remove(ctx, 1))
		settlementParamsAt(t, k, ctx, 1, 3)

		_, err := q.SettlementParamsVersion(ctx, &types.QuerySettlementParamsVersionRequest{Version: 1})
		require.Equal(t, codes.Internal, grpcCode(t, err))
	})

	t.Run("an origin carrying the wrong version fails", func(t *testing.T) {
		q, k, ctx, _ := queryFixture(t)
		// Epoch 1 holds a row numbered 4 rather than 1.
		require.NoError(t, k.SettlementParamsVersions.Set(ctx, 1, types.SettlementParamsVersion{
			Version: 4, EffectiveEpoch: 1,
			SettlementWindowEpochs:   types.DefaultSettlementWindowEpochs,
			MaxRecipientsPerChunk:    types.DefaultMaxRecipientsPerChunk,
			MaxChunksPerSettlement:   types.DefaultMaxChunksPerSettlement,
			MinRecipientPayoutAmount: types.DefaultMinRecipientPayoutAmount,
		}))
		_, err := q.SettlementParamsVersions(ctx, &types.QuerySettlementParamsVersionsRequest{})
		require.Equal(t, codes.Internal, grpcCode(t, err))
	})

	t.Run("every family is anchored", func(t *testing.T) {
		q, k, ctx, _ := queryFixture(t)
		require.NoError(t, k.DistributionModeVersions.Remove(ctx, 1))
		_, err := q.DistributionModeVersions(ctx, &types.QueryDistributionModeVersionsRequest{})
		require.Equal(t, codes.Internal, grpcCode(t, err))

		q, k, ctx, _ = queryFixture(t)
		require.NoError(t, k.SelectionParamsVersions.Remove(ctx, 1))
		_, err = q.SelectionParamsVersions(ctx, &types.QuerySelectionParamsVersionsRequest{})
		require.Equal(t, codes.Internal, grpcCode(t, err))
	})

	t.Run("numeric version gaps remain legal", func(t *testing.T) {
		q, k, ctx, _ := queryFixture(t)
		seedLater(t, k, ctx)
		// The origin is intact, so a gap between v1 and v5 is still an ordinary
		// absence rather than corruption.
		_, err := q.SettlementParamsVersion(ctx, &types.QuerySettlementParamsVersionRequest{Version: 3})
		require.Equal(t, codes.NotFound, grpcCode(t, err))
	})
}
