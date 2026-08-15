package consensusvectors

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDrawPack(t *testing.T) {
	pack, err := LoadDrawPack()
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if got, want := pack.Format, drawPackArtifact; got != want {
		t.Errorf("format = %q, want %q", got, want)
	}
	if got, want := pack.Version, drawPackVersion; got != want {
		t.Errorf("version = %d, want %d", got, want)
	}
	if got, want := pack.Revision, drawPackRevision; got != want {
		t.Errorf("revision = %d, want %d", got, want)
	}
	if !pack.Normative {
		t.Error("pack does not declare itself normative")
	}

	sections := []struct {
		name string
		got  int
		want int
	}{
		{"winner_count_vectors", len(pack.WinnerCountVectors), ExpectedWinnerCountVectors},
		{"timing_vectors", len(pack.TimingVectors), ExpectedTimingVectors},
		{"negative_vectors", len(pack.NegativeVectors), ExpectedDrawNegativeVectors},
		{"comparator_vectors", len(pack.ComparatorVectors), ExpectedComparatorVectors},
		{ProposerResolutionSection, len(pack.ProposerResolution), ExpectedProposerResolutionCases},
	}
	for _, section := range sections {
		if section.got != section.want {
			t.Errorf("%s holds %d cases, want %d", section.name, section.got, section.want)
		}
	}
}

func TestLoadSelectedDrawIDsPack(t *testing.T) {
	pack, err := LoadSelectedDrawIDsPack()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got, want := pack.Artifact, selectedDrawIDsArtifact; got != want {
		t.Errorf("artifact = %q, want %q", got, want)
	}
	if got, want := pack.Version, selectedDrawIDsVersion; got != want {
		t.Errorf("version = %d, want %d", got, want)
	}
	if got, want := pack.Revision, selectedDrawIDsRevision; got != want {
		t.Errorf("revision = %d, want %d", got, want)
	}
	if len(pack.Vectors) != ExpectedSelectedDrawIDsVectors {
		t.Errorf("vectors = %d, want %d", len(pack.Vectors), ExpectedSelectedDrawIDsVectors)
	}
	if len(pack.NegativeRequirement) != ExpectedSelectedNegativeReqs {
		t.Errorf("negative_requirements = %d, want %d", len(pack.NegativeRequirement), ExpectedSelectedNegativeReqs)
	}
}

func TestLoadRewardPack(t *testing.T) {
	pack, err := LoadRewardPack()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got, want := pack.Artifact, rewardPackArtifact; got != want {
		t.Errorf("artifact = %q, want %q", got, want)
	}
	if got, want := pack.Version, rewardPackVersion; got != want {
		t.Errorf("version = %d, want %d", got, want)
	}
	if got, want := pack.Revision, rewardPackRevision; got != want {
		t.Errorf("revision = %d, want %d", got, want)
	}

	sections := []struct {
		name string
		got  int
		want int
	}{
		{"emission_vectors", len(pack.EmissionVectors), ExpectedEmissionVectors},
		{"allocation_vectors", len(pack.AllocationVectors), ExpectedAllocationVectors},
		{"pool_vectors", len(pack.PoolVectors), ExpectedPoolVectors},
		{"required_assertions", len(pack.RequiredAssertions), ExpectedRequiredAssertions},
		{"negative_discriminators", len(pack.NegativeDiscriminators), ExpectedNegativeDiscriminators},
	}
	for _, section := range sections {
		if section.got != section.want {
			t.Errorf("%s holds %d cases, want %d", section.name, section.got, section.want)
		}
	}
}

// TestSupersededDrawPackIsNotTracked asserts the superseded unversioned pack is
// absent rather than merely unreferenced. Committing it beside r2 would create a
// second plausible artifact for the same behavior, which is exactly the
// ambiguity exact-filename loading exists to prevent.
func TestSupersededDrawPackIsNotTracked(t *testing.T) {
	path := filepath.Join("testdata", SupersededDrawPackFilename)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("superseded pack %s must not be tracked; os.Stat gave err = %v", path, err)
	}
}

// TestTestdataHoldsOnlyTrackedPacks asserts the tracked set is exactly the three
// canonical packs, so a stray copy cannot accumulate unnoticed.
func TestTestdataHoldsOnlyTrackedPacks(t *testing.T) {
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	want := map[string]bool{
		DrawPackFilename:            true,
		SelectedDrawIDsPackFilename: true,
		RewardPackFilename:          true,
	}
	for _, entry := range entries {
		if !want[entry.Name()] {
			t.Errorf("unexpected file in testdata: %s", entry.Name())
		}
		delete(want, entry.Name())
	}
	for missing := range want {
		t.Errorf("tracked pack is missing from testdata: %s", missing)
	}
}

func TestDecodePackRejectsMalformedJSON(t *testing.T) {
	var pack RewardPack
	err := decodePack("synthetic.json", []byte(`{"artifact":`), &pack)
	if !errors.Is(err, ErrMalformedPack) {
		t.Fatalf("err = %v, want ErrMalformedPack", err)
	}
}

// TestDecodePackRejectsUnknownFields is what stops a future revision adding a
// section that the harness then silently fails to execute.
func TestDecodePackRejectsUnknownFields(t *testing.T) {
	var pack RewardPack
	err := decodePack("synthetic.json", []byte(`{"artifact":"x","brand_new_section":[]}`), &pack)
	if !errors.Is(err, ErrMalformedPack) {
		t.Fatalf("err = %v, want ErrMalformedPack", err)
	}
}

// TestAssertMetadataRejects covers each way a pack can be the wrong artifact.
// Metadata mismatch is a distinct error from malformed JSON because the two call
// for different responses: a corrupted file versus the wrong file.
func TestAssertMetadataRejects(t *testing.T) {
	cases := []struct {
		name      string
		artifact  string
		version   int
		revision  int
		normative bool
	}{
		{"wrong artifact", "some-other-pack", 1, 1, true},
		{"wrong version", rewardPackArtifact, 2, 1, true},
		{"wrong revision", rewardPackArtifact, 1, 9, true},
		{"not normative", rewardPackArtifact, 1, 1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := assertMetadata(
				"synthetic.json",
				tc.artifact, rewardPackArtifact,
				tc.version, rewardPackVersion,
				tc.revision, rewardPackRevision,
				tc.normative,
			)
			if !errors.Is(err, ErrPackMetadataMismatch) {
				t.Fatalf("err = %v, want ErrPackMetadataMismatch", err)
			}
		})
	}

	if err := assertMetadata(
		"synthetic.json",
		rewardPackArtifact, rewardPackArtifact,
		rewardPackVersion, rewardPackVersion,
		rewardPackRevision, rewardPackRevision,
		true,
	); err != nil {
		t.Fatalf("matching metadata rejected: %v", err)
	}
}

// TestU64RejectsBareNumber pins the packs' string-encoded-integer convention. A
// bare JSON number would be decoded through float64 and lose precision above
// 2^53, which matters: one winner-count vector uses 2^64-1.
func TestU64RejectsBareNumber(t *testing.T) {
	var v U64
	if err := v.UnmarshalJSON([]byte(`18446744073709551615`)); !errors.Is(err, ErrMalformedPack) {
		t.Fatalf("err = %v, want ErrMalformedPack", err)
	}
	if err := v.UnmarshalJSON([]byte(`"18446744073709551615"`)); err != nil {
		t.Fatalf("quoted decimal rejected: %v", err)
	}
	if v.Uint64() != 18446744073709551615 {
		t.Fatalf("value = %d, want 18446744073709551615", v.Uint64())
	}
}
