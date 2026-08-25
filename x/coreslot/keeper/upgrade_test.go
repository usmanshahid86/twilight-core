package keeper_test

import (
	"context"
	"errors"
	"testing"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	coreheader "cosmossdk.io/core/header"
	"cosmossdk.io/log"
	storetypes "cosmossdk.io/store/types"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil/integration"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/x/coreslot/keeper"
	"github.com/twilight-project/twilight-core/x/coreslot/types"
)

// recordingScheduler stands in for x/upgrade and records exactly what reached it.
//
// The point of most tests below is that it recorded NOTHING: a refusal that still
// scheduled a plan would leave the chain halting at a height nobody authorized,
// and asserting only on the returned error would not notice.
type recordingScheduler struct {
	// known is the set of upgrade names this "binary" can execute. Empty means the
	// registry is empty, which is the released binary's actual state.
	known       map[string]bool
	pending     string
	scheduled   []scheduledPlan
	cancels     int
	scheduleErr error
	cancelErr   error
	pendingErr  error
}

type scheduledPlan struct {
	name   string
	height int64
	info   string
}

func knowing(names ...string) *recordingScheduler {
	known := make(map[string]bool, len(names))
	for _, n := range names {
		known[n] = true
	}
	return &recordingScheduler{known: known}
}

func (s *recordingScheduler) ScheduleUpgrade(_ context.Context, name string, height int64, info string) error {
	if s.scheduleErr != nil {
		return s.scheduleErr
	}
	s.scheduled = append(s.scheduled, scheduledPlan{name: name, height: height, info: info})
	s.pending = name
	return nil
}

func (s *recordingScheduler) CancelUpgrade(_ context.Context) error {
	if s.cancelErr != nil {
		return s.cancelErr
	}
	s.cancels++
	s.pending = ""
	return nil
}

func (s *recordingScheduler) HasUpgradeHandler(name string) bool { return s.known[name] }

func (s *recordingScheduler) PendingUpgrade(_ context.Context) (string, error) {
	if s.pendingErr != nil {
		return "", s.pendingErr
	}
	return s.pending, nil
}

// setupUpgrade builds a keeper wired to a recording scheduler, with params seeded
// and the block height at 100 so a "future height" has room on both sides.
func setupUpgrade(t *testing.T, scheduler types.UpgradeScheduler) (types.MsgServer, sdk.Context, string) {
	t.Helper()
	registry := codectypes.NewInterfaceRegistry()
	types.RegisterInterfaces(registry)
	cdc := codec.NewProtoCodec(registry)
	keys := storetypes.NewKVStoreKeys(types.StoreKey)
	cms := integration.CreateMultiStore(keys, log.NewNopLogger())
	// Genesis is imported at height 1 — an active slot's activation heights must
	// equal the initial height — and the context is then advanced, so the messages
	// under test run on a chain with history behind them rather than at genesis.
	ctx := sdk.NewContext(cms, cmtproto.Header{Height: 1}, false, log.NewNopLogger())

	k := keeper.NewKeeper(cdc, runtime.NewKVStoreService(keys[types.StoreKey]), testEconomicAddresses(t), scheduler)
	authority := sdk.AccAddress(make([]byte, 20)).String()
	emergency := sdk.AccAddress(append([]byte{1}, make([]byte, 19)...)).String()
	params := types.DefaultParams(authority, emergency)
	params.MinActiveSlots = 1
	// Genesis refuses an empty active set, so the chain these tests run against is
	// a real one with a validator in it rather than a bare parameter store.
	operator := sdk.AccAddress(append([]byte{2}, make([]byte, 19)...)).String()
	_, err := initGenesis(t, k, ctx, &types.GenesisState{
		Params:     &params,
		Slots:      []*types.CoreSlot{slot(t, 1, operator, 1, types.SlotStatus_SLOT_STATUS_ACTIVE, 1)},
		NextSlotId: 2,
	})
	require.NoError(t, err)
	// Both height sources are advanced. The handler reads HeaderInfo().Height —
	// the same source x/upgrade reads — and WithBlockHeight updates only the
	// header, so setting the header alone would leave HeaderInfo at 0 and the
	// future-height assertions would pass vacuously.
	return keeper.NewMsgServer(k),
		ctx.WithBlockHeight(100).WithHeaderInfo(coreheader.Info{Height: 100}),
		authority
}

