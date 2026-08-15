package consensusvectors

// Decoding-strictness regressions.
//
// Each case is a way a pack could load successfully while meaning something
// other than what its bytes say. A loader that accepts any of them certifies
// less than a passing test appears to claim, so each is exercised against
// mutated copies of the real embedded packs rather than against synthetic
// fixtures that might not share the real schema.
//
// The tracked pack files are never written to; every mutation happens in memory.

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// mutateJSON decodes raw into a generic document, applies fn, and re-encodes.
// Re-encoding changes key order and whitespace, neither of which any loader rule
// depends on.
func mutateJSON(t *testing.T, raw []byte, fn func(doc map[string]any)) []byte {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal pack: %v", err)
	}
	fn(doc)
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal mutated pack: %v", err)
	}
	return out
}

// firstElem returns the first element of a JSON array member as an object.
func firstElem(t *testing.T, doc map[string]any, key string) map[string]any {
	t.Helper()
	list, ok := doc[key].([]any)
	if !ok || len(list) == 0 {
		t.Fatalf("%s is not a non-empty array", key)
	}
	elem, ok := list[0].(map[string]any)
	if !ok {
		t.Fatalf("%s[0] is not an object", key)
	}
	return elem
}

func loadDrawFrom(data []byte) error {
	var pack DrawPack
	if err := decodePack(DrawPackFilename, data, &pack); err != nil {
		return err
	}
	return pack.validate(DrawPackFilename)
}

func loadSelectedFrom(data []byte) error {
	var pack SelectedDrawIDsPack
	if err := decodePack(SelectedDrawIDsPackFilename, data, &pack); err != nil {
		return err
	}
	return pack.validate(SelectedDrawIDsPackFilename)
}

func loadRewardFrom(data []byte) error {
	var pack RewardPack
	if err := decodePack(RewardPackFilename, data, &pack); err != nil {
		return err
	}
	return pack.validate(RewardPackFilename)
}

func requireMalformed(t *testing.T, err error, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s was accepted", what)
	}
	if !errors.Is(err, ErrMalformedPack) {
		t.Fatalf("%s: err = %v, want ErrMalformedPack", what, err)
	}
}

// --- A. trailing documents ----------------------------------------------------

// TestDecodePackRejectsTrailingDocument covers a file holding a valid pack
// followed by a second JSON value. Go's decoder reads one value and stops, so
// without this check the second document would load as if it were not there.
func TestDecodePackRejectsTrailingDocument(t *testing.T) {
	cases := []struct {
		name    string
		trailer string
	}{
		{"empty object", "\n{}"},
		{"second array", "\n[1,2,3]"},
		{"bare scalar", "\n42"},
		{"second full document", "\n" + string(rewardPackBytes)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := append(append([]byte{}, rewardPackBytes...), []byte(tc.trailer)...)
			requireMalformed(t, loadRewardFrom(data), "trailing "+tc.name)
		})
	}

	// Trailing whitespace is not trailing data.
	data := append(append([]byte{}, rewardPackBytes...), []byte("\n\t \r\n")...)
	if err := loadRewardFrom(data); err != nil {
		t.Fatalf("trailing whitespace rejected: %v", err)
	}
}

// --- B. duplicate object keys -------------------------------------------------

// TestDecodePackRejectsDuplicateRootKey covers the case the review named:
// encoding/json keeps the LAST value for a repeated key, so a pack declaring
// revision 999 and then revision 1 would satisfy a revision check while plainly
// saying otherwise.
func TestDecodePackRejectsDuplicateRootKey(t *testing.T) {
	raw := string(rewardPackBytes)
	opening := strings.Index(raw, "{")
	if opening < 0 {
		t.Fatal("pack has no opening brace")
	}
	injected := raw[:opening+1] + `"revision": 999,` + raw[opening+1:]

	// Confirm the mutation really is the dangerous one: a permissive decoder would
	// take the later, correct-looking value and validate happily.
	var permissive RewardPack
	if err := json.Unmarshal([]byte(injected), &permissive); err != nil {
		t.Fatalf("mutated pack is not valid JSON: %v", err)
	}
	if permissive.Revision != rewardPackRevision {
		t.Fatalf("last-value-wins gave revision %d, want %d; the test no longer covers the hazard",
			permissive.Revision, rewardPackRevision)
	}

	requireMalformed(t, loadRewardFrom([]byte(injected)), "duplicate root key")
}

