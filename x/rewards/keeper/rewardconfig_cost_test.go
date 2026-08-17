package keeper_test

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"

	corestore "cosmossdk.io/core/store"
	"cosmossdk.io/log"
	sdkmath "cosmossdk.io/math"
	storetypes "cosmossdk.io/store/types"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/runtime"
	"github.com/cosmos/cosmos-sdk/testutil/integration"
	sdk "github.com/cosmos/cosmos-sdk/types"

	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	"github.com/twilight-project/twilight-core/x/rewards/keeper"
	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// The cost of binding an epoch's reward configuration.
//
// This is not a benchmark and it is not about speed. Reward-configuration history
// grows for the life of the chain, so a lookup executed once per participant makes
// the cost of closing an epoch depend on how many configuration changes the chain
// has ever accepted. A block that gets slower the older the chain is, at a
// boundary every validator must reach the same conclusion about, is a liveness
// property — not a performance one.
//
// The measurement counts real store operations under the reward-configuration
// prefix, so it cannot be satisfied by a comment or by a fixture that happens to
// hold one version.

// storeAccess counts the work done under one store prefix.
//
// Two numbers, because two different claims are made about this module's reads.
// Reads counts point lookups and iterator opens: it answers "how many times did
// this run consult the collection". Rows counts positions an iterator actually
// visited: it answers "how much of the collection did it walk". A page bound is a
// claim about the second, and no amount of the first can substitute for it.
type storeAccess struct {
	reads int
	rows  int
}

// countingKVStore counts operations whose key or range falls under one prefix.
type countingKVStore struct {
	inner  corestore.KVStore
	prefix []byte
	access *storeAccess
}

func (s countingKVStore) count(key []byte) bool {
	if bytes.HasPrefix(key, s.prefix) {
		s.access.reads++
		return true
	}
	return false
}

func (s countingKVStore) Get(key []byte) ([]byte, error) { s.count(key); return s.inner.Get(key) }
func (s countingKVStore) Has(key []byte) (bool, error)   { s.count(key); return s.inner.Has(key) }
func (s countingKVStore) Set(key, value []byte) error    { return s.inner.Set(key, value) }
func (s countingKVStore) Delete(key []byte) error        { return s.inner.Delete(key) }

func (s countingKVStore) Iterator(start, end []byte) (corestore.Iterator, error) {
	tracked := s.count(start)
	iter, err := s.inner.Iterator(start, end)
	if err != nil || !tracked {
		return iter, err
	}
	return countingIterator{Iterator: iter, access: s.access}, nil
}

func (s countingKVStore) ReverseIterator(start, end []byte) (corestore.Iterator, error) {
	tracked := s.count(start)
	iter, err := s.inner.ReverseIterator(start, end)
	if err != nil || !tracked {
		return iter, err
	}
	return countingIterator{Iterator: iter, access: s.access}, nil
}

// countingIterator counts every advance the walk makes.
//
// Counted on Next rather than on reading a value, because the SDK's offset
// handling and its count_total loop both advance an iterator WITHOUT reading
// anything. Measuring value reads would report those two as free, and they are
// precisely the costs this measurement exists to expose.
type countingIterator struct {
	corestore.Iterator
	access *storeAccess
}

func (i countingIterator) Next() {
	i.access.rows++
	i.Iterator.Next()
}

type countingKVStoreService struct {
	inner  corestore.KVStoreService
	prefix []byte
	access *storeAccess
}

func (s countingKVStoreService) OpenKVStore(ctx context.Context) corestore.KVStore {
	return countingKVStore{inner: s.inner.OpenKVStore(ctx), prefix: s.prefix, access: s.access}
}

