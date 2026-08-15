package consensusvectors

import _ "embed"

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

// LoadRewardPack returns the r1 pack, verifying its declared identity.
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
	return pack, nil
}
