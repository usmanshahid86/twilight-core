package app_test

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	abci "github.com/cometbft/cometbft/abci/types"
	cmtproto "github.com/cometbft/cometbft/proto/tendermint/types"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	"github.com/cosmos/cosmos-sdk/std"
	"github.com/cosmos/cosmos-sdk/testutil/sims"
	sdk "github.com/cosmos/cosmos-sdk/types"
	grpctypes "github.com/cosmos/cosmos-sdk/types/grpc"

	"github.com/twilight-project/twilight-core/app"
	appparams "github.com/twilight-project/twilight-core/app/params"
	coreslottypes "github.com/twilight-project/twilight-core/x/coreslot/types"
	miningkeeper "github.com/twilight-project/twilight-core/x/mining/keeper"
	miningtypes "github.com/twilight-project/twilight-core/x/mining/types"
	rewardskeeper "github.com/twilight-project/twilight-core/x/rewards/keeper"
	rewardstypes "github.com/twilight-project/twilight-core/x/rewards/types"
)

// The historical-height contract.
//
// A settlement worker reconciles its position against a fixed view of the chain.
// If one query in that reconciliation answered from current head while the others
// answered from the requested height, the worker would combine two moments into a
// single decision — and the decision it is making is who to pay. Nothing in a
// response says which moment it came from, so this is a property that has to be
// proven rather than assumed.
//
// Every proof here runs against a REAL committed chain: genesis through InitChain,
// blocks through FinalizeBlock and Commit, so each height is an actual store
// version rather than a context constructed to look like one. A query answered
// from a manually built historical context would demonstrate only that keepers
// respect the context they are handed, which was never in doubt; what needs
// proving is that the height a consumer supplies reaches them at all.

// pinnedChain is a committed chain with one ACTIVE CoreSlot earning rewards.
type pinnedChain struct {
	app        *app.App
	operator   string
	payout     string
	credential string
	head       int64
}

// epochLength is the minimum the protocol permits. Using the floor keeps these
// runs inside the ratified envelope while closing an epoch as fast as the rules
// allow, which matters because the deadline proof has to outlast a whole
// settlement window measured in epochs.
const epochLength = int64(appparams.HardMinEpochLengthBlocks)

func bootPinnedChain(t *testing.T) *pinnedChain {
	t.Helper()
	a := bootApp(t)

	registry := codectypes.NewInterfaceRegistry()
	std.RegisterInterfaces(registry)
	coreslottypes.RegisterInterfaces(registry)
	cdc := codec.NewProtoCodec(registry)

	chain := &pinnedChain{app: a, operator: acc(0x02), payout: acc(0x0c), credential: acc(0x28)}

	csParams := coreslottypes.DefaultParams(app.AuthorityAddress(), app.EmergencyAuthorityAddress())
	csGen := &coreslottypes.GenesisState{
		Params: &csParams, NextSlotId: 2,
		Slots: []*coreslottypes.CoreSlot{{
			SlotId: 1, OperatorAddress: chain.operator, PayoutAddress: chain.payout,
			SettlementAddress: chain.credential, ConsensusPubkey: ed25519Any(t, 7),
			Status:         coreslottypes.SlotStatus_SLOT_STATUS_ACTIVE,
			ConsensusPower: 1, RewardWeight: coreslottypes.DefaultRewardWeight,
			ActivationSequence: 1, ActivatedHeight: 1, ActivationEffectiveHeight: 1,
			CurrentSelectionPolicyVersion: 1,
		}},
		SelectionPolicies: []*coreslottypes.SelectionPolicyVersion{{
			SlotId: 1, PolicyVersion: 1, SelectionRateBps: 2_500, MaxSelectedParticipants: 10,
			ValidFromHeight: 1,
		}},
		RewardWeights: []*coreslottypes.OperatorRewardWeight{
			{SlotId: 1, FinalWeight: coreslottypes.DefaultRewardWeight},
		},
	}

	params, snapshot := rewardsParams(t, func(p *rewardstypes.Params) {
		p.InitialBlockSubsidy = "100000"
		p.EpochLengthBlocks = uint64(epochLength)
	})
	rGen := genesisState(params, snapshot)

	genesis := a.DefaultGenesis()
	genesis[coreslottypes.ModuleName] = cdc.MustMarshalJSON(csGen)
	genesis[rewardstypes.ModuleName] = cdc.MustMarshalJSON(&rGen)
	appState, err := json.Marshal(genesis)
	require.NoError(t, err)

	_, err = a.InitChain(&abci.RequestInitChain{
		InitialHeight:   1,
		ConsensusParams: sims.DefaultConsensusParams,
		AppStateBytes:   appState,
	})
	require.NoError(t, err)
	return chain
}

