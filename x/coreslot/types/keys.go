package types

const (
	ModuleName = "coreslot"
	StoreKey   = ModuleName
	RouterKey  = ModuleName
)

// Durable store-prefix ledger. A prefix byte is permanent: once assigned it is
// never recycled for different state, even if the collection it named is later
// removed. Add new prefixes at the end of the ledger and never renumber.
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
	// SelectionPoliciesPrefix holds the immutable SelectionPolicyVersion history,
	// keyed by (slot_id, policy_version).
	SelectionPoliciesPrefix = []byte{0x0A}
	// ActiveSlotsPrefix holds the membership-only ACTIVE slot index, keyed by
	// slot_id with no value payload. CoreSlot remains the authority for slot data.
	ActiveSlotsPrefix = []byte{0x0B}
	// PolicyStartsPrefix holds the Selection-policy seek index, keyed by
	// (slot_id, valid_from_height) with the policy version as its value.
	//
	// This is DERIVED, rebuildable indexing state over the canonical policy
	// history at SelectionPoliciesPrefix — it is not canonical state and is not a
	// genesis collection. It exists so resolving "the version applicable at height
	// H" is one predecessor seek rather than a walk over a slot's whole history.
	PolicyStartsPrefix = []byte{0x0C}
)
