package app_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/cosmos/gogoproto/proto"
	descriptorpb "github.com/cosmos/gogoproto/protoc-gen-gogo/descriptor"
	"github.com/grpc-ecosystem/grpc-gateway/runtime"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"

	"github.com/twilight-project/twilight-core/app/openapi"
	miningtypes "github.com/twilight-project/twilight-core/x/mining/types"
)

// The consumer read contract has to exist on every surface it is offered on.
//
// A query registered in one place and missing from another is worse than one that
// was never added: an integrator reads the surface they have and concludes the
// chain cannot answer, or writes against a route that is not served. The two
// queries below are the ones downstream consumers read instead of reimplementing
// consensus rules, so their reachability is part of the contract rather than a
// packaging detail.

const (
	targetEpochMethod = "/twilight.mining.v1.Query/TargetEpochInterpretation"
	economicMethod    = "/twilight.mining.v1.Query/ValidateEconomicAddress"
)

// TestConsumerQueriesAreRoutedByTheApp proves the handlers are reachable through
// the app's own query router, which is what gRPC and the REST gateway both funnel
// into.
func TestConsumerQueriesAreRoutedByTheApp(t *testing.T) {
	a := bootApp(t)

	for _, method := range []string{targetEpochMethod, economicMethod} {
		require.NotNilf(t, a.GRPCQueryRouter().Route(method),
			"query route %s must be registered", method)
	}
}

// TestConsumerQueriesAppearInTheServedOpenAPISpec asserts the REST contract from
// the spec the node actually serves, not from a file beside it.
//
// The address route is a query PARAMETER rather than a path segment, and that is
// load-bearing: a gateway path segment must be non-empty to match its pattern, so
// a path form could not express the empty address — which this contract requires
// to be a successful domain rejection. A path-shaped route here would mean REST
// silently offering fewer answers than gRPC and the CLI.
func TestConsumerQueriesAppearInTheServedOpenAPISpec(t *testing.T) {
	spec, err := openapi.SwaggerSpec()
	require.NoError(t, err)
	served := string(spec)

	require.Contains(t, served, "/twilight/mining/v1/target-epochs/{target_epoch}")
	require.Contains(t, served, "/twilight/mining/v1/economic-address")
	require.NotContains(t, served, "/twilight/mining/v1/economic-address/{",
		"the address is a query parameter; a path segment could not express the empty address")
}

// TestConsumerQueriesAppearInTheExportedDescriptor keeps the offline artifact in
// step with the service.
//
// Indexers and explorers decode against the exported descriptor set rather than
// against this repository, so a method missing from it is a method they cannot
// see however well it is registered here.
func TestConsumerQueriesAppearInTheExportedDescriptor(t *testing.T) {
	raw, err := os.ReadFile("../docs/proto/twilight-descriptors.pb")
	require.NoError(t, err)

	var set descriptorpb.FileDescriptorSet
	require.NoError(t, proto.Unmarshal(raw, &set))

	methods := map[string]bool{}
	for _, file := range set.GetFile() {
		if file.GetPackage() != "twilight.mining.v1" {
			continue
		}
		for _, service := range file.GetService() {
			if service.GetName() != "Query" {
				continue
			}
			for _, method := range service.GetMethod() {
				methods[method.GetName()] = true
			}
		}
	}
	require.NotEmpty(t, methods, "the mining query service must be in the exported descriptor")

	for _, method := range []string{
		"TargetEpochInterpretation", "ValidateEconomicAddress", "SettlementParamsForEpoch",
	} {
		require.Truef(t, methods[method], "%s must appear in the exported descriptor", method)
	}
	require.Len(t, methods, 12, "one descriptor method per query, and no method without one")

	// The offline manifest lists transaction types. This increment adds none, so a
	// new entry there would mean a message had been introduced by accident.
	manifest, err := os.ReadFile("../docs/proto/twilight-msg-type-urls.json")
	require.NoError(t, err)
	require.NotContains(t, string(manifest), "TargetEpoch")
	require.NotContains(t, strings.ToLower(string(manifest)), "economicaddress")
}

// stubQueryClient records the request the gateway built and answers with a canned
// response. The recording is the point: what needs proving is what the gateway
// makes of a URL, not that a handler exists behind it.
type stubQueryClient struct {
	miningtypes.QueryClient

	address string
}

func (s *stubQueryClient) ValidateEconomicAddress(
	_ context.Context, req *miningtypes.QueryValidateEconomicAddressRequest, _ ...grpc.CallOption,
) (*miningtypes.QueryValidateEconomicAddressResponse, error) {
	s.address = req.Address
	return &miningtypes.QueryValidateEconomicAddressResponse{}, nil
}

// TestTheAddressRouteExpressesTheEmptyAddressOverRest exercises the claim the
// query-parameter form was chosen for.
//
// An empty address is a successful domain rejection, so it has to be askable on
// every surface the query is offered on. A path-segment route could not express it
// at all — a gateway path segment must be non-empty to match its pattern — so REST
// would answer strictly fewer cases than gRPC and the CLI. Both REST spellings of
// "no address" are driven here against the real generated gateway.
func TestTheAddressRouteExpressesTheEmptyAddressOverRest(t *testing.T) {
	const route = "/twilight/mining/v1/economic-address"

	for name, target := range map[string]string{
		"the parameter is omitted entirely":  route,
		"the parameter is present and empty": route + "?address=",
		"an ordinary address":                route + "?address=cosmos1qypqxpq9qcrsszg2pvxq6rs0zqg3yyc5lzv7xu",
	} {
		t.Run(name, func(t *testing.T) {
			client := &stubQueryClient{}
			mux := runtime.NewServeMux()
			require.NoError(t, miningtypes.RegisterQueryHandlerClient(context.Background(), mux, client))

			recorder := httptest.NewRecorder()
			mux.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))

			require.Equal(t, http.StatusOK, recorder.Code,
				"every enumerated case must reach the handler over REST")
			expected := ""
			if parsed, err := url.Parse(target); err == nil {
				expected = parsed.Query().Get("address")
			}
			require.Equal(t, expected, client.address,
				"the gateway must hand the handler exactly the address the URL carried")
		})
	}
}
