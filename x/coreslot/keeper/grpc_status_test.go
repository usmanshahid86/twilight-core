package keeper_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"cosmossdk.io/collections"

	"github.com/cosmos/cosmos-sdk/baseapp"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/x/coreslot/keeper"
	"github.com/twilight-project/twilight-core/x/coreslot/types"
)

// The status a query answers with is public API. A caller — a REST client, an
// indexer, a wallet — reads the code, not the message, and the difference between
// "this slot has no policy" and "this chain's stored policy state contradicts
// itself" is exactly the difference between a 404 and a page for the operator.
//
// These tests go through a REAL gRPC query router and the generated client rather
// than calling the query server and comparing errors with errors.Is. That
// distinction matters: the mapping being asserted lives at the boundary the
// router crosses, so a direct call would happily pass while every actual client
// saw Unknown.

// policyQueryClient registers the module's query service on the SDK's gRPC query
// router and returns a generated client bound to it.
func policyQueryClient(t *testing.T, k keeper.Keeper, ctx sdk.Context) types.QueryClient {
	t.Helper()
	registry := codectypes.NewInterfaceRegistry()
	types.RegisterInterfaces(registry)
	router := baseapp.NewQueryServerTestHelper(ctx, registry)
	types.RegisterQueryServer(router, keeper.NewQueryServer(k))
	return types.NewQueryClient(router)
}

// TestSelectionPolicyQueryAbsenceIsNotFound pins ordinary absence to NotFound on
// all three policy queries.
func TestSelectionPolicyQueryAbsenceIsNotFound(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	op := policySlotGenesis(t, k, ctx, authority, emergency)
	msgs := keeper.NewMsgServer(k)

	updateCtx := ctx.WithBlockHeight(400)
	_, err := msgs.UpdateSelectionPolicy(updateCtx, &types.MsgUpdateSelectionPolicy{
		Operator: op, SlotId: 1, SelectionRateBps: 4_321, MaxSelectedParticipants: 33,
	})
	require.NoError(t, err)

	client := policyQueryClient(t, k, updateCtx)
	background := context.Background()

	// A present policy still answers normally: without this the NotFound
	// assertions below could be satisfied by a router that fails at everything.
	current, err := client.SelectionPolicy(background, &types.QuerySelectionPolicyRequest{SlotId: 1})
	require.NoError(t, err)
	require.Equal(t, uint64(2), current.Policy.PolicyVersion)

	for _, tc := range []struct {
		name string
		call func() error
	}{
		{
			name: "an unknown slot has no current policy",
			call: func() error {
				_, err := client.SelectionPolicy(background, &types.QuerySelectionPolicyRequest{SlotId: 404})
				return err
			},
		},
		{
			name: "an unknown version of a known slot",
			call: func() error {
				_, err := client.SelectionPolicyVersion(background,
					&types.QuerySelectionPolicyVersionRequest{SlotId: 1, PolicyVersion: 99})
				return err
			},
		},
		{
			name: "any version of an unknown slot",
			call: func() error {
				_, err := client.SelectionPolicyVersion(background,
					&types.QuerySelectionPolicyVersionRequest{SlotId: 404, PolicyVersion: 1})
				return err
			},
		},
		{
			name: "a height below the slot's first version",
			call: func() error {
				_, err := client.SelectionPolicyAtHeight(background,
					&types.QuerySelectionPolicyAtHeightRequest{SlotId: 1, AtHeight: 0})
				return err
			},
		},
		{
			name: "any height of an unknown slot",
			call: func() error {
				_, err := client.SelectionPolicyAtHeight(background,
					&types.QuerySelectionPolicyAtHeightRequest{SlotId: 404, AtHeight: 500})
				return err
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			require.Error(t, err)
			require.Equalf(t, codes.NotFound, grpcstatus.Code(err),
				"ordinary absence must reach a client as NotFound, got %v", grpcstatus.Code(err))
		})
	}
}

