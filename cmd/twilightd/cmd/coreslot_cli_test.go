package cmd_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/twilight-project/twilight-core/cmd/twilightd/cmd"
)

// coreslotTxCommands is every CoreSlot operation an operator can perform, as the
// hand-written tree names them. All 13 must be reachable at `tx coreslot`.
var coreslotTxCommands = []string{
	"activate",
	"cancel-upgrade",
	"inactivate",
	"register",
	"remove",
	"rotate-key",
	"schedule-upgrade",
	"suspend",
	"update-metadata",
	"update-params",
	"update-payout",
	"update-selection-policy",
	"update-settlement",
}

// generatedOnlyTxCommands is the nine names AutoCLI derived from the Msg service
// that have no counterpart in the hand-written tree.
//
// It is NOT the full generated set. Four names — schedule-upgrade,
// cancel-upgrade, update-params and update-selection-policy — are spelled
// identically in both trees, so asserting "no generated name survives" would
// demand the removal of commands that must exist. 9 generated-only + 4 shared =
// the 13 AutoCLI produced; 9 custom-only + the same 4 = the 13 above.
var generatedOnlyTxCommands = []string{
	"activate-core-slot",
	"inactivate-core-slot",
	"register-core-slot",
	"remove-core-slot",
	"rotate-consensus-key",
	"suspend-core-slot",
	"update-operator-metadata",
	"update-payout-address",
	"update-settlement-address",
}

// The CoreSlot transaction surface is asserted against the FINAL assembled tree,
// for the reason set out in upgrade_cli_test.go: AutoCLI composes from two
// independent sources, and inspecting the options that feed it proves nothing
// about what an operator ends up with.
//
// What shipped before this test existed: `tx coreslot` was the AutoCLI-generated
// tree, and every command in it taking a consensus key failed during flag
// parsing, because AutoCLI resolves an Any's `@type` against a registry that
// does not contain cosmos.crypto.ed25519.PubKey. A working hand-written tree
// existed the whole time at root level, so both trees were present and the
// broken one occupied the path SDK convention sends operators to. Nothing
// asserted the command surface, so it went unnoticed until a live devnet.
func TestCoreSlotCommandSurface(t *testing.T) {
	root := cmd.NewRootCmd()
	require.NotNil(t, root)

	t.Run("every CoreSlot operation is reachable at tx coreslot", func(t *testing.T) {
		for _, name := range coreslotTxCommands {
			require.NotNil(t, find(root, "tx", "coreslot", name),
				"`tx coreslot %s` must exist: it is the canonical surface", name)
		}
	})

	t.Run("the generated-only names are absent", func(t *testing.T) {
		for _, name := range generatedOnlyTxCommands {
			require.Nil(t, find(root, "tx", "coreslot", name),
				"`tx coreslot %s` is an AutoCLI-generated name whose consensus-key "+
					"flag cannot be parsed; it must not be reachable", name)
		}
	})

	t.Run("the shared names survive under the hand-written tree", func(t *testing.T) {
		// These four are spelled the same in both trees. They must not be lost as
		// collateral when the generated tree goes: upgrade_cli_test.go already
		// requires the first two as the canonical scheduling surface.
		for _, name := range []string{
			"schedule-upgrade", "cancel-upgrade", "update-params", "update-selection-policy",
		} {
			require.NotNil(t, find(root, "tx", "coreslot", name),
				"`tx coreslot %s` is shared by both trees and must survive", name)
		}
	})

	t.Run("the counts stay consistent", func(t *testing.T) {
		// Pinned so the 9/4/13 split cannot drift silently. A new Msg needs a new
		// hand-written command, and this fails until it has one.
		require.Len(t, coreslotTxCommands, 13)
		require.Len(t, generatedOnlyTxCommands, 9)

		parent := find(root, "tx", "coreslot")
		require.NotNil(t, parent)
		require.Len(t, parent.Commands(), len(coreslotTxCommands),
			"`tx coreslot` must expose exactly the hand-written operations")
	})

	t.Run("the root-level compatibility surface remains", func(t *testing.T) {
		// scripts/localnet/*.sh drive admission through `coreslot register` at the
		// root. Moving the canonical surface must not move this one.
		require.NotNil(t, find(root, "coreslot", "register"))
		require.NotNil(t, find(root, "coreslot", "rotate-key"))
	})

	t.Run("unrelated module trees are untouched", func(t *testing.T) {
		// Sentinels, not snapshots: asserting the full shape of upstream SDK trees
		// would fail on every dependency bump while proving nothing about this
		// change. One representative command per module is enough to catch a
		// wiring mistake that dropped a tree wholesale.
		require.NotNil(t, find(root, "tx", "bank", "send"))
		require.NotNil(t, find(root, "tx", "auth"))
		require.NotNil(t, find(root, "tx", "consensus"))
		require.NotNil(t, find(root, "tx", "rewards"))
		require.NotNil(t, find(root, "query", "mining"))

		// Queries carry no Any and were never broken; the module supplies no custom
		// query command, so the generated CoreSlot queries must still be generated.
		require.NotNil(t, find(root, "query", "coreslot"))
	})
}
