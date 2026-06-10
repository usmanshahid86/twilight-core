package types

const (
	ModuleName = "coreslot"
	StoreKey   = ModuleName
	RouterKey  = ModuleName
)

var (
	ParamsKey       = []byte{0x01}
	SlotsPrefix     = []byte{0x02}
	OperatorPrefix  = []byte{0x03}
	ConsensusPrefix = []byte{0x04}
	ReservedPrefix  = []byte{0x05}
	RotationsPrefix = []byte{0x06}
	LastPrefix      = []byte{0x07}
	RewardsPrefix   = []byte{0x08}
	NextSlotIDKey   = []byte{0x09}
)
