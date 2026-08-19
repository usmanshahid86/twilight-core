package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"google.golang.org/grpc/codes"

	"github.com/twilight-project/twilight-core/x/mining/keeper"
	"github.com/twilight-project/twilight-core/x/mining/types"
)

// The chain's own reading of a target epoch.
//
// The N-2 binding rule and its bootstrap exception are consensus rules. A consumer
// that recomputes them holds a second copy which can disagree with the arm
// settlement actually takes while nothing fails loudly, so this query exists to
// hand back the chain's answer rather than the inputs to derive one. Every test
// below is ultimately about that single-answer property: the interpretation must
// track the history the chain actually built, and must never be reachable by
// arithmetic performed anywhere else.

func targetEpochFixture(t *testing.T) (types.QueryServer, keeper.Keeper, sdk.Context, *rewardsKeeperMock) {
	t.Helper()
	k, ctx, rewards := initialized(t)
	return keeper.NewQueryServer(k), k, ctx, rewards
}

func interpret(t *testing.T, q types.QueryServer, ctx sdk.Context, target uint64) *types.QueryTargetEpochInterpretationResponse {
	t.Helper()
	res, err := q.TargetEpochInterpretation(ctx, &types.QueryTargetEpochInterpretationRequest{TargetEpoch: target})
	require.NoError(t, err)
	require.Equal(t, target, res.TargetEpoch, "the response echoes the target it answers for")
	require.NotNil(t, res.DistributionModeVersion, "an interpretation always names its governing row")
	return res
}

func interpretErr(t *testing.T, q types.QueryServer, ctx sdk.Context, target uint64) codes.Code {
	t.Helper()
	_, err := q.TargetEpochInterpretation(ctx, &types.QueryTargetEpochInterpretationRequest{TargetEpoch: target})
	return grpcCode(t, err)
}

// TestAZeroTargetIsClassifiedBeforeAnyCanonicalRead pins the ordering the handler
// depends on, using the only condition that can tell the two orderings apart.
//
// bindingEpochForTarget reports a zero target as ErrInvalidState — a STATE error
// for what is purely a request defect — so a handler that reached it first would
// answer Internal and tell a consumer the chain was damaged when the only thing
// wrong was the question. The second half is the part that cannot be faked: on a
// chain whose mode history really is gone, a zero target must STILL be
// InvalidArgument, because request classification precedes chain state rather than
// competing with it.
func TestAZeroTargetIsClassifiedBeforeAnyCanonicalRead(t *testing.T) {
	q, k, ctx, _ := targetEpochFixture(t)

	require.Equal(t, codes.InvalidArgument, interpretErr(t, q, ctx, 0), "epoch numbers start at 1")

	// Now damage the chain underneath it. A healthy chain cannot distinguish the
	// two orderings, because both refuse; only this can.
	require.NoError(t, k.DistributionModeVersions.Remove(ctx, 1))
	require.Equal(t, codes.InvalidArgument, interpretErr(t, q, ctx, 0),
		"a malformed request stays malformed even when the state behind it is damaged")
}

// TestANilRequestEnvelopeIsMalformed keeps the defective envelope distinct from
// every deterministic answer about a target.
func TestANilRequestEnvelopeIsMalformed(t *testing.T) {
	q, _, ctx, _ := targetEpochFixture(t)

	_, err := q.TargetEpochInterpretation(ctx, nil)
	require.Equal(t, codes.InvalidArgument, grpcCode(t, err))
}

