package app_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtx "github.com/cosmos/cosmos-sdk/x/auth/tx"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	"github.com/twilight-project/twilight-core/app"
	"github.com/twilight-project/twilight-core/app/params"
)

// The cap must not become looser than the maximum fan-out permitted in one
// AUTHORIZED settlement chunk.
//
// One-directional on purpose, and narrow on purpose. It is not a claim that the
// two are the same parameter: HardMaxChunksPerSettlement lets a settlement span
// four chunks, so the protocol's broader settlement fan-out reaches 128, and no
// claim is made here about a privileged transaction in general. The cap is an
// independently ratified bank bound; this only stops the unprivileged bound
// drifting above the per-chunk authorized one, and leaves the settlement bound
// free to rise on its own terms.
func TestTheBankOutputCapDoesNotExceedTheAuthorizedChunkFanOut(t *testing.T) {
	require.LessOrEqual(t, uint64(app.MaxBankOutputsPerTx), params.HardMaxRecipientsPerChunk,
		"an unprivileged bank transaction may not demand more recipient fan-out than "+
			"the immutable maximum permitted in one authorized settlement chunk")
}

// A REAL transaction built through the SDK's tx builder, so the object the cap
// inspects is the one a node would decode off the wire.
func txOf(t *testing.T, msgs ...sdk.Msg) sdk.Tx {
	t.Helper()
	registry := codectypes.NewInterfaceRegistry()
	banktypes.RegisterInterfaces(registry)
	builder := authtx.NewTxConfig(codec.NewProtoCodec(registry), authtx.DefaultSignModes).NewTxBuilder()
	require.NoError(t, builder.SetMsgs(msgs...))
	return builder.GetTx()
}

// sender is shared by every MsgSend below, so the repeated-MsgSend cases model
// the real bypass: many recipient writes under ONE authentication envelope.
func sender() string { return addrOf(0xD1).String() }

func multiSend(outputs int) *banktypes.MsgMultiSend {
	outs := make([]banktypes.Output, outputs)
	for i := range outs {
		outs[i] = banktypes.Output{Address: addrOf(0xD0, byte(i)).String(), Coins: coins(minimum())}
	}
	return &banktypes.MsgMultiSend{
		Inputs:  []banktypes.Input{{Address: sender(), Coins: coins(int64(outputs) * minimum())}},
		Outputs: outs,
	}
}

func sends(n int) []sdk.Msg {
	msgs := make([]sdk.Msg, 0, n)
	for i := 0; i < n; i++ {
		msgs = append(msgs, &banktypes.MsgSend{
			FromAddress: sender(),
			ToAddress:   addrOf(0xD3, byte(i>>8), byte(i)).String(),
			Amount:      coins(minimum()),
		})
	}
	return msgs
}

// Taken from the built application rather than constructed in the test, so a cap
// that was never wired in cannot pass.
func wiredAnte(t *testing.T) (sdk.AnteHandler, sdk.Context) {
	t.Helper()
	a, ctx, _ := fundingApp(t)
	handler := a.AnteHandler()
	require.NotNil(t, handler, "the application must have an ante handler")
	return handler, ctx
}

// capRefused reports whether the error is this rule's rather than a later
// decorator's. The at-the-cap cases depend on the distinction.
func capRefused(err error) bool {
	return err != nil && strings.Contains(err.Error(), "above the maximum of")
}

// run returns whether the bank-output cap refused the transaction.
func run(t *testing.T, msgs ...sdk.Msg) bool {
	t.Helper()
	handler, ctx := wiredAnte(t)
	_, err := handler(ctx, txOf(t, msgs...), false)
	return capRefused(err)
}

// ---------------------------------------------------------------------------
// MsgMultiSend
// ---------------------------------------------------------------------------

func TestMultiSendOneOutputAboveTheCapIsRefused(t *testing.T) {
	require.True(t, run(t, multiSend(app.MaxBankOutputsPerTx+1)),
		"one output above the cap must be refused")
}

