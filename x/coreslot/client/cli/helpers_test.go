package cli

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/spf13/cobra"

	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	sdked25519 "github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"

	"github.com/twilight-project/twilight-core/x/coreslot/types"
)

// A real 32-byte Ed25519 public key, in the base64 form `tendermint
// show-validator` prints and `coreslot register` has always accepted.
const testPubKeyB64 = "ng/9/P+38duhpl+kHTjbI85xBqRKhjItaG8ftfNXBOQ="

func showValidatorJSON(key string) string {
	return `{"@type":"/cosmos.crypto.ed25519.PubKey","key":"` + key + `"}`
}

// The two input forms accepted by TRANSACTIONS must be interchangeable, not merely both valid.
//
// An operator reads a key out of `tendermint show-validator` and pastes it into
// `coreslot register`. If the JSON form produced an Any that differed in any
// byte from the bare form, admission would depend on which way the key was
// copied — so this asserts byte equality of the marshaled Any, not just that
// both calls succeed.
func TestTxPubKeyAnyAcceptsBothFormsIdentically(t *testing.T) {
	fromBare, err := txPubKeyAny(testPubKeyB64)
	require.NoError(t, err)
	fromJSON, err := txPubKeyAny(showValidatorJSON(testPubKeyB64))
	require.NoError(t, err)

	require.Equal(t, ed25519PubKeyTypeURL, fromBare.TypeUrl)
	require.Equal(t, fromBare.TypeUrl, fromJSON.TypeUrl)
	require.Equal(t, fromBare.Value, fromJSON.Value, "both input forms must yield a byte-identical Any")

	// And the Any actually carries the key that went in.
	raw, err := base64.StdEncoding.DecodeString(testPubKeyB64)
	require.NoError(t, err)
	expected, err := txPubKeyAny(base64.StdEncoding.EncodeToString(raw))
	require.NoError(t, err)
	require.Equal(t, expected.Value, fromJSON.Value)
	require.Len(t, raw, sdked25519.PubKeySize)
}

// Surrounding whitespace is trimmed before the form is detected, because a key
// pasted from a terminal or read from a file routinely carries a trailing
// newline. Detection is by leading brace, which is unambiguous: `{` is not in
// the base64 alphabet.
func TestTxPubKeyAnyTrimsWhitespaceBeforeDetectingForm(t *testing.T) {
	reference, err := txPubKeyAny(testPubKeyB64)
	require.NoError(t, err)

	for name, input := range map[string]string{
		"bare with newline":    testPubKeyB64 + "\n",
		"bare with spaces":     "  " + testPubKeyB64 + "  ",
		"JSON with newline":    showValidatorJSON(testPubKeyB64) + "\n",
		"JSON with whitespace": "\t " + showValidatorJSON(testPubKeyB64) + " \n",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := txPubKeyAny(input)
			require.NoError(t, err)
			require.Equal(t, reference.Value, got.Value)
		})
	}
}

// Unknown JSON fields are ignored deliberately. `@type` is what establishes the
// key is the kind we are about to claim it is; rejecting an added upstream
// metadata field would break operator onboarding while enforcing nothing.
func TestTxPubKeyAnyIgnoresUnknownJSONFields(t *testing.T) {
	reference, err := txPubKeyAny(testPubKeyB64)
	require.NoError(t, err)

	got, err := txPubKeyAny(
		`{"@type":"/cosmos.crypto.ed25519.PubKey","key":"` + testPubKeyB64 + `","future_field":"ignored"}`)
	require.NoError(t, err)
	require.Equal(t, reference.Value, got.Value)
}

