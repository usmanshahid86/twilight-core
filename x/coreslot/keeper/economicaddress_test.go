package keeper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/twilight-project/twilight-core/x/coreslot/keeper"
	"github.com/twilight-project/twilight-core/x/coreslot/types"
)

// Canonical economic-address admission in x/coreslot (§25).
//
// The keeper does not decide what an admissible address is; it routes the
// decision through the injected app-derived validator. These tests prove the
// routing exists at every admission boundary, and that the control plane —
// authority and emergency authority, which are module accounts by design — is
// deliberately exempt.

func registerMsg(t *testing.T, authority, operator, payout string, marker byte) *types.MsgRegisterCoreSlot {
	t.Helper()
	return &types.MsgRegisterCoreSlot{
		Authority:       authority,
		OperatorAddress: operator,
		PayoutAddress:   payout,
		ConsensusPubkey: pubkey(t, marker),
	}
}

func TestRegisterCoreSlotRejectsModuleAccountAddresses(t *testing.T) {
	good := testAccount(9)
	moduleAccount := testModuleAddress(testModuleAccountName)

	cases := []struct {
		name     string
		operator string
		payout   string
	}{
		{"operator is a module account", moduleAccount, good},
		{"payout is a module account", good, moduleAccount},
		{"both are module accounts", moduleAccount, moduleAccount},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx, authority, emergency := setup(t)
			oneActiveGenesis(t, k, ctx, authority, emergency)
			msgs := keeper.NewMsgServer(k)

			_, err := msgs.RegisterCoreSlot(ctx, registerMsg(t, authority, tc.operator, tc.payout, 2))
			require.ErrorIs(t, err, types.ErrInvalidAddress)
			require.Contains(t, err.Error(), "module account")
		})
	}
}

func TestRegisterCoreSlotRejectsBankBlockedAddresses(t *testing.T) {
	good := testAccount(9)
	blocked := testAccount(77)

	cases := []struct {
		name     string
		operator string
		payout   string
	}{
		{"operator is bank-blocked", blocked, good},
		{"payout is bank-blocked", good, blocked},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// blocked is an ordinary account that only bank prohibits, so this
			// exercises the bank branch independently of module exclusion.
			k, ctx, authority, emergency := setup(t, blocked)
			oneActiveGenesis(t, k, ctx, authority, emergency)
			msgs := keeper.NewMsgServer(k)

			_, err := msgs.RegisterCoreSlot(ctx, registerMsg(t, authority, tc.operator, tc.payout, 2))
			require.ErrorIs(t, err, types.ErrInvalidAddress)
			require.Contains(t, err.Error(), "blocked")
		})
	}
}

// TestRegisterCoreSlotStillAcceptsOrdinaryAddresses guards against the rule
// being too strict: ordinary registration must be unaffected.
func TestRegisterCoreSlotStillAcceptsOrdinaryAddresses(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	oneActiveGenesis(t, k, ctx, authority, emergency)
	msgs := keeper.NewMsgServer(k)

	operator := testAccount(9)
	res, err := msgs.RegisterCoreSlot(ctx, registerMsg(t, authority, operator, testAccount(10), 2))
	require.NoError(t, err)
	require.NotZero(t, res.SlotId)

	slot, err := k.GetSlot(ctx, res.SlotId)
	require.NoError(t, err)
	require.Equal(t, operator, slot.OperatorAddress)
}

// TestRegisterCoreSlotRejectsBeforeAnyStateWrite proves the check runs ahead of
// mutation: a rejected registration must leave no slot and must not consume the
// consensus key.
func TestRegisterCoreSlotRejectsBeforeAnyStateWrite(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	oneActiveGenesis(t, k, ctx, authority, emergency)
	msgs := keeper.NewMsgServer(k)

	before, err := k.NextSlotID.Get(ctx)
	require.NoError(t, err)

	_, err = msgs.RegisterCoreSlot(ctx,
		registerMsg(t, authority, testModuleAddress(testModuleAccountName), testAccount(9), 2))
	require.Error(t, err)

	after, err := k.NextSlotID.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, before, after, "a rejected registration must not advance the slot counter")

	// The consensus key must remain available for a subsequent valid attempt.
	res, err := msgs.RegisterCoreSlot(ctx, registerMsg(t, authority, testAccount(9), testAccount(10), 2))
	require.NoError(t, err)
	require.NotZero(t, res.SlotId)
}