// commitThrough produces real blocks up to and including height, so every height
// in between becomes a committed store version a query can be pinned to.
func (c *pinnedChain) commitThrough(t *testing.T, height int64) {
	t.Helper()
	for h := c.head + 1; h <= height; h++ {
		_, err := c.app.FinalizeBlock(&abci.RequestFinalizeBlock{Height: h})
		require.NoError(t, err)
		_, err = c.app.Commit()
		require.NoError(t, err)
	}
	c.head = height
}

// headContext is a context over committed state, for the state transitions a test
// drives itself. Writes made through it are committed by the next block, which is
// what makes them appear at that block's version and no earlier.
func (c *pinnedChain) headContext() sdk.Context {
	return c.app.NewUncachedContext(false, cmtproto.Header{Height: c.head})
}

// protoMessage is the marshaling surface every generated request and response
// already has.
type protoMessage interface {
	Marshal() ([]byte, error)
	Unmarshal([]byte) error
}

// queryAtHeight asks through the application's own ABCI query entry point, which
// is where the RPC layer delivers every gRPC and REST query. The height travels in
// the request exactly as the gateway sets it from x-cosmos-block-height, so this
// exercises the same CreateQueryContext and versioned-store resolution a remote
// consumer reaches — without needing a socket.
func queryAtHeight(t *testing.T, a *app.App, method string, req, resp protoMessage, height int64) {
	t.Helper()
	data, err := req.Marshal()
	require.NoError(t, err)

	res, err := a.Query(context.Background(), &abci.RequestQuery{
		Path: method, Data: data, Height: height,
	})
	require.NoError(t, err)
	require.Zerof(t, res.Code, "query %s at height %d failed: %s", method, height, res.Log)
	require.NoError(t, resp.Unmarshal(res.Value))
}

// headerQuerier drives queries through the gRPC server registration, where the
// x-cosmos-block-height header itself is parsed.
//
// This is deliberately not the ABCI path above. The header is read by an
// interceptor installed at gRPC registration, and nothing about the ABCI entry
// point proves that interceptor exists or resolves the height the same way. A
// consumer sending the header depends on that specific translation.
type headerQuerier struct {
	methods  map[string]grpc.MethodDesc
	handlers map[string]any
}

func (q *headerQuerier) RegisterService(sd *grpc.ServiceDesc, handler any) {
	for _, method := range sd.Methods {
		key := "/" + sd.ServiceName + "/" + method.MethodName
		q.methods[key] = method
		q.handlers[key] = handler
	}
}

func newHeaderQuerier(a *app.App) *headerQuerier {
	q := &headerQuerier{methods: map[string]grpc.MethodDesc{}, handlers: map[string]any{}}
	a.RegisterGRPCServer(q)
	return q
}

// query sends one request with x-cosmos-block-height set, the way a remote client
// asks for a historical view.
func (q *headerQuerier) query(t *testing.T, method string, req, resp protoMessage, height int64) {
	t.Helper()
	desc, found := q.methods[method]
	require.Truef(t, found, "method %s is not served over gRPC", method)

	data, err := req.Marshal()
	require.NoError(t, err)

	ctx := metadata.NewIncomingContext(context.Background(),
		metadata.Pairs(grpctypes.GRPCBlockHeightHeader, strconv.FormatInt(height, 10)))

	reply, err := desc.Handler(q.handlers[method], ctx, func(arg any) error {
		msg, ok := arg.(protoMessage)
		require.Truef(t, ok, "request %T is not a marshallable message", arg)
		return msg.Unmarshal(data)
	}, nil)
	require.NoError(t, err)

	replyBytes, err := reply.(protoMessage).Marshal()
	require.NoError(t, err)
	require.NoError(t, resp.Unmarshal(replyBytes))
}

const (
	settlementMethod    = "/twilight.mining.v1.Query/Settlement"
	settlementClock     = "/twilight.mining.v1.Query/SettlementClock"
	slotEntitlement     = "/twilight.rewards.v1.Query/SlotEntitlement"
	epochBoundaries     = "/twilight.rewards.v1.Query/EpochBoundaries"
	rewardsPauseState   = "/twilight.rewards.v1.Query/RewardsPauseState"
	coreSlotMethod      = "/twilight.coreslot.v1.Query/CoreSlot"
	interpretationQuery = "/twilight.mining.v1.Query/TargetEpochInterpretation"
	economicAddress     = "/twilight.mining.v1.Query/ValidateEconomicAddress"
)

