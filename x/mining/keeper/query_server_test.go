package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/query"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/twilight-project/twilight-core/x/mining/keeper"
	"github.com/twilight-project/twilight-core/x/mining/types"
	rewardstypes "github.com/twilight-project/twilight-core/x/rewards/types"
)

// The observability surface, and the classification that makes it safe to build on.
//
// A read-only surface has one failure mode worse than any other: reporting
// corrupted state as absence. A worker reconciling its own position cannot tell
// "this obligation never existed" from "this obligation exists and I could not read
// it", and would resume as though it owed nothing. Every test below is ultimately
// about keeping those two answers apart.

func queryFixture(t *testing.T) (types.QueryServer, keeper.Keeper, sdk.Context, *rewardsKeeperMock) {
	t.Helper()
	k, ctx, rewards := settlementFixture(t)
	return keeper.NewQueryServer(k), k, ctx, rewards
}

func grpcCode(t *testing.T, err error) codes.Code {
	t.Helper()
	require.Error(t, err)
	st, ok := status.FromError(err)
	require.Truef(t, ok, "error is not a gRPC status: %v", err)
	return st.Code()
}

// TestSettlementQueryReportsCanonicalCorrelatedAndDerivedState is the recovery
// answer in one response.
func TestSettlementQueryReportsCanonicalCorrelatedAndDerivedState(t *testing.T) {
	q, k, ctx, _ := queryFixture(t)

	res, err := q.Settlement(ctx, &types.QuerySettlementRequest{SlotId: 1, Epoch: 1})
	require.NoError(t, err)

	// Canonical, exactly as stored.
	require.Equal(t, uint64(1), res.Settlement.SlotId)
	require.Equal(t, types.SettlementMode_SETTLEMENT_MODE_TRUSTED_AS, res.Settlement.SettlementMode)
	require.Zero(t, res.Settlement.NextChunkIndex)
	require.False(t, res.Settlement.Finalized)

	// Correlated, from the authoritative entitlement.
	require.Equal(t, fixtureEntitlement, res.EntitlementAmount)
	require.Equal(t, "0", res.ReleasedAmount)
	require.NotEmpty(t, res.PayoutAddress)

	// Derived, computed here and stored nowhere.
	require.Equal(t, fixtureEntitlement, res.RemainingAmount)
	require.Equal(t, fixtureEntitlement, res.ParticipantDistributionCeiling)
	anchor, _, err := k.GetSettlementEpochAnchor(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, anchor.CreatedSettlementClock, res.CreatedSettlementClock)
	require.False(t, res.PermissionlessFinalizationNow, "inside the participant window")

	clock, err := k.GetSettlementClock(ctx)
	require.NoError(t, err)
	require.Equal(t, clock, res.CurrentSettlementClock)
	require.Greater(t, res.DeadlineClock, res.CurrentSettlementClock)
}

// TestSettlementQueryTracksTheWorkerThroughTheWholeLifecycle is the property A6
// exists for: recovery from committed state alone, with no event.
func TestSettlementQueryTracksTheWorkerThroughTheWholeLifecycle(t *testing.T) {
	q, k, ctx, _ := queryFixture(t)

	// After materialization: nothing released, everything remaining.
	res, err := q.Settlement(ctx, &types.QuerySettlementRequest{SlotId: 1, Epoch: 1})
	require.NoError(t, err)
	require.Zero(t, res.Settlement.NextChunkIndex, "chunk 0 is next")
	require.Equal(t, "0", res.ReleasedAmount)
	require.Equal(t, fixtureEntitlement, res.RemainingAmount)

	// After a chunk: the cursor advanced, so chunk 0 committed and 1 is next.
	_, err = k.SubmitSettlementChunk(ctx, chunk(0, line(participantA, "400000")))
	require.NoError(t, err)
	res, err = q.Settlement(ctx, &types.QuerySettlementRequest{SlotId: 1, Epoch: 1})
	require.NoError(t, err)
	require.Equal(t, uint64(1), res.Settlement.NextChunkIndex)
	require.Equal(t, "400000", res.ReleasedAmount)
	require.Equal(t, "600000", res.RemainingAmount)
	require.False(t, res.Settlement.Finalized)

	// After finalization: terminal, fully released, nothing left to do.
	_, _, err = k.FinalizeSettlement(ctx, finalize(settlementSigner))
	require.NoError(t, err)
	res, err = q.Settlement(ctx, &types.QuerySettlementRequest{SlotId: 1, Epoch: 1})
	require.NoError(t, err)
	require.True(t, res.Settlement.Finalized)
	require.Equal(t, fixtureEntitlement, res.ReleasedAmount)
	require.Equal(t, "0", res.RemainingAmount)
	require.Equal(t,
		types.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_AUTHORIZED_EARLY,
		res.Settlement.FinalizationReason)
	require.Positive(t, res.Settlement.FinalizedHeight)
	require.False(t, res.PermissionlessFinalizationNow,
		"a terminal settlement offers no finalization to perform")
}

