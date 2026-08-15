// Package consensusvectors loads the normative Twilight protocol vector packs
// that are tracked in this repository as compatibility fixtures.
//
// The packs are the executable form of frozen protocol behavior. They are
// committed verbatim under testdata/ and are never edited, reformatted or
// normalized: a byte change to a pack is a protocol change, and reviewing one as
// a diff against the published artifact is the point of tracking them.
//
// # Selection discipline
//
// Each pack is bound to a named variable by its exact filename through a single
// //go:embed directive. There is no glob, no directory walk and no "newest
// matching revision" behavior, so no pack can be silently substituted and no
// superseded artifact can be picked up by accident. In particular the
// unversioned twilight-tokendrop-draw-v1-golden-vectors.json is superseded by
// the r2 pack, is deliberately not committed here, and cannot be loaded.
//
// # Metadata discipline
//
// Every loader asserts the pack's declared artifact name, version and revision
// before returning it, and rejects a pack that does not declare itself
// normative. Decoding rejects unknown fields, so a future revision that adds a
// section cannot be silently ignored by tests that would then certify less than
// they appear to. Malformed JSON and metadata mismatch are distinguishable
// errors, because they call for different responses: one is a corrupted file,
// the other is the wrong file.
package consensusvectors

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
)

// Tracked pack filenames. These names are the identity of each artifact and are
// used both by the embed directives and by the deferred-fixture manifest.
const (
	DrawPackFilename            = "twilight-tokendrop-draw-v1-golden-vectors-r2.json"
	SelectedDrawIDsPackFilename = "twilight-selected-draw-ids-hash-v1-vectors-r1.json"
	RewardPackFilename          = "twilight-reward-consensus-vectors-v1-r1.json"

	// SupersededDrawPackFilename names the unversioned draw pack that r2 replaces.
	// It is recorded here so a test can assert it is absent rather than merely
	// unused, and it must never be embedded or loaded.
	SupersededDrawPackFilename = "twilight-tokendrop-draw-v1-golden-vectors.json"
)

var (
	// ErrMalformedPack reports a pack whose bytes are not valid JSON, or whose
	// JSON does not fit the expected structure.
	ErrMalformedPack = errors.New("consensusvectors: malformed vector pack")

	// ErrPackMetadataMismatch reports a pack whose declared artifact, version or
	// revision is not the one this code was written against.
	ErrPackMetadataMismatch = errors.New("consensusvectors: vector pack metadata mismatch")
)

// U64 is a uint64 carried as a JSON decimal string.
//
// The packs encode every protocol integer as a string rather than as a JSON
// number, because values up to 2^64-1 do not survive a float64 round trip. This
// type enforces that convention: a bare JSON number is rejected rather than
// quietly accepted at reduced precision.
type U64 uint64

// Uint64 returns the underlying value.
func (v U64) Uint64() uint64 { return uint64(v) }

// UnmarshalJSON decodes a quoted decimal string into a uint64.
func (v *U64) UnmarshalJSON(data []byte) error {
	if len(data) < 2 || data[0] != '"' || data[len(data)-1] != '"' {
		return fmt.Errorf("%w: integer %s is not a decimal string", ErrMalformedPack, data)
	}
	parsed, err := strconv.ParseUint(string(data[1:len(data)-1]), 10, 64)
	if err != nil {
		return fmt.Errorf("%w: integer %s: %v", ErrMalformedPack, data, err)
	}
	*v = U64(parsed)
	return nil
}

// decodePack decodes pack bytes into dst, rejecting unknown fields so a section
// added by a future revision cannot pass unnoticed.
func decodePack(filename string, data []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("%w: %s: %v", ErrMalformedPack, filename, err)
	}
	return nil
}

// assertMetadata verifies a pack's self-declared identity. The artifact name is
// carried under different keys by different packs, so the caller passes the
// value it read rather than this function guessing at the key.
func assertMetadata(filename, gotArtifact, wantArtifact string, gotVersion, wantVersion, gotRevision, wantRevision int, normative bool) error {
	if gotArtifact != wantArtifact {
		return fmt.Errorf(
			"%w: %s declares artifact %q, want %q",
			ErrPackMetadataMismatch, filename, gotArtifact, wantArtifact,
		)
	}
	if gotVersion != wantVersion {
		return fmt.Errorf(
			"%w: %s declares version %d, want %d",
			ErrPackMetadataMismatch, filename, gotVersion, wantVersion,
		)
	}
	if gotRevision != wantRevision {
		return fmt.Errorf(
			"%w: %s declares revision %d, want %d",
			ErrPackMetadataMismatch, filename, gotRevision, wantRevision,
		)
	}
	if !normative {
		return fmt.Errorf(
			"%w: %s does not declare itself normative",
			ErrPackMetadataMismatch, filename,
		)
	}
	return nil
}
