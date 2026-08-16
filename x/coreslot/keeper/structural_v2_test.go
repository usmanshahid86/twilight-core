package keeper_test

import (
	"math"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"

	appparams "github.com/twilight-project/twilight-core/app/params"
	"github.com/twilight-project/twilight-core/x/coreslot/keeper"
	"github.com/twilight-project/twilight-core/x/coreslot/types"
)

// Regression coverage for the V2 structural state: the activation generation,
// the two active-set ceilings, the ACTIVE membership index, the settlement
// credential and its mutation gate, and the fresh-genesis contract.

func activeIDs(t *testing.T, k keeper.Keeper, ctx sdk.Context) []uint64 {
	t.Helper()
	slots, err := k.GetActiveSlots(ctx)
	require.NoError(t, err)
	ids := make([]uint64, 0, len(slots))
	for _, s := range slots {
		ids = append(ids, s.SlotId)
	}
	return ids
}

// indexedIDs reads the membership index directly, so an assertion can distinguish
// "the index says so" from "GetActiveSlots says so".
func indexedIDs(t *testing.T, k keeper.Keeper, ctx sdk.Context) []uint64 {
	t.Helper()
	ids := make([]uint64, 0)
	require.NoError(t, k.ActiveSlots.Walk(ctx, nil, func(id uint64) (bool, error) {
		ids = append(ids, id)
		return false, nil
	}))
	return ids
}

// requireIndexMatchesStatus checks the invariant in BOTH directions over every
// stored slot: indexed if and only if ACTIVE. One direction alone would miss a
// stale entry left behind by a lifecycle transition.
func requireIndexMatchesStatus(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
	t.Helper()
	indexed := map[uint64]bool{}
	for _, id := range indexedIDs(t, k, ctx) {
		indexed[id] = true
	}
	seen := 0
	require.NoError(t, k.Slots.Walk(ctx, nil, func(id uint64, slot types.CoreSlot) (bool, error) {
		isActive := slot.Status == types.SlotStatus_SLOT_STATUS_ACTIVE
		require.Equalf(t, isActive, indexed[id], "slot %d status %s vs index membership %v", id, slot.Status, indexed[id])
		if isActive {
			seen++
		}
		return false, nil
	}))
	require.Equal(t, len(indexed), seen, "index holds an entry with no matching ACTIVE slot record")
}

func oneSlotGenesis(t *testing.T, k keeper.Keeper, ctx sdk.Context, authority, emergency string) string {
	t.Helper()
	params := types.DefaultParams(authority, emergency)
	op := sdk.AccAddress(append([]byte{2}, make([]byte, 19)...)).String()
	_, err := initGenesis(t, k, ctx, &types.GenesisState{
		Params: &params, NextSlotId: 2,
		Slots: []*types.CoreSlot{slot(t, 1, op, 1, types.SlotStatus_SLOT_STATUS_ACTIVE, 1)},
	})
	require.NoError(t, err)
	return op
}

// --- activation generation ---

func TestActivationAdvancesGenerationAndHeights(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	oneSlotGenesis(t, k, ctx, authority, emergency)
	msgs := keeper.NewMsgServer(k)

	op2 := sdk.AccAddress(append([]byte{3}, make([]byte, 19)...)).String()
	res, err := msgs.RegisterCoreSlot(ctx, registerMsg(t, authority, op2, op2, 2))
	require.NoError(t, err)

	// A registered slot is PENDING: never activated, and not in the index.
	pending, err := k.GetSlot(ctx, res.SlotId)
	require.NoError(t, err)
	require.Equal(t, uint64(0), pending.ActivationSequence)
	require.Equal(t, int64(0), pending.ActivatedHeight)
	require.Equal(t, int64(0), pending.ActivationEffectiveHeight)
	require.NotContains(t, indexedIDs(t, k, ctx), res.SlotId, "a PENDING slot must not be in the active index")

	// First activation succeeds from the zero sentinel: the ACTIVE invariant is
	// evaluated against the candidate post-transition record, not the PENDING row.
	activateCtx := ctx.WithBlockHeight(7)
	_, err = msgs.ActivateCoreSlot(activateCtx, &types.MsgActivateCoreSlot{Authority: authority, SlotId: res.SlotId})
	require.NoError(t, err)

	first, err := k.GetSlot(activateCtx, res.SlotId)
	require.NoError(t, err)
	require.Equal(t, uint64(1), first.ActivationSequence)
	require.Equal(t, int64(7), first.ActivatedHeight, "activated height is the transaction height H")
	require.Equal(t, int64(8), first.ActivationEffectiveHeight, "reward accounting starts at H+1")
	requireIndexMatchesStatus(t, k, activateCtx)

	// Reactivation from INACTIVE advances the generation again.
	_, err = msgs.InactivateCoreSlot(activateCtx, &types.MsgInactivateCoreSlot{
		AuthorityOrOperator: authority, SlotId: res.SlotId, Reason: "maintenance",
	})
	require.NoError(t, err)
	requireIndexMatchesStatus(t, k, activateCtx)

	reactivateCtx := ctx.WithBlockHeight(11)
	_, err = msgs.ActivateCoreSlot(reactivateCtx, &types.MsgActivateCoreSlot{Authority: authority, SlotId: res.SlotId})
	require.NoError(t, err)
	second, err := k.GetSlot(reactivateCtx, res.SlotId)
	require.NoError(t, err)
	require.Equal(t, uint64(2), second.ActivationSequence, "every reactivation advances the generation")
	require.Equal(t, int64(11), second.ActivatedHeight)
	require.Equal(t, int64(12), second.ActivationEffectiveHeight)

	// And again from SUSPENDED.
	_, err = msgs.SuspendCoreSlot(reactivateCtx, &types.MsgSuspendCoreSlot{
		Authority: authority, SlotId: res.SlotId, Reason: "evidence",
	})
	require.NoError(t, err)
	requireIndexMatchesStatus(t, k, reactivateCtx)

	thirdCtx := ctx.WithBlockHeight(20)
	_, err = msgs.ActivateCoreSlot(thirdCtx, &types.MsgActivateCoreSlot{Authority: authority, SlotId: res.SlotId})
	require.NoError(t, err)
	third, err := k.GetSlot(thirdCtx, res.SlotId)
	require.NoError(t, err)
	require.Equal(t, uint64(3), third.ActivationSequence)
	require.Equal(t, int64(21), third.ActivationEffectiveHeight)
}

