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
				// The whole history must move with the pointer, index included.
				// Leaving version 1 and its index entry behind would make this a
				// divergence fixture instead: the transition seam would reject it for
				// an incoherent index and the version arithmetic under test would
				// never run.
				require.NoError(t, k.SelectionPolicies.Remove(ctx, collections.Join(uint64(1), uint64(1))))
				require.NoError(t, k.PolicyStarts.Set(ctx, collections.Join(uint64(1), saturated.ValidFromHeight), uint64(math.MaxUint64)))
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

// TestSelectionPolicyCooldownRejectsUnrepresentableConfiguredValue covers a
// configured cooldown that fits uint64 but cannot be represented as a block
// height.
//
// The value is legal as a parameter: 720 is a default and 360 a floor, and
// neither is a maximum, so nothing in Params.Validate stops governance from
// configuring one this large. Admission is where it has to be refused, and it
// must be refused rather than narrowed — a wrapped conversion would silently
// become a tiny or negative window and disable the rate limit entirely.
func TestSelectionPolicyCooldownRejectsUnrepresentableConfiguredValue(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	op := policySlotGenesis(t, k, ctx, authority, emergency)
	msgs := keeper.NewMsgServer(k)

	const unrepresentable = uint64(math.MaxInt64) + 1
	admissible := types.DefaultParams(authority, emergency)
	admissible.SelectionPolicyUpdateCooldownBlocks = unrepresentable
	require.NoError(t, admissible.Validate(),
		"a cooldown above the int64 height domain is still a valid parameter — the bound is a floor, not a ceiling")
	setCooldown(t, k, ctx, unrepresentable)

	// First update: the last-update height is zero, which §26 exempts, so the
	// cooldown is never consulted and no conversion is required.
	firstCtx := ctx.WithBlockHeight(10)
	_, err := msgs.UpdateSelectionPolicy(firstCtx, &types.MsgUpdateSelectionPolicy{
		Operator: op, SlotId: 1, SelectionRateBps: 111, MaxSelectedParticipants: 11,
	})
	require.NoError(t, err, "the first post-registration update does not consult the cooldown")

	// Second update: the conversion is now required and must fail closed.
	secondCtx := ctx.WithBlockHeight(1_000_000).WithEventManager(sdk.NewEventManager())
	before := snapshotPolicyState(t, k, secondCtx, 1)
	_, err = msgs.UpdateSelectionPolicy(secondCtx, &types.MsgUpdateSelectionPolicy{
		Operator: op, SlotId: 1, SelectionRateBps: 222, MaxSelectedParticipants: 12,
	})
	require.ErrorIs(t, err, types.ErrInvalidParams)
	require.Contains(t, err.Error(), "not representable")
	require.Equal(t, before, snapshotPolicyState(t, k, secondCtx, 1), "a rejected conversion must write nothing")
	require.Zero(t, countEvents(secondCtx, types.EventTypeSelectionPolicyUpdated))
}

// --- malformed transition seam ---

// policyStateSnapshot captures everything a policy update would write for one
// slot: the record that holds the pointer and the cooldown height, the whole
// policy history, and the whole seek index. Comparing two snapshots is how a
// rejection is shown to be a NO-mutation rejection rather than a partly applied
// one — including the writes a targeted assertion would not think to look for.
type policyStateSnapshot struct {
	slot     types.CoreSlot
	policies map[uint64]types.SelectionPolicyVersion
	starts   map[int64]uint64
}

func snapshotPolicyState(t *testing.T, k keeper.Keeper, ctx sdk.Context, slotID uint64) policyStateSnapshot {
	t.Helper()
	snapshot := policyStateSnapshot{
		policies: map[uint64]types.SelectionPolicyVersion{},
		starts:   map[int64]uint64{},
	}
	var err error
	snapshot.slot, err = k.GetSlot(ctx, slotID)
	require.NoError(t, err)
	require.NoError(t, k.SelectionPolicies.Walk(ctx, collections.NewPrefixedPairRange[uint64, uint64](slotID),
		func(key collections.Pair[uint64, uint64], policy types.SelectionPolicyVersion) (bool, error) {
			snapshot.policies[key.K2()] = policy
			return false, nil
		}))
	require.NoError(t, k.PolicyStarts.Walk(ctx, collections.NewPrefixedPairRange[uint64, int64](slotID),
		func(key collections.Pair[uint64, int64], version uint64) (bool, error) {
			snapshot.starts[key.K2()] = version
			return false, nil
		}))
	return snapshot
}

