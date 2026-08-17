package types_test

import (
	"bytes"
	"compress/gzip"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	gogoproto "github.com/cosmos/gogoproto/proto"
	descriptorpb "github.com/cosmos/gogoproto/protoc-gen-gogo/descriptor"
	anypb "github.com/cosmos/gogoproto/types/any"

	"github.com/twilight-project/twilight-core/x/coreslot/types"
)

// Wire-compatibility guarantees for the additive V2 evolution of the v1 API.
//
// The protobuf package is unchanged, so every field number and scalar type
// already in use is permanent. These tests fail if one is renumbered, retyped or
// removed — including the deprecated parameters, which keep their numbers rather
// than being reserved.

// fieldLedger is the authoritative record of a message's wire schema: field
// number -> "name:kind". Every entry here is permanent. A test that only
// round-trips values would pass through a renumbering as long as both sides moved
// together; comparing the descriptor against a written-down ledger cannot.
type fieldLedger map[int32]string

// readFieldLedger derives the ledger from the compiled descriptor, so it reflects
// the schema actually generated rather than a hand-copied restatement of it.
func readFieldLedger(t *testing.T, msg descriptorMessage) fieldLedger {
	t.Helper()
	gzipped, _ := msg.Descriptor()
	raw, err := decompressDescriptor(gzipped)
	require.NoError(t, err)

	var file descriptorpb.FileDescriptorProto
	require.NoError(t, gogoproto.Unmarshal(raw, &file))

	name := gogoproto.MessageName(msg)
	shortName := name[strings.LastIndex(name, ".")+1:]
	for _, message := range file.MessageType {
		if message.GetName() != shortName {
			continue
		}
		ledger := fieldLedger{}
		for _, field := range message.Field {
			kind := field.GetType().String()
			if field.GetTypeName() != "" {
				kind = field.GetTypeName()
			}
			if field.GetLabel() == descriptorpb.FieldDescriptorProto_LABEL_REPEATED {
				kind = "repeated " + kind
			}
			ledger[field.GetNumber()] = field.GetName() + ":" + kind
		}
		return ledger
	}
	t.Fatalf("message %s not found in its own file descriptor", name)
	return nil
}

type descriptorMessage interface {
	gogoproto.Message
	Descriptor() ([]byte, []int)
}

func decompressDescriptor(gzipped []byte) ([]byte, error) {
	reader, err := gzip.NewReader(bytes.NewReader(gzipped))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	return io.ReadAll(reader)
}

