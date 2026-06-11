package types

const (
	EventTypeClaimRewards  = "rewards_claimed"
	EventTypeParamsUpdated = "rewards_params_updated"
	EventTypePaused        = "rewards_paused"
	EventTypeResumed       = "rewards_resumed"
)

const (
	AttributeKeySigner        = "signer"
	AttributeKeySlotID        = "slot_id"
	AttributeKeyStartEpoch    = "start_epoch"
	AttributeKeyEndEpoch      = "end_epoch"
	AttributeKeyAmount        = "amount"
	AttributeKeyPayoutAddress = "payout_address"
	AttributeKeyAuthority     = "authority"
)
