package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	storetypes "cosmossdk.io/store/types"
	upgradekeeper "cosmossdk.io/x/upgrade/keeper"
	upgradetypes "cosmossdk.io/x/upgrade/types"

	"github.com/cosmos/cosmos-sdk/runtime"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"

	coreslotkeeper "github.com/twilight-project/twilight-core/x/coreslot/keeper"
	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	miningkeeper "github.com/twilight-project/twilight-core/x/mining/keeper"
	rewardskeeper "github.com/twilight-project/twilight-core/x/rewards/keeper"
)

// The registry of every named upgrade this binary can execute.
//
// A binary can only perform an upgrade whose name it was compiled with. That is
// the property the whole mechanism rests on: a node that reaches an upgrade
// height carrying a name it does not know halts rather than guessing, so an
// operator running the wrong build stops instead of diverging from the network.
//
// Entries are appended and never edited once released.
//
// The reason is NOT that a later binary replays every historical upgrade from
// genesis — it cannot. Upstream aborts any block where a pending plan's handler
// is already registered and its height has not arrived, so a binary carrying a
// historical handler halts when it replays the block that scheduled it. Replay
// across an upgrade boundary uses the historical binary sequence, or a snapshot
// taken after the boundary.
//
// The reason is that a released entry is an executed artifact. Editing what runs
// under a name the network already applied changes what a node constructed from
// this source would do at that height, which is a silent rewrite of history for
// anyone rebuilding or auditing it. Retention is also what upstream's own
// completed-upgrade and downgrade checks consult — though those need only the
// most recently completed upgrade, so keeping all of them is a deliberately
// stronger local policy than upstream requires, not a requirement inherited from
// it.
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
	Migrate func(ctx sdk.Context, k MigrationKeepers) error
}

// MigrationKeepers is everything a migration may reach.
//
// All three are passed even though no upgrade exists yet, deliberately. Entries
// in the registry are append-only and may never be edited once released, so a
// later signature change to reach rewards or mining state would have to rewrite
// handlers that shipped — which is exactly what that rule forbids. The cost of
// widening it now is nothing; the cost of widening it later is a rule violation.
//
// It matters most for the value-critical modules: the fail-closed reasoning below
// is about outstanding entitlement liability, which lives in rewards, not in
// CoreSlot.
type MigrationKeepers struct {
	CoreSlot coreslotkeeper.Keeper
	Rewards  rewardskeeper.Keeper
	Mining   miningkeeper.Keeper
}

// Upgrades is the live registry.
//
// A scheduling-time check that a name is known would be worse than useless: it is
// the state upstream aborts the block on for every height before the upgrade's
// own. A plan naming an upgrade this binary cannot run stays visible through
// `query upgrade plan` and the coreslot_upgrade_scheduled event, and remains
// cancellable by the authority for the whole window before its height. Only when
// that height arrives do blocks stop, and the answer then is to supply the
// binary. See ADR-0003 §1b.
var Upgrades = []Upgrade{
	{
		// The first state-machine change after v0.1.0, which shipped CoreSlot at
		// consensus version 1. Two-step authority rotation takes it to 2, so a
		// chain running the released baseline needs a named upgrade to advance its
		// module version map.
		//
		// Named for the version it upgrades TO, per CONTRIBUTING.md. Released
		// entries are append-only and may never be edited.
		//
		// No StoreUpgrades: the pending-authority collection lives under a new
		// PREFIX inside the CoreSlot store, not in a new store, so nothing has to
		// be mounted before the application loads.
		//
		// No Migrate: the module-level 1->2 migration is a state no-op, and there
		// is no chain-specific state to transform beyond it. A body here would be
		// fabricating state the released chain never had.
		Name: "v0.2.0",
	},
	{
		// The TW-006 controls merged after v0.2.0: the first-funding minimum in
		// app/sendrestriction.go (#159) and the per-transaction bank-output cap in
		// app/antehandler.go (#163). Both change transaction validity — a send the
		// v0.2.0 binary accepts, a binary built from this source rejects — so they
		// need a coordinated halt rather than a rolling restart.
		//
		// Named for the version it upgrades TO, per CONTRIBUTING.md. Released
		// entries are append-only and may never be edited.
		//
		// This entry carries NOTHING, and unlike v0.2.0 that is not because its
		// migration happens to be a no-op. v0.2.0 existed because CoreSlot moved
		// from consensus version 1 to 2, and the boundary was what advanced the
		// module version map. Nothing here moves a module version at all: coreslot
		// stays 2, rewards 1, mining 1. RunMigrations will find no deltas and do
		// nothing.
		//
		// That is the point rather than an omission. Both controls live in
		// application wiring — a bank SendRestriction and an ante decorator — and
		// take effect the instant the new binary runs. There is no state to move;
		// what is needed is agreement on WHEN every validator starts enforcing
		// them. This entry is that agreement: a node reaching the height without
		// this name compiled in halts instead of accepting a transaction its peers
		// reject.
		//
		// No StoreUpgrades: no store is added, renamed or deleted.
		//
		// No Migrate: there is no chain-specific state to transform. A body here
		// would be fabricating state the released chain never had.
		//
		// TestThisReleaseMovesNoModuleConsensusVersion pins the assumption. If a
		// later change bumps a module version before v0.3.0 ships, that test fails
		// and this entry must be revisited rather than silently skipping a
		// migration.
		Name: "v0.3.0",
	},
}

