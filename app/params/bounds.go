package params

// V2 hard bounds and the relations that govern them.
//
// Two kinds of bound live here and must not be conflated.
//
// PROTOCOL-FIXED values and relations are normative. The chain architecture and
// the participant-selection protocol fix them, and an implementation may not
// choose otherwise. They are encoded below as constants and as relation checks.
//
// IMPLEMENTATION-CALIBRATED bounds are required to EXIST by the architecture,
// but their deployment values are not canonically fixed. CalibratedBounds names
// them and validates the relations that hold between them; this file
// deliberately supplies NO values. A number chosen here without a ratified
// source would become de-facto protocol the moment a consensus path read it, so
// callers pass calibrated bounds in explicitly until values are ratified.
//
// Like the rest of this package this file stays dependency-neutral. Its only
// repository import is internal/checked, which itself imports nothing beyond the
// standard library, so no module can create an import cycle through it.

import (
	"fmt"

	"github.com/twilight-project/twilight-core/internal/checked"
)

// ---------------------------------------------------------------------------
// Protocol-fixed values
// ---------------------------------------------------------------------------

const (
	// BasisPointsDenominator is the fixed denominator for every basis-point
	// ratio in the protocol: a share of n bps is n/10_000.
	//
	// x/rewards/types.MaxBasisPoints carries the same value for the existing V1
	// paths. Consolidating the two is deliberately out of scope here, because it
	// would edit existing consensus-adjacent validation.
	BasisPointsDenominator uint64 = 10_000

	// AbsoluteMaxSelectionRateBps is the immutable ceiling on a Slot's selection
	// rate: 5000 bps, half the candidate set. Unlike the calibrated bounds below
	// this is fixed by the protocol rather than chosen at deployment, so it is a
	// constant and not a CalibratedBounds field.
	AbsoluteMaxSelectionRateBps uint64 = 5_000
)

// ---------------------------------------------------------------------------
// Protocol-fixed relations
//
// Each function takes the implementation-calibrated bound it is measured
// against as an argument, so the relation can be enforced today without pinning
// an unratified number.
// ---------------------------------------------------------------------------

// ValidateEmissionTreasuryShareBps enforces
//
//	0 <= shareBps <= hardMaxShareBps < BasisPointsDenominator
//
// The strict upper bound is what guarantees the treasury share can never
// consume the entire emission, leaving the reward pool non-negative.
func ValidateEmissionTreasuryShareBps(shareBps, hardMaxShareBps uint64) error {
	if hardMaxShareBps >= BasisPointsDenominator {
		return fmt.Errorf(
			"hard max emission treasury share is %d bps, must be below %d",
			hardMaxShareBps, BasisPointsDenominator,
		)
	}
	if shareBps > hardMaxShareBps {
		return fmt.Errorf(
			"emission treasury share is %d bps, exceeds hard max %d bps",
			shareBps, hardMaxShareBps,
		)
	}
	return nil
}

// ValidateSelectionRateBps enforces
//
//	0 < rateBps <= operationalMaxBps <= AbsoluteMaxSelectionRateBps
//
// operationalMaxBps is the governance-set maximum; the absolute ceiling is
// immutable and bounds the operational maximum in turn.
func ValidateSelectionRateBps(rateBps, operationalMaxBps uint64) error {
	if operationalMaxBps == 0 || operationalMaxBps > AbsoluteMaxSelectionRateBps {
		return fmt.Errorf(
			"operational max selection rate is %d bps, must be in (0, %d]",
			operationalMaxBps, AbsoluteMaxSelectionRateBps,
		)
	}
	if rateBps == 0 || rateBps > operationalMaxBps {
		return fmt.Errorf(
			"selection rate is %d bps, must be in (0, %d]",
			rateBps, operationalMaxBps,
		)
	}
	return nil
}

// ValidateMaxSelectedParticipants enforces
//
//	0 < value <= hardMax
func ValidateMaxSelectedParticipants(value, hardMax uint64) error {
	return requirePositiveAtMost("max selected participants", value, hardMax)
}