func TestScheduleUpgradeByAuthority(t *testing.T) {
	scheduler := knowing("v2")
	msgs, ctx, authority := setupUpgrade(t, scheduler)

	_, err := msgs.ScheduleUpgrade(ctx, &types.MsgScheduleUpgrade{
		Authority: authority, Name: "v2", Height: 500, Info: "sha256:abc",
	})
	require.NoError(t, err)

	// Every field must arrive unaltered. The name in particular: a binary decides
	// whether it knows an upgrade by exact string match, so any normalization here
	// would halt the network at a name no build recognizes.
	require.Equal(t, []scheduledPlan{{name: "v2", height: 500, info: "sha256:abc"}}, scheduler.scheduled)
}

func TestScheduleUpgradeRefusesNonAuthority(t *testing.T) {
	scheduler := knowing("v2")
	msgs, ctx, authority := setupUpgrade(t, scheduler)
	stranger := sdk.AccAddress(append([]byte{9}, make([]byte, 19)...)).String()
	require.NotEqual(t, authority, stranger)

	_, err := msgs.ScheduleUpgrade(ctx, &types.MsgScheduleUpgrade{
		Authority: stranger, Name: "v2", Height: 500,
	})
	require.ErrorIs(t, err, types.ErrUnauthorized)
	require.Empty(t, scheduler.scheduled, "an unauthorized caller must not reach the upgrade module at all")
}

func TestCancelUpgradeRefusesNonAuthority(t *testing.T) {
	scheduler := knowing("v2")
	msgs, ctx, authority := setupUpgrade(t, scheduler)
	stranger := sdk.AccAddress(append([]byte{9}, make([]byte, 19)...)).String()
	require.NotEqual(t, authority, stranger)

	_, err := msgs.CancelUpgrade(ctx, &types.MsgCancelUpgrade{Authority: stranger})
	require.ErrorIs(t, err, types.ErrUnauthorized)
	require.Zero(t, scheduler.cancels)
}

func TestCancelUpgradeByAuthority(t *testing.T) {
	scheduler := knowing("v2")
	msgs, ctx, authority := setupUpgrade(t, scheduler)

	_, err := msgs.ScheduleUpgrade(ctx, &types.MsgScheduleUpgrade{Authority: authority, Name: "v2", Height: 500})
	require.NoError(t, err)
	_, err = msgs.CancelUpgrade(ctx, &types.MsgCancelUpgrade{Authority: authority})
	require.NoError(t, err)
	require.Equal(t, 1, scheduler.cancels)
}

func TestScheduleUpgradeRejectsUnusablePlans(t *testing.T) {
	cases := []struct {
		name   string
		msg    func(authority string) *types.MsgScheduleUpgrade
		reason string
	}{
		{
			name:   "empty name",
			msg:    func(a string) *types.MsgScheduleUpgrade { return &types.MsgScheduleUpgrade{Authority: a, Height: 500} },
			reason: "upgrade name is required",
		},
		{
			// The current height is 100. Halting at a height already reached is not
			// a halt, it is a plan that can never fire.
			name: "height in the past",
			msg: func(a string) *types.MsgScheduleUpgrade {
				return &types.MsgScheduleUpgrade{Authority: a, Name: "v2", Height: 50}
			},
			reason: "not in the future",
		},
		{
			name: "height is the current block",
			msg: func(a string) *types.MsgScheduleUpgrade {
				return &types.MsgScheduleUpgrade{Authority: a, Name: "v2", Height: 100}
			},
			reason: "not in the future",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scheduler := knowing("v2")
			msgs, ctx, authority := setupUpgrade(t, scheduler)

			_, err := msgs.ScheduleUpgrade(ctx, tc.msg(authority))
			require.ErrorIs(t, err, types.ErrInvalidUpgrade)
			require.Contains(t, err.Error(), tc.reason)
			require.Empty(t, scheduler.scheduled)
		})
	}
}