// TestDecodePackRejectsDuplicateNestedKey confirms the check recurses. A rule
// applied only to the root object would leave every vector body unprotected.
func TestDecodePackRejectsDuplicateNestedKey(t *testing.T) {
	t.Run("nested object", func(t *testing.T) {
		raw := string(drawPackBytes)
		const anchor = `"encoding_notes": {`
		index := strings.Index(raw, anchor)
		if index < 0 {
			t.Fatalf("anchor %q not found", anchor)
		}
		injected := raw[:index+len(anchor)] + `"integers": "shadowed",` + raw[index+len(anchor):]
		requireMalformed(t, loadDrawFrom([]byte(injected)), "duplicate nested key")
	})

	t.Run("object inside an array", func(t *testing.T) {
		raw := string(selectedDrawIDsPackBytes)
		const anchor = `"vectors": [`
		index := strings.Index(raw, anchor)
		if index < 0 {
			t.Fatalf("anchor %q not found", anchor)
		}
		// Open the first array element and shadow its name.
		brace := strings.Index(raw[index:], "{")
		if brace < 0 {
			t.Fatal("no object inside vectors")
		}
		at := index + brace + 1
		injected := raw[:at] + `"name": "shadowed",` + raw[at:]
		requireMalformed(t, loadSelectedFrom([]byte(injected)), "duplicate key inside an array element")
	})
}

// --- C. mandatory structure ---------------------------------------------------

func TestSelectedPackRequiresNormativeDeclarations(t *testing.T) {
	for _, key := range []string{"domain", "encoding"} {
		t.Run("missing "+key, func(t *testing.T) {
			data := mutateJSON(t, selectedDrawIDsPackBytes, func(doc map[string]any) {
				delete(doc, key)
			})
			requireMalformed(t, loadSelectedFrom(data), "selected pack without "+key)
		})
	}
}

func TestRewardPackRequiresNormativeDeclarations(t *testing.T) {
	for _, key := range []string{"emission_reference", "per_block_subsidies_semantics"} {
		t.Run("missing "+key, func(t *testing.T) {
			data := mutateJSON(t, rewardPackBytes, func(doc map[string]any) {
				delete(doc, key)
			})
			requireMalformed(t, loadRewardFrom(data), "reward pack without "+key)
		})
	}
}

// TestMissingVectorOutputIsRejected covers an expected output disappearing. Left
// unchecked it would decode to "" and a conformance test would assert against an
// empty expectation.
func TestMissingVectorOutputIsRejected(t *testing.T) {
	data := mutateJSON(t, selectedDrawIDsPackBytes, func(doc map[string]any) {
		delete(firstElem(t, doc, "vectors"), "expected_hash")
	})
	requireMalformed(t, loadSelectedFrom(data), "selected vector without expected_hash")
}

// TestMissingVectorInputIsRejected covers a required input disappearing, in both
// the string and the numeric case. The numeric case is the reason U64 tracks
// presence: zero is a legitimate value for a Slot ID, a height and a block count,
// so absence cannot be inferred from the value alone.
func TestMissingVectorInputIsRejected(t *testing.T) {
	t.Run("string input", func(t *testing.T) {
		data := mutateJSON(t, rewardPackBytes, func(doc map[string]any) {
			delete(firstElem(t, doc, "emission_vectors"), "max_supply")
		})
		requireMalformed(t, loadRewardFrom(data), "emission vector without max_supply")
	})

	t.Run("numeric input", func(t *testing.T) {
		data := mutateJSON(t, rewardPackBytes, func(doc map[string]any) {
			delete(firstElem(t, doc, "emission_vectors"), "reward_enabled_blocks")
		})
		requireMalformed(t, loadRewardFrom(data), "emission vector without reward_enabled_blocks")
	})

	t.Run("numeric input whose absent value would read as a legal zero", func(t *testing.T) {
		data := mutateJSON(t, selectedDrawIDsPackBytes, func(doc map[string]any) {
			delete(firstElem(t, doc, "vectors"), "slot_id")
		})
		requireMalformed(t, loadSelectedFrom(data), "selected vector without slot_id")
	})
}

