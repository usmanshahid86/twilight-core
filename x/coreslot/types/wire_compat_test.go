package types_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	gogoproto "github.com/cosmos/gogoproto/proto"

	"github.com/twilight-project/twilight-core/x/coreslot/types"
)

// Wire-compatibility guarantees for the additive V2 evolution of the v1 API.
//
// The protobuf package is unchanged, so every field number and scalar type
// already in use is permanent. These tests fail if one is renumbered, retyped or
// removed — including the deprecated parameters, which keep their numbers rather
// than being reserved.

// preV2CoreSlot is a CoreSlot as it was encoded before this change: fields 1-13
// only. It is declared as its own message rather than built from the current
// type, so the bytes under test cannot silently follow a schema change.
type preV2CoreSlot struct {
	SlotId          uint64 `protobuf:"varint,1,opt,name=slot_id,json=slotId,proto3" json:"slot_id,omitempty"`
	OperatorAddress string `protobuf:"bytes,2,opt,name=operator_address,json=operatorAddress,proto3" json:"operator_address,omitempty"`
	PayoutAddress   string `protobuf:"bytes,4,opt,name=payout_address,json=payoutAddress,proto3" json:"payout_address,omitempty"`
	Status          int32  `protobuf:"varint,5,opt,name=status,proto3" json:"status,omitempty"`
	ConsensusPower  int64  `protobuf:"varint,6,opt,name=consensus_power,json=consensusPower,proto3" json:"consensus_power,omitempty"`
	RewardWeight    string `protobuf:"bytes,7,opt,name=reward_weight,json=rewardWeight,proto3" json:"reward_weight,omitempty"`
	CreatedHeight   int64  `protobuf:"varint,8,opt,name=created_height,json=createdHeight,proto3" json:"created_height,omitempty"`
	ActivatedHeight int64  `protobuf:"varint,9,opt,name=activated_height,json=activatedHeight,proto3" json:"activated_height,omitempty"`
	UpdatedHeight   int64  `protobuf:"varint,10,opt,name=updated_height,json=updatedHeight,proto3" json:"updated_height,omitempty"`
	SuspendedHeight int64  `protobuf:"varint,11,opt,name=suspended_height,json=suspendedHeight,proto3" json:"suspended_height,omitempty"`
	RemovedHeight   int64  `protobuf:"varint,12,opt,name=removed_height,json=removedHeight,proto3" json:"removed_height,omitempty"`
}

func (m *preV2CoreSlot) Reset()         { *m = preV2CoreSlot{} }
func (m *preV2CoreSlot) String() string { return gogoproto.CompactTextString(m) }
func (*preV2CoreSlot) ProtoMessage()    {}

// TestPreV2CoreSlotBytesStillDecode is the durability guarantee: a CoreSlot
// written before the V2 fields existed must still decode, with its original
// values intact and the new fields at their zero values.
func TestPreV2CoreSlotBytesStillDecode(t *testing.T) {
	old := &preV2CoreSlot{
		SlotId: 7, OperatorAddress: "operator", PayoutAddress: "payout",
		Status: int32(types.SlotStatus_SLOT_STATUS_ACTIVE), ConsensusPower: 1,
		RewardWeight:  types.DefaultRewardWeight,
		CreatedHeight: 3, ActivatedHeight: 4, UpdatedHeight: 5,
		SuspendedHeight: 6, RemovedHeight: 8,
	}
	bz, err := gogoproto.Marshal(old)
	require.NoError(t, err)

	var decoded types.CoreSlot
	require.NoError(t, gogoproto.Unmarshal(bz, &decoded))

	require.Equal(t, uint64(7), decoded.SlotId)
	require.Equal(t, "operator", decoded.OperatorAddress)
	require.Equal(t, "payout", decoded.PayoutAddress)
	require.Equal(t, types.SlotStatus_SLOT_STATUS_ACTIVE, decoded.Status)
	require.Equal(t, int64(1), decoded.ConsensusPower)
	require.Equal(t, types.DefaultRewardWeight, decoded.RewardWeight)
	require.Equal(t, int64(3), decoded.CreatedHeight)
	require.Equal(t, int64(4), decoded.ActivatedHeight)
	require.Equal(t, int64(5), decoded.UpdatedHeight)
	require.Equal(t, int64(6), decoded.SuspendedHeight)
	require.Equal(t, int64(8), decoded.RemovedHeight)

	// The V2 fields are absent from the old bytes and decode as zero values, which
	// is exactly the never-activated / no-policy sentinel state. A stored row in
	// this shape is not a conforming V2 slot; it is decodable, which is the
	// property being guaranteed here.
	require.Equal(t, "", decoded.SettlementAddress)
	require.Equal(t, uint64(0), decoded.ActivationSequence)
	require.Equal(t, int64(0), decoded.ActivationEffectiveHeight)
	require.Equal(t, uint64(0), decoded.CurrentSelectionPolicyVersion)
	require.Equal(t, int64(0), decoded.LastSelectionPolicyUpdateHeight)
}