// settledChain drives one chain to the close of epoch 1, where an entitlement
// exists and its settlement has been materialized by consensus.
func settledChain(t *testing.T) *pinnedChain {
	t.Helper()
	chain := bootPinnedChain(t)
	chain.commitThrough(t, epochLength)
	return chain
}

// TestSettlementDerivedValuesComeFromThePinnedHeight is the central proof.
//
// A settlement's stored fields and its derived fields are produced by different
// code: the stored ones are read from the versioned store and are pinned almost by
// construction, while the derived ones are computed at query time from several
// reads. Computing any of them from current head would be invisible in a response
// and would tell a worker that value had already moved which had not, or the
// reverse.
func TestSettlementDerivedValuesComeFromThePinnedHeight(t *testing.T) {
	chain := settledChain(t)
	a := chain.app

	beforeChunk := chain.head
	entitlement, found, err := a.RewardsKeeper.GetSlotEntitlement(chain.headContext(), 1, 1)
	require.NoError(t, err)
	require.True(t, found, "the deployed reward path produced the entitlement")
	owed, err := entitlement.Amount()
	require.NoError(t, err)

	// A real distribution, committed by the next block.
	distributed := owed.QuoRaw(4)
	_, err = miningkeeper.NewMsgServer(a.MiningKeeper).SubmitSettlementChunk(chain.headContext(),
		&miningtypes.MsgSubmitSettlementChunk{
			SettlementAddress: chain.credential, SlotId: 1, Epoch: 1, ChunkIndex: 0,
			Payouts: []*miningtypes.SettlementChunkPayout{
				{Recipient: acc(0x55), Amount: distributed.String()},
			},
		})
	require.NoError(t, err)
	chain.commitThrough(t, beforeChunk+5)
	afterChunk := chain.head

	settlementAt := func(height int64) *miningtypes.QuerySettlementResponse {
		var res miningtypes.QuerySettlementResponse
		queryAtHeight(t, a, settlementMethod,
			&miningtypes.QuerySettlementRequest{SlotId: 1, Epoch: 1}, &res, height)
		return &res
	}

	// The earlier height, asked AFTER the later state exists and is committed.
	// That ordering is the test: a leak from head could not show up if the later
	// state had not happened yet.
	earlier := settlementAt(beforeChunk)
	require.Zero(t, earlier.Settlement.NextChunkIndex, "no chunk had been accepted at this height")
	require.Equal(t, "0", earlier.ReleasedAmount)
	require.Equal(t, owed.String(), earlier.RemainingAmount)
	require.Equal(t, beforeChunk, int64(earlier.CurrentSettlementClock),
		"the clock is the one this height committed, not the one head holds")

	later := settlementAt(afterChunk)
	require.Equal(t, uint64(1), later.Settlement.NextChunkIndex)
	require.Equal(t, distributed.String(), later.ReleasedAmount)
	require.Equal(t, owed.Sub(distributed).String(), later.RemainingAmount)
	require.Equal(t, afterChunk, int64(later.CurrentSettlementClock))

	// The epoch-bound derived values are the same at both heights, and that is a
	// result rather than a triviality: each is computed from the anchor and the
	// target-bound parameters as they stood at the requested height, so a version
	// promoted later must not reach back and change what an earlier height owed or
	// how long it had.
	require.Equal(t, owed.String(), earlier.ParticipantDistributionCeiling)
	require.Equal(t, owed.String(), later.ParticipantDistributionCeiling,
		"the ceiling is the entitlement, not what remains of it")
	require.Equal(t, earlier.DeadlineClock, later.DeadlineClock)
	require.Equal(t, earlier.CreatedSettlementClock, later.CreatedSettlementClock)

	// Entitlement-side state moves with it, since the released amount the
	// settlement reports is the entitlement's own.
	entitlementAt := func(height int64) *rewardstypes.QuerySlotEntitlementResponse {
		var res rewardstypes.QuerySlotEntitlementResponse
		queryAtHeight(t, a, slotEntitlement,
			&rewardstypes.QuerySlotEntitlementRequest{SlotId: 1, Epoch: 1}, &res, height)
		return &res
	}
	require.Equal(t, "0", entitlementAt(beforeChunk).Entitlement.ReleasedAmount)
	require.Equal(t, distributed.String(), entitlementAt(afterChunk).Entitlement.ReleasedAmount,
		"one authority for the released amount, pinned like everything else")
}