// TestRegisterCoreSlotChecksAuthorizationFirst keeps the ordering that stops an
// unauthorized caller probing which addresses the chain would accept.
func TestRegisterCoreSlotChecksAuthorizationFirst(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	oneActiveGenesis(t, k, ctx, authority, emergency)
	msgs := keeper.NewMsgServer(k)

	_, err := msgs.RegisterCoreSlot(ctx, registerMsg(
		t, testAccount(66), testModuleAddress(testModuleAccountName), testAccount(9), 2,
	))
	require.ErrorIs(t, err, types.ErrUnauthorized)
	require.NotErrorIs(t, err, types.ErrInvalidAddress)
}

func TestUpdatePayoutAddressRejectsInadmissibleDestinations(t *testing.T) {
	blocked := testAccount(77)

	cases := []struct {
		name       string
		newPayout  string
		wantReason string
	}{
		{"module account", testModuleAddress(testModuleAccountName), "module account"},
		{"bank-blocked", blocked, "blocked"},
		{"malformed", "not-an-address", "not a valid account address"},
		{"empty", "", "empty"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx, authority, emergency := setup(t, blocked)
			oneActiveGenesis(t, k, ctx, authority, emergency)
			msgs := keeper.NewMsgServer(k)

			operator := testAccount(9)
			res, err := msgs.RegisterCoreSlot(ctx, registerMsg(t, authority, operator, testAccount(10), 2))
			require.NoError(t, err)

			_, err = msgs.UpdatePayoutAddress(ctx, &types.MsgUpdatePayoutAddress{
				SlotId: res.SlotId, Operator: operator, NewPayoutAddress: tc.newPayout,
			})
			require.ErrorIs(t, err, types.ErrInvalidAddress)
			require.Contains(t, err.Error(), tc.wantReason)

			// The stored payout address is untouched.
			slot, err := k.GetSlot(ctx, res.SlotId)
			require.NoError(t, err)
			require.Equal(t, testAccount(10), slot.PayoutAddress)
		})
	}
}

func TestUpdatePayoutAddressAcceptsOrdinaryDestination(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	oneActiveGenesis(t, k, ctx, authority, emergency)
	msgs := keeper.NewMsgServer(k)

	operator := testAccount(9)
	res, err := msgs.RegisterCoreSlot(ctx, registerMsg(t, authority, operator, testAccount(10), 2))
	require.NoError(t, err)

	next := testAccount(11)
	_, err = msgs.UpdatePayoutAddress(ctx, &types.MsgUpdatePayoutAddress{
		SlotId: res.SlotId, Operator: operator, NewPayoutAddress: next,
	})
	require.NoError(t, err)

	slot, err := k.GetSlot(ctx, res.SlotId)
	require.NoError(t, err)
	require.Equal(t, next, slot.PayoutAddress)
}

// TestControlPlaneAuthoritiesRemainModuleAccounts is the guard against the most
// damaging way to get this wrong. Both authorities are module accounts by
// design; applying the economic rule to them would make the chain's own
// governance inadmissible.
func TestControlPlaneAuthoritiesRemainModuleAccounts(t *testing.T) {
	authority := testModuleAddress(testModuleAccountName)
	emergency := testModuleAddress(testOtherModuleAccountName)

	k, ctx, _, _ := setup(t)
	params := types.DefaultParams(authority, emergency)
	_, err := k.InitGenesis(ctx, &types.GenesisState{Params: &params, Slots: []*types.CoreSlot{
		slot(t, 1, testAccount(2), 1, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
	}, NextSlotId: 2})
	require.NoError(t, err, "module-account authorities must remain legal control-plane identities")

	stored, err := k.Params.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, authority, stored.Authority)
	require.Equal(t, emergency, stored.EmergencyAuthority)

	// And they still authorize: a registration signed by the module-account
	// authority succeeds.
	msgs := keeper.NewMsgServer(k)
	_, err = msgs.RegisterCoreSlot(ctx, registerMsg(t, authority, testAccount(9), testAccount(10), 2))
	require.NoError(t, err)
}