// TestBootstrapTargetsResolveFromTheGenesisAnchor covers the exception to N-2.
//
// Targets 1 and 2 have no N-2 boundary inside chain history. They report binding
// epoch 0 — which is diagnostic, not an epoch anyone may bind at — and resolve the
// permanent anchor instead.
func TestBootstrapTargetsResolveFromTheGenesisAnchor(t *testing.T) {
	q, _, ctx, _ := targetEpochFixture(t)

	for _, target := range []uint64{1, 2} {
		res := interpret(t, q, ctx, target)
		require.True(t, res.BootstrapModeWithoutFullNMinus_2Binding,
			"target %d has no N-2 boundary inside chain history", target)
		require.Zero(t, res.BindingEpoch, "a bootstrap target names no binding epoch")
		require.Equal(t, uint64(1), res.DistributionModeVersion.Version, "the permanent anchor")
		require.Equal(t, trustedAS, res.DistributionModeVersion.Mode)
		require.False(t, res.SelectionApplicable)
	}
}

// TestOrdinaryTargetsBindTheirOwnNMinus2Boundary is the rule itself, at the first
// target that has one and at a target well past it.
func TestOrdinaryTargetsBindTheirOwnNMinus2Boundary(t *testing.T) {
	q, _, ctx, _ := targetEpochFixture(t)

	for _, expected := range []struct{ target, binding uint64 }{{3, 1}, {4, 2}, {12, 10}} {
		res := interpret(t, q, ctx, expected.target)
		require.Falsef(t, res.BootstrapModeWithoutFullNMinus_2Binding,
			"target %d has a complete binding", expected.target)
		require.Equalf(t, expected.binding, res.BindingEpoch,
			"target %d binds epoch %d", expected.target, expected.binding)
		require.Equal(t, uint64(1), res.DistributionModeVersion.Version,
			"one open interval still governs every target on this chain")
	}
}

// TestAPromotedModeChangesWhichRowATargetResolves is the property that makes this
// query worth having: the answer follows the history the chain actually built.
//
// The second mode is created the way consensus creates one — scheduled, then made
// effective by the epoch-boundary promotion — rather than written into history for
// the test's convenience. A fabricated row would prove only that the query reads
// bytes at a version; a promoted one proves it resolves what promotion produced.
//
// It also carries the bootstrap underflow proof, which needs exactly this state to
// be falsifiable. Once a newer row exists, an unguarded target-2 subtraction
// underflows to an enormous epoch whose descending seek lands on the NEWEST
// version — so targets 1 and 2 continuing to resolve the anchor is a positive
// result rather than a coincidence of there being only one row.
func TestAPromotedModeChangesWhichRowATargetResolves(t *testing.T) {
	q, k, ctx, rewards := targetEpochFixture(t)

	require.NoError(t, k.ScheduledDistributionMode.Set(ctx, 2,
		types.ScheduledMiningDistributionMode{EffectiveEpoch: 2, Mode: protocolSelection}))
	rewards.finalize(1)
	require.NoError(t, k.EndBlock(ctx))

	// Target 3 binds epoch 1, which the promotion closed at 2. A closed interval
	// still covers the epochs inside it.
	res := interpret(t, q, ctx, 3)
	require.Equal(t, uint64(1), res.BindingEpoch)
	require.Equal(t, uint64(1), res.DistributionModeVersion.Version)
	require.Equal(t, trustedAS, res.DistributionModeVersion.Mode)
	require.Equal(t, uint64(2), res.DistributionModeVersion.ValidUntilEpochExclusive,
		"the full governing record is returned, closure and all")
	require.False(t, res.SelectionApplicable)

	// Target 4 binds epoch 2, where the successor became effective.
	res = interpret(t, q, ctx, 4)
	require.Equal(t, uint64(2), res.BindingEpoch)
	require.Equal(t, uint64(2), res.DistributionModeVersion.Version)
	require.Equal(t, protocolSelection, res.DistributionModeVersion.Mode)
	require.Zero(t, res.DistributionModeVersion.ValidUntilEpochExclusive, "the open interval")
	require.True(t, res.SelectionApplicable,
		"a complete N-2 binding onto the selection arm")

	// And the bootstrap targets are unmoved by the existence of a newer row.
	for _, target := range []uint64{1, 2} {
		res := interpret(t, q, ctx, target)
		require.True(t, res.BootstrapModeWithoutFullNMinus_2Binding)
		require.Zero(t, res.BindingEpoch)
		require.Equalf(t, uint64(1), res.DistributionModeVersion.Version,
			"target %d resolves the anchor, not the newest row an underflow would reach", target)
		require.Equal(t, trustedAS, res.DistributionModeVersion.Mode)
	}
}

