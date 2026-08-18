package mining

import (
	"context"
	"encoding/json"
	"fmt"

	gwruntime "github.com/grpc-ecosystem/grpc-gateway/runtime"

	"cosmossdk.io/core/appmodule"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"

	"github.com/twilight-project/twilight-core/x/mining/keeper"
	"github.com/twilight-project/twilight-core/x/mining/types"
)

const ConsensusVersion = 1

type AppModuleBasic struct{}

func NewAppModuleBasic() AppModuleBasic { return AppModuleBasic{} }

func (AppModuleBasic) Name() string { return types.ModuleName }
func (AppModuleBasic) RegisterLegacyAminoCodec(cdc *codec.LegacyAmino) {
	types.RegisterLegacyAminoCodec(cdc)
}
func (AppModuleBasic) RegisterInterfaces(registry codectypes.InterfaceRegistry) {
	types.RegisterInterfaces(registry)
}

// RegisterGRPCGatewayRoutes has no routes to register in this gate: the mining
// query service arrives with the settlement observability surface. It is
// implemented now so the module satisfies the interface and so adding the service
// later is a change to a body rather than a new registration point.
func (AppModuleBasic) RegisterGRPCGatewayRoutes(_ client.Context, _ *gwruntime.ServeMux) {}

func (AppModuleBasic) DefaultGenesis(cdc codec.JSONCodec) json.RawMessage {
	return cdc.MustMarshalJSON(types.DefaultGenesis())
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

func NewAppModule(k keeper.Keeper) AppModule {
	return AppModule{AppModuleBasic: NewAppModuleBasic(), keeper: k}
}

func (AppModule) IsAppModule()             {}
func (AppModule) IsOnePerModuleType()      {}
func (AppModule) Name() string             { return types.ModuleName }
func (AppModule) ConsensusVersion() uint64 { return ConsensusVersion }

// RegisterServices registers the settlement message service.
//
// The settlement transactions are the only messages this module will ever own.
// There is deliberately no mode, Selection-parameter or settlement-parameter
// update transaction in this profile — the state model for all three exists and is
// historically versioned, but the mutation surfaces do not — and there is no
// message that opens a settlement.
//
// The query service arrives with the observability gate.
func (am AppModule) RegisterServices(cfg module.Configurator) {
	types.RegisterMsgServer(cfg.MsgServer(), keeper.NewMsgServer(am.keeper))
}

func (am AppModule) InitGenesis(ctx sdk.Context, cdc codec.JSONCodec, raw json.RawMessage) {
	var genesis types.GenesisState
	cdc.MustUnmarshalJSON(raw, &genesis)
	if err := am.keeper.InitGenesis(ctx, genesis); err != nil {
		panic(fmt.Errorf("initialize mining genesis: %w", err))
	}
}

func (am AppModule) ExportGenesis(ctx sdk.Context, cdc codec.JSONCodec) json.RawMessage {
	genesis, err := am.keeper.ExportGenesis(ctx)
	if err != nil {
		panic(err)
	}
	return cdc.MustMarshalJSON(genesis)
}

// EndBlock is the settlement clock, materialization and promotion transition.
//
// It runs after x/rewards so a reward epoch finalized in this block is visible to
// materialization. It returns only an error and never emits validator updates;
// CoreSlot remains the sole validator-update emitter. It performs no bank
// operation of any kind — this module holds no bank keeper — so no automatic
// payout or finalization can occur here by construction.
//
// The transition is a no-op in this gate; the clock and materialization arrive
// with the settlement gate.
func (am AppModule) EndBlock(ctx context.Context) error {
	return am.keeper.EndBlock(ctx)
}

// module.AppModule is deprecated pending the SDK's extension-interface migration.
// It is asserted anyway because the module manager still resolves modules through
// it and the two existing custom modules assert the same set; a module that
// silently stopped satisfying it would be discovered at runtime rather than here.
// Migrating that surface is one change across all three modules, not a divergence
// introduced by the newest one.
//
//nolint:staticcheck // SA1019: consistent with the existing custom-module surface
var (
	_ module.AppModule        = AppModule{}
	_ module.HasGenesis       = AppModule{}
	_ module.HasServices      = AppModule{}
	_ appmodule.AppModule     = AppModule{}
	_ appmodule.HasEndBlocker = AppModule{}
)