// setupCountingFinalization mirrors the ordinary finalization fixture but routes
// every store operation through a counter, and opens at an epoch whose binding
// goes through the predecessor seek rather than the bootstrap branch.
//
// The history holds several versions on purpose: with one, a per-row lookup and a
// single lookup cost nearly the same and the measurement would prove nothing.
func setupCountingFinalization(
	t *testing.T, openEpoch uint64, participants int,
) (keeper.Keeper, sdk.Context, *storeAccess) {
	t.Helper()
	registry := codectypes.NewInterfaceRegistry()
	types.RegisterInterfaces(registry)
	cdc := codec.NewProtoCodec(registry)
	keys := storetypes.NewKVStoreKeys(types.StoreKey)
	cms := integration.CreateMultiStore(keys, log.NewNopLogger())
	ctx := sdk.NewContext(cms, cmtproto.Header{Height: 1}, false, log.NewNopLogger())

	access := &storeAccess{}
	service := countingKVStoreService{
		inner:  runtime.NewKVStoreService(keys[types.StoreKey]),
		prefix: types.RewardConfigVersionsPrefix.Bytes(),
		access: access,
	}

	params := rewardConfigParams()
	slots := make(map[uint64]coreslottypes.CoreSlot, participants)
	for i := 1; i <= participants; i++ {
		id := uint64(i)
		slots[id] = coreslottypes.CoreSlot{
			SlotId:          id,
			OperatorAddress: addr(byte(60 + id)),
			PayoutAddress:   addr(byte(100 + id)),
			Status:          coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE,
		}
	}
	core := &coreSlotKeeperMock{slots: slots}

	k := keeper.NewKeeper(cdc, service, accountKeeperMock{}, &bankKeeperMock{}, core, testEconomicAddresses(t))
	require.NoError(t, k.SetPauseState(ctx, types.RewardsPauseState{}))
	require.NoError(t, k.SetOpenRewardEnabledBlocks(ctx, params.EpochLengthBlocks))
	require.NoError(t, k.SetOutstandingEntitlementLiability(ctx, sdkmath.ZeroInt()))
	require.NoError(t, k.SetParams(ctx, params))

	state := types.RewardsState{
		CurrentEpoch: openEpoch, CurrentEpochStartHeight: 1,
		CumulativeEmitted: "0", CarryForwardRemainder: "0",
	}
	require.NoError(t, k.SetState(ctx, state))
	cfg, err := keeper.BuildEpochConfigSnapshot(params)
	require.NoError(t, err)
	require.NoError(t, k.SetCurrentEpochConfig(ctx, cfg))
	seedEpochTimeline(t, k, ctx, params, state)

	// A multi-version history. Version 1 is the permanent anchor; the rest are
	// ordinary later versions, so a bare version-number walk would have to traverse
	// them.
	seedRewardConfigTimeline(t, k, ctx, params)
	for i := uint64(2); i <= 6; i++ {
		seedRewardVersion(t, k, ctx, rewardVersionAt(i, i, fmt.Sprintf("%d", 10+i)))
	}

	for i := 1; i <= participants; i++ {
		require.NoError(t, k.SetActiveBlocks(ctx, openEpoch, uint64(i), 1))
	}
	return k, ctx, access
}

// TestEpochCloseResolvesTheRewardConfigExactlyOnce is B2.
//
// The bound is stated against a measured baseline rather than a magic number: one
// complete RewardConfigForTarget call is counted on the same fixture, and the whole
// epoch close is required to cost exactly that much reward-configuration reading.
// Anything per-row shows up immediately, and the assertion stays correct if the
// resolver's internal read count ever changes.
func TestEpochCloseResolvesTheRewardConfigExactlyOnce(t *testing.T) {
	const openEpoch = 9

	baseline := 0
	for _, participants := range []int{2, 8, 20} {
		t.Run(fmt.Sprintf("%d participants", participants), func(t *testing.T) {
			k, ctx, access := setupCountingFinalization(t, openEpoch, participants)

			// One resolution, measured. This is the ordinary N-2 branch: target 9 binds
			// epoch 7, which is a seek past several versions.
			governing, err := k.RewardConfigForTarget(ctx, openEpoch)
			require.NoError(t, err)
			require.Equal(t, uint64(6), governing.Version, "the fixture must exercise the seek branch")
			single := access.reads
			require.Positive(t, single, "the counter must be observing the history at all")

			access.reads = 0
			require.NoError(t, k.EndBlock(ctx.WithBlockHeight(finalizationEndHeight)))

			entitlements, err := k.IterateEntitlementsForEpoch(ctx, openEpoch)
			require.NoError(t, err)
			require.Len(t, entitlements, participants,
				"every participant must have produced an entitlement, or the loop under test did not run")

			require.Equal(t, single, access.reads,
				"closing an epoch that creates %d entitlements must read the reward configuration "+
					"history exactly as much as resolving it once", participants)

			// And the count does not track the number of participants, which is the
			// same claim stated so that a single-run regression cannot pass by luck.
			if baseline == 0 {
				baseline = access.reads
			}
			require.Equal(t, baseline, access.reads,
				"reward-configuration reads must not grow with the participant count")
		})
	}
}
