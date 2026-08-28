package keeper

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/x/coreslot/types"
)

// Authority rotation is a two-step handover: the incumbent nominates, and the
// nominee accepts by signing with the key it must actually hold.
//
// The single-step form it replaces wrote the whole Params struct, authority
// field included, checked only against the current authority and validated only
// as well-formed bech32. So one transaction could hand the role to any address
// that parsed — an old key, a payout address pasted from the wrong line, a
// module account nobody can sign for — and nothing could undo it. The loss is
// total rather than partial: Params.Authority gates validator admission,
// parameter updates, and the upgrade path, which ScheduleUpgrade reaches by
// reading this same field from state. A chain that loses it cannot even upgrade
// its way out.
//
// What this mechanism actually provides:
//
//   - proof that the destination key exists and is controlled, because only the
//     nominee can complete the handover
//   - refusal of module-account, bank-blocked and all-zero destinations
//   - separation of routine parameter editing from authority rotation, so an
//     unrelated max_active_slots edit cannot end governance
//   - a window in which a mistaken nomination can be cancelled or replaced
//
// What it does NOT provide, and must not be described as providing: protection
// against an attacker who already holds the incumbent key. There is no timelock,
// so such an attacker nominates an address they control and accepts immediately.
// Defenders are guaranteed no reaction window. A delay is a separate decision
// with its own cost to emergency response, and is a stated non-goal of #130.

// authorityRoleKey returns the collection key for a role, refusing anything
// outside the two defined operational roles.
//
// The unspecified zero value is refused rather than defaulted. A message that
// left the field unset would otherwise rotate the primary authority, which is
// the most consequential of the two and the one least likely to have been meant
// by omission.
func authorityRoleKey(role types.AuthorityRole) (int32, error) {
	switch role {
	case types.AuthorityRole_AUTHORITY_ROLE_PRIMARY, types.AuthorityRole_AUTHORITY_ROLE_EMERGENCY:
		return int32(role), nil
	default:
		return 0, types.ErrInvalidAuthorityRole.Wrapf("%s", role)
	}
}

// currentHolder returns the address currently holding a role.
func currentHolder(params types.Params, role types.AuthorityRole) string {
	if role == types.AuthorityRole_AUTHORITY_ROLE_EMERGENCY {
		return params.EmergencyAuthority
	}
	return params.Authority
}

// setHolder returns params with only the named role replaced. Every other field
// is carried through untouched: acceptance rotates one role, and must not become
// an incidental parameter write.
func setHolder(params types.Params, role types.AuthorityRole, addr string) types.Params {
	if role == types.AuthorityRole_AUTHORITY_ROLE_EMERGENCY {
		params.EmergencyAuthority = addr
		return params
	}
	params.Authority = addr
	return params
}

// NominateAuthority records a successor for one role without changing who holds
// it. The incumbent keeps every capability until the nominee accepts.
func (m msgServer) NominateAuthority(
	ctx context.Context, msg *types.MsgNominateAuthority,
) (*types.MsgNominateAuthorityResponse, error) {
	key, err := authorityRoleKey(msg.Role)
	if err != nil {
		return nil, err
	}
	params, err := m.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	// Only the holder of THIS role may nominate for it. The primary authority
	// cannot nominate a successor to the emergency role, and the reverse, because
	// either would let one role quietly absorb the other.
	if msg.Authority != currentHolder(params, msg.Role) {
		return nil, types.ErrUnauthorized
	}
	// The same canonical rule the chain already applies to payout and settlement
	// destinations. It refuses module accounts, bank-blocked addresses and the
	// all-zero address — each a well-formed encoding no key controls, and so each
	// a permanent loss of the capability this role gates.
	//
	// Note this rule is NOT applied to the authority fields at genesis: the
	// default genesis deliberately seeds them with module addresses as
	// placeholders, and would fail its own check. It is a rule about rotation
	// destinations, which is where the irreversible loss happens.
	if _, err := m.economicAddresses.Validate(msg.Nominee); err != nil {
		return nil, types.ErrInvalidAddress.Wrapf("nominee: %v", err)
	}
	// A nomination naming the incumbent is refused as a no-op rather than stored.
	// Accepting it would create a pending record whose completion changes nothing
	// while implying a handover is in flight.
	if msg.Nominee == currentHolder(params, msg.Role) {
		return nil, types.ErrInvalidAddress.Wrap("nominee already holds this role")
	}
	// Replaces any existing nomination for this role. The incumbent still holds
	// the role, so it may change its mind; the displaced nominee simply finds
	// nothing to accept.
	if err := m.PendingAuthority.Set(ctx, key, types.PendingAuthorityTransfer{
		Nominee:         msg.Nominee,
		NominatedHeight: sdk.UnwrapSDKContext(ctx).BlockHeight(),
	}); err != nil {
		return nil, err
	}
	emitAuthorityNominated(ctx, msg.Role, msg.Authority, msg.Nominee)
	return &types.MsgNominateAuthorityResponse{}, nil
}