func TestActivationRejectsOverflowingSequenceAndHeight(t *testing.T) {
	t.Run("activation sequence at uint64 maximum", func(t *testing.T) {
		k, ctx, authority, emergency := setup(t)
		oneSlotGenesis(t, k, ctx, authority, emergency)
		msgs := keeper.NewMsgServer(k)

		op2 := sdk.AccAddress(append([]byte{3}, make([]byte, 19)...)).String()
		res, err := msgs.RegisterCoreSlot(ctx, registerMsg(t, authority, op2, op2, 2))
		require.NoError(t, err)

		saturated, err := k.GetSlot(ctx, res.SlotId)
		require.NoError(t, err)
		saturated.ActivationSequence = math.MaxUint64
		require.NoError(t, k.Slots.Set(ctx, res.SlotId, saturated))

		_, err = msgs.ActivateCoreSlot(ctx, &types.MsgActivateCoreSlot{Authority: authority, SlotId: res.SlotId})
		require.ErrorIs(t, err, types.ErrInvalidTransition)
		require.Contains(t, err.Error(), "activation sequence exhausted")

		after, err := k.GetSlot(ctx, res.SlotId)
		require.NoError(t, err)
		require.Equal(t, types.SlotStatus_SLOT_STATUS_PENDING, after.Status, "a rejected activation must not mutate the slot")
		require.NotContains(t, indexedIDs(t, k, ctx), res.SlotId)
	})

	t.Run("effective height at int64 maximum", func(t *testing.T) {
		k, ctx, authority, emergency := setup(t)
		oneSlotGenesis(t, k, ctx, authority, emergency)
		msgs := keeper.NewMsgServer(k)

		op2 := sdk.AccAddress(append([]byte{3}, make([]byte, 19)...)).String()
		res, err := msgs.RegisterCoreSlot(ctx, registerMsg(t, authority, op2, op2, 2))
		require.NoError(t, err)

		topCtx := ctx.WithBlockHeight(math.MaxInt64)
		_, err = msgs.ActivateCoreSlot(topCtx, &types.MsgActivateCoreSlot{Authority: authority, SlotId: res.SlotId})
		require.ErrorIs(t, err, types.ErrInvalidTransition)
		require.Contains(t, err.Error(), "activation effective height overflows")

		after, err := k.GetSlot(topCtx, res.SlotId)
		require.NoError(t, err)
		require.Equal(t, types.SlotStatus_SLOT_STATUS_PENDING, after.Status)
	})
}

// --- active-set ceilings ---

// TestActivationRespectsBothActiveCeilings drives the operational ceiling to its
// boundary at two different configured values. The immutable ceiling is 100 and
// the configured one may be lower; a test at only one value could not tell an
// implementation that reads the right limit from one that reads either.
func TestActivationRespectsBothActiveCeilings(t *testing.T) {
	for _, tc := range []struct {
		name       string
		configured uint64
	}{
		{"configured maximum equals the hard ceiling", 100},
		{"configured maximum below the hard ceiling", 50},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx, authority, emergency := setup(t)
			params := types.DefaultParams(authority, emergency)
			params.MaxActiveSlots = tc.configured

			// Import one short of the configured ceiling, all ACTIVE.
			ops := makeOps(40, int(tc.configured))
			genesis := &types.GenesisState{Params: &params, NextSlotId: tc.configured}
			for i := uint64(1); i < tc.configured; i++ {
				genesis.Slots = append(genesis.Slots, slot(t, i, ops[i-1], byte(i), types.SlotStatus_SLOT_STATUS_ACTIVE, 1))
			}
			_, err := initGenesis(t, k, ctx, genesis)
			require.NoError(t, err)

			count, err := k.GetActiveSlots(ctx)
			require.NoError(t, err)
			require.Len(t, count, int(tc.configured)-1)

			// One more activation reaches the ceiling exactly and must succeed.
			msgs := keeper.NewMsgServer(k)
			lastOp := ops[tc.configured-1]
			res, err := msgs.RegisterCoreSlot(ctx, registerMsg(t, authority, lastOp, lastOp, byte(tc.configured)))
			require.NoError(t, err)
			_, err = msgs.ActivateCoreSlot(ctx, &types.MsgActivateCoreSlot{Authority: authority, SlotId: res.SlotId})
			require.NoError(t, err, "activation to exactly the configured ceiling must succeed")

			// The next one is over the ceiling and must be refused.
			overOp := sdk.AccAddress(append([]byte{9, 9}, make([]byte, 18)...)).String()
			over, err := msgs.RegisterCoreSlot(ctx, registerMsg(t, authority, overOp, overOp, 250))
			require.NoError(t, err)
			_, err = msgs.ActivateCoreSlot(ctx, &types.MsgActivateCoreSlot{Authority: authority, SlotId: over.SlotId})
			require.ErrorIs(t, err, types.ErrMaxActiveSlots)

			requireIndexMatchesStatus(t, k, ctx)
			active, err := k.GetActiveSlots(ctx)
			require.NoError(t, err)
			require.Len(t, active, int(tc.configured), "the active set must stop at the configured ceiling")
		})
	}
}

