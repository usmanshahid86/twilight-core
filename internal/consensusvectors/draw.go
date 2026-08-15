package consensusvectors

import (
	_ "embed"
	"fmt"
)

// The r2 TokenDrop draw golden-vector pack: the frozen Selection V1 primitives,
// the deterministic count K, ranking, timing rules and rejection cases.
//
//go:embed testdata/twilight-tokendrop-draw-v1-golden-vectors-r2.json
var drawPackBytes []byte

const (
	drawPackArtifact = "twilight-tokendrop-draw-v1-golden-vectors"
	drawPackVersion  = 1
	drawPackRevision = 2
)

// DrawPack is the r2 golden-vector pack.
//
// The pack calls a selected participant a "winner"; the specification and the
// selectionv1 package say "selected participant". The field names here follow
// the pack, because these types mirror an artifact whose bytes are frozen.
type DrawPack struct {
	Format               string               `json:"format"`
	Version              Int                  `json:"version"`
	Revision             Int                  `json:"revision"`
	Normative            Bool                 `json:"normative"`
	SpecStatus           string               `json:"spec_status"`
	EncodingNotes        DrawEncodingNotes    `json:"encoding_notes"`
	GenerationProvenance DrawProvenance       `json:"generation_provenance"`
	Primitives           DrawPrimitives       `json:"primitives"`
	WinnerCountVectors   []WinnerCountVector  `json:"winner_count_vectors"`
	EndToEnd             DrawEndToEnd         `json:"end_to_end"`
	ProposerResolution   []ProposerResVector  `json:"proposer_resolution_vectors"`
	TimingVectors        []TimingVector       `json:"timing_vectors"`
	NegativeVectors      []DrawNegativeVector `json:"negative_vectors"`
	ComparatorVectors    []ComparatorVector   `json:"comparator_vectors"`
	EmptySetCrossCheck   EmptySetCrossCheck   `json:"empty_set_cross_check"`
}

// DrawEncodingNotes records the pack's own statement of its byte conventions.
type DrawEncodingNotes struct {
	Integers   string `json:"integers"`
	ChainID    string `json:"chain_id"`
	HashFields string `json:"hash_fields"`
	DrawIDs    string `json:"draw_ids"`
	Ordering   string `json:"ordering"`
}

// DrawProvenance records how the pack's expected outputs were generated.
type DrawProvenance struct {
	GeneratedWith           string `json:"generated_with"`
	BlockHashFixtureRule    string `json:"block_hash_fixture_rule"`
	IndependentCheckRequire Bool   `json:"independent_check_required"`
	Note                    string `json:"note"`
}

// DrawPrimitives holds one vector per frozen hash primitive.
type DrawPrimitives struct {
	DrawIDV1                DrawIDVector           `json:"draw_id_v1"`
	CandidateSetHashV1      CandidateSetHashVector `json:"candidate_set_hash_v1"`
	CandidateSetHashEmptyV1 CandidateSetHashVector `json:"candidate_set_hash_empty_v1"`
	BeaconHashV1            BeaconHashVector       `json:"beacon_hash_v1"`
	TicketV1                TicketVector           `json:"ticket_v1"`
}

// DrawIDVector is the DrawIDV1 primitive vector.
type DrawIDVector struct {
	ChainID                string `json:"chain_id"`
	SlotID                 U64    `json:"slot_id"`
	TargetEpoch            U64    `json:"target_epoch"`
	ParticipationSecretHex string `json:"participation_secret_hex"`
	ExpectedDrawIDHex      string `json:"expected_draw_id_hex"`
}

// CandidateSetHashVector is a CandidateSetHashV1 primitive vector.
type CandidateSetHashVector struct {
	ChainID                     string   `json:"chain_id"`
	SlotID                      U64      `json:"slot_id"`
	TargetEpoch                 U64      `json:"target_epoch"`
	DrawIDsHex                  []string `json:"draw_ids_hex"`
	ExpectedCandidateSetHashHex string   `json:"expected_candidate_set_hash_hex"`
}

// BeaconHashVector is the BeaconHashV1 primitive vector.
type BeaconHashVector struct {
	ChainID                        string        `json:"chain_id"`
	SlotID                         U64           `json:"slot_id"`
	TargetEpoch                    U64           `json:"target_epoch"`
	CandidateSetHashHex            string        `json:"candidate_set_hash_hex"`
	BeaconStartHeight              U64           `json:"beacon_start_height"`
	BeaconEndHeight                U64           `json:"beacon_end_height"`
	IncludedEntries                []BeaconEntry `json:"included_entries"`
	ExpectedBeaconHashHex          string        `json:"expected_beacon_hash_hex"`
	CommittedHeightIntentionallyNo Bool          `json:"committed_height_intentionally_absent"`
}

// BeaconEntry is one included beacon entry.
type BeaconEntry struct {
	Height         U64    `json:"height"`
	ProposerSlotID U64    `json:"proposer_slot_id"`
	BlockHashHex   string `json:"block_hash_hex"`
}

// TicketVector is the TicketV1 primitive vector.
type TicketVector struct {
	ChainID             string `json:"chain_id"`
	SlotID              U64    `json:"slot_id"`
	TargetEpoch         U64    `json:"target_epoch"`
	CandidateSetHashHex string `json:"candidate_set_hash_hex"`
	BeaconHashHex       string `json:"beacon_hash_hex"`
	DrawIDHex           string `json:"draw_id_hex"`
	ExpectedTicketHex   string `json:"expected_ticket_hex"`
}