// AcceptAuthority completes a handover. The signer must be the nominee, which is
// the whole point: a wrong-but-valid address can never sign, so a typo fails
// harmlessly instead of ending governance.
func (m msgServer) AcceptAuthority(
	ctx context.Context, msg *types.MsgAcceptAuthority,
) (*types.MsgAcceptAuthorityResponse, error) {
	key, err := authorityRoleKey(msg.Role)
	if err != nil {
		return nil, err
	}
	pending, err := m.PendingAuthority.Get(ctx, key)
	if err != nil {
		// Covers accepted, cancelled and never-nominated alike. Distinguishing
		// them would tell an unauthorized caller which rotations are in flight.
		return nil, types.ErrNoPendingNomination.Wrapf("%s", msg.Role)
	}
	if msg.Nominee != pending.Nominee {
		return nil, types.ErrUnauthorized
	}
	// Re-validated at acceptance, not only at nomination. A nomination can outlive
	// a binary upgrade, and the set of inadmissible addresses is app-derived — a
	// module account added since, or an address newly bank-blocked, must not be
	// installed because it was admissible when it was named.
	if _, err := m.economicAddresses.Validate(msg.Nominee); err != nil {
		return nil, types.ErrInvalidAddress.Wrapf("nominee: %v", err)
	}
	params, err := m.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	previous := currentHolder(params, msg.Role)
	if err := m.Params.Set(ctx, setHolder(params, msg.Role, msg.Nominee)); err != nil {
		return nil, err
	}
	// Cleared in the same handler as the write, so a completed handover can never
	// leave a nomination that a replaced nominee could still act on.
	if err := m.PendingAuthority.Remove(ctx, key); err != nil {
		return nil, err
	}
	emitAuthorityAccepted(ctx, msg.Role, previous, msg.Nominee)
	return &types.MsgAcceptAuthorityResponse{}, nil
}

// CancelAuthorityNomination withdraws a pending nomination, which is what makes
// a mistaken one correctable rather than merely inert.
func (m msgServer) CancelAuthorityNomination(
	ctx context.Context, msg *types.MsgCancelAuthorityNomination,
) (*types.MsgCancelAuthorityNominationResponse, error) {
	key, err := authorityRoleKey(msg.Role)
	if err != nil {
		return nil, err
	}
	params, err := m.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	if msg.Authority != currentHolder(params, msg.Role) {
		return nil, types.ErrUnauthorized
	}
	pending, err := m.PendingAuthority.Get(ctx, key)
	if err != nil {
		return nil, types.ErrNoPendingNomination.Wrapf("%s", msg.Role)
	}
	if err := m.PendingAuthority.Remove(ctx, key); err != nil {
		return nil, err
	}
	emitAuthorityNominationCancelled(ctx, msg.Role, msg.Authority, pending.Nominee)
	return &types.MsgCancelAuthorityNominationResponse{}, nil
}