// TestEmptyCollectionsAreDistinguishedFromAbsent guards the other direction: the
// zero-candidate and fully-paused cases legitimately carry empty lists, and a
// validator that demanded non-emptiness everywhere would reject the pack exactly
// as its specification intends it.
func TestEmptyCollectionsAreDistinguishedFromAbsent(t *testing.T) {
	t.Run("empty per-block schedule is accepted", func(t *testing.T) {
		if err := loadRewardFrom(rewardPackBytes); err != nil {
			t.Fatalf("real reward pack rejected: %v", err)
		}
	})
	t.Run("absent per-block schedule is rejected", func(t *testing.T) {
		data := mutateJSON(t, rewardPackBytes, func(doc map[string]any) {
			delete(firstElem(t, doc, "emission_vectors"), "per_block_subsidies")
		})
		requireMalformed(t, loadRewardFrom(data), "emission vector without per_block_subsidies")
	})
	t.Run("absent selected list is rejected", func(t *testing.T) {
		data := mutateJSON(t, selectedDrawIDsPackBytes, func(doc map[string]any) {
			delete(firstElem(t, doc, "vectors"), "selected_draw_ids")
		})
		requireMalformed(t, loadSelectedFrom(data), "selected vector without selected_draw_ids")
	})
}

// TestNegativeDiscriminatorShapesAreVariantSpecific confirms the validator checks
// the shape each case actually uses rather than the union of both. The two
// discriminators describe different forbidden computations and populate
// different fields; demanding all of them would reject the real pack.
func TestNegativeDiscriminatorShapesAreVariantSpecific(t *testing.T) {
	pack, err := LoadRewardPack()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(pack.NegativeDiscriminators) != ExpectedNegativeDiscriminators {
		t.Fatalf("discriminators = %d, want %d", len(pack.NegativeDiscriminators), ExpectedNegativeDiscriminators)
	}

	// Each real case validates under its own shape, and the two shapes differ.
	var overAllocation, roundingOrder int
	for i, d := range pack.NegativeDiscriminators {
		if err := d.validate(RewardPackFilename, "case"); err != nil {
			t.Fatalf("real discriminator %d rejected: %v", i, err)
		}
		if len(d.IncorrectEntitlements) > 0 {
			overAllocation++
		}
		if len(d.IncorrectFloorPoolThenMultip) > 0 {
			roundingOrder++
		}
	}
	if overAllocation != 1 || roundingOrder != 1 {
		t.Errorf("expected one case of each shape, got over-allocation=%d rounding-order=%d",
			overAllocation, roundingOrder)
	}

	// A case matching neither shape is malformed rather than silently ignored.
	neither := NegativeDiscriminator{
		Name:           "neither",
		RequiredResult: "something",
		Pool:           "1000",
		BlocksActive:   pack.NegativeDiscriminators[0].BlocksActive,
	}
	requireMalformed(t, neither.validate(RewardPackFilename, "case"), "discriminator matching no shape")

	// So is one claiming both.
	both := pack.NegativeDiscriminators[0]
	both.IncorrectFloorPoolThenMultip = []string{"1"}
	requireMalformed(t, both.validate(RewardPackFilename, "case"), "discriminator matching both shapes")
}

// TestDrawPackVariantSectionsAreNotOverconstrained pins the same restraint for
// the draw pack: a timing vector states only the fields its own rule needs, and
// a negative vector deliberately carries a draw ID that is not 32 bytes.
func TestDrawPackVariantSectionsAreNotOverconstrained(t *testing.T) {
	pack, err := LoadDrawPack()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	var sparseTiming int
	for _, v := range pack.TimingVectors {
		if !v.EpochNMinus1StartHeight.IsSet() || !v.BeaconWindowBlocks.IsSet() {
			sparseTiming++
		}
	}
	if sparseTiming == 0 {
		t.Error("no timing vector omits beacon geometry; the restraint is no longer exercised")
	}

	var shortDrawID int
	for _, v := range pack.NegativeVectors {
		for _, id := range v.WinnerDrawIDsHex {
			if len(id) != 64 {
				shortDrawID++
			}
		}
	}
	if shortDrawID == 0 {
		t.Error("no negative vector carries a wrong-length draw id; the restraint is no longer exercised")
	}
}

// TestValidPacksStillLoad is the counterweight to every rejection above.
func TestValidPacksStillLoad(t *testing.T) {
	if _, err := LoadDrawPack(); err != nil {
		t.Errorf("draw pack: %v", err)
	}
	if _, err := LoadSelectedDrawIDsPack(); err != nil {
		t.Errorf("selected-draw-ids pack: %v", err)
	}
	if _, err := LoadRewardPack(); err != nil {
		t.Errorf("reward pack: %v", err)
	}
}
