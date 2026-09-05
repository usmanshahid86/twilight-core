package cli_test

import (
	"context"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtbytes "github.com/cometbft/cometbft/libs/bytes"
	rpcclient "github.com/cometbft/cometbft/rpc/client"
	rpcclientmock "github.com/cometbft/cometbft/rpc/client/mock"
	coretypes "github.com/cometbft/cometbft/rpc/core/types"
	gogoproto "github.com/cosmos/gogoproto/proto"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	clitestutil "github.com/cosmos/cosmos-sdk/testutil/cli"

	"github.com/twilight-project/twilight-core/x/mining/client/cli"
	"github.com/twilight-project/twilight-core/x/mining/types"
)

// The operator's half of the read contract.
//
// Registration alone would not be enough here. A command that is present but
// builds the wrong request is a surface that disagrees with gRPC and REST while
// looking complete, so the tests below assert the request each command actually
// puts on the wire — most importantly the empty address, which is a successful
// domain answer the operator must be able to ask for.

func subcommand(t *testing.T, parent *cobra.Command, name string) *cobra.Command {
	t.Helper()
	for _, c := range parent.Commands() {
		if c.Name() == name {
			return c
		}
	}
	t.Fatalf("subcommand %q not found under %q", name, parent.Name())
	return nil
}

// capturingRPC records the ABCI query a command sends and answers with a canned
// response. The recording is the point: a canned answer proves only that the
// command ran, and what needs proving is what it asked.
type capturingRPC struct {
	rpcclientmock.Client

	path     string
	data     []byte
	response abci.ResponseQuery
}

func (c *capturingRPC) ABCIQueryWithOptions(
	_ context.Context, path string, data cmtbytes.HexBytes, _ rpcclient.ABCIQueryOptions,
) (*coretypes.ResultABCIQuery, error) {
	c.path = path
	c.data = data
	return &coretypes.ResultABCIQuery{Response: c.response}, nil
}

// runQuery executes one mining-query subcommand against a recording transport and
// returns what it sent.
func runQuery(t *testing.T, response gogoproto.Message, args ...string) *capturingRPC {
	t.Helper()
	registry := codectypes.NewInterfaceRegistry()
	types.RegisterInterfaces(registry)
	cdc := codec.NewProtoCodec(registry)

	value, err := cdc.Marshal(response)
	require.NoError(t, err)

	rpc := &capturingRPC{response: abci.ResponseQuery{Code: 0, Value: value}}
	clientCtx := client.Context{}.
		WithCodec(cdc).
		WithInterfaceRegistry(registry).
		WithClient(rpc)

	_, err = clitestutil.ExecTestCLICmd(clientCtx, cli.GetQueryCmd(), args)
	require.NoError(t, err)
	return rpc
}

// TestGetQueryCmdRegistersEveryQuery keeps the command set and the service in step.
//
// The surface is one command per query by design: a settlement worker recovers
// from these answers, and a CLI offering fewer of them would leave an operator
// unable to see state the chain will act on.
func TestGetQueryCmdRegistersEveryQuery(t *testing.T) {
	cmd := cli.GetQueryCmd()
	require.Equal(t, "mining-query", cmd.Name())

	for _, name := range []string{
		"settlement", "open-settlements", "settlement-clock",
		"distribution-mode-version", "distribution-mode-versions",
		"selection-params-version", "selection-params-versions",
		"settlement-params-version", "settlement-params-versions",
		"target-epoch-interpretation", "validate-economic-address",
		"settlement-params-for-epoch",
	} {
		require.NotNil(t, subcommand(t, cmd, name))
	}
	require.Len(t, cmd.Commands(), 12, "one command per query, and no command without one")
}

// TestTargetEpochInterpretationSendsTheTargetItWasGiven covers the argument and
// the two shapes refused before any round trip.
//
// Zero is refused locally by the same convention every protocol identifier
// follows. That is a convenience ahead of the rule and not a substitute for it:
// the server refuses zero on its own authority, which is asserted where the
// handler is.
func TestTargetEpochInterpretationSendsTheTargetItWasGiven(t *testing.T) {
	rpc := runQuery(t, &types.QueryTargetEpochInterpretationResponse{TargetEpoch: 7},
		"target-epoch-interpretation", "7")
	require.Equal(t, "/twilight.mining.v1.Query/TargetEpochInterpretation", rpc.path)

	var sent types.QueryTargetEpochInterpretationRequest
	require.NoError(t, sent.Unmarshal(rpc.data))
	require.Equal(t, uint64(7), sent.TargetEpoch)

	cmd := cli.GetQueryCmd()
	target := subcommand(t, cmd, "target-epoch-interpretation")
	require.NoError(t, target.Args(target, []string{"1"}))
	require.Error(t, target.Args(target, []string{}), "the target is required")
	require.Error(t, target.Args(target, []string{"1", "2"}))
}

// TestValidateEconomicAddressSendsTheAddressUnaltered is the surface-consistency
// requirement, and the empty case is the whole reason it needs stating.
//
// An empty address is a successful domain rejection, not a malformed request, so
// it has to be askable everywhere the query is offered. Cobra passes an empty
// positional argument through, and the command must forward it rather than
// refusing it locally — a client-side check would make this surface answer
// differently from gRPC and REST for the one case a consumer is most likely to
// hit.
func TestValidateEconomicAddressSendsTheAddressUnaltered(t *testing.T) {
	for name, address := range map[string]string{
		"an ordinary address": "cosmos1qypqxpq9qcrsszg2pvxq6rs0zqg3yyc5lzv7xu",
		"the empty address":   "",
		"an unparseable one":  "not-an-address",
	} {
		t.Run(name, func(t *testing.T) {
			rpc := runQuery(t, &types.QueryValidateEconomicAddressResponse{},
				"validate-economic-address", address)
			require.Equal(t, "/twilight.mining.v1.Query/ValidateEconomicAddress", rpc.path)

			var sent types.QueryValidateEconomicAddressRequest
			require.NoError(t, sent.Unmarshal(rpc.data))
			require.Equal(t, address, sent.Address,
				"the chain owns the rule; the command must not edit the question")
		})
	}

	cmd := cli.GetQueryCmd()
	address := subcommand(t, cmd, "validate-economic-address")
	require.NoError(t, address.Args(address, []string{""}),
		"an empty positional argument is a question, not a missing argument")
	require.Error(t, address.Args(address, []string{}))
}
