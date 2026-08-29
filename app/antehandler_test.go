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

// ---------------------------------------------------------------------------
// the principle the value encodes
// ---------------------------------------------------------------------------

// One transaction may cause no more fan-out than the chain's most privileged
// transaction already may. A slot operator holding settlement credentials is
// limited to HardMaxRecipientsPerChunk in one MsgSettlementChunk; an anonymous
// sender must not exceed that.
//
// Asserted one-directionally on purpose. The cap is NOT derived from the
// settlement bound — no correctness invariant ties them, so binding them would
// let a settlement-capacity decision silently widen the spam surface. This
// catches the inversion returning while leaving the settlement bound free to
// rise on its own terms.
func TestTheOutputCapDoesNotExceedTheAuthorizedFanOut(t *testing.T) {
	require.LessOrEqual(t, uint64(app.MaxMultiSendOutputsPerTx), params.HardMaxRecipientsPerChunk,
		"an anonymous sender may not demand more fan-out in one transaction than a "+
			"credentialed operator may demand in one settlement chunk")
}

// ---------------------------------------------------------------------------
// a transaction carrying only the messages under test
// ---------------------------------------------------------------------------

// A REAL transaction built through the SDK's tx builder rather than a stand-in,
// so the object the cap inspects is the one a node would decode off the wire.
func txOf(t *testing.T, msgs ...sdk.Msg) sdk.Tx {
	t.Helper()
	registry := codectypes.NewInterfaceRegistry()
	banktypes.RegisterInterfaces(registry)
	builder := authtx.NewTxConfig(codec.NewProtoCodec(registry), authtx.DefaultSignModes).NewTxBuilder()
	require.NoError(t, builder.SetMsgs(msgs...))
	return builder.GetTx()
}

func multiSend(outputs int) *banktypes.MsgMultiSend {
	outs := make([]banktypes.Output, outputs)
	for i := range outs {
		outs[i] = banktypes.Output{Address: addrOf(0xD0, byte(i)).String(), Coins: coins(minimum())}
	}
	return &banktypes.MsgMultiSend{
		Inputs:  []banktypes.Input{{Address: addrOf(0xD1).String(), Coins: coins(int64(outputs) * minimum())}},
		Outputs: outs,
	}
}

// ---------------------------------------------------------------------------
// the cap, through the app's REAL wired ante chain
// ---------------------------------------------------------------------------

// Taken from the built application rather than by constructing a handler in the
// test, so a cap that was never wired in cannot pass. This is the lesson from
// #159, where every case exercised a keeper path no transaction takes.
func wiredAnte(t *testing.T) (sdk.AnteHandler, sdk.Context) {
	t.Helper()
	a, ctx, _ := fundingApp(t)
	handler := a.AnteHandler()
	require.NotNil(t, handler, "the application must have an ante handler")
	return handler, ctx
}

// capRefused reports whether the error is this rule's, as opposed to some later
// decorator's. The accept cases rely on the distinction.
func capRefused(err error) bool {
	return err != nil && strings.Contains(err.Error(), "above the maximum of")
}

func TestOneOutputAboveTheCapIsRefused(t *testing.T) {
	handler, ctx := wiredAnte(t)
	_, err := handler(ctx, txOf(t, multiSend(app.MaxMultiSendOutputsPerTx+1)), false)
	require.Error(t, err)
	require.True(t, capRefused(err), "must be refused by the output cap, got: %v", err)
}

// Exactly at the cap must PASS the cap. It then fails further down the chain,
// which is the point: reaching a later decorator proves the wrapper delegated
// rather than replaced.
func TestExactlyTheCapPassesThisCheckAndReachesTheRestOfTheChain(t *testing.T) {
	handler, ctx := wiredAnte(t)
	_, err := handler(ctx, txOf(t, multiSend(app.MaxMultiSendOutputsPerTx)), false)
	require.Error(t, err, "an unsigned transaction must still be refused by the chain")
	require.False(t, capRefused(err),
		"at the cap the refusal must come from a later decorator, not from this rule; got: %v", err)
}

// The cap is per TRANSACTION. Nothing limits messages per transaction, so a
// per-message cap would be defeated by splitting into several messages.
func TestOutputsAreSummedAcrossEveryMultiSendInTheTransaction(t *testing.T) {
	handler, ctx := wiredAnte(t)

	half := app.MaxMultiSendOutputsPerTx / 2
	_, err := handler(ctx, txOf(t, multiSend(half), multiSend(half)), false)
	require.False(t, capRefused(err), "two messages summing to the cap must pass this rule")

	_, err = handler(ctx, txOf(t, multiSend(half), multiSend(half), multiSend(1)), false)
	require.True(t, capRefused(err),
		"messages summing above the cap must be refused however they are split; got: %v", err)
}

// Ordinary sends carry one recipient and are not counted.
func TestNonMultiSendMessagesAreNotCounted(t *testing.T) {
	handler, ctx := wiredAnte(t)
	sends := make([]sdk.Msg, 0, app.MaxMultiSendOutputsPerTx*4)
	for i := 0; i < app.MaxMultiSendOutputsPerTx*4; i++ {
		sends = append(sends, &banktypes.MsgSend{
			FromAddress: addrOf(0xD1).String(),
			ToAddress:   addrOf(0xD2, byte(i)).String(),
			Amount:      coins(minimum()),
		})
	}
	_, err := handler(ctx, txOf(t, sends...), false)
	require.False(t, capRefused(err), "MsgSend has one recipient and must not count toward the cap")
}
