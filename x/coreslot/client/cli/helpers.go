package cli

import (
	"encoding/base64"
	"fmt"

	sdked25519 "github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	gogoproto "github.com/cosmos/gogoproto/proto"
	anypb "github.com/cosmos/gogoproto/types/any"
)

func pubKeyAny(value string) (*anypb.Any, error) {
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("decode consensus key: %w", err)
	}
	if len(raw) != sdked25519.PubKeySize {
		return nil, fmt.Errorf("consensus key must be %d bytes", sdked25519.PubKeySize)
	}
	bz, err := gogoproto.Marshal(&sdked25519.PubKey{Key: raw})
	if err != nil {
		return nil, err
	}
	return &anypb.Any{TypeUrl: "/cosmos.crypto.ed25519.PubKey", Value: bz}, nil
}
