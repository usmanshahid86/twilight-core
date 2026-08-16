package keeper_test

import (
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"cosmossdk.io/collections"

	sdk "github.com/cosmos/cosmos-sdk/types"

	appparams "github.com/twilight-project/twilight-core/app/params"
	"github.com/twilight-project/twilight-core/x/coreslot/keeper"
	"github.com/twilight-project/twilight-core/x/coreslot/types"
)

// Regression coverage for runtime Selection policy updates: the cooldown, the
// immutable version transition, the seek index, and the query surface.

// policyRow reads a stored version directly, so an assertion can distinguish what
// history holds from what a resolver reports.
func policyRow(t *testing.T, k keeper.Keeper, ctx sdk.Context, slotID, version uint64) types.SelectionPolicyVersion {
	t.Helper()
	policy, err := k.SelectionPolicies.Get(ctx, collections.Join(slotID, version))
	require.NoError(t, err)
	return policy
}

func hasPolicyRow(t *testing.T, k keeper.Keeper, ctx sdk.Context, slotID, version uint64) bool {
	t.Helper()
	has, err := k.SelectionPolicies.Has(ctx, collections.Join(slotID, version))
	require.NoError(t, err)
	return has
}

func hasSeekEntry(t *testing.T, k keeper.Keeper, ctx sdk.Context, slotID uint64, validFrom int64) bool {
	t.Helper()
	has, err := k.PolicyStarts.Has(ctx, collections.Join(slotID, validFrom))
	require.NoError(t, err)
	return has
}

// policySlotGenesis imports one ACTIVE slot whose operator can then update policy.
func policySlotGenesis(t *testing.T, k keeper.Keeper, ctx sdk.Context, authority, emergency string) string {
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

// setCooldown stores a configured cooldown, so runtime boundary tests can prove
// the keeper reads Params rather than a compile-time default.
func setCooldown(t *testing.T, k keeper.Keeper, ctx sdk.Context, blocks uint64) {
	t.Helper()
	params, err := k.Params.Get(ctx)
	require.NoError(t, err)
	params.SelectionPolicyUpdateCooldownBlocks = blocks
	require.NoError(t, k.Params.Set(ctx, params))
}

// --- params ---

func TestSelectionPolicyCooldownParamBounds(t *testing.T) {
	authority := sdk.AccAddress(make([]byte, 20)).String()
	emergency := sdk.AccAddress(append([]byte{1}, make([]byte, 19)...)).String()

	require.Equal(t, uint64(360), appparams.HardMinSelectionPolicyUpdateCooldownBlocks,
		"current pre-mainnet hard floor")
	require.Equal(t, uint64(720), types.DefaultSelectionPolicyUpdateCooldownBlocks,
		"current pre-mainnet default")

	base := types.DefaultParams(authority, emergency)
	require.Equal(t, uint64(720), base.SelectionPolicyUpdateCooldownBlocks)
	require.NoError(t, base.Validate())

	for _, tc := range []struct {
		name     string
		cooldown uint64
		valid    bool
	}{
		{"zero", 0, false},
		{"one below the floor", 359, false},
		{"exactly the floor", 360, true},
		{"the default", 720, true},
		{"far above the default", 1_000_000, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := types.DefaultParams(authority, emergency)
			params.SelectionPolicyUpdateCooldownBlocks = tc.cooldown
			if tc.valid {
				require.NoError(t, params.Validate())
				return
			}
			require.ErrorIs(t, params.Validate(), types.ErrInvalidParams)
		})
	}
}

// TestUpdateParamsCannotInstallCooldownBelowTheFloor proves the governance path
// reaches the same validation rather than carrying a second, weaker rule.
func TestUpdateParamsCannotInstallCooldownBelowTheFloor(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	policySlotGenesis(t, k, ctx, authority, emergency)
	msgs := keeper.NewMsgServer(k)

	below := types.DefaultParams(authority, emergency)
	below.SelectionPolicyUpdateCooldownBlocks = 359
	_, err := msgs.UpdateParams(ctx, &types.MsgUpdateParams{Authority: authority, Params: &below})
	require.ErrorIs(t, err, types.ErrInvalidParams)

	stored, err := k.Params.Get(ctx)
	require.NoError(t, err)
	require.Equal(t, uint64(720), stored.SelectionPolicyUpdateCooldownBlocks, "a rejected update must not alter params")

	at := types.DefaultParams(authority, emergency)
	at.SelectionPolicyUpdateCooldownBlocks = 360
	_, err = msgs.UpdateParams(ctx, &types.MsgUpdateParams{Authority: authority, Params: &at})
	require.NoError(t, err)
}