// TestPermissionlessFinalizationIsReadAtThePinnedClock covers the derived value
// with the sharpest consequence.
//
// Crossing the deadline does not change who may RECEIVE funds, but it changes who
// may trigger finalization: before it, only the settlement signer; after it,
// anyone. A query that answered this from head would tell a worker inspecting an
// earlier height that the window had already closed, and the worker would conclude
// its distribution opportunity was gone while it was in fact still open.
func TestPermissionlessFinalizationIsReadAtThePinnedClock(t *testing.T) {
	chain := settledChain(t)

	var open miningtypes.QuerySettlementResponse
	queryAtHeight(t, chain.app, settlementMethod,
		&miningtypes.QuerySettlementRequest{SlotId: 1, Epoch: 1}, &open, chain.head)
	require.False(t, open.PermissionlessFinalizationNow, "the window opens with the settlement")

	// The window is measured in settlement-enabled blocks, so on an unpaused chain
	// the deadline clock is reached at that block height.
	deadline := int64(open.DeadlineClock)
	require.Equal(t, epochLength+int64(miningtypes.DefaultSettlementWindowEpochs)*epochLength, deadline,
		"the window is the ratified number of epochs past the anchor")

	inside := deadline - 1
	chain.commitThrough(t, deadline+2)

	var atDeadline miningtypes.QuerySettlementResponse
	queryAtHeight(t, chain.app, settlementMethod,
		&miningtypes.QuerySettlementRequest{SlotId: 1, Epoch: 1}, &atDeadline, deadline)
	require.True(t, atDeadline.PermissionlessFinalizationNow,
		"at the deadline the arm changes and any caller may finalize")

	var justInside miningtypes.QuerySettlementResponse
	queryAtHeight(t, chain.app, settlementMethod,
		&miningtypes.QuerySettlementRequest{SlotId: 1, Epoch: 1}, &justInside, inside)
	require.False(t, justInside.PermissionlessFinalizationNow,
		"one block earlier the window was still the signer's, and head must not say otherwise")
	require.Equal(t, inside, int64(justInside.CurrentSettlementClock))
}

// TestTargetEpochInterpretationResolvesTheModeHistoryOfThePinnedHeight proves the
// interpretation is state-backed rather than a constant.
//
// The distribution-mode history grows by promotion at epoch boundaries. A target
// interpreted after a promotion must report the row that governed it at the height
// asked about, because a consumer that froze a mode from one height and paid under
// another would be acting on an arm the chain never bound that target to.
//
// The second row is created the only legitimate way: a mode is SCHEDULED, and the
// epoch boundary promotes it. Nothing writes to the canonical history directly —
// fabricating rows would prove the query reads bytes at a version, not that it
// resolves the history the chain actually built.
func TestTargetEpochInterpretationResolvesTheModeHistoryOfThePinnedHeight(t *testing.T) {
	chain := bootPinnedChain(t)
	chain.commitThrough(t, 1)

	require.NoError(t, chain.app.MiningKeeper.ScheduledDistributionMode.Set(chain.headContext(), 2,
		miningtypes.ScheduledMiningDistributionMode{
			EffectiveEpoch: 2,
			Mode:           miningtypes.MiningDistributionMode_MINING_DISTRIBUTION_MODE_PROTOCOL_SELECTION,
		}))

	// The last height before epoch 1 closes: the schedule is pending and the
	// history still holds only its anchor.
	beforePromotion := epochLength - 1
	chain.commitThrough(t, beforePromotion)
	// The boundary block, where the epoch closes and the promotion happens.
	chain.commitThrough(t, epochLength)
	afterPromotion := chain.head

	interpret := func(target uint64, height int64) *miningtypes.QueryTargetEpochInterpretationResponse {
		var res miningtypes.QueryTargetEpochInterpretationResponse
		queryAtHeight(t, chain.app, interpretationQuery,
			&miningtypes.QueryTargetEpochInterpretationRequest{TargetEpoch: target}, &res, height)
		return &res
	}

	// Target 4 binds epoch 2, which is exactly the boundary the promotion took
	// effect at, so the two heights disagree about it — as they should.
	before := interpret(4, beforePromotion)
	require.Equal(t, uint64(2), before.BindingEpoch)
	require.Equal(t, uint64(1), before.DistributionModeVersion.Version)
	require.Equal(t, miningtypes.MiningDistributionMode_MINING_DISTRIBUTION_MODE_TRUSTED_AS_DISTRIBUTION,
		before.DistributionModeVersion.Mode)
	require.False(t, before.SelectionApplicable)

	after := interpret(4, afterPromotion)
	require.Equal(t, uint64(2), after.BindingEpoch, "the binding rule is arithmetic and does not move")
	require.Equal(t, uint64(2), after.DistributionModeVersion.Version, "the promoted row governs it now")
	require.Equal(t, miningtypes.MiningDistributionMode_MINING_DISTRIBUTION_MODE_PROTOCOL_SELECTION,
		after.DistributionModeVersion.Mode)
	require.True(t, after.SelectionApplicable)

	// Asked again with the later state committed: the earlier height keeps its own
	// answer.
	require.Equal(t, uint64(1), interpret(4, beforePromotion).DistributionModeVersion.Version,
		"a promotion must not reach back and rebind a target at an earlier height")

	// And a target bound before the promotion is unaffected at either height,
	// which is what makes the difference above about history rather than about the
	// query having become unstable.
	for _, height := range []int64{beforePromotion, afterPromotion} {
		bound := interpret(3, height)
		require.Equal(t, uint64(1), bound.BindingEpoch)
		require.Equal(t, uint64(1), bound.DistributionModeVersion.Version)
		require.False(t, bound.SelectionApplicable)
	}
}

