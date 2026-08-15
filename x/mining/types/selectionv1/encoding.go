package selectionv1

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
)

// Domain tags. Exact ASCII byte strings with no trailing NUL (r6 §37).
const (
	DomainDrawID          = "TWILIGHT/TOKENDROP/DRAW-ID/V1"
	DomainCandidateSet    = "TWILIGHT/TOKENDROP/CANDIDATE-SET/V1"
	DomainBeacon          = "TWILIGHT/TOKENDROP/BEACON/V1"
	DomainTicket          = "TWILIGHT/TOKENDROP/TICKET/V1"
	DomainSelectedDrawIDs = "TWILIGHT/PARTICIPANT-SELECTION/SELECTED-DRAW-IDS/V1"
)

// HashSize is the byte length of every V1 digest, of a draw ID, and of a
// CometBFT BlockID.Hash.
const HashSize = 32

// Hash is a 32-byte V1 digest or block hash.
type Hash [HashSize]byte

// DrawID is a 32-byte participant draw identifier. It is a distinct type from
// Hash so a digest can never be passed where a draw ID belongs.
type DrawID [HashSize]byte

// String returns the lowercase hexadecimal transport form. Presentation only:
// hexadecimal text never enters a V1 hash preimage.
func (h Hash) String() string { return hex.EncodeToString(h[:]) }

// String returns the lowercase hexadecimal transport form. Presentation only:
// hexadecimal text never enters a V1 hash preimage.
func (d DrawID) String() string { return hex.EncodeToString(d[:]) }

// HashFromBytes converts exactly HashSize raw bytes to a Hash.
func HashFromBytes(b []byte) (Hash, error) {
	var h Hash
	if len(b) != HashSize {
		return Hash{}, fmt.Errorf("%w: hash is %d bytes, want %d", ErrInvalidLength, len(b), HashSize)
	}
	copy(h[:], b)
	return h, nil
}

// DrawIDFromBytes converts exactly HashSize raw bytes to a DrawID. A draw ID of
// any other length is rejected rather than padded or truncated.
func DrawIDFromBytes(b []byte) (DrawID, error) {
	var d DrawID
	if len(b) != HashSize {
		return DrawID{}, fmt.Errorf("%w: draw id is %d bytes, want %d", ErrInvalidLength, len(b), HashSize)
	}
	copy(d[:], b)
	return d, nil
}

// HashFromHex decodes the 64-character lowercase hexadecimal transport form.
func HashFromHex(s string) (Hash, error) {
	b, err := decodeHex32(s)
	if err != nil {
		return Hash{}, err
	}
	return HashFromBytes(b)
}

// DrawIDFromHex decodes the 64-character lowercase hexadecimal transport form.
func DrawIDFromHex(s string) (DrawID, error) {
	b, err := decodeHex32(s)
	if err != nil {
		return DrawID{}, err
	}
	return DrawIDFromBytes(b)
}

// decodeHex32 implements r6 §36's HEX32: a 64-character LOWERCASE hexadecimal
// value decoded to exactly 32 raw bytes. Uppercase is rejected rather than
// accepted leniently, because two spellings of one value would give the same
// bytes two transport forms and break canonical comparison of transport data.
func decodeHex32(s string) ([]byte, error) {
	if len(s) != 2*HashSize {
		return nil, fmt.Errorf("%w: hex value is %d characters, want %d", ErrInvalidLength, len(s), 2*HashSize)
	}
	if s != strings.ToLower(s) {
		return nil, fmt.Errorf("%w: hex value must be lowercase", ErrNotCanonical)
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedHex, err)
	}
	return b, nil
}

// SelectionContext is the (chain, Slot, target epoch) triple that scopes every
// V1 hash. Including it in each preimage is what stops a digest computed for one
// Selection from being replayed as another.
type SelectionContext struct {
	ChainID     string
	SlotID      uint64
	TargetEpoch uint64
}

// appendTo appends the context fragment shared by all five frozen primitives:
//
//	ChainIDEncoding(chain_id) || U64BE(slot_id) || U64BE(target_epoch)
//
// where ChainIDEncoding(chain_id) = U16BE(len(UTF8(chain_id))) || UTF8(chain_id)
// (r6 §36). Every primitive places this fragment immediately after its domain
// tag, so the three lines are assembled once here and exercised by all five
// golden-vector sets.
func (c SelectionContext) appendTo(buf []byte) ([]byte, error) {
	name := []byte(c.ChainID)
	if len(name) > math.MaxUint16 {
		return nil, fmt.Errorf(
			"%w: chain id is %d UTF-8 bytes, exceeds %d",
			ErrChainIDTooLong, len(name), math.MaxUint16,
		)
	}
	buf = binary.BigEndian.AppendUint16(buf, uint16(len(name)))
	buf = append(buf, name...)
	buf = binary.BigEndian.AppendUint64(buf, c.SlotID)
	buf = binary.BigEndian.AppendUint64(buf, c.TargetEpoch)
	return buf, nil
}

// Equal reports whether two contexts address the same Selection.
func (c SelectionContext) Equal(other SelectionContext) bool {
	return c.ChainID == other.ChainID &&
		c.SlotID == other.SlotID &&
		c.TargetEpoch == other.TargetEpoch
}

// String renders the context for error messages.
func (c SelectionContext) String() string {
	return fmt.Sprintf("(chain_id=%s, slot_id=%d, target_epoch=%d)", c.ChainID, c.SlotID, c.TargetEpoch)
}
