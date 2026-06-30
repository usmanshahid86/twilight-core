package app_test

// CoreSlot lifecycle simulation.
//
// A seeded, model-based randomized state-machine test (deterministic property
// fuzzing). Each run drives a long random sequence of authority/operator/
// emergency lifecycle operations (register / activate / inactivate / suspend /
// remove), interleaved with real CoreSlot EndBlock validator-set diffs, against
// the assembled app. A reference model of slot statuses is kept in lockstep with
// the chain; every operation's expected success/failure is predicted from the
// model and asserted, and after every step the CoreSlot conservation properties
// are checked:
//
//   - active-set power conservation: Σ power over ACTIVE == activeCount × SlotVotingPower
//   - the MinActiveSlots floor is never violated
//   - no duplicate consensus keys among ACTIVE slots
//   - the model status matches chain status for every slot
//   - removed/suspended slots retain their slot row and reward-weight row
//   - each EndBlock emits exactly the validator updates implied by the active-key
//     diff since the previous block (one update per added/removed key)
//
// The seed is fixed per subtest, so any failure reproduces deterministically.
// Key rotation is intentionally out of scope here (its delayed-apply path has
// dedicated coverage in x/coreslot keeper tests); this sim targets the lifecycle
// state graph and the validator-set diff.

import (
	"fmt"
	"math/rand"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/app"
	coreslotkeeper "github.com/twilight-project/twilight-core/x/coreslot/keeper"
	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	rewardstypes "github.com/twilight-project/twilight-core/x/rewards/types"
)

const (
	simSlotVotingPower = int64(1) // matches coreslottypes.DefaultParams
	simMinActiveSlots  = 1
)

type modelSlot struct {
	id        uint64
	operator  string
	payout    string
	keyMarker byte
	status    coreslottypes.SlotStatus
}

func (m *modelSlot) active() bool {
	return m.status == coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE
}

func TestCoreSlotLifecycleSimulation(t *testing.T) {
	for seed := int64(1); seed <= 8; seed++ {
		seed := seed
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			runCoreSlotSim(t, seed, 300)
		})
	}
}

