package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"math"
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

const (
	testMoniker      = "node0"
	testInitialAbsen = "" // sentinel: omit initial_height from the document entirely
)

func testCodec(t *testing.T) codec.Codec {
	t.Helper()
	registry := codectypes.NewInterfaceRegistry()
	types.RegisterInterfaces(registry)
	return codec.NewProtoCodec(registry)
}

func testAddresses() (authority, emergency, operator, payout, settlement string) {
	return sdk.AccAddress(append([]byte{1}, make([]byte, 19)...)).String(),
		sdk.AccAddress(append([]byte{2}, make([]byte, 19)...)).String(),
		sdk.AccAddress(append([]byte{3}, make([]byte, 19)...)).String(),
		sdk.AccAddress(append([]byte{4}, make([]byte, 19)...)).String(),
		sdk.AccAddress(append([]byte{5}, make([]byte, 19)...)).String()
}

func testConsensusKeyB64() string {
	key := make([]byte, sdked25519.PubKeySize)
	key[0] = 9
	return base64.StdEncoding.EncodeToString(key)
}

// runCLI executes a genesis subcommand against a temporary home directory.
func runCLI(t *testing.T, cmd interface {
	SetArgs([]string)
	SetContext(context.Context)
	Execute() error
}, home string, cdc codec.Codec, args ...string) error {
	t.Helper()
	cmd.SetArgs(args)
	clientCtx := client.Context{}.WithCodec(cdc).WithHomeDir(home)
	cmd.SetContext(context.WithValue(context.Background(), client.ClientContextKey, &clientCtx))
	return cmd.Execute()
}

// TestGenesisCLIHonoursTheDocumentInitialHeight is the S2 matrix.
//
// The genesis document owns the chain's first block height. These commands author
// slot rows that must be normalized against it, so they must READ it — a command
// that declared its own would be a second authority, and would silently corrupt
// any genesis whose initial height is not 1.
//
// The SDK convention is applied when reading: absent or zero means the chain
// starts at height 1, any other value is used exactly. Nothing rewrites the field.
func TestGenesisCLIHonoursTheDocumentInitialHeight(t *testing.T) {
	for _, tc := range []struct {
		name           string
		documentHeight string
		wantHeight     int64
	}{
		{"absent initial height", testInitialAbsen, 1},
		{"initial height 0", `"0"`, 1},
		{"initial height 1", `"1"`, 1},
		{"initial height 5", `"5"`, 5},
		{"bare numeric initial height", `5`, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			configDir := filepath.Join(home, "config")
			require.NoError(t, os.MkdirAll(configDir, 0o755))

			cdc := testCodec(t)
			authority, emergency, operator, payout, settlement := testAddresses()
			pubkeyB64 := testConsensusKeyB64()

			writeTestGenesis(t, configDir, cdc, types.DefaultGenesis(authority, emergency), tc.documentHeight)

			require.NoError(t, runCLI(t, addGenesisSlotCmd(), home, cdc,
				operator, payout, settlement, pubkeyB64, testMoniker))

			doc, state := readTestGenesis(t, configDir)
			var genesis types.GenesisState
			require.NoError(t, cdc.UnmarshalJSON(state[types.ModuleName], &genesis))

			require.Len(t, genesis.Slots, 1)
			slot := genesis.Slots[0]
			require.Equal(t, settlement, slot.SettlementAddress, "settlement comes from its own argument")
			require.Equal(t, testMoniker, slot.Metadata.Moniker, "moniker is not read from the pubkey argument")
			require.Equal(t, uint64(1), slot.ActivationSequence)
			require.Equal(t, tc.wantHeight, slot.ActivatedHeight)
			require.Equal(t, tc.wantHeight, slot.ActivationEffectiveHeight,
				"fresh genesis is the explicit exception to the runtime H+1 rule")
			require.Equal(t, uint64(1), slot.CurrentSelectionPolicyVersion)
			require.Equal(t, int64(0), slot.LastSelectionPolicyUpdateHeight)

			require.Len(t, genesis.SelectionPolicies, 1)
			require.Equal(t, tc.wantHeight, genesis.SelectionPolicies[0].ValidFromHeight)

			// The document's own field is left exactly as it was found, including the
			// zero case: preserving a zero while authoring slots at effective height 1
			// is precisely BaseApp's reading of it.
			requireDocumentHeightUnchanged(t, doc, tc.documentHeight)

			// The CometBFT validator entry carries the consensus key, not whichever
			// argument sits at that index.
			var validators []genesisValidator
			require.NoError(t, json.Unmarshal(doc["validators"], &validators))
			require.Len(t, validators, 1)
			require.Equal(t, pubkeyB64, validators[0].PubKey.Value)
			require.Equal(t, testMoniker, validators[0].Name)

			require.NoError(t, genesis.Validate(), "the authored genesis must satisfy boot validation")

			// set-authorities and validate must both leave the height alone.
			require.NoError(t, runCLI(t, setAuthoritiesCmd(), home, cdc, authority, emergency))
			afterAuthorities, _ := readTestGenesis(t, configDir)
			requireDocumentHeightUnchanged(t, afterAuthorities, tc.documentHeight)

			before, err := os.ReadFile(filepath.Join(configDir, "genesis.json"))
			require.NoError(t, err)
			require.NoError(t, runCLI(t, validateGenesisCmd(), home, cdc))
			after, err := os.ReadFile(filepath.Join(configDir, "genesis.json"))
			require.NoError(t, err)
			require.Equal(t, before, after, "validate must be read-only")
		})
	}
}

