package consensusvectors

import (
	"fmt"
	"slices"
	"strings"
)

// Deferred fixtures: vector cases that are tracked and metadata-validated but
// deliberately NOT executed yet, because executing them would require chain
// machinery this repository does not have.
//
// A deferral is data, not a comment. It is declared here, checked against the
// pack it refers to, and cross-checked against what a conformance run actually
// executed. That is what stops the three ways a deferral silently rots: the
// fixture disappearing from a future pack revision, a second fixture appearing
// beside it and going unnoticed, and the fixture being quietly folded into the
// executed set so a run appears to cover more than it does.

// ProposerResolutionSection is the pack section holding historical
// proposer-attribution fixtures.
const ProposerResolutionSection = "proposer_resolution_vectors"

// DeferredFixture identifies one vector case that is tracked but not executed,
// and records why.
type DeferredFixture struct {
	// Pack is the tracked pack filename.
	Pack string
	// Section is the pack section holding the fixture.
	Section string
	// Name is the fixture's own name field. Fixtures are identified by name and
	// never by array position, so reordering a pack cannot silently retarget a
	// deferral onto a different case.
	Name string
	// Reason states what must exist before the fixture can be executed.
	Reason string
}

// deferredFixtures is the complete manifest.
var deferredFixtures = []DeferredFixture{
	{
		Pack:    DrawPackFilename,
		Section: ProposerResolutionSection,
		Name:    "validator_update_effective_u_plus_2",
		Reason: "execution requires the canonical historical proposer resolver and " +
			"consensus-key history, neither of which exists in this repository yet; " +
			"the fixture must then be executed against the real resolver rather than a " +
			"test-only substitute",
	},
}

// DeferredFixtures returns a copy of the manifest.
func DeferredFixtures() []DeferredFixture {
	return slices.Clone(deferredFixtures)
}

// DeferredIn returns the deferred fixture names declared for one pack section.
func DeferredIn(pack, section string) []string {
	var names []string
	for _, fixture := range deferredFixtures {
		if fixture.Pack == pack && fixture.Section == section {
			names = append(names, fixture.Name)
		}
	}
	return names
}

// ValidateProposerResolutionDeferral checks the manifest against the draw pack
// as loaded.
//
// It fails if the deferred fixture has disappeared from the pack, if a second
// proposer-resolution fixture has appeared without a corresponding decision, if
// the manifest does not declare exactly one deferral for the section, or if the
// recorded reason does not state the two prerequisites that justify deferring.
func ValidateProposerResolutionDeferral(pack DrawPack) error {
	deferred := DeferredIn(DrawPackFilename, ProposerResolutionSection)
	if len(deferred) != 1 {
		return fmt.Errorf(
			"deferral manifest declares %d deferred %s fixtures, want exactly 1",
			len(deferred), ProposerResolutionSection,
		)
	}
	if len(pack.ProposerResolution) != 1 {
		return fmt.Errorf(
			"%s holds %d %s fixtures, want exactly 1; a fixture was added or removed, "+
				"which needs an explicit decision about whether it can now be executed",
			DrawPackFilename, len(pack.ProposerResolution), ProposerResolutionSection,
		)
	}
	if got, want := pack.ProposerResolution[0].Name, deferred[0]; got != want {
		return fmt.Errorf(
			"%s holds %s fixture %q, but the deferral manifest names %q",
			DrawPackFilename, ProposerResolutionSection, got, want,
		)
	}

	// The reason must name the prerequisites, so a future reader learns what
	// unblocks the fixture rather than only that it was skipped.
	var reason string
	for _, fixture := range deferredFixtures {
		if fixture.Pack == DrawPackFilename && fixture.Section == ProposerResolutionSection {
			reason = fixture.Reason
		}
	}
	for _, required := range []string{"historical proposer resolver", "consensus-key history"} {
		if !strings.Contains(reason, required) {
			return fmt.Errorf(
				"deferral reason for %q does not state the prerequisite %q: %s",
				deferred[0], required, reason,
			)
		}
	}
	return nil
}

// CaseLedger records the vector cases a conformance run actually executed, so
// coverage can be asserted as a number rather than trusted.
//
// The zero value is ready to use.
type CaseLedger struct {
	executed []DeferredFixture
}

// Record notes that one case ran. Name may be empty for sections whose cases the
// pack does not name individually.
func (l *CaseLedger) Record(pack, section, name string) {
	l.executed = append(l.executed, DeferredFixture{Pack: pack, Section: section, Name: name})
}

// Count returns how many cases of one pack section were executed.
func (l *CaseLedger) Count(pack, section string) int {
	var n int
	for _, entry := range l.executed {
		if entry.Pack == pack && entry.Section == section {
			n++
		}
	}
	return n
}

// Total returns how many cases were executed across all packs.
func (l *CaseLedger) Total() int { return len(l.executed) }

// ValidateNoDeferredExecuted fails if any case the manifest defers was recorded
// as executed. Without this check a deferred fixture could be quietly folded
// into a passing run and inflate its apparent coverage.
func (l *CaseLedger) ValidateNoDeferredExecuted() error {
	for _, entry := range l.executed {
		for _, fixture := range deferredFixtures {
			if entry.Pack == fixture.Pack && entry.Section == fixture.Section && entry.Name == fixture.Name {
				return fmt.Errorf(
					"deferred fixture %q in %s/%s was recorded as executed; it must not be "+
						"counted until it runs against the real historical proposer resolver",
					fixture.Name, fixture.Pack, fixture.Section,
				)
			}
		}
	}
	return nil
}
