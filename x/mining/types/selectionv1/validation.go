package selectionv1

import (
	"fmt"
)

// Structural validation of a published Selection result against its commitment
// (r6 §28, §56). These are the checks a consensus message handler performs, kept
// as separate functions so each rejection reason is reached by exactly one rule.

// ValidateSelectionKey enforces that a result addresses the same Selection as
// its commitment. A result carrying a different Slot or target epoch is not a
// late or malformed result — it is a result for a different Selection.
func ValidateSelectionKey(commitment, result SelectionContext) error {
	if !commitment.Equal(result) {
		return fmt.Errorf(
			"%w: commitment %s, result %s",
			ErrSelectionKeyMismatch, commitment, result,
		)
	}
	return nil
}

// ValidateCandidateCount enforces that the committed candidate count equals the
// published candidate list length. The count is committed on chain while the
// list is served off chain, so the two must be reconciled explicitly.
func ValidateCandidateCount(committedCount uint64, drawIDs []DrawID) error {
	if committedCount != uint64(len(drawIDs)) {
		return fmt.Errorf(
			"%w: commitment declares %d candidates, list holds %d",
			ErrCandidateCountMismatch, committedCount, len(drawIDs),
		)
	}
	return nil
}

// VerifyCandidateSetHash recomputes CandidateSetHashV1 from the published
// candidate list and compares it with the committed digest. Recomputation is the
// point: comparing two supplied digests would certify nothing about the list.
func VerifyCandidateSetHash(sc SelectionContext, committedHash Hash, drawIDs []DrawID) error {
	computed, err := ComputeCandidateSetHash(sc, drawIDs)
	if err != nil {
		return err
	}
	if computed != committedHash {
		return fmt.Errorf(
			"%w: commitment holds %s, candidate list yields %s",
			ErrCandidateSetHashMismatch, committedHash, computed,
		)
	}
	return nil
}

// VerifyBeaconHash compares a published beacon hash with the independently
// derived one.
func VerifyBeaconHash(derived, published Hash) error {
	if derived != published {
		return fmt.Errorf(
			"%w: derived %s, published %s",
			ErrBeaconHashMismatch, derived, published,
		)
	}
	return nil
}

// ValidateSelectedList enforces the self-consistency of a published selected
// list: the declared count matches the list length, and no draw ID repeats.
//
// A repeated ID would let one participant occupy several selected places, so
// duplicate rejection is an economic rule and not merely a hygiene check. The
// duplicate set is used for membership only and is never iterated, so nothing
// here depends on Go's map ordering.
func ValidateSelectedList(declaredCount uint64, selected []DrawID) error {
	if declaredCount != uint64(len(selected)) {
		return fmt.Errorf(
			"%w: result declares %d selected participants, list holds %d",
			ErrSelectedCountMismatch, declaredCount, len(selected),
		)
	}
	seen := make(map[DrawID]struct{}, len(selected))
	for i, id := range selected {
		if _, duplicate := seen[id]; duplicate {
			return fmt.Errorf("%w: %s repeats at position %d", ErrDuplicateSelectedID, id, i)
		}
		seen[id] = struct{}{}
	}
	return nil
}

// VerifySelectedDrawIDs compares a published selected list with the
// independently derived one, in order. Order is significant: the list carried by
// MsgPublishSelectionResult must be in ranking order, and SelectedDrawIDsHashV1
// commits to that order.
func VerifySelectedDrawIDs(derived, published []DrawID) error {
	if len(derived) != len(published) {
		return fmt.Errorf(
			"%w: derived %d selected participants, published %d",
			ErrSelectedSetMismatch, len(derived), len(published),
		)
	}
	for i := range derived {
		if derived[i] != published[i] {
			return fmt.Errorf(
				"%w: position %d derived %s, published %s",
				ErrSelectedSetMismatch, i, derived[i], published[i],
			)
		}
	}
	return nil
}
