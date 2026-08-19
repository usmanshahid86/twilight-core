package app

import (
	"cosmossdk.io/depinject"
	"cosmossdk.io/log"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"

	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	miningtypes "github.com/twilight-project/twilight-core/x/mining/types"
	rewardstypes "github.com/twilight-project/twilight-core/x/rewards/types"
)

type EncodingConfig struct {
	InterfaceRegistry codectypes.InterfaceRegistry
	Codec             codec.Codec
	TxConfig          client.TxConfig
	Amino             *codec.LegacyAmino
}

// MakeEncodingConfig builds the codec the CLIENT side uses: the client context in
// cmd/twilightd, and through it the node's own transaction service.
//
// # Why the custom modules are registered by hand here
//
// depinject supplies the registry, codec, tx config and amino, but it does not
// construct the application — and the module manager's
// RegisterInterfaces / RegisterLegacyAminoCodec pass runs only when an App is
// built (runtime.App, on each module in turn). Everything this function returns
// would therefore know every standard module's messages and none of this chain's.
//
// The failure that causes is quiet and asymmetric. Block execution uses the App's
// own registry and keeps working, so custom transactions execute perfectly, while
// anything decoding a transaction OUTSIDE execution cannot resolve a
// twilight.* type URL: `twilightd query tx`, the REST and gRPC
// `GetTx` endpoints, and offline transaction building all fail on a transaction
// the chain itself just accepted. A chain that can execute a message and cannot
// read it back is the worst version of this, because nothing fails at the point
// the mistake is made.
//
// Registering both codecs — not only the interface registry — keeps the two in
// step: amino carries the concrete-name registration that interface-valued
// marshaling needs, and it is skipped by the same absent pass.
func MakeEncodingConfig() EncodingConfig {
	var cfg EncodingConfig
	if err := depinject.Inject(depinject.Configs(AppConfig, depinject.Supply(log.NewNopLogger())), &cfg.InterfaceRegistry, &cfg.Codec, &cfg.TxConfig, &cfg.Amino); err != nil {
		panic(err)
	}
	registerCustomModuleCodecs(cfg)
	return cfg
}

// registerCustomModuleCodecs performs, for this chain's own modules, the
// registration the module manager would perform if an App had been built.
//
// It is deliberately one list used for both codecs. A module added to the app and
// forgotten here reintroduces exactly the defect this exists to fix, which is why
// the accompanying test derives what it checks from the exported type-URL manifest
// rather than from a list a reader has to keep in sync by eye.
func registerCustomModuleCodecs(cfg EncodingConfig) {
	coreslottypes.RegisterInterfaces(cfg.InterfaceRegistry)
	rewardstypes.RegisterInterfaces(cfg.InterfaceRegistry)
	miningtypes.RegisterInterfaces(cfg.InterfaceRegistry)

	coreslottypes.RegisterLegacyAminoCodec(cfg.Amino)
	rewardstypes.RegisterLegacyAminoCodec(cfg.Amino)
	miningtypes.RegisterLegacyAminoCodec(cfg.Amino)
}