// WinnerCountVector is one deterministic-count K vector. RateK is null for the
// zero- and one-candidate short circuits, where the rate arithmetic is not
// evaluated at all.
type WinnerCountVector struct {
	CandidateCount            U64         `json:"candidate_count"`
	SelectionRateBps          U64         `json:"selection_rate_bps"`
	SlotMaxWinners            U64         `json:"slot_max_winners"`
	ProtocolMaxWinnersPerDraw U64         `json:"protocol_max_winners_per_draw"`
	QuotientQ                 U64         `json:"quotient_q"`
	RemainderRem              U64         `json:"remainder_rem"`
	RateK                     NullableU64 `json:"rate_k"`
	ExpectedK                 U64         `json:"expected_k"`
}

// DrawEndToEnd holds the six end-to-end Selection cases.
type DrawEndToEnd struct {
	SuccessWithTargetSlotExclusion    SuccessCase        `json:"success_with_target_slot_exclusion"`
	CommitmentHeightInvariance        InvarianceCase     `json:"commitment_height_invariance"`
	NoCandidates                      SmallCandidateCase `json:"no_candidates"`
	SingleCandidate                   SmallCandidateCase `json:"single_candidate"`
	NoValidBeaconInsufficientUsable   InvalidBeaconCase  `json:"no_valid_beacon_insufficient_usable_blocks"`
	NoValidBeaconInsufficientDistinct InvalidBeaconCase  `json:"no_valid_beacon_insufficient_distinct_proposers"`
}

// DrawCommitment is the committed Selection data of an end-to-end case.
type DrawCommitment struct {
	CandidateCount      U64    `json:"candidate_count"`
	CandidateSetHashHex string `json:"candidate_set_hash_hex"`
	CommittedHeight     U64    `json:"committed_height"`
	SelectionRateBps    U64    `json:"selection_rate_bps"`
	SlotMaxWinners      U64    `json:"slot_max_winners"`
}

// DrawParams are the Selection parameters of an end-to-end case.
// ProtocolMaxWinnersPerDraw is absent from the invalid-beacon cases, which never
// reach the count arithmetic.
type DrawParams struct {
	BeaconStartOffsetBlocks      U64 `json:"beacon_start_offset_blocks"`
	BeaconWindowBlocks           U64 `json:"beacon_window_blocks"`
	MinExternalBeaconBlocks      U64 `json:"min_external_beacon_blocks"`
	MinDistinctExternalProposers U64 `json:"min_distinct_external_proposers"`
	ProtocolMaxWinnersPerDraw    U64 `json:"protocol_max_winners_per_draw"`
}

// ObservedBlock is one observed block of a beacon window, with its proposer
// already resolved to a Core Slot.
type ObservedBlock struct {
	Height                 U64    `json:"height"`
	ResolvedProposerSlotID U64    `json:"resolved_proposer_slot_id"`
	BlockHashHex           string `json:"block_hash_hex"`
}

// RankingEntry is one (draw ID, ticket) pair in expected ranking order.
type RankingEntry struct {
	DrawIDHex string `json:"draw_id_hex"`
	TicketHex string `json:"ticket_hex"`
}

// SuccessCase is the full multi-candidate success path.
type SuccessCase struct {
	ChainID                           string          `json:"chain_id"`
	SlotID                            U64             `json:"slot_id"`
	TargetEpoch                       U64             `json:"target_epoch"`
	EpochNMinus1StartHeight           U64             `json:"epoch_n_minus_1_start_height"`
	EpochNStartHeight                 U64             `json:"epoch_n_start_height"`
	DrawCommitment                    DrawCommitment  `json:"draw_commitment"`
	DrawParams                        DrawParams      `json:"draw_params"`
	CandidateListDrawIDsHex           []string        `json:"candidate_list_draw_ids_hex"`
	ObservedBeaconWindow              []ObservedBlock `json:"observed_beacon_window"`
	ExpectedIncludedEntries           []BeaconEntry   `json:"expected_included_entries"`
	ExpectedUsableBlockCount          U64             `json:"expected_usable_block_count"`
	ExpectedDistinctExternalProposers U64             `json:"expected_distinct_external_proposers"`
	ExpectedBeaconHashHex             string          `json:"expected_beacon_hash_hex"`
	ExpectedK                         U64             `json:"expected_k"`
	ExpectedRanking                   []RankingEntry  `json:"expected_ranking"`
	ExpectedWinnerDrawIDsHex          []string        `json:"expected_winner_draw_ids_hex"`
	ExpectedOutcome                   string          `json:"expected_outcome"`
}

// InvarianceCase asserts that commitment timing does not move the beacon.
type InvarianceCase struct {
	ChainID                      string   `json:"chain_id"`
	SlotID                       U64      `json:"slot_id"`
	TargetEpoch                  U64      `json:"target_epoch"`
	EpochNMinus1StartHeight      U64      `json:"epoch_n_minus_1_start_height"`
	BeaconStartHeight            U64      `json:"beacon_start_height"`
	CommitmentHeights            []U64    `json:"commitment_heights"`
	ExpectedSameBeaconHashHex    string   `json:"expected_same_beacon_hash_hex"`
	ExpectedSameWinnerDrawIDsHex []string `json:"expected_same_winner_draw_ids_hex"`
	Assertion                    string   `json:"assertion"`
}

