package selectionv1

import (
	"fmt"

	"github.com/twilight-project/twilight-core/internal/checked"
)

// ObservedBlock is one block of the deterministic beacon window whose proposer
// has already been attributed to a Core Slot at that height.
//
// A height whose proposer could not be attributed is UNUSABLE and is expressed
// by omitting it from the observed window entirely; the same applies to a block
// that could not be obtained. Both are unusable for the same reason and produce
// the same result, so the library needs no separate representation for them.
//
// BlockHash is the committed CometBFT BlockID.Hash for the height. AppHash,
// DataHash, LastBlockID.Hash and transaction hashes are not substitutes.
type ObservedBlock struct {
	Height         uint64
	ProposerSlotID uint64
	BlockHash      Hash
}

// BeaconEntry is one block that survived the protocol filters and therefore
// contributes to the beacon preimage. It is a distinct type from ObservedBlock
// so filtered and unfiltered data cannot be confused at a call site.
type BeaconEntry struct {
	Height         uint64
	ProposerSlotID uint64
	BlockHash      Hash
}

// BeaconThresholds carries the V1 beacon validity thresholds taken from the
// immutable SelectionParamsVersion referenced by the commitment.
type BeaconThresholds struct {
	MinExternalBeaconBlocks      uint64
	MinDistinctExternalProposers uint64
}

// BeaconStats summarizes a filtered entry list against the validity thresholds.
type BeaconStats struct {
	UsableCount               uint64
	DistinctExternalProposers uint64
}

// DeriveBeaconWindow implements the epoch-anchored V1 window (r6 §30.2):
//
//	beacon_start_height = EpochStartHeight(N-1) + beacon_start_offset_blocks
//	beacon_end_height   = beacon_start_height + beacon_window_blocks - 1
//
// The commitment height is deliberately not an input: the window is a function
// of the epoch anchor and the parameters alone, so every valid commitment height
// yields the same window. All arithmetic is checked.
func DeriveBeaconWindow(
	epochNMinus1StartHeight, beaconStartOffsetBlocks, beaconWindowBlocks uint64,
) (startHeight, endHeight uint64, err error) {
	if beaconWindowBlocks == 0 {
		return 0, 0, fmt.Errorf("%w: beacon window blocks must be positive", ErrInvalidParams)
	}

	startHeight, err = checked.AddUint64(epochNMinus1StartHeight, beaconStartOffsetBlocks)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: beacon start height: %v", ErrInvalidParams, err)
	}
	endHeight, err = checked.AddUint64(startHeight, beaconWindowBlocks-1)
	if err != nil {
		return 0, 0, fmt.Errorf("%w: beacon end height: %v", ErrInvalidParams, err)
	}
	return startHeight, endHeight, nil
}

// FilterBeaconEntries applies the per-height protocol filters (r6 §31, §32) to
// the observed window, in strictly increasing height order:
//
//   - a height outside [beaconStartHeight, beaconEndHeight] is not part of the
//     deterministic window and is rejected as malformed input;
//   - a height proposed by the target Slot itself is EXCLUDED, so a Slot cannot
//     grind its own proposed blocks to influence its own Selection;
//   - every other height contributes exactly one BeaconEntry.
//
// The returned slice is freshly allocated; the input is not modified.
func FilterBeaconEntries(
	targetSlotID, beaconStartHeight, beaconEndHeight uint64,
	observed []ObservedBlock,
) ([]BeaconEntry, error) {
	if beaconStartHeight > beaconEndHeight {
		return nil, fmt.Errorf(
			"%w: start height %d exceeds end height %d",
			ErrInvalidBeaconWindow, beaconStartHeight, beaconEndHeight,
		)
	}

	entries := make([]BeaconEntry, 0, len(observed))
	for i, block := range observed {
		if block.Height < beaconStartHeight || block.Height > beaconEndHeight {
			return nil, fmt.Errorf(
				"%w: observed height %d is outside window [%d, %d]",
				ErrInvalidBeaconWindow, block.Height, beaconStartHeight, beaconEndHeight,
			)
		}
		if i > 0 && block.Height <= observed[i-1].Height {
			return nil, fmt.Errorf(
				"%w: observed height %d does not follow %d in strictly increasing order",
				ErrInvalidBeaconWindow, block.Height, observed[i-1].Height,
			)
		}
		if block.ProposerSlotID == targetSlotID {
			continue
		}
		// A surviving block becomes an entry unchanged. The conversion is
		// deliberate: the two types are kept distinct so filtered and unfiltered
		// data cannot be confused at a call site, and if their fields ever
		// diverge this stops compiling, which forces an explicit decision about
		// what belongs in the beacon preimage.
		entries = append(entries, BeaconEntry(block))
	}
	return entries, nil
}

// ComputeBeaconStats counts usable entries and distinct external proposers
// (r6 §33).
//
// The distinct count uses a map for membership only and reads its size; the map
// is never iterated, so no result depends on Go's map ordering.
func ComputeBeaconStats(entries []BeaconEntry) BeaconStats {
	seen := make(map[uint64]struct{}, len(entries))
	for _, e := range entries {
		seen[e.ProposerSlotID] = struct{}{}
	}
	return BeaconStats{
		UsableCount:               uint64(len(entries)),
		DistinctExternalProposers: uint64(len(seen)),
	}
}

// Satisfied reports whether a filtered window meets both V1 validity thresholds:
//
//	usable_count      >= min_external_beacon_blocks
//	distinct_external >= min_distinct_external_proposers
//
// If either fails the Selection outcome is NO_VALID_BEACON, BeaconHashV1 is
// undefined, and no alternate window or reroll is permitted.
func (t BeaconThresholds) Satisfied(stats BeaconStats) bool {
	return stats.UsableCount >= t.MinExternalBeaconBlocks &&
		stats.DistinctExternalProposers >= t.MinDistinctExternalProposers
}

// validateEntryOrder enforces the strictly increasing height order that
// BeaconEntryV1 requires (r6 §40). Hashing entries in any other order would
// produce a digest no conforming verifier could reproduce.
func validateEntryOrder(entries []BeaconEntry) error {
	for i := 1; i < len(entries); i++ {
		if entries[i].Height <= entries[i-1].Height {
			return fmt.Errorf(
				"%w: entry height %d does not follow %d in strictly increasing order",
				ErrInvalidBeaconWindow, entries[i].Height, entries[i-1].Height,
			)
		}
	}
	return nil
}
