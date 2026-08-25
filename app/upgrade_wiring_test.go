package app_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The version map x/upgrade persists at genesis must cover EVERY module the app
// mounts, not only the ones depinject wired.
//
// x/upgrade's depinject invoker builds that map from the depinject module set
// alone. This application registers coreslot, rewards and mining afterwards, via
// runtimeApp.RegisterModules, so they are invisible to it — and the omission is
// silent, because an incomplete map is a perfectly valid map.
//
// The damage lands at the FIRST upgrade rather than at genesis. RunMigrations
// reads the stored map, finds a mounted module absent, classifies it as newly
// added, and runs its InitGenesis with DefaultGenesis against live state: a
// default validator set over the real one, and a default rewards genesis over
// outstanding entitlement liability. Nothing before that moment looks wrong.
func TestUpgradeInitVersionMapCoversEveryMountedModule(t *testing.T) {
	a := bootApp(t)

	initVM := a.UpgradeKeeper.GetInitVersionMap()
	require.NotEmpty(t, initVM, "the init version map must be set during app wiring")

	for name := range a.ModuleManager.Modules {
		require.Contains(t, initVM, name,
			"module %q is mounted but missing from the version map x/upgrade persists at genesis; "+
				"the first upgrade would run its InitGenesis over live state instead of migrating it", name)
	}

	// And specifically the three that depinject cannot see, named so a regression
	// reports which one came loose rather than only that something did.
	for _, name := range []string{"coreslot", "rewards", "mining"} {
		require.Contains(t, initVM, name)
	}
}
