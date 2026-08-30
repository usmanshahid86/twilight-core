package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	gogoproto "github.com/cosmos/gogoproto/proto"
	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"

	"github.com/twilight-project/twilight-core/x/mining/types"
)

// The operator-facing view of the settlement observability surface.
//
// Every command here maps one-to-one onto a query, deliberately: this is the
// surface a settlement worker recovers from, and a CLI that summarized or merged
// responses would be a second, divergent account of the same state. It follows the
// repository's existing convention of a top-level `mining-query` command.
func GetQueryCmd() *cobra.Command {
	cmd, _ := buildQueryCmd()
	return cmd
}

// querySpec pairs a registered command with the request it builds; see the
// equivalent in x/rewards for why the pairs are exposed.
type querySpec struct {
	name  string
	build func(*cobra.Command, []string) (interface{}, error)
}

func buildQueryCmd() (*cobra.Command, []querySpec) {
	var specs []querySpec
	cmd := &cobra.Command{
		Use:                        "mining-query",
		Short:                      "Query mining settlement state",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
	}
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
		add("settlement [slot-id] [epoch]", cobra.ExactArgs(2), false, func(_ *cobra.Command, a []string) (interface{}, error) {
			slot, epoch, err := twoIDs(a)
			if err != nil {
				return nil, err
			}
			return &types.QuerySettlementRequest{SlotId: slot, Epoch: epoch}, nil
		}),
		add("open-settlements [slot-id]", cobra.ExactArgs(1), true, func(cmd *cobra.Command, a []string) (interface{}, error) {
			slot, err := positiveID(a[0], "slot-id")
			if err != nil {
				return nil, err
			}
			page, err := client.ReadPageRequest(cmd.Flags())
			if err != nil {
				return nil, err
			}
			return &types.QueryOpenSettlementsRequest{SlotId: slot, Pagination: page}, nil
		}),
		add("settlement-clock", cobra.NoArgs, false, func(*cobra.Command, []string) (interface{}, error) {
			return &types.QuerySettlementClockRequest{}, nil
		}),
		add("distribution-mode-version [version]", cobra.ExactArgs(1), false, func(_ *cobra.Command, a []string) (interface{}, error) {
			version, err := positiveID(a[0], "version")
			if err != nil {
				return nil, err
			}
			return &types.QueryDistributionModeVersionRequest{Version: version}, nil
		}),
		add("distribution-mode-versions", cobra.NoArgs, true, func(cmd *cobra.Command, _ []string) (interface{}, error) {
			page, err := client.ReadPageRequest(cmd.Flags())
			if err != nil {
				return nil, err
			}
			return &types.QueryDistributionModeVersionsRequest{Pagination: page}, nil
		}),
		add("selection-params-version [version]", cobra.ExactArgs(1), false, func(_ *cobra.Command, a []string) (interface{}, error) {
			version, err := positiveID(a[0], "version")
			if err != nil {
				return nil, err
			}
			return &types.QuerySelectionParamsVersionRequest{Version: version}, nil
		}),
		add("selection-params-versions", cobra.NoArgs, true, func(cmd *cobra.Command, _ []string) (interface{}, error) {
			page, err := client.ReadPageRequest(cmd.Flags())
			if err != nil {
				return nil, err
			}
			return &types.QuerySelectionParamsVersionsRequest{Pagination: page}, nil
		}),
		add("settlement-params-version [version]", cobra.ExactArgs(1), false, func(_ *cobra.Command, a []string) (interface{}, error) {
			version, err := positiveID(a[0], "version")
			if err != nil {
				return nil, err
			}
			return &types.QuerySettlementParamsVersionRequest{Version: version}, nil
		}),
		add("settlement-params-versions", cobra.NoArgs, true, func(cmd *cobra.Command, _ []string) (interface{}, error) {
			page, err := client.ReadPageRequest(cmd.Flags())
			if err != nil {
				return nil, err
			}
			return &types.QuerySettlementParamsVersionsRequest{Pagination: page}, nil
		}),
		add("target-epoch-interpretation [target-epoch]", cobra.ExactArgs(1), false, func(_ *cobra.Command, a []string) (interface{}, error) {
			// Zero is refused here by the same convention every other protocol
			// identifier follows, so a typo costs no round trip. The server refuses
			// it too, and on its own authority: this is a convenience ahead of the
			// rule, never a substitute for it.
			target, err := positiveID(a[0], "target-epoch")
			if err != nil {
				return nil, err
			}
			return &types.QueryTargetEpochInterpretationRequest{TargetEpoch: target}, nil
		}),
		add("validate-economic-address [address]", cobra.ExactArgs(1), false, func(_ *cobra.Command, a []string) (interface{}, error) {
			// Deliberately unvalidated. The chain owns the admissibility rule, and
			// the whole purpose of this command is to ask it rather than to hold a
			// second opinion locally — including for the empty address, which is a
			// successful domain rejection the operator is entitled to see. Cobra
			// passes an empty positional argument through, so `validate-economic-address ""`
			// reaches the handler and returns that answer.
			return &types.QueryValidateEconomicAddressRequest{Address: a[0]}, nil
		}),
	)
	return cmd, specs
}

