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
// cosmossdk.io/math is an external module dependency already used across the
// repository and cannot create a repository cycle.

import (
	"fmt"

	sdkmath "cosmossdk.io/math"

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

	// HardMaxActiveCoreSlots is the immutable ceiling on the number of
	// simultaneously ACTIVE CoreSlots. It is ratified, not calibrated, so like
	// AbsoluteMaxSelectionRateBps it is a fixed constant rather than a
	// CalibratedBounds field.
	//
	// A larger registered population may exist; at most this many Slots are
	// ACTIVE at once. The bound is an implementation/network safety ceiling and
	// closes the architecture's O(A) workload bound for rewards BeginBlock.
	//
	// This is NOT the governance-configured maximum. Params.MaxActiveSlots is a
	// separate operational ceiling that sits beneath this one and may be set
	// lower; the two are enforced as distinct layers and must never be collapsed
	// into a single value, or a governance change could move the immutable
	// ceiling. That coreslot's DefaultParams happens to ship MaxActiveSlots: 100
	// is a coincidence of the V1 operating model, not the source of this value.
	HardMaxActiveCoreSlots uint64 = 100

	// HardMinSelectionPolicyUpdateCooldownBlocks is the immutable floor on the
	// configured Selection-policy update cooldown. Governance may configure any
	// value at or above it; it can never configure below it, and within a running
	// network the floor itself is not a parameter.
	//
	// What it protects is permanent state: an accepted policy update writes a new
	// immutable, non-prunable history version, so an unbounded update surface is a
	// permanent state-growth primitive that no-op rejection alone does not close.
	//
	// This is the CURRENT PRE-MAINNET calibration. The relation
	// (configured >= floor > 0) is fixed by the architecture; this number is not
	// yet final mainnet calibration and a mandatory calibration review — measuring
	// real per-update database growth, hostile churn, larger registered
	// populations and observed operator behavior — must confirm or replace it
	// before production-genesis freeze. It is deliberately NOT described as "one
	// epoch": epoch length is independently configurable and its own minimum is
	// not yet fixed.
	HardMinSelectionPolicyUpdateCooldownBlocks uint64 = 360
)

