package app_test

import (
	"testing"

	dbm "github.com/cosmos/cosmos-db"
	"github.com/stretchr/testify/require"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"

	"github.com/twilight-project/twilight-core/app"
	coreslot "github.com/twilight-project/twilight-core/x/coreslot"
	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
)

// v0.1.0 shipped CoreSlot at consensus version 1. Two-step authority rotation
// takes it to 2, and this proves the boundary is real: a chain carrying the
// released version map advances to 2 through RunMigrations, without inventing
// state the released chain never had.
//
// The version map is not bookkeeping. It is what a future upgrade consults, per
// module, to decide whether to migrate or to treat the module as newly added and
// run its InitGenesis over live state. Leaving both implementations at version 1
// would make a v0.1.0 chain and a post-rotation chain indistinguishable to that
// decision.
func TestCoreSlotMigratesFromTheReleasedVersion(t *testing.T) {
	a := newAppWithHome(t, dbm.NewMemDB(), t.TempDir())
	initUpgradeGenesis(t, a)

	require.EqualValues(t, 2, coreslot.ConsensusVersion,
		"the released baseline was 1; this test is about the step to 2")

	ctx := a.NewUncachedContext(false, cmtproto.Header{Height: 2})

	// State as it stands before any migration, so the no-op claim is checked
	// rather than asserted.
	beforeParams, err := a.CoreSlotKeeper.Params.Get(ctx)
	require.NoError(t, err)
	beforeGenesis, err := a.CoreSlotKeeper.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Empty(t, beforeGenesis.PendingAuthorityTransfers)

	// A released-style version map: everything at its current version except
	// CoreSlot, which is pinned back to what v0.1.0 committed.
	fromVM := a.ModuleManager.GetVersionMap()
	require.EqualValues(t, 2, fromVM[coreslottypes.ModuleName])
	fromVM[coreslottypes.ModuleName] = 1

	toVM, err := a.ModuleManager.RunMigrations(ctx, a.Configurator(), fromVM)
	require.NoError(t, err, "the 1->2 migration must be registered and runnable")
	require.EqualValues(t, 2, toVM[coreslottypes.ModuleName],
		"RunMigrations must advance CoreSlot to the current consensus version")

	// Every other module is carried through untouched.
	for name, version := range fromVM {
		if name == coreslottypes.ModuleName {
			continue
		}
		require.Equal(t, version, toVM[name], "module %q version must not move", name)
	}

	// The migration is a STATE no-op, and this is the half that matters: a
	// migration that fabricated an empty nomination, or rewrote params, would
	// change a released chain's state on upgrade for no reason.
	afterParams, err := a.CoreSlotKeeper.Params.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, beforeParams, afterParams, "params must be untouched by the migration")

	afterGenesis, err := a.CoreSlotKeeper.ExportGenesis(ctx)
	require.NoError(t, err)
	require.Empty(t, afterGenesis.PendingAuthorityTransfers,
		"the migration must not synthesize a pending nomination")
	require.Equal(t, beforeGenesis.Slots, afterGenesis.Slots, "slots must be untouched")
	require.Equal(t, beforeGenesis.NextSlotId, afterGenesis.NextSlotId)
	require.Equal(t, beforeGenesis.SelectionPolicies, afterGenesis.SelectionPolicies)
}

// The production registry must carry the entry that lets a v0.1.0 chain reach
// this version at all. Without it the module version bump is unreachable on a
// live network: nothing would advance the stored map.
func TestProductionRegistryCarriesTheFirstPostReleaseUpgrade(t *testing.T) {
	require.NoError(t, app.ValidateUpgrades(app.Upgrades))

	var found *app.Upgrade
	for i := range app.Upgrades {
		if app.Upgrades[i].Name == "v0.2.0" {
			found = &app.Upgrades[i]
			break
		}
	}
	require.NotNil(t, found, "the first state-machine change after v0.1.0 needs a named upgrade")

	// No store is added — the pending-authority collection is a new PREFIX inside
	// the existing CoreSlot store, so nothing has to be mounted before load.
	require.Nil(t, found.StoreUpgrades,
		"a new key prefix inside an existing store needs no store upgrade")

	// No chain-specific migration body: the module-level 1->2 step is a no-op and
	// there is nothing further to transform. A body here would be fabricating
	// state.
	require.Nil(t, found.Migrate,
		"there is no chain-specific state to transform for this upgrade")
}
