package selectionv1_test

// Focused tests for implementation invariants the vector packs do not make
// obvious. The packs remain the primary oracle for protocol behavior; nothing
// here restates a protocol rule that a vector already pins.

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/twilight-project/twilight-core/x/mining/types/selectionv1"
)

func TestHexTransportIsStrict(t *testing.T) {
	const valid = "4779843297ece3a95590eee123f80a846cc2cecbfcda1fd88c7d2b3cc2ce91e3"

	if _, err := selectionv1.DrawIDFromHex(valid); err != nil {
		t.Fatalf("valid draw id rejected: %v", err)
	}

	// Uppercase decodes to identical bytes, so accepting it would give one value
	// two transport spellings and break canonical comparison of transport data.
	if _, err := selectionv1.DrawIDFromHex(strings.ToUpper(valid)); !errors.Is(err, selectionv1.ErrNotCanonical) {
		t.Errorf("uppercase hex: err = %v, want ErrNotCanonical", err)
	}
	for _, tc := range []struct {
		name  string
		value string
	}{
		{"too short", valid[:62]},
		{"too long", valid + "aa"},
		{"empty", ""},
	} {
		if _, err := selectionv1.DrawIDFromHex(tc.value); !errors.Is(err, selectionv1.ErrInvalidLength) {
			t.Errorf("%s: err = %v, want ErrInvalidLength", tc.name, err)
		}
	}
	// Correct length, non-hexadecimal content.
	if _, err := selectionv1.DrawIDFromHex(strings.Repeat("z", 64)); !errors.Is(err, selectionv1.ErrMalformedHex) {
		t.Errorf("non-hex: err = %v, want ErrMalformedHex", err)
	}
}

// TestChainIDLengthPrefixIsBounded covers the one input that can make a preimage
// unencodable: the chain ID is length-prefixed with U16BE, so a longer name has
// no representation and must be refused rather than truncated.
func TestChainIDLengthPrefixIsBounded(t *testing.T) {
	oversize := selectionv1.SelectionContext{
		ChainID:     strings.Repeat("a", math.MaxUint16+1),
		SlotID:      7,
		TargetEpoch: 1042,
	}
	if _, err := selectionv1.ComputeCandidateSetHash(oversize, nil); !errors.Is(err, selectionv1.ErrChainIDTooLong) {
		t.Errorf("oversize chain id: err = %v, want ErrChainIDTooLong", err)
	}

	atLimit := selectionv1.SelectionContext{
		ChainID:     strings.Repeat("a", math.MaxUint16),
		SlotID:      7,
		TargetEpoch: 1042,
	}
	if _, err := selectionv1.ComputeCandidateSetHash(atLimit, nil); err != nil {
		t.Errorf("chain id at the encodable limit rejected: %v", err)
	}
}

func TestDeriveBeaconWindow(t *testing.T) {
	start, end, err := selectionv1.DeriveBeaconWindow(1000, 48, 24)
	if err != nil {
		t.Fatalf("DeriveBeaconWindow: %v", err)
	}
	if start != 1048 || end != 1071 {
		t.Errorf("window = [%d, %d], want [1048, 1071]", start, end)
	}

	// A single-block window is degenerate but well defined: end == start.
	if start, end, err := selectionv1.DeriveBeaconWindow(1000, 48, 1); err != nil || start != 1048 || end != 1048 {
		t.Errorf("single-block window = [%d, %d], err = %v", start, end, err)
	}

	if _, _, err := selectionv1.DeriveBeaconWindow(1000, 48, 0); !errors.Is(err, selectionv1.ErrInvalidParams) {
		t.Errorf("zero window: err = %v, want ErrInvalidParams", err)
	}
	// Checked arithmetic: the window must not wrap into a small, plausible range.
	if _, _, err := selectionv1.DeriveBeaconWindow(math.MaxUint64, 48, 24); !errors.Is(
		err, selectionv1.ErrInvalidParams,
	) {
		t.Errorf("start overflow: err = %v, want ErrInvalidParams", err)
	}
	if _, _, err := selectionv1.DeriveBeaconWindow(0, math.MaxUint64, 24); !errors.Is(
		err, selectionv1.ErrInvalidParams,
	) {
		t.Errorf("end overflow: err = %v, want ErrInvalidParams", err)
	}
}

