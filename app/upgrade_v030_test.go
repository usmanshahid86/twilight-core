package app_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/types/module"

	"github.com/twilight-project/twilight-core/app"
)

// The v0.3.0 entry carries no StoreUpgrades and no Migrate, which is only
// correct while this release moves no module version and mounts no new module.
//
// The WHOLE map is pinned, not just the three custom modules. A narrower check
// would miss the case the empty entry most depends on: a module added before the
// tag needs StoreUpgrades, and an SDK-side consensus-version bump needs a
// migration. dependabot ignores cosmos-sdk and cometbft outright but permits
// cosmossdk.io/* patch bumps, so the SDK half is not hypothetical.
//
// Pinned rather than derived, following the same reasoning as
// scripts/localnet/release-upgrade-rehearsal.sh: a module silently dropped, an
// unexpected one appearing, or a migration that failed to bump a version all
// have to fail here. Deriving these numbers from the constants they guard would
// make the assertion vacuous.
//
// If this fails, decide whether v0.3.0 needs a Migrate or StoreUpgrades BEFORE
// the tag is cut — after release the entry may never be edited.
func TestThisReleaseMovesNoModuleVersionAndMountsNoNewModule(t *testing.T) {
	a := newAppOnDB(t, memDB())
	require.Equal(t, module.VersionMap{
		"auth":      5,
		"bank":      4,
		"consensus": 1,
		"coreslot":  2,
		"mining":    1,
		"rewards":   1,
		"runtime":   0,
		"upgrade":   2,
	}, a.ModuleManager.GetVersionMap(),
		"the module set or a consensus version changed; the v0.3.0 entry carries no "+
			"Migrate and no StoreUpgrades, so this must be reconciled before the tag")
}

// The entry must be REGISTERED ON THE BUILT APPLICATION, not merely present in a
// Go slice.
//
// Asking the keeper is the whole point. Registration walks the registry in
// registerUpgradeHandlers, and a plausible refactor there — say skipping entries
// whose Migrate is nil, since both shipped entries have none — silently strips
// every handler from the binary while leaving the slice untouched. Every node
// would then halt at the upgrade height and no build could resume it, which is
// the exact failure this registry exists to prevent. A slice-only assertion
// cannot see that.
//
// v0.2.0 is asserted alongside v0.3.0 deliberately: the mutation above removes
// both, and a check that only knew about the new entry would let a released
// boundary disappear.
func TestBothUpgradeBoundariesAreRegisteredOnTheBuiltApp(t *testing.T) {
	a := newAppOnDB(t, memDB())
	require.True(t, a.UpgradeKeeper.HasHandler("v0.2.0"),
		"the released v0.2.0 boundary must remain executable by this binary")
	require.True(t, a.UpgradeKeeper.HasHandler("v0.3.0"),
		"a node reaching the v0.3.0 height without a registered handler halts and "+
			"cannot resume; being listed in the registry slice is not enough")
}

// And the entry's shape: coordination only, no state movement.
func TestTheV030EntryCarriesNothing(t *testing.T) {
	var found *app.Upgrade
	for i := range app.Upgrades {
		if app.Upgrades[i].Name == "v0.3.0" {
			found = &app.Upgrades[i]
		}
	}
	require.NotNil(t, found, "the boundary must exist in the registry")
	require.Nil(t, found.StoreUpgrades, "no store is added, renamed or deleted")
	require.Nil(t, found.Migrate, "there is no chain-specific state to transform")

	require.NoError(t, app.ValidateUpgrades(app.Upgrades),
		"the registry as a whole must remain unambiguously executable")
}
