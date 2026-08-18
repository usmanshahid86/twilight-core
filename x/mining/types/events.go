package types

// Events emitted by x/mining.
//
// Events are an accelerator here and never a correctness requirement. The
// architecture requires the settlement worker to be able to lose every event,
// restart, query committed chain state, determine exactly what operation is next,
// and continue without operator intervention — so nothing an event carries may be
// the only way to learn it. Each attribute below is readable from state.
const (
	EventTypeSettlementChunkSubmitted = "mining_settlement_chunk_submitted"
)

// Event attribute keys.
const (
	AttributeKeySlotID         = "slot_id"
	AttributeKeyEpoch          = "epoch"
	AttributeKeyChunkIndex     = "chunk_index"
	AttributeKeyNextChunkIndex = "next_chunk_index"
	AttributeKeyRecipientCount = "recipient_count"
	AttributeKeyChunkTotal     = "chunk_total"
)

// Finalization events.
const (
	EventTypeSettlementFinalized = "mining_settlement_finalized"
)

// Finalization attribute keys.
const (
	AttributeKeyFinalizationReason = "finalization_reason"
	AttributeKeyReleasedRemainder  = "released_remainder"
	AttributeKeyFinalizedHeight    = "finalized_height"
)
