package app

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	cmttypes "github.com/cometbft/cometbft/types"
	dbm "github.com/cosmos/cosmos-db"

	"cosmossdk.io/depinject"
	"cosmossdk.io/log"
	storetypes "cosmossdk.io/store/types"

	"github.com/cosmos/cosmos-sdk/baseapp"
	"github.com/cosmos/cosmos-sdk/codec"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	"github.com/cosmos/cosmos-sdk/runtime"
	servertypes "github.com/cosmos/cosmos-sdk/server/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	_ "github.com/cosmos/cosmos-sdk/x/auth"
	_ "github.com/cosmos/cosmos-sdk/x/auth/tx/config"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	_ "github.com/cosmos/cosmos-sdk/x/bank"
	_ "github.com/cosmos/cosmos-sdk/x/consensus"

	"github.com/nyks/nyks-core/x/coreslot"
	coreslotkeeper "github.com/nyks/nyks-core/x/coreslot/keeper"
	coreslottypes "github.com/nyks/nyks-core/x/coreslot/types"
)

const (
	Name                         = "nyks"
	AccountPrefix                = "nyks"
	BaseDenom                    = "unyks"
	DisplayDenom                 = "NYKS"
	AuthorityModuleName          = "coreslot-authority"
	EmergencyAuthorityModuleName = "coreslot-emergency"
)

var DefaultNodeHome = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".nyks"
	}
	return filepath.Join(home, ".nyks")
}()

type App struct {
	*runtime.App
	CoreSlotKeeper coreslotkeeper.Keeper
	appCodec       codec.Codec
}

func AuthorityAddress() string {
	return authtypes.NewModuleAddress(AuthorityModuleName).String()
}

func EmergencyAuthorityAddress() string {
	return authtypes.NewModuleAddress(EmergencyAuthorityModuleName).String()
}

func New(logger log.Logger, db dbm.DB, traceStore io.Writer, loadLatest bool, _ servertypes.AppOptions, baseAppOptions ...func(*baseapp.BaseApp)) *App {
	var (
		builder *runtime.AppBuilder
		cdc     codec.Codec
	)
	if err := depinject.Inject(depinject.Configs(AppConfig, depinject.Supply(logger)), &builder, &cdc); err != nil {
		panic(err)
	}
	runtimeApp := builder.Build(db, traceStore, baseAppOptions...)
	key := storetypes.NewKVStoreKey(coreslottypes.StoreKey)
	keeper := coreslotkeeper.NewKeeper(cdc, runtime.NewKVStoreService(key))
	module := coreslot.NewAppModule(keeper, AuthorityAddress(), EmergencyAuthorityAddress())
	if err := runtimeApp.RegisterStores(key); err != nil {
		panic(err)
	}
	if err := runtimeApp.RegisterModules(module); err != nil {
		panic(err)
	}
	if err := runtimeApp.Load(loadLatest); err != nil {
		panic(err)
	}
	return &App{App: runtimeApp, CoreSlotKeeper: keeper, appCodec: cdc}
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
