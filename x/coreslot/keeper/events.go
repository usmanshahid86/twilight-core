package keeper

import (
	"context"
	"strconv"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/x/coreslot/types"
)

func u64(v uint64) string { return strconv.FormatUint(v, 10) }
func i64(v int64) string  { return strconv.FormatInt(v, 10) }

// emit writes a typed event onto the (possibly cached) context's event manager.
func emit(ctx context.Context, evtType string, attrs ...sdk.Attribute) {
	sdk.UnwrapSDKContext(ctx).EventManager().EmitEvent(sdk.NewEvent(evtType, attrs...))
}

func emitRegistered(ctx context.Context, slotID uint64, operator, consAddr string) {
	emit(ctx, types.EventTypeRegistered,
		sdk.NewAttribute(types.AttributeKeySlotID, u64(slotID)),
		sdk.NewAttribute(types.AttributeKeyOperatorAddress, operator),
		sdk.NewAttribute(types.AttributeKeyConsensusAddress, consAddr),
		sdk.NewAttribute(types.AttributeKeyNewStatus, types.SlotStatus_SLOT_STATUS_PENDING.String()),
	)
}

func emitActivated(ctx context.Context, slotID uint64, operator string, oldStatus types.SlotStatus, consAddr string, power int64) {
	emit(ctx, types.EventTypeActivated,
		sdk.NewAttribute(types.AttributeKeySlotID, u64(slotID)),
		sdk.NewAttribute(types.AttributeKeyOperatorAddress, operator),
		sdk.NewAttribute(types.AttributeKeyOldStatus, oldStatus.String()),
		sdk.NewAttribute(types.AttributeKeyNewStatus, types.SlotStatus_SLOT_STATUS_ACTIVE.String()),
		sdk.NewAttribute(types.AttributeKeyConsensusAddress, consAddr),
		sdk.NewAttribute(types.AttributeKeyPower, i64(power)),
	)
}

func emitInactivated(ctx context.Context, slotID uint64, operator, consAddr string, oldStatus types.SlotStatus, reason string) {
	emit(ctx, types.EventTypeInactivated,
		sdk.NewAttribute(types.AttributeKeySlotID, u64(slotID)),
		sdk.NewAttribute(types.AttributeKeyOperatorAddress, operator),
		sdk.NewAttribute(types.AttributeKeyConsensusAddress, consAddr),
		sdk.NewAttribute(types.AttributeKeyOldStatus, oldStatus.String()),
		sdk.NewAttribute(types.AttributeKeyNewStatus, types.SlotStatus_SLOT_STATUS_INACTIVE.String()),
		sdk.NewAttribute(types.AttributeKeyPower, i64(0)),
		sdk.NewAttribute(types.AttributeKeyReason, reason),
	)
}

func emitSuspended(ctx context.Context, slotID uint64, operator, consAddr string, oldStatus types.SlotStatus, reason string) {
	emit(ctx, types.EventTypeSuspended,
		sdk.NewAttribute(types.AttributeKeySlotID, u64(slotID)),
		sdk.NewAttribute(types.AttributeKeyOperatorAddress, operator),
		sdk.NewAttribute(types.AttributeKeyConsensusAddress, consAddr),
		sdk.NewAttribute(types.AttributeKeyOldStatus, oldStatus.String()),
		sdk.NewAttribute(types.AttributeKeyNewStatus, types.SlotStatus_SLOT_STATUS_SUSPENDED.String()),
		sdk.NewAttribute(types.AttributeKeyPower, i64(0)),
		sdk.NewAttribute(types.AttributeKeyReason, reason),
	)
}

func emitRemoved(ctx context.Context, slotID uint64, operator string, oldStatus types.SlotStatus, consAddr, reason string) {
	emit(ctx, types.EventTypeRemoved,
		sdk.NewAttribute(types.AttributeKeySlotID, u64(slotID)),
		sdk.NewAttribute(types.AttributeKeyOperatorAddress, operator),
		sdk.NewAttribute(types.AttributeKeyOldStatus, oldStatus.String()),
		sdk.NewAttribute(types.AttributeKeyNewStatus, types.SlotStatus_SLOT_STATUS_REMOVED.String()),
		sdk.NewAttribute(types.AttributeKeyConsensusAddress, consAddr),
		sdk.NewAttribute(types.AttributeKeyReason, reason),
	)
}