// TestPermissionlessNowFollowsTheDeadline pins the derived authorization signal.
func TestPermissionlessNowFollowsTheDeadline(t *testing.T) {
	q, k, ctx, _ := queryFixture(t)

	pastDeadline(t, k, ctx, -1)
	res, err := q.Settlement(ctx, &types.QuerySettlementRequest{SlotId: 1, Epoch: 1})
	require.NoError(t, err)
	require.False(t, res.PermissionlessFinalizationNow, "one tick before the deadline")

	pastDeadline(t, k, ctx, 0)
	res, err = q.Settlement(ctx, &types.QuerySettlementRequest{SlotId: 1, Epoch: 1})
	require.NoError(t, err)
	require.True(t, res.PermissionlessFinalizationNow, "at the deadline")
}

// TestOperatorOnlyQueryIsPermissionlessWithoutBecomingTimeGated respects the A5
// closure.
//
// Such a settlement is finalizable immediately and never becomes deadline-gated, so
// the query must report it permissionless regardless of the clock — and must not
// acquire a settlement-parameter dependency the transition itself does not have.
func TestOperatorOnlyQueryIsPermissionlessWithoutBecomingTimeGated(t *testing.T) {
	q, k, ctx, _ := queryFixture(t)
	setSettlementMode(t, k, ctx, types.SettlementMode_SETTLEMENT_MODE_OPERATOR_ONLY)
	require.NoError(t, k.SettlementClock.Set(ctx, 1))

	res, err := q.Settlement(ctx, &types.QuerySettlementRequest{SlotId: 1, Epoch: 1})
	require.NoError(t, err)
	require.True(t, res.PermissionlessFinalizationNow, "immediately finalizable")
	require.Equal(t, "0", res.ParticipantDistributionCeiling,
		"its whole entitlement belongs to the operator")
	anchor, _, err := k.GetSettlementEpochAnchor(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, anchor.CreatedSettlementClock, res.DeadlineClock,
		"the derived deadline is the anchor clock, and is not an authorization boundary")

	// Bound parameters that would refuse a participant-capable settlement must not
	// affect this one.
	settlement := settlementRow(t, k, ctx)
	settlement.SettlementParamsVersion = 7
	require.NoError(t, k.Settlements.Set(ctx, collections.Join(uint64(1), uint64(1)), settlement))
	res, err = q.Settlement(ctx, &types.QuerySettlementRequest{SlotId: 1, Epoch: 1})
	require.NoError(t, err, "no settlement-parameter dependency was introduced")
	require.True(t, res.PermissionlessFinalizationNow)
}