// TestTheBlockHeightHeaderSelectsTheSameViewAsTheAbciPath closes the gap between
// the entry point a test can reach easily and the one consumers actually use.
//
// A consumer asks for a historical view by setting x-cosmos-block-height. That
// header is parsed by an interceptor installed when the gRPC services are
// registered, and nothing about the ABCI entry point demonstrates that the
// interceptor is present or resolves the same version. This drives both and
// requires them to agree.
func TestTheBlockHeightHeaderSelectsTheSameViewAsTheAbciPath(t *testing.T) {
	chain := settledChain(t)
	early := chain.head
	chain.commitThrough(t, early+10)

	querier := newHeaderQuerier(chain.app)

	for _, height := range []int64{early, chain.head} {
		var viaABCI, viaHeader miningtypes.QuerySettlementResponse
		request := &miningtypes.QuerySettlementRequest{SlotId: 1, Epoch: 1}
		queryAtHeight(t, chain.app, settlementMethod, request, &viaABCI, height)
		querier.query(t, settlementMethod, request, &viaHeader, height)
		require.Equalf(t, viaABCI.String(), viaHeader.String(),
			"the height header must select the same view as the query height at %d", height)
		require.Equal(t, height, int64(viaHeader.CurrentSettlementClock))

		var interpretation miningtypes.QueryTargetEpochInterpretationResponse
		querier.query(t, interpretationQuery,
			&miningtypes.QueryTargetEpochInterpretationRequest{TargetEpoch: 4}, &interpretation, height)
		require.Equal(t, uint64(2), interpretation.BindingEpoch)
	}

	// A height the chain has not reached is refused rather than silently served
	// from head, which is what keeps "not yet" from reading as "here it is".
	data, err := (&miningtypes.QuerySettlementRequest{SlotId: 1, Epoch: 1}).Marshal()
	require.NoError(t, err)
	res, err := chain.app.Query(context.Background(), &abci.RequestQuery{
		Path: settlementMethod, Data: data, Height: chain.head + 1_000,
	})
	require.NoError(t, err)
	require.NotZero(t, res.Code, "a future height is not a servable view")
	require.Contains(t, res.Log, "height")
	require.Empty(t, res.Value, "a refused query carries no state to mistake for an answer")
}