// SmallCandidateCase covers the zero- and one-candidate short circuits, neither
// of which requires a beacon.
type SmallCandidateCase struct {
	ChainID                     string   `json:"chain_id"`
	SlotID                      U64      `json:"slot_id"`
	TargetEpoch                 U64      `json:"target_epoch"`
	CandidateListDrawIDsHex     []string `json:"candidate_list_draw_ids_hex"`
	ExpectedCandidateSetHashHex string   `json:"expected_candidate_set_hash_hex"`
	ExpectedK                   U64      `json:"expected_k"`
	BeaconRequired              Bool     `json:"beacon_required"`
	ExpectedOutcome             string   `json:"expected_outcome"`
	ExpectedWinnerDrawIDsHex    []string `json:"expected_winner_draw_ids_hex"`
}

// InvalidBeaconCase covers a window that fails a V1 validity threshold.
type InvalidBeaconCase struct {
	ChainID                           string          `json:"chain_id"`
	SlotID                            U64             `json:"slot_id"`
	TargetEpoch                       U64             `json:"target_epoch"`
	DrawParams                        DrawParams      `json:"draw_params"`
	ObservedBeaconWindow              []ObservedBlock `json:"observed_beacon_window"`
	ExpectedUsableBlockCount          U64             `json:"expected_usable_block_count"`
	ExpectedDistinctExternalProposers U64             `json:"expected_distinct_external_proposers"`
	ExpectedOutcome                   string          `json:"expected_outcome"`
	BeaconHashDefined                 Bool            `json:"beacon_hash_defined"`
}

// ProposerResVector is a historical proposer-attribution fixture. Executing one
// requires the canonical historical proposer resolver and consensus-key history;
// see the deferred-fixture manifest.
type ProposerResVector struct {
	Name                          string              `json:"name"`
	SlotID                        U64                 `json:"slot_id"`
	ValidatorUpdateEmittedHeightU U64                 `json:"validator_update_emitted_height_u"`
	OldConsensusAddressHex        string              `json:"old_consensus_address_hex"`
	NewConsensusAddressHex        string              `json:"new_consensus_address_hex"`
	OldValidUntilHeightExclusive  U64                 `json:"old_valid_until_height_exclusive"`
	NewValidFromHeight            U64                 `json:"new_valid_from_height"`
	Assertions                    []ProposerAssertion `json:"assertions"`
	Note                          string              `json:"note"`
}

// ProposerAssertion is one height-to-Slot attribution expectation.
type ProposerAssertion struct {
	Height              U64    `json:"height"`
	ConsensusAddressHex string `json:"consensus_address_hex"`
	ExpectedSlotID      U64    `json:"expected_slot_id"`
}

// TimingVector is one commitment- or publication-height rule case. The fields
// populated vary by case: each vector exercises exactly one rule.
type TimingVector struct {
	Name                           string `json:"name"`
	EpochNMinus1StartHeight        U64    `json:"epoch_n_minus_1_start_height"`
	EpochNStartHeight              U64    `json:"epoch_n_start_height"`
	BeaconStartOffsetBlocks        U64    `json:"beacon_start_offset_blocks"`
	BeaconWindowBlocks             U64    `json:"beacon_window_blocks"`
	CommittedHeight                U64    `json:"committed_height"`
	DerivedBeaconStartHeight       U64    `json:"derived_beacon_start_height"`
	DerivedBeaconEndHeight         U64    `json:"derived_beacon_end_height"`
	LatestPermittedBeaconEndHeight U64    `json:"latest_permitted_beacon_end_height"`
	TargetEpochStartHeight         U64    `json:"target_epoch_start_height"`
	PublishedHeight                U64    `json:"published_height"`
	Expected                       string `json:"expected"`
}

// ComparatorVector pins the ranking tie-break. It is synthetic: it does not
// claim a real SHA-256 ticket collision.
type ComparatorVector struct {
	Name                   string         `json:"name"`
	SyntheticTicketHex     string         `json:"synthetic_ticket_hex"`
	Candidates             []RankingEntry `json:"candidates"`
	ExpectedOrderDrawIDHex []string       `json:"expected_order_draw_ids_hex"`
	Note                   string         `json:"note"`
}

// EmptySetCrossCheck asserts that the empty candidate set hashes deterministically.
type EmptySetCrossCheck struct {
	InputA           CandidateSetHashVector `json:"input_a"`
	InputB           CandidateSetHashVector `json:"input_b"`
	ExpectedHashAHex string                 `json:"expected_hash_a_hex"`
	ExpectedHashBHex string                 `json:"expected_hash_b_hex"`
	ExpectedEqual    Bool                   `json:"expected_equal"`
	Note             string                 `json:"note"`
}

// DrawNegativeVector is one rejection case. The populated fields vary by case.
type DrawNegativeVector struct {
	Name                        string   `json:"name"`
	ExpectedError               string   `json:"expected_error"`
	CandidateListDrawIDsHex     []string `json:"candidate_list_draw_ids_hex"`
	ExpectedCandidateSetHashHex string   `json:"expected_candidate_set_hash_hex"`
	PublishedCandidateSetHash   string   `json:"published_candidate_set_hash_hex"`
	DrawCommitmentCandidateCnt  U64      `json:"draw_commitment_candidate_count"`
	CandidateListCandidateCount U64      `json:"candidate_list_candidate_count"`
	CandidateListLength         U64      `json:"candidate_list_length"`
	ExpectedWinnerDrawIDsHex    []string `json:"expected_winner_draw_ids_hex"`
	PublishedWinnerDrawIDsHex   []string `json:"published_winner_draw_ids_hex"`
	ExpectedBeaconHashHex       string   `json:"expected_beacon_hash_hex"`
	PublishedBeaconHashHex      string   `json:"published_beacon_hash_hex"`
	WinnerCount                 U64      `json:"winner_count"`
	WinnerDrawIDsHex            []string `json:"winner_draw_ids_hex"`
	CommitmentSlotID            U64      `json:"commitment_slot_id"`
	CommitmentTargetEpoch       U64      `json:"commitment_target_epoch"`
	ResultSlotID                U64      `json:"result_slot_id"`
	ResultTargetEpoch           U64      `json:"result_target_epoch"`
}

