package app_test

import (
	"os"
	"strings"
	"testing"

	"github.com/cosmos/gogoproto/proto"
	descriptorpb "github.com/cosmos/gogoproto/protoc-gen-gogo/descriptor"
	"github.com/stretchr/testify/require"

	"github.com/twilight-project/twilight-core/app/openapi"
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

	for _, method := range []string{"TargetEpochInterpretation", "ValidateEconomicAddress"} {
		require.Truef(t, methods[method], "%s must appear in the exported descriptor", method)
	}
	require.Len(t, methods, 11, "one descriptor method per query, and no method without one")

	// The offline manifest lists transaction types. This increment adds none, so a
	// new entry there would mean a message had been introduced by accident.
	manifest, err := os.ReadFile("../docs/proto/twilight-msg-type-urls.json")
	require.NoError(t, err)
	require.NotContains(t, string(manifest), "TargetEpoch")
	require.NotContains(t, strings.ToLower(string(manifest)), "economicaddress")
}
