package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdked25519 "github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/x/coreslot/types"
)

// TestAddGenesisSlotProducesConformingGenesis exercises the genesis-authoring
// command end to end on a temporary home directory.
//
// The command builds two things that must agree: the CoreSlot module state and
// the CometBFT validator list. They are assembled from the same positional
// arguments, so an off-by-one in either — the kind an inserted argument
// introduces — writes a genesis that only fails later, when a node refuses to
// start. This asserts both sides, and asserts the module state is accepted by the
// same validation the chain applies at boot.
func TestAddGenesisSlotProducesConformingGenesis(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, "config")
	require.NoError(t, os.MkdirAll(configDir, 0o755))

	authority := sdk.AccAddress(append([]byte{1}, make([]byte, 19)...)).String()
	emergency := sdk.AccAddress(append([]byte{2}, make([]byte, 19)...)).String()
	operator := sdk.AccAddress(append([]byte{3}, make([]byte, 19)...)).String()
	payout := sdk.AccAddress(append([]byte{4}, make([]byte, 19)...)).String()
	settlement := sdk.AccAddress(append([]byte{5}, make([]byte, 19)...)).String()

	key := make([]byte, sdked25519.PubKeySize)
	key[0] = 9
	pubkeyB64 := base64.StdEncoding.EncodeToString(key)
	const moniker = "node0"

	registry := codectypes.NewInterfaceRegistry()
	types.RegisterInterfaces(registry)
	cdc := codec.NewProtoCodec(registry)

	base := types.DefaultGenesis(authority, emergency)
	writeTestGenesis(t, configDir, cdc, base)

	cmd := addGenesisSlotCmd()
	cmd.SetArgs([]string{operator, payout, settlement, pubkeyB64, moniker})
	clientCtx := client.Context{}.WithCodec(cdc).WithHomeDir(home)
	cmd.SetContext(context.WithValue(context.Background(), client.ClientContextKey, &clientCtx))
	require.NoError(t, cmd.Execute())

	doc, state := readTestGenesis(t, configDir)

	var genesis types.GenesisState
	require.NoError(t, cdc.UnmarshalJSON(state[types.ModuleName], &genesis))

	require.Len(t, genesis.Slots, 1)
	slot := genesis.Slots[0]
	require.Equal(t, operator, slot.OperatorAddress)
	require.Equal(t, payout, slot.PayoutAddress)
	require.Equal(t, settlement, slot.SettlementAddress, "the settlement address must come from its own argument")
	require.Equal(t, moniker, slot.Metadata.Moniker, "the moniker must not be read from the pubkey argument")
	require.Equal(t, types.SlotStatus_SLOT_STATUS_ACTIVE, slot.Status)

	// §80 fresh-genesis normalization for an ACTIVE slot.
	require.Equal(t, uint64(1), slot.ActivationSequence)
	require.Equal(t, genesisInitialHeight, slot.ActivatedHeight)
	require.Equal(t, genesisInitialHeight, slot.ActivationEffectiveHeight)
	require.Equal(t, uint64(1), slot.CurrentSelectionPolicyVersion)
	require.Equal(t, int64(0), slot.LastSelectionPolicyUpdateHeight)

	require.Len(t, genesis.SelectionPolicies, 1)
	policy := genesis.SelectionPolicies[0]
	require.Equal(t, slot.SlotId, policy.SlotId)
	require.Equal(t, uint64(1), policy.PolicyVersion)
	require.Equal(t, genesisInitialHeight, policy.ValidFromHeight)
	require.Equal(t, int64(0), policy.ValidUntilHeightExclusive)

	// The CometBFT validator entry must carry the CONSENSUS KEY, not whichever
	// argument happens to sit at that index.
	var validators []genesisValidator
	require.NoError(t, json.Unmarshal(doc["validators"], &validators))
	require.Len(t, validators, 1)
	require.Equal(t, pubkeyB64, validators[0].PubKey.Value)
	require.Equal(t, moniker, validators[0].Name)

	// And the whole thing satisfies the validation the chain applies at boot.
	require.NoError(t, genesis.Validate())
}

func writeTestGenesis(t *testing.T, configDir string, cdc codec.Codec, genesis *types.GenesisState) {
	t.Helper()
	moduleState, err := cdc.MarshalJSON(genesis)
	require.NoError(t, err)
	appState, err := json.Marshal(map[string]json.RawMessage{
		types.ModuleName: moduleState,
		"bank":           json.RawMessage(`{}`),
	})
	require.NoError(t, err)
	doc, err := json.Marshal(map[string]json.RawMessage{
		"app_state":  appState,
		"validators": json.RawMessage(`[]`),
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "genesis.json"), doc, 0o600))
}

func readTestGenesis(t *testing.T, configDir string) (map[string]json.RawMessage, map[string]json.RawMessage) {
	t.Helper()
	bz, err := os.ReadFile(filepath.Join(configDir, "genesis.json"))
	require.NoError(t, err)
	var doc map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(bz, &doc))
	var state map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(doc["app_state"], &state))
	return doc, state
}
