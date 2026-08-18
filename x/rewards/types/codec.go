package types

import (
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/msgservice"
)

func RegisterInterfaces(registry codectypes.InterfaceRegistry) {
	registry.RegisterImplementations(
		(*sdk.Msg)(nil),
		&MsgUpdateRewardsParams{},
		&MsgPauseRewards{},
		&MsgResumeRewards{},
	)
	msgservice.RegisterMsgServiceDesc(registry, &_Msg_serviceDesc)
}

func RegisterLegacyAminoCodec(cdc *codec.LegacyAmino) {
	cdc.RegisterConcrete(&MsgUpdateRewardsParams{}, "twilight/rewards/MsgUpdateRewardsParams", nil)
	cdc.RegisterConcrete(&MsgPauseRewards{}, "twilight/rewards/MsgPauseRewards", nil)
	cdc.RegisterConcrete(&MsgResumeRewards{}, "twilight/rewards/MsgResumeRewards", nil)
}