// TestSettlementQueryClassifiesAbsenceApartFromCorruption is the central rule.
func TestSettlementQueryClassifiesAbsenceApartFromCorruption(t *testing.T) {
	t.Run("no settlement is NotFound", func(t *testing.T) {
		q, _, ctx, _ := queryFixture(t)
		_, err := q.Settlement(ctx, &types.QuerySettlementRequest{SlotId: 1, Epoch: 9})
		require.Equal(t, codes.NotFound, grpcCode(t, err))
	})

	t.Run("zero identifiers are InvalidArgument", func(t *testing.T) {
		q, _, ctx, _ := queryFixture(t)
		_, err := q.Settlement(ctx, &types.QuerySettlementRequest{SlotId: 0, Epoch: 1})
		require.Equal(t, codes.InvalidArgument, grpcCode(t, err))
		_, err = q.Settlement(ctx, &types.QuerySettlementRequest{SlotId: 1, Epoch: 0})
		require.Equal(t, codes.InvalidArgument, grpcCode(t, err))
	})

	corruptions := map[string]func(*testing.T, keeper.Keeper, sdk.Context, *rewardsKeeperMock){
		"missing entitlement": func(_ *testing.T, _ keeper.Keeper, _ sdk.Context, r *rewardsKeeperMock) {
			r.entitlements[1] = nil
		},
		"entitlement naming another obligation": func(_ *testing.T, _ keeper.Keeper, _ sdk.Context, r *rewardsKeeperMock) {
			mismatched := entitlement(1, 1, fixtureEntitlement)
			mismatched.Epoch = 4
			r.entitlements[1] = []rewardstypes.SlotEntitlement{mismatched}
		},
		"released above the entitlement": func(_ *testing.T, _ keeper.Keeper, _ sdk.Context, r *rewardsKeeperMock) {
			over := entitlement(1, 1, fixtureEntitlement)
			over.ReleasedAmount = "2000000"
			r.entitlements[1] = []rewardstypes.SlotEntitlement{over}
		},
		"missing epoch anchor": func(t *testing.T, k keeper.Keeper, ctx sdk.Context, _ *rewardsKeeperMock) {
			require.NoError(t, k.SettlementEpochAnchors.Remove(ctx, 1))
		},
		"anchor disagreeing with its key": func(t *testing.T, k keeper.Keeper, ctx sdk.Context, _ *rewardsKeeperMock) {
			require.NoError(t, k.SettlementEpochAnchors.Set(ctx, 1,
				types.SettlementEpochAnchor{Epoch: 5, CreatedSettlementClock: 0}))
		},
		"bound parameters disagreeing with the settlement": func(t *testing.T, k keeper.Keeper, ctx sdk.Context, _ *rewardsKeeperMock) {
			settlement := settlementRow(t, k, ctx)
			settlement.SettlementParamsVersion = 7
			require.NoError(t, k.Settlements.Set(ctx, collections.Join(uint64(1), uint64(1)), settlement))
		},
		"anchor ahead of the canonical clock": func(t *testing.T, k keeper.Keeper, ctx sdk.Context, _ *rewardsKeeperMock) {
			clock, err := k.GetSettlementClock(ctx)
			require.NoError(t, err)
			anchor, _, err := k.GetSettlementEpochAnchor(ctx, 1)
			require.NoError(t, err)
			anchor.CreatedSettlementClock = clock + 5_000
			require.NoError(t, k.SettlementEpochAnchors.Set(ctx, 1, anchor))
		},
	}
	for name, breakIt := range corruptions {
		t.Run(name+" is Internal", func(t *testing.T) {
			q, k, ctx, rewards := queryFixture(t)
			breakIt(t, k, ctx, rewards)
			_, err := q.Settlement(ctx, &types.QuerySettlementRequest{SlotId: 1, Epoch: 1})
			require.Equal(t, codes.Internal, grpcCode(t, err),
				"corruption must never be reported as absence")
		})
	}
}

