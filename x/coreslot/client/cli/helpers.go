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
// value required in the `@type` field when the key arrives as JSON. They are the
// same string, and must stay so: accepting a JSON document that names one key
// type while emitting an Any that claims another is exactly the confusion this
// helper exists to prevent.
const ed25519PubKeyTypeURL = "/cosmos.crypto.ed25519.PubKey"

// pubKeyAny turns an operator-supplied consensus key into an Any.
//
// Two input forms are accepted, because operators legitimately have two:
//
//	qazgp3Js…                                                     (bare base64)
//	{"@type":"/cosmos.crypto.ed25519.PubKey","key":"qazgp3Js…"}   (show-validator)
//
// `twilightd tendermint show-validator` emits the second, and this command used
// to accept only the first, so onboarding required hand-extracting the `key`
// field. Both forms now produce a byte-identical Any.
func pubKeyAny(value string) (*anypb.Any, error) {
	raw, err := decodeConsensusKey(value)
	if err != nil {
		return nil, err
	}
	if len(raw) != sdked25519.PubKeySize {
		return nil, fmt.Errorf("consensus key must be %d bytes", sdked25519.PubKeySize)
	}
	bz, err := gogoproto.Marshal(&sdked25519.PubKey{Key: raw})
	if err != nil {
		return nil, err
	}
	return &anypb.Any{TypeUrl: ed25519PubKeyTypeURL, Value: bz}, nil
}

// decodeConsensusKey returns the raw key bytes from either accepted form.
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
func decodeConsensusKey(value string) ([]byte, error) {
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