func runCoreSlotSim(t *testing.T, seed int64, steps int) {
	rng := rand.New(rand.NewSource(seed))
	a := bootApp(t)
	base := a.NewUncachedContext(false, cmtproto.Header{Height: 1})

	// Disjoint marker namespaces so addresses and consensus keys never collide.
	var nextKeyMarker byte = 1
	var nextOpMarker byte = 40
	var nextPayMarker byte = 140
	newKey := func() byte { m := nextKeyMarker; nextKeyMarker++; return m }
	newOp := func() string { m := nextOpMarker; nextOpMarker++; return acc(m) }
	newPay := func() string { m := nextPayMarker; nextPayMarker++; return acc(m) }

	// Seed 3 initial ACTIVE slots through the real genesis baker.
	var specs []slotSpec
	model := map[uint64]*modelSlot{}
	for i := uint64(1); i <= 3; i++ {
		km := newKey()
		op, pay := newOp(), newPay()
		specs = append(specs, slotSpec{id: i, operator: op, payout: pay, keyMarker: km})
		model[i] = &modelSlot{id: i, operator: op, payout: pay, keyMarker: km, status: coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE}
	}
	rp, snap := rewardsParams(t, func(p *rewardstypes.Params) {})
	rGen := genesisState(rp, snap)
	initCoreSlotsAndRewards(t, a, base, specs, rGen)
	nextSlotID := uint64(4)

	// `applied` mirrors the validator set as of the last EndBlock (keyMarker -> power).
	applied := map[byte]int64{}
	for _, s := range model {
		if s.active() {
			applied[s.keyMarker] = simSlotVotingPower
		}
	}

	msgSrv := coreslotkeeper.NewMsgServer(a.CoreSlotKeeper)
	auth := app.AuthorityAddress()
	emer := app.EmergencyAuthorityAddress()
	height := int64(1)
	ctx := base.WithBlockHeight(height)

	checkInvariants(t, a, ctx, model, seed)

	for step := 0; step < steps; step++ {
		switch rng.Intn(7) {
		case 0, 1: // advance a block: EndBlock applies the net validator diff
			ctx = base.WithBlockHeight(height)
			updates, err := a.CoreSlotKeeper.EndBlock(ctx)
			require.NoErrorf(t, err, "seed %d step %d: EndBlock", seed, step)
			assertValidatorDiff(t, updates, applied, desiredSet(model), seed, step)
			applied = desiredSet(model)
			height++
			ctx = base.WithBlockHeight(height)

		case 2: // register -> PENDING (always valid: fresh operator + key)
			km, op, pay := newKey(), newOp(), newPay()
			_, err := msgSrv.RegisterCoreSlot(ctx, &coreslottypes.MsgRegisterCoreSlot{
				Authority: auth, OperatorAddress: op, PayoutAddress: pay,
				ConsensusPubkey: ed25519Any(t, km),
				Metadata:        &coreslottypes.OperatorMetadata{Moniker: fmt.Sprintf("sim-%d", nextSlotID)},
			})
			require.NoErrorf(t, err, "seed %d step %d: register", seed, step)
			model[nextSlotID] = &modelSlot{id: nextSlotID, operator: op, payout: pay, keyMarker: km, status: coreslottypes.SlotStatus_SLOT_STATUS_PENDING}
			nextSlotID++

		case 3: // activate a non-active slot. MaxActiveSlots (100) is unreachable from
			// 3 slots within this step budget, so activation always succeeds here.
			if s := pickByStatus(rng, model, coreslottypes.SlotStatus_SLOT_STATUS_PENDING, coreslottypes.SlotStatus_SLOT_STATUS_INACTIVE, coreslottypes.SlotStatus_SLOT_STATUS_SUSPENDED); s != nil {
				_, err := msgSrv.ActivateCoreSlot(ctx, &coreslottypes.MsgActivateCoreSlot{Authority: auth, SlotId: s.id})
				require.NoErrorf(t, err, "seed %d step %d: activate", seed, step)
				s.status = coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE
			}

		case 4: // inactivate an ACTIVE slot (authority or operator); floor-gated
			if s := pickByStatus(rng, model, coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE); s != nil {
				signer := auth
				if rng.Intn(2) == 0 {
					signer = s.operator
				}
				_, err := msgSrv.InactivateCoreSlot(ctx, &coreslottypes.MsgInactivateCoreSlot{AuthorityOrOperator: signer, SlotId: s.id, Reason: "sim"})
				if activeCount(model) <= simMinActiveSlots {
					require.Errorf(t, err, "seed %d step %d: inactivating last active must fail (MinActiveSlots)", seed, step)
				} else {
					require.NoErrorf(t, err, "seed %d step %d: inactivate", seed, step)
					s.status = coreslottypes.SlotStatus_SLOT_STATUS_INACTIVE
				}
			}

		case 5: // suspend a non-removed/non-suspended slot (authority or emergency)
			if s := pickByStatus(rng, model, coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE, coreslottypes.SlotStatus_SLOT_STATUS_PENDING, coreslottypes.SlotStatus_SLOT_STATUS_INACTIVE); s != nil {
				signer := emer
				if rng.Intn(2) == 0 {
					signer = auth
				}
				_, err := msgSrv.SuspendCoreSlot(ctx, &coreslottypes.MsgSuspendCoreSlot{Authority: signer, SlotId: s.id, Reason: "sim"})
				if s.active() && activeCount(model) <= simMinActiveSlots {
					require.Errorf(t, err, "seed %d step %d: suspending last active must fail", seed, step)
				} else {
					require.NoErrorf(t, err, "seed %d step %d: suspend", seed, step)
					s.status = coreslottypes.SlotStatus_SLOT_STATUS_SUSPENDED
				}
			}

		case 6: // remove a non-active, non-removed slot
			if s := pickByStatus(rng, model, coreslottypes.SlotStatus_SLOT_STATUS_PENDING, coreslottypes.SlotStatus_SLOT_STATUS_INACTIVE, coreslottypes.SlotStatus_SLOT_STATUS_SUSPENDED); s != nil {
				_, err := msgSrv.RemoveCoreSlot(ctx, &coreslottypes.MsgRemoveCoreSlot{Authority: auth, SlotId: s.id, Reason: "sim"})
				require.NoErrorf(t, err, "seed %d step %d: remove", seed, step)
				s.status = coreslottypes.SlotStatus_SLOT_STATUS_REMOVED
			}
		}

		checkInvariants(t, a, ctx, model, seed)
	}

	// Final block + invariants.
	ctx = base.WithBlockHeight(height)
	updates, err := a.CoreSlotKeeper.EndBlock(ctx)
	require.NoError(t, err)
	assertValidatorDiff(t, updates, applied, desiredSet(model), seed, steps)
	checkInvariants(t, a, base.WithBlockHeight(height+1), model, seed)
}

