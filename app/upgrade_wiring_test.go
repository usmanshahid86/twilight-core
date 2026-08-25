package app_test

import (
	"encoding/json"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/testutil/sims"

	"github.com/twilight-project/twilight-core/app"
)

// The version map x/upgrade persists at genesis must cover EVERY module the app
// mounts — asserted against COMMITTED state, not against the wiring value.
//
// The distinction is the whole point. GetInitVersionMap returns what app wiring
// placed in memory; GetModuleVersionMap returns what genesis actually wrote. They
// diverge whenever x/upgrade's InitGenesis does not run, because the module
// manager skips any module the genesis document has no section for. A test
// asserting the in-memory value passes on a chain whose stored map is empty —
// which is exactly the state the wiring exists to prevent.
//
// A module missing from the STORED map is not a missing record. At the first
// upgrade RunMigrations classifies it as newly added and runs its InitGenesis
// with DefaultGenesis against live state.
func TestCommittedModuleVersionMapCoversEveryMountedModule(t *testing.T) {
	a := newAppOnDB(t, dbm.NewMemDB())
	initUpgradeGenesis(t, a)

	ctx := a.NewUncachedContext(false, cmtproto.Header{Height: 1})
	stored, err := a.UpgradeKeeper.GetModuleVersionMap(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, stored, "genesis must commit a module version map")

	for name, module := range a.ModuleManager.Modules {
		version, ok := stored[name]
		require.True(t, ok,
			"module %q is mounted but absent from the COMMITTED version map; the first upgrade "+
				"would run its InitGenesis over live state instead of migrating it", name)
		if versioned, hasVersion := module.(interface{ ConsensusVersion() uint64 }); hasVersion {
			require.Equal(t, versioned.ConsensusVersion(), version,
				"module %q committed the wrong consensus version", name)
		}
	}

	// Named explicitly: these three are registered after depinject has built its
	// module set, so they are the ones a wiring regression drops.
	for _, name := range []string{"coreslot", "rewards", "mining"} {
		require.Contains(t, stored, name)
	}

	// The stored map must describe the mounted set exactly — no extra entries for
	// things that are not modules. `tx` is a config provider, not a module.
	require.Len(t, stored, len(a.ModuleManager.Modules))
	require.NotContains(t, stored, "tx")
}

// A genesis document with no x/upgrade section must be refused at InitChain.
//
// Without the guard such a document starts, produces blocks, and carries an EMPTY
// version map — every existing check passes, because the module manager skips a
// module whose section is absent and x/upgrade declares no ValidateGenesis at all.
// Nothing looks wrong until the first upgrade, which is the worst possible moment
// to discover it.
func TestGenesisWithoutTheUpgradeSectionIsRefused(t *testing.T) {
	a := newAppOnDB(t, dbm.NewMemDB())

	genesis := upgradeGenesisMap(t, a)
	require.Contains(t, genesis, "upgrade", "a conforming genesis must carry the upgrade section")
	delete(genesis, "upgrade")
	appState, err := json.Marshal(genesis)
	require.NoError(t, err)

	err = initChainErr(a, appState)
	require.Error(t, err, "a genesis with no upgrade section must not start a chain")
	require.Contains(t, err.Error(), "module version")
}

// initChainErr reports InitChain failure as an error whether it is returned or
// raised as a panic.
func initChainErr(a *app.App, appState []byte) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(error); ok {
				err = e
				return
			}
			err = errorf("%v", r)
		}
	}()
	_, err = a.InitChain(&abci.RequestInitChain{
		InitialHeight:   1,
		ConsensusParams: sims.DefaultConsensusParams,
		AppStateBytes:   appState,
	})
	return err
}
