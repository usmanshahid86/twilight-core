package coreslot

import (
	"context"
	"encoding/json"
	"fmt"

	abci "github.com/cometbft/cometbft/abci/types"
	gwruntime "github.com/grpc-ecosystem/grpc-gateway/runtime"

	"cosmossdk.io/core/appmodule"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"

	"github.com/twilight-project/twilight-core/x/coreslot/keeper"
	"github.com/twilight-project/twilight-core/x/coreslot/types"
)

const ConsensusVersion = 1

type AppModuleBasic struct {
	authority          string
	emergencyAuthority string
}

func NewAppModuleBasic(authority, emergencyAuthority string) AppModuleBasic {
	return AppModuleBasic{authority: authority, emergencyAuthority: emergencyAuthority}
}

func (AppModuleBasic) Name() string { return types.ModuleName }
func (AppModuleBasic) RegisterLegacyAminoCodec(cdc *codec.LegacyAmino) {
	types.RegisterLegacyAminoCodec(cdc)
}
func (AppModuleBasic) RegisterInterfaces(registry codectypes.InterfaceRegistry) {
	types.RegisterInterfaces(registry)
}
func (AppModuleBasic) RegisterGRPCGatewayRoutes(client.Context, *gwruntime.ServeMux) {}
func (b AppModuleBasic) DefaultGenesis(cdc codec.JSONCodec) json.RawMessage {
	return cdc.MustMarshalJSON(types.DefaultGenesis(b.authority, b.emergencyAuthority))
}
func (AppModuleBasic) ValidateGenesis(cdc codec.JSONCodec, _ client.TxEncodingConfig, raw json.RawMessage) error {
	var genesis types.GenesisState
	if err := cdc.UnmarshalJSON(raw, &genesis); err != nil {
		return err
	}
	return genesis.Validate()
}

type AppModule struct {
	AppModuleBasic
	keeper keeper.Keeper
}

func NewAppModule(k keeper.Keeper, authority, emergencyAuthority string) AppModule {
	return AppModule{AppModuleBasic: NewAppModuleBasic(authority, emergencyAuthority), keeper: k}
}

func (AppModule) IsAppModule()             {}
func (AppModule) IsOnePerModuleType()      {}
func (AppModule) Name() string             { return types.ModuleName }
func (AppModule) ConsensusVersion() uint64 { return ConsensusVersion }

func (am AppModule) RegisterServices(cfg module.Configurator) {
	types.RegisterMsgServer(cfg.MsgServer(), keeper.NewMsgServer(am.keeper))
	types.RegisterQueryServer(cfg.QueryServer(), keeper.NewQueryServer(am.keeper))
}

func (am AppModule) InitGenesis(ctx sdk.Context, cdc codec.JSONCodec, raw json.RawMessage) []abci.ValidatorUpdate {
	var genesis types.GenesisState
	cdc.MustUnmarshalJSON(raw, &genesis)
	updates, err := am.keeper.InitGenesis(ctx, &genesis)
	if err != nil {
		panic(fmt.Errorf("initialize coreslot genesis: %w", err))
	}
	return updates
}

func (am AppModule) ExportGenesis(ctx sdk.Context, cdc codec.JSONCodec) json.RawMessage {
	genesis, err := am.keeper.ExportGenesis(ctx)
	if err != nil {
		panic(err)
	}
	return cdc.MustMarshalJSON(genesis)
}

func (am AppModule) EndBlock(ctx context.Context) ([]abci.ValidatorUpdate, error) {
	return am.keeper.EndBlock(ctx)
}

var (
	_ module.AppModule       = AppModule{}
	_ module.HasABCIGenesis  = AppModule{}
	_ module.HasABCIEndBlock = AppModule{}
	_ appmodule.AppModule    = AppModule{}
)