// TestTheAsCriticalReadSurfaceIsHeightPinned walks the rest of the surface a
// worker reconciles against.
//
// Each of these can move independently, and a worker reads them together. The
// pause state is included because it is the one that changes the meaning of every
// other answer: a paused chain still produces blocks, so a worker that read a
// stale pause state alongside a pinned clock would draw the wrong conclusion about
// why time had stopped.
func TestTheAsCriticalReadSurfaceIsHeightPinned(t *testing.T) {
	chain := settledChain(t)
	running := chain.head

	// Pause takes effect at H+1, so the state a height reports is the one that
	// governed it.
	_, err := rewardskeeper.NewMsgServer(chain.app.RewardsKeeper).PauseRewards(chain.headContext(),
		&rewardstypes.MsgPauseRewards{EmergencyAuthority: app.EmergencyAuthorityAddress()})
	require.NoError(t, err)
	chain.commitThrough(t, running+5)
	paused := chain.head

	pauseAt := func(height int64) *rewardstypes.QueryRewardsPauseStateResponse {
		var res rewardstypes.QueryRewardsPauseStateResponse
		queryAtHeight(t, chain.app, rewardsPauseState,
			&rewardstypes.QueryRewardsPauseStateRequest{}, &res, height)
		return &res
	}
	require.False(t, pauseAt(running).PauseState.CurrentPaused, "this height ran unpaused")
	require.True(t, pauseAt(running).ReleaseEnabled, "and permitted monetary release")
	require.True(t, pauseAt(paused).PauseState.CurrentPaused)
	require.False(t, pauseAt(paused).ReleaseEnabled,
		"the pause governs accrual and release together")

	// The settlement clock stops with the pause, which is the property the whole
	// deadline model rests on — and it must be visible per height, not only now.
	clockAt := func(height int64) uint64 {
		var res miningtypes.QuerySettlementClockResponse
		queryAtHeight(t, chain.app, settlementClock,
			&miningtypes.QuerySettlementClockRequest{}, &res, height)
		return res.SettlementClock
	}
	require.Equal(t, uint64(running), clockAt(running))
	require.Greater(t, uint64(paused), clockAt(paused),
		"a paused chain produces blocks without producing settlement time")

	// Epoch boundaries and the CoreSlot are stable here, and answering at a pinned
	// height is still the contract: a query that could not resolve them at an
	// earlier version would break reconciliation just as surely as one that
	// answered from head.
	for _, height := range []int64{running, paused} {
		var boundaries rewardstypes.QueryEpochBoundariesResponse
		queryAtHeight(t, chain.app, epochBoundaries,
			&rewardstypes.QueryEpochBoundariesRequest{EpochNumber: 1}, &boundaries, height)
		require.Equal(t, uint64(1), boundaries.StartHeight)
		require.Equal(t, uint64(epochLength), boundaries.EndHeight)

		var slot coreslottypes.QueryCoreSlotResponse
		queryAtHeight(t, chain.app, coreSlotMethod,
			&coreslottypes.QueryCoreSlotRequest{SlotId: 1}, &slot, height)
		require.Equal(t, chain.credential, slot.Slot.SettlementAddress)
		require.Equal(t, chain.payout, slot.Slot.PayoutAddress)
	}
}

// TestEconomicAddressValidationIsNotAHistoricalQuery states the contract that
// differs, so nobody later "fixes" it into a claim it cannot support.
//
// The module-account and bank-blocked sets are application configuration copied
// into the validator when the process is constructed. They are not consensus state
// read through the query context, and supplying a height does not reconstruct the
// configuration the node was running under at that height. The query therefore
// reports admissibility under the SERVING NODE'S CURRENT rule, and answering
// identically at every height is the honest behavior rather than a defect.
func TestEconomicAddressValidationIsNotAHistoricalQuery(t *testing.T) {
	chain := settledChain(t)
	early := chain.head
	chain.commitThrough(t, early+10)

	for _, address := range []string{acc(0x55), "", "not-an-address"} {
		var atEarly, atHead miningtypes.QueryValidateEconomicAddressResponse
		request := &miningtypes.QueryValidateEconomicAddressRequest{Address: address}
		queryAtHeight(t, chain.app, economicAddress, request, &atEarly, early)
		queryAtHeight(t, chain.app, economicAddress, request, &atHead, chain.head)
		require.Equal(t, atEarly.String(), atHead.String(),
			"the rule is process configuration; a height does not change which rule answers")
	}

	// It is still a real answer rather than an inert one: the enumerated verdicts
	// are reachable here exactly as they are at the keeper.
	var admissible, empty miningtypes.QueryValidateEconomicAddressResponse
	queryAtHeight(t, chain.app, economicAddress,
		&miningtypes.QueryValidateEconomicAddressRequest{Address: acc(0x55)}, &admissible, early)
	require.True(t, admissible.Admissible)
	queryAtHeight(t, chain.app, economicAddress,
		&miningtypes.QueryValidateEconomicAddressRequest{Address: ""}, &empty, early)
	require.False(t, empty.Admissible)
	require.Equal(t,
		miningtypes.EconomicAddressRejectionReason_ECONOMIC_ADDRESS_REJECTION_REASON_EMPTY,
		empty.RejectionReason)
}