func TestGenesisCLIRejectsMalformedInitialHeight(t *testing.T) {
	for _, tc := range []struct{ name, height string }{
		{"negative", `"-1"`},
		{"empty string", `""`},
		{"not a number", `"soon"`},
		{"null", `null`},
		{"boolean", `true`},
		{"unrepresentable", `"99999999999999999999999"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			configDir := filepath.Join(home, "config")
			require.NoError(t, os.MkdirAll(configDir, 0o755))

			cdc := testCodec(t)
			authority, emergency, operator, payout, settlement := testAddresses()
			writeTestGenesis(t, configDir, cdc, types.DefaultGenesis(authority, emergency), tc.height)

			before, err := os.ReadFile(filepath.Join(configDir, "genesis.json"))
			require.NoError(t, err)

			require.Error(t, runCLI(t, addGenesisSlotCmd(), home, cdc,
				operator, payout, settlement, testConsensusKeyB64(), testMoniker),
				"a nonsensical height must not be silently normalized")

			after, err := os.ReadFile(filepath.Join(configDir, "genesis.json"))
			require.NoError(t, err)
			require.Equal(t, before, after, "a rejected add must not touch the file")
		})
	}
}

// TestGenesisCLIValidatePinsRowsToTheDocumentHeight proves validate does more
// than check that initial_height parses: it applies the same height relation the
// keeper will enforce at InitChain, while remaining byte-for-byte read-only.
func TestGenesisCLIValidatePinsRowsToTheDocumentHeight(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, "config")
	require.NoError(t, os.MkdirAll(configDir, 0o755))

	cdc := testCodec(t)
	authority, emergency, operator, payout, settlement := testAddresses()
	writeTestGenesis(t, configDir, cdc, types.DefaultGenesis(authority, emergency), `"1"`)
	require.NoError(t, runCLI(t, addGenesisSlotCmd(), home, cdc,
		operator, payout, settlement, testConsensusKeyB64(), testMoniker))

	doc, _ := readTestGenesis(t, configDir)
	doc["initial_height"] = json.RawMessage(`"5"`)
	require.NoError(t, saveGenesis(filepath.Join(configDir, "genesis.json"), doc))

	before, err := os.ReadFile(filepath.Join(configDir, "genesis.json"))
	require.NoError(t, err)
	err = runCLI(t, validateGenesisCmd(), home, cdc)
	require.ErrorIs(t, err, types.ErrInvalidGenesis)
	require.Contains(t, err.Error(), "initial height 5")
	after, readErr := os.ReadFile(filepath.Join(configDir, "genesis.json"))
	require.NoError(t, readErr)
	require.Equal(t, before, after, "rejected validation must not rewrite the file")
}

// TestGenesisCLIRejectsExhaustedSlotIDSpace is the S3 regression. The successor is
// computed with checked arithmetic before anything that will be saved is touched,
// so an exhausted identifier space fails without writing a file whose next id has
// wrapped to zero.
func TestGenesisCLIRejectsExhaustedSlotIDSpace(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, "config")
	require.NoError(t, os.MkdirAll(configDir, 0o755))

	cdc := testCodec(t)
	authority, emergency, operator, payout, settlement := testAddresses()
	base := types.DefaultGenesis(authority, emergency)
	base.NextSlotId = math.MaxUint64
	writeTestGenesis(t, configDir, cdc, base, `"1"`)

	before, err := os.ReadFile(filepath.Join(configDir, "genesis.json"))
	require.NoError(t, err)

	err = runCLI(t, addGenesisSlotCmd(), home, cdc,
		operator, payout, settlement, testConsensusKeyB64(), testMoniker)
	require.Error(t, err)
	require.Contains(t, err.Error(), "next slot id")

	after, err := os.ReadFile(filepath.Join(configDir, "genesis.json"))
	require.NoError(t, err)
	require.Equal(t, before, after, "a rejected add must leave the genesis bytes unchanged")
}

// requireDocumentHeightUnchanged asserts the top-level field still reads exactly
// as authored, including its absence.
func requireDocumentHeightUnchanged(t *testing.T, doc map[string]json.RawMessage, want string) {
	t.Helper()
	raw, present := doc["initial_height"]
	if want == testInitialAbsen {
		require.False(t, present, "initial_height must not be introduced by these commands")
		return
	}
	require.True(t, present, "initial_height must not be dropped")
	require.JSONEq(t, want, string(raw), "initial_height must not be rewritten")
}

func writeTestGenesis(t *testing.T, configDir string, cdc codec.Codec, genesis *types.GenesisState, initialHeight string) {
	t.Helper()
	moduleState, err := cdc.MarshalJSON(genesis)
	require.NoError(t, err)
	appState, err := json.Marshal(map[string]json.RawMessage{
		types.ModuleName: moduleState,
		"bank":           json.RawMessage(`{}`),
	})
	require.NoError(t, err)
	doc := map[string]json.RawMessage{
		"app_state":  appState,
		"validators": json.RawMessage(`[]`),
	}
	if initialHeight != testInitialAbsen {
		doc["initial_height"] = json.RawMessage(initialHeight)
	}
	bz, err := json.Marshal(doc)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(configDir, "genesis.json"), bz, 0o600))
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

// TestGenesisCLIPreservesExistingValidators is the incremental-build property.
//
// A genesis is assembled one `add` at a time, and each call rewrites the whole
// CometBFT validators array. So every call must first READ what is there. It
// previously discarded that decode error, which meant a malformed array silently
// produced a single-entry result — the operators added before it simply gone, in a
// document nobody re-reads before launch.
func TestGenesisCLIPreservesExistingValidators(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, "config")
	require.NoError(t, os.MkdirAll(configDir, 0o755))

	cdc := testCodec(t)
	authority, emergency, operator, payout, settlement := testAddresses()
	writeTestGenesis(t, configDir, cdc, types.DefaultGenesis(authority, emergency), `"1"`)

	// Two operators, added one after the other, as a real genesis is built.
	firstKey := testConsensusKeyB64()
	secondRaw := make([]byte, sdked25519.PubKeySize)
	secondRaw[0] = 11
	secondKey := base64.StdEncoding.EncodeToString(secondRaw)

	require.NoError(t, runCLI(t, addGenesisSlotCmd(), home, cdc,
		operator, payout, settlement, firstKey, "node0"))
	require.NoError(t, runCLI(t, addGenesisSlotCmd(), home, cdc,
		sdk.AccAddress(append([]byte{6}, make([]byte, 19)...)).String(),
		payout, settlement, secondKey, "node1"))

	doc, state := readTestGenesis(t, configDir)

	var validators []genesisValidator
	require.NoError(t, json.Unmarshal(doc["validators"], &validators))
	require.Len(t, validators, 2, "the second add must not discard the first validator")
	require.Equal(t, firstKey, validators[0].PubKey.Value)
	require.Equal(t, "node0", validators[0].Name)
	require.Equal(t, secondKey, validators[1].PubKey.Value)
	require.Equal(t, "node1", validators[1].Name)

	// The module state must agree: two slots, and the document still boots.
	var genesis types.GenesisState
	require.NoError(t, cdc.UnmarshalJSON(state[types.ModuleName], &genesis))
	require.Len(t, genesis.Slots, 2)
	require.NoError(t, genesis.Validate())
}

// TestGenesisCLIRefusesUnreadableValidators covers the case the discarded error
// used to swallow.
//
// Refusing is the only safe answer. The command cannot append to a list it cannot
// read, and writing the append anyway destroys whatever the malformed value stood
// for — silently, into the one file that has no earlier version to restore from.
func TestGenesisCLIRefusesUnreadableValidators(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, "config")
	require.NoError(t, os.MkdirAll(configDir, 0o755))

	cdc := testCodec(t)
	authority, emergency, operator, payout, settlement := testAddresses()
	writeTestGenesis(t, configDir, cdc, types.DefaultGenesis(authority, emergency), `"1"`)

	// A validators value that is present and undecodable, as a hand-edited or
	// half-written genesis would leave it.
	path := filepath.Join(configDir, "genesis.json")
	bz, err := os.ReadFile(path)
	require.NoError(t, err)
	var doc map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(bz, &doc))
	doc["validators"] = json.RawMessage(`{"not":"an array"}`)
	bz, err = json.Marshal(doc)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, bz, 0o600))
	before, err := os.ReadFile(path)
	require.NoError(t, err)

	err = runCLI(t, addGenesisSlotCmd(), home, cdc,
		operator, payout, settlement, testConsensusKeyB64(), testMoniker)
	require.Error(t, err, "an unreadable validators array must not be appended to")
	require.ErrorContains(t, err, "unreadable")

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, before, after, "a refused add must leave the document untouched")
}

// TestGenesisCLIToleratesAnAbsentValidatorsKey keeps the first add working.
//
// Absence is not corruption: the very first add legitimately finds no validators
// array, and a fix that refused it would break authoring a genesis from scratch —
// which is the only way one is ever authored.
func TestGenesisCLIToleratesAnAbsentValidatorsKey(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, "config")
	require.NoError(t, os.MkdirAll(configDir, 0o755))

	cdc := testCodec(t)
	authority, emergency, operator, payout, settlement := testAddresses()
	writeTestGenesis(t, configDir, cdc, types.DefaultGenesis(authority, emergency), `"1"`)

	path := filepath.Join(configDir, "genesis.json")
	bz, err := os.ReadFile(path)
	require.NoError(t, err)
	var doc map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(bz, &doc))

	for name, value := range map[string]json.RawMessage{
		"key absent": nil,
		"json null":  json.RawMessage(`null`),
	} {
		t.Run(name, func(t *testing.T) {
			fresh := map[string]json.RawMessage{}
			for k, v := range doc {
				fresh[k] = v
			}
			if value == nil {
				delete(fresh, "validators")
			} else {
				fresh["validators"] = value
			}
			out, err := json.Marshal(fresh)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(path, out, 0o600))

			require.NoError(t, runCLI(t, addGenesisSlotCmd(), home, cdc,
				operator, payout, settlement, testConsensusKeyB64(), testMoniker))

			written, _ := readTestGenesis(t, configDir)
			var validators []genesisValidator
			require.NoError(t, json.Unmarshal(written["validators"], &validators))
			require.Len(t, validators, 1)
		})
	}
}

// TestGenesisCLIRejectsNonBareConsensusKeys pins the genesis authoring contract
// to a bare base64 key, and proves a refusal leaves the document untouched.
//
// This is a regression, not a new rule. The transaction commands were widened to
// accept the object `tendermint show-validator` prints, and to tolerate
// surrounding whitespace, through a helper genesis authoring shared. Genesis
// then decoded the widened input into the CoreSlot record while writing the
// caller's ORIGINAL argument verbatim into the CometBFT validator entry — so for
// any input the two steps did not agree on, `coreslot-genesis add` exited 0 and
// produced a document whose two halves described different keys, caught only
// later by `coreslot-genesis validate`.
//
// The transaction paths carry only the decoded key, so they have no second
// representation to disagree with; genesis does. It therefore keeps the strict
// helper, and this test fails if the two are ever merged again.
func TestGenesisCLIRejectsNonBareConsensusKeys(t *testing.T) {
	bare := testConsensusKeyB64()

	for _, tc := range []struct{ name, key string }{
		{
			// Exactly what `twilightd tendermint show-validator` prints.
			"show-validator JSON",
			`{"@type":"/cosmos.crypto.ed25519.PubKey","key":"` + bare + `"}`,
		},
		{
			// A valid key that only becomes acceptable if the input is trimmed.
			// Genesis would write the padded string into the validator entry.
			"bare key with surrounding whitespace",
			"  " + bare + "\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			configDir := filepath.Join(home, "config")
			require.NoError(t, os.MkdirAll(configDir, 0o755))

			cdc := testCodec(t)
			authority, emergency, operator, payout, settlement := testAddresses()
			writeTestGenesis(t, configDir, cdc, types.DefaultGenesis(authority, emergency), `"1"`)

			path := filepath.Join(configDir, "genesis.json")
			before, err := os.ReadFile(path)
			require.NoError(t, err)

			require.Error(t,
				runCLI(t, addGenesisSlotCmd(), home, cdc, operator, payout, settlement, tc.key, testMoniker),
				"genesis add must refuse a key form it cannot write back verbatim")

			after, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, before, after,
				"a refused add must leave the genesis bytes byte-for-byte unchanged")
		})
	}
}

// TestGenesisCLIStillAcceptsABareConsensusKey is the other half of the test
// above: the contract was narrowed back, not broken. The documented genesis
// input — a bare base64 key, as scripts/localnet/gen-consensus-key.sh emits —
// must still be admitted and must still produce a document that validates.
func TestGenesisCLIStillAcceptsABareConsensusKey(t *testing.T) {
	home := t.TempDir()
	configDir := filepath.Join(home, "config")
	require.NoError(t, os.MkdirAll(configDir, 0o755))

	cdc := testCodec(t)
	authority, emergency, operator, payout, settlement := testAddresses()
	writeTestGenesis(t, configDir, cdc, types.DefaultGenesis(authority, emergency), `"1"`)

	require.NoError(t, runCLI(t, addGenesisSlotCmd(), home, cdc,
		operator, payout, settlement, testConsensusKeyB64(), testMoniker))

	// The written document must be internally consistent, which is precisely what
	// the divergent-representation bug produced but failed.
	require.NoError(t, runCLI(t, validateGenesisCmd(), home, cdc))
}

// TestGenesisCLIRejectsNonCanonicalConsensusKeys closes the last form of the
// divergence #145 began: an input that decodes to the right bytes but is not the
// canonical spelling of them.
//
// Go's base64 decoder ignores \r and \n, so a newline-padded key decoded fine
// while genesis wrote the caller's original string — newline included — into the
// CometBFT validator entry. `coreslot-genesis add` exited 0 and produced a
// document whose two halves named different keys, caught only by a later
// `coreslot-genesis validate`.
//
// A key read from a file or piped through a shell carries a trailing newline
// routinely, so this was reachable by ordinary use rather than by hand-editing.
func TestGenesisCLIRejectsNonCanonicalConsensusKeys(t *testing.T) {
	bare := testConsensusKeyB64()

	for name, key := range map[string]string{
		"trailing newline": bare + "\n",
		"leading newline":  "\n" + bare,
		"embedded newline": bare[:10] + "\n" + bare[10:],
		"carriage return":  bare + "\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			configDir := filepath.Join(home, "config")
			require.NoError(t, os.MkdirAll(configDir, 0o755))

			cdc := testCodec(t)
			authority, emergency, operator, payout, settlement := testAddresses()
			writeTestGenesis(t, configDir, cdc, types.DefaultGenesis(authority, emergency), `"1"`)

			path := filepath.Join(configDir, "genesis.json")
			before, err := os.ReadFile(path)
			require.NoError(t, err)

			require.Error(t,
				runCLI(t, addGenesisSlotCmd(), home, cdc, operator, payout, settlement, key, testMoniker),
				"a key that does not round-trip verbatim must be refused before anything is written")

			after, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, before, after,
				"a refused add must leave the genesis bytes byte-for-byte unchanged")
		})
	}
}
