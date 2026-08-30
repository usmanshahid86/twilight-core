package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	gogoproto "github.com/cosmos/gogoproto/proto"

	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// GetQueryCmd returns the read-only rewards query command tree, mirroring the
// repository's coreslot CLI convention (a top-level `rewards-query` command).
func GetQueryCmd() *cobra.Command {
	cmd, _ := buildQueryCmd()
	return cmd
}

// querySpec pairs a registered command with the request it builds.
type querySpec struct {
	name  string
	build func(*cobra.Command, []string) (interface{}, error)
}

// buildQueryCmd returns the command tree AND the command/request pairs it
// registered.
//
// The pairs exist so the pinned contract test can assert WHICH REQUEST each
// command builds without standing up a node. Without them a test can only compare
// the set of registered commands against the set of dispatch cases, and that
// comparison passes when a command is wired to the wrong request — which is the
// same class of defect as the missing cases this file just fixed, one step
// earlier in the path.
func buildQueryCmd() (*cobra.Command, []querySpec) {
	var specs []querySpec
	cmd := &cobra.Command{Use: "rewards-query", Short: "Query rewards module state", DisableFlagParsing: true, SuggestionsMinimumDistance: 2}
	add := func(use string, args cobra.PositionalArgs, paginated bool, request func(*cobra.Command, []string) (interface{}, error)) *cobra.Command {
		specs = append(specs, querySpec{name: strings.Fields(use)[0], build: request})
		q := &cobra.Command{Use: use, Args: args, RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			req, err := request(cmd, args)
			if err != nil {
				return err
			}
			resp, err := dispatchQuery(cmd.Context(), types.NewQueryClient(clientCtx), req)
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(resp)
		}}
		flags.AddQueryFlagsToCmd(q)
		if paginated {
			flags.AddPaginationFlagsToCmd(q, q.Name())
		}
		return q
	}
	cmd.AddCommand(
		add("params", cobra.NoArgs, false, func(*cobra.Command, []string) (interface{}, error) { return &types.QueryParamsRequest{}, nil }),
		add("epoch-info", cobra.NoArgs, false, func(*cobra.Command, []string) (interface{}, error) { return &types.QueryEpochInfoRequest{}, nil }),
		add("next-halving", cobra.NoArgs, false, func(*cobra.Command, []string) (interface{}, error) { return &types.QueryNextHalvingRequest{}, nil }),
		add("epoch-reward [epoch]", cobra.ExactArgs(1), false, func(_ *cobra.Command, a []string) (interface{}, error) { return buildEpochRewardRequest(a) }),
		add("cumulative-emitted", cobra.NoArgs, false, func(*cobra.Command, []string) (interface{}, error) {
			return &types.QueryCumulativeEmittedRequest{}, nil
		}),
		add("supply-schedule", cobra.NoArgs, false, func(*cobra.Command, []string) (interface{}, error) { return &types.QuerySupplyScheduleRequest{}, nil }),
		add("current-active-blocks", cobra.NoArgs, true, func(cmd *cobra.Command, _ []string) (interface{}, error) {
			return buildCurrentActiveBlocksRequest(cmd.Flags())
		}),
		add("module-balances", cobra.NoArgs, false, func(*cobra.Command, []string) (interface{}, error) { return &types.QueryModuleBalancesRequest{}, nil }),
		add("epoch-config-versions", cobra.NoArgs, true, func(cmd *cobra.Command, _ []string) (interface{}, error) {
			return buildEpochConfigVersionsRequest(cmd.Flags())
		}),
		add("epoch-boundaries [epoch]", cobra.ExactArgs(1), false, func(_ *cobra.Command, a []string) (interface{}, error) {
			return buildEpochBoundariesRequest(a)
		}),
		add("pause-state", cobra.NoArgs, false, func(*cobra.Command, []string) (interface{}, error) {
			return &types.QueryRewardsPauseStateRequest{}, nil
		}),
		add("entitlement [slot-id] [epoch]", cobra.ExactArgs(2), false, func(_ *cobra.Command, a []string) (interface{}, error) {
			return buildSlotEntitlementRequest(a)
		}),
		add("epoch-entitlements [epoch]", cobra.ExactArgs(1), true, func(cmd *cobra.Command, a []string) (interface{}, error) {
			return buildEpochEntitlementsRequest(a, cmd.Flags())
		}),
		add("reward-config-versions", cobra.NoArgs, true, func(cmd *cobra.Command, _ []string) (interface{}, error) {
			return buildRewardConfigVersionsRequest(cmd.Flags())
		}),
		// One selector, positional, so the two identity forms cannot be given
		// together at the command line either.
		add("reward-config-version [version|epoch:N]", cobra.ExactArgs(1), false, func(_ *cobra.Command, a []string) (interface{}, error) {
			return buildRewardConfigVersionRequest(a)
		}),
	)
	return cmd, specs
}

