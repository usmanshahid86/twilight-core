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
// # Decoding discipline
//
// A loader accepts exactly one well-formed JSON document with unambiguous object
// structure. Four checks enforce that, and each closes a way a pack could appear
// to load while meaning something other than it says:
//
//   - UNKNOWN FIELDS are rejected, so a future revision cannot add a section that
//     the harness then silently fails to execute.
//   - TRAILING DATA is rejected. Go's decoder reads one value and stops, so a
//     file holding a valid pack followed by a second document would load as if
//     the second were not there.
//   - DUPLICATE OBJECT KEYS are rejected, recursively. Go's decoder takes the
//     last value for a repeated key, so `"revision": 999, "revision": 1` would
//     satisfy a revision check while the file plainly says otherwise.
//   - MANDATORY STRUCTURE is validated. An absent field decodes to a Go zero
//     value, so a normative declaration or an expected output could disappear and
//     leave a passing test asserting against "".
//
// Malformed structure and metadata mismatch stay distinguishable, because they
// call for different responses: a corrupted file versus the wrong file.
package consensusvectors

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
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
	// ErrMalformedPack reports a pack whose bytes are not valid JSON, whose JSON
	// does not fit the expected structure, whose object keys repeat, which carries
	// more than one document, or which is missing a mandatory declaration.
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
//
// Presence is tracked separately from value. Zero is a legitimate protocol value
// for a Slot ID, a height and a block count, so an absent field and a field that
// says "0" are indistinguishable by value alone; only IsSet separates them.
type U64 struct {
	value uint64
	set   bool
}

// Uint64 returns the underlying value. An absent field reads as zero; use IsSet
// where the difference matters.
func (v U64) Uint64() uint64 { return v.value }

// IsSet reports whether the field was present in the pack.
func (v U64) IsSet() bool { return v.set }

// UnmarshalJSON decodes a quoted decimal string into a uint64.
func (v *U64) UnmarshalJSON(data []byte) error {
	if len(data) < 2 || data[0] != '"' || data[len(data)-1] != '"' {
		return fmt.Errorf("%w: integer %s is not a decimal string", ErrMalformedPack, data)
	}
	parsed, err := strconv.ParseUint(string(data[1:len(data)-1]), 10, 64)
	if err != nil {
		return fmt.Errorf("%w: integer %s: %v", ErrMalformedPack, data, err)
	}
	v.value = parsed
	v.set = true
	return nil
}

// Bool is a JSON boolean whose presence is tracked.
//
// A plain bool cannot separate "declared false" from "not declared at all", and
// several required declarations in the packs are canonically false. Without this
// type, deleting one of them would decode to the same value it is supposed to
// have and the deletion would be invisible.
type Bool struct {
	value bool
	set   bool
}

// Bool returns the underlying value.
func (v Bool) Bool() bool { return v.value }

// IsSet reports whether the field was present in the pack.
func (v Bool) IsSet() bool { return v.set }

// UnmarshalJSON decodes a JSON boolean. A JSON null is not a boolean and is
// rejected rather than silently leaving the value false.
func (v *Bool) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return fmt.Errorf("%w: boolean field is null", ErrMalformedPack)
	}
	var parsed bool
	if err := json.Unmarshal(data, &parsed); err != nil {
		return fmt.Errorf("%w: %s is not a JSON boolean", ErrMalformedPack, data)
	}
	v.value = parsed
	v.set = true
	return nil
}

// Int is a bare JSON number whose presence is tracked.
//
// The packs carry protocol integers as decimal strings and use bare numbers only
// for a few counts, so this type is deliberately narrow. Zero is a legitimate
// value for every one of them, which is why presence has to be tracked
// separately.
type Int struct {
	value int
	set   bool
}

// Value returns the underlying value.
func (v Int) Value() int { return v.value }

// IsSet reports whether the field was present in the pack.
func (v Int) IsSet() bool { return v.set }

// UnmarshalJSON decodes a bare JSON number.
func (v *Int) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		return fmt.Errorf("%w: integer field is null", ErrMalformedPack)
	}
	var parsed int
	if err := json.Unmarshal(data, &parsed); err != nil {
		return fmt.Errorf("%w: %s is not a JSON integer", ErrMalformedPack, data)
	}
	v.value = parsed
	v.set = true
	return nil
}

// NullableU64 is a uint64 field that the packs may legitimately state as null,
// and which distinguishes three states: absent, explicitly null, and a value.
//
// A *U64 collapses the first two, because encoding/json leaves a pointer nil for
// both an absent member and an explicit null without consulting the field at
// all. That matters where null is itself normative: the winner-count vectors use
// it to record that the rate arithmetic was never evaluated, so an absent member
// would masquerade as that statement.
//
// Declared as a value type rather than a pointer on purpose. encoding/json calls
// UnmarshalJSON on a value that implements Unmarshaler even when the input is
// null, which is exactly the hook needed to observe an explicit null.
type NullableU64 struct {
	value uint64
	set   bool
	null  bool
}

// IsSet reports whether the member was present at all.
func (v NullableU64) IsSet() bool { return v.set }

// IsNull reports whether the member was present and explicitly null.
func (v NullableU64) IsNull() bool { return v.null }

// Uint64 returns the value. It is zero when the member is null or absent; use
// IsSet and IsNull where the difference matters.
func (v NullableU64) Uint64() uint64 { return v.value }

// UnmarshalJSON records presence, then nullness, then the value.
func (v *NullableU64) UnmarshalJSON(data []byte) error {
	v.set = true
	if string(data) == "null" {
		v.null = true
		return nil
	}
	var inner U64
	if err := inner.UnmarshalJSON(data); err != nil {
		return err
	}
	v.value = inner.Uint64()
	return nil
}

