package app

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"
	dbm "github.com/cosmos/cosmos-db"

	"cosmossdk.io/client/v2/autocli"
	"cosmossdk.io/core/appmodule"
	"cosmossdk.io/depinject"
	"cosmossdk.io/log"
	storetypes "cosmossdk.io/store/types"
	_ "cosmossdk.io/x/upgrade"
	upgradekeeper "cosmossdk.io/x/upgrade/keeper"
	upgradetypes "cosmossdk.io/x/upgrade/types"

	"github.com/spf13/cast"

	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/codec"
	addresscodec "github.com/cosmos/cosmos-sdk/codec/address"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/runtime"
	runtimeservices "github.com/cosmos/cosmos-sdk/runtime/services"
	serverapi "github.com/cosmos/cosmos-sdk/server/api"
	serverconfig "github.com/cosmos/cosmos-sdk/server/config"
	servertypes "github.com/cosmos/cosmos-sdk/server/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	_ "github.com/cosmos/cosmos-sdk/x/auth"
	authkeeper "github.com/cosmos/cosmos-sdk/x/auth/keeper"
	_ "github.com/cosmos/cosmos-sdk/x/auth/tx/config"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	_ "github.com/cosmos/cosmos-sdk/x/bank"
	bankkeeper "github.com/cosmos/cosmos-sdk/x/bank/keeper"
	_ "github.com/cosmos/cosmos-sdk/x/consensus"

	"github.com/twilight-project/twilight-core/app/openapi"
	"github.com/twilight-project/twilight-core/app/params"
	"github.com/twilight-project/twilight-core/internal/economicaddress"
	"github.com/twilight-project/twilight-core/x/coreslot"
	coreslotkeeper "github.com/twilight-project/twilight-core/x/coreslot/keeper"
	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	"github.com/twilight-project/twilight-core/x/mining"
	miningkeeper "github.com/twilight-project/twilight-core/x/mining/keeper"
	miningtypes "github.com/twilight-project/twilight-core/x/mining/types"
	"github.com/twilight-project/twilight-core/x/rewards"
	rewardskeeper "github.com/twilight-project/twilight-core/x/rewards/keeper"
	rewardstypes "github.com/twilight-project/twilight-core/x/rewards/types"
)

const (
	Name          = "twilight"
	AccountPrefix = "twilight"
	// Denom identity is sourced from the canonical, dependency-neutral
	// app/params package (see §8 of the migration plan). BaseDenom (utwlt) is the
	// only denom used in stateful accounting; DisplayDenom (twlt) and Symbol
	// (TWLT) are display metadata only.
	BaseDenom                    = params.NativeBaseDenom
	DisplayDenom                 = params.NativeDisplayDenom
	Symbol                       = params.NativeSymbol
	AuthorityModuleName          = "coreslot-authority"
	EmergencyAuthorityModuleName = "coreslot-emergency"
)

var DefaultNodeHome = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".twilightd"
	}
	return filepath.Join(home, ".twilightd")
}()

type App struct {
	*runtime.App
	CoreSlotKeeper coreslotkeeper.Keeper
	UpgradeKeeper  *upgradekeeper.Keeper
	RewardsKeeper  rewardskeeper.Keeper
	MiningKeeper   miningkeeper.Keeper
	AccountKeeper  authkeeper.AccountKeeper
	BankKeeper     bankkeeper.BaseKeeper
	appCodec       codec.Codec
}

func AuthorityAddress() string {
	return authtypes.NewModuleAddress(AuthorityModuleName).String()
}

func EmergencyAuthorityAddress() string {
	return authtypes.NewModuleAddress(EmergencyAuthorityModuleName).String()
}

// AutoCliOpts returns the AutoCLI options built from the live module manager. It
// is used by the root command to generate the standard `tx`/`query` command
// trees for every wired module (bank, auth, consensus, ...).
func (a *App) AutoCliOpts() autocli.AppOptions {
	modules := make(map[string]appmodule.AppModule, len(a.ModuleManager.Modules))
	for name, m := range a.ModuleManager.Modules {
		if am, ok := m.(appmodule.AppModule); ok {
			modules[name] = am
		}
	}
	moduleOptions := runtimeservices.ExtractAutoCLIOptions(a.ModuleManager.Modules)
	// x/upgrade's generated tx commands all submit GOVERNANCE proposals, and this
	// chain has no governance module — so `tx upgrade software-upgrade` and its
	// siblings build a MsgSubmitProposal nothing can route, and an operator who
	// finds them instead of `tx coreslot schedule-upgrade` gets a failure at
	// broadcast time with no indication why.
	//
	// Only the tx tree is dropped. The queries stay: `query upgrade plan` is how an
	// operator sees a pending halt, which matters more here than anywhere, because
	// scheduling is deliberately routed through x/coreslot instead.
	if opts, ok := moduleOptions[upgradetypes.ModuleName]; ok && opts != nil {
		opts.Tx = nil
	}
	return autocli.AppOptions{
		Modules:               modules,
		ModuleOptions:         moduleOptions,
		AddressCodec:          addresscodec.NewBech32Codec(AccountPrefix),
		ValidatorAddressCodec: addresscodec.NewBech32Codec(AccountPrefix + "valoper"),
		ConsensusAddressCodec: addresscodec.NewBech32Codec(AccountPrefix + "valcons"),
	}
}

