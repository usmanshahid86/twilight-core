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

// requireNoSubcommand proves a retired command is genuinely gone from the public
// surface, rather than merely unused. An operator discovers what a chain supports by
// running --help, so a retired path that still lists is a retired path they will try.
func requireNoSubcommand(t *testing.T, parent *cobra.Command, name string) {
	t.Helper()
	for _, c := range parent.Commands() {
		require.NotEqualf(t, name, c.Name(),
			"%q is retired and must not appear under %q", name, parent.Name())
	}
}

func TestGetQueryCmdRegistersAllQueries(t *testing.T) {
	cmd := cli.GetQueryCmd()
	require.Equal(t, "rewards-query", cmd.Name())
	for _, name := range []string{
		"params", "epoch-info", "next-halving", "epoch-reward",
		"cumulative-emitted", "supply-schedule", "current-active-blocks", "module-balances",
	} {
		require.NotNil(t, subcommand(t, cmd, name))
	}

	// Positional-arg enforcement.
	er := subcommand(t, cmd, "epoch-reward")
	require.Error(t, er.Args(er, []string{}), "epoch-reward requires an epoch arg")
	require.NoError(t, er.Args(er, []string{"1"}))

	// The claim-backed queries are retired. slot-rewards went with them: it paged
	// the claim-record collection directly, so it had no state left to read once
	// that collection was removed.
	requireNoSubcommand(t, cmd, "claimable")
	requireNoSubcommand(t, cmd, "slot-rewards")
}

func TestGetTxCmdRegistersAllTxs(t *testing.T) {
	cmd := cli.GetTxCmd()
	require.Equal(t, "rewards", cmd.Name())
	for _, name := range []string{"update-params", "pause", "resume"} {
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

	// The claim transaction is retired: Settlement is the only way entitlement
	// value leaves escrow, so there is no second payout command to offer.
	requireNoSubcommand(t, cmd, "claim")
}