// TestSelectionApplicabilityRequiresACompleteBinding separates the two conditions
// the field asserts together.
//
// selection_applicable is a statement about canonical BINDING: the target is bound
// to the selection arm AND that binding is complete. A bootstrap target resolves
// its mode from the genesis anchor rather than from its own N-2 boundary, so it
// has no complete binding to report no matter what mode the anchor carries.
//
// The anchor state here is built directly and is unreachable on a real chain: a
// fresh genesis may only anchor trusted distribution, and promotion closes an
// effective row without ever rewriting its mode. That is exactly why it needs
// constructing — the guard it proves would otherwise be unfalsifiable, and a
// profile that later admits a selection anchor would inherit an untested claim.
func TestSelectionApplicabilityRequiresACompleteBinding(t *testing.T) {
	q, k, ctx, _ := targetEpochFixture(t)

	anchor, err := k.DistributionModeVersions.Get(ctx, 1)
	require.NoError(t, err)
	anchor.Mode = protocolSelection
	require.NoError(t, k.DistributionModeVersions.Set(ctx, 1, anchor))

	for _, target := range []uint64{1, 2} {
		res := interpret(t, q, ctx, target)
		require.Equal(t, protocolSelection, res.DistributionModeVersion.Mode,
			"the governing row is reported exactly as stored")
		require.True(t, res.BootstrapModeWithoutFullNMinus_2Binding)
		require.Falsef(t, res.SelectionApplicable,
			"target %d resolves the selection arm without a complete N-2 binding", target)
	}

	// The same mode one boundary later, bound properly, is applicable — so the
	// refusal above is about the binding and not about the mode.
	res := interpret(t, q, ctx, 3)
	require.False(t, res.BootstrapModeWithoutFullNMinus_2Binding)
	require.Equal(t, protocolSelection, res.DistributionModeVersion.Mode)
	require.True(t, res.SelectionApplicable)
}

// TestADamagedModeHistoryIsInternalRatherThanAbsence inverts the usual not-found
// mapping, deliberately.
//
// Every initialized chain has a distribution-mode history: it is mandatory state,
// not an object a caller asks after. Its absence therefore means the chain is
// damaged, and a consumer told NotFound would reasonably read that as "no mode
// configured yet" and carry on — precisely the wrong response to a chain that
// cannot say how it distributes. Both the missing and the self-contradictory
// anchor are covered, because a reader that only handled absence would classify a
// corrupt row by whichever branch it happened to fall through.
func TestADamagedModeHistoryIsInternalRatherThanAbsence(t *testing.T) {
	// Targets on both sides of the bootstrap boundary: they reach the history
	// through different paths and must classify its damage identically.
	targets := []uint64{1, 2, 3, 12}

	t.Run("the anchor is missing", func(t *testing.T) {
		q, k, ctx, _ := targetEpochFixture(t)
		require.NoError(t, k.DistributionModeVersions.Remove(ctx, 1))

		for _, target := range targets {
			require.Equalf(t, codes.Internal, interpretErr(t, q, ctx, target),
				"target %d: a mandatory history that is absent is a damaged chain", target)
		}
	})

	t.Run("the anchor contradicts its own key", func(t *testing.T) {
		q, k, ctx, _ := targetEpochFixture(t)
		anchor, err := k.DistributionModeVersions.Get(ctx, 1)
		require.NoError(t, err)
		anchor.ValidFromEpoch = 7
		require.NoError(t, k.DistributionModeVersions.Set(ctx, 1, anchor))

		for _, target := range targets {
			require.Equalf(t, codes.Internal, interpretErr(t, q, ctx, target),
				"target %d: state that exists and cannot be trusted is never absence", target)
		}
	})
}