func TestFilterBeaconEntries(t *testing.T) {
	observed := []selectionv1.ObservedBlock{
		{Height: 10, ProposerSlotID: 7},
		{Height: 11, ProposerSlotID: 2},
		{Height: 12, ProposerSlotID: 3},
		{Height: 13, ProposerSlotID: 7},
		{Height: 14, ProposerSlotID: 2},
	}

	entries, err := selectionv1.FilterBeaconEntries(7, 10, 14, observed)
	if err != nil {
		t.Fatalf("FilterBeaconEntries: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("entries = %d, want 3", len(entries))
	}
	for _, e := range entries {
		if e.ProposerSlotID == 7 {
			t.Errorf("target Slot block at height %d was not excluded", e.Height)
		}
	}

	stats := selectionv1.ComputeBeaconStats(entries)
	if stats.UsableCount != 3 {
		t.Errorf("usable = %d, want 3", stats.UsableCount)
	}
	// Slot 2 proposes twice; it counts once.
	if stats.DistinctExternalProposers != 2 {
		t.Errorf("distinct = %d, want 2", stats.DistinctExternalProposers)
	}

	// A height outside the deterministic window is malformed input, not a block
	// to silently drop: dropping it would let a caller shrink the window.
	if _, err := selectionv1.FilterBeaconEntries(7, 10, 14, []selectionv1.ObservedBlock{
		{Height: 15, ProposerSlotID: 2},
	}); !errors.Is(err, selectionv1.ErrInvalidBeaconWindow) {
		t.Errorf("out-of-window height: err = %v, want ErrInvalidBeaconWindow", err)
	}
	// Repeated or descending heights would make the beacon preimage ambiguous.
	for _, tc := range []struct {
		name   string
		blocks []selectionv1.ObservedBlock
	}{
		{"repeated", []selectionv1.ObservedBlock{{Height: 11, ProposerSlotID: 2}, {Height: 11, ProposerSlotID: 3}}},
		{"descending", []selectionv1.ObservedBlock{{Height: 12, ProposerSlotID: 2}, {Height: 11, ProposerSlotID: 3}}},
	} {
		if _, err := selectionv1.FilterBeaconEntries(7, 10, 14, tc.blocks); !errors.Is(
			err, selectionv1.ErrInvalidBeaconWindow,
		) {
			t.Errorf("%s heights: err = %v, want ErrInvalidBeaconWindow", tc.name, err)
		}
	}
}

func TestBeaconThresholdsSatisfied(t *testing.T) {
	thresholds := selectionv1.BeaconThresholds{MinExternalBeaconBlocks: 12, MinDistinctExternalProposers: 3}
	cases := []struct {
		name  string
		stats selectionv1.BeaconStats
		want  bool
	}{
		{"exactly at both thresholds", selectionv1.BeaconStats{UsableCount: 12, DistinctExternalProposers: 3}, true},
		{"one usable block short", selectionv1.BeaconStats{UsableCount: 11, DistinctExternalProposers: 3}, false},
		{"one proposer short", selectionv1.BeaconStats{UsableCount: 12, DistinctExternalProposers: 2}, false},
		{"comfortably above", selectionv1.BeaconStats{UsableCount: 20, DistinctExternalProposers: 5}, true},
	}
	for _, tc := range cases {
		if got := thresholds.Satisfied(tc.stats); got != tc.want {
			t.Errorf("%s: satisfied = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestSelectedCountRejectsInvalidLimits(t *testing.T) {
	valid := selectionv1.CountLimits{SelectionRateBps: 2_500, SlotMaxSelected: 100, ProtocolMaxSelected: 1_000}
	if _, err := selectionv1.SelectedCount(10, valid); err != nil {
		t.Fatalf("valid limits rejected: %v", err)
	}

	invalid := []struct {
		name   string
		limits selectionv1.CountLimits
	}{
		{"zero rate", selectionv1.CountLimits{SelectionRateBps: 0, SlotMaxSelected: 100, ProtocolMaxSelected: 1_000}},
		{"rate above the immutable ceiling", selectionv1.CountLimits{SelectionRateBps: 5_001, SlotMaxSelected: 100, ProtocolMaxSelected: 1_000}},
		{"zero slot maximum", selectionv1.CountLimits{SelectionRateBps: 2_500, SlotMaxSelected: 0, ProtocolMaxSelected: 1_000}},
		{"zero protocol maximum", selectionv1.CountLimits{SelectionRateBps: 2_500, SlotMaxSelected: 100, ProtocolMaxSelected: 0}},
	}
	for _, tc := range invalid {
		if _, err := selectionv1.SelectedCount(10, tc.limits); !errors.Is(err, selectionv1.ErrInvalidParams) {
			t.Errorf("%s: err = %v, want ErrInvalidParams", tc.name, err)
		}
	}
}

// TestSelectedCountNeverExceedsHalf pins the immutable floor(C/2) cap across the
// full uint64 range, including the maximum candidate count where a naive C*r
// product would wrap.
func TestSelectedCountNeverExceedsHalf(t *testing.T) {
	limits := selectionv1.CountLimits{
		SelectionRateBps:    5_000,
		SlotMaxSelected:     math.MaxUint64,
		ProtocolMaxSelected: math.MaxUint64,
	}
	for _, candidateCount := range []uint64{
		2, 3, 9_999, 10_000, 10_001, 1 << 32, math.MaxUint64 - 1, math.MaxUint64,
	} {
		k, err := selectionv1.SelectedCount(candidateCount, limits)
		if err != nil {
			t.Fatalf("C=%d: %v", candidateCount, err)
		}
		if k > candidateCount/2 {
			t.Errorf("C=%d: K = %d exceeds floor(C/2) = %d", candidateCount, k, candidateCount/2)
		}
		if k == 0 {
			t.Errorf("C=%d: K = 0, but K is at least 1 for C >= 2", candidateCount)
		}
	}
}

// TestRankingIsIndependentOfInputOrder is a determinism property, not a protocol
// rule: the comparator is a strict total order over distinct draw IDs, so any
// permutation of the same candidates must rank identically. Without this, two
// nodes assembling the candidate slice differently could select different
// participants from the same committed data.
func TestRankingIsIndependentOfInputOrder(t *testing.T) {
	sc := selectionv1.SelectionContext{ChainID: "twilight-1", SlotID: 7, TargetEpoch: 1042}
	var candidateSetHash, beaconHash selectionv1.Hash
	for i := range candidateSetHash {
		candidateSetHash[i] = byte(i)
		beaconHash[i] = byte(255 - i)
	}

	ids := make([]selectionv1.DrawID, 0, 16)
	for i := 0; i < 16; i++ {
		var id selectionv1.DrawID
		id[31] = byte(i + 1)
		ids = append(ids, id)
	}

	base, err := selectionv1.ComputeTickets(sc, candidateSetHash, beaconHash, ids)
	if err != nil {
		t.Fatalf("ComputeTickets: %v", err)
	}
	want := selectionv1.RankCandidates(base)

	// Rotations and a reversal are enough to show order independence without
	// enumerating 16! permutations.
	for shift := 1; shift < len(base); shift++ {
		rotated := append(append([]selectionv1.RankedCandidate{}, base[shift:]...), base[:shift]...)
		got := selectionv1.RankCandidates(rotated)
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("rotation by %d changed ranking at %d", shift, i)
			}
		}
	}
	reversed := make([]selectionv1.RankedCandidate, 0, len(base))
	for i := len(base) - 1; i >= 0; i-- {
		reversed = append(reversed, base[i])
	}
	got := selectionv1.RankCandidates(reversed)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("reversal changed ranking at %d", i)
		}
	}

	// The ranking must be strictly ascending under the normative comparator.
	for i := 1; i < len(want); i++ {
		if selectionv1.CompareCandidates(want[i-1], want[i]) >= 0 {
			t.Errorf("ranking is not strictly ascending at %d", i)
		}
	}
}

