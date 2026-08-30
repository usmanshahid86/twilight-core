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
	"github.com/twilight-project/twilight-core/x/rewards/types"
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

func (c *recordingQueryClient) EpochInfo(_ context.Context, _ *types.QueryEpochInfoRequest, _ ...grpc.CallOption) (*types.QueryEpochInfoResponse, error) {
	c.called = "EpochInfo"
	return &types.QueryEpochInfoResponse{}, nil
}

func (c *recordingQueryClient) NextHalving(_ context.Context, _ *types.QueryNextHalvingRequest, _ ...grpc.CallOption) (*types.QueryNextHalvingResponse, error) {
	c.called = "NextHalving"
	return &types.QueryNextHalvingResponse{}, nil
}

func (c *recordingQueryClient) EpochReward(_ context.Context, _ *types.QueryEpochRewardRequest, _ ...grpc.CallOption) (*types.QueryEpochRewardResponse, error) {
	c.called = "EpochReward"
	return &types.QueryEpochRewardResponse{}, nil
}

func (c *recordingQueryClient) CumulativeEmitted(_ context.Context, _ *types.QueryCumulativeEmittedRequest, _ ...grpc.CallOption) (*types.QueryCumulativeEmittedResponse, error) {
	c.called = "CumulativeEmitted"
	return &types.QueryCumulativeEmittedResponse{}, nil
}

func (c *recordingQueryClient) SupplySchedule(_ context.Context, _ *types.QuerySupplyScheduleRequest, _ ...grpc.CallOption) (*types.QuerySupplyScheduleResponse, error) {
	c.called = "SupplySchedule"
	return &types.QuerySupplyScheduleResponse{}, nil
}

func (c *recordingQueryClient) CurrentEpochActiveBlocks(_ context.Context, _ *types.QueryCurrentEpochActiveBlocksRequest, _ ...grpc.CallOption) (*types.QueryCurrentEpochActiveBlocksResponse, error) {
	c.called = "CurrentEpochActiveBlocks"
	return &types.QueryCurrentEpochActiveBlocksResponse{}, nil
}

func (c *recordingQueryClient) ModuleBalances(_ context.Context, _ *types.QueryModuleBalancesRequest, _ ...grpc.CallOption) (*types.QueryModuleBalancesResponse, error) {
	c.called = "ModuleBalances"
	return &types.QueryModuleBalancesResponse{}, nil
}

func (c *recordingQueryClient) EpochConfigVersions(_ context.Context, _ *types.QueryEpochConfigVersionsRequest, _ ...grpc.CallOption) (*types.QueryEpochConfigVersionsResponse, error) {
	c.called = "EpochConfigVersions"
	return &types.QueryEpochConfigVersionsResponse{}, nil
}

func (c *recordingQueryClient) EpochBoundaries(_ context.Context, _ *types.QueryEpochBoundariesRequest, _ ...grpc.CallOption) (*types.QueryEpochBoundariesResponse, error) {
	c.called = "EpochBoundaries"
	return &types.QueryEpochBoundariesResponse{}, nil
}

func (c *recordingQueryClient) SlotEntitlement(_ context.Context, _ *types.QuerySlotEntitlementRequest, _ ...grpc.CallOption) (*types.QuerySlotEntitlementResponse, error) {
	c.called = "SlotEntitlement"
	return &types.QuerySlotEntitlementResponse{}, nil
}

func (c *recordingQueryClient) SlotEntitlementsByEpoch(_ context.Context, _ *types.QuerySlotEntitlementsByEpochRequest, _ ...grpc.CallOption) (*types.QuerySlotEntitlementsByEpochResponse, error) {
	c.called = "SlotEntitlementsByEpoch"
	return &types.QuerySlotEntitlementsByEpochResponse{}, nil
}

func (c *recordingQueryClient) RewardConfigVersions(_ context.Context, _ *types.QueryRewardConfigVersionsRequest, _ ...grpc.CallOption) (*types.QueryRewardConfigVersionsResponse, error) {
	c.called = "RewardConfigVersions"
	return &types.QueryRewardConfigVersionsResponse{}, nil
}

func (c *recordingQueryClient) RewardConfigVersion(_ context.Context, _ *types.QueryRewardConfigVersionRequest, _ ...grpc.CallOption) (*types.QueryRewardConfigVersionResponse, error) {
	c.called = "RewardConfigVersion"
	return &types.QueryRewardConfigVersionResponse{}, nil
}

func (c *recordingQueryClient) RewardsPauseState(_ context.Context, _ *types.QueryRewardsPauseStateRequest, _ ...grpc.CallOption) (*types.QueryRewardsPauseStateResponse, error) {
	c.called = "RewardsPauseState"
	return &types.QueryRewardsPauseStateResponse{}, nil
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
	pinned := queryapi.ForModule("rewards")
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
