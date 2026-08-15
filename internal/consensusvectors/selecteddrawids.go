package consensusvectors

import (
	_ "embed"
	"fmt"
)

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
	Version             Int                     `json:"version"`
	Revision            Int                     `json:"revision"`
	Normative           Bool                    `json:"normative"`
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

// LoadSelectedDrawIDsPack returns the r1 pack, verifying its declared identity
// and its mandatory structure.
func LoadSelectedDrawIDsPack() (SelectedDrawIDsPack, error) {
	var pack SelectedDrawIDsPack
	if err := decodePack(SelectedDrawIDsPackFilename, selectedDrawIDsPackBytes, &pack); err != nil {
		return SelectedDrawIDsPack{}, err
	}
	if err := requireMetadataPresence(SelectedDrawIDsPackFilename, pack.Version, pack.Revision, pack.Normative); err != nil {
		return SelectedDrawIDsPack{}, err
	}
	if err := assertMetadata(
		SelectedDrawIDsPackFilename,
		pack.Artifact, selectedDrawIDsArtifact,
		pack.Version.Value(), selectedDrawIDsVersion,
		pack.Revision.Value(), selectedDrawIDsRevision,
		pack.Normative.Bool(),
	); err != nil {
		return SelectedDrawIDsPack{}, err
	}
	if err := pack.validate(SelectedDrawIDsPackFilename); err != nil {
		return SelectedDrawIDsPack{}, err
	}
	return pack, nil
}

// validate checks the mandatory structure of the r1 pack.
//
// The domain and encoding declarations are normative: the domain is what a
// conformance test cross-checks against the library's own domain constant, so if
// it disappeared the cross-check would compare against "" and pass for the wrong
// reason.
func (p SelectedDrawIDsPack) validate(filename string) error {
	if err := firstError(
		requireText(filename, "domain", p.Domain),
		requireText(filename, "encoding", p.Encoding),
		requireNonEmptySlice(filename, "vectors", len(p.Vectors)),
		requireNonEmptySlice(filename, "negative_requirements", len(p.NegativeRequirement)),
	); err != nil {
		return err
	}

	for i, v := range p.Vectors {
		prefix := fmt.Sprintf("vectors[%d]", i)
		if err := firstError(
			requireText(filename, prefix+".name", v.Name),
			requireContext(filename, prefix, v.ChainID, v.SlotID, v.TargetEpoch),
			requireHex32(filename, prefix+".expected_hash", v.ExpectedHash),
		); err != nil {
			return err
		}
		// The empty vector legitimately carries an empty list; absence is not.
		if v.SelectedDrawIDs == nil {
			return structureError(filename, "%s.selected_draw_ids is missing", prefix)
		}
		for j, id := range v.SelectedDrawIDs {
			if err := requireHex32(filename, fmt.Sprintf("%s.selected_draw_ids[%d]", prefix, j), id); err != nil {
				return err
			}
		}
	}

	for i, requirement := range p.NegativeRequirement {
		if err := requireText(filename, fmt.Sprintf("negative_requirements[%d]", i), requirement); err != nil {
			return err
		}
	}
	return nil
}