// ValidateMinRecipientPayoutAmount enforces
//
//	value >= hardMin > 0
//
// The positive floor is what stops settlement creating dust payouts, and with
// them cheap accounts, on a feeless chain.
func ValidateMinRecipientPayoutAmount(value, hardMin uint64) error {
	return requirePositiveAtLeast("min recipient payout amount", value, hardMin)
}

// ValidateSelectionPolicyUpdateCooldownBlocks enforces
//
//	value >= hardMin > 0
//
// The cooldown is denominated in block height and is CoreSlot-local, which is
// what keeps policy-update admission from reading epoch state.
func ValidateSelectionPolicyUpdateCooldownBlocks(value, hardMin uint64) error {
	return requirePositiveAtLeast("selection policy update cooldown blocks", value, hardMin)
}

// SelectionParams carries the operational Selection parameters whose
// inequalities the participant-selection protocol defines as a set. They are
// validated together because several of them constrain each other.
type SelectionParams struct {
	MaxSelectionRateBps          uint64
	BeaconStartOffsetBlocks      uint64
	BeaconWindowBlocks           uint64
	MinExternalBeaconBlocks      uint64
	MinDistinctExternalProposers uint64
}

// Validate enforces the protocol's SelectionParams inequalities:
//
//	0 < MaxSelectionRateBps          <= AbsoluteMaxSelectionRateBps
//	0 < BeaconStartOffsetBlocks
//	0 < BeaconWindowBlocks
//	0 < MinExternalBeaconBlocks      <= BeaconWindowBlocks
//	0 < MinDistinctExternalProposers <= MinExternalBeaconBlocks
//	BeaconStartOffsetBlocks + BeaconWindowBlocks + 1 <= hardMinEpochLengthBlocks
//
// The final inequality is what guarantees a beacon window plus at least one
// publication block fits inside the shortest permitted epoch, for every
// permitted epoch length. hardMinEpochLengthBlocks is implementation-calibrated
// and supplied by the caller.
func (p SelectionParams) Validate(hardMinEpochLengthBlocks uint64) error {
	if p.MaxSelectionRateBps == 0 || p.MaxSelectionRateBps > AbsoluteMaxSelectionRateBps {
		return fmt.Errorf(
			"max selection rate is %d bps, must be in (0, %d]",
			p.MaxSelectionRateBps, AbsoluteMaxSelectionRateBps,
		)
	}
	if p.BeaconStartOffsetBlocks == 0 {
		return fmt.Errorf("beacon start offset blocks must be positive")
	}
	if p.BeaconWindowBlocks == 0 {
		return fmt.Errorf("beacon window blocks must be positive")
	}
	if err := requirePositiveAtMost(
		"min external beacon blocks", p.MinExternalBeaconBlocks, p.BeaconWindowBlocks,
	); err != nil {
		return err
	}
	if err := requirePositiveAtMost(
		"min distinct external proposers", p.MinDistinctExternalProposers, p.MinExternalBeaconBlocks,
	); err != nil {
		return err
	}

	if hardMinEpochLengthBlocks == 0 {
		return fmt.Errorf("hard min epoch length blocks must be positive")
	}

	// Checked throughout: the two window components are operator-supplied and
	// their sum must not be allowed to wrap into a small value that would appear
	// to fit inside the epoch.
	span, err := checked.AddUint64(p.BeaconStartOffsetBlocks, p.BeaconWindowBlocks)
	if err != nil {
		return fmt.Errorf("beacon geometry span overflows: %w", err)
	}
	span, err = checked.AddUint64(span, 1)
	if err != nil {
		return fmt.Errorf("beacon geometry span overflows: %w", err)
	}
	if span > hardMinEpochLengthBlocks {
		return fmt.Errorf(
			"beacon geometry needs %d blocks, exceeds hard min epoch length %d",
			span, hardMinEpochLengthBlocks,
		)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Implementation-calibrated bounds
// ---------------------------------------------------------------------------

// CalibratedBounds names every implementation hard bound the V2 architecture
// requires to exist. The architecture fixes that these bounds must be defined
// and enforced; it does not fix their deployment values, which are calibration
// work informed by adversarial load testing.
//
// There is deliberately no default instance and no package-level constants for
// these. Callers supply values explicitly, and Validate enforces the relations
// that hold between them whatever numbers are eventually ratified.
//
// Bounds absent from this struct are absent on purpose:
//
//   - The positive minimums for settlement_window_epochs,
//     max_chunks_per_settlement, max_recipients_per_chunk and
//     max_candidates_per_selection are not currently specified. Note that
//     MaxCandidatesPerSelection below is the ceiling on the candidate set, which
//     is a different bound from the unspecified positive minimum on the
//     configurable max_candidates_per_selection.
//   - No maximum for the initial block subsidy is defined.
//   - The finite block gas and execution budget is a deployment gate rather than
//     a compile-time constant, so it is not represented here.
type CalibratedBounds struct {
	MaxActiveCoreSlots                     uint64
	MinEpochLengthBlocks                   uint64
	MaxEpochLengthBlocks                   uint64
	MaxSelectedParticipants                uint64
	MaxCandidatesPerSelection              uint64
	MaxRecipientsPerChunk                  uint64
	MaxChunksPerSettlement                 uint64
	MinSettlementPayoutAmount              uint64
	MinSelectionPolicyUpdateCooldownBlocks uint64
	MaxEmissionTreasuryShareBps            uint64
	MaxCoreSlotMetadataBytes               uint64
	MaxTxMessageBytes                      uint64
}

// Validate enforces the structural relations every calibrated bound set must
// satisfy regardless of the values chosen: each bound is positive, because a
// zero bound would silently disable the path it governs; the epoch-length window
// is non-empty; and the treasury share ceiling stays strictly below the
// basis-point denominator.
func (b CalibratedBounds) Validate() error {
	// Ordered slice rather than a map: error reporting must be deterministic.
	positive := []struct {
		name  string
		value uint64
	}{
		{"MaxActiveCoreSlots", b.MaxActiveCoreSlots},
		{"MinEpochLengthBlocks", b.MinEpochLengthBlocks},
		{"MaxEpochLengthBlocks", b.MaxEpochLengthBlocks},
		{"MaxSelectedParticipants", b.MaxSelectedParticipants},
		{"MaxCandidatesPerSelection", b.MaxCandidatesPerSelection},
		{"MaxRecipientsPerChunk", b.MaxRecipientsPerChunk},
		{"MaxChunksPerSettlement", b.MaxChunksPerSettlement},
		{"MinSettlementPayoutAmount", b.MinSettlementPayoutAmount},
		{"MinSelectionPolicyUpdateCooldownBlocks", b.MinSelectionPolicyUpdateCooldownBlocks},
		{"MaxEmissionTreasuryShareBps", b.MaxEmissionTreasuryShareBps},
		{"MaxCoreSlotMetadataBytes", b.MaxCoreSlotMetadataBytes},
		{"MaxTxMessageBytes", b.MaxTxMessageBytes},
	}
	for _, field := range positive {
		if field.value == 0 {
			return fmt.Errorf("calibrated bound %s must be positive", field.name)
		}
	}

	if b.MinEpochLengthBlocks > b.MaxEpochLengthBlocks {
		return fmt.Errorf(
			"min epoch length %d exceeds max epoch length %d",
			b.MinEpochLengthBlocks, b.MaxEpochLengthBlocks,
		)
	}
	if b.MaxEmissionTreasuryShareBps >= BasisPointsDenominator {
		return fmt.Errorf(
			"max emission treasury share is %d bps, must be below %d",
			b.MaxEmissionTreasuryShareBps, BasisPointsDenominator,
		)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Shared relation helpers
// ---------------------------------------------------------------------------

func requirePositiveAtMost(name string, value, hardMax uint64) error {
	if hardMax == 0 {
		return fmt.Errorf("hard max for %s must be positive", name)
	}
	if value == 0 || value > hardMax {
		return fmt.Errorf("%s is %d, must be in (0, %d]", name, value, hardMax)
	}
	return nil
}

func requirePositiveAtLeast(name string, value, hardMin uint64) error {
	if hardMin == 0 {
		return fmt.Errorf("hard min for %s must be positive", name)
	}
	if value < hardMin {
		return fmt.Errorf("%s is %d, below hard min %d", name, value, hardMin)
	}
	return nil
}
