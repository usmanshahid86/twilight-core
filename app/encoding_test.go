package app_test

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/app"
	miningtypes "github.com/twilight-project/twilight-core/x/mining/types"
	rewardstypes "github.com/twilight-project/twilight-core/x/rewards/types"
)

// The client-side codec has to know every message the chain can execute.
//
// Block execution uses the application's own registry, which the module manager
// populates while building the App. Nothing else does: the CLI, the node's
// transaction service and offline transaction building all go through
// MakeEncodingConfig. When those two disagree the chain executes a message
// perfectly and then cannot read it back, and no consensus path fails to say so.

// TestEveryChainMsgResolvesFromTheClientCodec walks the exported type-URL manifest
// rather than a list written here.
//
// The manifest is generated from the protos and CI already fails when it drifts, so
// a module added later arrives in this test automatically. A hand-written list
// would pass forever while the surface it claims to cover grew past it — which is
// the shape of the defect this test exists for.
func TestEveryChainMsgResolvesFromTheClientCodec(t *testing.T) {
	raw, err := os.ReadFile("../docs/proto/twilight-msg-type-urls.json")
	require.NoError(t, err)

	var manifest struct {
		Modules map[string][]string `json:"modules"`
	}
	require.NoError(t, json.Unmarshal(raw, &manifest))
	require.NotEmpty(t, manifest.Modules, "the manifest must list the chain's messages")

	encoding := app.MakeEncodingConfig()

	checked := 0
	for module, urls := range manifest.Modules {
		require.NotEmptyf(t, urls, "module %s lists no messages", module)
		for _, url := range urls {
			_, err := encoding.InterfaceRegistry.Resolve(url)
			require.NoErrorf(t, err,
				"%s is executable on this chain but unresolvable from the client codec", url)
			checked++
		}
	}
	require.GreaterOrEqual(t, checked, 16, "the manifest should cover every custom message")
}

// TestCustomMsgsMarshalThroughTheLegacyAminoCodec covers the second registration
// the same absent pass would have performed.
//
// Amino needs the concrete-name registration to encode a value held as an
// interface, which is how a transaction carries a message. Without it the failure
// is "cannot encode unregistered concrete type" at signing time, far from its
// cause.
//
// x/coreslot is deliberately absent from this table and the omission is not an
// oversight to be tidied away: its RegisterLegacyAminoCodec is EMPTY, and its
// protos declare one amino.name across eleven messages. That gap is inside the
// module and predates this fix — registering the client codec cannot invent names
// the module never assigned — so it is reported separately rather than papered
// over with a skipped assertion here.
func TestCustomMsgsMarshalThroughTheLegacyAminoCodec(t *testing.T) {
	encoding := app.MakeEncodingConfig()

	for name, msg := range map[string]sdk.Msg{
		"mining":  &miningtypes.MsgSubmitSettlementChunk{SlotId: 1},
		"rewards": &rewardstypes.MsgPauseRewards{},
	} {
		t.Run(name, func(t *testing.T) {
			held := msg
			bz, err := encoding.Amino.MarshalJSON(&held)
			require.NoError(t, err, "an interface-held message must carry its registered name")
			require.NotEmpty(t, bz)
		})
	}
}
