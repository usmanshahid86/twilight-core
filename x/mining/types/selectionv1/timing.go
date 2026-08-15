package selectionv1

import (
	"fmt"

	"github.com/twilight-project/twilight-core/internal/checked"
)

// The height rules governing when a Selection may be committed and when its
// result may be published. Each rule is a separate function so a caller — and a
// conformance vector — exercises exactly one rule at a time and gets exactly one
// rejection reason back.

// ValidateCommitmentHeight enforces r6 §30.2:
//
//	EpochStartHeight(N-1) <= committed_height < beacon_start_height
//
// A commitment at or after the beacon start would be made when the first block
// hash feeding the beacon is already knowable, which would break the
// candidate-before-randomness invariant.
func ValidateCommitmentHeight(committedHeight, epochNMinus1StartHeight, beaconStartHeight uint64) error {
	if committedHeight < epochNMinus1StartHeight {
		return fmt.Errorf(
			"%w: committed height %d precedes epoch N-1 start %d",
			ErrCommitmentWindow, committedHeight, epochNMinus1StartHeight,
		)
	}
	if committedHeight >= beaconStartHeight {
		return fmt.Errorf(
			"%w: committed height %d is at or after beacon start %d",
			ErrCommitmentWindow, committedHeight, beaconStartHeight,
		)
	}
	return nil
}

// ValidateBeaconWindowFits enforces r6 §30.2:
//
//	beacon_end_height <= EpochStartHeight(N) - 2
//
// so at least one block of epoch N-1 remains after the window for the result to
// be published in. A target epoch starting at height 0 or 1 leaves no such room
// and is rejected rather than allowed to underflow.
func ValidateBeaconWindowFits(beaconEndHeight, epochNStartHeight uint64) error {
	latestPermitted, err := checked.SubUint64(epochNStartHeight, 2)
	if err != nil {
		return fmt.Errorf(
			"%w: target epoch start %d leaves no publication block after a beacon window: %v",
			ErrBeaconWindowDoesNotFit, epochNStartHeight, err,
		)
	}
	if beaconEndHeight > latestPermitted {
		return fmt.Errorf(
			"%w: beacon end height %d exceeds latest permitted %d for target epoch start %d",
			ErrBeaconWindowDoesNotFit, beaconEndHeight, latestPermitted, epochNStartHeight,
		)
	}
	return nil
}

// ValidateResultPublishedBeforeTargetEpoch enforces the upper bound of r6 §47:
//
//	published_height < EpochStartHeight(N)
//
// A result published once the target epoch has begun is rejected outright; V1
// has no late-result path and does not synthesize one.
func ValidateResultPublishedBeforeTargetEpoch(publishedHeight, epochNStartHeight uint64) error {
	if publishedHeight >= epochNStartHeight {
		return fmt.Errorf(
			"%w: published height %d is at or after target epoch start %d",
			ErrLateResult, publishedHeight, epochNStartHeight,
		)
	}
	return nil
}
