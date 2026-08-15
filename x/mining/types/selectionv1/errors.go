package selectionv1

import "errors"

// Sentinel errors. Each one names a distinct rejection reason so a caller — a
// consensus message handler or a public verifier — can branch on the cause
// rather than on error text. Compare with errors.Is.
//
// The names in comments are the rejection codes used by the normative golden
// vector packs, kept here so a vector case can be traced to the code that
// rejects it.
var (
	// ErrInvalidLength reports a value that is not exactly 32 bytes.
	// Vector code: INVALID_WINNER_ID_LENGTH.
	ErrInvalidLength = errors.New("selectionv1: value has wrong byte length")

	// ErrMalformedHex reports transport hexadecimal that does not decode.
	ErrMalformedHex = errors.New("selectionv1: malformed hexadecimal value")

	// ErrNotCanonical reports a value whose encoding is decodable but not in the
	// single permitted canonical form.
	ErrNotCanonical = errors.New("selectionv1: value is not in canonical form")

	// ErrChainIDTooLong reports a chain ID whose UTF-8 form exceeds 65,535 bytes
	// and therefore cannot be length-prefixed by U16BE.
	ErrChainIDTooLong = errors.New("selectionv1: chain id exceeds maximum encodable length")

	// ErrCandidateListNotCanonical reports a candidate list that is not strictly
	// increasing in raw-byte order. Vector code: CANDIDATE_LIST_NOT_CANONICAL.
	ErrCandidateListNotCanonical = errors.New("selectionv1: candidate list is not strictly increasing")

	// ErrCandidateCountMismatch reports a committed candidate count that does not
	// equal the candidate list length. Vector code: CANDIDATE_COUNT_MISMATCH.
	ErrCandidateCountMismatch = errors.New("selectionv1: candidate count does not match candidate list length")

	// ErrCandidateSetHashMismatch reports a published candidate-set hash that does
	// not equal the hash recomputed from the list. Vector code:
	// CANDIDATE_SET_HASH_MISMATCH.
	ErrCandidateSetHashMismatch = errors.New("selectionv1: candidate set hash mismatch")

	// ErrBeaconHashMismatch reports a published beacon hash that does not equal the
	// independently derived one. Vector code: BEACON_HASH_MISMATCH.
	ErrBeaconHashMismatch = errors.New("selectionv1: beacon hash mismatch")

	// ErrSelectedSetMismatch reports a published selected list that does not equal
	// the independently derived one. Vector code: WINNER_SET_MISMATCH.
	ErrSelectedSetMismatch = errors.New("selectionv1: selected participant set mismatch")

	// ErrSelectedCountMismatch reports a declared selected count that does not
	// equal the selected list length. Vector code: WINNER_COUNT_MISMATCH.
	ErrSelectedCountMismatch = errors.New("selectionv1: selected count does not match selected list length")

	// ErrDuplicateSelectedID reports a draw ID appearing more than once in a
	// selected list. Vector code: DUPLICATE_WINNER_ID.
	ErrDuplicateSelectedID = errors.New("selectionv1: duplicate selected draw id")

	// ErrSelectionKeyMismatch reports a result whose (chain, Slot, epoch) key does
	// not match its commitment. Vector code: DRAW_RESULT_KEY_MISMATCH.
	ErrSelectionKeyMismatch = errors.New("selectionv1: selection key mismatch between commitment and result")

	// ErrCommitmentWindow reports a commitment height outside
	// [EpochStartHeight(N-1), beacon_start_height). Vector code:
	// REJECT_COMMITMENT_WINDOW.
	ErrCommitmentWindow = errors.New("selectionv1: commitment height outside permitted window")

	// ErrBeaconWindowDoesNotFit reports a beacon window that does not leave a
	// publication block inside epoch N-1. Vector code:
	// REJECT_DRAW_PARAMS_OR_COMMIT.
	ErrBeaconWindowDoesNotFit = errors.New("selectionv1: beacon window does not fit before target epoch")

	// ErrLateResult reports a result published at or after the target epoch start.
	// Vector code: REJECT_LATE_RESULT.
	ErrLateResult = errors.New("selectionv1: result published at or after target epoch start")

	// ErrResultBeforeEpochStart reports a result published before epoch N-1 begins.
	// It is deliberately distinct from ErrLateResult: a result outside its epoch on
	// the early side is a different fault, and describing it as "late" would send a
	// reader looking for the opposite problem.
	ErrResultBeforeEpochStart = errors.New("selectionv1: result published before epoch N-1 start")

	// ErrResultBeforeBeaconEnd reports a multi-candidate result published at or
	// before the end of its beacon window, which would mean publishing before the
	// randomness it depends on was complete.
	ErrResultBeforeBeaconEnd = errors.New("selectionv1: multi-candidate result published at or before beacon end")

	// ErrInvalidParams reports Selection parameters that violate a protocol
	// relation independently of any particular Selection.
	ErrInvalidParams = errors.New("selectionv1: invalid selection parameters")

	// ErrInvalidBeaconWindow reports an observed window whose entries are not
	// strictly increasing or fall outside the derived height range.
	ErrInvalidBeaconWindow = errors.New("selectionv1: invalid observed beacon window")

	// ErrBeaconUndefined reports an attempt to use a beacon hash for a Selection
	// whose beacon did not satisfy the V1 validity thresholds. BeaconHashV1 is
	// undefined in that case; there is no alternate window and no reroll.
	ErrBeaconUndefined = errors.New("selectionv1: beacon hash is undefined for this selection")
)
