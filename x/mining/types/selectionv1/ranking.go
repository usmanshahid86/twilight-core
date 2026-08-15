package selectionv1

import (
	"bytes"
	"slices"
)

// RankedCandidate pairs a candidate draw ID with its derived ticket.
type RankedCandidate struct {
	DrawID DrawID
	Ticket Hash
}

// ComputeTickets derives one ticket per candidate (r6 §34), preserving the
// order supplied. The returned slice is freshly allocated.
func ComputeTickets(
	sc SelectionContext,
	candidateSetHash, beaconHash Hash,
	drawIDs []DrawID,
) ([]RankedCandidate, error) {
	candidates := make([]RankedCandidate, 0, len(drawIDs))
	for _, id := range drawIDs {
		ticket, err := ComputeTicket(sc, candidateSetHash, beaconHash, id)
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, RankedCandidate{DrawID: id, Ticket: ticket})
	}
	return candidates, nil
}

// CompareCandidates is the normative ranking comparator (r6 §35):
//
//	(ticket ASC, draw_id ASC)
//
// Both comparisons are raw-byte lexicographic, which for a fixed 32-byte value
// is equivalent to comparing it as one unsigned 256-bit big-endian integer. The
// draw-ID tie-break is normative even though a SHA-256 ticket collision is
// expected to be negligible: without it, two colliding tickets would leave the
// order undefined and two conforming implementations could disagree.
//
// It returns a negative number, zero, or a positive number as a sorts before,
// equal to, or after b. Because draw IDs within one Selection are unique, zero
// occurs only when a and b are the same candidate, so the comparator is a strict
// total order over a canonical candidate set.
func CompareCandidates(a, b RankedCandidate) int {
	if c := bytes.Compare(a.Ticket[:], b.Ticket[:]); c != 0 {
		return c
	}
	return bytes.Compare(a.DrawID[:], b.DrawID[:])
}

// RankCandidates returns the candidates in ranking order. The input slice is
// copied first: a caller's slice is never reordered underneath it, because a
// silent permutation of the caller's candidate list would be an easy way to
// corrupt a candidate-set commitment computed later from the same slice.
func RankCandidates(candidates []RankedCandidate) []RankedCandidate {
	ranked := slices.Clone(candidates)
	slices.SortStableFunc(ranked, CompareCandidates)
	return ranked
}

// SelectFirstK returns the draw IDs of the first k ranked candidates (r6 §35).
// If k exceeds the number of candidates, every candidate is selected; K is
// already capped at floor(C/2) for C >= 2, so that can only arise for the
// short-circuited small-candidate paths.
func SelectFirstK(ranked []RankedCandidate, k uint64) []DrawID {
	if k > uint64(len(ranked)) {
		k = uint64(len(ranked))
	}
	selected := make([]DrawID, 0, k)
	for i := uint64(0); i < k; i++ {
		selected = append(selected, ranked[i].DrawID)
	}
	return selected
}
