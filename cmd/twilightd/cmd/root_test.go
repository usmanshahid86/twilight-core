package cmd_test

import (
	"testing"

	"github.com/stretchr/testify/require"

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
