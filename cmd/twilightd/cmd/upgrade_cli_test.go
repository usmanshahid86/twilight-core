package cmd_test

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/twilight-project/twilight-core/cmd/twilightd/cmd"
)

// find walks a path of subcommand names and returns the command, or nil.
func find(root *cobra.Command, path ...string) *cobra.Command {
	current := root
	for _, name := range path {
		var next *cobra.Command
		for _, candidate := range current.Commands() {
			if candidate.Name() == name {
				next = candidate
				break
			}
		}
		if next == nil {
			return nil
		}
		current = next
	}
	return current
}

// The upgrade surface is asserted against the FINAL assembled command tree.
//
// AutoCLI composes from two independent sources: generated commands, enumerated
// from ModuleOptions, and CUSTOM commands, collected by calling GetTxCmd on every
// module in the Modules set that implements it. Suppressing one does nothing to
// the other — a previous fix set the generated tx options to nil and left
// x/upgrade's custom `software-upgrade` and `cancel-software-upgrade` in place,
// and that went unnoticed because nothing inspected the built tree.
//
// So this asserts the tree, not the options that produce it.
func TestUpgradeCommandSurface(t *testing.T) {
	root := cmd.NewRootCmd()
	require.NotNil(t, root)

	t.Run("x/upgrade governance tx commands are absent", func(t *testing.T) {
		// Both build a governance MsgSubmitProposal. This chain wires no governance
		// module, so they can never be routed — an operator who finds them instead
		// of `tx coreslot schedule-upgrade` gets an unexplained broadcast failure.
		require.Nil(t, find(root, "tx", "upgrade", "software-upgrade"))
		require.Nil(t, find(root, "tx", "upgrade", "cancel-software-upgrade"))
		require.Nil(t, find(root, "tx", "upgrade", "cancel-upgrade-proposal"))

		// If a `tx upgrade` parent survives for framework reasons it must at least
		// carry nothing executable.
		if parent := find(root, "tx", "upgrade"); parent != nil {
			require.Empty(t, parent.Commands(),
				"a surviving `tx upgrade` group must expose no runnable subcommands")
		}
	})

	t.Run("the canonical scheduling surface exists", func(t *testing.T) {
		require.NotNil(t, find(root, "tx", "coreslot", "schedule-upgrade"))
		require.NotNil(t, find(root, "tx", "coreslot", "cancel-upgrade"))
	})

	t.Run("upgrade queries are retained", func(t *testing.T) {
		// `query upgrade plan` is how an operator sees a pending halt, which matters
		// more here than on a normal chain because scheduling is routed elsewhere.
		require.NotNil(t, find(root, "query", "upgrade", "plan"))
		require.NotNil(t, find(root, "query", "upgrade", "applied"))
		require.NotNil(t, find(root, "query", "upgrade", "module-versions"))
		require.NotNil(t, find(root, "query", "upgrade", "authority"))
	})
}