// LoadDrawPack returns the r2 draw pack, verifying its declared identity and its
// mandatory structure.
func LoadDrawPack() (DrawPack, error) {
	var pack DrawPack
	if err := decodePack(DrawPackFilename, drawPackBytes, &pack); err != nil {
		return DrawPack{}, err
	}
	if err := requireMetadataPresence(DrawPackFilename, pack.Version, pack.Revision, pack.Normative); err != nil {
		return DrawPack{}, err
	}
	if err := assertMetadata(
		DrawPackFilename,
		pack.Format, drawPackArtifact,
		pack.Version.Value(), drawPackVersion,
		pack.Revision.Value(), drawPackRevision,
		pack.Normative.Bool(),
	); err != nil {
		return DrawPack{}, err
	}
	if err := pack.validate(DrawPackFilename); err != nil {
		return DrawPack{}, err
	}
	return pack, nil
}

// requireContext validates the (chain, Slot, target epoch) triple every Selection
// vector is scoped by.
func requireContext(filename, prefix, chainID string, slotID, targetEpoch U64) error {
	return firstError(
		requireText(filename, prefix+".chain_id", chainID),
		requireSet(filename, prefix+".slot_id", slotID),
		requireSet(filename, prefix+".target_epoch", targetEpoch),
	)
}

// validate checks the mandatory structure of the r2 pack.
//
// Sections whose cases legitimately populate different fields — timing vectors
// and negative vectors — are checked only for the fields every case carries. A
// timing vector that exercises the late-result rule states no beacon geometry,
// and a negative vector that exercises an invalid draw-ID length deliberately
// carries a value that is not 32 bytes. Demanding the union of every modeled
// field would reject the pack exactly as its specification intends it.
func (p DrawPack) validate(filename string) error {
	if err := firstError(
		requireText(filename, "spec_status", p.SpecStatus),
		requireText(filename, "encoding_notes.integers", p.EncodingNotes.Integers),
		requireText(filename, "encoding_notes.chain_id", p.EncodingNotes.ChainID),
		requireText(filename, "encoding_notes.hash_fields", p.EncodingNotes.HashFields),
		requireText(filename, "encoding_notes.draw_ids", p.EncodingNotes.DrawIDs),
		requireText(filename, "encoding_notes.ordering", p.EncodingNotes.Ordering),
		requireText(filename, "generation_provenance.generated_with", p.GenerationProvenance.GeneratedWith),
		requireText(filename, "generation_provenance.block_hash_fixture_rule", p.GenerationProvenance.BlockHashFixtureRule),
		requireText(filename, "generation_provenance.note", p.GenerationProvenance.Note),
		requireBoolSet(filename, "generation_provenance.independent_check_required", p.GenerationProvenance.IndependentCheckRequire),
	); err != nil {
		return err
	}

	if err := p.Primitives.validate(filename); err != nil {
		return err
	}

	if err := requireNonEmptySlice(filename, "winner_count_vectors", len(p.WinnerCountVectors)); err != nil {
		return err
	}
	for i, v := range p.WinnerCountVectors {
		prefix := fmt.Sprintf("winner_count_vectors[%d]", i)
		if err := firstError(
			requireSet(filename, prefix+".candidate_count", v.CandidateCount),
			requireSet(filename, prefix+".selection_rate_bps", v.SelectionRateBps),
			requireSet(filename, prefix+".slot_max_winners", v.SlotMaxWinners),
			requireSet(filename, prefix+".protocol_max_winners_per_draw", v.ProtocolMaxWinnersPerDraw),
			requireSet(filename, prefix+".quotient_q", v.QuotientQ),
			requireSet(filename, prefix+".remainder_rem", v.RemainderRem),
			requireSet(filename, prefix+".expected_k", v.ExpectedK),
		); err != nil {
			return err
		}

		// rate_k is null for the zero- and one-candidate short circuits, where the
		// rate arithmetic is never evaluated, and a value everywhere else. The null
		// is itself a statement, so the member must be PRESENT in both cases: an
		// absent member would otherwise read as "not evaluated" without the pack
		// ever saying so.
		if !v.RateK.IsSet() {
			return structureError(filename, "%s.rate_k is missing", prefix)
		}
		shortCircuit := v.CandidateCount.Uint64() < 2
		switch {
		case shortCircuit && !v.RateK.IsNull():
			return structureError(filename,
				"%s states candidate_count %d but a non-null rate_k; the rate arithmetic is not evaluated below two candidates",
				prefix, v.CandidateCount.Uint64())
		case !shortCircuit && v.RateK.IsNull():
			return structureError(filename,
				"%s states candidate_count %d but a null rate_k; the rate arithmetic is evaluated at two candidates or more",
				prefix, v.CandidateCount.Uint64())
		}
	}

	if err := p.EndToEnd.validate(filename); err != nil {
		return err
	}

	if err := requireNonEmptySlice(filename, ProposerResolutionSection, len(p.ProposerResolution)); err != nil {
		return err
	}
	for i, v := range p.ProposerResolution {
		prefix := fmt.Sprintf("%s[%d]", ProposerResolutionSection, i)
		if err := firstError(
			requireText(filename, prefix+".name", v.Name),
			requireSet(filename, prefix+".slot_id", v.SlotID),
			requireSet(filename, prefix+".validator_update_emitted_height_u", v.ValidatorUpdateEmittedHeightU),
			requireText(filename, prefix+".old_consensus_address_hex", v.OldConsensusAddressHex),
			requireText(filename, prefix+".new_consensus_address_hex", v.NewConsensusAddressHex),
			requireSet(filename, prefix+".old_valid_until_height_exclusive", v.OldValidUntilHeightExclusive),
			requireSet(filename, prefix+".new_valid_from_height", v.NewValidFromHeight),
			requireNonEmptySlice(filename, prefix+".assertions", len(v.Assertions)),
			requireText(filename, prefix+".note", v.Note),
		); err != nil {
			return err
		}
		for j, a := range v.Assertions {
			assertion := fmt.Sprintf("%s.assertions[%d]", prefix, j)
			if err := firstError(
				requireSet(filename, assertion+".height", a.Height),
				requireText(filename, assertion+".consensus_address_hex", a.ConsensusAddressHex),
				requireSet(filename, assertion+".expected_slot_id", a.ExpectedSlotID),
			); err != nil {
				return err
			}
		}
	}

	if err := requireNonEmptySlice(filename, "timing_vectors", len(p.TimingVectors)); err != nil {
		return err
	}
	for i, v := range p.TimingVectors {
		if err := v.validate(filename, fmt.Sprintf("timing_vectors[%d]", i)); err != nil {
			return err
		}
	}

	if err := requireNonEmptySlice(filename, "negative_vectors", len(p.NegativeVectors)); err != nil {
		return err
	}
	for i, v := range p.NegativeVectors {
		if err := v.validate(filename, fmt.Sprintf("negative_vectors[%d]", i)); err != nil {
			return err
		}
	}

	if err := requireNonEmptySlice(filename, "comparator_vectors", len(p.ComparatorVectors)); err != nil {
		return err
	}
	for i, v := range p.ComparatorVectors {
		prefix := fmt.Sprintf("comparator_vectors[%d]", i)
		if err := firstError(
			requireText(filename, prefix+".name", v.Name),
			requireHex32(filename, prefix+".synthetic_ticket_hex", v.SyntheticTicketHex),
			requireNonEmptySlice(filename, prefix+".candidates", len(v.Candidates)),
			requireNonEmptySlice(filename, prefix+".expected_order_draw_ids_hex", len(v.ExpectedOrderDrawIDHex)),
			requireText(filename, prefix+".note", v.Note),
		); err != nil {
			return err
		}
		for j, c := range v.Candidates {
			candidate := fmt.Sprintf("%s.candidates[%d]", prefix, j)
			if err := firstError(
				requireHex32(filename, candidate+".draw_id_hex", c.DrawIDHex),
				requireHex32(filename, candidate+".ticket_hex", c.TicketHex),
			); err != nil {
				return err
			}
		}
	}

	return p.EmptySetCrossCheck.validate(filename)
}