// TestSelectionPolicyUpdateRejectsMalformedTransitionSeam proves each seam of the
// transition is independently load-bearing.
//
// The writes are unconditional Sets: closing the outgoing row, occupying the next
// history key, occupying the new index key. Against malformed state each one is a
// way to destroy something immutable — reclosing an already-closed version,
// overwriting a version that exists, repointing a start height already claimed.
// Every case here is therefore given its OWN fixture rather than folded into a
// single "corrupted state" scenario: one shared fixture would let a single
// surviving check mask the removal of any of the others.
func TestSelectionPolicyUpdateRejectsMalformedTransitionSeam(t *testing.T) {
	// Each fixture corrupts one seam of a healthy slot 1 and returns the height the
	// update is attempted at.
	for _, tc := range []struct {
		name       string
		corrupt    func(t *testing.T, k keeper.Keeper, ctx sdk.Context)
		wantErr    error
		wantDetail string
	}{
		{
			name: "the pointer names an already-closed version",
			corrupt: func(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
				closed := policyRow(t, k, ctx, 1, 1)
				closed.ValidUntilHeightExclusive = 100
				require.NoError(t, k.SelectionPolicies.Set(ctx, collections.Join(uint64(1), uint64(1)), closed))
			},
			wantErr:    types.ErrInvalidSelectionPolicy,
			wantDetail: "already closed",
		},
		{
			name: "the next history key is already occupied",
			corrupt: func(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
				// A row already sitting where the successor would land. It carries no
				// index entry, so the current version still resolves and this is the
				// only seam under test.
				require.NoError(t, k.SelectionPolicies.Set(ctx, collections.Join(uint64(1), uint64(2)),
					types.SelectionPolicyVersion{
						SlotId: 1, PolicyVersion: 2, SelectionRateBps: 999, MaxSelectedParticipants: 9,
						ValidFromHeight: 5_000,
					}))
			},
			wantErr:    types.ErrInvalidSelectionPolicy,
			wantDetail: "already has a policy row at version 2",
		},
		{
			name: "the new seek key is already occupied",
			corrupt: func(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
				// The update runs at height 10, so its version would start at 11.
				// The entry is above the queried height, so current resolution is
				// untouched and only the vacancy check can reject this.
				require.NoError(t, k.PolicyStarts.Set(ctx, collections.Join(uint64(1), int64(11)), uint64(42)))
			},
			wantErr:    types.ErrInvalidSelectionPolicy,
			wantDetail: "already has a policy index entry starting at height 11",
		},
		{
			name: "the current version has no seek entry",
			corrupt: func(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
				require.NoError(t, k.PolicyStarts.Remove(ctx, collections.Join(uint64(1), testInitialHeight)))
			},
			wantErr:    types.ErrInvalidSelectionPolicy,
			wantDetail: "does not resolve at height 10",
		},
		{
			name: "the current seek entry names a different version",
			corrupt: func(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
				require.NoError(t, k.PolicyStarts.Set(ctx, collections.Join(uint64(1), testInitialHeight), uint64(3)))
			},
			wantErr:    types.ErrInvalidSelectionPolicy,
			wantDetail: "does not resolve at height 10",
		},
		{
			name: "a rogue later start shadows the current version",
			corrupt: func(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
				// A well-formed but unreferenced version starting after the current
				// one. History and pointer agree with each other; the INDEX is what
				// disagrees, and it is the index every height query trusts.
				require.NoError(t, k.SelectionPolicies.Set(ctx, collections.Join(uint64(1), uint64(7)),
					types.SelectionPolicyVersion{
						SlotId: 1, PolicyVersion: 7, SelectionRateBps: 3_000, MaxSelectedParticipants: 5,
						ValidFromHeight: 5,
					}))
				require.NoError(t, k.PolicyStarts.Set(ctx, collections.Join(uint64(1), int64(5)), uint64(7)))
			},
			wantErr:    types.ErrInvalidSelectionPolicy,
			wantDetail: "resolves version 7 at height 10 but the slot points at version 1",
		},
		{
			name: "the stored current policy is locally invalid",
			corrupt: func(t *testing.T, k keeper.Keeper, ctx sdk.Context) {
				invalid := policyRow(t, k, ctx, 1, 1)
				invalid.MaxSelectedParticipants = 0
				require.NoError(t, k.SelectionPolicies.Set(ctx, collections.Join(uint64(1), uint64(1)), invalid))
			},
			wantErr:    types.ErrInvalidSelectionPolicy,
			wantDetail: "stored current policy version 1 is invalid",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, ctx, authority, emergency := setup(t)
			op := policySlotGenesis(t, k, ctx, authority, emergency)
			msgs := keeper.NewMsgServer(k)

			tc.corrupt(t, k, ctx)

			// The baseline is taken AFTER corruption: the corrupted values are part
			// of the state that must survive untouched, and a rejection that
			// "repaired" one of them would be a mutation too.
			runCtx := ctx.WithBlockHeight(10).WithEventManager(sdk.NewEventManager())
			before := snapshotPolicyState(t, k, runCtx, 1)

			_, err := msgs.UpdateSelectionPolicy(runCtx, &types.MsgUpdateSelectionPolicy{
				Operator: op, SlotId: 1, SelectionRateBps: 1_111, MaxSelectedParticipants: 11,
			})
			require.ErrorIs(t, err, tc.wantErr)
			require.Contains(t, err.Error(), tc.wantDetail,
				"the rejection must name the seam it refused, not merely fail somewhere")

			after := snapshotPolicyState(t, k, runCtx, 1)
			require.Equal(t, before, after, "a malformed seam must leave history, index and record byte-identical")
			// Named explicitly as well, because the struct comparison above would
			// still pass if a future refactor narrowed what the snapshot captures.
			require.Equal(t, before.slot.CurrentSelectionPolicyVersion, after.slot.CurrentSelectionPolicyVersion,
				"the pointer must not move")
			require.Equal(t, before.slot.LastSelectionPolicyUpdateHeight, after.slot.LastSelectionPolicyUpdateHeight,
				"the cooldown height must not move")
			// Stated by count as well as by value, because one fixture pre-plants an
			// entry at the height a new version would have claimed: "unchanged" there
			// means the planted entry survives, not that no entry exists.
			require.Len(t, after.starts, len(before.starts), "no new seek entry")
			require.Len(t, after.policies, len(before.policies), "no new policy row")
			require.Zero(t, countEvents(runCtx, types.EventTypeSelectionPolicyUpdated),
				"a rejected update must not announce a version that was never written")
		})
	}
}

