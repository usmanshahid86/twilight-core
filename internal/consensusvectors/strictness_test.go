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

// The three helpers run the REAL load sequence over mutated bytes — decode,
// identity presence, identity comparison, then structure — rather than a
// reimplementation of it. A second copy of that sequence in the test file could
// drift out of step with the loader, and a strictness test that exercises a
// drifted copy proves nothing about what LoadDrawPack actually does.

func loadDrawFrom(data []byte) error {
	_, err := loadDrawPack(data)
	return err
}

func loadSelectedFrom(data []byte) error {
	_, err := loadSelectedDrawIDsPack(data)
	return err
}

func loadRewardFrom(data []byte) error {
	_, err := loadRewardPack(data)
	return err
}

func requireMetadataMismatch(t *testing.T, err error, what string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s was accepted", what)
	}
	if !errors.Is(err, ErrPackMetadataMismatch) {
		t.Fatalf("%s: err = %v, want ErrPackMetadataMismatch", what, err)
	}
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
	if permissive.Revision.Value() != rewardPackRevision {
		t.Fatalf("last-value-wins gave revision %d, want %d; the test no longer covers the hazard",
			permissive.Revision.Value(), rewardPackRevision)
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

// nested walks a chain of object members and returns the object at the end.
func nested(t *testing.T, doc map[string]any, path ...string) map[string]any {
	t.Helper()
	current := doc
	for _, key := range path {
		next, ok := current[key].(map[string]any)
		if !ok {
			t.Fatalf("%s is not an object", key)
		}
		current = next
	}
	return current
}

// elemAt returns the i-th element of an array member as an object.
func elemAt(t *testing.T, doc map[string]any, key string, i int) map[string]any {
	t.Helper()
	list, ok := doc[key].([]any)
	if !ok || len(list) <= i {
		t.Fatalf("%s has no element %d", key, i)
	}
	elem, ok := list[i].(map[string]any)
	if !ok {
		t.Fatalf("%s[%d] is not an object", key, i)
	}
	return elem
}

// elemNamed returns the array element whose "name" member equals name.
func elemNamed(t *testing.T, doc map[string]any, key, name string) map[string]any {
	t.Helper()
	list, ok := doc[key].([]any)
	if !ok {
		t.Fatalf("%s is not an array", key)
	}
	for _, raw := range list {
		elem, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if elem["name"] == name {
			return elem
		}
	}
	t.Fatalf("%s has no element named %q", key, name)
	return nil
}

// --- D. counts, booleans and nullable members ---------------------------------

// TestAllocationVectorRequiresNPos closes a real silent weakening. n_pos was a
// plain int, so deleting it decoded as 0, and the reward harness skips the
// carry <= n_pos-1 assertion when n_pos is 0. The pack could therefore certify
// strictly less while every test stayed green.
func TestAllocationVectorRequiresNPos(t *testing.T) {
	data := mutateJSON(t, rewardPackBytes, func(doc map[string]any) {
		delete(firstElem(t, doc, "allocation_vectors"), "n_pos")
	})
	requireMalformed(t, loadRewardFrom(data), "allocation vector without n_pos")

	// A legal zero must still be accepted: the zero-participation vector states
	// n_pos 0 legitimately.
	zeroed := mutateJSON(t, rewardPackBytes, func(doc map[string]any) {
		firstElem(t, doc, "allocation_vectors")["n_pos"] = 0
	})
	if err := loadRewardFrom(zeroed); err != nil {
		t.Fatalf("n_pos of 0 rejected: %v", err)
	}
}

// TestRequiredBooleansMustBePresent covers both polarities. A plain bool cannot
// separate "declared false" from "not declared", and three of these are
// canonically false — so for those, a deletion would decode to exactly the value
// the pack is supposed to state and would be invisible.
func TestRequiredBooleansMustBePresent(t *testing.T) {
	cases := []struct {
		name      string
		canonical bool
		remove    func(t *testing.T, doc map[string]any)
	}{
		{
			"generation_provenance.independent_check_required", true,
			func(t *testing.T, doc map[string]any) {
				delete(nested(t, doc, "generation_provenance"), "independent_check_required")
			},
		},
		{
			"primitives.beacon_hash_v1.committed_height_intentionally_absent", true,
			func(t *testing.T, doc map[string]any) {
				delete(nested(t, doc, "primitives", "beacon_hash_v1"), "committed_height_intentionally_absent")
			},
		},
		{
			"empty_set_cross_check.expected_equal", true,
			func(t *testing.T, doc map[string]any) {
				delete(nested(t, doc, "empty_set_cross_check"), "expected_equal")
			},
		},
		{
			"end_to_end.no_candidates.beacon_required", false,
			func(t *testing.T, doc map[string]any) {
				delete(nested(t, doc, "end_to_end", "no_candidates"), "beacon_required")
			},
		},
		{
			"end_to_end.single_candidate.beacon_required", false,
			func(t *testing.T, doc map[string]any) {
				delete(nested(t, doc, "end_to_end", "single_candidate"), "beacon_required")
			},
		},
		{
			"end_to_end.no_valid_beacon_insufficient_usable_blocks.beacon_hash_defined", false,
			func(t *testing.T, doc map[string]any) {
				delete(nested(t, doc, "end_to_end", "no_valid_beacon_insufficient_usable_blocks"), "beacon_hash_defined")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := mutateJSON(t, drawPackBytes, func(doc map[string]any) { tc.remove(t, doc) })
			requireMalformed(t, loadDrawFrom(data), "draw pack without "+tc.name)
		})
	}

	// A required boolean is required to be PRESENT, not to be true; flipping a
	// canonical true to false must still load, because the validator constrains
	// presence and the conformance test constrains the value.
	flipped := mutateJSON(t, drawPackBytes, func(doc map[string]any) {
		nested(t, doc, "generation_provenance")["independent_check_required"] = false
	})
	if err := loadDrawFrom(flipped); err != nil {
		t.Fatalf("a present-but-false boolean was rejected by structural validation: %v", err)
	}
}

// TestRateKDistinguishesAbsentFromNull covers the third state a *U64 cannot
// express. The packs use null to record that the rate arithmetic was never
// evaluated, which is a statement; an absent member would impersonate it.
func TestRateKDistinguishesAbsentFromNull(t *testing.T) {
	// Vector 0 is the zero-candidate short circuit, where rate_k is null.
	t.Run("missing for C<2 is rejected", func(t *testing.T) {
		data := mutateJSON(t, drawPackBytes, func(doc map[string]any) {
			delete(elemAt(t, doc, "winner_count_vectors", 0), "rate_k")
		})
		requireMalformed(t, loadDrawFrom(data), "winner-count vector without rate_k")
	})

	t.Run("explicit null for C<2 is accepted", func(t *testing.T) {
		data := mutateJSON(t, drawPackBytes, func(doc map[string]any) {
			elemAt(t, doc, "winner_count_vectors", 0)["rate_k"] = nil
		})
		if err := loadDrawFrom(data); err != nil {
			t.Fatalf("explicit null rate_k rejected: %v", err)
		}
	})

	t.Run("non-null for C<2 is rejected", func(t *testing.T) {
		data := mutateJSON(t, drawPackBytes, func(doc map[string]any) {
			elemAt(t, doc, "winner_count_vectors", 0)["rate_k"] = "0"
		})
		requireMalformed(t, loadDrawFrom(data), "short-circuit vector with a non-null rate_k")
	})

	// Vector 2 is the first case with two or more candidates.
	t.Run("missing for C>=2 is rejected", func(t *testing.T) {
		data := mutateJSON(t, drawPackBytes, func(doc map[string]any) {
			elem := elemAt(t, doc, "winner_count_vectors", 2)
			if elem["candidate_count"] == "0" || elem["candidate_count"] == "1" {
				t.Fatalf("vector 2 is a short circuit; the test targets the wrong element")
			}
			delete(elem, "rate_k")
		})
		requireMalformed(t, loadDrawFrom(data), "multi-candidate vector without rate_k")
	})

	t.Run("null for C>=2 is rejected", func(t *testing.T) {
		data := mutateJSON(t, drawPackBytes, func(doc map[string]any) {
			elemAt(t, doc, "winner_count_vectors", 2)["rate_k"] = nil
		})
		requireMalformed(t, loadDrawFrom(data), "multi-candidate vector with a null rate_k")
	})
}

// TestMetadataPresenceIsRequired separates "declares version 0" from "declares
// no version". Both are rejected, but only the second is an accurate report.
func TestMetadataPresenceIsRequired(t *testing.T) {
	for _, key := range []string{"version", "revision", "normative"} {
		t.Run("missing "+key, func(t *testing.T) {
			data := mutateJSON(t, rewardPackBytes, func(doc map[string]any) { delete(doc, key) })
			requireMalformed(t, loadRewardFrom(data), "reward pack without "+key)
		})
	}
}

// TestArtifactIdentityPresenceIsRequired separates a pack that declares the
// wrong artifact from one that declares none.
//
// Both are rejected, but only under the right class does the error describe what
// happened: an absent identifier is missing mandatory structure, while a
// metadata mismatch means the file names an artifact this code does not support.
// Reporting an absent name as `declares artifact ""` describes the file as
// saying something it does not say.
func TestArtifactIdentityPresenceIsRequired(t *testing.T) {
	cases := []struct {
		pack  string
		key   string
		bytes []byte
		load  func([]byte) error
		wrong string
	}{
		{"draw", "format", drawPackBytes, loadDrawFrom, "some-other-artifact"},
		{"selected-draw-ids", "artifact", selectedDrawIDsPackBytes, loadSelectedFrom, "some-other-artifact"},
		{"reward", "artifact", rewardPackBytes, loadRewardFrom, "some-other-artifact"},
	}

	for _, tc := range cases {
		t.Run(tc.pack+" pack without "+tc.key, func(t *testing.T) {
			data := mutateJSON(t, tc.bytes, func(doc map[string]any) { delete(doc, tc.key) })
			requireMalformed(t, tc.load(data), tc.pack+" pack without "+tc.key)
		})

		// Positive control for the other half of the taxonomy: a PRESENT but wrong
		// identifier must still report as a metadata mismatch, not as malformed.
		t.Run(tc.pack+" pack with the wrong "+tc.key, func(t *testing.T) {
			data := mutateJSON(t, tc.bytes, func(doc map[string]any) { doc[tc.key] = tc.wrong })
			requireMetadataMismatch(t, tc.load(data), tc.pack+" pack with a wrong "+tc.key)
		})

		// An empty string is present-but-blank rather than absent. It is treated as
		// missing, because a blank name identifies nothing.
		t.Run(tc.pack+" pack with a blank "+tc.key, func(t *testing.T) {
			data := mutateJSON(t, tc.bytes, func(doc map[string]any) { doc[tc.key] = "" })
			requireMalformed(t, tc.load(data), tc.pack+" pack with a blank "+tc.key)
		})
	}

	// The version and revision halves of the taxonomy, for completeness.
	t.Run("wrong revision is a metadata mismatch", func(t *testing.T) {
		data := mutateJSON(t, rewardPackBytes, func(doc map[string]any) { doc["revision"] = 999 })
		requireMetadataMismatch(t, loadRewardFrom(data), "reward pack with revision 999")
	})
}

// TestEndToEndCasesRequireExpectedOutcome restores coverage for a check that was
// removed by accident while the boolean-presence checks were added.
//
// Without it a case can lose the outcome it exists to assert, decode as "", and
// still load. The conformance comparison would then fail rather than pass, so
// nothing goes silently green — but the loader would no longer honor the
// contract that every mandatory field is detected at load time.
func TestEndToEndCasesRequireExpectedOutcome(t *testing.T) {
	cases := []struct {
		name string
		path []string
	}{
		{"no_candidates", []string{"end_to_end", "no_candidates"}},
		{"single_candidate", []string{"end_to_end", "single_candidate"}},
		{
			"no_valid_beacon_insufficient_usable_blocks",
			[]string{"end_to_end", "no_valid_beacon_insufficient_usable_blocks"},
		},
		{
			"no_valid_beacon_insufficient_distinct_proposers",
			[]string{"end_to_end", "no_valid_beacon_insufficient_distinct_proposers"},
		},
		{"success_with_target_slot_exclusion", []string{"end_to_end", "success_with_target_slot_exclusion"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			data := mutateJSON(t, drawPackBytes, func(doc map[string]any) {
				delete(nested(t, doc, tc.path...), "expected_outcome")
			})
			requireMalformed(t, loadDrawFrom(data), tc.name+" without expected_outcome")
		})
	}
}

// --- E. variant-specific timing and negative schemas --------------------------

// TestTimingVectorVariantSchemas deletes one required field from each timing
// shape. Before this, the loader asked only for name and expected, so any of
// these deletions passed.
func TestTimingVectorVariantSchemas(t *testing.T) {
	cases := []struct {
		vector string
		field  string
	}{
		{"valid_commit_first_block", "committed_height"},
		{"valid_commit_first_block", "epoch_n_minus_1_start_height"},
		{"valid_commit_last_pre_beacon_block", "beacon_start_offset_blocks"},
		{"commit_at_beacon_start_rejected", "beacon_window_blocks"},
		{"beacon_fit_rejection", "derived_beacon_end_height"},
		{"beacon_fit_rejection", "latest_permitted_beacon_end_height"},
		{"late_result_rejection", "published_height"},
		{"valid_result_last_block", "target_epoch_start_height"},
	}
	for _, tc := range cases {
		t.Run(tc.vector+" without "+tc.field, func(t *testing.T) {
			data := mutateJSON(t, drawPackBytes, func(doc map[string]any) {
				delete(elemNamed(t, doc, "timing_vectors", tc.vector), tc.field)
			})
			requireMalformed(t, loadDrawFrom(data), tc.vector+" without "+tc.field)
		})
	}

	// The beacon-fit case states no committed height, so requiring one would
	// reject the pack as written.
	t.Run("beacon_fit_rejection needs no committed_height", func(t *testing.T) {
		if elemNamed(t, decodeDoc(t, drawPackBytes), "timing_vectors", "beacon_fit_rejection")["committed_height"] != nil {
			t.Skip("the pack now states a committed height for this case")
		}
		if err := loadDrawFrom(drawPackBytes); err != nil {
			t.Fatalf("real pack rejected: %v", err)
		}
	})

	// An unrecognized case must fail loudly rather than fall through to the
	// weakest schema, where it would be checked by nothing.
	t.Run("unknown timing vector name", func(t *testing.T) {
		data := mutateJSON(t, drawPackBytes, func(doc map[string]any) {
			elemNamed(t, doc, "timing_vectors", "late_result_rejection")["name"] = "brand_new_timing_case"
		})
		requireMalformed(t, loadDrawFrom(data), "unknown timing vector name")
	})
}

// TestNegativeVectorVariantSchemas does the same for the rejection cases, and
// pins the one restraint that matters most: the deliberately wrong-length draw
// ID must still load, because malformed input is this vector's payload.
func TestNegativeVectorVariantSchemas(t *testing.T) {
	cases := []struct {
		vector string
		field  string
	}{
		{"candidate_list_not_strictly_sorted", "candidate_list_draw_ids_hex"},
		{"candidate_set_hash_mismatch", "published_candidate_set_hash_hex"},
		{"candidate_count_mismatch", "candidate_list_length"},
		{"wrong_published_winner", "published_winner_draw_ids_hex"},
		{"wrong_beacon_hash", "expected_beacon_hash_hex"},
		{"duplicate_winner", "winner_draw_ids_hex"},
		{"non_32_byte_winner", "winner_draw_ids_hex"},
		{"draw_result_key_mismatch", "result_slot_id"},
		{"winner_count_list_length_mismatch", "winner_count"},
	}
	for _, tc := range cases {
		t.Run(tc.vector+" without "+tc.field, func(t *testing.T) {
			data := mutateJSON(t, drawPackBytes, func(doc map[string]any) {
				delete(elemNamed(t, doc, "negative_vectors", tc.vector), tc.field)
			})
			requireMalformed(t, loadDrawFrom(data), tc.vector+" without "+tc.field)
		})
	}

	// Presence validation is not semantic validation. This vector exists to carry
	// a draw ID that is NOT 32 bytes; rejecting it structurally would delete the
	// case the conformance test needs.
	t.Run("wrong-length payload still loads", func(t *testing.T) {
		doc := decodeDoc(t, drawPackBytes)
		ids, ok := elemNamed(t, doc, "negative_vectors", "non_32_byte_winner")["winner_draw_ids_hex"].([]any)
		if !ok || len(ids) == 0 {
			t.Fatal("the wrong-length payload is missing")
		}
		if id, _ := ids[0].(string); len(id) == 64 {
			t.Fatal("the payload is 32 bytes; this vector no longer exercises the restraint")
		}
		if err := loadDrawFrom(drawPackBytes); err != nil {
			t.Fatalf("real pack rejected: %v", err)
		}
	})

	t.Run("unknown negative vector name", func(t *testing.T) {
		data := mutateJSON(t, drawPackBytes, func(doc map[string]any) {
			elemNamed(t, doc, "negative_vectors", "duplicate_winner")["name"] = "brand_new_negative_case"
		})
		requireMalformed(t, loadDrawFrom(data), "unknown negative vector name")
	})
}

func decodeDoc(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return doc
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
