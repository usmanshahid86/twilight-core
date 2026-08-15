package consensusvectors

import (
	_ "embed"
	"fmt"
)

// The r1 reward consensus-vector pack: per-block emission with supply-threshold
// halving, uniform active-block allocation, and the reward-pool relation.
//
//go:embed testdata/twilight-reward-consensus-vectors-v1-r1.json
var rewardPackBytes []byte

const (
	rewardPackArtifact = "twilight-reward-consensus-vectors-v1"
	rewardPackVersion  = 1
	rewardPackRevision = 1
)

// RewardPack is the r1 reward consensus-vector pack.
//
// Monetary amounts are decimal strings rather than U64: they are
// arbitrary-precision base-denomination values and are parsed into
// cosmossdk.io/math.Int, never narrowed through a fixed-width type.
type RewardPack struct {
	Artifact                 string                  `json:"artifact"`
	Version                  int                     `json:"version"`
	Revision                 int                     `json:"revision"`
	Normative                bool                    `json:"normative"`
	EmissionReference        string                  `json:"emission_reference"`
	EmissionVectors          []EmissionVector        `json:"emission_vectors"`
	AllocationVectors        []AllocationVector      `json:"allocation_vectors"`
	PoolVectors              []PoolVector            `json:"pool_vectors"`
	RequiredAssertions       []string                `json:"required_assertions"`
	NegativeDiscriminators   []NegativeDiscriminator `json:"negative_discriminators"`
	PerBlockSubsidySemantics string                  `json:"per_block_subsidies_semantics"`
}

// EmissionVector is one epoch-emission case. PerBlockSubsidies is conceptually
// as long as reward_enabled_blocks; once the subsidy reaches zero every trailing
// entry is zero.
type EmissionVector struct {
	Name               string   `json:"name"`
	CumulativeBefore   string   `json:"cumulative_before"`
	RewardEnabledBlock U64      `json:"reward_enabled_blocks"`
	MaxSupply          string   `json:"max_supply"`
	InitialBlockSubsid string   `json:"initial_block_subsidy"`
	PerBlockSubsidies  []string `json:"per_block_subsidies"`
	MintedEmission     string   `json:"minted_emission"`
	CumulativeAfter    string   `json:"cumulative_after"`
}

// AllocationVector is one uniform active-block allocation case. NPos is the
// number of Slots with positive activity and bounds the permitted carry.
type AllocationVector struct {
	Name         string   `json:"name"`
	Pool         string   `json:"pool"`
	BlocksActive []U64    `json:"blocks_active"`
	Entitlements []string `json:"entitlements"`
	CarryOut     string   `json:"carry_out"`
	Allocated    string   `json:"allocated"`
	NPos         int      `json:"n_pos"`
}

// PoolVector is one reward-pool relation case:
//
//	pool = minted_emission - treasury + carry_in
type PoolVector struct {
	Name             string `json:"name"`
	MintedEmission   string `json:"minted_emission"`
	TreasuryShareBps U64    `json:"treasury_share_bps"`
	Treasury         string `json:"treasury"`
	CarryIn          string `json:"carry_in"`
	Pool             string `json:"pool"`
}

// NegativeDiscriminator is an incorrect-algorithm case. Each names both the
// forbidden computation and the observable consequence of performing it, so a
// conforming implementation can be distinguished from a plausible wrong one.
type NegativeDiscriminator struct {
	Name string `json:"name"`

	Pool         string `json:"pool"`
	BlocksActive []U64  `json:"blocks_active"`

	// Fields of wrong-denominator-overallocation-must-reject.
	CorrectDenominatorW     U64      `json:"correct_denominator_W"`
	IncorrectDenominatorREB U64      `json:"incorrect_denominator_reward_enabled_blocks"`
	IncorrectEntitlements   []string `json:"incorrect_entitlements"`
	IncorrectAllocated      string   `json:"incorrect_allocated"`

	// Fields of floor-pool-before-multiply-must-not-conform.
	CorrectEntitlements          []string `json:"correct_entitlements"`
	IncorrectFloorPoolThenMultip []string `json:"incorrect_floor_pool_over_W_then_multiply"`

	RequiredResult string `json:"required_result"`
}

