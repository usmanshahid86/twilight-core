package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"
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

// TestRequestClassificationPrecedesChainState is A6-R1.
//
// A malformed request stays malformed no matter what condition the chain is in.
// Answering Internal because the history behind a bad request happens to be damaged
// would send the caller to investigate the chain when the fault is in its own
// request — and, worse, would make the classification of an identical request depend
// on state the caller cannot see.
//
// The corrupt-origin variants are the point of this test. A valid request against a
// damaged history must still be Internal, so the two rules are genuinely ordered
// rather than one having replaced the other.
func TestRequestClassificationPrecedesChainState(t *testing.T) {
	// The three ways a family origin can be broken, each of which makes a VALID
	// request answer Internal.
	breakOrigin := map[string]func(*testing.T, keeper.Keeper, sdk.Context){
		"origin missing": func(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
			require.NoError(t, k.SettlementParamsVersions.Remove(ctx, 1))
			require.NoError(t, k.DistributionModeVersions.Remove(ctx, 1))
			require.NoError(t, k.SelectionParamsVersions.Remove(ctx, 1))
		},
		"origin carries the wrong version": func(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
			require.NoError(t, k.SettlementParamsVersions.Set(ctx, 1, types.SettlementParamsVersion{
				Version: 4, EffectiveEpoch: 1,
				SettlementWindowEpochs:   types.DefaultSettlementWindowEpochs,
				MaxRecipientsPerChunk:    types.DefaultMaxRecipientsPerChunk,
				MaxChunksPerSettlement:   types.DefaultMaxChunksPerSettlement,
				MinRecipientPayoutAmount: types.DefaultMinRecipientPayoutAmount,
			}))
			mode, err := k.DistributionModeVersions.Get(ctx, 1)
			require.NoError(t, err)
			mode.Version = 4
			require.NoError(t, k.DistributionModeVersions.Set(ctx, 1, mode))
			sel, err := k.SelectionParamsVersions.Get(ctx, 1)
			require.NoError(t, err)
			sel.Version = 4
			require.NoError(t, k.SelectionParamsVersions.Set(ctx, 1, sel))
		},
	}

	// --- exact-version queries: A, B, C ---------------------------------------
	exact := map[string]func(types.QueryServer, sdk.Context, uint64) error{
		"distribution mode": func(q types.QueryServer, ctx sdk.Context, v uint64) error {
			_, err := q.DistributionModeVersion(ctx, &types.QueryDistributionModeVersionRequest{Version: v})
			return err
		},
		"selection params": func(q types.QueryServer, ctx sdk.Context, v uint64) error {
			_, err := q.SelectionParamsVersion(ctx, &types.QuerySelectionParamsVersionRequest{Version: v})
			return err
		},
		"settlement params": func(q types.QueryServer, ctx sdk.Context, v uint64) error {
			_, err := q.SettlementParamsVersion(ctx, &types.QuerySettlementParamsVersionRequest{Version: v})
			return err
		},
	}

	for family, ask := range exact {
		// A. version 0 with an intact origin.
		t.Run(family+"/version zero, origin valid -> InvalidArgument", func(t *testing.T) {
			q, _, ctx, _ := queryFixture(t)
			require.Equal(t, codes.InvalidArgument, grpcCode(t, ask(q, ctx, 0)))
		})

		// B and C. version 0 with a broken origin: the request is still what is wrong.
		for name, breakIt := range breakOrigin {
			t.Run(family+"/version zero, "+name+" -> InvalidArgument", func(t *testing.T) {
				q, k, ctx, _ := queryFixture(t)
				breakIt(t, k, ctx)
				require.Equal(t, codes.InvalidArgument, grpcCode(t, ask(q, ctx, 0)),
					"no chain-state read may override request classification")
			})

			// G and H. A VALID request against the same damage is still Internal, so
			// the bootstrap proof was ordered rather than removed.
			t.Run(family+"/valid version, "+name+" -> Internal", func(t *testing.T) {
				q, k, ctx, _ := queryFixture(t)
				breakIt(t, k, ctx)
				require.Equal(t, codes.Internal, grpcCode(t, ask(q, ctx, 1)))
			})
		}

		// I. Intact origin, valid request: ordinary behavior.
		t.Run(family+"/valid version, intact origin -> success", func(t *testing.T) {
			q, _, ctx, _ := queryFixture(t)
			require.NoError(t, ask(q, ctx, 1))
		})
	}

	// --- history listings: D, E, F --------------------------------------------
	lists := map[string]func(types.QueryServer, sdk.Context, *query.PageRequest) error{
		"distribution mode": func(q types.QueryServer, ctx sdk.Context, p *query.PageRequest) error {
			_, err := q.DistributionModeVersions(ctx, &types.QueryDistributionModeVersionsRequest{Pagination: p})
			return err
		},
		"selection params": func(q types.QueryServer, ctx sdk.Context, p *query.PageRequest) error {
			_, err := q.SelectionParamsVersions(ctx, &types.QuerySelectionParamsVersionsRequest{Pagination: p})
			return err
		},
		"settlement params": func(q types.QueryServer, ctx sdk.Context, p *query.PageRequest) error {
			_, err := q.SettlementParamsVersions(ctx, &types.QuerySettlementParamsVersionsRequest{Pagination: p})
			return err
		},
	}
	unsupported := map[string]*query.PageRequest{
		"offset":      {Offset: 1},
		"count_total": {CountTotal: true},
		"reverse":     {Reverse: true},
	}

	for family, list := range lists {
		for shape, page := range unsupported {
			// D, E, F. Unsupported pagination against a broken origin.
			for name, breakIt := range breakOrigin {
				t.Run(family+"/"+shape+" pagination, "+name+" -> InvalidArgument", func(t *testing.T) {
					q, k, ctx, _ := queryFixture(t)
					breakIt(t, k, ctx)
					require.Equal(t, codes.InvalidArgument, grpcCode(t, list(q, ctx, page)),
						"an unsupported page request is malformed regardless of the history")
				})
			}
		}

		// The listing counterparts of G/H/I.
		for name, breakIt := range breakOrigin {
			t.Run(family+"/valid pagination, "+name+" -> Internal", func(t *testing.T) {
				q, k, ctx, _ := queryFixture(t)
				breakIt(t, k, ctx)
				require.Equal(t, codes.Internal, grpcCode(t, list(q, ctx, nil)))
			})
		}
		t.Run(family+"/valid pagination, intact origin -> success", func(t *testing.T) {
			q, _, ctx, _ := queryFixture(t)
			require.NoError(t, list(q, ctx, &query.PageRequest{Limit: 10}))
		})
	}
}