func emitKeyRotationRequested(ctx context.Context, slotID uint64, operator, oldConsAddr, newConsAddr string, effectiveHeight int64) {
	emit(ctx, types.EventTypeKeyRotationRequested,
		sdk.NewAttribute(types.AttributeKeySlotID, u64(slotID)),
		sdk.NewAttribute(types.AttributeKeyOperatorAddress, operator),
		sdk.NewAttribute(types.AttributeKeyOldConsensusAddress, oldConsAddr),
		sdk.NewAttribute(types.AttributeKeyNewConsensusAddress, newConsAddr),
		sdk.NewAttribute(types.AttributeKeyEffectiveHeight, i64(effectiveHeight)),
	)
}

func emitKeyRotated(ctx context.Context, slotID uint64, operator, oldConsAddr, newConsAddr string, power, effectiveHeight int64) {
	emit(ctx, types.EventTypeKeyRotated,
		sdk.NewAttribute(types.AttributeKeySlotID, u64(slotID)),
		sdk.NewAttribute(types.AttributeKeyOperatorAddress, operator),
		sdk.NewAttribute(types.AttributeKeyOldConsensusAddress, oldConsAddr),
		sdk.NewAttribute(types.AttributeKeyNewConsensusAddress, newConsAddr),
		sdk.NewAttribute(types.AttributeKeyPower, i64(power)),
		sdk.NewAttribute(types.AttributeKeyEffectiveHeight, i64(effectiveHeight)),
	)
}

func emitPayoutUpdated(ctx context.Context, slotID uint64, operator string) {
	emit(ctx, types.EventTypePayoutUpdated,
		sdk.NewAttribute(types.AttributeKeySlotID, u64(slotID)),
		sdk.NewAttribute(types.AttributeKeyOperatorAddress, operator),
	)
}

// emitSettlementUpdated announces a settlement-credential replacement. The
// address itself is deliberately not an attribute: the event marks that the
// authorizing credential changed, and the current value is readable from the slot
// record.
func emitSettlementUpdated(ctx context.Context, slotID uint64, operator string) {
	emit(ctx, types.EventTypeSettlementUpdated,
		sdk.NewAttribute(types.AttributeKeySlotID, u64(slotID)),
		sdk.NewAttribute(types.AttributeKeyOperatorAddress, operator),
	)
}

// emitSelectionPolicyUpdated announces a new Selection policy version. It stays
// compact deliberately: the resulting version and the height it becomes effective
// are what an observer needs to locate the change, and the policy payload itself
// is read from the version the event names rather than duplicated into the event
// log.
func emitSelectionPolicyUpdated(ctx context.Context, slotID uint64, operator string, version uint64, effectiveHeight int64) {
	emit(ctx, types.EventTypeSelectionPolicyUpdated,
		sdk.NewAttribute(types.AttributeKeySlotID, u64(slotID)),
		sdk.NewAttribute(types.AttributeKeyOperatorAddress, operator),
		sdk.NewAttribute(types.AttributeKeyPolicyVersion, u64(version)),
		sdk.NewAttribute(types.AttributeKeyEffectiveHeight, i64(effectiveHeight)),
	)
}

func emitMetadataUpdated(ctx context.Context, slotID uint64, operator string) {
	emit(ctx, types.EventTypeMetadataUpdated,
		sdk.NewAttribute(types.AttributeKeySlotID, u64(slotID)),
		sdk.NewAttribute(types.AttributeKeyOperatorAddress, operator),
	)
}

func emitParamsUpdated(ctx context.Context, authority string) {
	emit(ctx, types.EventTypeParamsUpdated,
		sdk.NewAttribute(types.AttributeKeyAuthority, authority),
	)
}

