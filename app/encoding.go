package app

import (
	"cosmossdk.io/depinject"
	"cosmossdk.io/log"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
)

type EncodingConfig struct {
	InterfaceRegistry codectypes.InterfaceRegistry
	Codec             codec.Codec
	TxConfig          client.TxConfig
	Amino             *codec.LegacyAmino
}

func MakeEncodingConfig() EncodingConfig {
	var cfg EncodingConfig
	if err := depinject.Inject(depinject.Configs(AppConfig, depinject.Supply(log.NewNopLogger())), &cfg.InterfaceRegistry, &cfg.Codec, &cfg.TxConfig, &cfg.Amino); err != nil {
		panic(err)
	}
	return cfg
}
