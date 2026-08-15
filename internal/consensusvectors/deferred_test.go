package consensusvectors

import (
	"strings"
	"testing"
)

// The deferral manifest exists to stop a tracked-but-unexecuted fixture from
// rotting silently. These tests exercise each way that could happen, because a
// guard that is never shown to fire is indistinguishable from no guard.

func TestProposerResolutionDeferralMatchesPack(t *testing.T) {
	pack, err := LoadDrawPack()
	if err != nil {
		t.Fatalf("load draw pack: %v", err)
	}
	if err := ValidateProposerResolutionDeferral(pack); err != nil {
		t.Fatalf("manifest does not match the pack: %v", err)
	}

	deferred := DeferredIn(DrawPackFilename, ProposerResolutionSection)
	if len(deferred) != 1 {
		t.Fatalf("manifest declares %d deferred fixtures for %s, want 1", len(deferred), ProposerResolutionSection)
	}
	if got, want := deferred[0], "validator_update_effective_u_plus_2"; got != want {
		t.Errorf("deferred fixture = %q, want %q", got, want)
	}
	// Identity is by name. Position must never be load-bearing, or reordering a
	// pack would silently retarget the deferral onto a different case.
	if got := pack.ProposerResolution[0].Name; got != deferred[0] {
		t.Errorf("pack fixture name = %q, manifest name = %q", got, deferred[0])
	}
}

func TestProposerResolutionDeferralDetectsDisappearance(t *testing.T) {
	pack, err := LoadDrawPack()
	if err != nil {
		t.Fatalf("load draw pack: %v", err)
	}
	pack.ProposerResolution = nil
	err = ValidateProposerResolutionDeferral(pack)
	if err == nil {
		t.Fatal("a vanished fixture was accepted")
	}
	if !strings.Contains(err.Error(), "want exactly 1") {
		t.Errorf("error does not report the cardinality breach: %v", err)
	}
}

func TestProposerResolutionDeferralDetectsDuplication(t *testing.T) {
	pack, err := LoadDrawPack()
	if err != nil {
		t.Fatalf("load draw pack: %v", err)
	}
	// A second fixture is not automatically covered by the existing deferral: it
	// needs its own decision about whether it can be executed now.
	pack.ProposerResolution = append(pack.ProposerResolution, pack.ProposerResolution[0])
	if err := ValidateProposerResolutionDeferral(pack); err == nil {
		t.Fatal("a duplicated fixture was accepted")
	}
}

func TestProposerResolutionDeferralDetectsRename(t *testing.T) {
	pack, err := LoadDrawPack()
	if err != nil {
		t.Fatalf("load draw pack: %v", err)
	}
	pack.ProposerResolution[0].Name = "some_other_fixture"
	if err := ValidateProposerResolutionDeferral(pack); err == nil {
		t.Fatal("a renamed fixture was accepted")
	}
}

// TestDeferralReasonStatesPrerequisites keeps the manifest from degenerating
// into "skipped, reason unknown".
func TestDeferralReasonStatesPrerequisites(t *testing.T) {
	for _, fixture := range DeferredFixtures() {
		if fixture.Reason == "" {
			t.Errorf("deferred fixture %q carries no reason", fixture.Name)
		}
	}
	pack, err := LoadDrawPack()
	if err != nil {
		t.Fatalf("load draw pack: %v", err)
	}
	if err := ValidateProposerResolutionDeferral(pack); err != nil {
		t.Fatalf("reason check failed: %v", err)
	}
}

func TestDeferredFixturesReturnsCopy(t *testing.T) {
	first := DeferredFixtures()
	if len(first) == 0 {
		t.Fatal("manifest is empty")
	}
	first[0].Name = "mutated"
	if DeferredFixtures()[0].Name == "mutated" {
		t.Error("DeferredFixtures exposed the manifest for mutation")
	}
}

// TestCaseLedgerRejectsExecutedDeferral is the guard that stops a deferred
// fixture from being quietly folded into a passing run.
func TestCaseLedgerRejectsExecutedDeferral(t *testing.T) {
	fixture := DeferredFixtures()[0]

	var clean CaseLedger
	clean.Record(fixture.Pack, "primitives", "draw_id_v1")
	if err := clean.ValidateNoDeferredExecuted(); err != nil {
		t.Fatalf("a non-deferred case was rejected: %v", err)
	}

	var dirty CaseLedger
	dirty.Record(fixture.Pack, fixture.Section, fixture.Name)
	err := dirty.ValidateNoDeferredExecuted()
	if err == nil {
		t.Fatal("executing a deferred fixture was accepted")
	}
	if !strings.Contains(err.Error(), fixture.Name) {
		t.Errorf("error does not name the offending fixture: %v", err)
	}
}

func TestCaseLedgerCounts(t *testing.T) {
	var ledger CaseLedger
	if got := ledger.Total(); got != 0 {
		t.Errorf("empty ledger total = %d, want 0", got)
	}
	ledger.Record(DrawPackFilename, "primitives", "a")
	ledger.Record(DrawPackFilename, "primitives", "b")
	ledger.Record(RewardPackFilename, "emission_vectors", "")

	if got := ledger.Count(DrawPackFilename, "primitives"); got != 2 {
		t.Errorf("draw primitives = %d, want 2", got)
	}
	if got := ledger.Count(RewardPackFilename, "emission_vectors"); got != 1 {
		t.Errorf("reward emission = %d, want 1", got)
	}
	if got := ledger.Count(DrawPackFilename, "timing_vectors"); got != 0 {
		t.Errorf("unrecorded section = %d, want 0", got)
	}
	if got := ledger.Total(); got != 3 {
		t.Errorf("total = %d, want 3", got)
	}
}