// ValidateUpgrades rejects a registry that cannot be executed unambiguously.
//
// A duplicate name is the dangerous case, and it is dangerous asymmetrically:
// handler registration is a map assignment, so the LAST entry with a given name
// wins, while the store loader scans the slice and takes the FIRST entry with
// non-nil StoreUpgrades. Two entries sharing a name can therefore pair one
// declaration's store layout with another's migration, under a name the operator
// believes identifies a single upgrade.
//
// An empty name is rejected because it can never match a plan: x/upgrade's
// Plan.ValidateBasic requires a non-empty name, so such an entry is unreachable
// and its presence means the registry does not say what its author thought.
//
// Exported for testing. The registry is compiled in, so a violation is a build
// mistake rather than an operational one, and the caller panics.
func ValidateUpgrades(upgrades []Upgrade) error {
	seen := make(map[string]struct{}, len(upgrades))
	for i, upgrade := range upgrades {
		if upgrade.Name == "" {
			return fmt.Errorf("upgrade registry entry %d has an empty name", i)
		}
		if _, duplicate := seen[upgrade.Name]; duplicate {
			return fmt.Errorf("upgrade registry declares %q more than once", upgrade.Name)
		}
		seen[upgrade.Name] = struct{}{}
	}
	return nil
}

// registerUpgradeHandlers binds every declared upgrade to the keeper, and refuses
// to start on a registry that cannot be executed unambiguously.
//
// Registration is unconditional and happens on every start, not only when an
// upgrade is pending. It exists so that the binary carrying a handler can EXECUTE
// it at the upgrade height — Binary B's job. It is not there to let a binary vet
// a name being scheduled: a binary that holds a pending upgrade's handler before
// that upgrade's height is precisely what upstream aborts the block on.
func registerUpgradeHandlers(
	runtimeApp *runtime.App,
	upgradeKeeper *upgradekeeper.Keeper,
	coreSlot coreslotkeeper.Keeper,
	rewards rewardskeeper.Keeper,
	mining miningkeeper.Keeper,
) {
	// Fail at construction rather than at the upgrade height. A malformed registry
	// is a property of the binary, so every node carrying it is wrong in the same
	// way, and the cheapest moment to say so is before the node starts.
	if err := ValidateUpgrades(Upgrades); err != nil {
		panic(err)
	}
	keepers := MigrationKeepers{CoreSlot: coreSlot, Rewards: rewards, Mining: mining}
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
					if err := upgrade.Migrate(sdk.UnwrapSDKContext(ctx), keepers); err != nil {
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

// The nil checks guard the keeper, not the interface. x/coreslot holds this as a
// non-nil interface value wrapping a struct, so a nil check on its side can never
// fire in the real application — only the pointer inside can be absent, and
// dereferencing it would panic inside a message handler, halting the chain at an
// arbitrary block.
func (s upgradeScheduler) ScheduleUpgrade(ctx context.Context, name string, height int64, info string) error {
	if s.keeper == nil {
		return coreslottypes.ErrUpgradeUnavailable
	}
	return s.keeper.ScheduleUpgrade(ctx, upgradetypes.Plan{Name: name, Height: height, Info: info})
}

func (s upgradeScheduler) CancelUpgrade(ctx context.Context) error {
	if s.keeper == nil {
		return coreslottypes.ErrUpgradeUnavailable
	}
	return s.keeper.ClearUpgradePlan(ctx)
}

// PendingUpgrade reports the name of the scheduled plan, or "" when none is set.
func (s upgradeScheduler) PendingUpgrade(ctx context.Context) (string, error) {
	if s.keeper == nil {
		return "", coreslottypes.ErrUpgradeUnavailable
	}
	plan, err := s.keeper.GetUpgradePlan(ctx)
	if err != nil {
		if errors.Is(err, upgradetypes.ErrNoUpgradePlanFound) {
			return "", nil
		}
		return "", err
	}
	return plan.Name, nil
}

// requireCompleteModuleVersionMap fails genesis unless every mounted module's
// version was committed to state.
//
// The map is what a future upgrade reads to decide, per module, whether to run a
// migration or to treat the module as newly added and run its InitGenesis with
// DefaultGenesis. A module missing from it is therefore not a missing record but
// a scheduled overwrite of live state, deferred until the first upgrade — long
// after the genesis that caused it.
func requireCompleteModuleVersionMap(
	ctx sdk.Context, runtimeApp *runtime.App, upgradeKeeper *upgradekeeper.Keeper,
) error {
	stored, err := upgradeKeeper.GetModuleVersionMap(ctx)
	if err != nil {
		return fmt.Errorf("reading the committed module version map: %w", err)
	}
	missing := make([]string, 0, len(runtimeApp.ModuleManager.Modules))
	for name := range runtimeApp.ModuleManager.Modules {
		if _, ok := stored[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	// State what was observed, then offer the likeliest cause. The guard is broader
	// than that one cause — it also catches a version map prepared incompletely at
	// wiring time, where the section IS present and InitGenesis DID run — and an
	// error naming only the missing section would send an operator to inspect a
	// genesis file that is correct.
	return fmt.Errorf(
		"genesis committed no module version for %s, so the stored version map does not describe "+
			"every mounted module; the first upgrade would treat those modules as newly added and "+
			"run their InitGenesis over live state. The usual cause is a genesis document with no "+
			"%q section, which makes the module manager skip x/upgrade's InitGenesis entirely",
		strings.Join(missing, ", "), upgradetypes.ModuleName)
}
