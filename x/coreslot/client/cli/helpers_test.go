package cli

import (
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"

	sdked25519 "github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
)

// A real 32-byte Ed25519 public key, in the base64 form `tendermint
// show-validator` prints and `coreslot register` has always accepted.
const testPubKeyB64 = "ng/9/P+38duhpl+kHTjbI85xBqRKhjItaG8ftfNXBOQ="

func showValidatorJSON(key string) string {
	return `{"@type":"/cosmos.crypto.ed25519.PubKey","key":"` + key + `"}`
}

// The two accepted input forms must be interchangeable, not merely both valid.
//
// An operator reads a key out of `tendermint show-validator` and pastes it into
// `coreslot register`. If the JSON form produced an Any that differed in any
// byte from the bare form, admission would depend on which way the key was
// copied — so this asserts byte equality of the marshaled Any, not just that
// both calls succeed.
func TestPubKeyAnyAcceptsBothFormsIdentically(t *testing.T) {
	fromBare, err := pubKeyAny(testPubKeyB64)
	require.NoError(t, err)
	fromJSON, err := pubKeyAny(showValidatorJSON(testPubKeyB64))
	require.NoError(t, err)

	require.Equal(t, ed25519PubKeyTypeURL, fromBare.TypeUrl)
	require.Equal(t, fromBare.TypeUrl, fromJSON.TypeUrl)
	require.Equal(t, fromBare.Value, fromJSON.Value, "both input forms must yield a byte-identical Any")

	// And the Any actually carries the key that went in.
	raw, err := base64.StdEncoding.DecodeString(testPubKeyB64)
	require.NoError(t, err)
	expected, err := pubKeyAny(base64.StdEncoding.EncodeToString(raw))
	require.NoError(t, err)
	require.Equal(t, expected.Value, fromJSON.Value)
	require.Len(t, raw, sdked25519.PubKeySize)
}

// Surrounding whitespace is trimmed before the form is detected, because a key
// pasted from a terminal or read from a file routinely carries a trailing
// newline. Detection is by leading brace, which is unambiguous: `{` is not in
// the base64 alphabet.
func TestPubKeyAnyTrimsWhitespaceBeforeDetectingForm(t *testing.T) {
	reference, err := pubKeyAny(testPubKeyB64)
	require.NoError(t, err)

	for name, input := range map[string]string{
		"bare with newline":    testPubKeyB64 + "\n",
		"bare with spaces":     "  " + testPubKeyB64 + "  ",
		"JSON with newline":    showValidatorJSON(testPubKeyB64) + "\n",
		"JSON with whitespace": "\t " + showValidatorJSON(testPubKeyB64) + " \n",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := pubKeyAny(input)
			require.NoError(t, err)
			require.Equal(t, reference.Value, got.Value)
		})
	}
}

// Unknown JSON fields are ignored deliberately. `@type` is what establishes the
// key is the kind we are about to claim it is; rejecting an added upstream
// metadata field would break operator onboarding while enforcing nothing.
func TestPubKeyAnyIgnoresUnknownJSONFields(t *testing.T) {
	reference, err := pubKeyAny(testPubKeyB64)
	require.NoError(t, err)

	got, err := pubKeyAny(
		`{"@type":"/cosmos.crypto.ed25519.PubKey","key":"` + testPubKeyB64 + `","future_field":"ignored"}`)
	require.NoError(t, err)
	require.Equal(t, reference.Value, got.Value)
}

// Every way the input can be wrong must be refused, and refused distinguishably:
// an operator debugging a failed admission needs to know whether the key was the
// wrong type, the wrong length, or simply mistyped.
func TestPubKeyAnyRejectsBadInput(t *testing.T) {
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
			got, err := pubKeyAny(tc.input)
			require.Error(t, err)
			require.Nil(t, got)
			require.Contains(t, err.Error(), tc.wantErr)
		})
	}
}