func (p DrawPrimitives) validate(filename string) error {
	drawID := p.DrawIDV1
	if err := firstError(
		requireContext(filename, "primitives.draw_id_v1", drawID.ChainID, drawID.SlotID, drawID.TargetEpoch),
		requireHex32(filename, "primitives.draw_id_v1.participation_secret_hex", drawID.ParticipationSecretHex),
		requireHex32(filename, "primitives.draw_id_v1.expected_draw_id_hex", drawID.ExpectedDrawIDHex),
	); err != nil {
		return err
	}

	for _, v := range []struct {
		prefix string
		value  CandidateSetHashVector
	}{
		{"primitives.candidate_set_hash_v1", p.CandidateSetHashV1},
		{"primitives.candidate_set_hash_empty_v1", p.CandidateSetHashEmptyV1},
	} {
		if err := firstError(
			requireContext(filename, v.prefix, v.value.ChainID, v.value.SlotID, v.value.TargetEpoch),
			requireHex32(filename, v.prefix+".expected_candidate_set_hash_hex", v.value.ExpectedCandidateSetHashHex),
		); err != nil {
			return err
		}
		// An empty list is legitimate here; an ABSENT list is not, and the two are
		// distinguishable only by nil.
		if v.value.DrawIDsHex == nil {
			return structureError(filename, "%s.draw_ids_hex is missing", v.prefix)
		}
		for i, id := range v.value.DrawIDsHex {
			if err := requireHex32(filename, fmt.Sprintf("%s.draw_ids_hex[%d]", v.prefix, i), id); err != nil {
				return err
			}
		}
	}

	beacon := p.BeaconHashV1
	if err := firstError(
		requireContext(filename, "primitives.beacon_hash_v1", beacon.ChainID, beacon.SlotID, beacon.TargetEpoch),
		requireHex32(filename, "primitives.beacon_hash_v1.candidate_set_hash_hex", beacon.CandidateSetHashHex),
		requireSet(filename, "primitives.beacon_hash_v1.beacon_start_height", beacon.BeaconStartHeight),
		requireSet(filename, "primitives.beacon_hash_v1.beacon_end_height", beacon.BeaconEndHeight),
		requireNonEmptySlice(filename, "primitives.beacon_hash_v1.included_entries", len(beacon.IncludedEntries)),
		requireHex32(filename, "primitives.beacon_hash_v1.expected_beacon_hash_hex", beacon.ExpectedBeaconHashHex),
		requireBoolSet(filename, "primitives.beacon_hash_v1.committed_height_intentionally_absent", beacon.CommittedHeightIntentionallyNo),
	); err != nil {
		return err
	}
	for i, e := range beacon.IncludedEntries {
		if err := validateBeaconEntry(filename, fmt.Sprintf("primitives.beacon_hash_v1.included_entries[%d]", i), e); err != nil {
			return err
		}
	}

	ticket := p.TicketV1
	return firstError(
		requireContext(filename, "primitives.ticket_v1", ticket.ChainID, ticket.SlotID, ticket.TargetEpoch),
		requireHex32(filename, "primitives.ticket_v1.candidate_set_hash_hex", ticket.CandidateSetHashHex),
		requireHex32(filename, "primitives.ticket_v1.beacon_hash_hex", ticket.BeaconHashHex),
		requireHex32(filename, "primitives.ticket_v1.draw_id_hex", ticket.DrawIDHex),
		requireHex32(filename, "primitives.ticket_v1.expected_ticket_hex", ticket.ExpectedTicketHex),
	)
}