// dispatchQuery routes a typed request to the generated query client.
func dispatchQuery(ctx context.Context, qc types.QueryClient, req interface{}) (gogoproto.Message, error) {
	switch v := req.(type) {
	case *types.QueryParamsRequest:
		return qc.Params(ctx, v)
	case *types.QueryEpochInfoRequest:
		return qc.EpochInfo(ctx, v)
	case *types.QueryNextHalvingRequest:
		return qc.NextHalving(ctx, v)
	case *types.QueryEpochRewardRequest:
		return qc.EpochReward(ctx, v)
	case *types.QuerySlotEntitlementRequest:
		return qc.SlotEntitlement(ctx, v)
	case *types.QuerySlotEntitlementsByEpochRequest:
		return qc.SlotEntitlementsByEpoch(ctx, v)
	case *types.QueryRewardConfigVersionsRequest:
		return qc.RewardConfigVersions(ctx, v)
	case *types.QueryRewardConfigVersionRequest:
		return qc.RewardConfigVersion(ctx, v)
	case *types.QueryCumulativeEmittedRequest:
		return qc.CumulativeEmitted(ctx, v)
	case *types.QuerySupplyScheduleRequest:
		return qc.SupplySchedule(ctx, v)
	case *types.QueryCurrentEpochActiveBlocksRequest:
		return qc.CurrentEpochActiveBlocks(ctx, v)
	case *types.QueryModuleBalancesRequest:
		return qc.ModuleBalances(ctx, v)
	// These three were registered as commands but had no case here, so every
	// invocation fell through to the error below. `rewards-query pause-state`
	// printed usage and has never returned data.
	case *types.QueryEpochConfigVersionsRequest:
		return qc.EpochConfigVersions(ctx, v)
	case *types.QueryEpochBoundariesRequest:
		return qc.EpochBoundaries(ctx, v)
	case *types.QueryRewardsPauseStateRequest:
		return qc.RewardsPauseState(ctx, v)
	}
	return nil, fmt.Errorf("unsupported query request %T", req)
}

func buildEpochRewardRequest(args []string) (*types.QueryEpochRewardRequest, error) {
	epoch, err := strconv.ParseUint(args[0], 10, 64)
	if err != nil {
		return nil, err
	}
	return &types.QueryEpochRewardRequest{EpochNumber: epoch}, nil
}

func buildCurrentActiveBlocksRequest(fs *pflag.FlagSet) (*types.QueryCurrentEpochActiveBlocksRequest, error) {
	page, err := client.ReadPageRequest(fs)
	if err != nil {
		return nil, err
	}
	return &types.QueryCurrentEpochActiveBlocksRequest{Pagination: page}, nil
}

func buildSlotEntitlementRequest(args []string) (*types.QuerySlotEntitlementRequest, error) {
	slotID, err := strconv.ParseUint(args[0], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid slot id %q: %w", args[0], err)
	}
	epoch, err := strconv.ParseUint(args[1], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid epoch %q: %w", args[1], err)
	}
	return &types.QuerySlotEntitlementRequest{SlotId: slotID, Epoch: epoch}, nil
}

func buildEpochEntitlementsRequest(args []string, fs *pflag.FlagSet) (*types.QuerySlotEntitlementsByEpochRequest, error) {
	epoch, err := strconv.ParseUint(args[0], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid epoch %q: %w", args[0], err)
	}
	page, err := client.ReadPageRequest(fs)
	if err != nil {
		return nil, err
	}
	return &types.QuerySlotEntitlementsByEpochRequest{Epoch: epoch, Pagination: page}, nil
}

func buildRewardConfigVersionsRequest(fs *pflag.FlagSet) (*types.QueryRewardConfigVersionsRequest, error) {
	page, err := client.ReadPageRequest(fs)
	if err != nil {
		return nil, err
	}
	return &types.QueryRewardConfigVersionsRequest{Pagination: page}, nil
}

// buildRewardConfigVersionRequest parses the single identity selector.
//
// A bare number is a version; the "epoch:" prefix selects by effective epoch. One
// positional argument rather than two optional flags, because the query admits
// exactly one selector and an argument shape that cannot express both is a
// clearer contract than a runtime rejection of the pair.
func buildRewardConfigVersionRequest(args []string) (*types.QueryRewardConfigVersionRequest, error) {
	if epoch, ok := strings.CutPrefix(args[0], "epoch:"); ok {
		effective, err := strconv.ParseUint(epoch, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid effective epoch %q: %w", epoch, err)
		}
		return &types.QueryRewardConfigVersionRequest{EffectiveEpoch: effective}, nil
	}
	version, err := strconv.ParseUint(args[0], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid reward configuration version %q: %w", args[0], err)
	}
	return &types.QueryRewardConfigVersionRequest{Version: version}, nil
}

func buildEpochConfigVersionsRequest(fs *pflag.FlagSet) (*types.QueryEpochConfigVersionsRequest, error) {
	page, err := client.ReadPageRequest(fs)
	if err != nil {
		return nil, err
	}
	return &types.QueryEpochConfigVersionsRequest{Pagination: page}, nil
}

func buildEpochBoundariesRequest(args []string) (*types.QueryEpochBoundariesRequest, error) {
	epoch, err := strconv.ParseUint(args[0], 10, 64)
	if err != nil {
		return nil, err
	}
	return &types.QueryEpochBoundariesRequest{EpochNumber: epoch}, nil
}
