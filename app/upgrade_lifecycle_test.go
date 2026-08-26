package app_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	dbm "github.com/cosmos/cosmos-db"
	"github.com/stretchr/testify/require"

	coreheader "cosmossdk.io/core/header"
	"cosmossdk.io/log"

	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/app"
	coreslotkeeper "github.com/twilight-project/twilight-core/x/coreslot/keeper"
	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	rewardstypes "github.com/twilight-project/twilight-core/x/rewards/types"
)

// withUpgrades swaps the compiled upgrade registry for the duration of a test.
//
// The registry is what distinguishes one binary from another — it is the only
// difference between "Binary A" and "Binary B" in the lifecycle below, which is
// exactly the distinction upstream x/upgrade is built around.
func withUpgrades(t *testing.T, upgrades []app.Upgrade) {
	t.Helper()
	previous := app.Upgrades
	app.Upgrades = upgrades
	t.Cleanup(func() { app.Upgrades = previous })
}

func memDB() dbm.DB { return dbm.NewMemDB() }

// testHome supplies a real home directory, which matters for more than tidiness:
// x/upgrade writes upgrade-info.json under <home>/data when a node halts at an
// upgrade height, so a test with an empty home performs that write relative to
// the working directory. A real home keeps the write inside the test's temp dir
// AND makes the file assertable.
type testHome struct{ dir string }

func (h testHome) Get(key string) interface{} {
	if key == flags.FlagHome {
		return h.dir
	}
	return nil
}

func newAppOnDB(t *testing.T, db dbm.DB) *app.App {
	t.Helper()
	return newAppWithHome(t, db, t.TempDir())
}

func newAppWithHome(t *testing.T, db dbm.DB, home string) *app.App {
	t.Helper()
	return app.New(log.NewNopLogger(), db, nil, true, testHome{dir: home})
}

// initUpgradeGenesis runs a real InitChain with a conforming V2 genesis and
// commits the first block.
func initUpgradeGenesis(t *testing.T, a *app.App) {
	t.Helper()
	appState, err := json.Marshal(upgradeGenesisMap(t, a))
	require.NoError(t, err)
	_, err = a.InitChain(&abci.RequestInitChain{
		InitialHeight:   1,
		ConsensusParams: sims.DefaultConsensusParams,
		AppStateBytes:   appState,
	})
	require.NoError(t, err)
	_, err = a.FinalizeBlock(&abci.RequestFinalizeBlock{Height: 1})
	require.NoError(t, err)
	_, err = a.Commit()
	require.NoError(t, err)
}

// upgradeGenesisMap builds a conforming genesis document, returned as the section
// map so a test can remove one section and prove the removal is refused.
func upgradeGenesisMap(t *testing.T, a *app.App) map[string]json.RawMessage {
	t.Helper()
	cdc := genesisCodec()
	const initialHeight = int64(1)

	operator, payout, settlement := acc(2), acc(12), acc(22)
	csParams := coreslottypes.DefaultParams(app.AuthorityAddress(), app.EmergencyAuthorityAddress())
	csGen := &coreslottypes.GenesisState{
		Params: &csParams,
		Slots: []*coreslottypes.CoreSlot{{
			SlotId: 1, OperatorAddress: operator, PayoutAddress: payout,
			SettlementAddress: settlement,
			ConsensusPubkey:   ed25519Any(t, 7),
			Status:            coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE,
			ConsensusPower:    1, RewardWeight: coreslottypes.DefaultRewardWeight,
			ActivationSequence: 1, ActivatedHeight: initialHeight,
			ActivationEffectiveHeight:     initialHeight,
			CurrentSelectionPolicyVersion: 1,
		}},
		SelectionPolicies: []*coreslottypes.SelectionPolicyVersion{{
			SlotId: 1, PolicyVersion: 1, SelectionRateBps: 2_500, MaxSelectedParticipants: 10,
			ValidFromHeight: initialHeight,
		}},
		RewardWeights: []*coreslottypes.OperatorRewardWeight{{SlotId: 1, FinalWeight: coreslottypes.DefaultRewardWeight}},
		NextSlotId:    2,
	}

	rParams := rewardstypes.DefaultParams()
	rSnap := rewardstypes.DefaultEpochConfigSnapshot(rParams)
	rAnchor := rewardstypes.DefaultEpochConfigVersion(rParams, uint64(initialHeight))
	rRewardAnchor := rewardstypes.DefaultRewardConfigVersion(rParams)
	rGen := &rewardstypes.GenesisState{
		Params: &rParams,
		State: &rewardstypes.RewardsState{
			CurrentEpoch: 1, CurrentEpochStartHeight: uint64(initialHeight),
			CumulativeEmitted: "0", CarryForwardRemainder: "0",
		},
		CurrentEpochConfig:              &rSnap,
		EpochConfigVersions:             []*rewardstypes.EpochConfigVersion{&rAnchor},
		RewardConfigVersions:            []*rewardstypes.RewardConfigVersion{&rRewardAnchor},
		PauseState:                      &rewardstypes.RewardsPauseState{},
		OutstandingEntitlementLiability: "0",
	}

	genMap := a.DefaultGenesis()
	genMap[coreslottypes.ModuleName] = cdc.MustMarshalJSON(csGen)
	genMap[rewardstypes.ModuleName] = cdc.MustMarshalJSON(rGen)
	return genMap
}