// TestOpenSettlementsReadsCanonicalRowsNotTheDerivedIndex is A6-B2.
//
// The derived index can identify candidate rows, but its ABSENCE proves nothing —
// a missing entry looks exactly like a lost one. Traversing it would let a real
// outstanding obligation vanish from a successful recovery response, which is the
// worst failure available to a surface that exists to tell a worker what it owes.
func TestOpenSettlementsReadsCanonicalRowsNotTheDerivedIndex(t *testing.T) {
	t.Run("an open canonical row is returned", func(t *testing.T) {
		q, _, ctx, _ := queryFixture(t)
		res, err := q.OpenSettlements(ctx, &types.QueryOpenSettlementsRequest{SlotId: 1})
		require.NoError(t, err)
		require.Len(t, res.Settlements, 1)
		require.Equal(t, uint64(1), res.Settlements[0].Epoch)
	})

	// The load-bearing regression: delete the derived entry and the canonical
	// obligation must still be reported.
	t.Run("an open canonical row survives losing its index entry", func(t *testing.T) {
		q, k, ctx, _ := queryFixture(t)
		require.NoError(t, k.OpenSettlementsBySlot.Remove(ctx, collections.Join(uint64(1), uint64(1))))
		has, err := k.OpenSettlementsBySlot.Has(ctx, collections.Join(uint64(1), uint64(1)))
		require.NoError(t, err)
		require.False(t, has, "the derived entry really is gone")

		res, err := q.OpenSettlements(ctx, &types.QueryOpenSettlementsRequest{SlotId: 1})
		require.NoError(t, err)
		require.Len(t, res.Settlements, 1,
			"a lost index entry must never hide a canonical obligation")
		require.Equal(t, uint64(1), res.Settlements[0].Epoch)
	})

	t.Run("a finalized canonical row is not returned", func(t *testing.T) {
		q, k, ctx, _ := queryFixture(t)
		_, _, err := k.FinalizeSettlement(ctx, finalize(settlementSigner))
		require.NoError(t, err)
		res, err := q.OpenSettlements(ctx, &types.QueryOpenSettlementsRequest{SlotId: 1})
		require.NoError(t, err)
		require.Empty(t, res.Settlements)
	})

	t.Run("a stale index entry cannot resurrect a finalized row", func(t *testing.T) {
		q, k, ctx, _ := queryFixture(t)
		_, _, err := k.FinalizeSettlement(ctx, finalize(settlementSigner))
		require.NoError(t, err)
		require.NoError(t, k.OpenSettlementsBySlot.Set(ctx, collections.Join(uint64(1), uint64(1)), 1))

		res, err := q.OpenSettlements(ctx, &types.QueryOpenSettlementsRequest{SlotId: 1})
		require.NoError(t, err)
		require.Empty(t, res.Settlements, "lifecycle is decided by the canonical row alone")
	})

	t.Run("a mixed history returns only the open rows, in order", func(t *testing.T) {
		q, k, ctx, _ := queryFixture(t)
		seedSettlements(t, k, ctx, map[uint64]bool{2: true, 3: false, 4: true, 5: false})

		res, err := q.OpenSettlements(ctx, &types.QueryOpenSettlementsRequest{SlotId: 1})
		require.NoError(t, err)
		epochs := make([]uint64, 0, len(res.Settlements))
		for _, s := range res.Settlements {
			epochs = append(epochs, s.Epoch)
			require.False(t, s.Finalized)
		}
		require.Equal(t, []uint64{1, 3, 5}, epochs)
	})

	t.Run("a malformed canonical row fails the page", func(t *testing.T) {
		q, k, ctx, _ := queryFixture(t)
		// A row filed under epoch 2 that declares epoch 9.
		require.NoError(t, k.Settlements.Set(ctx, collections.Join(uint64(1), uint64(2)),
			types.Settlement{
				SlotId: 1, Epoch: 9, DistributionModeVersion: 1,
				SettlementMode:          types.SettlementMode_SETTLEMENT_MODE_TRUSTED_AS,
				SettlementParamsVersion: 1,
			}))
		_, err := q.OpenSettlements(ctx, &types.QueryOpenSettlementsRequest{SlotId: 1})
		require.Equal(t, codes.Internal, grpcCode(t, err))
	})

	t.Run("zero slot is InvalidArgument", func(t *testing.T) {
		q, _, ctx, _ := queryFixture(t)
		_, err := q.OpenSettlements(ctx, &types.QueryOpenSettlementsRequest{SlotId: 0})
		require.Equal(t, codes.InvalidArgument, grpcCode(t, err))
	})
}

