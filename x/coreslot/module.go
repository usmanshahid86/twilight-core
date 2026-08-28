package coreslot

import (
	"context"
	"encoding/json"
	"fmt"

	abci "github.com/cometbft/cometbft/abci/types"
	gwruntime "github.com/grpc-ecosystem/grpc-gateway/runtime"
	"github.com/spf13/cobra"

	"cosmossdk.io/core/appmodule"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/module"

	"github.com/twilight-project/twilight-core/x/coreslot/client/cli"
	"github.com/twilight-project/twilight-core/x/coreslot/keeper"
	"github.com/twilight-project/twilight-core/x/coreslot/types"
)

// ConsensusVersion is 2 as of the two-step authority rotation.
//
// v0.1.0 shipped CoreSlot at version 1 (tag resolves to b8ed78e), and that is a
// released baseline capable of carrying v1 live state. Version 2 adds the pending-authority
// collection and changes the authorization path for a role that gates validator
// admission, so the two implementations must be distinguishable in the module
// version map. Leaving both at 1 would make a later migration unable to tell a
// v0.1.0 chain from a post-rotation one, and the version map is exactly what a
// future upgrade consults to decide whether to migrate a module or to treat it
// as newly added and run InitGenesis over live state.
const ConsensusVersion = 2

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

// RegisterGRPCGatewayRoutes wires the coreslot query service into the REST
// gRPC-gateway mux (1317). It panics on a registration error so a broken gateway
// crashes the node at startup rather than silently serving 501s.
func (AppModuleBasic) RegisterGRPCGatewayRoutes(clientCtx client.Context, mux *gwruntime.ServeMux) {
	if err := types.RegisterQueryHandlerClient(context.Background(), mux, types.NewQueryClient(clientCtx)); err != nil {
		panic(err)
	}
}

// GetTxCmd supplies the hand-written CoreSlot transaction tree, which AutoCLI
// mounts at `tx coreslot` INSTEAD OF generating one from the Msg service.
//
// The generated tree could not work. Every CoreSlot message carrying a consensus
// key takes a google.protobuf.Any, and AutoCLI's Any flag builder resolves
// `@type` against protoregistry.GlobalTypes, which holds only pulsar-registered
// types. cosmos.crypto.ed25519.PubKey lives in the gogoproto InterfaceRegistry,
// which that builder never consults, so `tx coreslot register-core-slot` failed
// during flag parsing — before signing, before any network call — for every
// encoding an operator could supply. An operator hit this on a live devnet.
//
// The two trees covered the same 13 operations, so nothing is lost by supplying
// this one: the four names they shared are unchanged, and the nine generated-only
// names give way to the nine hand-written equivalents. This tree builds the Any
// in Go from a base64 or show-validator key, which is why it works.
//
// No GetQueryCmd counterpart is defined, deliberately. Queries carry no Any and
// the generated ones are correct; supplying a custom query command here would
// displace them and move `query coreslot ...` for no reason.
func (AppModuleBasic) GetTxCmd() *cobra.Command { return cli.GetTxCmd() }

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

	// 1 -> 2 is a STATE no-op, and deliberately so.
	//
	// Version 2 adds the pending-authority collection, which is correctly absent
	// on version-1 state: no nomination can exist on a chain whose binary had no
	// way to create one, and an absent collection reads as empty. There is
	// therefore nothing to transform, and writing anything here would be
	// fabricating state the released chain never had.
	//
	// It is registered anyway because its purpose is the version boundary, not the
	// transformation. Without it RunMigrations refuses to advance a module whose
	// consensus version moved, and the module version map would keep describing a
	// post-rotation chain as CoreSlot v1.
	if err := cfg.RegisterMigration(types.ModuleName, 1, func(sdk.Context) error {
		return nil
	}); err != nil {
		panic(fmt.Errorf("register coreslot 1->2 migration: %w", err))
	}
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