func validateBeaconEntry(filename, prefix string, e BeaconEntry) error {
	return firstError(
		requireSet(filename, prefix+".height", e.Height),
		requireSet(filename, prefix+".proposer_slot_id", e.ProposerSlotID),
		requireHex32(filename, prefix+".block_hash_hex", e.BlockHashHex),
	)
}

// validate checks the draw params of an end-to-end case. The invalid-beacon
// cases never reach the count arithmetic and state no protocol maximum, so that
// field is required only where the case actually selects participants.
func (p DrawParams) validate(filename, prefix string, requireProtocolMax bool) error {
	if err := firstError(
		requireSet(filename, prefix+".beacon_start_offset_blocks", p.BeaconStartOffsetBlocks),
		requireSet(filename, prefix+".beacon_window_blocks", p.BeaconWindowBlocks),
		requireSet(filename, prefix+".min_external_beacon_blocks", p.MinExternalBeaconBlocks),
		requireSet(filename, prefix+".min_distinct_external_proposers", p.MinDistinctExternalProposers),
	); err != nil {
		return err
	}
	if requireProtocolMax {
		return requireSet(filename, prefix+".protocol_max_winners_per_draw", p.ProtocolMaxWinnersPerDraw)
	}
	return nil
}

func (e DrawEndToEnd) validate(filename string) error {
	success := e.SuccessWithTargetSlotExclusion
	const successPrefix = "end_to_end.success_with_target_slot_exclusion"
	if err := firstError(
		requireContext(filename, successPrefix, success.ChainID, success.SlotID, success.TargetEpoch),
		requireSet(filename, successPrefix+".epoch_n_minus_1_start_height", success.EpochNMinus1StartHeight),
		requireSet(filename, successPrefix+".epoch_n_start_height", success.EpochNStartHeight),
		requireSet(filename, successPrefix+".draw_commitment.candidate_count", success.DrawCommitment.CandidateCount),
		requireHex32(filename, successPrefix+".draw_commitment.candidate_set_hash_hex", success.DrawCommitment.CandidateSetHashHex),
		requireSet(filename, successPrefix+".draw_commitment.committed_height", success.DrawCommitment.CommittedHeight),
		requireSet(filename, successPrefix+".draw_commitment.selection_rate_bps", success.DrawCommitment.SelectionRateBps),
		requireSet(filename, successPrefix+".draw_commitment.slot_max_winners", success.DrawCommitment.SlotMaxWinners),
		success.DrawParams.validate(filename, successPrefix+".draw_params", true),
		requireNonEmptySlice(filename, successPrefix+".candidate_list_draw_ids_hex", len(success.CandidateListDrawIDsHex)),
		requireNonEmptySlice(filename, successPrefix+".observed_beacon_window", len(success.ObservedBeaconWindow)),
		requireNonEmptySlice(filename, successPrefix+".expected_included_entries", len(success.ExpectedIncludedEntries)),
		requireSet(filename, successPrefix+".expected_usable_block_count", success.ExpectedUsableBlockCount),
		requireSet(filename, successPrefix+".expected_distinct_external_proposers", success.ExpectedDistinctExternalProposers),
		requireHex32(filename, successPrefix+".expected_beacon_hash_hex", success.ExpectedBeaconHashHex),
		requireSet(filename, successPrefix+".expected_k", success.ExpectedK),
		requireNonEmptySlice(filename, successPrefix+".expected_ranking", len(success.ExpectedRanking)),
		requireNonEmptySlice(filename, successPrefix+".expected_winner_draw_ids_hex", len(success.ExpectedWinnerDrawIDsHex)),
		requireText(filename, successPrefix+".expected_outcome", success.ExpectedOutcome),
	); err != nil {
		return err
	}
	for i, block := range success.ObservedBeaconWindow {
		if err := validateObservedBlock(filename, fmt.Sprintf("%s.observed_beacon_window[%d]", successPrefix, i), block); err != nil {
			return err
		}
	}
	for i, entry := range success.ExpectedIncludedEntries {
		if err := validateBeaconEntry(filename, fmt.Sprintf("%s.expected_included_entries[%d]", successPrefix, i), entry); err != nil {
			return err
		}
	}
	for i, ranked := range success.ExpectedRanking {
		prefix := fmt.Sprintf("%s.expected_ranking[%d]", successPrefix, i)
		if err := firstError(
			requireHex32(filename, prefix+".draw_id_hex", ranked.DrawIDHex),
			requireHex32(filename, prefix+".ticket_hex", ranked.TicketHex),
		); err != nil {
			return err
		}
	}

	invariance := e.CommitmentHeightInvariance
	const invariancePrefix = "end_to_end.commitment_height_invariance"
	if err := firstError(
		requireContext(filename, invariancePrefix, invariance.ChainID, invariance.SlotID, invariance.TargetEpoch),
		requireSet(filename, invariancePrefix+".epoch_n_minus_1_start_height", invariance.EpochNMinus1StartHeight),
		requireSet(filename, invariancePrefix+".beacon_start_height", invariance.BeaconStartHeight),
		requireNonEmptySlice(filename, invariancePrefix+".commitment_heights", len(invariance.CommitmentHeights)),
		requireHex32(filename, invariancePrefix+".expected_same_beacon_hash_hex", invariance.ExpectedSameBeaconHashHex),
		requireNonEmptySlice(filename, invariancePrefix+".expected_same_winner_draw_ids_hex", len(invariance.ExpectedSameWinnerDrawIDsHex)),
		requireText(filename, invariancePrefix+".assertion", invariance.Assertion),
	); err != nil {
		return err
	}

	for _, tc := range []struct {
		prefix string
		value  SmallCandidateCase
	}{
		{"end_to_end.no_candidates", e.NoCandidates},
		{"end_to_end.single_candidate", e.SingleCandidate},
	} {
		if err := firstError(
			requireContext(filename, tc.prefix, tc.value.ChainID, tc.value.SlotID, tc.value.TargetEpoch),
			requireHex32(filename, tc.prefix+".expected_candidate_set_hash_hex", tc.value.ExpectedCandidateSetHashHex),
			requireSet(filename, tc.prefix+".expected_k", tc.value.ExpectedK),
			requireBoolSet(filename, tc.prefix+".beacon_required", tc.value.BeaconRequired),
		); err != nil {
			return err
		}
		// Both lists are legitimately empty on the zero-candidate path, so absence
		// is what has to be rejected, not emptiness.
		if tc.value.CandidateListDrawIDsHex == nil {
			return structureError(filename, "%s.candidate_list_draw_ids_hex is missing", tc.prefix)
		}
		if tc.value.ExpectedWinnerDrawIDsHex == nil {
			return structureError(filename, "%s.expected_winner_draw_ids_hex is missing", tc.prefix)
		}
	}

	for _, tc := range []struct {
		prefix string
		value  InvalidBeaconCase
	}{
		{"end_to_end.no_valid_beacon_insufficient_usable_blocks", e.NoValidBeaconInsufficientUsable},
		{"end_to_end.no_valid_beacon_insufficient_distinct_proposers", e.NoValidBeaconInsufficientDistinct},
	} {
		if err := firstError(
			requireContext(filename, tc.prefix, tc.value.ChainID, tc.value.SlotID, tc.value.TargetEpoch),
			tc.value.DrawParams.validate(filename, tc.prefix+".draw_params", false),
			requireNonEmptySlice(filename, tc.prefix+".observed_beacon_window", len(tc.value.ObservedBeaconWindow)),
			requireSet(filename, tc.prefix+".expected_usable_block_count", tc.value.ExpectedUsableBlockCount),
			requireSet(filename, tc.prefix+".expected_distinct_external_proposers", tc.value.ExpectedDistinctExternalProposers),
			requireBoolSet(filename, tc.prefix+".beacon_hash_defined", tc.value.BeaconHashDefined),
		); err != nil {
			return err
		}
		for i, block := range tc.value.ObservedBeaconWindow {
			if err := validateObservedBlock(filename, fmt.Sprintf("%s.observed_beacon_window[%d]", tc.prefix, i), block); err != nil {
				return err
			}
		}
	}

	return nil
}