// dispatchQuery routes a typed request to the generated query client.
//
// A type switch rather than reflection, so the mapping is explicit and greppable.
//
// It does NOT give a compile-time guarantee, and the comment here used to claim it
// did. Go type switches are not exhaustive: a query added to the service, or a
// command registered without a case below, compiles perfectly and falls through to
// the error at the bottom — which is exactly how three rewards commands shipped
// having never returned data (#136). What actually holds the mapping together is
// the pinned command/request/RPC contract asserted in the tests, and the default
// branch here, which turns a gap into a named error rather than a nil response.
func dispatchQuery(ctx context.Context, qc types.QueryClient, req interface{}) (gogoproto.Message, error) {
	switch r := req.(type) {
	case *types.QuerySettlementRequest:
		return qc.Settlement(ctx, r)
	case *types.QueryOpenSettlementsRequest:
		return qc.OpenSettlements(ctx, r)
	case *types.QuerySettlementClockRequest:
		return qc.SettlementClock(ctx, r)
	case *types.QueryDistributionModeVersionRequest:
		return qc.DistributionModeVersion(ctx, r)
	case *types.QueryDistributionModeVersionsRequest:
		return qc.DistributionModeVersions(ctx, r)
	case *types.QuerySelectionParamsVersionRequest:
		return qc.SelectionParamsVersion(ctx, r)
	case *types.QuerySelectionParamsVersionsRequest:
		return qc.SelectionParamsVersions(ctx, r)
	case *types.QuerySettlementParamsVersionRequest:
		return qc.SettlementParamsVersion(ctx, r)
	case *types.QuerySettlementParamsVersionsRequest:
		return qc.SettlementParamsVersions(ctx, r)
	case *types.QueryTargetEpochInterpretationRequest:
		return qc.TargetEpochInterpretation(ctx, r)
	case *types.QueryValidateEconomicAddressRequest:
		return qc.ValidateEconomicAddress(ctx, r)
	default:
		return nil, fmt.Errorf("unsupported mining query request %T", req)
	}
}

// positiveID parses an identifier that the protocol numbers from one.
//
// Rejected here rather than left to the server so an obvious typo costs no round
// trip, and so the message names the argument the operator actually typed.
func positiveID(arg, name string) (uint64, error) {
	value, err := strconv.ParseUint(arg, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a positive integer: %w", name, err)
	}
	if value == 0 {
		return 0, fmt.Errorf("%s must be positive", name)
	}
	return value, nil
}

func twoIDs(args []string) (uint64, uint64, error) {
	slot, err := positiveID(args[0], "slot-id")
	if err != nil {
		return 0, 0, err
	}
	epoch, err := positiveID(args[1], "epoch")
	if err != nil {
		return 0, 0, err
	}
	return slot, epoch, nil
}