// finalize runs one block and reports failure as an error, whether the module
// returned one or the SDK turned it into a panic. Upstream's own comment on the
// upgrade PreBlocker says "returning an error will end up in a panic", so a test
// that only handled one of the two would be asserting against the harness rather
// than the chain.
func finalize(a *app.App, height int64) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(error); ok {
				err = e
				return
			}
			err = errorf("%v", r)
		}
	}()
	_, err = a.FinalizeBlock(&abci.RequestFinalizeBlock{Height: height})
	return err
}

func errorf(format string, args ...any) error { return fmt.Errorf(format, args...) }

// The supported software-upgrade lifecycle, end to end, on the real modules.
//
// This exists because the scheduling tests elsewhere prove things about a fake
// scheduler, and a fake has no PreBlocker. The state that upstream x/upgrade
// makes FATAL — a pending plan, a height not yet reached, and a binary that
// already holds the handler — has no representation in a fake at all, so a rule
// requiring exactly that state passed every unit test while making the mechanism
// unusable. Only a real app across a real block boundary can tell the difference.
//
//	Binary A   registry WITHOUT the upgrade   schedules it, rides to H, halts
//	Binary B   registry WITH the upgrade      resumes H, migrates once, continues
func TestRealUpgradeLifecycleOldBinarySchedulesNewBinaryExecutes(t *testing.T) {
	const upgradeName = "probe-v2"
	const upgradeHeight = int64(5)

	db := dbm.NewMemDB()
	// One home for both binaries, as a real operator swap has: B must read the
	// upgrade-info file A wrote at the halt.
	home := t.TempDir()

	// ---------- Binary A: does NOT know probe-v2 ----------
	withUpgrades(t, nil)
	binaryA := newAppWithHome(t, db, home)
	initUpgradeGenesis(t, binaryA)

	// Schedule from the binary that cannot execute it. This is the normal case,
	// not an edge case: the binary that performs an upgrade does not exist yet on
	// the nodes that schedule it.
	require.NoError(t, finalize(binaryA, 2))
	// NewUncachedContext, not NewContextLegacy. FinalizeBlock flushes its own cache
	// at the END of the call (workingHash), so a write made through that cache
	// afterwards is discarded when the next block starts a fresh one — the message
	// would appear to succeed and change nothing. The uncached context writes
	// straight to the committed multistore, which Commit then persists.
	ctx := binaryA.NewUncachedContext(false, cmtproto.Header{Height: 2}).
		WithHeaderInfo(coreheader.Info{Height: 2})
	authority, err := binaryA.CoreSlotKeeper.Params.Get(ctx)
	require.NoError(t, err)
	_, err = coreslotkeeper.NewMsgServer(binaryA.CoreSlotKeeper).ScheduleUpgrade(ctx,
		&coreslottypes.MsgScheduleUpgrade{
			Authority: authority.Authority, Name: upgradeName, Height: upgradeHeight,
		})
	require.NoError(t, err, "a binary without the handler must be able to schedule the upgrade")
	_, err = binaryA.Commit()
	require.NoError(t, err)

	plan, err := binaryA.UpgradeKeeper.GetUpgradePlan(
		binaryA.NewUncachedContext(false, cmtproto.Header{Height: 2}))
	require.NoError(t, err)
	require.Equal(t, upgradeName, plan.Name)
	require.Equal(t, upgradeHeight, plan.Height)

	// Blocks between the scheduling height and H must be ordinary. This is the
	// window the removed admission rule made fatal.
	for height := int64(3); height < upgradeHeight; height++ {
		require.NoError(t, finalize(binaryA, height),
			"block %d carries a pending plan the binary cannot execute; it must still commit", height)
		_, err = binaryA.Commit()
		require.NoError(t, err)
	}

	// At H the old binary must refuse to continue.
	err = finalize(binaryA, upgradeHeight)
	require.Error(t, err, "the old binary must halt at the upgrade height")
	require.Contains(t, err.Error(), upgradeName)
	require.Contains(t, err.Error(), "UPGRADE")

	// The halt must leave the operator the file the next binary needs. This is what
	// carries the upgrade name and height across the process boundary, and it is
	// what setUpgradeStoreLoader reads before the store is mounted.
	infoPath := filepath.Join(home, "data", "upgrade-info.json")
	raw, readErr := os.ReadFile(infoPath)
	require.NoError(t, readErr, "the halting binary must write upgrade-info.json")
	var info struct {
		Name   string `json:"name"`
		Height int64  `json:"height"`
	}
	require.NoError(t, json.Unmarshal(raw, &info))
	require.Equal(t, upgradeName, info.Name)
	require.Equal(t, upgradeHeight, info.Height)

	// Stated rather than implied: the halted binary committed nothing at H. B
	// resuming that height would otherwise be the only evidence, and that is an
	// inference about the harness rather than an assertion about the chain.
	require.Equal(t, upgradeHeight-1, binaryA.LastBlockHeight(),
		"the halted binary must not have committed the upgrade height")

	// ---------- Binary B: knows probe-v2, same database ----------
	migrated := 0
	withUpgrades(t, []app.Upgrade{{
		Name: upgradeName,
		Migrate: func(_ sdk.Context, _ app.MigrationKeepers) error {
			migrated++
			return nil
		},
	}})
	binaryB := newAppWithHome(t, db, home)

	require.NoError(t, finalize(binaryB, upgradeHeight),
		"the new binary must resume the height the old one halted at")
	require.Equal(t, 1, migrated, "the migration must run exactly once")
	_, err = binaryB.Commit()
	require.NoError(t, err)

	// The chain continues, and the migration does not run again.
	require.NoError(t, finalize(binaryB, upgradeHeight+1))
	_, err = binaryB.Commit()
	require.NoError(t, err)
	require.Equal(t, 1, migrated, "the migration must not run again after the upgrade height")

	after := binaryB.NewUncachedContext(false, cmtproto.Header{Height: upgradeHeight + 1})
	// The plan is consumed, so a restart cannot re-apply it.
	_, err = binaryB.UpgradeKeeper.GetUpgradePlan(after)
	require.Error(t, err, "the applied plan must be cleared")

	stored, err := binaryB.UpgradeKeeper.GetModuleVersionMap(after)
	require.NoError(t, err)
	for name := range binaryB.ModuleManager.Modules {
		require.Contains(t, stored, name, "module %q missing from the post-upgrade version map", name)
	}

	// A restart AFTER the upgrade, with the stale upgrade-info.json still on disk
	// and the handler still compiled in, must not re-apply anything. Operators
	// restart nodes for unrelated reasons, and the file is not cleaned up — so the
	// only thing standing between a routine restart and a second migration is the
	// store loader's height guard and the consumed plan.
	require.FileExists(t, infoPath, "the upgrade-info file is deliberately left in place")
	binaryC := newAppWithHome(t, db, home)
	require.Equal(t, upgradeHeight+1, binaryC.LastBlockHeight(),
		"a restart must resume from the committed height")
	require.NoError(t, finalize(binaryC, upgradeHeight+2))
	_, err = binaryC.Commit()
	require.NoError(t, err)
	require.Equal(t, 1, migrated,
		"restarting with a stale upgrade-info file must not re-run the migration")
}