func New(logger log.Logger, db dbm.DB, traceStore io.Writer, loadLatest bool, appOpts servertypes.AppOptions, baseAppOptions ...func(*baseapp.BaseApp)) *App {
	var (
		builder       *runtime.AppBuilder
		cdc           codec.Codec
		accountKeeper authkeeper.AccountKeeper
		bankKeeper    bankkeeper.BaseKeeper
		upgradeKeeper *upgradekeeper.Keeper
	)
	// appOpts was previously discarded. It must be supplied: x/upgrade reads the
	// home directory from it, and without one the keeper looks for its
	// upgrade-info file at an empty path. Nothing fails at startup — the node
	// simply never learns that it is resuming into a new store layout, which is a
	// silent divergence rather than an error. depinject marks the input optional,
	// so the omission would not be reported either.
	// Resolved here as well as inside x/upgrade, because the store-loader call
	// below has to know whether a real home exists before touching the filesystem.
	var homePath string
	if appOpts != nil {
		homePath = cast.ToString(appOpts.Get(flags.FlagHome))
	}
	configs := []depinject.Config{AppConfig, depinject.Supply(logger)}
	if appOpts != nil {
		// Supplied separately because depinject panics on a nil interface value
		// rather than treating it as absent. The one caller that passes nil is the
		// throwaway in-memory app the root command builds to extract AutoCLI
		// options: it never loads stores from disk and never processes a block, so
		// an empty home directory costs it nothing.
		configs = append(configs, depinject.Supply(appOpts))
	}
	if err := depinject.Inject(
		depinject.Configs(configs...),
		&builder, &cdc, &accountKeeper, &bankKeeper, &upgradeKeeper,
	); err != nil {
		panic(err)
	}
	runtimeApp := builder.Build(db, traceStore, baseAppOptions...)

	// The one canonical economic-address rule (§25), derived here from the two
	// authorities that own the answer: the auth configuration's module-account
	// declaration, and the bank module's own blocked-destination set. It is
	// injected into both custom modules as a plain value, so neither gains a
	// keeper edge and neither has to import this package.
	economicAddresses, err := economicaddress.New(
		accountKeeper.AddressCodec(),
		ModuleAccountNames(),
		bankKeeper.GetBlockedAddresses(),
	)
	if err != nil {
		panic(err)
	}

	// CoreSlot keeper must be constructed before the rewards keeper: rewards
	// depends on CoreSlot's read-only interface and must never depend on the
	// reverse.
	coreSlotKey := storetypes.NewKVStoreKey(coreslottypes.StoreKey)
	// The upgrade scheduler is injected as a plain value, not a keeper edge: it is
	// the only route from x/coreslot to x/upgrade, and the only route to x/upgrade
	// at all, because the upgrade module's own authority is a module address with
	// no private key. See ADR-0003.
	coreSlotKeeper := coreslotkeeper.NewKeeper(
		cdc, runtime.NewKVStoreService(coreSlotKey), economicAddresses, upgradeScheduler{keeper: upgradeKeeper})
	coreSlotModule := coreslot.NewAppModule(coreSlotKeeper, AuthorityAddress(), EmergencyAuthorityAddress())

	rewardsKey := storetypes.NewKVStoreKey(rewardstypes.StoreKey)
	rewardsKeeper := rewardskeeper.NewKeeper(
		cdc,
		runtime.NewKVStoreService(rewardsKey),
		accountKeeper,
		bankKeeper,
		coreSlotKeeper,
		economicAddresses,
	)
	rewardsModule := rewards.NewAppModule(rewardsKeeper)

	// The mining keeper is constructed last: it reads both custom modules and is
	// read by neither. It takes no bank or account keeper, which is what makes
	// "x/mining never moves value itself" a structural property rather than a rule.
	miningKey := storetypes.NewKVStoreKey(miningtypes.StoreKey)
	miningKeeper := miningkeeper.NewKeeper(
		cdc,
		runtime.NewKVStoreService(miningKey),
		coreSlotKeeper,
		rewardsKeeper,
		economicAddresses,
	)
	miningModule := mining.NewAppModule(miningKeeper)

	if err := runtimeApp.RegisterStores(coreSlotKey, rewardsKey, miningKey); err != nil {
		panic(err)
	}
	if err := runtimeApp.RegisterModules(coreSlotModule, rewardsModule, miningModule); err != nil {
		panic(err)
	}
	// The version map persisted at genesis MUST cover every module the app mounts.
	//
	// x/upgrade's depinject invoker (PopulateVersionMap) only sees the modules
	// depinject itself wired — auth, bank, consensus, runtime, upgrade. The three
	// custom modules are registered above, afterwards, so without this line they
	// never reach the map InitGenesis stores.
	//
	// The consequence is not a missing entry, it is data loss. At the first
	// upgrade, RunMigrations reads the stored map, finds coreslot, rewards and
	// mining absent, classifies each as a NEW module, and runs its InitGenesis
	// with DefaultGenesis over live state — a default validator set over the real
	// one, and a default rewards genesis over outstanding entitlement liability.
	//
	// Set from the module manager, after RegisterModules, so the map is the one
	// the app actually runs rather than the one depinject happened to assemble.
	upgradeKeeper.SetInitVersionMap(runtimeApp.ModuleManager.GetVersionMap())

	// Must run BEFORE Load: handler registration has to be in place for the keeper
	// to answer whether it knows an upgrade name at the moment a plan is proposed.
	registerUpgradeHandlers(runtimeApp, upgradeKeeper, coreSlotKeeper, rewardsKeeper, miningKeeper)

	// Only when a home directory is actually configured. Reading the upgrade-info
	// file creates <home>/data, so with an empty home it would create a relative
	// "data" directory in the process's working directory — on every twilightd
	// invocation, including `version` and `--help`, because the root command
	// builds a throwaway app to extract AutoCLI options. It also panics outright
	// when the working directory is read-only, which is a routine container and
	// systemd hardening setup.
	if homePath != "" {
		setUpgradeStoreLoader(runtimeApp, upgradeKeeper)
	}

	if err := runtimeApp.Load(loadLatest); err != nil {
		panic(err)
	}
	return &App{
		App:            runtimeApp,
		CoreSlotKeeper: coreSlotKeeper,
		UpgradeKeeper:  upgradeKeeper,
		RewardsKeeper:  rewardsKeeper,
		MiningKeeper:   miningKeeper,
		AccountKeeper:  accountKeeper,
		BankKeeper:     bankKeeper,
		appCodec:       cdc,
	}
}