// TestSelectionPolicyQueryCorruptionIsInternal pins the other half of the
// contract: a disagreement between policy history and its derived index is a
// state-integrity failure and must NOT be reported as an ordinary absence, which
// would tell a client the chain is fine and the data simply is not there.
func TestSelectionPolicyQueryCorruptionIsInternal(t *testing.T) {
	for _, tc := range []struct {
		name    string
		corrupt func(t *testing.T, k keeper.Keeper, ctx sdk.Context)
		call    func(client types.QueryClient) error
	}{
		{
			name: "the index names a version that does not exist",
			corrupt: func(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
				require.NoError(t, k.PolicyStarts.Set(ctx, collections.Join(uint64(1), int64(5)), uint64(99)))
			},
			call: func(client types.QueryClient) error {
				_, err := client.SelectionPolicyAtHeight(context.Background(),
					&types.QuerySelectionPolicyAtHeightRequest{SlotId: 1, AtHeight: 10})
				return err
			},
		},
		{
			name: "the pointer names a version that does not exist",
			corrupt: func(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
				stored, err := k.GetSlot(ctx, 1)
				require.NoError(t, err)
				stored.CurrentSelectionPolicyVersion = 77
				require.NoError(t, k.Slots.Set(ctx, 1, stored))
			},
			call: func(client types.QueryClient) error {
				_, err := client.SelectionPolicy(context.Background(), &types.QuerySelectionPolicyRequest{SlotId: 1})
				return err
			},
		},
		{
			name: "an exact version row disagrees with its own key",
			corrupt: func(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
				// Stored under (slot 1, version 2) while claiming to be a different
				// slot and version entirely.
				require.NoError(t, k.SelectionPolicies.Set(ctx, collections.Join(uint64(1), uint64(2)),
					types.SelectionPolicyVersion{
						SlotId: 9, PolicyVersion: 5, SelectionRateBps: 2_500, MaxSelectedParticipants: 10,
						ValidFromHeight: 1,
					}))
			},
			call: func(client types.QueryClient) error {
				_, err := client.SelectionPolicyVersion(context.Background(),
					&types.QuerySelectionPolicyVersionRequest{SlotId: 1, PolicyVersion: 2})
				return err
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx, authority, emergency := setup(t)
			policySlotGenesis(t, k, ctx, authority, emergency)
			tc.corrupt(t, k, ctx)

			err := tc.call(policyQueryClient(t, k, ctx))
			require.Error(t, err)
			require.NotEqual(t, codes.NotFound, grpcstatus.Code(err),
				"state corruption must never be reported as an ordinary absence")
			require.Equal(t, codes.Internal, grpcstatus.Code(err))
		})
	}
}

// TestSelectionPolicyExactVersionIdentityIsChecked states the identity rule
// directly at the keeper's query surface, including that a mismatched row is
// neither returned as a success nor downgraded to not-found.
func TestSelectionPolicyExactVersionIdentityIsChecked(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	policySlotGenesis(t, k, ctx, authority, emergency)
	qs := keeper.NewQueryServer(k)

	// A well-formed row under the wrong key: everything about it is plausible
	// except where it lives.
	require.NoError(t, k.SelectionPolicies.Set(ctx, collections.Join(uint64(1), uint64(2)),
		types.SelectionPolicyVersion{
			SlotId: 1, PolicyVersion: 3, SelectionRateBps: 2_500, MaxSelectedParticipants: 10,
			ValidFromHeight: 1,
		}))

	resp, err := qs.SelectionPolicyVersion(ctx, &types.QuerySelectionPolicyVersionRequest{SlotId: 1, PolicyVersion: 2})
	require.Nil(t, resp)
	require.ErrorIs(t, err, types.ErrInvalidSelectionPolicy)
	require.NotErrorIs(t, err, types.ErrSelectionPolicyNotFound,
		"a contradiction is not an absence")
	require.Contains(t, err.Error(), "row identity does not match its key")

	// The slot component is checked too, not only the version.
	require.NoError(t, k.SelectionPolicies.Set(ctx, collections.Join(uint64(1), uint64(2)),
		types.SelectionPolicyVersion{
			SlotId: 2, PolicyVersion: 2, SelectionRateBps: 2_500, MaxSelectedParticipants: 10,
			ValidFromHeight: 1,
		}))
	_, err = qs.SelectionPolicyVersion(ctx, &types.QuerySelectionPolicyVersionRequest{SlotId: 1, PolicyVersion: 2})
	require.ErrorIs(t, err, types.ErrInvalidSelectionPolicy)

	// An intact row is still returned unchanged.
	intact, err := qs.SelectionPolicyVersion(ctx, &types.QuerySelectionPolicyVersionRequest{SlotId: 1, PolicyVersion: 1})
	require.NoError(t, err)
	require.Equal(t, uint64(1), intact.Policy.PolicyVersion)
	require.Equal(t, uint64(1), intact.Policy.SlotId)
}