// A build with no route to x/upgrade must refuse rather than dereference a nil
// scheduler. A panic here would be a halt at an arbitrary block rather than a
// failed transaction.
func TestUpgradeMessagesRefusedWithoutAScheduler(t *testing.T) {
	msgs, ctx, authority := setupUpgrade(t, nil)

	_, err := msgs.ScheduleUpgrade(ctx, &types.MsgScheduleUpgrade{Authority: authority, Name: "v2", Height: 500})
	require.ErrorIs(t, err, types.ErrUpgradeUnavailable)
	_, err = msgs.CancelUpgrade(ctx, &types.MsgCancelUpgrade{Authority: authority})
	require.ErrorIs(t, err, types.ErrUpgradeUnavailable)
}

// An error from x/upgrade must surface, not be swallowed into a success that
// leaves the operator believing a halt is scheduled when none is.
func TestScheduleUpgradePropagatesModuleError(t *testing.T) {
	boom := errors.New("upgrade module refused")
	scheduler := knowing("v2")
	scheduler.scheduleErr = boom
	msgs, ctx, authority := setupUpgrade(t, scheduler)

	_, err := msgs.ScheduleUpgrade(ctx, &types.MsgScheduleUpgrade{Authority: authority, Name: "v2", Height: 500})
	require.ErrorIs(t, err, boom)
}

// The released binary carries an EMPTY upgrade registry, so this is the default
// state, not an edge case: every name is unknown until one is compiled in.
//
// x/upgrade would accept such a plan — it validates the plan, refuses a past
// height and refuses a completed name, and checks nothing else. The whole network
// would then halt at that height with no way to withdraw it, because the chain
// cannot produce the block that would carry the cancellation.
func TestScheduleUpgradeRefusesAnUpgradeThisBinaryCannotRun(t *testing.T) {
	scheduler := knowing() // empty registry, as shipped
	msgs, ctx, authority := setupUpgrade(t, scheduler)

	_, err := msgs.ScheduleUpgrade(ctx, &types.MsgScheduleUpgrade{
		Authority: authority, Name: "v2", Height: 500,
	})
	require.ErrorIs(t, err, types.ErrInvalidUpgrade)
	require.Contains(t, err.Error(), "no handler for upgrade")
	require.Empty(t, scheduler.scheduled, "a plan the binary cannot execute must never reach the upgrade module")
}

// ClearUpgradePlan returns success when nothing is scheduled, so without an
// explicit check the message would report a withdrawal that never happened.
func TestCancelUpgradeRefusesWhenNothingIsScheduled(t *testing.T) {
	scheduler := knowing("v2")
	msgs, ctx, authority := setupUpgrade(t, scheduler)

	_, err := msgs.CancelUpgrade(ctx, &types.MsgCancelUpgrade{Authority: authority})
	require.ErrorIs(t, err, types.ErrInvalidUpgrade)
	require.Contains(t, err.Error(), "no upgrade is scheduled")
	require.Zero(t, scheduler.cancels, "nothing to cancel must not reach the upgrade module")
}

// The mirror of TestScheduleUpgradePropagatesModuleError. Without it, a refactor
// that swallowed this error would still pass the suite while telling operators a
// halt had been withdrawn when it had not.
func TestCancelUpgradePropagatesModuleError(t *testing.T) {
	boom := errors.New("upgrade module refused")
	scheduler := knowing("v2")
	msgs, ctx, authority := setupUpgrade(t, scheduler)

	_, err := msgs.ScheduleUpgrade(ctx, &types.MsgScheduleUpgrade{Authority: authority, Name: "v2", Height: 500})
	require.NoError(t, err)

	scheduler.cancelErr = boom
	_, err = msgs.CancelUpgrade(ctx, &types.MsgCancelUpgrade{Authority: authority})
	require.ErrorIs(t, err, boom)
}

// A failure to READ the pending plan must not be mistaken for "nothing is
// scheduled", which would let a cancellation be refused on corrupt state instead
// of surfacing the corruption.
func TestCancelUpgradePropagatesPendingLookupError(t *testing.T) {
	boom := errors.New("plan unreadable")
	scheduler := knowing("v2")
	scheduler.pendingErr = boom
	msgs, ctx, authority := setupUpgrade(t, scheduler)

	_, err := msgs.CancelUpgrade(ctx, &types.MsgCancelUpgrade{Authority: authority})
	require.ErrorIs(t, err, boom)
	require.Zero(t, scheduler.cancels)
}