// Every way the input can be wrong must be refused, and refused distinguishably:
// an operator debugging a failed admission needs to know whether the key was the
// wrong type, the wrong length, or simply mistyped.
func TestTxPubKeyAnyRejectsBadInput(t *testing.T) {
	shortKey := base64.StdEncoding.EncodeToString(make([]byte, 31))

	for name, tc := range map[string]struct{ input, wantErr string }{
		"wrong @type": {
			`{"@type":"/cosmos.crypto.secp256k1.PubKey","key":"` + testPubKeyB64 + `"}`,
			"@type must be",
		},
		"missing @type": {
			`{"key":"` + testPubKeyB64 + `"}`,
			"@type must be",
		},
		"empty key field": {
			showValidatorJSON(""),
			`has no "key" value`,
		},
		"malformed JSON": {
			`{"@type":"/cosmos.crypto.ed25519.PubKey","key":`,
			"parse consensus key JSON",
		},
		"invalid base64 inside JSON": {
			showValidatorJSON("not!valid!base64"),
			"decode consensus key",
		},
		"invalid bare base64": {
			"not!valid!base64",
			"decode consensus key",
		},
		"wrong decoded length, bare": {
			shortKey,
			"must be 32 bytes",
		},
		"wrong decoded length, JSON": {
			showValidatorJSON(shortKey),
			"must be 32 bytes",
		},
		"empty input": {
			"",
			"must be 32 bytes",
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := txPubKeyAny(tc.input)
			require.Error(t, err)
			require.Nil(t, got)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// pubKeyAny is the strict helper genesis authoring uses, and it must stay strict.
//
// `coreslot-genesis add` decodes the key through it to build the CoreSlot record,
// then writes the caller's ORIGINAL argument verbatim into the CometBFT
// validator entry. Any input it accepts but does not round-trip byte-for-byte
// yields a genesis whose two halves name different keys — written, exit 0, and
// caught only later by `coreslot-genesis validate`.
//
// So the transaction forms must be refused here even though they are valid keys.
// This is the unit-level counterpart to the byte-preservation tests in
// genesis_test.go: those prove the document is untouched, this proves why.
func TestPubKeyAnyStaysBareBase64Only(t *testing.T) {
	// Newline-padded input is refused now. Go's base64 decoder ignores \r and \n
	// (though not spaces), so these decode to the right bytes and would have been
	// written back non-canonically — the divergence this helper exists to prevent.
	for name, input := range map[string]string{
		"show-validator JSON":  showValidatorJSON(testPubKeyB64),
		"trailing newline":     testPubKeyB64 + "\n",
		"leading newline":      "\n" + testPubKeyB64,
		"embedded newline":     testPubKeyB64[:10] + "\n" + testPubKeyB64[10:],
		"carriage return":      testPubKeyB64 + "\r\n",
		"leading space":        " " + testPubKeyB64,
		"surrounding spaces":   "  " + testPubKeyB64 + "  ",
		"invalid base64":       "not!valid!base64",
		"wrong decoded length": base64.StdEncoding.EncodeToString(make([]byte, 31)),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := pubKeyAny(input)
			require.Error(t, err, "genesis authoring must not accept a form it cannot write back verbatim")
			require.Nil(t, got)
		})
	}
}

// The strict and expanded helpers must agree on the bytes they emit for the one
// input both accept, so a key admitted at genesis and the same key submitted by
// transaction describe an identical validator.
func TestBothHelpersAgreeOnABareKey(t *testing.T) {
	strict, err := pubKeyAny(testPubKeyB64)
	require.NoError(t, err)
	expanded, err := txPubKeyAny(testPubKeyB64)
	require.NoError(t, err)

	require.Equal(t, strict.TypeUrl, expanded.TypeUrl)
	require.Equal(t, strict.Value, expanded.Value)
}

// decodeParams is exercised directly, not imitated.
//
// The previous version of this test called cdc.UnmarshalJSON and json.Unmarshal
// separately and never touched the production decoder, so it stayed green
// whichever branch production kept — including the fallback that made a typo'd
// field decode to zero. A test that routes around the code it is about cannot
// fail for the reason it exists.
func paramsCmdWithCodec(t *testing.T) *cobra.Command {
	t.Helper()
	registry := codectypes.NewInterfaceRegistry()
	types.RegisterInterfaces(registry)

	// The real command, so its flags and context handling are the ones under test.
	cmd := updateParamsCmd()
	clientCtx := client.Context{}.
		WithCodec(codec.NewProtoCodec(registry)).
		WithTxConfig(nil).
		WithOffline(true)
	cmd.SetContext(context.WithValue(context.Background(), client.ClientContextKey, &clientCtx))
	return cmd
}

const paramsTestAddr = "twilight1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqx9k9g5"

func paramsJSON(t *testing.T, fields string) []byte {
	t.Helper()
	return []byte(`{"authority":"` + paramsTestAddr + `","emergency_authority":"` + paramsTestAddr + `",` + fields + `}`)
}

// Both numeric spellings must decode, and to the SAME thing. gogoproto's jsonpb
// strips the quotes from a 64-bit integer before parsing, so the chain's own
// codec reads query output and a hand-written file alike — which is why no second
// parser is needed.
func TestDecodeParamsAcceptsBothNumericSpellings(t *testing.T) {
	cmd := paramsCmdWithCodec(t)

	quoted, err := decodeParams(cmd, paramsJSON(t,
		`"slot_voting_power":"1","min_active_slots":"1","max_active_slots":"100",`+
			`"key_rotation_delay_blocks":"1","consensus_key_reuse_lockout":"100000",`+
			`"selection_policy_update_cooldown_blocks":"720"`))
	require.NoError(t, err, "query output must decode; it is the document the chain itself produced")

	bare, err := decodeParams(cmd, paramsJSON(t,
		`"slot_voting_power":1,"min_active_slots":1,"max_active_slots":100,`+
			`"key_rotation_delay_blocks":1,"consensus_key_reuse_lockout":100000,`+
			`"selection_policy_update_cooldown_blocks":720`))
	require.NoError(t, err, "a hand-written file with bare numbers must keep working")

	require.Equal(t, *bare, *quoted, "both spellings must produce identical Params")
	require.EqualValues(t, 100, quoted.MaxActiveSlots)
	require.EqualValues(t, 1, quoted.SlotVotingPower)
	require.EqualValues(t, 100000, quoted.ConsensusKeyReuseLockout)
}

// A field the parser does not recognize must be an error, not a silent default.
//
// update-params writes the WHOLE struct, so an unrecognized name means some real
// field keeps its zero value. Params.Validate permits zero for several of them,
// so the transaction would succeed and persist a parameter nobody chose — the
// bulk-write hazard, reintroduced through a decoder.
func TestDecodeParamsRejectsUnknownFields(t *testing.T) {
	cmd := paramsCmdWithCodec(t)

	// BARE numbers throughout, deliberately. An encoding/json fallback rejects a
	// quoted number outright, so a quoted fixture would fail for the wrong reason
	// and pass whether or not the fallback exists. Only a document encoding/json
	// would happily accept — bare numbers, unknown field ignored — discriminates
	// between the two decoders. Verified: with the fallback restored, these fail;
	// with quoted fixtures they did not.
	for name, fields := range map[string]string{
		// One character short of key_rotation_delay_blocks: the realistic typo.
		"singular typo":  `"max_active_slots":100,"key_rotation_delay_block":5`,
		"invented field": `"max_active_slots":100,"max_activ_slots":7`,
		"stray field":    `"max_active_slots":100,"note":"why I changed this"`,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := decodeParams(cmd, paramsJSON(t, fields))
			require.Error(t, err, "an unrecognized field must fail rather than default to zero")
			require.Nil(t, got)
			require.Contains(t, err.Error(), "unknown field")
		})
	}
}

func TestDecodeParamsRejectsInvalidValues(t *testing.T) {
	cmd := paramsCmdWithCodec(t)

	for name, fields := range map[string]string{
		"wrong type":        `"max_active_slots":"not-a-number"`,
		"boolean for uint":  `"max_active_slots":true`,
		"negative for uint": `"max_active_slots":"-1"`,
		// Beyond uint64.
		"out of range":   `"max_active_slots":"18446744073709551616"`,
		"malformed json": `"max_active_slots":`,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := decodeParams(cmd, paramsJSON(t, fields))
			require.Error(t, err)
			require.Nil(t, got)
		})
	}
}