func TestParamsBoundMaxActiveSlotsByHardCeiling(t *testing.T) {
	authority := sdk.AccAddress(make([]byte, 20)).String()
	emergency := sdk.AccAddress(append([]byte{1}, make([]byte, 19)...)).String()

	require.Equal(t, uint64(100), appparams.HardMaxActiveCoreSlots, "the ratified immutable ceiling")

	at := types.DefaultParams(authority, emergency)
	at.MaxActiveSlots = appparams.HardMaxActiveCoreSlots
	require.NoError(t, at.Validate(), "the configured maximum may equal the immutable ceiling")

	over := types.DefaultParams(authority, emergency)
	over.MaxActiveSlots = appparams.HardMaxActiveCoreSlots + 1
	require.ErrorIs(t, over.Validate(), types.ErrInvalidParams)

	zero := types.DefaultParams(authority, emergency)
	zero.MinActiveSlots, zero.MaxActiveSlots = 0, 0
	require.ErrorIs(t, zero.Validate(), types.ErrInvalidParams)

	// The pre-existing min/max relation is untouched.
	inverted := types.DefaultParams(authority, emergency)
	inverted.MinActiveSlots, inverted.MaxActiveSlots = 5, 4
	require.ErrorIs(t, inverted.Validate(), types.ErrInvalidParams)
}

func TestDeprecatedParamsMustCarryZeroValues(t *testing.T) {
	authority := sdk.AccAddress(make([]byte, 20)).String()
	emergency := sdk.AccAddress(append([]byte{1}, make([]byte, 19)...)).String()

	require.NoError(t, types.DefaultParams(authority, emergency).Validate())

	// These fields are deprecated on purpose and this test exists precisely to
	// prove a non-zero value is refused, so the deprecation warning is expected
	// rather than a signal to stop using them here.
	//nolint:staticcheck // SA1019: deliberate use of the deprecated fields under test
	for _, tc := range []struct {
		name   string
		break_ func(*types.Params)
	}{
		{"activation delay", func(p *types.Params) { p.ActivationDelayBlocks = 1 }},
		{"removal delay", func(p *types.Params) { p.RemovalDelayBlocks = 1 }},
		{"self registration", func(p *types.Params) { p.AllowSelfRegistration = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := types.DefaultParams(authority, emergency)
			tc.break_(&params)
			require.ErrorIs(t, params.Validate(), types.ErrInvalidParams)
		})
	}
}

// TestRegistrationIsAuthorityOnlyRegardlessOfStoredFlag proves the authorization
// branch is gone rather than merely disabled: even with the deprecated flag
// forced true in the store, a self-registering operator is refused.
func TestRegistrationIsAuthorityOnlyRegardlessOfStoredFlag(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	oneSlotGenesis(t, k, ctx, authority, emergency)

	stored, err := k.Params.Get(ctx)
	require.NoError(t, err)
	// Forcing the deprecated flag on is the whole point: the authorization branch
	// that once read it is gone, so the stored value cannot change the outcome.
	stored.AllowSelfRegistration = true //nolint:staticcheck // SA1019: deliberate use of the deprecated field under test
	require.NoError(t, k.Params.Set(ctx, stored))

	op := sdk.AccAddress(append([]byte{4}, make([]byte, 19)...)).String()
	msgs := keeper.NewMsgServer(k)
	_, err = msgs.RegisterCoreSlot(ctx, registerMsg(t, op, op, op, 4))
	require.ErrorIs(t, err, types.ErrUnauthorized, "self-registration must be impossible even with the deprecated flag set")
}

// --- active index ---

