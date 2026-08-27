//go:build !upgradedrill

package app_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/twilight-project/twilight-core/app"
)

// The shipped binary must not contain the drill upgrade.
//
// A production binary carrying a handler for an upgrade the network might later
// schedule is not a cosmetic problem: upstream aborts every block between the
// scheduling transaction and that upgrade's height on any node whose binary
// already holds the handler. A stray drill entry would therefore be a latent
// network halt, armed the moment anyone scheduled that name.
//
// Guarded by the inverse build tag so it runs in ordinary `go test ./...`.
func TestProductionRegistryExcludesTheDrillUpgrade(t *testing.T) {
	for _, upgrade := range app.Upgrades {
		require.NotEqual(t, "drill-v2", upgrade.Name,
			"the drill upgrade must exist only under the `upgradedrill` build tag")
	}
	require.NoError(t, app.ValidateUpgrades(app.Upgrades))
}