// TestOpenSettlementsBudgetsCanonicalRowsNotResults pins the resource rule and the
// continuation contract that follows from it.
//
// The bound is on rows INSPECTED. A Slot with a long finalized history therefore
// yields pages that are short or empty while more open work exists further on —
// filling the page instead would mean scanning past the budget, which is exactly the
// lifetime-proportional cost the bound exists to prevent.
func TestOpenSettlementsBudgetsCanonicalRowsNotResults(t *testing.T) {
	q, k, ctx, _ := queryFixture(t)
	// 150 canonical rows for the Slot: epochs 1..150, every third one open.
	rows := map[uint64]bool{}
	openEpochs := []uint64{}
	for epoch := uint64(2); epoch <= 150; epoch++ {
		finalized := epoch%3 != 0
		rows[epoch] = finalized
		if !finalized {
			openEpochs = append(openEpochs, epoch)
		}
	}
	seedSettlements(t, k, ctx, rows)
	openEpochs = append([]uint64{1}, openEpochs...) // the fixture's own open row

	t.Run("one request inspects at most the server cap", func(t *testing.T) {
		res, err := q.OpenSettlements(ctx, &types.QueryOpenSettlementsRequest{SlotId: 1})
		require.NoError(t, err)
		require.NotEmpty(t, res.Pagination.NextKey, "150 rows cannot be inspected in one page")
		require.Less(t, len(res.Settlements), 100,
			"a page of 100 inspected rows yields fewer open results")
	})

	t.Run("continuation returns every open row exactly once", func(t *testing.T) {
		seen := []uint64{}
		var key []byte
		for pages := 0; pages < 10; pages++ {
			res, err := q.OpenSettlements(ctx, &types.QueryOpenSettlementsRequest{
				SlotId: 1, Pagination: &query.PageRequest{Key: key},
			})
			require.NoError(t, err)
			for _, s := range res.Settlements {
				seen = append(seen, s.Epoch)
			}
			key = res.Pagination.NextKey
			if len(key) == 0 {
				break
			}
		}
		require.Equal(t, openEpochs, seen, "every open settlement, once, in order")
	})

	t.Run("a malformed continuation key is refused", func(t *testing.T) {
		// Any length other than the cursor's own. Note that a well-formed cursor
		// pointing past the end is NOT malformed — it is an ordinary exhausted page —
		// so the check is on shape, not on reachability.
		for _, bad := range [][]byte{{0x01}, {0x01, 0x02, 0x03}, make([]byte, 9)} {
			_, err := q.OpenSettlements(ctx, &types.QueryOpenSettlementsRequest{
				SlotId: 1, Pagination: &query.PageRequest{Key: bad},
			})
			require.Equalf(t, codes.InvalidArgument, grpcCode(t, err),
				"a %d-byte pagination key is not a continuation key", len(bad))
		}
	})

	t.Run("a cursor past the end is an ordinary empty page", func(t *testing.T) {
		res, err := q.OpenSettlements(ctx, &types.QueryOpenSettlementsRequest{
			SlotId: 1, Pagination: &query.PageRequest{Key: keeper.EncodeEpochCursor(9_000)},
		})
		require.NoError(t, err)
		require.Empty(t, res.Settlements)
		require.Empty(t, res.Pagination.NextKey)
	})
}

// seedSettlements writes canonical settlement rows for slot 1, each either open or
// finalized, without touching the derived index — so tests can build a history the
// index does not describe.
func seedSettlements(t *testing.T, k keeper.Keeper, ctx sdk.Context, rows map[uint64]bool) {
	t.Helper()
	for epoch, finalized := range rows {
		settlement := types.Settlement{
			SlotId: 1, Epoch: epoch, DistributionModeVersion: 1,
			SettlementMode:          types.SettlementMode_SETTLEMENT_MODE_TRUSTED_AS,
			SettlementParamsVersion: 1,
		}
		if finalized {
			settlement.Finalized = true
			settlement.FinalizedHeight = 10
			settlement.FinalizationReason =
				types.SettlementFinalizationReason_SETTLEMENT_FINALIZATION_REASON_AUTHORIZED_EARLY
		}
		require.NoError(t, k.Settlements.Set(ctx, collections.Join(uint64(1), epoch), settlement))
	}
}

// TestListingsAreBoundedIndependentlyOfChainLifetime pins the resource rules.
//
// offset and count_total are refused rather than capped: each makes a request's cost
// track the whole collection while looking like a request for one page.
func TestListingsAreBoundedIndependentlyOfChainLifetime(t *testing.T) {
	q, _, ctx, _ := queryFixture(t)

	for name, page := range map[string]*query.PageRequest{
		"offset":      {Offset: 1},
		"count_total": {CountTotal: true},
		"reverse":     {Reverse: true},
	} {
		t.Run(name+" is refused", func(t *testing.T) {
			_, err := q.OpenSettlements(ctx, &types.QueryOpenSettlementsRequest{
				SlotId: 1, Pagination: page,
			})
			require.Equal(t, codes.InvalidArgument, grpcCode(t, err))
			_, err = q.SettlementParamsVersions(ctx, &types.QuerySettlementParamsVersionsRequest{
				Pagination: page,
			})
			require.Equal(t, codes.InvalidArgument, grpcCode(t, err))
		})
	}

	t.Run("omitted and zero limits are served under the server cap", func(t *testing.T) {
		res, err := q.SettlementParamsVersions(ctx, &types.QuerySettlementParamsVersionsRequest{})
		require.NoError(t, err)
		require.Len(t, res.Versions, 1)
		res, err = q.SettlementParamsVersions(ctx, &types.QuerySettlementParamsVersionsRequest{
			Pagination: &query.PageRequest{Limit: 0},
		})
		require.NoError(t, err)
		require.Len(t, res.Versions, 1)
	})

	t.Run("an oversized limit is capped rather than honored", func(t *testing.T) {
		res, err := q.SettlementParamsVersions(ctx, &types.QuerySettlementParamsVersionsRequest{
			Pagination: &query.PageRequest{Limit: 1_000_000},
		})
		require.NoError(t, err)
		require.LessOrEqual(t, len(res.Versions), 100)
	})
}

