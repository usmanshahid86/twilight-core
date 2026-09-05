package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/twilight-project/twilight-core/x/mining/keeper"
	"github.com/twilight-project/twilight-core/x/mining/types"
)

// The query exists so a consumer stops re-implementing a consensus rule. That is only
// true if its answer is the SAME answer consensus reaches — not an answer derived the
// same way, which is what a second implementation would be.
//
// So the load-bearing assertion is not "the query returns a plausible version". It is
// that the version the query names for an epoch equals the version consensus STAMPED
// onto a settlement materialized for that same epoch. Nothing else proves the
// duplication was actually removed.
func TestSettlementParamsForEpochAgreesWithTheVersionConsensusStamps(t *testing.T) {
	k, ctx, rewards := initialized(t)
	qs := keeper.NewQueryServer(k)

	// A parameter change lands, so the answer is not trivially "version 1 forever" —
	// a query hard-wired to the genesis anchor would pass this test without it.
	require.NoError(t, k.ScheduledSettlementParams.Set(ctx, 2, types.ScheduledSettlementParams{
		EffectiveEpoch:           2,
		SettlementWindowEpochs:   3,
		MaxRecipientsPerChunk:    16,
		MaxChunksPerSettlement:   2,
		MinRecipientPayoutAmount: "20000",
	}))
	rewards.finalize(1)
	require.NoError(t, k.EndBlock(ctx))

	for epoch := uint64(1); epoch <= 6; epoch++ {
		resp, err := qs.SettlementParamsForEpoch(ctx, &types.QuerySettlementParamsForEpochRequest{Epoch: epoch})
		require.NoError(t, err, "epoch %d", epoch)
		require.NotNil(t, resp.SettlementParamsVersion)

		// The same call the EndBlock materialization path makes when it stamps a
		// version onto a settlement.
		bound, err := k.SettlementParamsForTarget(ctx, epoch)
		require.NoError(t, err)

		require.Equal(t, bound.Version, resp.SettlementParamsVersion.Version,
			"epoch %d: the query names a different version than consensus binds", epoch)
		require.Equal(t, bound, *resp.SettlementParamsVersion,
			"epoch %d: the query returned a record that differs from the bound one", epoch)
		require.Equal(t, epoch, resp.Epoch)
	}
}

// binding_epoch is diagnostic, and its value is the rule's own boundary rather than
// anything the handler computes. Bootstrap targets have no N-2 boundary inside chain
// history and resolve from the genesis anchor; everything else binds at epoch-2.
func TestSettlementParamsForEpochReportsTheBoundaryItResolvedAt(t *testing.T) {
	k, ctx, _ := initialized(t)
	qs := keeper.NewQueryServer(k)

	for _, tc := range []struct {
		epoch     uint64
		binding   uint64
		bootstrap bool
	}{
		{epoch: 1, binding: 0, bootstrap: true},
		{epoch: 2, binding: 0, bootstrap: true},
		{epoch: 3, binding: 1, bootstrap: false},
		{epoch: 9, binding: 7, bootstrap: false},
	} {
		resp, err := qs.SettlementParamsForEpoch(ctx, &types.QuerySettlementParamsForEpochRequest{Epoch: tc.epoch})
		require.NoError(t, err, "epoch %d", tc.epoch)
		require.Equal(t, tc.binding, resp.BindingEpoch, "epoch %d binding boundary", tc.epoch)
		require.Equal(t, tc.bootstrap, resp.Bootstrap, "epoch %d bootstrap", tc.epoch)
	}
}

// An epoch past every scheduled change is answered with what WOULD bind if nothing
// further lands — not an error, and not a distinct "provisional" shape a caller would
// have to branch on. effective_epoch inside the record is the signal to re-query
// against, which is why the whole record is returned rather than chosen fields.
func TestSettlementParamsForEpochAnswersBeyondTheBindingHorizon(t *testing.T) {
	k, ctx, _ := initialized(t)
	qs := keeper.NewQueryServer(k)

	resp, err := qs.SettlementParamsForEpoch(ctx, &types.QuerySettlementParamsForEpochRequest{Epoch: 10_000})
	require.NoError(t, err, "a far-future epoch is answerable, not an error")
	require.NotNil(t, resp.SettlementParamsVersion)
	require.NotZero(t, resp.SettlementParamsVersion.EffectiveEpoch,
		"the effective epoch is what a caller re-queries against; zero would give it nothing to watch")
}

// Epoch 0 does not exist — epochs are numbered from 1 — so it is a malformed request
// rather than an absent object. Classified as InvalidArgument for the reason the rest
// of this surface gives: a consumer told NotFound would read it as "nothing configured
// yet" and carry on.
func TestSettlementParamsForEpochRejectsEpochZero(t *testing.T) {
	k, ctx, _ := initialized(t)
	qs := keeper.NewQueryServer(k)

	_, err := qs.SettlementParamsForEpoch(ctx, &types.QuerySettlementParamsForEpochRequest{Epoch: 0})
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))

	_, err = qs.SettlementParamsForEpoch(ctx, nil)
	require.Error(t, err)
	require.Equal(t, codes.InvalidArgument, status.Code(err))
}