func TestRankCandidatesDoesNotMutateInput(t *testing.T) {
	var low, high selectionv1.RankedCandidate
	low.DrawID[31] = 1
	low.Ticket[0] = 0x01
	high.DrawID[31] = 2
	high.Ticket[0] = 0xff

	input := []selectionv1.RankedCandidate{high, low}
	_ = selectionv1.RankCandidates(input)
	if input[0] != high || input[1] != low {
		t.Error("RankCandidates reordered the caller's slice")
	}
}

func TestSelectFirstKClampsToCandidateCount(t *testing.T) {
	var a, b selectionv1.RankedCandidate
	a.DrawID[31] = 1
	b.DrawID[31] = 2
	ranked := []selectionv1.RankedCandidate{a, b}

	if got := selectionv1.SelectFirstK(ranked, 0); len(got) != 0 {
		t.Errorf("k=0 selected %d", len(got))
	}
	if got := selectionv1.SelectFirstK(ranked, 1); len(got) != 1 || got[0] != a.DrawID {
		t.Errorf("k=1 selected %v", got)
	}
	if got := selectionv1.SelectFirstK(ranked, 99); len(got) != 2 {
		t.Errorf("k beyond the candidate count selected %d, want 2", len(got))
	}
}

func TestEvaluateRejectsNonCanonicalCandidateList(t *testing.T) {
	sc := selectionv1.SelectionContext{ChainID: "twilight-1", SlotID: 7, TargetEpoch: 1042}
	var first, second selectionv1.DrawID
	first[31] = 2
	second[31] = 1

	_, err := selectionv1.Evaluate(selectionv1.EvaluationInput{
		Context:          sc,
		CandidateDrawIDs: []selectionv1.DrawID{first, second},
	})
	if !errors.Is(err, selectionv1.ErrCandidateListNotCanonical) {
		t.Errorf("err = %v, want ErrCandidateListNotCanonical", err)
	}

	// Duplicates are non-canonical too: strict ordering is what makes the
	// candidate count an honest cardinality.
	_, err = selectionv1.Evaluate(selectionv1.EvaluationInput{
		Context:          sc,
		CandidateDrawIDs: []selectionv1.DrawID{first, first},
	})
	if !errors.Is(err, selectionv1.ErrCandidateListNotCanonical) {
		t.Errorf("duplicate ids: err = %v, want ErrCandidateListNotCanonical", err)
	}
}

