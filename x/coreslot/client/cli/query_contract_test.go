package cli

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/twilight-project/twilight-core/internal/queryapi"
	"github.com/twilight-project/twilight-core/x/coreslot/types"
)

// recordingQueryClient implements types.QueryClient and records which RPC was
// reached.
//
// A fake rather than source inspection, deliberately: what must be proven is that
// Go dispatch sends THIS request to THAT method, and reading the switch only
// proves someone wrote a case. It needs no node, so this runs in the ordinary test
// suite rather than behind a localnet.
type recordingQueryClient struct{ called string }

func (c *recordingQueryClient) Params(_ context.Context, _ *types.QueryParamsRequest, _ ...grpc.CallOption) (*types.QueryParamsResponse, error) {
	c.called = "Params"
	return &types.QueryParamsResponse{}, nil
}

func (c *recordingQueryClient) CoreSlot(_ context.Context, _ *types.QueryCoreSlotRequest, _ ...grpc.CallOption) (*types.QueryCoreSlotResponse, error) {
	c.called = "CoreSlot"
	return &types.QueryCoreSlotResponse{}, nil
}

func (c *recordingQueryClient) CoreSlots(_ context.Context, _ *types.QueryCoreSlotsRequest, _ ...grpc.CallOption) (*types.QueryCoreSlotsResponse, error) {
	c.called = "CoreSlots"
	return &types.QueryCoreSlotsResponse{}, nil
}

func (c *recordingQueryClient) ActiveCoreSlots(_ context.Context, _ *types.QueryActiveCoreSlotsRequest, _ ...grpc.CallOption) (*types.QueryCoreSlotsResponse, error) {
	c.called = "ActiveCoreSlots"
	return &types.QueryCoreSlotsResponse{}, nil
}

func (c *recordingQueryClient) CoreSlotByOperator(_ context.Context, _ *types.QueryCoreSlotByOperatorRequest, _ ...grpc.CallOption) (*types.QueryCoreSlotResponse, error) {
	c.called = "CoreSlotByOperator"
	return &types.QueryCoreSlotResponse{}, nil
}

func (c *recordingQueryClient) CoreSlotByConsensusAddress(_ context.Context, _ *types.QueryCoreSlotByConsensusAddressRequest, _ ...grpc.CallOption) (*types.QueryCoreSlotResponse, error) {
	c.called = "CoreSlotByConsensusAddress"
	return &types.QueryCoreSlotResponse{}, nil
}

func (c *recordingQueryClient) PendingKeyRotations(_ context.Context, _ *types.QueryPendingKeyRotationsRequest, _ ...grpc.CallOption) (*types.QueryPendingKeyRotationsResponse, error) {
	c.called = "PendingKeyRotations"
	return &types.QueryPendingKeyRotationsResponse{}, nil
}

func (c *recordingQueryClient) LastAppliedValidators(_ context.Context, _ *types.QueryLastAppliedValidatorsRequest, _ ...grpc.CallOption) (*types.QueryLastAppliedValidatorsResponse, error) {
	c.called = "LastAppliedValidators"
	return &types.QueryLastAppliedValidatorsResponse{}, nil
}

func (c *recordingQueryClient) ReservedConsensusAddress(_ context.Context, _ *types.QueryReservedConsensusAddressRequest, _ ...grpc.CallOption) (*types.QueryReservedConsensusAddressResponse, error) {
	c.called = "ReservedConsensusAddress"
	return &types.QueryReservedConsensusAddressResponse{}, nil
}

func (c *recordingQueryClient) RewardWeight(_ context.Context, _ *types.QueryRewardWeightRequest, _ ...grpc.CallOption) (*types.QueryRewardWeightResponse, error) {
	c.called = "RewardWeight"
	return &types.QueryRewardWeightResponse{}, nil
}

func (c *recordingQueryClient) SelectionPolicy(_ context.Context, _ *types.QuerySelectionPolicyRequest, _ ...grpc.CallOption) (*types.QuerySelectionPolicyResponse, error) {
	c.called = "SelectionPolicy"
	return &types.QuerySelectionPolicyResponse{}, nil
}

func (c *recordingQueryClient) SelectionPolicyVersion(_ context.Context, _ *types.QuerySelectionPolicyVersionRequest, _ ...grpc.CallOption) (*types.QuerySelectionPolicyResponse, error) {
	c.called = "SelectionPolicyVersion"
	return &types.QuerySelectionPolicyResponse{}, nil
}

func (c *recordingQueryClient) SelectionPolicyAtHeight(_ context.Context, _ *types.QuerySelectionPolicyAtHeightRequest, _ ...grpc.CallOption) (*types.QuerySelectionPolicyResponse, error) {
	c.called = "SelectionPolicyAtHeight"
	return &types.QuerySelectionPolicyResponse{}, nil
}

// TestQuerySurfaceMatchesPinnedContract holds this query surface to the
// hand-written contract in internal/queryapi.
//
// Three assertions, because any two of them can pass while the surface is broken:
//
//	commands  the pinned set equals the registered cobra children BOTH ways, so
//	          deleting a command and its contract row does not cancel out
//	request   each command builds the request the contract names, so a command
//	          wired to the wrong request is caught before dispatch
//	rpc       each request reaches the RPC the contract names, recorded from a
//	          real dispatch call rather than read out of the source
func TestQuerySurfaceMatchesPinnedContract(t *testing.T) {
	cmd, specs := buildQueryCmd()
	pinned := queryapi.ForModule("coreslot")
	require.NotEmpty(t, pinned, "the pinned contract has no entries for this module")

	expected := make([]string, 0, len(pinned))
	for _, e := range pinned {
		expected = append(expected, e.Command)
	}

	registered := make([]string, 0, len(cmd.Commands()))
	for _, c := range cmd.Commands() {
		registered = append(registered, c.Name())
	}
	require.ElementsMatch(t, expected, registered,
		"registered commands and the pinned contract disagree; a command was added or removed without updating internal/queryapi")

	built := make([]string, 0, len(specs))
	byName := make(map[string]querySpec, len(specs))
	for _, s := range specs {
		built = append(built, s.name)
		byName[s.name] = s
	}
	require.ElementsMatch(t, expected, built,
		"a command is registered without a request builder, or vice versa")

	for _, e := range pinned {
		t.Run(e.Command, func(t *testing.T) {
			sp, ok := byName[e.Command]
			require.True(t, ok, "no registered command named %q", e.Command)

			var sub *cobra.Command
			for _, c := range cmd.Commands() {
				if c.Name() == e.Command {
					sub = c
				}
			}
			require.NotNil(t, sub, "no cobra child named %q", e.Command)

			req, err := sp.build(e.Args)
			require.NoError(t, err, "the command could not build its request from the contract's sample arguments")
			require.Equal(t, e.Request, strings.TrimPrefix(fmt.Sprintf("%T", req), "*types."),
				"this command builds a different request than the contract pins")

			fake := &recordingQueryClient{}
			if _, err := dispatchQuery(context.Background(), fake, req); err != nil {
				t.Fatalf("dispatch refused the request this command builds: %v", err)
			}
			require.Equal(t, e.RPC, fake.called,
				"the request reached a different query than the contract pins")
		})
	}
}
