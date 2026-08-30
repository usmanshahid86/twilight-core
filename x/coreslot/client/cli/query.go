package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	gogoproto "github.com/cosmos/gogoproto/proto"

	"github.com/twilight-project/twilight-core/x/coreslot/types"
)

// dispatchQuery routes a typed request to the generated query client.
//
// Extracted from an inline switch inside the command body, which had NO default
// branch. A request with no case left `response` as a nil interface, `err` nil,
// and the code then reached `response.(gogoproto.Message)` — a type assertion on
// nil, which panics. So the failure mode for a missing case was a stack trace in
// an operator's terminal rather than a message naming the problem.
//
// Nothing reached that path today: every registered command had a case. But the
// same gap in x/rewards shipped three commands that never worked (#136), and the
// difference between the two modules was only that rewards failed politely. The
// explicit error below makes coreslot fail the same way.
func dispatchQuery(ctx context.Context, qc types.QueryClient, req interface{}) (gogoproto.Message, error) {
	switch v := req.(type) {
	case *types.QueryParamsRequest:
		return qc.Params(ctx, v)
	case *types.QueryCoreSlotRequest:
		return qc.CoreSlot(ctx, v)
	case *types.QueryCoreSlotsRequest:
		return qc.CoreSlots(ctx, v)
	case *types.QueryActiveCoreSlotsRequest:
		return qc.ActiveCoreSlots(ctx, v)
	case *types.QueryCoreSlotByOperatorRequest:
		return qc.CoreSlotByOperator(ctx, v)
	case *types.QueryCoreSlotByConsensusAddressRequest:
		return qc.CoreSlotByConsensusAddress(ctx, v)
	case *types.QueryPendingKeyRotationsRequest:
		return qc.PendingKeyRotations(ctx, v)
	case *types.QueryLastAppliedValidatorsRequest:
		return qc.LastAppliedValidators(ctx, v)
	case *types.QueryReservedConsensusAddressRequest:
		return qc.ReservedConsensusAddress(ctx, v)
	case *types.QueryRewardWeightRequest:
		return qc.RewardWeight(ctx, v)
	case *types.QuerySelectionPolicyRequest:
		return qc.SelectionPolicy(ctx, v)
	case *types.QuerySelectionPolicyVersionRequest:
		return qc.SelectionPolicyVersion(ctx, v)
	case *types.QuerySelectionPolicyAtHeightRequest:
		return qc.SelectionPolicyAtHeight(ctx, v)
	}
	return nil, fmt.Errorf("unsupported query request %T", req)
}

func GetQueryCmd() *cobra.Command {
	cmd, _ := buildQueryCmd()
	return cmd
}

// querySpec pairs a registered command with the request it builds; see the
// equivalent in x/rewards for why the pairs are exposed.
type querySpec struct {
	name  string
	build func([]string) (interface{}, error)
}

func buildQueryCmd() (*cobra.Command, []querySpec) {
	var specs []querySpec
	cmd := &cobra.Command{Use: "coreslot-query", Short: "Query core slots", DisableFlagParsing: true, SuggestionsMinimumDistance: 2}
	add := func(use string, args cobra.PositionalArgs, request func([]string) (interface{}, error)) *cobra.Command {
		specs = append(specs, querySpec{name: strings.Fields(use)[0], build: request})
		q := &cobra.Command{Use: use, Args: args, RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			req, err := request(args)
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
		return q
	}
	parseID := func(args []string) (uint64, error) { return strconv.ParseUint(args[0], 10, 64) }
	cmd.AddCommand(
		add("params", cobra.NoArgs, func([]string) (interface{}, error) { return &types.QueryParamsRequest{}, nil }),
		add("slot [slot-id]", cobra.ExactArgs(1), func(a []string) (interface{}, error) {
			id, e := parseID(a)
			return &types.QueryCoreSlotRequest{SlotId: id}, e
		}),
		add("slots", cobra.NoArgs, func([]string) (interface{}, error) { return &types.QueryCoreSlotsRequest{}, nil }),
		add("active", cobra.NoArgs, func([]string) (interface{}, error) { return &types.QueryActiveCoreSlotsRequest{}, nil }),
		add("by-operator [address]", cobra.ExactArgs(1), func(a []string) (interface{}, error) {
			return &types.QueryCoreSlotByOperatorRequest{OperatorAddress: a[0]}, nil
		}),
		add("by-consensus [hex-address]", cobra.ExactArgs(1), func(a []string) (interface{}, error) {
			return &types.QueryCoreSlotByConsensusAddressRequest{ConsensusAddress: a[0]}, nil
		}),
		add("pending-rotations", cobra.NoArgs, func([]string) (interface{}, error) { return &types.QueryPendingKeyRotationsRequest{}, nil }),
		add("last-applied", cobra.NoArgs, func([]string) (interface{}, error) { return &types.QueryLastAppliedValidatorsRequest{}, nil }),
		add("reserved [hex-address]", cobra.ExactArgs(1), func(a []string) (interface{}, error) {
			return &types.QueryReservedConsensusAddressRequest{ConsensusAddress: a[0]}, nil
		}),
		add("reward-weight [slot-id]", cobra.ExactArgs(1), func(a []string) (interface{}, error) {
			id, e := parseID(a)
			return &types.QueryRewardWeightRequest{SlotId: id}, e
		}),
		add("selection-policy [slot-id]", cobra.ExactArgs(1), func(a []string) (interface{}, error) {
			id, e := parseID(a)
			return &types.QuerySelectionPolicyRequest{SlotId: id}, e
		}),
		add("selection-policy-version [slot-id] [policy-version]", cobra.ExactArgs(2), func(a []string) (interface{}, error) {
			id, e := parseID(a)
			if e != nil {
				return nil, e
			}
			version, e := strconv.ParseUint(a[1], 10, 64)
			return &types.QuerySelectionPolicyVersionRequest{SlotId: id, PolicyVersion: version}, e
		}),
		add("selection-policy-at-height [slot-id] [height]", cobra.ExactArgs(2), func(a []string) (interface{}, error) {
			id, e := parseID(a)
			if e != nil {
				return nil, e
			}
			height, e := strconv.ParseInt(a[1], 10, 64)
			return &types.QuerySelectionPolicyAtHeightRequest{SlotId: id, AtHeight: height}, e
		}),
	)
	return cmd, specs
}