func emitValidatorUpdateEmitted(ctx context.Context, slotID uint64, operator, consAddr string, power, height int64) {
	emit(ctx, types.EventTypeValidatorUpdateEmitted,
		sdk.NewAttribute(types.AttributeKeySlotID, u64(slotID)),
		sdk.NewAttribute(types.AttributeKeyOperatorAddress, operator),
		sdk.NewAttribute(types.AttributeKeyConsensusAddress, consAddr),
		sdk.NewAttribute(types.AttributeKeyPower, i64(power)),
		sdk.NewAttribute(types.AttributeKeyHeight, i64(height)),
	)
}

func emitRotationCanceled(ctx context.Context, slotID uint64, operator, oldConsAddr, newConsAddr, reason string, height int64) {
	emit(ctx, types.EventTypeRotationCanceled,
		sdk.NewAttribute(types.AttributeKeySlotID, u64(slotID)),
		sdk.NewAttribute(types.AttributeKeyOperatorAddress, operator),
		sdk.NewAttribute(types.AttributeKeyOldConsensusAddress, oldConsAddr),
		sdk.NewAttribute(types.AttributeKeyNewConsensusAddress, newConsAddr),
		sdk.NewAttribute(types.AttributeKeyReason, reason),
		sdk.NewAttribute(types.AttributeKeyHeight, i64(height)),
	)
}

// emitUpgradeScheduled announces a coordinated halt.
//
// The event is observability, never the authority: x/upgrade holds the plan, and
// an operator preparing for a halt should read it from there. This exists so a
// scheduling decision is visible in the block that made it, alongside every other
// authority action.
func emitUpgradeScheduled(ctx context.Context, authority, name string, height int64, info string) {
	emit(ctx, types.EventTypeUpgradeScheduled,
		sdk.NewAttribute(types.AttributeKeyAuthority, authority),
		sdk.NewAttribute(types.AttributeKeyUpgradeName, name),
		sdk.NewAttribute(types.AttributeKeyUpgradeHeight, i64(height)),
		sdk.NewAttribute(types.AttributeKeyUpgradeInfo, info),
	)
}

// emitUpgradeCanceled names the plan that was actually withdrawn, so the event
// records which halt was called off rather than only that someone called one off.
func emitUpgradeCanceled(ctx context.Context, authority, name string) {
	emit(ctx, types.EventTypeUpgradeCanceled,
		sdk.NewAttribute(types.AttributeKeyAuthority, authority),
		sdk.NewAttribute(types.AttributeKeyUpgradeName, name),
	)
}

// The nomination event is the audit record of who nominated whom. The pending
// state deliberately does not store the nominating address — the incumbent is
// still in Params until acceptance, so persisting it would create a second copy
// of a value the chain already holds.
func emitAuthorityNominated(ctx context.Context, role types.AuthorityRole, authority, nominee string) {
	emit(ctx, types.EventTypeAuthorityNominated,
		sdk.NewAttribute(types.AttributeKeyAuthorityRole, role.String()),
		sdk.NewAttribute(types.AttributeKeyAuthority, authority),
		sdk.NewAttribute(types.AttributeKeyNominee, nominee),
	)
}

func emitAuthorityAccepted(ctx context.Context, role types.AuthorityRole, previous, nominee string) {
	emit(ctx, types.EventTypeAuthorityAccepted,
		sdk.NewAttribute(types.AttributeKeyAuthorityRole, role.String()),
		sdk.NewAttribute(types.AttributeKeyPreviousAuthority, previous),
		sdk.NewAttribute(types.AttributeKeyAuthority, nominee),
	)
}

func emitAuthorityNominationCancelled(ctx context.Context, role types.AuthorityRole, authority, nominee string) {
	emit(ctx, types.EventTypeAuthorityNominationCancelled,
		sdk.NewAttribute(types.AttributeKeyAuthorityRole, role.String()),
		sdk.NewAttribute(types.AttributeKeyAuthority, authority),
		sdk.NewAttribute(types.AttributeKeyNominee, nominee),
	)
}