// Upstream's rule, pinned: a binary that ALREADY holds a pending upgrade's
// handler aborts every block until that upgrade's height arrives.
//
// This is the exact state a scheduling-time "the binary must know this name"
// admission rule would require, and it is why such a rule cannot exist. The two
// conditions are the same map lookup with opposite polarity, so the only height
// they both admit is the very next block — which leaves no window to swap a
// binary in.
//
// Kept as a test rather than a comment because the rule was added once already,
// passed every unit test, and made the mechanism unusable.
func TestBinaryHoldingAFutureHandlerHaltsBeforeTheUpgradeHeight(t *testing.T) {
	const upgradeName = "probe-early"
	const upgradeHeight = int64(6)

	db := dbm.NewMemDB()

	// Schedule from a binary that does not know the name — the supported path.
	withUpgrades(t, nil)
	scheduler := newAppOnDB(t, db)
	initUpgradeGenesis(t, scheduler)
	require.NoError(t, finalize(scheduler, 2))
	ctx := scheduler.NewUncachedContext(false, cmtproto.Header{Height: 2}).
		WithHeaderInfo(coreheader.Info{Height: 2})
	params, err := scheduler.CoreSlotKeeper.Params.Get(ctx)
	require.NoError(t, err)
	_, err = coreslotkeeper.NewMsgServer(scheduler.CoreSlotKeeper).ScheduleUpgrade(ctx,
		&coreslottypes.MsgScheduleUpgrade{
			Authority: params.Authority, Name: upgradeName, Height: upgradeHeight,
		})
	require.NoError(t, err)
	_, err = scheduler.Commit()
	require.NoError(t, err)

	// Now bring up a binary that DOES hold the handler, well before the height.
	withUpgrades(t, []app.Upgrade{{
		Name:    upgradeName,
		Migrate: func(_ sdk.Context, _ app.MigrationKeepers) error { return nil },
	}})
	early := newAppOnDB(t, db)

	err = finalize(early, 3)
	require.Error(t, err,
		"upstream must refuse a binary that holds a pending upgrade's handler before its height")
	require.Contains(t, err.Error(), "BINARY UPDATED BEFORE TRIGGER",
		"this is the guard that makes a present-handler scheduling rule unusable")
}