// TestSelectedDrawIDsHashAcceptsAnyOrder is the counterpart to the candidate
// list rule and guards against over-applying canonicality: the selected list is
// in RANKING order, which is not draw-ID order, so requiring it to be sorted
// would reject every honest multi-participant result.
func TestSelectedDrawIDsHashAcceptsAnyOrder(t *testing.T) {
	sc := selectionv1.SelectionContext{ChainID: "twilight-1", SlotID: 7, TargetEpoch: 1042}
	var low, high selectionv1.DrawID
	low[31] = 1
	high[31] = 2

	descending, err := selectionv1.ComputeSelectedDrawIDsHash(sc, []selectionv1.DrawID{high, low})
	if err != nil {
		t.Fatalf("descending order rejected: %v", err)
	}
	ascending, err := selectionv1.ComputeSelectedDrawIDsHash(sc, []selectionv1.DrawID{low, high})
	if err != nil {
		t.Fatalf("ascending order rejected: %v", err)
	}
	if descending == ascending {
		t.Error("digest does not commit to the order of the selected list")
	}
}

func TestOutcomeString(t *testing.T) {
	cases := map[selectionv1.Outcome]string{
		selectionv1.OutcomeSuccess:       "SUCCESS",
		selectionv1.OutcomeNoCandidates:  "NO_CANDIDATES",
		selectionv1.OutcomeNoValidBeacon: "NO_VALID_BEACON",
		selectionv1.OutcomeUnspecified:   "UNSPECIFIED",
	}
	for outcome, want := range cases {
		if got := outcome.String(); got != want {
			t.Errorf("Outcome(%d).String() = %q, want %q", outcome, got, want)
		}
	}
}

func TestValidateCanonicalDrawIDsAcceptsSmallLists(t *testing.T) {
	if err := selectionv1.ValidateCanonicalDrawIDs(nil); err != nil {
		t.Errorf("empty list rejected: %v", err)
	}
	var only selectionv1.DrawID
	only[0] = 9
	if err := selectionv1.ValidateCanonicalDrawIDs([]selectionv1.DrawID{only}); err != nil {
		t.Errorf("single-element list rejected: %v", err)
	}
}