// LoadRewardPack returns the r1 pack, verifying its declared identity and its
// mandatory structure.
func LoadRewardPack() (RewardPack, error) {
	var pack RewardPack
	if err := decodePack(RewardPackFilename, rewardPackBytes, &pack); err != nil {
		return RewardPack{}, err
	}
	if err := assertMetadata(
		RewardPackFilename,
		pack.Artifact, rewardPackArtifact,
		pack.Version, rewardPackVersion,
		pack.Revision, rewardPackRevision,
		pack.Normative,
	); err != nil {
		return RewardPack{}, err
	}
	if err := pack.validate(RewardPackFilename); err != nil {
		return RewardPack{}, err
	}
	return pack, nil
}

// validate checks the mandatory structure of the r1 pack.
//
// emission_reference and per_block_subsidies_semantics are normative prose that
// states which recurrence the numbers come from and how a truncated per-block
// schedule is to be read. Either could vanish and leave every numeric assertion
// still passing while the pack no longer says what it is asserting about.
func (p RewardPack) validate(filename string) error {
	if err := firstError(
		requireText(filename, "emission_reference", p.EmissionReference),
		requireText(filename, "per_block_subsidies_semantics", p.PerBlockSubsidySemantics),
		requireNonEmptySlice(filename, "emission_vectors", len(p.EmissionVectors)),
		requireNonEmptySlice(filename, "allocation_vectors", len(p.AllocationVectors)),
		requireNonEmptySlice(filename, "pool_vectors", len(p.PoolVectors)),
		requireNonEmptySlice(filename, "required_assertions", len(p.RequiredAssertions)),
		requireNonEmptySlice(filename, "negative_discriminators", len(p.NegativeDiscriminators)),
	); err != nil {
		return err
	}

	for i, v := range p.EmissionVectors {
		prefix := fmt.Sprintf("emission_vectors[%d]", i)
		if err := firstError(
			requireText(filename, prefix+".name", v.Name),
			requireAmount(filename, prefix+".cumulative_before", v.CumulativeBefore),
			requireSet(filename, prefix+".reward_enabled_blocks", v.RewardEnabledBlock),
			requireAmount(filename, prefix+".max_supply", v.MaxSupply),
			requireAmount(filename, prefix+".initial_block_subsidy", v.InitialBlockSubsid),
			requireAmount(filename, prefix+".minted_emission", v.MintedEmission),
			requireAmount(filename, prefix+".cumulative_after", v.CumulativeAfter),
		); err != nil {
			return err
		}
		// A fully paused epoch legitimately carries an empty schedule.
		if v.PerBlockSubsidies == nil {
			return structureError(filename, "%s.per_block_subsidies is missing", prefix)
		}
		for j, subsidy := range v.PerBlockSubsidies {
			if err := requireAmount(filename, fmt.Sprintf("%s.per_block_subsidies[%d]", prefix, j), subsidy); err != nil {
				return err
			}
		}
	}

	for i, v := range p.AllocationVectors {
		prefix := fmt.Sprintf("allocation_vectors[%d]", i)
		if err := firstError(
			requireText(filename, prefix+".name", v.Name),
			requireAmount(filename, prefix+".pool", v.Pool),
			requireNonEmptySlice(filename, prefix+".blocks_active", len(v.BlocksActive)),
			requireNonEmptySlice(filename, prefix+".entitlements", len(v.Entitlements)),
			requireAmount(filename, prefix+".carry_out", v.CarryOut),
			requireAmount(filename, prefix+".allocated", v.Allocated),
		); err != nil {
			return err
		}
		if len(v.BlocksActive) != len(v.Entitlements) {
			return structureError(filename,
				"%s states %d activity rows but %d entitlements", prefix, len(v.BlocksActive), len(v.Entitlements))
		}
		for j, blocks := range v.BlocksActive {
			if err := requireSet(filename, fmt.Sprintf("%s.blocks_active[%d]", prefix, j), blocks); err != nil {
				return err
			}
		}
		for j, entitlement := range v.Entitlements {
			if err := requireAmount(filename, fmt.Sprintf("%s.entitlements[%d]", prefix, j), entitlement); err != nil {
				return err
			}
		}
	}

	for i, v := range p.PoolVectors {
		prefix := fmt.Sprintf("pool_vectors[%d]", i)
		if err := firstError(
			requireText(filename, prefix+".name", v.Name),
			requireAmount(filename, prefix+".minted_emission", v.MintedEmission),
			requireSet(filename, prefix+".treasury_share_bps", v.TreasuryShareBps),
			requireAmount(filename, prefix+".treasury", v.Treasury),
			requireAmount(filename, prefix+".carry_in", v.CarryIn),
			requireAmount(filename, prefix+".pool", v.Pool),
		); err != nil {
			return err
		}
	}

	for i, assertion := range p.RequiredAssertions {
		if err := requireText(filename, fmt.Sprintf("required_assertions[%d]", i), assertion); err != nil {
			return err
		}
	}

	for i, v := range p.NegativeDiscriminators {
		if err := v.validate(filename, fmt.Sprintf("negative_discriminators[%d]", i)); err != nil {
			return err
		}
	}
	return nil
}