func TestInitGenesisRejectsInadmissibleSlotAddresses(t *testing.T) {
	blocked := testAccount(77)

	cases := []struct {
		name     string
		operator string
		payout   string
		reason   string
	}{
		{"module-account operator", testModuleAddress(testModuleAccountName), testAccount(3), "module account"},
		{"module-account payout", testAccount(2), testModuleAddress(testModuleAccountName), "module account"},
		{"bank-blocked payout", testAccount(2), blocked, "blocked"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx, authority, emergency := setup(t, blocked)
			params := types.DefaultParams(authority, emergency)

			imported := slot(t, 1, tc.operator, 1, types.SlotStatus_SLOT_STATUS_ACTIVE, 1)
			imported.PayoutAddress = tc.payout

			_, err := k.InitGenesis(ctx, &types.GenesisState{
				Params: &params, Slots: []*types.CoreSlot{imported}, NextSlotId: 2,
			})
			require.ErrorIs(t, err, types.ErrInvalidAddress)
			require.Contains(t, err.Error(), tc.reason)
		})
	}
}

// TestInitGenesisRejectsBeforeAnyWrite is the preflight property: a genesis
// whose LAST slot is inadmissible must leave nothing behind, not params and not
// the earlier slots.
func TestInitGenesisRejectsBeforeAnyWrite(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	params := types.DefaultParams(authority, emergency)

	good := slot(t, 1, testAccount(2), 1, types.SlotStatus_SLOT_STATUS_ACTIVE, 1)
	bad := slot(t, 2, testAccount(3), 2, types.SlotStatus_SLOT_STATUS_ACTIVE, 1)
	bad.PayoutAddress = testModuleAddress(testModuleAccountName)

	_, err := k.InitGenesis(ctx, &types.GenesisState{
		Params: &params, Slots: []*types.CoreSlot{good, bad}, NextSlotId: 3,
	})
	require.ErrorIs(t, err, types.ErrInvalidAddress)

	// Nothing was persisted: not the params, not the earlier valid slot.
	_, err = k.Params.Get(ctx)
	require.Error(t, err, "params must not survive a rejected genesis")
	has, err := k.Slots.Has(ctx, good.SlotId)
	require.NoError(t, err)
	require.False(t, has, "an earlier valid slot must not survive a rejected genesis")
}

func TestInitGenesisAcceptsOrdinarySlotAddresses(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	params := types.DefaultParams(authority, emergency)

	_, err := k.InitGenesis(ctx, &types.GenesisState{Params: &params, Slots: []*types.CoreSlot{
		slot(t, 1, testAccount(2), 1, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
		slot(t, 2, testAccount(3), 2, types.SlotStatus_SLOT_STATUS_PENDING, 0),
	}, NextSlotId: 3})
	require.NoError(t, err)

	stored, err := k.GetSlot(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, testAccount(2), stored.OperatorAddress)
}

// TestUnconfiguredValidatorRejectsRegistration proves the keeper cannot be built
// without the capability and still admit addresses: the zero value fails closed
// rather than defaulting to permissive.
func TestUnconfiguredValidatorRejectsRegistration(t *testing.T) {
	k, ctx, authority, emergency := setupWithoutEconomicAddresses(t)
	params := types.DefaultParams(authority, emergency)
	require.NoError(t, k.Params.Set(ctx, params))

	msgs := keeper.NewMsgServer(k)
	_, err := msgs.RegisterCoreSlot(ctx, registerMsg(t, authority, testAccount(9), testAccount(10), 2))
	require.ErrorIs(t, err, types.ErrInvalidAddress)
	require.Contains(t, err.Error(), "unconfigured")
}
