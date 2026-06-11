package rewards

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

	"github.com/twilight-project/twilight-core/x/rewards/keeper"
	"github.com/twilight-project/twilight-core/x/rewards/types"
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

// RegisterGRPCGatewayRoutes is intentionally empty: the rewards query service
// and its REST gateway are deferred to Phase 9. Only the Msg service is wired in
// Phase 8.
func (AppModuleBasic) RegisterGRPCGatewayRoutes(client.Context, *gwruntime.ServeMux) {}

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

// RegisterServices wires the Msg service and the read-only Query service.
func (am AppModule) RegisterServices(cfg module.Configurator) {
	types.RegisterMsgServer(cfg.MsgServer(), keeper.NewMsgServer(am.keeper))
	types.RegisterQueryServer(cfg.QueryServer(), keeper.NewQueryServer(am.keeper))
}

// RegisterInvariants exposes the rewards invariants to an invariant registry if
// one is present. The chain does not run the crisis module, so these are
// callable backstops rather than auto-run invariants.
func (am AppModule) RegisterInvariants(ir sdk.InvariantRegistry) {
	ir.RegisterRoute(types.ModuleName, "supply-cap", am.keeper.SupplyCapInvariant())
	ir.RegisterRoute(types.ModuleName, "cumulative-emitted", am.keeper.CumulativeEmittedInvariant())
	ir.RegisterRoute(types.ModuleName, "module-balance-coverage", am.keeper.ModuleBalanceCoverageInvariant())
	ir.RegisterRoute(types.ModuleName, "denom-correctness", am.keeper.DenomCorrectnessInvariant())
	ir.RegisterRoute(types.ModuleName, "closed-epoch-immutability", am.keeper.ClosedEpochImmutabilityInvariant())
}

func (am AppModule) InitGenesis(ctx sdk.Context, cdc codec.JSONCodec, raw json.RawMessage) {
	var genesis types.GenesisState
	cdc.MustUnmarshalJSON(raw, &genesis)
	if err := am.keeper.InitGenesis(ctx, genesis); err != nil {
		panic(fmt.Errorf("initialize rewards genesis: %w", err))
	}
}

func (am AppModule) ExportGenesis(ctx sdk.Context, cdc codec.JSONCodec) json.RawMessage {
	genesis, err := am.keeper.ExportGenesis(ctx)
	if err != nil {
		panic(err)
	}
	return cdc.MustMarshalJSON(genesis)
}

// BeginBlock credits active-block participation for the open epoch. It returns
// only an error and never emits validator updates.
func (am AppModule) BeginBlock(ctx context.Context) error {
	return am.keeper.BeginBlock(ctx)
}

// EndBlock runs the epoch finalization gate. It returns only an error and never
// emits validator updates; CoreSlot remains the sole validator-update emitter.
func (am AppModule) EndBlock(ctx context.Context) error {
	return am.keeper.EndBlock(ctx)
}

var (
	_ module.AppModule          = AppModule{}
	_ module.HasGenesis         = AppModule{}
	_ module.HasServices        = AppModule{}
	_ module.HasInvariants      = AppModule{}
	_ appmodule.AppModule       = AppModule{}
	_ appmodule.HasBeginBlocker = AppModule{}
	_ appmodule.HasEndBlocker   = AppModule{}
)