// TestCoreSlotGuardRejections covers the negative-path guards the random lifecycle
// sim does not exercise (each scenario isolates ONE guard, so require.ErrorIs is
// unambiguous): authority rejection, duplicate-consensus-key rejection, active-slot
// removal rejection, MaxActiveSlots pressure, and the F6 hard floor of one active
// validator made decisive via AllowEmergencyBelowMinActive.
func TestCoreSlotGuardRejections(t *testing.T) {
	auth := app.AuthorityAddress()
	emer := app.EmergencyAuthorityAddress()
	setup := func() (sdk.Context, coreslottypes.MsgServer) {
		a := bootApp(t)
		base := a.NewUncachedContext(false, cmtproto.Header{Height: 1})
		specs := []slotSpec{
			{id: 1, operator: acc(2), payout: acc(12), keyMarker: 1},
			{id: 2, operator: acc(3), payout: acc(13), keyMarker: 2},
			{id: 3, operator: acc(4), payout: acc(14), keyMarker: 3},
		}
		rp, snap := rewardsParams(t, func(p *rewardstypes.Params) {})
		initCoreSlotsAndRewards(t, a, base, specs, genesisState(rp, snap))
		return base.WithBlockHeight(1), coreslotkeeper.NewMsgServer(a.CoreSlotKeeper)
	}

	t.Run("authority-rejection", func(t *testing.T) {
		ctx, srv := setup()
		_, err := srv.RegisterCoreSlot(ctx, &coreslottypes.MsgRegisterCoreSlot{
			Authority: acc(99), OperatorAddress: acc(50), PayoutAddress: acc(150),
			ConsensusPubkey: ed25519Any(t, 50), Metadata: &coreslottypes.OperatorMetadata{Moniker: "x"},
		})
		require.ErrorIs(t, err, coreslottypes.ErrUnauthorized, "non-authority register must be rejected")
		_, err = srv.ActivateCoreSlot(ctx, &coreslottypes.MsgActivateCoreSlot{Authority: acc(99), SlotId: 1})
		require.ErrorIs(t, err, coreslottypes.ErrUnauthorized, "non-authority activate must be rejected")
	})

	t.Run("duplicate-consensus-key", func(t *testing.T) {
		ctx, srv := setup()
		_, err := srv.RegisterCoreSlot(ctx, &coreslottypes.MsgRegisterCoreSlot{
			Authority: auth, OperatorAddress: acc(51), PayoutAddress: acc(151),
			ConsensusPubkey: ed25519Any(t, 1), // key marker 1 already belongs to slot 1
			Metadata:        &coreslottypes.OperatorMetadata{Moniker: "dup"},
		})
		require.ErrorIs(t, err, coreslottypes.ErrDuplicateConsensusKey)
	})

	t.Run("active-removal-rejected", func(t *testing.T) {
		ctx, srv := setup()
		_, err := srv.RemoveCoreSlot(ctx, &coreslottypes.MsgRemoveCoreSlot{Authority: auth, SlotId: 1, Reason: "x"})
		require.ErrorIs(t, err, coreslottypes.ErrInvalidTransition, "active slots must be inactivated/suspended before removal")
	})

	t.Run("max-active-slots", func(t *testing.T) {
		ctx, srv := setup()
		p := coreslottypes.DefaultParams(auth, emer)
		p.MaxActiveSlots = 4 // 3 active now -> room for exactly one more
		_, err := srv.UpdateParams(ctx, &coreslottypes.MsgUpdateParams{Authority: auth, Params: &p})
		require.NoError(t, err)
		for _, km := range []byte{64, 65} {
			_, err = srv.RegisterCoreSlot(ctx, &coreslottypes.MsgRegisterCoreSlot{
				Authority: auth, OperatorAddress: acc(km), PayoutAddress: acc(km + 100),
				ConsensusPubkey: ed25519Any(t, km), Metadata: &coreslottypes.OperatorMetadata{Moniker: "m"},
			})
			require.NoError(t, err)
		}
		_, err = srv.ActivateCoreSlot(ctx, &coreslottypes.MsgActivateCoreSlot{Authority: auth, SlotId: 4})
		require.NoError(t, err, "4th activation is at the cap and allowed")
		_, err = srv.ActivateCoreSlot(ctx, &coreslottypes.MsgActivateCoreSlot{Authority: auth, SlotId: 5})
		require.ErrorIs(t, err, coreslottypes.ErrMaxActiveSlots, "5th activation must exceed MaxActiveSlots")
	})

	t.Run("hard-floor-emergency-below-min", func(t *testing.T) {
		ctx, srv := setup()
		p := coreslottypes.DefaultParams(auth, emer)
		p.AllowEmergencyBelowMinActive = true // skips the MinActiveSlots check, so count<=1 is decisive
		_, err := srv.UpdateParams(ctx, &coreslottypes.MsgUpdateParams{Authority: auth, Params: &p})
		require.NoError(t, err)
		for _, id := range []uint64{2, 3} {
			_, e := srv.InactivateCoreSlot(ctx, &coreslottypes.MsgInactivateCoreSlot{AuthorityOrOperator: auth, SlotId: id, Reason: "x"})
			require.NoError(t, e)
		}
		_, err = srv.SuspendCoreSlot(ctx, &coreslottypes.MsgSuspendCoreSlot{Authority: emer, SlotId: 1, Reason: "x"})
		require.ErrorIs(t, err, coreslottypes.ErrCannotRemoveLastValidator, "the hard floor of one active validator must hold")
	})
}