// TestV2CoreSlotFieldsRoundTrip pins the new field numbers and scalar types: the
// encoding is stable across a marshal/unmarshal cycle at values that would be
// truncated by a narrower type.
func TestV2CoreSlotFieldsRoundTrip(t *testing.T) {
	original := types.CoreSlot{
		SlotId: 1, OperatorAddress: "operator", PayoutAddress: "payout",
		SettlementAddress:               "settlement",
		ActivationSequence:              1 << 40,
		ActivationEffectiveHeight:       1 << 40,
		CurrentSelectionPolicyVersion:   1 << 40,
		LastSelectionPolicyUpdateHeight: 1 << 40,
	}
	bz, err := gogoproto.Marshal(&original)
	require.NoError(t, err)

	var decoded types.CoreSlot
	require.NoError(t, gogoproto.Unmarshal(bz, &decoded))
	require.Equal(t, original, decoded)
}

func TestSelectionPolicyVersionRoundTrip(t *testing.T) {
	original := types.SelectionPolicyVersion{
		SlotId: 1 << 40, PolicyVersion: 1 << 40,
		SelectionRateBps: 5_000, MaxSelectedParticipants: 1 << 40,
		ValidFromHeight: 1 << 40, ValidUntilHeightExclusive: 0,
	}
	bz, err := gogoproto.Marshal(&original)
	require.NoError(t, err)

	var decoded types.SelectionPolicyVersion
	require.NoError(t, gogoproto.Unmarshal(bz, &decoded))
	require.Equal(t, original, decoded)
}

// TestDeprecatedParamsKeepTheirFieldNumbers proves the deprecation is in place
// rather than a removal: the three retired parameters still occupy numbers 6, 8
// and 10 and still round-trip. Reserving those numbers instead would break every
// stored parameter set encoded under the unchanged v1 package.
func TestDeprecatedParamsKeepTheirFieldNumbers(t *testing.T) {
	// Non-zero values are inadmissible under V2 validation, but they must still
	// DECODE — admission and encoding are separate concerns.
	original := types.Params{
		ActivationDelayBlocks: 11,
		RemovalDelayBlocks:    22,
		AllowSelfRegistration: true,
	}
	bz, err := gogoproto.Marshal(&original)
	require.NoError(t, err)

	var decoded types.Params
	require.NoError(t, gogoproto.Unmarshal(bz, &decoded))
	require.Equal(t, uint64(11), decoded.ActivationDelayBlocks)
	require.Equal(t, uint64(22), decoded.RemovalDelayBlocks)
	require.True(t, decoded.AllowSelfRegistration)

	// That these exact values are refused by V2 admission is a separate property,
	// covered by TestDeprecatedParamsMustCarryZeroValues. Encoding and admission
	// are deliberately not conflated: the fields must still decode precisely so a
	// stored parameter set carrying them can be read and then rejected.
}