// ValidateMaxActiveSlots enforces
//
//	0 < configured <= HardMaxActiveCoreSlots
//
// on the governance-configured operational ceiling. The positive lower bound is
// what stops a configuration from making activation impossible; the upper bound
// is what stops governance from raising its own limit past the immutable one.
func ValidateMaxActiveSlots(configured uint64) error {
	if configured == 0 || configured > HardMaxActiveCoreSlots {
		return fmt.Errorf(
			"max active slots is %d, must be in (0, %d]",
			configured, HardMaxActiveCoreSlots,
		)
	}
	return nil
}

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
//	amount >= hardMin > 0
//
// The positive floor is what stops settlement creating dust payouts, and with
// them cheap accounts, on a feeless chain.
//
// Both operands are arbitrary-precision base-denom integers. Settlement amounts
// are canonical monetary values and are never narrowed through a fixed-width
// type: a payout above math.MaxUint64 is a legitimate value, not an overflow.
//
// The zero value of sdkmath.Int carries a nil big.Int and panics on any
// comparison or sign query, so both operands are checked with IsNil first;
// IsNil is the only method safe to call on it. Note that a positive amount
// follows from the relation itself: hardMin is required positive and amount is
// required to be at least hardMin.
func ValidateMinRecipientPayoutAmount(amount, hardMin sdkmath.Int) error {
	if amount.IsNil() {
		return fmt.Errorf("min recipient payout amount is uninitialized")
	}
	if hardMin.IsNil() {
		return fmt.Errorf("hard min settlement payout amount is uninitialized")
	}
	if !hardMin.IsPositive() {
		return fmt.Errorf("hard min settlement payout amount is %s, must be positive", hardMin)
	}
	if amount.LT(hardMin) {
		return fmt.Errorf("min recipient payout amount is %s, below hard min %s", amount, hardMin)
	}
	return nil
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
//
// Several of these bounds interact, and their combined workload is NOT validated
// here. MaxActiveCoreSlots together with MaxEpochLengthBlocks bounds per-epoch
// participation work, and MaxRecipientsPerChunk together with
// MaxChunksPerSettlement bounds settlement fan-out. Both pairings remain
// mandatory pre-genesis calibration and load-test gates. No numeric combined-work
// ceiling is invented here, and no representability requirement is imposed on
// those conceptual products: fitting a fixed-width type would not demonstrate
// that a configuration is operationally safe. Where future runtime code performs
// an actual fixed-width multiplication, that computation must use checked
// arithmetic suited to it.
type CalibratedBounds struct {
	// MaxActiveCoreSlots is superseded by the ratified HardMaxActiveCoreSlots
	// constant above and is no longer a calibration input. It is retained here
	// only so this struct keeps naming every bound the architecture enumerates;
	// consensus paths read the constant. Removing it is deliberate later cleanup,
	// not part of the change that ratified the value.
	MaxActiveCoreSlots                     uint64
	MinEpochLengthBlocks                   uint64
	MaxEpochLengthBlocks                   uint64
	MaxSelectedParticipants                uint64
	MaxCandidatesPerSelection              uint64
	MaxRecipientsPerChunk                  uint64
	MaxChunksPerSettlement                 uint64
	MinSelectionPolicyUpdateCooldownBlocks uint64
	MaxEmissionTreasuryShareBps            uint64
	MaxCoreSlotMetadataBytes               uint64
	MaxTxMessageBytes                      uint64

	// MinSettlementPayoutAmount is a base-denom monetary value and is therefore
	// arbitrary-precision, not fixed-width: a legitimate floor may exceed
	// math.MaxUint64.
	MinSettlementPayoutAmount sdkmath.Int
}

// ValidateStructural enforces only those relations that hold regardless of the
// values eventually ratified. It deliberately does not certify that a bound set
// is operationally safe: the combined workload gates described on CalibratedBounds
// remain pre-genesis calibration and load-test work, and nothing here substitutes
// for them. The name states that limit so a caller cannot read a passing result
// as a completeness guarantee.
//
// Checked here: bounds whose zero value would disable the path they govern are
// positive; the epoch-length window is non-empty; the settlement payout floor is
// initialized and positive; and the treasury share ceiling stays strictly below
// the basis-point denominator.
//
// MaxEmissionTreasuryShareBps is deliberately exempt from the positivity rule. The
// normative relation is
//
//	0 <= emission_treasury_share_bps <= HARD_MAX_EMISSION_TREASURY_SHARE_BPS < 10_000
//
// which places no lower bound on the ceiling. A ceiling of zero is legal and means
// treasury diversion is permanently disabled, so rejecting it would impose a
// constraint the protocol does not state.
func (b CalibratedBounds) ValidateStructural() error {
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
		{"MinSelectionPolicyUpdateCooldownBlocks", b.MinSelectionPolicyUpdateCooldownBlocks},
		{"MaxCoreSlotMetadataBytes", b.MaxCoreSlotMetadataBytes},
		{"MaxTxMessageBytes", b.MaxTxMessageBytes},
	}
	for _, field := range positive {
		if field.value == 0 {
			return fmt.Errorf("calibrated bound %s must be positive", field.name)
		}
	}

	// IsNil is the only method safe to call on an uninitialized sdkmath.Int.
	if b.MinSettlementPayoutAmount.IsNil() {
		return fmt.Errorf("calibrated bound MinSettlementPayoutAmount is uninitialized")
	}
	if !b.MinSettlementPayoutAmount.IsPositive() {
		return fmt.Errorf(
			"calibrated bound MinSettlementPayoutAmount is %s, must be positive",
			b.MinSettlementPayoutAmount,
		)
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
