package types

// Event types emitted by x/coreslot. Validator-affecting changes are only ever
// applied in EndBlock, so the consensus-facing events
// (coreslot_validator_update_emitted, coreslot_key_rotated) originate there.
const (
	EventTypeRegistered             = "coreslot_registered"
	EventTypeActivated              = "coreslot_activated"
	EventTypeInactivated            = "coreslot_inactivated"
	EventTypeSuspended              = "coreslot_suspended"
	EventTypeRemoved                = "coreslot_removed"
	EventTypeKeyRotationRequested   = "coreslot_key_rotation_requested"
	EventTypeKeyRotated             = "coreslot_key_rotated"
	EventTypePayoutUpdated          = "coreslot_payout_updated"
	EventTypeMetadataUpdated        = "coreslot_metadata_updated"
	EventTypeSettlementUpdated      = "coreslot_settlement_updated"
	EventTypeSelectionPolicyUpdated = "coreslot_selection_policy_updated"
	EventTypeParamsUpdated          = "coreslot_params_updated"
	EventTypeUpgradeScheduled       = "coreslot_upgrade_scheduled"
	EventTypeUpgradeCanceled        = "coreslot_upgrade_canceled"
	EventTypeValidatorUpdateEmitted = "coreslot_validator_update_emitted"
	EventTypeRotationCanceled       = "coreslot_rotation_canceled"
)

// Event attribute keys.
const (
	AttributeKeySlotID              = "slot_id"
	AttributeKeyOperatorAddress     = "operator_address"
	AttributeKeyOldStatus           = "old_status"
	AttributeKeyNewStatus           = "new_status"
	AttributeKeyConsensusAddress    = "consensus_address"
	AttributeKeyOldConsensusAddress = "old_consensus_address"
	AttributeKeyNewConsensusAddress = "new_consensus_address"
	AttributeKeyPower               = "power"
	AttributeKeyHeight              = "height"
	AttributeKeyEffectiveHeight     = "effective_height"
	AttributeKeyPolicyVersion       = "policy_version"
	AttributeKeyReason              = "reason"
	AttributeKeyAuthority           = "authority"
	AttributeKeyUpgradeName         = "upgrade_name"
	AttributeKeyUpgradeHeight       = "upgrade_height"
	AttributeKeyUpgradeInfo         = "upgrade_info"
)

// Reason values used on coreslot_rotation_canceled.
const (
	RotationCancelReasonLifecycle = "lifecycle_change"
	RotationCancelReasonStale     = "stale_rotation"
)
