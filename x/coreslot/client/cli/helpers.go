package cli

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	sdked25519 "github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	gogoproto "github.com/cosmos/gogoproto/proto"
	anypb "github.com/cosmos/gogoproto/types/any"
)

// ed25519PubKeyTypeURL is both the type URL stamped on the Any we build and the
// value required in the `@type` field when a transaction key arrives as JSON.
// They are the same string, and must stay so: accepting a document that names
// one key type while emitting an Any that claims another is exactly the
// confusion txPubKeyAny exists to prevent.
const ed25519PubKeyTypeURL = "/cosmos.crypto.ed25519.PubKey"

// pubKeyAny accepts a bare base64 Ed25519 key and nothing else.
//
// This is the STRICT form, and genesis authoring depends on it staying strict.
// `coreslot-genesis add` decodes the key here to build the CoreSlot record, then
// writes the caller's original argument verbatim into the CometBFT validator
// entry. Those two are the same document, so any input this accepts but does not
// round-trip byte-for-byte would produce a genesis whose two halves disagree —
// written, exit 0, and only caught later by `coreslot-genesis validate`.
//
// Widening it is therefore not a local change. An accepted-input form must be
// added to the writer at the same time, or not at all. Transaction paths, which
// have no such coupling, use txPubKeyAny instead.
func pubKeyAny(value string) (*anypb.Any, error) {
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("decode consensus key: %w", err)
	}
	// The input must be the CANONICAL encoding of the bytes it decodes to, not
	// merely a decodable spelling of them.
	//
	// Go's base64 decoder ignores \r and \n (though not spaces), so a key with a
	// trailing newline — the shape a key read from a file or piped through a shell
	// routinely has — decodes to the right 32 bytes. Genesis then writes the
	// caller's ORIGINAL string into the CometBFT validator entry while the CoreSlot
	// record holds the decoded key, and the two halves of one document describe
	// different keys. The command exits 0 and `coreslot-genesis validate` refuses
	// what it just produced.
	//
	// Re-encoding and requiring equality is the whole rule: this helper may accept
	// only what the writer can store back verbatim.
	if canonical := base64.StdEncoding.EncodeToString(raw); canonical != value {
		return nil, fmt.Errorf(
			"consensus key must be canonical base64 with no surrounding or embedded whitespace")
	}
	return ed25519AnyFromBytes(raw)
}

// txPubKeyAny accepts either form an operator has a consensus key in:
//
//	qazgp3Js…                                                     (bare base64)
//	{"@type":"/cosmos.crypto.ed25519.PubKey","key":"qazgp3Js…"}   (show-validator)
//
// `twilightd tendermint show-validator` emits the second, and the transaction
// commands used to accept only the first, so onboarding required hand-extracting
// the `key` field. Both forms produce a byte-identical Any.
//
// Transaction-scoped deliberately. A transaction carries only the decoded key,
// so no other representation of the input survives to disagree with it — the
// coupling that keeps genesis on the strict helper does not exist here.
func txPubKeyAny(value string) (*anypb.Any, error) {
	raw, err := decodeTxConsensusKey(value)
	if err != nil {
		return nil, err
	}
	return ed25519AnyFromBytes(raw)
}

// ed25519AnyFromBytes applies the length check and builds the Any, so both
// helpers agree on what a valid key is and on the bytes they emit for it.
func ed25519AnyFromBytes(raw []byte) (*anypb.Any, error) {
	if len(raw) != sdked25519.PubKeySize {
		return nil, fmt.Errorf("consensus key must be %d bytes", sdked25519.PubKeySize)
	}
	bz, err := gogoproto.Marshal(&sdked25519.PubKey{Key: raw})
	if err != nil {
		return nil, err
	}
	return &anypb.Any{TypeUrl: ed25519PubKeyTypeURL, Value: bz}, nil
}

// decodeTxConsensusKey returns the raw key bytes from either transaction form.
//
// Form detection is by leading brace, which is unambiguous: `{` is not in the
// base64 alphabet, so no valid bare key can be mistaken for JSON and no JSON
// document can be mistaken for a bare key.
//
// The JSON path is strict about the contract it needs — an exact `@type` and a
// non-empty `key` — and indifferent to everything else. Unknown fields are
// ignored rather than rejected: `@type` is what actually establishes the key is
// the kind we are about to claim it is, so failing on an added upstream metadata
// field would break operator onboarding to enforce nothing.
func decodeTxConsensusKey(value string) ([]byte, error) {
	trimmed := strings.TrimSpace(value)
	if !strings.HasPrefix(trimmed, "{") {
		raw, err := base64.StdEncoding.DecodeString(trimmed)
		if err != nil {
			return nil, fmt.Errorf("decode consensus key: %w", err)
		}
		return raw, nil
	}

	var doc struct {
		Type string `json:"@type"`
		Key  string `json:"key"`
	}
	if err := json.Unmarshal([]byte(trimmed), &doc); err != nil {
		return nil, fmt.Errorf("parse consensus key JSON: %w", err)
	}
	if doc.Type != ed25519PubKeyTypeURL {
		return nil, fmt.Errorf("consensus key @type must be %q, got %q", ed25519PubKeyTypeURL, doc.Type)
	}
	if doc.Key == "" {
		return nil, fmt.Errorf("consensus key JSON has no %q value", "key")
	}
	raw, err := base64.StdEncoding.DecodeString(doc.Key)
	if err != nil {
		return nil, fmt.Errorf("decode consensus key: %w", err)
	}
	return raw, nil
}