func validateObservedBlock(filename, prefix string, b ObservedBlock) error {
	return firstError(
		requireSet(filename, prefix+".height", b.Height),
		requireSet(filename, prefix+".resolved_proposer_slot_id", b.ResolvedProposerSlotID),
		requireHex32(filename, prefix+".block_hash_hex", b.BlockHashHex),
	)
}

// validate checks a timing vector against the schema of its own rule.
//
// Timing vectors are variant-specific by design: a case about the commitment
// window states the epoch anchor and the beacon geometry, while a case about a
// late result states only the target-epoch start and the publication height.
// Requiring the union would reject the pack; requiring only name and expected
// would let a case lose the very field it exists to constrain.
//
// An unrecognized name is a hard failure rather than a fall-through to the
// weakest schema, because a new case admitted without a schema would be checked
// by nothing.
func (v TimingVector) validate(filename, prefix string) error {
	if err := firstError(
		requireText(filename, prefix+".name", v.Name),
		requireText(filename, prefix+".expected", v.Expected),
	); err != nil {
		return err
	}

	commitmentWindow := func() error {
		return firstError(
			requireSet(filename, prefix+".epoch_n_minus_1_start_height", v.EpochNMinus1StartHeight),
			requireSet(filename, prefix+".epoch_n_start_height", v.EpochNStartHeight),
			requireSet(filename, prefix+".beacon_start_offset_blocks", v.BeaconStartOffsetBlocks),
			requireSet(filename, prefix+".beacon_window_blocks", v.BeaconWindowBlocks),
			requireSet(filename, prefix+".committed_height", v.CommittedHeight),
		)
	}
	publication := func() error {
		return firstError(
			requireSet(filename, prefix+".target_epoch_start_height", v.TargetEpochStartHeight),
			requireSet(filename, prefix+".published_height", v.PublishedHeight),
		)
	}

	switch v.Name {
	case "valid_commit_first_block", "valid_commit_last_pre_beacon_block", "commit_at_beacon_start_rejected":
		return commitmentWindow()

	case "beacon_fit_rejection":
		// This case states no committed height: it rejects on geometry alone.
		return firstError(
			requireSet(filename, prefix+".epoch_n_minus_1_start_height", v.EpochNMinus1StartHeight),
			requireSet(filename, prefix+".epoch_n_start_height", v.EpochNStartHeight),
			requireSet(filename, prefix+".beacon_start_offset_blocks", v.BeaconStartOffsetBlocks),
			requireSet(filename, prefix+".beacon_window_blocks", v.BeaconWindowBlocks),
			requireSet(filename, prefix+".derived_beacon_start_height", v.DerivedBeaconStartHeight),
			requireSet(filename, prefix+".derived_beacon_end_height", v.DerivedBeaconEndHeight),
			requireSet(filename, prefix+".latest_permitted_beacon_end_height", v.LatestPermittedBeaconEndHeight),
		)

	case "late_result_rejection", "valid_result_last_block":
		return publication()

	default:
		return structureError(filename,
			"%s names timing vector %q, which has no schema; a new case needs one before it can be validated",
			prefix, v.Name)
	}
}

