package selectionv1

import (
	"fmt"

	"github.com/twilight-project/twilight-core/app/params"
	"github.com/twilight-project/twilight-core/internal/checked"
)

// CountLimits carries the three inputs that bound the selected-participant
// count, from their two different sources:
//
//	SelectionRateBps    — the Slot's SelectionPolicy.selection_rate_bps
//	SlotMaxSelected     — the Slot's SelectionPolicy.max_selected_participants
//	ProtocolMaxSelected — SelectionParams.max_selected_participants_per_selection
type CountLimits struct {
	SelectionRateBps    uint64
	SlotMaxSelected     uint64
	ProtocolMaxSelected uint64
}

// SelectedCount implements the deterministic count K (r6 §16):
//
//	if C == 0: K = 0
//	if C == 1: K = 1
//	if C >= 2:
//	    q      = C / 10_000
//	    rem    = C % 10_000
//	    rate_k = q*r + floor((rem*r)/10_000)
//	    K      = max(1, rate_k)
//	    K      = min(K, M, P, floor(C/2))
//
// The decomposition computes floor((C*r)/10_000) without ever forming the
// product C*r, which would overflow uint64 for large candidate counts. With
// r bounded by 5000 the decomposition is safe for every uint64 candidate count;
// the arithmetic is nonetheless performed with checked helpers, so a future
// bound change cannot silently reintroduce a wrap. Floating point is forbidden.
//
// The single-candidate case yields K = 1 unconditionally, before any cap is
// applied — that is what the specification states, and it is what makes the
// one-candidate path independent of randomness.
func SelectedCount(candidateCount uint64, limits CountLimits) (uint64, error) {
	if err := limits.validate(); err != nil {
		return 0, err
	}

	switch candidateCount {
	case 0:
		return 0, nil
	case 1:
		return 1, nil
	}

	const denominator = params.BasisPointsDenominator

	q := candidateCount / denominator
	rem := candidateCount % denominator

	whole, err := checked.MulUint64(q, limits.SelectionRateBps)
	if err != nil {
		return 0, fmt.Errorf("%w: quotient term q*r overflows: %v", ErrInvalidParams, err)
	}
	partial, err := checked.MulUint64(rem, limits.SelectionRateBps)
	if err != nil {
		return 0, fmt.Errorf("%w: remainder term rem*r overflows: %v", ErrInvalidParams, err)
	}
	rateK, err := checked.AddUint64(whole, partial/denominator)
	if err != nil {
		return 0, fmt.Errorf("%w: rate_k overflows: %v", ErrInvalidParams, err)
	}

	k := rateK
	if k < 1 {
		k = 1
	}
	// The floor(C/2) cap is immutable: no more than half a candidate set may be
	// selected, whatever the configured rate and maxima permit.
	for _, limit := range []uint64{limits.SlotMaxSelected, limits.ProtocolMaxSelected, candidateCount / 2} {
		if limit < k {
			k = limit
		}
	}
	return k, nil
}

// validate enforces the positivity the protocol requires of all three limits
// (r6 §27) and the immutable ceiling on the selection rate (r6 §27, §39 of the
// chain architecture), expressed by the shared bound registry.
func (l CountLimits) validate() error {
	if l.SelectionRateBps == 0 || l.SelectionRateBps > params.AbsoluteMaxSelectionRateBps {
		return fmt.Errorf(
			"%w: selection rate is %d bps, must be in (0, %d]",
			ErrInvalidParams, l.SelectionRateBps, params.AbsoluteMaxSelectionRateBps,
		)
	}
	if l.SlotMaxSelected == 0 {
		return fmt.Errorf("%w: slot max selected participants must be positive", ErrInvalidParams)
	}
	if l.ProtocolMaxSelected == 0 {
		return fmt.Errorf("%w: protocol max selected participants must be positive", ErrInvalidParams)
	}
	return nil
}