// desiredSet is the validator set implied by the model's ACTIVE slots.
func desiredSet(model map[uint64]*modelSlot) map[byte]int64 {
	d := map[byte]int64{}
	for _, s := range model {
		if s.active() {
			d[s.keyMarker] = simSlotVotingPower
		}
	}
	return d
}

func activeCount(model map[uint64]*modelSlot) int {
	n := 0
	for _, s := range model {
		if s.active() {
			n++
		}
	}
	return n
}

// pickByStatus returns a random slot whose status is in the allowed set, or nil.
func pickByStatus(rng *rand.Rand, model map[uint64]*modelSlot, allowed ...coreslottypes.SlotStatus) *modelSlot {
	var cands []*modelSlot
	for _, s := range model {
		for _, st := range allowed {
			if s.status == st {
				cands = append(cands, s)
				break
			}
		}
	}
	if len(cands) == 0 {
		return nil
	}
	// Deterministic order before random pick (map iteration is unordered).
	for i := 0; i < len(cands); i++ {
		for j := i + 1; j < len(cands); j++ {
			if cands[j].id < cands[i].id {
				cands[i], cands[j] = cands[j], cands[i]
			}
		}
	}
	return cands[rng.Intn(len(cands))]
}

// assertValidatorDiff checks EndBlock emitted exactly the (key->power) changes
// between the previously-applied set and the new desired set.
func assertValidatorDiff(t *testing.T, updates []abci.ValidatorUpdate, applied, desired map[byte]int64, seed int64, step int) {
	t.Helper()
	expected := map[byte]int64{}
	for m, p := range desired {
		if applied[m] != p {
			expected[m] = p
		}
	}
	for m := range applied {
		if _, ok := desired[m]; !ok {
			expected[m] = 0 // removed -> power 0
		}
	}
	actual := map[byte]int64{}
	for _, u := range updates {
		k := u.PubKey.GetEd25519()
		require.NotEmptyf(t, k, "seed %d step %d: non-ed25519 validator update", seed, step)
		// Enforce the one-update-per-key oracle directly: a second update for the
		// same pubkey would otherwise silently overwrite in the map and be masked.
		_, dup := actual[k[0]]
		require.Falsef(t, dup, "seed %d step %d: EndBlock emitted a duplicate validator update for key %d", seed, step, k[0])
		actual[k[0]] = u.Power
	}
	require.Equalf(t, expected, actual, "seed %d step %d: validator-update diff mismatch", seed, step)
}

// checkInvariants asserts the CoreSlot conservation properties and model/chain
// agreement against the real keeper state.
func checkInvariants(t *testing.T, a *app.App, ctx sdk.Context, model map[uint64]*modelSlot, seed int64) {
	t.Helper()
	active, err := a.CoreSlotKeeper.GetActiveSlots(ctx)
	require.NoErrorf(t, err, "seed %d: GetActiveSlots", seed)

	require.Equalf(t, activeCount(model), len(active), "seed %d: active count (model vs chain)", seed)
	require.GreaterOrEqualf(t, len(active), simMinActiveSlots, "seed %d: MinActiveSlots floor violated", seed)

	seenKeys := map[string]bool{}
	var totalPower int64
	for i := range active {
		s := active[i]
		require.Equalf(t, coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE, s.Status, "seed %d: GetActiveSlots returned non-active slot %d", seed, s.SlotId)
		totalPower += s.ConsensusPower
		kb := s.ConsensusPubkey.String()
		require.Falsef(t, seenKeys[kb], "seed %d: duplicate active consensus key on slot %d", seed, s.SlotId)
		seenKeys[kb] = true
	}
	require.Equalf(t, int64(len(active))*simSlotVotingPower, totalPower, "seed %d: active power conservation", seed)

	// Every model slot must exist on chain with the expected status, and its
	// reward-weight row must be retained (incl. removed/suspended slots).
	for id, ms := range model {
		cs, err := a.CoreSlotKeeper.GetSlot(ctx, id)
		require.NoErrorf(t, err, "seed %d: slot %d row must be retained (model status %v)", seed, id, ms.status)
		require.Equalf(t, ms.status, cs.Status, "seed %d: slot %d status (model vs chain)", seed, id)
		_, err = a.CoreSlotKeeper.GetRewardWeight(ctx, id)
		require.NoErrorf(t, err, "seed %d: slot %d reward-weight row must be retained", seed, id)
	}
}
