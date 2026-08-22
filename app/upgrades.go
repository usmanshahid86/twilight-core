package app

import (
	"context"

	storetypes "cosmossdk.io/store/types"
	upgradekeeper "cosmossdk.io/x/upgrade/keeper"
	upgradetypes "cosmossdk.io/x/upgrade/types"

	"github.com/cosmos/cosmos-sdk/runtime"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"

	coreslotkeeper "github.com/twilight-project/twilight-core/x/coreslot/keeper"
)

// The registry of every named upgrade this binary can execute.
//
// A binary can only perform an upgrade whose name it was compiled with. That is
// the property the whole mechanism rests on: a node that reaches an upgrade
// height carrying a name it does not know halts rather than guessing, so an
// operator running the wrong build stops instead of diverging from the network.
//
// Entries are appended and never edited once released. A released upgrade name
// is a permanent part of the chain's history — a node replaying from genesis has
// to execute the same handler the network executed at that height, so changing
// one silently rewrites history for every node that syncs afterwards.
type Upgrade struct {
	// Name must match the name in the on-chain plan exactly.
	Name string
	// StoreUpgrades declares stores added, renamed or deleted by this upgrade.
	// Nil when the upgrade adds no module. It is applied by a store loader before
	// the application loads its stores, which is why it cannot be decided inside
	// the handler.
	StoreUpgrades *storetypes.StoreUpgrades
	// Migrate carries chain-specific state changes beyond the module-level
	// migrations the module manager performs. Nil when there are none.
	//
	// It runs AFTER module migrations, so it observes state already at the new
	// consensus version rather than a half-migrated mixture.
	Migrate func(ctx sdk.Context, coreSlot coreslotkeeper.Keeper) error
}

// Upgrades is the live registry. It is deliberately empty in the released binary:
// no upgrade has been performed on any long-lived chain yet, and an entry here is
// a commitment that cannot be withdrawn.
//
// The upgrade drill appends a test entry under the `upgradedrill` build tag, so
// the mechanism is exercised end to end without shipping a test upgrade to
// operators.
var Upgrades []Upgrade

// registerUpgradeHandlers binds every declared upgrade to the keeper.
//
// Registration is unconditional and happens on every start, not only when an
// upgrade is pending: the keeper has to be able to answer "do I know this name?"
// at the moment a plan is submitted, so that a plan naming an unknown upgrade is
// refused when it is proposed rather than discovered at the halt height.
func registerUpgradeHandlers(
	runtimeApp *runtime.App,
	upgradeKeeper *upgradekeeper.Keeper,
	coreSlot coreslotkeeper.Keeper,
) {
	for _, upgrade := range Upgrades {
		upgrade := upgrade
		upgradeKeeper.SetUpgradeHandler(
			upgrade.Name,
			func(ctx context.Context, _ upgradetypes.Plan, fromVM module.VersionMap) (module.VersionMap, error) {
				// Module-level migrations first, so any chain-specific step below
				// observes state already at the new consensus version.
				toVM, err := runtimeApp.ModuleManager.RunMigrations(ctx, runtimeApp.Configurator(), fromVM)
				if err != nil {
					return nil, err
				}
				if upgrade.Migrate != nil {
					// Fail closed, on the same terms as BeginBlock and EndBlock: an
					// error here aborts the whole block, so nothing is committed and
					// the node halts. A migration that half-applied against
					// outstanding entitlement liability would not be recoverable by
					// retrying.
					if err := upgrade.Migrate(sdk.UnwrapSDKContext(ctx), coreSlot); err != nil {
						return nil, err
					}
				}
				return toVM, nil
			},
		)
	}
}

// setUpgradeStoreLoader applies any store additions or removals the pending
// upgrade declares, before the application loads its stores.
//
// This runs on EVERY start, because it is the restart after the halt that has to
// mount the new store layout. The upgrade info is written to disk by the halting
// node, so the binary that comes back up reads what the previous one recorded.
//
// It must be called BEFORE runtime.App.Load.
func setUpgradeStoreLoader(runtimeApp *runtime.App, upgradeKeeper *upgradekeeper.Keeper) {
	info, err := upgradeKeeper.ReadUpgradeInfoFromDisk()
	if err != nil {
		// An unreadable upgrade-info file means the node cannot know whether it is
		// resuming into a new store layout. Starting anyway risks loading the old
		// layout and diverging, so refuse to start at all.
		panic(err)
	}
	if info.Name == "" || upgradeKeeper.IsSkipHeight(info.Height) {
		return
	}
	for _, upgrade := range Upgrades {
		if upgrade.Name == info.Name && upgrade.StoreUpgrades != nil {
			runtimeApp.SetStoreLoader(upgradetypes.UpgradeStoreLoader(info.Height, upgrade.StoreUpgrades))
			return
		}
	}
}

// upgradeScheduler adapts x/upgrade's keeper to the narrow interface x/coreslot
// depends on, and is the ONLY place an upgrade Plan is constructed.
//
// That matters because Plan carries fields this chain must never accept. A
// wall-clock upgrade time would make a consensus decision depend on node-local
// time, and an upgraded client state belongs to IBC, which is not wired. Building
// the Plan here, from three primitives, leaves them permanently zero and gives
// them no representation on any path a caller can reach — unrepresentable rather
// than validated away.
type upgradeScheduler struct{ keeper *upgradekeeper.Keeper }

func (s upgradeScheduler) ScheduleUpgrade(ctx context.Context, name string, height int64, info string) error {
	return s.keeper.ScheduleUpgrade(ctx, upgradetypes.Plan{Name: name, Height: height, Info: info})
}

func (s upgradeScheduler) CancelUpgrade(ctx context.Context) error {
	return s.keeper.ClearUpgradePlan(ctx)
}
