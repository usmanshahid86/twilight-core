package cli_test

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/twilight-project/twilight-core/x/rewards/client/cli"
)

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

func TestGetQueryCmdRegistersAllQueries(t *testing.T) {
	cmd := cli.GetQueryCmd()
	require.Equal(t, "rewards-query", cmd.Name())
	for _, name := range []string{
		"params", "epoch-info", "next-halving", "epoch-reward", "slot-rewards",
		"claimable", "cumulative-emitted", "supply-schedule", "current-active-blocks", "module-balances",
	} {
		require.NotNil(t, subcommand(t, cmd, name))
	}

	// Positional-arg enforcement.
	er := subcommand(t, cmd, "epoch-reward")
	require.Error(t, er.Args(er, []string{}), "epoch-reward requires an epoch arg")
	require.NoError(t, er.Args(er, []string{"1"}))

	claimable := subcommand(t, cmd, "claimable")
	require.Error(t, claimable.Args(claimable, []string{"1"}))
	require.NoError(t, claimable.Args(claimable, []string{"1", "2", "3"}))
}

func TestGetTxCmdRegistersAllTxs(t *testing.T) {
	cmd := cli.GetTxCmd()
	require.Equal(t, "rewards", cmd.Name())
	for _, name := range []string{"update-params", "pause", "resume", "claim"} {
		require.NotNil(t, subcommand(t, cmd, name))
	}

	// Pause/resume are GLOBAL and take no area selectors: offering them would
	// advertise a partial pause the protocol no longer has.
	for _, name := range []string{"pause", "resume"} {
		c := subcommand(t, cmd, name)
		require.NoError(t, c.Args(c, []string{}))
		for _, retired := range []string{"emissions", "settlement", "claims"} {
			require.Nilf(t, c.Flags().Lookup(retired),
				"%s must not expose the retired --%s selector", name, retired)
		}
	}

	// update-params takes a single JSON file arg.
	up := subcommand(t, cmd, "update-params")
	require.Error(t, up.Args(up, []string{}))
	require.NoError(t, up.Args(up, []string{"./params.json"}))

	// claim takes slot-id + epoch range.
	claim := subcommand(t, cmd, "claim")
	require.Error(t, claim.Args(claim, []string{"1"}))
	require.NoError(t, claim.Args(claim, []string{"1", "2", "3"}))
}
