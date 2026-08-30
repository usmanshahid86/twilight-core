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
	"github.com/twilight-project/twilight-core/x/mining/types"
)

// recordingQueryClient implements types.QueryClient and records which RPC was
// reached.
//
// A fake rather than source inspection, deliberately: what must be proven is that
// Go dispatch sends THIS request to THAT method, and reading the switch only
// proves someone wrote a case. It needs no node, so this runs in the ordinary test
// suite rather than behind a localnet.
type recordingQueryClient struct{ called string }

func (c *recordingQueryClient) Settlement(_ context.Context, _ *types.QuerySettlementRequest, _ ...grpc.CallOption) (*types.QuerySettlementResponse, error) {
	c.called = "Settlement"
	return &types.QuerySettlementResponse{}, nil
}

func (c *recordingQueryClient) OpenSettlements(_ context.Context, _ *types.QueryOpenSettlementsRequest, _ ...grpc.CallOption) (*types.QueryOpenSettlementsResponse, error) {
	c.called = "OpenSettlements"
	return &types.QueryOpenSettlementsResponse{}, nil
}

func (c *recordingQueryClient) SettlementClock(_ context.Context, _ *types.QuerySettlementClockRequest, _ ...grpc.CallOption) (*types.QuerySettlementClockResponse, error) {
	c.called = "SettlementClock"
	return &types.QuerySettlementClockResponse{}, nil
}

func (c *recordingQueryClient) DistributionModeVersion(_ context.Context, _ *types.QueryDistributionModeVersionRequest, _ ...grpc.CallOption) (*types.QueryDistributionModeVersionResponse, error) {
	c.called = "DistributionModeVersion"
	return &types.QueryDistributionModeVersionResponse{}, nil
}

func (c *recordingQueryClient) DistributionModeVersions(_ context.Context, _ *types.QueryDistributionModeVersionsRequest, _ ...grpc.CallOption) (*types.QueryDistributionModeVersionsResponse, error) {
	c.called = "DistributionModeVersions"
	return &types.QueryDistributionModeVersionsResponse{}, nil
}

func (c *recordingQueryClient) SelectionParamsVersion(_ context.Context, _ *types.QuerySelectionParamsVersionRequest, _ ...grpc.CallOption) (*types.QuerySelectionParamsVersionResponse, error) {
	c.called = "SelectionParamsVersion"
	return &types.QuerySelectionParamsVersionResponse{}, nil
}

func (c *recordingQueryClient) SelectionParamsVersions(_ context.Context, _ *types.QuerySelectionParamsVersionsRequest, _ ...grpc.CallOption) (*types.QuerySelectionParamsVersionsResponse, error) {
	c.called = "SelectionParamsVersions"
	return &types.QuerySelectionParamsVersionsResponse{}, nil
}

func (c *recordingQueryClient) SettlementParamsVersion(_ context.Context, _ *types.QuerySettlementParamsVersionRequest, _ ...grpc.CallOption) (*types.QuerySettlementParamsVersionResponse, error) {
	c.called = "SettlementParamsVersion"
	return &types.QuerySettlementParamsVersionResponse{}, nil
}

func (c *recordingQueryClient) SettlementParamsVersions(_ context.Context, _ *types.QuerySettlementParamsVersionsRequest, _ ...grpc.CallOption) (*types.QuerySettlementParamsVersionsResponse, error) {
	c.called = "SettlementParamsVersions"
	return &types.QuerySettlementParamsVersionsResponse{}, nil
}

func (c *recordingQueryClient) TargetEpochInterpretation(_ context.Context, _ *types.QueryTargetEpochInterpretationRequest, _ ...grpc.CallOption) (*types.QueryTargetEpochInterpretationResponse, error) {
	c.called = "TargetEpochInterpretation"
	return &types.QueryTargetEpochInterpretationResponse{}, nil
}

func (c *recordingQueryClient) ValidateEconomicAddress(_ context.Context, _ *types.QueryValidateEconomicAddressRequest, _ ...grpc.CallOption) (*types.QueryValidateEconomicAddressResponse, error) {
	c.called = "ValidateEconomicAddress"
	return &types.QueryValidateEconomicAddressResponse{}, nil
}

// TestQuerySurfaceMatchesPinnedContract holds this query surface to the
// hand-written contract in internal/queryapi.
//
// Three assertions, because any two of them can pass while the surface is broken:
//
//	commands  the pinned set equals the registered cobra children BOTH ways, so
//	          removing a registration is caught while its pinned row remains. It is
//	          NOT a claim that deleting the row as well is caught — nothing could
//	          catch that. The point is that the row lives in a separate,
//	          hand-maintained file, so removing it is a deliberate edit rather than
//	          a side effect of touching this one
//	runnable  the pinned child actually has a RunE, so the builder and dispatch
//	          asserted below are on a path the command can still reach
//	request   each command builds the request the contract names, so a command
//	          wired to the wrong request is caught before dispatch
//	rpc       each request reaches the RPC the contract names, recorded from a
//	          real dispatch call rather than read out of the source
func TestQuerySurfaceMatchesPinnedContract(t *testing.T) {
	cmd, specs := buildQueryCmd()
	pinned := queryapi.ForModule("mining")
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

			// Without this the two assertions below prove only that the captured
			// builder and dispatchQuery work when called DIRECTLY — which they do
			// even if the command can no longer call them. Renaming RunE to PreRunE
			// leaves the command registered, leaves its builder captured, leaves
			// every assertion below green, and makes the command a no-op that
			// returns success having issued no query at all. That is the same
			// presence-versus-function gap this whole file exists for, one level up.
			require.NotNil(t, sub.RunE,
				"the command is registered but has no RunE, so nothing on the real execution path reaches the builder and dispatch asserted below")

			req, err := sp.build(sub, e.Args)
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