// Exactly at the cap must PASS this rule and then fail further down the chain.
// Reaching a later decorator is what proves the wrapper delegated rather than
// replaced the SDK chain.
func TestExactlyTheCapPassesAndReachesTheRestOfTheChain(t *testing.T) {
	handler, ctx := wiredAnte(t)
	_, err := handler(ctx, txOf(t, multiSend(app.MaxBankOutputsPerTx)), false)
	require.Error(t, err, "an unsigned transaction must still be refused by the chain")
	require.False(t, capRefused(err),
		"at the cap the refusal must come from a later decorator, not this rule; got: %v", err)
}

func TestOutputsAreSummedAcrossEveryMultiSend(t *testing.T) {
	half := app.MaxBankOutputsPerTx / 2
	require.False(t, run(t, multiSend(half), multiSend(half)),
		"two messages summing to the cap must pass this rule")
	require.True(t, run(t, multiSend(half), multiSend(half), multiSend(1)),
		"messages summing above the cap must be refused however they are split")
}

// ---------------------------------------------------------------------------
// MsgSend — the bypass this correction closes
// ---------------------------------------------------------------------------

// Repeated MsgSend produces the same recipient fan-out as one MsgMultiSend, under
// a single authentication envelope. Counting only MsgMultiSend left this
// unbounded while the rule claimed a transaction-wide bound.
func TestRepeatedMsgSendAboveTheCapIsRefused(t *testing.T) {
	require.True(t, run(t, sends(app.MaxBankOutputsPerTx+1)...),
		"a transaction of %d MsgSend from one sender must be refused",
		app.MaxBankOutputsPerTx+1)
}

func TestRepeatedMsgSendAtTheCapPassesAndReachesTheRestOfTheChain(t *testing.T) {
	handler, ctx := wiredAnte(t)
	_, err := handler(ctx, txOf(t, sends(app.MaxBankOutputsPerTx)...), false)
	require.Error(t, err, "an unsigned transaction must still be refused by the chain")
	require.False(t, capRefused(err),
		"at the cap the refusal must come from a later decorator; got: %v", err)
}

// ---------------------------------------------------------------------------
// mixed message types
// ---------------------------------------------------------------------------

func TestOutputsAreSummedAcrossMessageTypes(t *testing.T) {
	cap := app.MaxBankOutputsPerTx

	mixed := append([]sdk.Msg{multiSend(cap - 1)}, sends(1)...)
	require.False(t, run(t, mixed...), "%d + 1 must reach the cap exactly and pass", cap-1)

	over := append([]sdk.Msg{multiSend(cap - 1)}, sends(2)...)
	require.True(t, run(t, over...), "%d + 2 is one above the cap and must be refused", cap-1)

	split := append(sends(10), multiSend(cap-10))
	require.False(t, run(t, split...), "10 sends + %d outputs must reach the cap exactly", cap-10)

	splitOver := append(sends(10), multiSend(cap-10), multiSend(1))
	require.True(t, run(t, splitOver...), "one more output in either form must be refused")
}

// A bank message that moves nothing contributes zero. The unit counted is the
// recipient output, not the message.
func TestNonSendBankMessagesContributeNothing(t *testing.T) {
	params := make([]sdk.Msg, 0, app.MaxBankOutputsPerTx*4)
	for i := 0; i < app.MaxBankOutputsPerTx*4; i++ {
		params = append(params, &banktypes.MsgUpdateParams{Authority: sender()})
	}
	require.False(t, run(t, params...),
		"messages that produce no recipient output must not count toward the cap")
}

// The unit is the output OPERATION, not the distinct recipient. Two outputs to
// one address are two balance writes and cost consensus twice, so counting
// unique addresses would let a transaction repeat a recipient without limit.
func TestDuplicateRecipientsCountSeparately(t *testing.T) {
	same := addrOf(0xD4).String()
	outs := make([]banktypes.Output, app.MaxBankOutputsPerTx+1)
	for i := range outs {
		outs[i] = banktypes.Output{Address: same, Coins: coins(minimum())}
	}
	repeated := &banktypes.MsgMultiSend{
		Inputs:  []banktypes.Input{{Address: sender(), Coins: coins(int64(len(outs)) * minimum())}},
		Outputs: outs,
	}
	require.True(t, run(t, repeated),
		"outputs to a repeated address must each count; the cap bounds operations, not recipients")
}