func TestActiveIndexEnumeratesAscendingAndTracksLifecycle(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	params := types.DefaultParams(authority, emergency)
	ops := makeOps(50, 5)

	// Deliberately scrambled insertion order: ascending output must come from the
	// index's key encoding, not from the order the fixture happened to supply.
	_, err := initGenesis(t, k, ctx, &types.GenesisState{
		Params: &params, NextSlotId: 6,
		Slots: []*types.CoreSlot{
			slot(t, 4, ops[3], 4, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
			slot(t, 1, ops[0], 1, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
			slot(t, 5, ops[4], 5, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
			slot(t, 2, ops[1], 2, types.SlotStatus_SLOT_STATUS_PENDING, 0),
			slot(t, 3, ops[2], 3, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
		},
	})
	require.NoError(t, err)

	require.Equal(t, []uint64{1, 3, 4, 5}, indexedIDs(t, k, ctx), "genesis population is exact and ascending")
	require.Equal(t, []uint64{1, 3, 4, 5}, activeIDs(t, k, ctx))
	requireIndexMatchesStatus(t, k, ctx)

	msgs := keeper.NewMsgServer(k)
	_, err = msgs.InactivateCoreSlot(ctx, &types.MsgInactivateCoreSlot{AuthorityOrOperator: authority, SlotId: 3, Reason: "maintenance"})
	require.NoError(t, err)
	require.Equal(t, []uint64{1, 4, 5}, activeIDs(t, k, ctx))
	requireIndexMatchesStatus(t, k, ctx)

	_, err = msgs.SuspendCoreSlot(ctx, &types.MsgSuspendCoreSlot{Authority: authority, SlotId: 4, Reason: "evidence"})
	require.NoError(t, err)
	require.Equal(t, []uint64{1, 5}, activeIDs(t, k, ctx))
	requireIndexMatchesStatus(t, k, ctx)

	_, err = msgs.RemoveCoreSlot(ctx, &types.MsgRemoveCoreSlot{Authority: authority, SlotId: 3, Reason: "decommission"})
	require.NoError(t, err)
	require.Equal(t, []uint64{1, 5}, activeIDs(t, k, ctx))
	requireIndexMatchesStatus(t, k, ctx)

	// Reactivation puts it back, still in ascending position.
	_, err = msgs.ActivateCoreSlot(ctx, &types.MsgActivateCoreSlot{Authority: authority, SlotId: 4})
	require.NoError(t, err)
	require.Equal(t, []uint64{1, 4, 5}, activeIDs(t, k, ctx))
	requireIndexMatchesStatus(t, k, ctx)
}

// TestActiveEnumerationDoesNotScanEveryRegisteredSlot is the complexity guard. It
// removes the slot records for non-active slots while leaving the ACTIVE ones
// intact: an implementation that walks Slots and filters by status would still
// return the right answer, so instead the check is that enumeration reads ONLY
// the active rows — a full walk would have to touch the missing ones.
func TestActiveEnumerationDoesNotScanEveryRegisteredSlot(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	params := types.DefaultParams(authority, emergency)
	ops := makeOps(60, 4)
	_, err := initGenesis(t, k, ctx, &types.GenesisState{
		Params: &params, NextSlotId: 5,
		Slots: []*types.CoreSlot{
			slot(t, 1, ops[0], 1, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
			slot(t, 2, ops[1], 2, types.SlotStatus_SLOT_STATUS_PENDING, 0),
			slot(t, 3, ops[2], 3, types.SlotStatus_SLOT_STATUS_PENDING, 0),
			slot(t, 4, ops[3], 4, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
		},
	})
	require.NoError(t, err)

	// Simulate lifetime history that enumeration must not depend on. These rows
	// are unreachable through the index, so only a full scan would notice them.
	for _, id := range []uint64{2, 3} {
		require.NoError(t, k.Slots.Remove(ctx, id))
	}

	active, err := k.GetActiveSlots(ctx)
	require.NoError(t, err)
	require.Len(t, active, 2)
	require.Equal(t, []uint64{1, 4}, []uint64{active[0].SlotId, active[1].SlotId})

	// EndBlock's validator diff reads the same way and must also stay clear of the
	// missing history.
	_, err = k.EndBlock(ctx.WithBlockHeight(2))
	require.NoError(t, err)
}

// TestActiveIndexDivergenceFailsClosed proves the absence of a silent fallback:
// an index entry with no ACTIVE record is an error, not something to skip.
func TestActiveIndexDivergenceFailsClosed(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	oneSlotGenesis(t, k, ctx, authority, emergency)

	require.NoError(t, k.ActiveSlots.Set(ctx, 404))
	_, err := k.GetActiveSlots(ctx)
	require.ErrorIs(t, err, types.ErrInvalidGenesis)
	require.Contains(t, err.Error(), "missing slot 404")

	require.NoError(t, k.ActiveSlots.Remove(ctx, 404))

	// An indexed slot whose record says otherwise is equally a divergence.
	stored, err := k.GetSlot(ctx, 1)
	require.NoError(t, err)
	stored.Status = types.SlotStatus_SLOT_STATUS_INACTIVE
	require.NoError(t, k.Slots.Set(ctx, 1, stored))
	_, err = k.GetActiveSlots(ctx)
	require.ErrorIs(t, err, types.ErrInvalidTransition)
}

// --- selection policy ---

func TestRegistrationWritesPolicyVersionOne(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	oneSlotGenesis(t, k, ctx, authority, emergency)
	msgs := keeper.NewMsgServer(k)

	op2 := sdk.AccAddress(append([]byte{3}, make([]byte, 19)...)).String()
	registerCtx := ctx.WithBlockHeight(9)
	res, err := msgs.RegisterCoreSlot(registerCtx, registerMsg(t, authority, op2, op2, 2))
	require.NoError(t, err)

	stored, err := k.GetSlot(registerCtx, res.SlotId)
	require.NoError(t, err)
	require.Equal(t, uint64(1), stored.CurrentSelectionPolicyVersion)
	require.Equal(t, int64(0), stored.LastSelectionPolicyUpdateHeight,
		"the update height is stored and starts at zero; registration does not consume the cooldown")

	policy, err := k.SelectionPolicies.Get(registerCtx, collections.Join(res.SlotId, uint64(1)))
	require.NoError(t, err)
	require.Equal(t, res.SlotId, policy.SlotId)
	require.Equal(t, uint64(1), policy.PolicyVersion)
	require.Equal(t, uint64(2_500), policy.SelectionRateBps)
	require.Equal(t, uint64(10), policy.MaxSelectedParticipants)
	require.Equal(t, int64(9), policy.ValidFromHeight, "version 1 is effective from the registration height")
	require.Equal(t, int64(0), policy.ValidUntilHeightExclusive, "version 1 is current")
}

// TestRegistrationIgnoresCallerSuppliedHistoryMetadata is the shape guarantee: the
// caller-input message carries only the two operator-selectable values, so a
// registration cannot choose its own version numbering or validity window. If the
// input type ever grew those fields this test would stop compiling, which is the
// point.
func TestRegistrationIgnoresCallerSuppliedHistoryMetadata(t *testing.T) {
	fields := map[string]bool{}
	typ := reflect.TypeOf(types.InitialSelectionPolicy{})
	for i := 0; i < typ.NumField(); i++ {
		fields[typ.Field(i).Name] = true
	}
	require.Equal(t, map[string]bool{"SelectionRateBps": true, "MaxSelectedParticipants": true}, fields,
		"InitialSelectionPolicy must expose exactly the two operator-selectable values; "+
			"slot id, version and the validity window are assigned by consensus")
}

func TestSelectionPolicyLocalValidity(t *testing.T) {
	require.NoError(t, types.ValidateSelectionPolicyValues(1, 1))
	require.NoError(t, types.ValidateSelectionPolicyValues(appparams.AbsoluteMaxSelectionRateBps, 10),
		"the absolute rate ceiling itself is admissible")

	require.ErrorIs(t, types.ValidateSelectionPolicyValues(0, 10), types.ErrInvalidSelectionPolicy)
	require.ErrorIs(t, types.ValidateSelectionPolicyValues(appparams.AbsoluteMaxSelectionRateBps+1, 10), types.ErrInvalidSelectionPolicy)
	require.ErrorIs(t, types.ValidateSelectionPolicyValues(2_500, 0), types.ErrInvalidSelectionPolicy)

	// A-SEL-01 removed the independent HARD_MAX_SELECTED_PARTICIPANTS, so nothing
	// here consults a participant ceiling. This asserts the ABSENCE of that
	// dependency at the validator, and deliberately says nothing about how extreme
	// positive values should be admitted through registration or genesis: the
	// operational-envelope question (B5/Y-4) is unresolved, and a test that pinned
	// either answer would decide it by accident.
	require.NoError(t, types.ValidateSelectionPolicyValues(2_500, 1_000_000),
		"a large participant maximum is not rejected by any local ceiling")
}

// --- settlement address ---

func TestSettlementAddressRequiredAndEconomicallyValidated(t *testing.T) {
	blocked := testAccount(77)
	k, ctx, authority, emergency := setup(t, blocked)
	oneSlotGenesis(t, k, ctx, authority, emergency)
	msgs := keeper.NewMsgServer(k)

	for i, tc := range []struct {
		name       string
		settlement string
	}{
		{"empty", ""},
		{"module account", testModuleAddress(testModuleAccountName)},
		{"bank blocked", blocked},
		{"all zero", zeroAddress()},
	} {
		t.Run("registration rejects "+tc.name, func(t *testing.T) {
			// A distinct operator per case so a rejection is never the duplicate-
			// operator guard wearing the settlement rejection's clothes.
			op := sdk.AccAddress(append([]byte{5, byte(i)}, make([]byte, 18)...)).String()
			msg := registerMsg(t, authority, op, op, byte(30+i))
			msg.SettlementAddress = tc.settlement
			_, err := msgs.RegisterCoreSlot(ctx, msg)
			require.ErrorIs(t, err, types.ErrInvalidAddress)
			require.Contains(t, err.Error(), "settlement address")
		})
	}

	// The counterweight: an ordinary destination is admitted and stored.
	op := sdk.AccAddress(append([]byte{6}, make([]byte, 19)...)).String()
	msg := registerMsg(t, authority, op, op, 31)
	msg.SettlementAddress = testAccount(40)
	res, err := msgs.RegisterCoreSlot(ctx, msg)
	require.NoError(t, err)
	stored, err := k.GetSlot(ctx, res.SlotId)
	require.NoError(t, err)
	require.Equal(t, testAccount(40), stored.SettlementAddress)

	// And a bank-blocked address remains a legitimate OPERATOR identity, because
	// the operator is a control identity rather than a value destination.
	blockedOp := registerMsg(t, authority, blocked, testAccount(41), 32)
	_, err = msgs.RegisterCoreSlot(ctx, blockedOp)
	require.NoError(t, err, "a bank-blocked operator identity must remain admissible")
}

func TestOperatorMutationsFreezeOnSuspendAndRemove(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	params := types.DefaultParams(authority, emergency)
	ops := makeOps(70, 3)
	_, err := initGenesis(t, k, ctx, &types.GenesisState{
		Params: &params, NextSlotId: 4,
		Slots: []*types.CoreSlot{
			slot(t, 1, ops[0], 1, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
			slot(t, 2, ops[1], 2, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
			slot(t, 3, ops[2], 3, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
		},
	})
	require.NoError(t, err)
	msgs := keeper.NewMsgServer(k)

	// PENDING, ACTIVE and INACTIVE all permit the operator surface.
	_, err = msgs.UpdateSettlementAddress(ctx, &types.MsgUpdateSettlementAddress{
		Operator: ops[0], SlotId: 1, SettlementAddress: testAccount(50),
	})
	require.NoError(t, err, "ACTIVE permits a settlement update")

	_, err = msgs.InactivateCoreSlot(ctx, &types.MsgInactivateCoreSlot{AuthorityOrOperator: authority, SlotId: 1, Reason: "maintenance"})
	require.NoError(t, err)
	_, err = msgs.UpdateSettlementAddress(ctx, &types.MsgUpdateSettlementAddress{
		Operator: ops[0], SlotId: 1, SettlementAddress: testAccount(51),
	})
	require.NoError(t, err, "INACTIVE permits a settlement update")

	// An identical replacement is a no-op and is refused.
	_, err = msgs.UpdateSettlementAddress(ctx, &types.MsgUpdateSettlementAddress{
		Operator: ops[0], SlotId: 1, SettlementAddress: testAccount(51),
	})
	require.ErrorIs(t, err, types.ErrNoOpUpdate)

	// The replacement itself is a value destination and takes the economic rule.
	_, err = msgs.UpdateSettlementAddress(ctx, &types.MsgUpdateSettlementAddress{
		Operator: ops[0], SlotId: 1, SettlementAddress: testModuleAddress(testModuleAccountName),
	})
	require.ErrorIs(t, err, types.ErrInvalidAddress)

	// SUSPENDED freezes the whole operator surface.
	_, err = msgs.SuspendCoreSlot(ctx, &types.MsgSuspendCoreSlot{Authority: authority, SlotId: 2, Reason: "evidence"})
	require.NoError(t, err)
	requireFrozen(t, msgs, ctx, ops[1], 2)

	// REMOVED is terminal.
	_, err = msgs.RemoveCoreSlot(ctx, &types.MsgRemoveCoreSlot{Authority: authority, SlotId: 1, Reason: "decommission"})
	require.NoError(t, err)
	requireFrozen(t, msgs, ctx, ops[0], 1)
}

func requireFrozen(t *testing.T, msgs types.MsgServer, ctx sdk.Context, operator string, slotID uint64) {
	t.Helper()
	_, err := msgs.UpdateSettlementAddress(ctx, &types.MsgUpdateSettlementAddress{
		Operator: operator, SlotId: slotID, SettlementAddress: testAccount(60),
	})
	require.ErrorIs(t, err, types.ErrInvalidTransition, "settlement address must be frozen")

	_, err = msgs.UpdatePayoutAddress(ctx, &types.MsgUpdatePayoutAddress{
		Operator: operator, SlotId: slotID, NewPayoutAddress: testAccount(61),
	})
	require.ErrorIs(t, err, types.ErrInvalidTransition, "payout address must be frozen")

	_, err = msgs.UpdateOperatorMetadata(ctx, &types.MsgUpdateOperatorMetadata{
		Operator: operator, SlotId: slotID, Metadata: &types.OperatorMetadata{Moniker: "changed"},
	})
	require.ErrorIs(t, err, types.ErrInvalidTransition, "metadata must be frozen")
}

// --- fresh genesis ---

func TestFreshGenesisNormalization(t *testing.T) {
	authority := sdk.AccAddress(make([]byte, 20)).String()
	emergency := sdk.AccAddress(append([]byte{1}, make([]byte, 19)...)).String()
	op := sdk.AccAddress(append([]byte{2}, make([]byte, 19)...)).String()

	build := func(t *testing.T) (*types.GenesisState, *types.CoreSlot) {
		t.Helper()
		params := types.DefaultParams(authority, emergency)
		s := slot(t, 1, op, 1, types.SlotStatus_SLOT_STATUS_ACTIVE, 1)
		genesis := freshGenesis(t, &types.GenesisState{
			Params: &params, Slots: []*types.CoreSlot{s}, NextSlotId: 2,
		})
		require.NoError(t, genesis.Validate(), "the baseline fixture must be conforming")
		return genesis, s
	}

	for _, tc := range []struct {
		name   string
		mutate func(*types.GenesisState, *types.CoreSlot)
	}{
		{"inactive status", func(_ *types.GenesisState, s *types.CoreSlot) {
			s.Status, s.ConsensusPower, s.ActivationSequence = types.SlotStatus_SLOT_STATUS_INACTIVE, 0, 0
		}},
		{"suspended status", func(_ *types.GenesisState, s *types.CoreSlot) {
			s.Status, s.ConsensusPower, s.ActivationSequence = types.SlotStatus_SLOT_STATUS_SUSPENDED, 0, 0
		}},
		{"removed status", func(_ *types.GenesisState, s *types.CoreSlot) {
			s.Status, s.ConsensusPower, s.ActivationSequence = types.SlotStatus_SLOT_STATUS_REMOVED, 0, 0
		}},
		{"active sequence not one", func(_ *types.GenesisState, s *types.CoreSlot) { s.ActivationSequence = 2 }},
		{"heights disagree", func(_ *types.GenesisState, s *types.CoreSlot) { s.ActivationEffectiveHeight = s.ActivatedHeight + 1 }},
		{"missing settlement address", func(_ *types.GenesisState, s *types.CoreSlot) { s.SettlementAddress = "" }},
		{"policy pointer not one", func(_ *types.GenesisState, s *types.CoreSlot) { s.CurrentSelectionPolicyVersion = 2 }},
		{"prior policy update recorded", func(_ *types.GenesisState, s *types.CoreSlot) { s.LastSelectionPolicyUpdateHeight = 1 }},
		{"missing policy row", func(g *types.GenesisState, _ *types.CoreSlot) { g.SelectionPolicies = nil }},
		{"policy version not one", func(g *types.GenesisState, _ *types.CoreSlot) { g.SelectionPolicies[0].PolicyVersion = 2 }},
		{"policy superseded", func(g *types.GenesisState, _ *types.CoreSlot) { g.SelectionPolicies[0].ValidUntilHeightExclusive = 5 }},
		{"policy rate invalid", func(g *types.GenesisState, _ *types.CoreSlot) { g.SelectionPolicies[0].SelectionRateBps = 0 }},
		{"policy participants invalid", func(g *types.GenesisState, _ *types.CoreSlot) { g.SelectionPolicies[0].MaxSelectedParticipants = 0 }},
		{"duplicate policy rows", func(g *types.GenesisState, _ *types.CoreSlot) {
			g.SelectionPolicies = append(g.SelectionPolicies, &types.SelectionPolicyVersion{
				SlotId: 1, PolicyVersion: 1, SelectionRateBps: 100, MaxSelectedParticipants: 1, ValidFromHeight: 1,
			})
		}},
		{"orphan policy row", func(g *types.GenesisState, _ *types.CoreSlot) {
			g.SelectionPolicies = append(g.SelectionPolicies, &types.SelectionPolicyVersion{
				SlotId: 99, PolicyVersion: 1, SelectionRateBps: 100, MaxSelectedParticipants: 1, ValidFromHeight: 1,
			})
		}},
		{"next slot id does not exceed the maximum", func(g *types.GenesisState, _ *types.CoreSlot) { g.NextSlotId = 1 }},
	} {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			genesis, s := build(t)
			tc.mutate(genesis, s)
			require.Error(t, genesis.Validate())
		})
	}

	t.Run("rejects a pending slot carrying an activation generation", func(t *testing.T) {
		params := types.DefaultParams(authority, emergency)
		pending := slot(t, 1, op, 1, types.SlotStatus_SLOT_STATUS_PENDING, 0)
		active := slot(t, 2, sdk.AccAddress(append([]byte{3}, make([]byte, 19)...)).String(), 2, types.SlotStatus_SLOT_STATUS_ACTIVE, 1)
		genesis := freshGenesis(t, &types.GenesisState{
			Params: &params, Slots: []*types.CoreSlot{pending, active}, NextSlotId: 3,
		})
		require.NoError(t, genesis.Validate())

		pending.ActivationSequence = 1
		require.Error(t, genesis.Validate(), "PENDING must carry the never-activated sentinel")
	})
}

// TestFreshGenesisPinsHeightsAgainstTheChainInitialHeight covers the leg the pure
// types layer cannot decide: the exact activation heights, which are only
// meaningful against the height the chain actually starts at.
func TestFreshGenesisPinsHeightsAgainstTheChainInitialHeight(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	params := types.DefaultParams(authority, emergency)
	op := sdk.AccAddress(append([]byte{2}, make([]byte, 19)...)).String()

	// The context height is 1, so a slot normalized to height 2 is rejected even
	// though it is internally self-consistent.
	s := slot(t, 1, op, 1, types.SlotStatus_SLOT_STATUS_ACTIVE, 1)
	s.ActivationSequence = 1
	s.ActivatedHeight, s.ActivationEffectiveHeight = 2, 2
	genesis := freshGenesis(t, &types.GenesisState{Params: &params, Slots: []*types.CoreSlot{s}, NextSlotId: 2})
	_, err := k.InitGenesis(ctx, genesis)
	require.ErrorIs(t, err, types.ErrInvalidGenesis)
	require.Contains(t, err.Error(), "equal to the initial height 1")

	// Nothing survived the rejection: validation is total before the first write.
	_, err = k.Params.Get(ctx)
	require.Error(t, err, "params must not survive a rejected genesis")
	has, err := k.Slots.Has(ctx, 1)
	require.NoError(t, err)
	require.False(t, has)

	// A policy whose validity starts at the wrong height is refused the same way.
	k2, ctx2, authority2, emergency2 := setup(t)
	params2 := types.DefaultParams(authority2, emergency2)
	genesis2 := freshGenesis(t, &types.GenesisState{
		Params: &params2, NextSlotId: 2,
		Slots: []*types.CoreSlot{slot(t, 1, op, 1, types.SlotStatus_SLOT_STATUS_ACTIVE, 1)},
	})
	genesis2.SelectionPolicies[0].ValidFromHeight = 5
	_, err = k2.InitGenesis(ctx2, genesis2)
	require.ErrorIs(t, err, types.ErrInvalidGenesis)
	require.Contains(t, err.Error(), "valid from the initial height 1")
}

// TestGenesisRejectsMoreActiveSlotsThanTheCeilings covers the genesis half of the
// two-layer bound: genesis bypasses the activation handler, so it must not be
// able to seed a state runtime activation is forbidden to create.
func TestGenesisRejectsMoreActiveSlotsThanTheCeilings(t *testing.T) {
	authority := sdk.AccAddress(make([]byte, 20)).String()
	emergency := sdk.AccAddress(append([]byte{1}, make([]byte, 19)...)).String()

	build := func(t *testing.T, configuredMax uint64, activeCount int) *types.GenesisState {
		t.Helper()
		params := types.DefaultParams(authority, emergency)
		params.MaxActiveSlots = configuredMax
		ops := makeOps(80, activeCount)
		genesis := &types.GenesisState{Params: &params, NextSlotId: uint64(activeCount) + 1}
		for i := 0; i < activeCount; i++ {
			genesis.Slots = append(genesis.Slots,
				slot(t, uint64(i+1), ops[i], byte(i+1), types.SlotStatus_SLOT_STATUS_ACTIVE, 1))
		}
		return freshGenesis(t, genesis)
	}

	t.Run("rejects more active slots than the configured maximum", func(t *testing.T) {
		// 51 ACTIVE under a configured maximum of 50 is refused even though 51 is
		// below the immutable ceiling of 100.
		require.Error(t, build(t, 50, 51).Validate())
	})

	t.Run("accepts exactly the configured maximum", func(t *testing.T) {
		require.NoError(t, build(t, 50, 50).Validate())
	})

	t.Run("accepts exactly the hard ceiling", func(t *testing.T) {
		require.NoError(t, build(t, appparams.HardMaxActiveCoreSlots, int(appparams.HardMaxActiveCoreSlots)).Validate())
	})

	t.Run("rejects more active slots than the hard ceiling", func(t *testing.T) {
		// Note what this does and does not prove. Params validation caps the
		// configured maximum at 100, so with a valid parameter set the OPERATIONAL
		// check rejects 101 ACTIVE slots first and the immutable check is never
		// reached. This asserts the outcome — 101 is refused — not which layer
		// refused it. The immutable check is unreachable defense-in-depth while
		// Params validation holds, which is precisely why it is asserted separately
		// rather than folded into the operational one: it is what must still hold
		// if that validation ever changes.
		genesis := build(t, appparams.HardMaxActiveCoreSlots, int(appparams.HardMaxActiveCoreSlots)+1)
		require.Error(t, genesis.Validate())
	})
}

// TestGenesisValidatorSetsAgreeInBothDirections covers the CoreSlot-expressible
// half of the §80 validator-set contract.
func TestGenesisValidatorSetsAgreeInBothDirections(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	params := types.DefaultParams(authority, emergency)
	ops := makeOps(90, 3)
	updates, err := initGenesis(t, k, ctx, &types.GenesisState{
		Params: &params, NextSlotId: 4,
		Slots: []*types.CoreSlot{
			slot(t, 1, ops[0], 1, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
			slot(t, 2, ops[1], 2, types.SlotStatus_SLOT_STATUS_PENDING, 0),
			slot(t, 3, ops[2], 3, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
		},
	})
	require.NoError(t, err)

	// One update per ACTIVE slot and nothing else: no duplicate delta, and no
	// delta for the PENDING slot.
	require.Len(t, updates, 2)

	active := activeIDs(t, k, ctx)
	require.Equal(t, []uint64{1, 3}, active)

	lastApplied := lastAppliedBySlot(t, k, ctx)
	require.Len(t, lastApplied, len(active), "nothing outside the ACTIVE set may appear in LastApplied")
	for _, id := range active {
		_, ok := lastApplied[id]
		require.Truef(t, ok, "active slot %d must appear in LastApplied", id)
	}
	requireIndexMatchesStatus(t, k, ctx)
}

// --- storage permanence ---

// TestStorePrefixLedgerHasNoCollisions pins the durable prefix ledger. A prefix is
// permanent: reusing one for different state would silently alias two collections.
func TestStorePrefixLedgerHasNoCollisions(t *testing.T) {
	ledger := map[string][]byte{
		"params":             types.ParamsKey,
		"slots":              types.SlotsPrefix,
		"by_operator":        types.OperatorPrefix,
		"by_consensus":       types.ConsensusPrefix,
		"reserved_consensus": types.ReservedPrefix,
		"rotations":          types.RotationsPrefix,
		"last_applied":       types.LastPrefix,
		"reward_weights":     types.RewardsPrefix,
		"next_slot_id":       types.NextSlotIDKey,
		"selection_policies": types.SelectionPoliciesPrefix,
		"active_slots":       types.ActiveSlotsPrefix,
	}
	seen := map[byte]string{}
	for name, prefix := range ledger {
		require.Lenf(t, prefix, 1, "%s prefix must be a single byte", name)
		if other, dup := seen[prefix[0]]; dup {
			t.Fatalf("prefix 0x%02X is used by both %s and %s", prefix[0], other, name)
		}
		seen[prefix[0]] = name
	}
	// The two this change introduces, pinned by value.
	require.Equal(t, []byte{0x0A}, types.SelectionPoliciesPrefix)
	require.Equal(t, []byte{0x0B}, types.ActiveSlotsPrefix)
}
