package types

const (
	EventTypeEpochFinalized     = "epoch_finalized"
	EventTypeClaimRewards       = "reward_claimed"
	EventTypeParamsUpdateQueued = "params_update_queued"
	EventTypeParamsActivated    = "params_activated"
	EventTypePaused             = "rewards_paused"
	EventTypeResumed            = "rewards_resumed"
	EventTypeTreasuryPaid       = "treasury_paid"
)

const (
	AttributeKeySigner             = "signer"
	AttributeKeySlotID             = "slot_id"
	AttributeKeyStartEpoch         = "start_epoch"
	AttributeKeyEndEpoch           = "end_epoch"
	AttributeKeyAmount             = "amount"
	AttributeKeyPayoutAddress      = "payout_address"
	AttributeKeyPayoutCount        = "payout_count"
	AttributeKeyAuthority          = "authority"
	AttributeKeyEpoch              = "epoch"
	AttributeKeyStartHeight        = "start_height"
	AttributeKeyEndHeight          = "end_height"
	AttributeKeyMintedEmission     = "minted_emission"
	AttributeKeyCumulativeEmitted  = "cumulative_emitted"
	AttributeKeyRewardPool         = "reward_pool"
	AttributeKeyAllocated          = "allocated"
	AttributeKeyCarryOut           = "carry_out"
	AttributeKeyEligibleSlots      = "eligible_slots"
	AttributeKeyDistributionMethod = "distribution_method"
)
