package cli

import (
	"strconv"

	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	gogoproto "github.com/cosmos/gogoproto/proto"

	"github.com/twilight-project/twilight-core/x/coreslot/types"
)

func GetQueryCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "coreslot-query", Short: "Query core slots", DisableFlagParsing: true, SuggestionsMinimumDistance: 2}
	add := func(use string, args cobra.PositionalArgs, request func([]string) (interface{}, error)) *cobra.Command {
		q := &cobra.Command{Use: use, Args: args, RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx, err := client.GetClientQueryContext(cmd)
			if err != nil {
				return err
			}
			req, err := request(args)
			if err != nil {
				return err
			}
			query := types.NewQueryClient(clientCtx)
			var response interface{}
			switch v := req.(type) {
			case *types.QueryParamsRequest:
				response, err = query.Params(cmd.Context(), v)
			case *types.QueryCoreSlotRequest:
				response, err = query.CoreSlot(cmd.Context(), v)
			case *types.QueryCoreSlotsRequest:
				response, err = query.CoreSlots(cmd.Context(), v)
			case *types.QueryActiveCoreSlotsRequest:
				response, err = query.ActiveCoreSlots(cmd.Context(), v)
			case *types.QueryCoreSlotByOperatorRequest:
				response, err = query.CoreSlotByOperator(cmd.Context(), v)
			case *types.QueryCoreSlotByConsensusAddressRequest:
				response, err = query.CoreSlotByConsensusAddress(cmd.Context(), v)
			case *types.QueryPendingKeyRotationsRequest:
				response, err = query.PendingKeyRotations(cmd.Context(), v)
			case *types.QueryLastAppliedValidatorsRequest:
				response, err = query.LastAppliedValidators(cmd.Context(), v)
			case *types.QueryReservedConsensusAddressRequest:
				response, err = query.ReservedConsensusAddress(cmd.Context(), v)
			case *types.QueryRewardWeightRequest:
				response, err = query.RewardWeight(cmd.Context(), v)
			}
			if err != nil {
				return err
			}
			return clientCtx.PrintProto(response.(gogoproto.Message))
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
	)
	return cmd
}
