package cli

import (
	"encoding/base64"
	"testing"

	sdked25519 "github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
)

// FuzzPubKeyAny fuzzes the consensus-key parser that turns an operator-supplied,
// base64 CLI string into a packed ed25519 pubkey Any. This is untrusted input.
// Property: never panic; on success the Any is well-formed.
func FuzzPubKeyAny(f *testing.F) {
	valid := base64.StdEncoding.EncodeToString(make([]byte, sdked25519.PubKeySize))
	for _, s := range []string{
		"", "not-base64!!", valid,
		base64.StdEncoding.EncodeToString([]byte("short")),
		base64.StdEncoding.EncodeToString(make([]byte, sdked25519.PubKeySize+1)),
		"YWJj",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, value string) {
		packed, err := pubKeyAny(value)
		if err != nil {
			return
		}
		if packed == nil || packed.TypeUrl != "/cosmos.crypto.ed25519.PubKey" || len(packed.Value) == 0 {
			t.Fatalf("pubKeyAny(%q) returned a malformed Any on success", value)
		}
	})
}
