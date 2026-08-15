package selectionv1

// Outcome is a terminal V1 Selection outcome (r6 §43). The three below are the
// complete set: V1 defines no further lifecycle state, and in particular none
// for cancellation, reroll, expiry or replacement.
type Outcome uint8

const (
	// OutcomeUnspecified is the zero value and is never a valid result.
	OutcomeUnspecified Outcome = iota
	// OutcomeSuccess reports a Selection that produced a selected list.
	OutcomeSuccess
	// OutcomeNoCandidates reports a Selection committed with zero candidates.
	OutcomeNoCandidates
	// OutcomeNoValidBeacon reports a multi-candidate Selection whose deterministic
	// window failed a validity threshold. BeaconHashV1 is undefined and no
	// alternate window or reroll is permitted.
	OutcomeNoValidBeacon
)

// String returns the outcome name used by the protocol and by the normative
// vector packs.
func (o Outcome) String() string {
	switch o {
	case OutcomeSuccess:
		return "SUCCESS"
	case OutcomeNoCandidates:
		return "NO_CANDIDATES"
	case OutcomeNoValidBeacon:
		return "NO_VALID_BEACON"
	default:
		return "UNSPECIFIED"
	}
}

// EvaluationInput is everything needed to derive a Selection outcome from public
// data. It is named for evaluation rather than for the on-chain records, so it
// cannot be confused with the SelectionCommitment and SelectionResult messages.
//
// ObservedWindow carries only heights whose proposer was resolved to a Core Slot;
// see ObservedBlock. The beacon fields are consulted only when the candidate
// count is two or more, because the zero- and one-candidate paths require no
// beacon at all.
type EvaluationInput struct {
	Context          SelectionContext
	CandidateDrawIDs []DrawID

	EpochNMinus1StartHeight uint64
	BeaconStartOffsetBlocks uint64
	BeaconWindowBlocks      uint64
	ObservedWindow          []ObservedBlock
	Thresholds              BeaconThresholds

	Limits CountLimits
}

// EvaluationResult is the derived outcome. Every field is a function of the
// input alone.
//
// SelectedCount is the number of participants actually selected and always
// equals len(SelectedDrawIDs). On the SUCCESS paths it equals K; on
// NO_CANDIDATES and NO_VALID_BEACON it is zero, because no participant is
// selected under either outcome.
type EvaluationResult struct {
	Outcome          Outcome
	CandidateSetHash Hash

	// BeaconHash is meaningful only when BeaconHashDefined is true. It is
	// undefined for the zero- and one-candidate paths, which need no beacon, and
	// for NO_VALID_BEACON, where the protocol states no beacon hash exists.
	BeaconHash        Hash
	BeaconHashDefined bool

	BeaconStartHeight uint64
	BeaconEndHeight   uint64
	IncludedEntries   []BeaconEntry
	Stats             BeaconStats

	Ranking         []RankedCandidate
	SelectedCount   uint64
	SelectedDrawIDs []DrawID
}

// Evaluate derives the complete Selection outcome from public data.
//
// It returns an error only for malformed input. A Selection that legitimately
// yields no selected participants is not an error: NO_CANDIDATES and
// NO_VALID_BEACON are terminal protocol outcomes and are returned as results.
func Evaluate(in EvaluationInput) (EvaluationResult, error) {
	candidateSetHash, err := ComputeCandidateSetHash(in.Context, in.CandidateDrawIDs)
	if err != nil {
		return EvaluationResult{}, err
	}

	result := EvaluationResult{CandidateSetHash: candidateSetHash}
	candidateCount := uint64(len(in.CandidateDrawIDs))

	// Zero candidates (r6 §44): K = 0, no beacon required, empty selected list.
	if candidateCount == 0 {
		result.Outcome = OutcomeNoCandidates
		result.SelectedDrawIDs = []DrawID{}
		return result, nil
	}

	// One candidate (r6 §45): K = 1 and that candidate is selected
	// deterministically. No beacon is required because randomness cannot change
	// the result, so beacon_hash stays empty.
	if candidateCount == 1 {
		result.Outcome = OutcomeSuccess
		result.SelectedCount = 1
		result.SelectedDrawIDs = []DrawID{in.CandidateDrawIDs[0]}
		return result, nil
	}

	// Beacon parameters are validated before anything is derived from them. An
	// unvalidated threshold of zero would let an empty window satisfy the validity
	// predicate and report SUCCESS, so this check has to precede evaluation rather
	// than accompany it.
	if err := ValidateBeaconParams(
		in.BeaconStartOffsetBlocks, in.BeaconWindowBlocks, in.Thresholds,
	); err != nil {
		return EvaluationResult{}, err
	}

	startHeight, endHeight, err := DeriveBeaconWindow(
		in.EpochNMinus1StartHeight, in.BeaconStartOffsetBlocks, in.BeaconWindowBlocks,
	)
	if err != nil {
		return EvaluationResult{}, err
	}
	result.BeaconStartHeight = startHeight
	result.BeaconEndHeight = endHeight

	entries, err := FilterBeaconEntries(in.Context.SlotID, startHeight, endHeight, in.ObservedWindow)
	if err != nil {
		return EvaluationResult{}, err
	}
	result.IncludedEntries = entries
	result.Stats = ComputeBeaconStats(entries)

	// Invalid beacon (r6 §46): terminal outcome, no beacon hash, no selection.
	if !in.Thresholds.Satisfied(result.Stats) {
		result.Outcome = OutcomeNoValidBeacon
		result.SelectedDrawIDs = []DrawID{}
		return result, nil
	}

	beaconHash, err := ComputeBeaconHash(in.Context, candidateSetHash, startHeight, endHeight, entries)
	if err != nil {
		return EvaluationResult{}, err
	}
	result.BeaconHash = beaconHash
	result.BeaconHashDefined = true

	candidates, err := ComputeTickets(in.Context, candidateSetHash, beaconHash, in.CandidateDrawIDs)
	if err != nil {
		return EvaluationResult{}, err
	}
	result.Ranking = RankCandidates(candidates)

	k, err := SelectedCount(candidateCount, in.Limits)
	if err != nil {
		return EvaluationResult{}, err
	}
	result.SelectedDrawIDs = SelectFirstK(result.Ranking, k)
	result.SelectedCount = uint64(len(result.SelectedDrawIDs))
	result.Outcome = OutcomeSuccess

	return result, nil
}
