package cmd_test

import (
	"encoding/json"
	"sort"
	"testing"

	dbm "github.com/cosmos/cosmos-db"
	"github.com/stretchr/testify/require"

	"cosmossdk.io/log"

	"github.com/cosmos/cosmos-sdk/testutil/sims"

	"github.com/twilight-project/twilight-core/app"
	"github.com/twilight-project/twilight-core/cmd/twilightd/cmd"
)

// TestNewRootCmdBuilds is a build-time guard on the assembled CLI.
//
// AutoCLI derives commands from the registered query/tx services, and it panics
// at construction if a generated flag collides with one of the SDK's universal
// flags. A request field named `height`, for example, collides with the standard
// `--height` query flag — the binary then fails to start at all, while every
// keeper and query unit test still passes, because none of them build the root
// command.
//
// This test exists so that failure mode surfaces in `go test` rather than in a
// localnet run. It deliberately asserts nothing about the command tree's shape:
// its whole value is that construction does not panic.
func TestNewRootCmdBuilds(t *testing.T) {
	require.NotPanics(t, func() {
		root := cmd.NewRootCmd()
		require.NotNil(t, root)
		require.NotEmpty(t, root.Commands(), "the root command must register subcommands")
	})
}

// TestInitGenesisCoversEveryModuleTheAppMounts is the regression for a failure
// mode that is silent everywhere it could have been noticed.
//
// `twilightd init` writes a genesis document from the CLI's basic module manager,
// which is a hand-maintained list. The node, separately, mounts a store for every
// module it registers. When a module is present in the second list and missing
// from the first, nothing complains:
//
//   - `init` writes a genesis with no section for that module;
//   - the SDK's module manager SKIPS InitGenesis for a module whose genesis data
//     is absent, rather than failing;
//   - the module's store is mounted but never written, so its IAVL tree ends up
//     carrying no versions at all;
//   - the chain still starts, still produces blocks, and still commits.
//
// The only symptom is that EVERY historical state read fails, for EVERY module,
// at EVERY height — because loading a version requires every mounted store to have
// that version. Unit tests pass, the app-level tests pass, the node looks healthy,
// `catching_up` is false, and the block height advances. The chain is simply
// unable to answer a single query.
//
// Comparing the two genesis documents is the cheapest way to make that
// correspondence explicit. A module added to the app but not to the CLI fails here
// instead of at the first query of a localnet.
func TestInitGenesisCoversEveryModuleTheAppMounts(t *testing.T) {
	a := app.New(log.NewNopLogger(), dbm.NewMemDB(), nil, true, sims.EmptyAppOptions{})
	cdc := app.MakeEncodingConfig().Codec

	appGenesis := a.DefaultGenesis()
	cliGenesis := cmd.BasicManager().DefaultGenesis(cdc)

	// Two modules legitimately carry no `app_state` section and are excluded by
	// name rather than by a rule, so that adding a third requires justifying it
	// here.
	//
	//   runtime   — the app wiring itself; it mounts no module store.
	//   consensus — its parameters travel in the genesis document's own
	//               consensus_params field and are applied by baseapp at
	//               InitChain, not by module InitGenesis.
	//
	// Everything else owns a mounted KV store that only its InitGenesis writes,
	// and must therefore appear in both documents.
	exempt := map[string]string{
		"runtime":   "app wiring, mounts no module store",
		"consensus": "parameters travel in genesis consensus_params, applied by baseapp",
	}

	var required []string
	for _, name := range sortedKeys(appGenesis) {
		if _, ok := exempt[name]; ok {
			continue
		}
		required = append(required, name)
	}

	require.Equal(t, required, sortedKeys(cliGenesis),
		"every module the node initializes must have a genesis section written by `twilightd init`; "+
			"a module missing from the CLI basic manager mounts a store that is never written, "+
			"which makes every query at every height fail for every module")
}

func sortedKeys(genesis map[string]json.RawMessage) []string {
	names := make([]string, 0, len(genesis))
	for name := range genesis {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
