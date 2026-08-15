package consensusvectors

import _ "embed"

// The r1 SelectedDrawIDsHashV1 vector pack: the chain-state commitment over an
// ordered selected-participant list.
//
//go:embed testdata/twilight-selected-draw-ids-hash-v1-vectors-r1.json
var selectedDrawIDsPackBytes []byte

const (
	selectedDrawIDsArtifact = "twilight-selected-draw-ids-hash-v1-vectors"
	selectedDrawIDsVersion  = 1
	selectedDrawIDsRevision = 1
)

// SelectedDrawIDsPack is the r1 SelectedDrawIDsHashV1 vector pack.
type SelectedDrawIDsPack struct {
	Artifact            string                  `json:"artifact"`
	Version             int                     `json:"version"`
	Revision            int                     `json:"revision"`
	Normative           bool                    `json:"normative"`
	Domain              string                  `json:"domain"`
	Encoding            string                  `json:"encoding"`
	Vectors             []SelectedDrawIDsVector `json:"vectors"`
	NegativeRequirement []string                `json:"negative_requirements"`
}

// SelectedDrawIDsVector is one digest vector. The selected list is in the exact
// order the result message carries; reordering it is a different input, not an
// error.
type SelectedDrawIDsVector struct {
	Name            string   `json:"name"`
	ChainID         string   `json:"chain_id"`
	SlotID          U64      `json:"slot_id"`
	TargetEpoch     U64      `json:"target_epoch"`
	SelectedDrawIDs []string `json:"selected_draw_ids"`
	ExpectedHash    string   `json:"expected_hash"`
}

// LoadSelectedDrawIDsPack returns the r1 pack, verifying its declared identity.
func LoadSelectedDrawIDsPack() (SelectedDrawIDsPack, error) {
	var pack SelectedDrawIDsPack
	if err := decodePack(SelectedDrawIDsPackFilename, selectedDrawIDsPackBytes, &pack); err != nil {
		return SelectedDrawIDsPack{}, err
	}
	if err := assertMetadata(
		SelectedDrawIDsPackFilename,
		pack.Artifact, selectedDrawIDsArtifact,
		pack.Version, selectedDrawIDsVersion,
		pack.Revision, selectedDrawIDsRevision,
		pack.Normative,
	); err != nil {
		return SelectedDrawIDsPack{}, err
	}
	return pack, nil
}
