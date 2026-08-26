//go:build upgradedrill

package app_test

import (
	"testing"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/stretchr/testify/require"

	"github.com/twilight-project/twilight-core/app"
)

// Under the tag, the drill upgrade must be present, valid, and NOT idempotent.
//
// The non-idempotence is the property the drill's exactly-once proof rests on: if
// a second application were harmless, "the chain still runs after a restart"
// would prove nothing about whether the migration ran twice.
func TestDrillRegistryUnderTag(t *testing.T) {
	var drill *app.Upgrade
	for i := range app.Upgrades {
		if app.Upgrades[i].Name == app.DrillUpgradeName {
			drill = &app.Upgrades[i]
		}
	}
	require.NotNil(t, drill, "the tagged build must carry %q", app.DrillUpgradeName)
	require.NotNil(t, drill.Migrate)
	require.Nil(t, drill.StoreUpgrades, "the drill deliberately exercises no store layout change")
	require.NoError(t, app.ValidateUpgrades(app.Upgrades))

	a := newAppOnDB(t, dbm.NewMemDB())
	initUpgradeGenesis(t, a)
	ctx := a.NewUncachedContext(false, cmtproto.Header{Height: 1})
	keepers := app.MigrationKeepers{
		CoreSlot: a.CoreSlotKeeper, Rewards: a.RewardsKeeper, Mining: a.MiningKeeper,
	}

	before, err := a.CoreSlotKeeper.Params.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(1), before.KeyRotationDelayBlocks, "the drill assumes the default")

	require.NoError(t, drill.Migrate(ctx, keepers), "first application must succeed")
	after, err := a.CoreSlotKeeper.Params.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(2), after.KeyRotationDelayBlocks)

	// The whole point: a second application must fail loudly.
	err = drill.Migrate(ctx, keepers)
	require.Error(t, err, "the migration must refuse to run twice")
	require.Contains(t, err.Error(), "exactly once")
}