// TestWireLedgersArePinned is the durability proof for every message this change
// extended. It pins the pre-existing numbers and wire kinds AND the additions, so
// a renumbering, a retype, a removal or an unreviewed new number all fail here.
//
// It pairs with the decode test below, and the two prove different things. That
// test round-trips representative legacy VALUES across all of old fields 1-13,
// including the Any-typed consensus key (3) and the nested metadata message (13);
// this one pins the complete field-number and wire-kind SCHEMA, which no amount of
// value round-tripping can establish because a renumbering moves both sides
// together.
func TestWireLedgersArePinned(t *testing.T) {
	for _, tc := range []struct {
		name   string
		msg    descriptorMessage
		ledger fieldLedger
	}{
		{"CoreSlot", &types.CoreSlot{}, fieldLedger{
			// Pre-existing, unchanged.
			1:  "slot_id:TYPE_UINT64",
			2:  "operator_address:TYPE_STRING",
			3:  "consensus_pubkey:.google.protobuf.Any",
			4:  "payout_address:TYPE_STRING",
			5:  "status:.twilight.coreslot.v1.SlotStatus",
			6:  "consensus_power:TYPE_INT64",
			7:  "reward_weight:TYPE_STRING",
			8:  "created_height:TYPE_INT64",
			9:  "activated_height:TYPE_INT64",
			10: "updated_height:TYPE_INT64",
			11: "suspended_height:TYPE_INT64",
			12: "removed_height:TYPE_INT64",
			13: "metadata:.twilight.coreslot.v1.OperatorMetadata",
			// Added by this change.
			14: "settlement_address:TYPE_STRING",
			15: "activation_sequence:TYPE_UINT64",
			16: "activation_effective_height:TYPE_INT64",
			17: "current_selection_policy_version:TYPE_UINT64",
			18: "last_selection_policy_update_height:TYPE_INT64",
		}},
		{"GenesisState", &types.GenesisState{}, fieldLedger{
			1: "params:.twilight.coreslot.v1.Params",
			2: "slots:repeated .twilight.coreslot.v1.CoreSlot",
			3: "pending_key_rotations:repeated .twilight.coreslot.v1.PendingKeyRotation",
			4: "reserved_consensus_addresses:repeated .twilight.coreslot.v1.ReservedConsensusAddress",
			5: "reward_weights:repeated .twilight.coreslot.v1.OperatorRewardWeight",
			6: "last_applied_validators:repeated .twilight.coreslot.v1.LastAppliedValidator",
			7: "next_slot_id:TYPE_UINT64",
			8: "selection_policies:repeated .twilight.coreslot.v1.SelectionPolicyVersion",
		}},
		{"MsgRegisterCoreSlot", &types.MsgRegisterCoreSlot{}, fieldLedger{
			1: "authority:TYPE_STRING",
			2: "operator_address:TYPE_STRING",
			3: "consensus_pubkey:.google.protobuf.Any",
			4: "payout_address:TYPE_STRING",
			5: "metadata:.twilight.coreslot.v1.OperatorMetadata",
			6: "settlement_address:TYPE_STRING",
			7: "initial_selection_policy:.twilight.coreslot.v1.InitialSelectionPolicy",
		}},
		{"Params", &types.Params{}, fieldLedger{
			1: "authority:TYPE_STRING",
			2: "emergency_authority:TYPE_STRING",
			3: "slot_voting_power:TYPE_INT64",
			4: "min_active_slots:TYPE_UINT64",
			5: "max_active_slots:TYPE_UINT64",
			// Deprecated in place: the numbers are retained, never reserved.
			6:  "activation_delay_blocks:TYPE_UINT64",
			7:  "key_rotation_delay_blocks:TYPE_UINT64",
			8:  "removal_delay_blocks:TYPE_UINT64",
			9:  "consensus_key_reuse_lockout:TYPE_UINT64",
			10: "allow_self_registration:TYPE_BOOL",
			11: "allow_emergency_below_min_active:TYPE_BOOL",
			12: "selection_policy_update_cooldown_blocks:TYPE_UINT64",
		}},
		{"MsgUpdateSelectionPolicy", &types.MsgUpdateSelectionPolicy{}, fieldLedger{
			1: "operator:TYPE_STRING",
			2: "slot_id:TYPE_UINT64",
			3: "selection_rate_bps:TYPE_UINT64",
			4: "max_selected_participants:TYPE_UINT64",
		}},
		{"MsgUpdateSelectionPolicyResponse", &types.MsgUpdateSelectionPolicyResponse{}, fieldLedger{
			1: "policy_version:TYPE_UINT64",
		}},
		{"QuerySelectionPolicyRequest", &types.QuerySelectionPolicyRequest{}, fieldLedger{
			1: "slot_id:TYPE_UINT64",
		}},
		{"QuerySelectionPolicyVersionRequest", &types.QuerySelectionPolicyVersionRequest{}, fieldLedger{
			1: "slot_id:TYPE_UINT64",
			2: "policy_version:TYPE_UINT64",
		}},
		{"QuerySelectionPolicyAtHeightRequest", &types.QuerySelectionPolicyAtHeightRequest{}, fieldLedger{
			1: "slot_id:TYPE_UINT64",
			2: "at_height:TYPE_INT64",
		}},
		{"QuerySelectionPolicyResponse", &types.QuerySelectionPolicyResponse{}, fieldLedger{
			1: "policy:.twilight.coreslot.v1.SelectionPolicyVersion",
		}},
		{"SelectionPolicyVersion", &types.SelectionPolicyVersion{}, fieldLedger{
			1: "slot_id:TYPE_UINT64",
			2: "policy_version:TYPE_UINT64",
			3: "selection_rate_bps:TYPE_UINT64",
			4: "max_selected_participants:TYPE_UINT64",
			5: "valid_from_height:TYPE_INT64",
			6: "valid_until_height_exclusive:TYPE_INT64",
		}},
		{"InitialSelectionPolicy", &types.InitialSelectionPolicy{}, fieldLedger{
			1: "selection_rate_bps:TYPE_UINT64",
			2: "max_selected_participants:TYPE_UINT64",
		}},
		{"MsgUpdateSettlementAddress", &types.MsgUpdateSettlementAddress{}, fieldLedger{
			1: "operator:TYPE_STRING",
			2: "slot_id:TYPE_UINT64",
			3: "settlement_address:TYPE_STRING",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.ledger, readFieldLedger(t, tc.msg))
		})
	}
}

// TestDeprecatedParamsAreMarkedDeprecated pins the deprecation itself, which the
// number ledger alone cannot express: the fields must remain present AND carry
// the deprecated option, so tooling reports them as retired rather than absent.
func TestDeprecatedParamsAreMarkedDeprecated(t *testing.T) {
	gzipped, _ := (&types.Params{}).Descriptor()
	raw, err := decompressDescriptor(gzipped)
	require.NoError(t, err)
	var file descriptorpb.FileDescriptorProto
	require.NoError(t, gogoproto.Unmarshal(raw, &file))

	deprecated := map[int32]string{}
	for _, message := range file.MessageType {
		if message.GetName() != "Params" {
			continue
		}
		for _, field := range message.Field {
			if field.Options.GetDeprecated() {
				deprecated[field.GetNumber()] = field.GetName()
			}
		}
	}
	require.Equal(t, map[int32]string{
		6:  "activation_delay_blocks",
		8:  "removal_delay_blocks",
		10: "allow_self_registration",
	}, deprecated)
}

