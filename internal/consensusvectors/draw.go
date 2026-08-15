package consensusvectors

import _ "embed"

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
	Version              int                  `json:"version"`
	Revision             int                  `json:"revision"`
	Normative            bool                 `json:"normative"`
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
	IndependentCheckRequire bool   `json:"independent_check_required"`
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
	CommittedHeightIntentionallyNo bool          `json:"committed_height_intentionally_absent"`
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
	CandidateCount            U64  `json:"candidate_count"`
	SelectionRateBps          U64  `json:"selection_rate_bps"`
	SlotMaxWinners            U64  `json:"slot_max_winners"`
	ProtocolMaxWinnersPerDraw U64  `json:"protocol_max_winners_per_draw"`
	QuotientQ                 U64  `json:"quotient_q"`
	RemainderRem              U64  `json:"remainder_rem"`
	RateK                     *U64 `json:"rate_k"`
	ExpectedK                 U64  `json:"expected_k"`
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
	BeaconRequired              bool     `json:"beacon_required"`
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
	BeaconHashDefined                 bool            `json:"beacon_hash_defined"`
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
	ExpectedEqual    bool                   `json:"expected_equal"`
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

// LoadDrawPack returns the r2 draw pack, verifying its declared identity.
func LoadDrawPack() (DrawPack, error) {
	var pack DrawPack
	if err := decodePack(DrawPackFilename, drawPackBytes, &pack); err != nil {
		return DrawPack{}, err
	}
	if err := assertMetadata(
		DrawPackFilename,
		pack.Format, drawPackArtifact,
		pack.Version, drawPackVersion,
		pack.Revision, drawPackRevision,
		pack.Normative,
	); err != nil {
		return DrawPack{}, err
	}
	return pack, nil
}