// validate checks a negative vector against the schema of its own rejection case.
//
// Presence is all that is checked on the payload fields, deliberately. These
// vectors carry deliberately malformed input — one of them states a draw ID that
// is NOT 32 bytes, because that is the condition under test — so applying the
// semantic HEX32 rule here would reject the pack for containing exactly what it
// is supposed to contain. Structural validation asks whether the payload is
// present; the conformance test asks what the implementation does with it.
//
// An unrecognized name is a hard failure, for the same reason as above.
func (v DrawNegativeVector) validate(filename, prefix string) error {
	if err := firstError(
		requireText(filename, prefix+".name", v.Name),
		requireText(filename, prefix+".expected_error", v.ExpectedError),
	); err != nil {
		return err
	}

	switch v.Name {
	case "candidate_list_not_strictly_sorted":
		return requireNonEmptySlice(filename, prefix+".candidate_list_draw_ids_hex", len(v.CandidateListDrawIDsHex))

	case "candidate_set_hash_mismatch":
		return firstError(
			requireText(filename, prefix+".expected_candidate_set_hash_hex", v.ExpectedCandidateSetHashHex),
			requireText(filename, prefix+".published_candidate_set_hash_hex", v.PublishedCandidateSetHash),
		)

	case "candidate_count_mismatch":
		return firstError(
			requireSet(filename, prefix+".draw_commitment_candidate_count", v.DrawCommitmentCandidateCnt),
			requireSet(filename, prefix+".candidate_list_candidate_count", v.CandidateListCandidateCount),
			requireSet(filename, prefix+".candidate_list_length", v.CandidateListLength),
		)

	case "wrong_published_winner":
		return firstError(
			requireNonEmptySlice(filename, prefix+".expected_winner_draw_ids_hex", len(v.ExpectedWinnerDrawIDsHex)),
			requireNonEmptySlice(filename, prefix+".published_winner_draw_ids_hex", len(v.PublishedWinnerDrawIDsHex)),
		)

	case "wrong_beacon_hash":
		return firstError(
			requireText(filename, prefix+".expected_beacon_hash_hex", v.ExpectedBeaconHashHex),
			requireText(filename, prefix+".published_beacon_hash_hex", v.PublishedBeaconHashHex),
		)

	case "duplicate_winner", "non_32_byte_winner", "winner_count_list_length_mismatch":
		// non_32_byte_winner deliberately carries a draw ID of the wrong length;
		// only its presence is required here.
		return firstError(
			requireSet(filename, prefix+".winner_count", v.WinnerCount),
			requireNonEmptySlice(filename, prefix+".winner_draw_ids_hex", len(v.WinnerDrawIDsHex)),
		)

	case "draw_result_key_mismatch":
		return firstError(
			requireSet(filename, prefix+".commitment_slot_id", v.CommitmentSlotID),
			requireSet(filename, prefix+".commitment_target_epoch", v.CommitmentTargetEpoch),
			requireSet(filename, prefix+".result_slot_id", v.ResultSlotID),
			requireSet(filename, prefix+".result_target_epoch", v.ResultTargetEpoch),
		)

	default:
		return structureError(filename,
			"%s names negative vector %q, which has no schema; a new case needs one before it can be validated",
			prefix, v.Name)
	}
}

func (c EmptySetCrossCheck) validate(filename string) error {
	const prefix = "empty_set_cross_check"
	if err := firstError(
		requireContext(filename, prefix+".input_a", c.InputA.ChainID, c.InputA.SlotID, c.InputA.TargetEpoch),
		requireContext(filename, prefix+".input_b", c.InputB.ChainID, c.InputB.SlotID, c.InputB.TargetEpoch),
		requireHex32(filename, prefix+".expected_hash_a_hex", c.ExpectedHashAHex),
		requireHex32(filename, prefix+".expected_hash_b_hex", c.ExpectedHashBHex),
		requireText(filename, prefix+".note", c.Note),
		requireBoolSet(filename, prefix+".expected_equal", c.ExpectedEqual),
	); err != nil {
		return err
	}
	if c.InputA.DrawIDsHex == nil {
		return structureError(filename, "%s.input_a.draw_ids_hex is missing", prefix)
	}
	if c.InputB.DrawIDsHex == nil {
		return structureError(filename, "%s.input_b.draw_ids_hex is missing", prefix)
	}
	return nil
}
