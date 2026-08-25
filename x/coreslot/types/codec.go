package types

import (
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/msgservice"
)

func RegisterInterfaces(registry codectypes.InterfaceRegistry) {
	registry.RegisterImplementations(
		(*sdk.Msg)(nil),
		&MsgRegisterCoreSlot{},
		&MsgActivateCoreSlot{},
		&MsgInactivateCoreSlot{},
		&MsgSuspendCoreSlot{},
		&MsgRemoveCoreSlot{},
		&MsgRotateConsensusKey{},
		&MsgUpdatePayoutAddress{},
		&MsgUpdateOperatorMetadata{},
		&MsgUpdateSettlementAddress{},
		&MsgUpdateSelectionPolicy{},
		&MsgUpdateParams{},
		&MsgScheduleUpgrade{},
		&MsgCancelUpgrade{},
	)
	cryptocodec.RegisterInterfaces(registry)
	msgservice.RegisterMsgServiceDesc(registry, &_Msg_serviceDesc)
}

func RegisterLegacyAminoCodec(cdc *codec.LegacyAmino) {}

func UnpackPubKey(cdc codec.Codec, any interface{ GetTypeUrl() string }) (cryptotypes.PubKey, error) {
	_ = cdc
	return nil, ErrInvalidPubKey.Wrapf("unsupported Any implementation for %s", any.GetTypeUrl())
}
