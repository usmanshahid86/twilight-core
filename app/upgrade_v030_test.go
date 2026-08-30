package app_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/twilight-project/twilight-core/app"
	coreslot "github.com/twilight-project/twilight-core/x/coreslot"
	mining "github.com/twilight-project/twilight-core/x/mining"
	rewards "github.com/twilight-project/twilight-core/x/rewards"
)

// The v0.3.0 entry carries no StoreUpgrades and no Migrate, and that is only
// correct while this release moves no module consensus version.
//
// Pinned against the modules' own declared constants rather than a copy of the
// registry, so this is a statement about the CHAIN rather than about the entry
// agreeing with itself. A change that bumps a module version before v0.3.0 ships
// fails here, which is the moment to decide whether the entry needs a migration
// — not after the tag, when the entry may no longer be edited.
//
// The numbers are deliberately literal. Deriving them from the same constants
// they are meant to guard would make the assertion vacuous.
func TestThisReleaseMovesNoModuleConsensusVersion(t *testing.T) {
	for _, tc := range []struct {
		module  string
		version uint64
		actual  uint64
	}{
		{"coreslot", 2, coreslot.ConsensusVersion},
		{"rewards", 1, rewards.ConsensusVersion},
		{"mining", 1, mining.ConsensusVersion},
	} {
		require.Equal(t, tc.version, tc.actual,
			"%s consensus version moved; the v0.3.0 entry carries no Migrate and no "+
				"StoreUpgrades, so a version bump means that entry must be revisited "+
				"before the tag is cut", tc.module)
	}
}

// The boundary must exist and must be the empty, coordination-only shape the
// merged controls need — they change application wiring, not module state.
func TestTheV030BoundaryIsRegisteredAndCarriesNothing(t *testing.T) {
	var found *app.Upgrade
	for i := range app.Upgrades {
		if app.Upgrades[i].Name == "v0.3.0" {
			found = &app.Upgrades[i]
		}
	}
	require.NotNil(t, found,
		"the merged TW-006 controls change transaction validity and cannot be "+
			"scheduled without a named boundary")
	require.Nil(t, found.StoreUpgrades, "no store is added, renamed or deleted")
	require.Nil(t, found.Migrate, "there is no chain-specific state to transform")

	// And the registry as a whole must still be executable unambiguously.
	require.NoError(t, app.ValidateUpgrades(app.Upgrades))
}