// validate checks a negative discriminator against the shape its own case uses.
//
// The two discriminators describe different forbidden computations and therefore
// populate different fields. Requiring the union of both would reject the pack as
// written, so the shape is identified first and only its own fields are demanded.
func (d NegativeDiscriminator) validate(filename, prefix string) error {
	if err := firstError(
		requireText(filename, prefix+".name", d.Name),
		requireText(filename, prefix+".required_result", d.RequiredResult),
		requireAmount(filename, prefix+".pool", d.Pool),
		requireNonEmptySlice(filename, prefix+".blocks_active", len(d.BlocksActive)),
	); err != nil {
		return err
	}
	for j, blocks := range d.BlocksActive {
		if err := requireSet(filename, fmt.Sprintf("%s.blocks_active[%d]", prefix, j), blocks); err != nil {
			return err
		}
	}

	overAllocation := len(d.IncorrectEntitlements) > 0
	roundingOrder := len(d.IncorrectFloorPoolThenMultip) > 0
	switch {
	case overAllocation && roundingOrder:
		return structureError(filename, "%s matches two discriminator shapes at once", prefix)
	case overAllocation:
		if err := firstError(
			requireSet(filename, prefix+".correct_denominator_W", d.CorrectDenominatorW),
			requireSet(filename, prefix+".incorrect_denominator_reward_enabled_blocks", d.IncorrectDenominatorREB),
			requireAmount(filename, prefix+".incorrect_allocated", d.IncorrectAllocated),
		); err != nil {
			return err
		}
		return requireAmountSlice(filename, prefix+".incorrect_entitlements", d.IncorrectEntitlements)
	case roundingOrder:
		if err := requireNonEmptySlice(filename, prefix+".correct_entitlements", len(d.CorrectEntitlements)); err != nil {
			return err
		}
		if err := requireAmountSlice(filename, prefix+".correct_entitlements", d.CorrectEntitlements); err != nil {
			return err
		}
		return requireAmountSlice(filename, prefix+".incorrect_floor_pool_over_W_then_multiply", d.IncorrectFloorPoolThenMultip)
	default:
		return structureError(filename, "%s matches no known discriminator shape", prefix)
	}
}

func requireAmountSlice(filename, field string, values []string) error {
	for i, value := range values {
		if err := requireAmount(filename, fmt.Sprintf("%s[%d]", field, i), value); err != nil {
			return err
		}
	}
	return nil
}