// RegisterAPIRoutes registers all the REST gRPC-gateway routes via the embedded
// runtime.App, then mounts the Swagger UI + merged OpenAPI spec at /swagger/ when
// api.swagger is enabled. It overrides runtime.App.RegisterAPIRoutes (which does not
// serve Swagger). When the API server is disabled this method is never called, and
// when swagger=false the Swagger registration is a no-op, so REST is unaffected.
func (a *App) RegisterAPIRoutes(apiSvr *serverapi.Server, apiConfig serverconfig.APIConfig) {
	a.App.RegisterAPIRoutes(apiSvr, apiConfig)
	if apiConfig.Swagger {
		if err := openapi.RegisterSwaggerAPI(apiSvr.Router, apiConfig.Swagger); err != nil {
			panic(err)
		}
	}
}

func (a *App) ExportAppStateAndValidators(_ bool, _ []string, modulesToExport []string) (servertypes.ExportedApp, error) {
	ctx := a.NewContextLegacy(true, cmtproto.Header{Height: a.LastBlockHeight()})
	genesis, err := a.ModuleManager.ExportGenesisForModules(ctx, a.appCodec, modulesToExport)
	if err != nil {
		return servertypes.ExportedApp{}, err
	}
	appState, err := json.MarshalIndent(genesis, "", "  ")
	if err != nil {
		return servertypes.ExportedApp{}, err
	}
	coreGenesis, err := a.CoreSlotKeeper.ExportGenesis(ctx)
	if err != nil {
		return servertypes.ExportedApp{}, err
	}
	validators := make([]cmttypes.GenesisValidator, 0)
	for _, slot := range coreGenesis.Slots {
		if slot.Status != coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE {
			continue
		}
		pk, err := coreslotkeeper.DecodePubKey(slot.ConsensusPubkey)
		if err != nil {
			return servertypes.ExportedApp{}, err
		}
		cmtPK, err := cryptocodec.ToCmtPubKeyInterface(pk)
		if err != nil {
			return servertypes.ExportedApp{}, err
		}
		validators = append(validators, cmttypes.GenesisValidator{PubKey: cmtPK, Power: slot.ConsensusPower, Name: slot.Metadata.GetMoniker()})
	}
	height := a.LastBlockHeight() + 1
	return servertypes.ExportedApp{
		AppState: appState, Validators: validators, Height: height, ConsensusParams: a.GetConsensusParams(ctx),
	}, nil
}

func init() {
	config := sdk.GetConfig()
	config.SetBech32PrefixForAccount(AccountPrefix, AccountPrefix+"pub")
	config.SetBech32PrefixForValidator(AccountPrefix+"valoper", AccountPrefix+"valoperpub")
	config.SetBech32PrefixForConsensusNode(AccountPrefix+"valcons", AccountPrefix+"valconspub")
	config.SetCoinType(118)
}
