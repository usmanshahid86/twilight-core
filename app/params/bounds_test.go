package params

import (
	"math"
	"testing"

	sdkmath "cosmossdk.io/math"
)

// TestProtocolFixedValuesLocked freezes the values the protocol fixes, so a
// later edit cannot quietly relax a ceiling that is not a deployment choice.
func TestProtocolFixedValuesLocked(t *testing.T) {
	cases := []struct {
		name      string
		got, want uint64
	}{
		{"BasisPointsDenominator", BasisPointsDenominator, 10_000},
		{"AbsoluteMaxSelectionRateBps", AbsoluteMaxSelectionRateBps, 5_000},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
	if AbsoluteMaxSelectionRateBps*2 != BasisPointsDenominator {
		t.Errorf("absolute max selection rate should be exactly half of the denominator")
	}
}

func TestValidateEmissionTreasuryShareBps(t *testing.T) {
	cases := []struct {
		name           string
		share, hardMax uint64
		wantErr        bool
	}{
		{name: "zero share is allowed", share: 0, hardMax: 2_000},
		// A zero ceiling permanently disables treasury diversion. It is legal,
		// and a zero share still satisfies it.
		{name: "zero share under a zero ceiling is allowed", share: 0, hardMax: 0},
		{name: "positive share under a zero ceiling is rejected", share: 1, hardMax: 0, wantErr: true},
		{name: "share below hard max", share: 1_999, hardMax: 2_000},
		{name: "share exactly at hard max", share: 2_000, hardMax: 2_000},
		{name: "share one past hard max", share: 2_001, hardMax: 2_000, wantErr: true},
		{name: "hard max just below denominator", share: 0, hardMax: BasisPointsDenominator - 1},
		{name: "hard max equal to denominator is rejected", share: 0, hardMax: BasisPointsDenominator, wantErr: true},
		{name: "hard max above denominator is rejected", share: 0, hardMax: BasisPointsDenominator + 1, wantErr: true},
		{name: "hard max at max uint64 is rejected", share: 0, hardMax: math.MaxUint64, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateEmissionTreasuryShareBps(c.share, c.hardMax)
			if c.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateSelectionRateBps(t *testing.T) {
	cases := []struct {
		name        string
		rate, opMax uint64
		wantErr     bool
	}{
		{name: "rate below operational max", rate: 2_500, opMax: 5_000},
		{name: "rate exactly at operational max", rate: 5_000, opMax: 5_000},
		{name: "rate one past operational max", rate: 5_001, opMax: 5_000, wantErr: true},
		{name: "zero rate is rejected", rate: 0, opMax: 5_000, wantErr: true},
		{name: "zero operational max is rejected", rate: 1, opMax: 0, wantErr: true},
		{name: "operational max at absolute ceiling", rate: 1, opMax: AbsoluteMaxSelectionRateBps},
		{name: "operational max one past absolute ceiling", rate: 1, opMax: AbsoluteMaxSelectionRateBps + 1, wantErr: true},
		{name: "operational max at denominator is rejected", rate: 1, opMax: BasisPointsDenominator, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateSelectionRateBps(c.rate, c.opMax)
			if c.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateMaxSelectedParticipants(t *testing.T) {
	cases := []struct {
		name           string
		value, hardMax uint64
		wantErr        bool
	}{
		{name: "one below hard max", value: 9, hardMax: 10},
		{name: "exactly at hard max", value: 10, hardMax: 10},
		{name: "one past hard max", value: 11, hardMax: 10, wantErr: true},
		{name: "zero value is rejected", value: 0, hardMax: 10, wantErr: true},
		{name: "zero hard max is rejected", value: 1, hardMax: 0, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateMaxSelectedParticipants(c.value, c.hardMax)
			if c.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// mustInt parses a decimal string into an arbitrary-precision Int, so test cases
// can express monetary values that do not fit a fixed-width type.
func mustInt(t *testing.T, s string) sdkmath.Int {
	t.Helper()
	v, ok := sdkmath.NewIntFromString(s)
	if !ok {
		t.Fatalf("could not parse %q as sdkmath.Int", s)
	}
	return v
}

func TestValidateMinRecipientPayoutAmount(t *testing.T) {
	const (
		maxUint64 = "18446744073709551615" // 2^64 - 1
		twoPow64  = "18446744073709551616" // 2^64, one past uint64
		twoPow128 = "340282366920938463463374607431768211456"
	)

	cases := []struct {
		name            string
		amount, hardMin sdkmath.Int
		wantErr         bool
	}{
		{name: "above hard min", amount: sdkmath.NewInt(1_001), hardMin: sdkmath.NewInt(1_000)},
		{name: "exactly at hard min", amount: sdkmath.NewInt(1_000), hardMin: sdkmath.NewInt(1_000)},
		{name: "one below hard min", amount: sdkmath.NewInt(999), hardMin: sdkmath.NewInt(1_000), wantErr: true},
		{name: "zero amount is below a positive floor", amount: sdkmath.NewInt(0), hardMin: sdkmath.NewInt(1_000), wantErr: true},
		{name: "negative amount is rejected", amount: sdkmath.NewInt(-1), hardMin: sdkmath.NewInt(1_000), wantErr: true},
		{name: "zero hard min is rejected", amount: sdkmath.NewInt(1), hardMin: sdkmath.NewInt(0), wantErr: true},
		{name: "negative hard min is rejected", amount: sdkmath.NewInt(1), hardMin: sdkmath.NewInt(-1), wantErr: true},

		// The zero value of sdkmath.Int panics on every comparison and sign
		// query, so both operands must be rejected rather than dereferenced.
		{name: "uninitialized amount is rejected, not panicked on", amount: sdkmath.Int{}, hardMin: sdkmath.NewInt(1), wantErr: true},
		{name: "uninitialized hard min is rejected, not panicked on", amount: sdkmath.NewInt(1), hardMin: sdkmath.Int{}, wantErr: true},
		{name: "both operands uninitialized", amount: sdkmath.Int{}, hardMin: sdkmath.Int{}, wantErr: true},

		// Settlement amounts are arbitrary-precision base-denom values. A payout
		// above math.MaxUint64 is legitimate, not an overflow.
		//
		// The three passing cases below are the discriminators against narrowing:
		// 2^64 truncates to 0 through uint64, so each would flip from passing to
		// failing if either operand were narrowed.
		{name: "amount above max uint64 clears a small floor", amount: mustInt(t, twoPow64), hardMin: sdkmath.NewInt(1)},
		{name: "amount far above max uint64", amount: mustInt(t, twoPow128), hardMin: mustInt(t, twoPow64)},
		{name: "equal operands both above max uint64", amount: mustInt(t, twoPow64), hardMin: mustInt(t, twoPow64)},

		// Boundary either side of the fixed-width limit. This case is not a
		// narrowing discriminator: it fails under both a correct and a narrowed
		// implementation, for different reasons.
		{name: "max uint64 is below a floor of two-to-the-64", amount: mustInt(t, maxUint64), hardMin: mustInt(t, twoPow64), wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateMinRecipientPayoutAmount(c.amount, c.hardMin)
			if c.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateSelectionPolicyUpdateCooldownBlocks(t *testing.T) {
	cases := []struct {
		name           string
		value, hardMin uint64
		wantErr        bool
	}{
		{name: "above hard min", value: 101, hardMin: 100},
		{name: "exactly at hard min", value: 100, hardMin: 100},
		{name: "one below hard min", value: 99, hardMin: 100, wantErr: true},
		{name: "zero cooldown is rejected", value: 0, hardMin: 100, wantErr: true},
		{name: "zero hard min is rejected", value: 1, hardMin: 0, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateSelectionPolicyUpdateCooldownBlocks(c.value, c.hardMin)
			if c.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestSelectionParamsValidate(t *testing.T) {
	// valid is a structurally sound set used as the base for each mutation. The
	// numbers are test fixtures only and carry no protocol meaning.
	valid := SelectionParams{
		MaxSelectionRateBps:          2_500,
		BeaconStartOffsetBlocks:      48,
		BeaconWindowBlocks:           24,
		MinExternalBeaconBlocks:      12,
		MinDistinctExternalProposers: 3,
	}
	const hardMinEpochLength uint64 = 100

	cases := []struct {
		name    string
		mutate  func(*SelectionParams)
		hardMin uint64
		wantErr bool
	}{
		{name: "structurally valid", mutate: func(*SelectionParams) {}, hardMin: hardMinEpochLength},

		{name: "zero selection rate", mutate: func(p *SelectionParams) { p.MaxSelectionRateBps = 0 }, hardMin: hardMinEpochLength, wantErr: true},
		{name: "selection rate at absolute ceiling", mutate: func(p *SelectionParams) { p.MaxSelectionRateBps = AbsoluteMaxSelectionRateBps }, hardMin: hardMinEpochLength},
		{name: "selection rate one past absolute ceiling", mutate: func(p *SelectionParams) { p.MaxSelectionRateBps = AbsoluteMaxSelectionRateBps + 1 }, hardMin: hardMinEpochLength, wantErr: true},

		{name: "zero beacon start offset", mutate: func(p *SelectionParams) { p.BeaconStartOffsetBlocks = 0 }, hardMin: hardMinEpochLength, wantErr: true},
		{name: "zero beacon window", mutate: func(p *SelectionParams) { p.BeaconWindowBlocks = 0 }, hardMin: hardMinEpochLength, wantErr: true},

		{name: "min external equals window", mutate: func(p *SelectionParams) { p.MinExternalBeaconBlocks = p.BeaconWindowBlocks }, hardMin: hardMinEpochLength},
		{name: "min external one past window", mutate: func(p *SelectionParams) { p.MinExternalBeaconBlocks = p.BeaconWindowBlocks + 1 }, hardMin: hardMinEpochLength, wantErr: true},
		{name: "zero min external", mutate: func(p *SelectionParams) { p.MinExternalBeaconBlocks = 0 }, hardMin: hardMinEpochLength, wantErr: true},

		{name: "distinct proposers equals min external", mutate: func(p *SelectionParams) { p.MinDistinctExternalProposers = p.MinExternalBeaconBlocks }, hardMin: hardMinEpochLength},
		{name: "distinct proposers one past min external", mutate: func(p *SelectionParams) { p.MinDistinctExternalProposers = p.MinExternalBeaconBlocks + 1 }, hardMin: hardMinEpochLength, wantErr: true},
		{name: "zero distinct proposers", mutate: func(p *SelectionParams) { p.MinDistinctExternalProposers = 0 }, hardMin: hardMinEpochLength, wantErr: true},

		// Geometry must fit: offset + window + 1 <= hardMin.
		{name: "geometry exactly fits", mutate: func(p *SelectionParams) {
			p.BeaconStartOffsetBlocks = 50
			p.BeaconWindowBlocks = 49
		}, hardMin: hardMinEpochLength},
		{name: "geometry one block too large", mutate: func(p *SelectionParams) {
			p.BeaconStartOffsetBlocks = 50
			p.BeaconWindowBlocks = 50
		}, hardMin: hardMinEpochLength, wantErr: true},
		{name: "zero hard min epoch length", mutate: func(*SelectionParams) {}, hardMin: 0, wantErr: true},

		// The window components are operator-supplied; their sum must not wrap
		// into a small value that would appear to fit inside the epoch.
		{name: "geometry sum overflows rather than wrapping", mutate: func(p *SelectionParams) {
			p.BeaconStartOffsetBlocks = math.MaxUint64
			p.BeaconWindowBlocks = math.MaxUint64
			p.MinExternalBeaconBlocks = 1
			p.MinDistinctExternalProposers = 1
		}, hardMin: hardMinEpochLength, wantErr: true},
		{name: "geometry sum overflows on the trailing block", mutate: func(p *SelectionParams) {
			p.BeaconStartOffsetBlocks = math.MaxUint64 - 1
			p.BeaconWindowBlocks = 1
			p.MinExternalBeaconBlocks = 1
			p.MinDistinctExternalProposers = 1
		}, hardMin: hardMinEpochLength, wantErr: true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := valid
			c.mutate(&p)
			err := p.Validate(c.hardMin)
			if c.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestCalibratedBoundsValidateStructural(t *testing.T) {
	// sound is structurally valid. Every number here is a test fixture with no
	// protocol meaning: this package deliberately ships no calibrated values.
	sound := CalibratedBounds{
		MaxActiveCoreSlots:                     1,
		MinEpochLengthBlocks:                   1,
		MaxEpochLengthBlocks:                   2,
		MaxSelectedParticipants:                1,
		MaxCandidatesPerSelection:              1,
		MaxRecipientsPerChunk:                  1,
		MaxChunksPerSettlement:                 1,
		MinSelectionPolicyUpdateCooldownBlocks: 1,
		MaxEmissionTreasuryShareBps:            1,
		MaxCoreSlotMetadataBytes:               1,
		MaxTxMessageBytes:                      1,
		MinSettlementPayoutAmount:              sdkmath.NewInt(1),
	}

	if err := sound.ValidateStructural(); err != nil {
		t.Fatalf("structurally sound bounds rejected: %v", err)
	}

	// A zero value disables the path these bounds govern, so each is rejected at
	// zero. MaxEmissionTreasuryShareBps is deliberately absent: a zero ceiling is
	// legal and means treasury diversion is permanently disabled.
	zeroing := []struct {
		name string
		zero func(*CalibratedBounds)
	}{
		{"MaxActiveCoreSlots", func(b *CalibratedBounds) { b.MaxActiveCoreSlots = 0 }},
		{"MinEpochLengthBlocks", func(b *CalibratedBounds) { b.MinEpochLengthBlocks = 0 }},
		{"MaxEpochLengthBlocks", func(b *CalibratedBounds) { b.MaxEpochLengthBlocks = 0 }},
		{"MaxSelectedParticipants", func(b *CalibratedBounds) { b.MaxSelectedParticipants = 0 }},
		{"MaxCandidatesPerSelection", func(b *CalibratedBounds) { b.MaxCandidatesPerSelection = 0 }},
		{"MaxRecipientsPerChunk", func(b *CalibratedBounds) { b.MaxRecipientsPerChunk = 0 }},
		{"MaxChunksPerSettlement", func(b *CalibratedBounds) { b.MaxChunksPerSettlement = 0 }},
		{"MinSelectionPolicyUpdateCooldownBlocks", func(b *CalibratedBounds) { b.MinSelectionPolicyUpdateCooldownBlocks = 0 }},
		{"MaxCoreSlotMetadataBytes", func(b *CalibratedBounds) { b.MaxCoreSlotMetadataBytes = 0 }},
		{"MaxTxMessageBytes", func(b *CalibratedBounds) { b.MaxTxMessageBytes = 0 }},
	}
	for _, c := range zeroing {
		t.Run("zero "+c.name, func(t *testing.T) {
			b := sound
			c.zero(&b)
			if err := b.ValidateStructural(); err == nil {
				t.Fatalf("expected zero %s to be rejected", c.name)
			}
		})
	}

	t.Run("empty bounds are rejected", func(t *testing.T) {
		if err := (CalibratedBounds{}).ValidateStructural(); err == nil {
			t.Fatalf("expected the zero value to be rejected")
		}
	})

	t.Run("epoch length window may be a single value", func(t *testing.T) {
		b := sound
		b.MinEpochLengthBlocks = 7
		b.MaxEpochLengthBlocks = 7
		if err := b.ValidateStructural(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("inverted epoch length window is rejected", func(t *testing.T) {
		b := sound
		b.MinEpochLengthBlocks = 8
		b.MaxEpochLengthBlocks = 7
		if err := b.ValidateStructural(); err == nil {
			t.Fatalf("expected inverted epoch length window to be rejected")
		}
	})

	// The normative relation places no lower bound on the treasury ceiling:
	// 0 <= share <= HARD_MAX < 10_000. Rejecting a zero ceiling would impose a
	// constraint the protocol does not state.
	t.Run("treasury share ceiling of zero is accepted", func(t *testing.T) {
		b := sound
		b.MaxEmissionTreasuryShareBps = 0
		if err := b.ValidateStructural(); err != nil {
			t.Fatalf("a zero treasury ceiling is legal, got: %v", err)
		}
	})

	t.Run("treasury share ceiling just below the denominator is accepted", func(t *testing.T) {
		b := sound
		b.MaxEmissionTreasuryShareBps = BasisPointsDenominator - 1
		if err := b.ValidateStructural(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("treasury share ceiling at the denominator is rejected", func(t *testing.T) {
		b := sound
		b.MaxEmissionTreasuryShareBps = BasisPointsDenominator
		if err := b.ValidateStructural(); err == nil {
			t.Fatalf("expected a treasury ceiling equal to the denominator to be rejected")
		}
	})

	// The settlement payout floor is arbitrary-precision and must not be
	// dereferenced while uninitialized.
	t.Run("uninitialized settlement payout floor is rejected", func(t *testing.T) {
		b := sound
		b.MinSettlementPayoutAmount = sdkmath.Int{}
		if err := b.ValidateStructural(); err == nil {
			t.Fatalf("expected an uninitialized payout floor to be rejected")
		}
	})

	t.Run("zero settlement payout floor is rejected", func(t *testing.T) {
		b := sound
		b.MinSettlementPayoutAmount = sdkmath.NewInt(0)
		if err := b.ValidateStructural(); err == nil {
			t.Fatalf("expected a zero payout floor to be rejected")
		}
	})

	t.Run("negative settlement payout floor is rejected", func(t *testing.T) {
		b := sound
		b.MinSettlementPayoutAmount = sdkmath.NewInt(-1)
		if err := b.ValidateStructural(); err == nil {
			t.Fatalf("expected a negative payout floor to be rejected")
		}
	})

	t.Run("settlement payout floor above max uint64 is accepted", func(t *testing.T) {
		b := sound
		b.MinSettlementPayoutAmount = mustInt(t, "18446744073709551616") // 2^64
		if err := b.ValidateStructural(); err != nil {
			t.Fatalf("an arbitrary-precision floor is legal, got: %v", err)
		}
	})
}

// TestValidateEpochLengthBlocks exercises the ratified admission interval
//
//	HardMinEpochLengthBlocks <= epoch_length_blocks <= HardMaxEpochLengthBlocks
//
// against the real constants. The bounds are consensus values now, so a test that
// injected its own numbers would prove the relation while saying nothing about
// what the chain actually admits.
func TestValidateEpochLengthBlocks(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value uint64
		ok    bool
	}{
		{name: "one below the floor", value: HardMinEpochLengthBlocks - 1},
		{name: "at the floor", value: HardMinEpochLengthBlocks, ok: true},
		{name: "one above the floor", value: HardMinEpochLengthBlocks + 1, ok: true},
		{name: "one below the ceiling", value: HardMaxEpochLengthBlocks - 1, ok: true},
		{name: "at the ceiling", value: HardMaxEpochLengthBlocks, ok: true},
		{name: "one above the ceiling", value: HardMaxEpochLengthBlocks + 1},
		{name: "zero", value: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateEpochLengthBlocks(tc.value)
			if tc.ok {
				if err != nil {
					t.Fatalf("epoch length %d rejected: %v", tc.value, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("epoch length %d accepted", tc.value)
			}
		})
	}
}

// TestRatifiedEpochLengthBoundsLocked freezes the two ratified values.
//
// They are immutable within a running network, so a later edit is a consensus
// change rather than a calibration tweak. The literals are spelled out here
// deliberately: the test must fail if the constants move, not track them.
func TestRatifiedEpochLengthBoundsLocked(t *testing.T) {
	if HardMinEpochLengthBlocks != 360 {
		t.Errorf("HardMinEpochLengthBlocks = %d, want 360", HardMinEpochLengthBlocks)
	}
	if HardMaxEpochLengthBlocks != 720 {
		t.Errorf("HardMaxEpochLengthBlocks = %d, want 720", HardMaxEpochLengthBlocks)
	}
	if HardMinEpochLengthBlocks > HardMaxEpochLengthBlocks {
		t.Fatal("the admission interval is inverted")
	}
}

// TestRecommendedBeaconGeometryFitsTheHardMinimum is the relation the floor was
// chosen to satisfy: r6's recommended geometry must fit inside the SHORTEST
// permitted epoch, and must keep fitting as geometries change.
func TestRecommendedBeaconGeometryFitsTheHardMinimum(t *testing.T) {
	recommended := SelectionParams{
		MaxSelectionRateBps:          2_500,
		BeaconStartOffsetBlocks:      48,
		BeaconWindowBlocks:           24,
		MinExternalBeaconBlocks:      12,
		MinDistinctExternalProposers: 4,
	}
	if err := recommended.Validate(HardMinEpochLengthBlocks); err != nil {
		t.Fatalf("recommended beacon geometry must fit the hard minimum: %v", err)
	}

	// And a geometry that does not fit is refused, so the check is load-bearing.
	tooWide := recommended
	tooWide.BeaconWindowBlocks = HardMinEpochLengthBlocks
	if err := tooWide.Validate(HardMinEpochLengthBlocks); err == nil {
		t.Error("a beacon window as long as the shortest epoch must be refused")
	}
}
