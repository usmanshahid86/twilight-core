package types

import (
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
)

// RegisterInterfaces registers this module's implementations.
//
// x/mining carries no messages in this gate: the Settlement transactions arrive
// with the message gates, and there is deliberately no mode, Selection-parameter
// or settlement-parameter update transaction in this profile. The function exists
// so the module satisfies the interface and so adding a message later is a change
// to a body rather than a new registration point.
func RegisterInterfaces(_ codectypes.InterfaceRegistry) {}

// RegisterLegacyAminoCodec has nothing to register for the same reason.
func RegisterLegacyAminoCodec(_ *codec.LegacyAmino) {}
