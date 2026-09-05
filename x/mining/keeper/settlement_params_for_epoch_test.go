package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	"github.com/twilight-project/twilight-core/x/mining/keeper"
	"github.com/twilight-project/twilight-core/x/mining/types"
)

// The query exists so a consumer stops re-implementing a consensus rule. That is only
// true if its answer is the SAME answer consensus reaches.
//
// So this compares the query against the version MATERIALIZATION STAMPED onto a real
// settlement — not against the resolver the query itself calls, which would only prove
// the handler dials the function it obviously dials.
//
// The parameter change is what gives the comparison teeth. Epochs 1 and 2 are bootstrap
// and resolve from the genesis anchor; epoch 4 binds at epoch 2, where version 2 is
// effective. So a materialization stamping version 1 — a perfectly legitimate record —
// disagrees with the query, and this test is what notices.
func TestSettlementParamsForEpochAgreesWithTheVersionConsensusStamps(t *testing.T) {
	core := &coreSlotKeeperMock{
		active:   []coreslottypes.CoreSlot{settlementSlot(1, account(0x11))},
		policies: map[uint64]coreslottypes.SelectionPolicyVersion{1: policy(1, 2_500, 10)},
	}
	k, ctx, rewards := setupKeeperWithRewards(t, core, newRewardsMock())
	require.NoError(t, k.InitGenesis(ctx, *types.DefaultGenesis()))

	// A second parameter version, effective at epoch 2. Without this every epoch binds
	// version 1 and the comparison could not distinguish a correct stamp from a
	// hard-wired one.
	require.NoError(t, k.ScheduledSettlementParams.Set(ctx, 2, types.ScheduledSettlementParams{
		EffectiveEpoch:           2,
		SettlementWindowEpochs:   3,
		MaxRecipientsPerChunk:    16,
		MaxChunksPerSettlement:   2,
		MinRecipientPayoutAmount: "20000",
	}))

	// Drive epochs through the ordinary path: each finalized epoch yields an
	// entitlement, and EndBlock materializes a settlement against it.
	for epoch := uint64(1); epoch <= 4; epoch++ {
		rewards.finalize(epoch, entitlement(1, epoch, "1000000"))
		require.NoError(t, k.EndBlock(ctx), "EndBlock for epoch %d", epoch)
	}

	qs := keeper.NewQueryServer(k)
	sawChangedVersion := false

	for epoch := uint64(1); epoch <= 4; epoch++ {
		settlement, found, err := k.GetSettlement(ctx, 1, epoch)
		require.NoError(t, err)
		require.Truef(t, found, "epoch %d produced no settlement, so there is nothing to compare against", epoch)

		resp, err := qs.SettlementParamsForEpoch(ctx, &types.QuerySettlementParamsForEpochRequest{Epoch: epoch})
		require.NoError(t, err, "epoch %d", epoch)
		require.NotNil(t, resp.SettlementParamsVersion)

		require.Equalf(t, settlement.SettlementParamsVersion, resp.SettlementParamsVersion.Version,
			"epoch %d: consensus stamped version %d onto the settlement, the query says %d",
			epoch, settlement.SettlementParamsVersion, resp.SettlementParamsVersion.Version)

		if settlement.SettlementParamsVersion != 1 {
			sawChangedVersion = true
		}
	}

	require.True(t, sawChangedVersion,
		"every settlement bound version 1, so the parameter change never took effect and this test proved nothing")
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