// TestSettlementClockQueryHasNoDefault keeps a missing clock from reading as zero.
func TestSettlementClockQueryHasNoDefault(t *testing.T) {
	q, k, ctx, _ := queryFixture(t)
	res, err := q.SettlementClock(ctx, &types.QuerySettlementClockRequest{})
	require.NoError(t, err)
	clock, err := k.GetSettlementClock(ctx)
	require.NoError(t, err)
	require.Equal(t, clock, res.SettlementClock)

	require.NoError(t, k.SettlementClock.Remove(ctx))
	_, err = q.SettlementClock(ctx, &types.QuerySettlementClockRequest{})
	require.Equal(t, codes.Internal, grpcCode(t, err),
		"an absent clock on an initialized chain is corruption, not zero")
}

// TestAFinalizedSettlementMustHaveReleasedItsEntitlementInFull is A6-B1.
//
// Finalization proves released == entitlement before it writes terminal metadata,
// so a finalized row with value still unreleased is state no admitted transition
// produced. Answering successfully would tell a worker the obligation is closed
// while the chain still owes against it — and the worker's own reconciliation would
// then agree, because it would be reading the same corrupt row.
func TestAFinalizedSettlementMustHaveReleasedItsEntitlementInFull(t *testing.T) {
	t.Run("finalized with value unreleased is Internal", func(t *testing.T) {
		q, k, ctx, rewards := queryFixture(t)
		_, _, err := k.FinalizeSettlement(ctx, finalize(settlementSigner))
		require.NoError(t, err)
		// Wind the authoritative released amount back below the entitlement.
		partial := entitlement(1, 1, fixtureEntitlement)
		partial.ReleasedAmount = "400000"
		rewards.entitlements[1] = []rewardstypes.SlotEntitlement{partial}

		_, err = q.Settlement(ctx, &types.QuerySettlementRequest{SlotId: 1, Epoch: 1})
		require.Equal(t, codes.Internal, grpcCode(t, err))
	})

	t.Run("finalized and fully released is served", func(t *testing.T) {
		q, k, ctx, _ := queryFixture(t)
		_, _, err := k.FinalizeSettlement(ctx, finalize(settlementSigner))
		require.NoError(t, err)

		res, err := q.Settlement(ctx, &types.QuerySettlementRequest{SlotId: 1, Epoch: 1})
		require.NoError(t, err)
		require.True(t, res.Settlement.Finalized)
		require.Equal(t, "0", res.RemainingAmount)
		require.Equal(t, fixtureEntitlement, res.ReleasedAmount)
	})

	t.Run("open and only partly released stays valid", func(t *testing.T) {
		q, k, ctx, _ := queryFixture(t)
		_, err := k.SubmitSettlementChunk(ctx, chunk(0, line(participantA, "400000")))
		require.NoError(t, err)

		res, err := q.Settlement(ctx, &types.QuerySettlementRequest{SlotId: 1, Epoch: 1})
		require.NoError(t, err)
		require.False(t, res.Settlement.Finalized)
		require.Equal(t, "600000", res.RemainingAmount)
	})

	t.Run("open and fully distributed stays valid", func(t *testing.T) {
		q, k, ctx, _ := queryFixture(t)
		_, err := k.SubmitSettlementChunk(ctx, chunk(0, line(participantA, fixtureEntitlement)))
		require.NoError(t, err)

		res, err := q.Settlement(ctx, &types.QuerySettlementRequest{SlotId: 1, Epoch: 1})
		require.NoError(t, err)
		require.False(t, res.Settlement.Finalized,
			"exhausting the window before finalizing is ordinary, not corruption")
		require.Equal(t, "0", res.RemainingAmount)
	})
}