// decodePack decodes pack bytes into dst under the full decoding discipline
// described on the package.
func decodePack(filename string, data []byte, dst any) error {
	if err := rejectDuplicateKeys(data); err != nil {
		return fmt.Errorf("%w: %s: %v", ErrMalformedPack, filename, err)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return fmt.Errorf("%w: %s: %v", ErrMalformedPack, filename, err)
	}

	// One document only. Token returns io.EOF when nothing but whitespace remains.
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf(
			"%w: %s: trailing data after the root JSON document", ErrMalformedPack, filename,
		)
	}
	return nil
}

// rejectDuplicateKeys walks the token stream and fails on any repeated member
// name, at any depth.
//
// encoding/json silently keeps the last value for a repeated key, so without
// this a pack could declare one revision and validate as another. The walk uses
// the standard library only; a set is consulted for membership and never
// iterated.
func rejectDuplicateKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	return checkJSONValue(decoder, "$")
}

func checkJSONValue(decoder *json.Decoder, path string) error {
	token, err := decoder.Token()
	if err != nil {
		// Malformed JSON is reported by the typed decode that follows; this pass
		// only looks for duplicate keys and yields on anything else.
		return nil //nolint:nilerr // structural errors are surfaced by decodePack
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil // scalar
	}
	switch delim {
	case '{':
		return checkJSONObject(decoder, path)
	case '[':
		return checkJSONArray(decoder, path)
	default:
		return nil
	}
}

func checkJSONObject(decoder *json.Decoder, path string) error {
	seen := make(map[string]struct{})
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil //nolint:nilerr // structural errors are surfaced by decodePack
		}
		key, ok := token.(string)
		if !ok {
			return nil
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("duplicate object key %q at %s", key, path)
		}
		seen[key] = struct{}{}
		if err := checkJSONValue(decoder, path+"."+key); err != nil {
			return err
		}
	}
	_, _ = decoder.Token() // closing brace
	return nil
}

func checkJSONArray(decoder *json.Decoder, path string) error {
	for index := 0; decoder.More(); index++ {
		if err := checkJSONValue(decoder, path+"["+strconv.Itoa(index)+"]"); err != nil {
			return err
		}
	}
	_, _ = decoder.Token() // closing bracket
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

// ---------------------------------------------------------------------------
// Mandatory-structure helpers
//
// These validate the schema each frozen pack actually uses. They deliberately do
// not require every modeled field: several are variant-specific, and demanding
// the union of every field would reject a pack that is exactly as its
// specification intends.
// ---------------------------------------------------------------------------

// structureError reports a missing or malformed mandatory element.
func structureError(filename, format string, args ...any) error {
	return fmt.Errorf("%w: %s: %s", ErrMalformedPack, filename, fmt.Sprintf(format, args...))
}

// requireText fails when a mandatory string declaration is absent or blank.
func requireText(filename, field, value string) error {
	if strings.TrimSpace(value) == "" {
		return structureError(filename, "%s is missing or empty", field)
	}
	return nil
}

// requireSet fails when a mandatory numeric field was absent from the pack. The
// distinction matters because zero is a legitimate value for most of them.
func requireSet(filename, field string, value U64) error {
	if !value.IsSet() {
		return structureError(filename, "%s is missing", field)
	}
	return nil
}

// requireBoolSet fails when a mandatory boolean declaration was absent. It does
// NOT constrain the value: several of these are canonically false, and requiring
// true would reject the pack as written.
func requireBoolSet(filename, field string, value Bool) error {
	if !value.IsSet() {
		return structureError(filename, "%s is missing", field)
	}
	return nil
}

// requireIntSet fails when a mandatory bare-number count was absent.
func requireIntSet(filename, field string, value Int) error {
	if !value.IsSet() {
		return structureError(filename, "%s is missing", field)
	}
	return nil
}

// requireMetadataPresence checks that a pack declares its own identity fields at
// all, before their values are compared. Without it an absent version reports as
// "declares version 0", which describes a file that says something it does not.
func requireMetadataPresence(filename string, version, revision Int, normative Bool) error {
	return firstError(
		requireIntSet(filename, "version", version),
		requireIntSet(filename, "revision", revision),
		requireBoolSet(filename, "normative", normative),
	)
}

// requireHex32 fails when a mandatory 32-byte hexadecimal value is absent or is
// not in the pack's lowercase 64-character transport form.
func requireHex32(filename, field, value string) error {
	if value == "" {
		return structureError(filename, "%s is missing", field)
	}
	if len(value) != 64 {
		return structureError(filename, "%s is %d characters, want 64", field, len(value))
	}
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return structureError(filename, "%s is not lowercase hexadecimal", field)
		}
	}
	return nil
}

// requireAmount fails when a mandatory base-denomination amount is absent or is
// not a non-negative decimal integer. Amounts are arbitrary precision, so they
// are checked as digits rather than parsed into a fixed-width type.
func requireAmount(filename, field, value string) error {
	if value == "" {
		return structureError(filename, "%s is missing", field)
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return structureError(filename, "%s (%q) is not a non-negative decimal integer", field, value)
		}
	}
	return nil
}

// requireNonEmptySlice fails when a mandatory collection is absent or empty.
func requireNonEmptySlice(filename, field string, length int) error {
	if length == 0 {
		return structureError(filename, "%s is missing or empty", field)
	}
	return nil
}

// firstError returns the first non-nil error, keeping validators flat and their
// failure order deterministic.
func firstError(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