// --- authorization and status gate ---

func TestSelectionPolicyUpdateAuthorizationAndStatusGate(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	params := types.DefaultParams(authority, emergency)
	ops := makeOps(130, 4)
	_, err := initGenesis(t, k, ctx, &types.GenesisState{
		Params: &params, NextSlotId: 5,
		Slots: []*types.CoreSlot{
			slot(t, 1, ops[0], 1, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
			slot(t, 2, ops[1], 2, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
			slot(t, 3, ops[2], 3, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
			slot(t, 4, ops[3], 4, types.SlotStatus_SLOT_STATUS_PENDING, 0),
		},
	})
	require.NoError(t, err)
	msgs := keeper.NewMsgServer(k)

	update := func(operator string, slotID uint64, rate uint64) error {
		_, err := msgs.UpdateSelectionPolicy(ctx, &types.MsgUpdateSelectionPolicy{
			Operator: operator, SlotId: slotID, SelectionRateBps: rate, MaxSelectedParticipants: 11,
		})
		return err
	}

	// A signer who is not the slot's operator is refused.
	require.ErrorIs(t, update(ops[1], 1, 100), types.ErrUnauthorized)

	// PENDING and ACTIVE both permit an update.
	require.NoError(t, update(ops[3], 4, 100), "PENDING permits a policy update")
	require.NoError(t, update(ops[0], 1, 100), "ACTIVE permits a policy update")

	// INACTIVE permits one too.
	_, err = msgs.InactivateCoreSlot(ctx, &types.MsgInactivateCoreSlot{
		AuthorityOrOperator: authority, SlotId: 2, Reason: "maintenance",
	})
	require.NoError(t, err)
	setCooldown(t, k, ctx, 360)
	require.NoError(t, update(ops[1], 2, 100), "INACTIVE permits a policy update")

	// SUSPENDED freezes the operator surface.
	_, err = msgs.SuspendCoreSlot(ctx, &types.MsgSuspendCoreSlot{Authority: authority, SlotId: 3, Reason: "evidence"})
	require.NoError(t, err)
	require.ErrorIs(t, update(ops[2], 3, 100), types.ErrInvalidTransition)

	// REMOVED is terminal.
	_, err = msgs.RemoveCoreSlot(ctx, &types.MsgRemoveCoreSlot{Authority: authority, SlotId: 2, Reason: "decommission"})
	require.NoError(t, err)
	require.ErrorIs(t, update(ops[1], 2, 200), types.ErrInvalidTransition)
}

func TestSelectionPolicyIdenticalUpdateRejected(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	op := policySlotGenesis(t, k, ctx, authority, emergency)
	msgs := keeper.NewMsgServer(k)

	current := policyRow(t, k, ctx, 1, 1)
	_, err := msgs.UpdateSelectionPolicy(ctx, &types.MsgUpdateSelectionPolicy{
		Operator: op, SlotId: 1,
		SelectionRateBps: current.SelectionRateBps, MaxSelectedParticipants: current.MaxSelectedParticipants,
	})
	require.ErrorIs(t, err, types.ErrNoOpUpdate)

	// Changing only one of the two fields is a real change.
	_, err = msgs.UpdateSelectionPolicy(ctx, &types.MsgUpdateSelectionPolicy{
		Operator: op, SlotId: 1,
		SelectionRateBps: current.SelectionRateBps, MaxSelectedParticipants: current.MaxSelectedParticipants + 1,
	})
	require.NoError(t, err)
}

// --- version transition ---

func TestSelectionPolicyVersionTransition(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	op := policySlotGenesis(t, k, ctx, authority, emergency)
	msgs := keeper.NewMsgServer(k)

	v1 := policyRow(t, k, ctx, 1, 1)
	require.Equal(t, int64(0), v1.ValidUntilHeightExclusive, "version 1 starts current")

	updateCtx := ctx.WithBlockHeight(50)
	res, err := msgs.UpdateSelectionPolicy(updateCtx, &types.MsgUpdateSelectionPolicy{
		Operator: op, SlotId: 1, SelectionRateBps: 1_234, MaxSelectedParticipants: 42,
	})
	require.NoError(t, err)
	require.Equal(t, uint64(2), res.PolicyVersion)

	closed := policyRow(t, k, updateCtx, 1, 1)
	require.Equal(t, int64(51), closed.ValidUntilHeightExclusive, "the previous version closes at H+1")
	// Nothing else about the closed row may change.
	require.Equal(t, v1.SlotId, closed.SlotId)
	require.Equal(t, v1.PolicyVersion, closed.PolicyVersion)
	require.Equal(t, v1.SelectionRateBps, closed.SelectionRateBps)
	require.Equal(t, v1.MaxSelectedParticipants, closed.MaxSelectedParticipants)
	require.Equal(t, v1.ValidFromHeight, closed.ValidFromHeight)

	v2 := policyRow(t, k, updateCtx, 1, 2)
	require.Equal(t, int64(51), v2.ValidFromHeight, "the new version starts at the same H+1 — gap-free")
	require.Equal(t, int64(0), v2.ValidUntilHeightExclusive, "the new version is current")
	require.Equal(t, uint64(1_234), v2.SelectionRateBps)
	require.Equal(t, uint64(42), v2.MaxSelectedParticipants)

	stored, err := k.GetSlot(updateCtx, 1)
	require.NoError(t, err)
	require.Equal(t, uint64(2), stored.CurrentSelectionPolicyVersion, "the pointer advances")
	require.Equal(t, int64(50), stored.LastSelectionPolicyUpdateHeight,
		"the cooldown height is the transaction height H, not the version's H+1")

	// A later update must not rewrite the already-closed row.
	laterCtx := ctx.WithBlockHeight(50 + 720)
	_, err = msgs.UpdateSelectionPolicy(laterCtx, &types.MsgUpdateSelectionPolicy{
		Operator: op, SlotId: 1, SelectionRateBps: 2_222, MaxSelectedParticipants: 43,
	})
	require.NoError(t, err)
	require.Equal(t, closed, policyRow(t, k, laterCtx, 1, 1), "a closed version is immutable forever")
	require.Equal(t, int64(50+720+1), policyRow(t, k, laterCtx, 1, 2).ValidUntilHeightExclusive)
}

// --- cooldown ---

// TestSelectionPolicyCooldownBoundary uses a configured value distinct from the
// shipped default, so a keeper that read a hard-coded 720 instead of the stored
// parameter would fail here.
func TestSelectionPolicyCooldownBoundary(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	op := policySlotGenesis(t, k, ctx, authority, emergency)
	msgs := keeper.NewMsgServer(k)

	const configured = int64(400) // deliberately not 720
	setCooldown(t, k, ctx, uint64(configured))

	stored, err := k.GetSlot(ctx, 1)
	require.NoError(t, err)
	require.Equal(t, int64(0), stored.LastSelectionPolicyUpdateHeight,
		"registration's version 1 does not consume the cooldown")

	// The first post-registration update is unrestricted.
	firstCtx := ctx.WithBlockHeight(10)
	_, err = msgs.UpdateSelectionPolicy(firstCtx, &types.MsgUpdateSelectionPolicy{
		Operator: op, SlotId: 1, SelectionRateBps: 111, MaxSelectedParticipants: 11,
	})
	require.NoError(t, err)

	stored, err = k.GetSlot(firstCtx, 1)
	require.NoError(t, err)
	require.Equal(t, int64(10), stored.LastSelectionPolicyUpdateHeight)

	// One block short of the window.
	earlyCtx := ctx.WithBlockHeight(10 + configured - 1)
	_, err = msgs.UpdateSelectionPolicy(earlyCtx, &types.MsgUpdateSelectionPolicy{
		Operator: op, SlotId: 1, SelectionRateBps: 222, MaxSelectedParticipants: 12,
	})
	require.ErrorIs(t, err, types.ErrSelectionPolicyCooldown)
	require.False(t, hasPolicyRow(t, k, earlyCtx, 1, 3), "a cooled-down update writes nothing")

	// Exactly at the window.
	atCtx := ctx.WithBlockHeight(10 + configured)
	_, err = msgs.UpdateSelectionPolicy(atCtx, &types.MsgUpdateSelectionPolicy{
		Operator: op, SlotId: 1, SelectionRateBps: 222, MaxSelectedParticipants: 12,
	})
	require.NoError(t, err)
	stored, err = k.GetSlot(atCtx, 1)
	require.NoError(t, err)
	require.Equal(t, 10+configured, stored.LastSelectionPolicyUpdateHeight)
}

// --- overflow and atomicity ---

// TestSelectionPolicyUpdateRejectionsAreAtomic proves that every rejection path
// leaves the whole six-part transition unperformed, not partially applied.
func TestSelectionPolicyUpdateRejectionsAreAtomic(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(t *testing.T, k keeper.Keeper, ctx sdk.Context) sdk.Context
		wantErr error
	}{
		{
			name: "effective height overflows int64",
			prepare: func(t *testing.T, k keeper.Keeper, ctx sdk.Context) sdk.Context {
				return ctx.WithBlockHeight(math.MaxInt64)
			},
			wantErr: types.ErrInvalidSelectionPolicy,
		},
		{
			name: "policy version space is exhausted",
			prepare: func(t *testing.T, k keeper.Keeper, ctx sdk.Context) sdk.Context {
				saturated := policyRow(t, k, ctx, 1, 1)
				saturated.PolicyVersion = math.MaxUint64
				require.NoError(t, k.SelectionPolicies.Set(ctx, collections.Join(uint64(1), uint64(math.MaxUint64)), saturated))
				stored, err := k.GetSlot(ctx, 1)
				require.NoError(t, err)
				stored.CurrentSelectionPolicyVersion = math.MaxUint64
				require.NoError(t, k.Slots.Set(ctx, 1, stored))
				return ctx.WithBlockHeight(10)
			},
			wantErr: types.ErrInvalidSelectionPolicy,
		},
		{
			name: "cooldown window overflows",
			prepare: func(t *testing.T, k keeper.Keeper, ctx sdk.Context) sdk.Context {
				stored, err := k.GetSlot(ctx, 1)
				require.NoError(t, err)
				stored.LastSelectionPolicyUpdateHeight = math.MaxInt64
				require.NoError(t, k.Slots.Set(ctx, 1, stored))
				return ctx.WithBlockHeight(10)
			},
			wantErr: types.ErrSelectionPolicyCooldown,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx, authority, emergency := setup(t)
			op := policySlotGenesis(t, k, ctx, authority, emergency)
			msgs := keeper.NewMsgServer(k)

			// The fixture itself may seed state (a saturated version, an extreme
			// last-update height), so the baseline is taken AFTER preparation —
			// otherwise the assertions would compare against a state the test
			// deliberately replaced and fail for its own reason rather than the
			// production one.
			runCtx := tc.prepare(t, k, ctx)
			before, err := k.GetSlot(runCtx, 1)
			require.NoError(t, err)
			beforeCurrent := policyRow(t, k, runCtx, 1, before.CurrentSelectionPolicyVersion)

			_, err = msgs.UpdateSelectionPolicy(runCtx, &types.MsgUpdateSelectionPolicy{
				Operator: op, SlotId: 1, SelectionRateBps: 999, MaxSelectedParticipants: 99,
			})
			require.ErrorIs(t, err, tc.wantErr)

			// Nothing of the transition survived.
			after, err := k.GetSlot(runCtx, 1)
			require.NoError(t, err)
			require.Equal(t, before.CurrentSelectionPolicyVersion, after.CurrentSelectionPolicyVersion, "pointer must not move")
			require.Equal(t, before.LastSelectionPolicyUpdateHeight, after.LastSelectionPolicyUpdateHeight,
				"last-update height must not move")
			require.Equal(t, beforeCurrent, policyRow(t, k, runCtx, 1, before.CurrentSelectionPolicyVersion),
				"the prior current row must be untouched, including its open interval")
			require.False(t, hasPolicyRow(t, k, runCtx, 1, 2), "no new policy row")
			require.False(t, hasSeekEntry(t, k, runCtx, 1, runCtx.BlockHeight()+1), "no new seek entry")
		})
	}
}

// --- seek index and height resolution ---

func TestSelectionPolicySeekIndexAndHeightResolution(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	op := policySlotGenesis(t, k, ctx, authority, emergency)
	msgs := keeper.NewMsgServer(k)

	// Genesis version 1 is indexed from the admitted row, not carried in genesis.
	require.True(t, hasSeekEntry(t, k, ctx, 1, testInitialHeight), "fresh-genesis version 1 must be indexed")

	setCooldown(t, k, ctx, 360)
	heights := []int64{100, 500, 900}
	for i, h := range heights {
		updateCtx := ctx.WithBlockHeight(h)
		_, err := msgs.UpdateSelectionPolicy(updateCtx, &types.MsgUpdateSelectionPolicy{
			Operator: op, SlotId: 1, SelectionRateBps: uint64(100 * (i + 1)), MaxSelectedParticipants: uint64(10 + i),
		})
		require.NoError(t, err)
		require.True(t, hasSeekEntry(t, k, updateCtx, 1, h+1), "each new version must be indexed")
	}

	// Intervals: v1 [1,101), v2 [101,501), v3 [501,901), v4 [901,∞).
	for _, tc := range []struct {
		height  int64
		version uint64
	}{
		{1, 1}, {100, 1}, {101, 2}, {500, 2}, {501, 3}, {900, 3}, {901, 4}, {10_000, 4},
	} {
		policy, err := k.SelectionPolicyAtHeight(ctx, 1, tc.height)
		require.NoErrorf(t, err, "height %d", tc.height)
		require.Equalf(t, tc.version, policy.PolicyVersion, "height %d resolves to the containing interval", tc.height)
	}

	// Below the first version there is no policy — not version 1.
	_, err := k.SelectionPolicyAtHeight(ctx, 1, 0)
	require.ErrorIs(t, err, types.ErrSelectionPolicyNotFound)

	// An unknown slot has no policy.
	_, err = k.SelectionPolicyAtHeight(ctx, 404, 100)
	require.ErrorIs(t, err, types.ErrSelectionPolicyNotFound)
}

// TestSelectionPolicyResolutionDoesNotCrossSlots proves the predecessor seek is
// prefixed by slot: a neighboring slot's later start must never satisfy a query.
func TestSelectionPolicyResolutionDoesNotCrossSlots(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	params := types.DefaultParams(authority, emergency)
	ops := makeOps(140, 2)
	_, err := initGenesis(t, k, ctx, &types.GenesisState{
		Params: &params, NextSlotId: 3,
		Slots: []*types.CoreSlot{
			slot(t, 1, ops[0], 1, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
			slot(t, 2, ops[1], 2, types.SlotStatus_SLOT_STATUS_ACTIVE, 1),
		},
	})
	require.NoError(t, err)

	msgs := keeper.NewMsgServer(k)
	updateCtx := ctx.WithBlockHeight(200)
	_, err = msgs.UpdateSelectionPolicy(updateCtx, &types.MsgUpdateSelectionPolicy{
		Operator: ops[1], SlotId: 2, SelectionRateBps: 777, MaxSelectedParticipants: 7,
	})
	require.NoError(t, err)

	// Slot 1 has only version 1; slot 2's version 2 must not leak into its answer.
	policy, err := k.SelectionPolicyAtHeight(ctx, 1, 500)
	require.NoError(t, err)
	require.Equal(t, uint64(1), policy.PolicyVersion)
	require.Equal(t, uint64(1), policy.SlotId)
}

// TestSelectionPolicyIndexDivergenceFailsClosed proves the resolver refuses to
// paper over a mismatch: the index is derived state, so a disagreement means the
// two have diverged, and repairing or scanning around it would hide that.
func TestSelectionPolicyIndexDivergenceFailsClosed(t *testing.T) {
	t.Run("index names a missing version", func(t *testing.T) {
		k, ctx, authority, emergency := setup(t)
		policySlotGenesis(t, k, ctx, authority, emergency)
		require.NoError(t, k.PolicyStarts.Set(ctx, collections.Join(uint64(1), int64(5)), uint64(99)))
		_, err := k.SelectionPolicyAtHeight(ctx, 1, 10)
		require.ErrorIs(t, err, types.ErrInvalidSelectionPolicy)
		require.Contains(t, err.Error(), "missing version")
	})

	t.Run("index entry disagrees with the row", func(t *testing.T) {
		k, ctx, authority, emergency := setup(t)
		policySlotGenesis(t, k, ctx, authority, emergency)
		// An index entry at a start height the named version does not carry.
		require.NoError(t, k.PolicyStarts.Set(ctx, collections.Join(uint64(1), int64(7)), uint64(1)))
		_, err := k.SelectionPolicyAtHeight(ctx, 1, 10)
		require.ErrorIs(t, err, types.ErrInvalidSelectionPolicy)
		require.Contains(t, err.Error(), "disagrees")
	})

	t.Run("named version does not contain the height", func(t *testing.T) {
		k, ctx, authority, emergency := setup(t)
		policySlotGenesis(t, k, ctx, authority, emergency)
		// Close version 1 without creating a successor, then ask past its end.
		closed := policyRow(t, k, ctx, 1, 1)
		closed.ValidUntilHeightExclusive = 20
		require.NoError(t, k.SelectionPolicies.Set(ctx, collections.Join(uint64(1), uint64(1)), closed))
		_, err := k.SelectionPolicyAtHeight(ctx, 1, 50)
		require.ErrorIs(t, err, types.ErrInvalidSelectionPolicy)
		require.Contains(t, err.Error(), "does not contain")
	})
}

// TestSelectionPolicyResolutionDoesNotScanHistory is the complexity guard. It
// corrupts a dormant historical row that no seek can reach for the queried
// height: a resolver that walked the whole history would have to decode it and
// fail, while the seek path never touches it.
func TestSelectionPolicyResolutionDoesNotScanHistory(t *testing.T) {
	k, ctx, authority, emergency, storeKey := setupWithRawStore(t)
	params := types.DefaultParams(authority, emergency)
	op := sdk.AccAddress(append([]byte{2}, make([]byte, 19)...)).String()
	_, err := initGenesis(t, k, ctx, &types.GenesisState{
		Params: &params, NextSlotId: 2,
		Slots: []*types.CoreSlot{slot(t, 1, op, 1, types.SlotStatus_SLOT_STATUS_ACTIVE, 1)},
	})
	require.NoError(t, err)

	msgs := keeper.NewMsgServer(k)
	setCooldown(t, k, ctx, 360)
	updateCtx := ctx.WithBlockHeight(1_000)
	_, err = msgs.UpdateSelectionPolicy(updateCtx, &types.MsgUpdateSelectionPolicy{
		Operator: op, SlotId: 1, SelectionRateBps: 321, MaxSelectedParticipants: 21,
	})
	require.NoError(t, err)

	// Corrupt version 1's row in place. Version 2 covers height 2000.
	store := ctx.KVStore(storeKey)
	rawKey := append(append([]byte{}, types.SelectionPoliciesPrefix...), policyRawKey(1, 1)...)
	require.NotNil(t, store.Get(rawKey), "the row must exist before it is corrupted")
	store.Set(rawKey, []byte{0xff, 0xff, 0xff, 0xff})

	require.Error(t, k.SelectionPolicies.Walk(ctx, nil, func(collections.Pair[uint64, uint64], types.SelectionPolicyVersion) (bool, error) {
		return false, nil
	}), "a scan over the whole history must fail on the corrupted row")

	policy, err := k.SelectionPolicyAtHeight(ctx, 1, 2_000)
	require.NoError(t, err, "height resolution must not read unrelated history rows")
	require.Equal(t, uint64(2), policy.PolicyVersion)
}

// policyRawKey builds the collections key bytes for a (slot, version) pair:
// big-endian uint64 components, matching collections.PairKeyCodec.
func policyRawKey(slotID, version uint64) []byte {
	out := make([]byte, 0, 16)
	for _, v := range []uint64{slotID, version} {
		out = append(out, byte(v>>56), byte(v>>48), byte(v>>40), byte(v>>32),
			byte(v>>24), byte(v>>16), byte(v>>8), byte(v))
	}
	return out
}

// --- query surface ---

func TestSelectionPolicyQueries(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	op := policySlotGenesis(t, k, ctx, authority, emergency)
	msgs := keeper.NewMsgServer(k)
	qs := keeper.NewQueryServer(k)

	setCooldown(t, k, ctx, 360)
	updateCtx := ctx.WithBlockHeight(400)
	_, err := msgs.UpdateSelectionPolicy(updateCtx, &types.MsgUpdateSelectionPolicy{
		Operator: op, SlotId: 1, SelectionRateBps: 4_321, MaxSelectedParticipants: 33,
	})
	require.NoError(t, err)

	current, err := qs.SelectionPolicy(updateCtx, &types.QuerySelectionPolicyRequest{SlotId: 1})
	require.NoError(t, err)
	require.Equal(t, uint64(2), current.Policy.PolicyVersion)
	require.Equal(t, uint64(4_321), current.Policy.SelectionRateBps)

	exact, err := qs.SelectionPolicyVersion(updateCtx, &types.QuerySelectionPolicyVersionRequest{SlotId: 1, PolicyVersion: 1})
	require.NoError(t, err)
	require.Equal(t, uint64(1), exact.Policy.PolicyVersion)
	require.Equal(t, int64(401), exact.Policy.ValidUntilHeightExclusive)

	_, err = qs.SelectionPolicyVersion(updateCtx, &types.QuerySelectionPolicyVersionRequest{SlotId: 1, PolicyVersion: 99})
	require.ErrorIs(t, err, types.ErrSelectionPolicyNotFound)
	_, err = qs.SelectionPolicyVersion(updateCtx, &types.QuerySelectionPolicyVersionRequest{SlotId: 404, PolicyVersion: 1})
	require.ErrorIs(t, err, types.ErrSelectionPolicyNotFound)

	// Boundaries through the public query path.
	for _, tc := range []struct {
		height  int64
		version uint64
	}{{1, 1}, {400, 1}, {401, 2}, {5_000, 2}} {
		resp, err := qs.SelectionPolicyAtHeight(updateCtx, &types.QuerySelectionPolicyAtHeightRequest{SlotId: 1, AtHeight: tc.height})
		require.NoErrorf(t, err, "height %d", tc.height)
		require.Equalf(t, tc.version, resp.Policy.PolicyVersion, "height %d", tc.height)
	}

	_, err = qs.SelectionPolicyAtHeight(updateCtx, &types.QuerySelectionPolicyAtHeightRequest{SlotId: 1, AtHeight: 0})
	require.ErrorIs(t, err, types.ErrSelectionPolicyNotFound)

	_, err = qs.SelectionPolicy(updateCtx, &types.QuerySelectionPolicyRequest{SlotId: 404})
	require.ErrorIs(t, err, types.ErrSlotNotFound)
}

// --- local validity ---

func TestSelectionPolicyUpdateLocalValidity(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	op := policySlotGenesis(t, k, ctx, authority, emergency)
	msgs := keeper.NewMsgServer(k)

	for _, tc := range []struct {
		name        string
		rate        uint64
		maxSelected uint64
		valid       bool
	}{
		{"zero rate", 0, 10, false},
		{"rate above the absolute ceiling", appparams.AbsoluteMaxSelectionRateBps + 1, 10, false},
		{"rate at the absolute ceiling", appparams.AbsoluteMaxSelectionRateBps, 10, true},
		{"zero participants", 2_500, 0, false},
		{"representative positive participants", 1_000, 25, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Each case gets its own keeper so an accepted update does not consume
			// the one free post-registration update for the next case.
			k, ctx, authority, emergency := setup(t)
			op := policySlotGenesis(t, k, ctx, authority, emergency)
			msgs := keeper.NewMsgServer(k)
			_, err := msgs.UpdateSelectionPolicy(ctx, &types.MsgUpdateSelectionPolicy{
				Operator: op, SlotId: 1, SelectionRateBps: tc.rate, MaxSelectedParticipants: tc.maxSelected,
			})
			if tc.valid {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, types.ErrInvalidSelectionPolicy)
		})
	}

	// No global ceiling is consulted: nothing here decides how extreme positive
	// participant maxima are treated, which remains an unresolved envelope
	// question outside this change.
	_ = op
	_ = msgs
	_ = k
	_ = ctx
	_ = authority
	_ = emergency
}