// preV2CoreSlot is a CoreSlot as it was encoded before this change: fields 1-13
// only. It is declared as its own message rather than built from the current
// type, so the bytes under test cannot silently follow a schema change.
// Field names mirror the GENERATED type this shadows, initialism warts and all
// (SlotId, not SlotID). That correspondence is the point: a reader comparing this
// struct against the real CoreSlot must be able to do it field by field, and a
// tidied name here would be one more difference to discount.
//
//nolint:staticcheck // ST1003: deliberately mirrors generated protobuf field names.
type preV2CoreSlot struct {
	SlotId          uint64                  `protobuf:"varint,1,opt,name=slot_id,json=slotId,proto3" json:"slot_id,omitempty"`
	OperatorAddress string                  `protobuf:"bytes,2,opt,name=operator_address,json=operatorAddress,proto3" json:"operator_address,omitempty"`
	ConsensusPubkey *anypb.Any              `protobuf:"bytes,3,opt,name=consensus_pubkey,json=consensusPubkey,proto3" json:"consensus_pubkey,omitempty"`
	PayoutAddress   string                  `protobuf:"bytes,4,opt,name=payout_address,json=payoutAddress,proto3" json:"payout_address,omitempty"`
	Status          int32                   `protobuf:"varint,5,opt,name=status,proto3" json:"status,omitempty"`
	ConsensusPower  int64                   `protobuf:"varint,6,opt,name=consensus_power,json=consensusPower,proto3" json:"consensus_power,omitempty"`
	RewardWeight    string                  `protobuf:"bytes,7,opt,name=reward_weight,json=rewardWeight,proto3" json:"reward_weight,omitempty"`
	CreatedHeight   int64                   `protobuf:"varint,8,opt,name=created_height,json=createdHeight,proto3" json:"created_height,omitempty"`
	ActivatedHeight int64                   `protobuf:"varint,9,opt,name=activated_height,json=activatedHeight,proto3" json:"activated_height,omitempty"`
	UpdatedHeight   int64                   `protobuf:"varint,10,opt,name=updated_height,json=updatedHeight,proto3" json:"updated_height,omitempty"`
	SuspendedHeight int64                   `protobuf:"varint,11,opt,name=suspended_height,json=suspendedHeight,proto3" json:"suspended_height,omitempty"`
	RemovedHeight   int64                   `protobuf:"varint,12,opt,name=removed_height,json=removedHeight,proto3" json:"removed_height,omitempty"`
	Metadata        *types.OperatorMetadata `protobuf:"bytes,13,opt,name=metadata,proto3" json:"metadata,omitempty"`
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
		ConsensusPubkey: &anypb.Any{TypeUrl: "/cosmos.crypto.ed25519.PubKey", Value: []byte{1, 2, 3}},
		Status:          int32(types.SlotStatus_SLOT_STATUS_ACTIVE), ConsensusPower: 1,
		RewardWeight:  types.DefaultRewardWeight,
		CreatedHeight: 3, ActivatedHeight: 4, UpdatedHeight: 5,
		SuspendedHeight: 6, RemovedHeight: 8,
		Metadata: &types.OperatorMetadata{Moniker: "legacy", Details: "all original fields retained"},
	}
	bz, err := gogoproto.Marshal(old)
	require.NoError(t, err)

	var decoded types.CoreSlot
	require.NoError(t, gogoproto.Unmarshal(bz, &decoded))

	require.Equal(t, uint64(7), decoded.SlotId)
	require.Equal(t, "operator", decoded.OperatorAddress)
	require.True(t, gogoproto.Equal(old.ConsensusPubkey, decoded.ConsensusPubkey))
	require.Equal(t, "payout", decoded.PayoutAddress)
	require.Equal(t, types.SlotStatus_SLOT_STATUS_ACTIVE, decoded.Status)
	require.Equal(t, int64(1), decoded.ConsensusPower)
	require.Equal(t, types.DefaultRewardWeight, decoded.RewardWeight)
	require.Equal(t, int64(3), decoded.CreatedHeight)
	require.Equal(t, int64(4), decoded.ActivatedHeight)
	require.Equal(t, int64(5), decoded.UpdatedHeight)
	require.Equal(t, int64(6), decoded.SuspendedHeight)
	require.Equal(t, int64(8), decoded.RemovedHeight)
	require.True(t, gogoproto.Equal(old.Metadata, decoded.Metadata))

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