// TestSelectionPolicyUpdateNeverReopensAClosedVersion is the same rule stated as
// the outcome it protects: once a version's exclusive end is set, no later update
// may move it, whatever the pointer says.
func TestSelectionPolicyUpdateNeverReopensAClosedVersion(t *testing.T) {
	k, ctx, authority, emergency := setup(t)
	op := policySlotGenesis(t, k, ctx, authority, emergency)
	msgs := keeper.NewMsgServer(k)

	// A genuine update, so version 1 is closed the way production closes it.
	updateCtx := ctx.WithBlockHeight(50)
	_, err := msgs.UpdateSelectionPolicy(updateCtx, &types.MsgUpdateSelectionPolicy{
		Operator: op, SlotId: 1, SelectionRateBps: 1_234, MaxSelectedParticipants: 42,
	})
	require.NoError(t, err)
	closed := policyRow(t, k, updateCtx, 1, 1)
	require.Equal(t, int64(51), closed.ValidUntilHeightExclusive)

	// Now roll the pointer back to the closed version, as a corrupt migration or a
	// bad genesis import could.
	stored, err := k.GetSlot(updateCtx, 1)
	require.NoError(t, err)
	stored.CurrentSelectionPolicyVersion = 1
	stored.LastSelectionPolicyUpdateHeight = 0
	require.NoError(t, k.Slots.Set(updateCtx, 1, stored))

	laterCtx := ctx.WithBlockHeight(5_000).WithEventManager(sdk.NewEventManager())
	before := snapshotPolicyState(t, k, laterCtx, 1)
	_, err = msgs.UpdateSelectionPolicy(laterCtx, &types.MsgUpdateSelectionPolicy{
		Operator: op, SlotId: 1, SelectionRateBps: 4_321, MaxSelectedParticipants: 43,
	})
	require.ErrorIs(t, err, types.ErrInvalidSelectionPolicy)
	require.Equal(t, closed, policyRow(t, k, laterCtx, 1, 1),
		"a closed version's end must never be rewritten, not even to a later height")
	require.Equal(t, before, snapshotPolicyState(t, k, laterCtx, 1))
	require.Zero(t, countEvents(laterCtx, types.EventTypeSelectionPolicyUpdated))
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

// TestSelectionPolicyResolutionDoesNotScanHistory is the complexity guard: height
// resolution must not regress to a cost proportional to the number of versions a
// slot has ever had.
//
// A guard that only corrupts an OLD version proves less than it appears to. It
// rejects an ascending full-history walk, but a descending walk that starts at
// the newest version and stops at the first containing row would sail past it and
// still pass — so the test would certify a linear implementation as index-bounded.
//
// This fixture therefore builds five versions, targets one in the MIDDLE, and
// corrupts the rows on BOTH sides of it. Whichever direction a linear scan runs,
// it must decode a corrupted row before reaching the target and fail; the seek
// index reads exactly one row — the target — and never sees them.
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
	// Versions 2..5, so the target is genuinely interior rather than adjacent to
	// either end of the history. Intervals: v1 [1,1001), v2 [1001,2001),
	// v3 [2001,3001), v4 [3001,4001), v5 [4001,∞).
	for _, h := range []int64{1_000, 2_000, 3_000, 4_000} {
		_, err = msgs.UpdateSelectionPolicy(ctx.WithBlockHeight(h), &types.MsgUpdateSelectionPolicy{
			Operator: op, SlotId: 1, SelectionRateBps: uint64(h), MaxSelectedParticipants: uint64(h / 100),
		})
		require.NoErrorf(t, err, "update at height %d", h)
	}

	const targetHeight = int64(2_500) // inside version 3
	want := policyRow(t, k, ctx, 1, 3)
	require.Equal(t, int64(2_001), want.ValidFromHeight)
	require.Equal(t, int64(3_001), want.ValidUntilHeightExclusive)

	// Corrupt every version except the target, as raw bytes: a value the typed API
	// cannot decode is the only way to express "reading this row is itself the
	// failure", which is what makes the direction of a scan observable.
	store := ctx.KVStore(storeKey)
	for _, version := range []uint64{1, 2, 4, 5} {
		rawKey := append(append([]byte{}, types.SelectionPoliciesPrefix...), policyRawKey(1, version)...)
		require.NotNilf(t, store.Get(rawKey), "version %d must exist before it is corrupted", version)
		store.Set(rawKey, []byte{0xff, 0xff, 0xff, 0xff})
	}

	// Both linear directions are now unusable, which is the premise the guard
	// rests on. If either of these stopped failing the assertions below would
	// prove nothing.
	require.Error(t, walkPolicyHistory(t, k, ctx, 1, false),
		"an ascending history scan must fail on a corrupted row before the target")
	require.Error(t, walkPolicyHistory(t, k, ctx, 1, true),
		"a descending history scan must fail on a corrupted row before the target")

	policy, err := k.SelectionPolicyAtHeight(ctx, 1, targetHeight)
	require.NoError(t, err, "height resolution must read only the row the seek index names")
	require.Equal(t, want, policy)

	// The public query path inherits the same bound.
	resp, err := keeper.NewQueryServer(k).SelectionPolicyAtHeight(ctx, &types.QuerySelectionPolicyAtHeightRequest{
		SlotId: 1, AtHeight: targetHeight,
	})
	require.NoError(t, err)
	require.Equal(t, want, *resp.Policy)
}

// walkPolicyHistory reads a slot's policy history linearly in one direction until
// a row fails to decode. It is the shape of the two implementations the guard
// above must reject — an ascending scan and a descending one — kept here so the
// test states what it is ruling out rather than implying it.
func walkPolicyHistory(t *testing.T, k keeper.Keeper, ctx sdk.Context, slotID uint64, descending bool) error {
	t.Helper()
	rng := collections.NewPrefixedPairRange[uint64, uint64](slotID)
	if descending {
		rng = rng.Descending()
	}
	iter, err := k.SelectionPolicies.Iterate(ctx, rng)
	require.NoError(t, err)
	defer iter.Close()
	for ; iter.Valid(); iter.Next() {
		if _, err := iter.Value(); err != nil {
			return err
		}
	}
	return nil
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
