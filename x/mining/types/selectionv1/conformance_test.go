package selectionv1_test

// Conformance of the pure Selection V1 library against the two normative vector
// packs that cover it.
//
// Every case runs against the exported selectionv1 API — the same functions a
// consensus path or a public verifier would call. Nothing here reimplements a
// hash, a comparator or the count arithmetic: a test-local implementation would
// certify only that the test agrees with itself.
//
// Executed cases are recorded in a ledger and the totals are asserted at the
// end, so a case that stops running is a failure rather than a silent gap.

import (
	"errors"
	"testing"

	"github.com/twilight-project/twilight-core/internal/consensusvectors"
	"github.com/twilight-project/twilight-core/x/mining/types/selectionv1"
)

// ---------------------------------------------------------------------------
// Decoding helpers. These convert pack transport values into library types and
// fail the test on malformed fixture data, which is a fixture bug rather than an
// implementation result.
// ---------------------------------------------------------------------------

func mustDrawID(t *testing.T, hexValue string) selectionv1.DrawID {
	t.Helper()
	id, err := selectionv1.DrawIDFromHex(hexValue)
	if err != nil {
		t.Fatalf("decode draw id %q: %v", hexValue, err)
	}
	return id
}

func mustHash(t *testing.T, hexValue string) selectionv1.Hash {
	t.Helper()
	h, err := selectionv1.HashFromHex(hexValue)
	if err != nil {
		t.Fatalf("decode hash %q: %v", hexValue, err)
	}
	return h
}

func mustDrawIDs(t *testing.T, hexValues []string) []selectionv1.DrawID {
	t.Helper()
	ids := make([]selectionv1.DrawID, 0, len(hexValues))
	for _, value := range hexValues {
		ids = append(ids, mustDrawID(t, value))
	}
	return ids
}

func contextOf(chainID string, slotID, targetEpoch consensusvectors.U64) selectionv1.SelectionContext {
	return selectionv1.SelectionContext{
		ChainID:     chainID,
		SlotID:      slotID.Uint64(),
		TargetEpoch: targetEpoch.Uint64(),
	}
}

func observedWindow(t *testing.T, blocks []consensusvectors.ObservedBlock) []selectionv1.ObservedBlock {
	t.Helper()
	observed := make([]selectionv1.ObservedBlock, 0, len(blocks))
	for _, block := range blocks {
		observed = append(observed, selectionv1.ObservedBlock{
			Height:         block.Height.Uint64(),
			ProposerSlotID: block.ResolvedProposerSlotID.Uint64(),
			BlockHash:      mustHash(t, block.BlockHashHex),
		})
	}
	return observed
}

func assertEntriesEqual(t *testing.T, got []selectionv1.BeaconEntry, want []consensusvectors.BeaconEntry) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("included entries = %d, want %d", len(got), len(want))
	}
	for i := range want {
		wantHash := mustHash(t, want[i].BlockHashHex)
		if got[i].Height != want[i].Height.Uint64() ||
			got[i].ProposerSlotID != want[i].ProposerSlotID.Uint64() ||
			got[i].BlockHash != wantHash {
			t.Errorf(
				"entry %d = (h=%d, slot=%d, hash=%s), want (h=%d, slot=%d, hash=%s)",
				i, got[i].Height, got[i].ProposerSlotID, got[i].BlockHash,
				want[i].Height.Uint64(), want[i].ProposerSlotID.Uint64(), wantHash,
			)
		}
	}
}

func assertDrawIDsEqual(t *testing.T, label string, got []selectionv1.DrawID, wantHex []string) {
	t.Helper()
	if len(got) != len(wantHex) {
		t.Fatalf("%s = %d ids, want %d", label, len(got), len(wantHex))
	}
	for i := range wantHex {
		if want := mustDrawID(t, wantHex[i]); got[i] != want {
			t.Errorf("%s[%d] = %s, want %s", label, i, got[i], want)
		}
	}
}

// ---------------------------------------------------------------------------
// Draw pack (r2)
// ---------------------------------------------------------------------------

func TestDrawPackConformance(t *testing.T) {
	pack, err := consensusvectors.LoadDrawPack()
	if err != nil {
		t.Fatalf("load draw pack: %v", err)
	}
	ledger := &consensusvectors.CaseLedger{}
	const packName = consensusvectors.DrawPackFilename

	t.Run("primitives", func(t *testing.T) {
		runPrimitiveVectors(t, pack.Primitives, ledger)
	})
	t.Run("winner_count_vectors", func(t *testing.T) {
		runWinnerCountVectors(t, pack.WinnerCountVectors, ledger)
	})
	t.Run("end_to_end", func(t *testing.T) {
		runEndToEndVectors(t, pack.EndToEnd, ledger)
	})
	t.Run("timing_vectors", func(t *testing.T) {
		runTimingVectors(t, pack.TimingVectors, ledger)
	})
	t.Run("negative_vectors", func(t *testing.T) {
		runNegativeVectors(t, pack.NegativeVectors, ledger)
	})
	t.Run("comparator_vectors", func(t *testing.T) {
		runComparatorVectors(t, pack.ComparatorVectors, ledger)
	})
	t.Run("empty_set_cross_check", func(t *testing.T) {
		runEmptySetCrossCheck(t, pack.EmptySetCrossCheck, ledger)
	})

	// The proposer-resolution fixture is tracked and validated but deliberately
	// not executed; see the deferral manifest.
	t.Run("deferred_proposer_resolution", func(t *testing.T) {
		if err := consensusvectors.ValidateProposerResolutionDeferral(pack); err != nil {
			t.Fatalf("deferral manifest: %v", err)
		}
	})

	t.Run("coverage_ledger", func(t *testing.T) {
		if err := ledger.ValidateNoDeferredExecuted(); err != nil {
			t.Fatal(err)
		}
		sections := []struct {
			section string
			want    int
		}{
			{"primitives", 5},
			{"winner_count_vectors", consensusvectors.ExpectedWinnerCountVectors},
			{"end_to_end", 6},
			{"timing_vectors", consensusvectors.ExpectedTimingVectors},
			{"negative_vectors", consensusvectors.ExpectedDrawNegativeVectors},
			{"comparator_vectors", consensusvectors.ExpectedComparatorVectors},
			{"empty_set_cross_check", 1},
		}
		total := 0
		for _, section := range sections {
			if got := ledger.Count(packName, section.section); got != section.want {
				t.Errorf("executed %d %s cases, want %d", got, section.section, section.want)
			}
			total += section.want
		}
		if got := ledger.Count(packName, consensusvectors.ProposerResolutionSection); got != 0 {
			t.Errorf("executed %d proposer-resolution cases, want 0 (deferred)", got)
		}
		if got := ledger.Total(); got != total {
			t.Errorf("executed %d draw-pack cases in total, want %d", got, total)
		}
	})
}

func runPrimitiveVectors(t *testing.T, primitives consensusvectors.DrawPrimitives, ledger *consensusvectors.CaseLedger) {
	const packName = consensusvectors.DrawPackFilename
	const section = "primitives"

	t.Run("draw_id_v1", func(t *testing.T) {
		v := primitives.DrawIDV1
		secretHash := mustHash(t, v.ParticipationSecretHex)
		var secret [selectionv1.HashSize]byte
		copy(secret[:], secretHash[:])

		got, err := selectionv1.ComputeDrawID(secret, contextOf(v.ChainID, v.SlotID, v.TargetEpoch))
		if err != nil {
			t.Fatalf("ComputeDrawID: %v", err)
		}
		if want := mustDrawID(t, v.ExpectedDrawIDHex); got != want {
			t.Errorf("draw id = %s, want %s", got, want)
		}
		ledger.Record(packName, section, "draw_id_v1")
	})

	t.Run("candidate_set_hash_v1", func(t *testing.T) {
		v := primitives.CandidateSetHashV1
		got, err := selectionv1.ComputeCandidateSetHash(
			contextOf(v.ChainID, v.SlotID, v.TargetEpoch), mustDrawIDs(t, v.DrawIDsHex),
		)
		if err != nil {
			t.Fatalf("ComputeCandidateSetHash: %v", err)
		}
		if want := mustHash(t, v.ExpectedCandidateSetHashHex); got != want {
			t.Errorf("candidate set hash = %s, want %s", got, want)
		}
		ledger.Record(packName, section, "candidate_set_hash_v1")
	})

	t.Run("candidate_set_hash_empty_v1", func(t *testing.T) {
		v := primitives.CandidateSetHashEmptyV1
		if len(v.DrawIDsHex) != 0 {
			t.Fatalf("empty-set vector carries %d draw ids", len(v.DrawIDsHex))
		}
		got, err := selectionv1.ComputeCandidateSetHash(
			contextOf(v.ChainID, v.SlotID, v.TargetEpoch), mustDrawIDs(t, v.DrawIDsHex),
		)
		if err != nil {
			t.Fatalf("ComputeCandidateSetHash: %v", err)
		}
		want := mustHash(t, v.ExpectedCandidateSetHashHex)
		if got != want {
			t.Errorf("empty candidate set hash = %s, want %s", got, want)
		}
		// The empty set has a deterministic NONZERO digest: it is a real
		// commitment to "no candidates", not an absent one.
		if got == (selectionv1.Hash{}) {
			t.Error("empty candidate set hash is all zeroes; it must be a real digest")
		}
		ledger.Record(packName, section, "candidate_set_hash_empty_v1")
	})

	t.Run("beacon_hash_v1", func(t *testing.T) {
		v := primitives.BeaconHashV1
		entries := make([]selectionv1.BeaconEntry, 0, len(v.IncludedEntries))
		for _, e := range v.IncludedEntries {
			entries = append(entries, selectionv1.BeaconEntry{
				Height:         e.Height.Uint64(),
				ProposerSlotID: e.ProposerSlotID.Uint64(),
				BlockHash:      mustHash(t, e.BlockHashHex),
			})
		}
		got, err := selectionv1.ComputeBeaconHash(
			contextOf(v.ChainID, v.SlotID, v.TargetEpoch),
			mustHash(t, v.CandidateSetHashHex),
			v.BeaconStartHeight.Uint64(), v.BeaconEndHeight.Uint64(),
			entries,
		)
		if err != nil {
			t.Fatalf("ComputeBeaconHash: %v", err)
		}
		if want := mustHash(t, v.ExpectedBeaconHashHex); got != want {
			t.Errorf("beacon hash = %s, want %s", got, want)
		}
		if !v.CommittedHeightIntentionallyNo.Bool() {
			t.Error("vector no longer asserts that committed_height is absent from the preimage")
		}
		ledger.Record(packName, section, "beacon_hash_v1")
	})

	t.Run("ticket_v1", func(t *testing.T) {
		v := primitives.TicketV1
		got, err := selectionv1.ComputeTicket(
			contextOf(v.ChainID, v.SlotID, v.TargetEpoch),
			mustHash(t, v.CandidateSetHashHex),
			mustHash(t, v.BeaconHashHex),
			mustDrawID(t, v.DrawIDHex),
		)
		if err != nil {
			t.Fatalf("ComputeTicket: %v", err)
		}
		if want := mustHash(t, v.ExpectedTicketHex); got != want {
			t.Errorf("ticket = %s, want %s", got, want)
		}
		ledger.Record(packName, section, "ticket_v1")
	})
}

func runWinnerCountVectors(t *testing.T, vectors []consensusvectors.WinnerCountVector, ledger *consensusvectors.CaseLedger) {
	const packName = consensusvectors.DrawPackFilename
	const section = "winner_count_vectors"
	const denominator = 10_000

	for i, v := range vectors {
		t.Run(caseName(i, v.CandidateCount.Uint64(), v.SelectionRateBps.Uint64()), func(t *testing.T) {
			candidateCount := v.CandidateCount.Uint64()

			// The pack publishes the decomposition's intermediate terms, so the
			// test pins the order of operations and not merely the final K.
			if got := candidateCount / denominator; got != v.QuotientQ.Uint64() {
				t.Errorf("quotient q = %d, want %d", got, v.QuotientQ.Uint64())
			}
			if got := candidateCount % denominator; got != v.RemainderRem.Uint64() {
				t.Errorf("remainder rem = %d, want %d", got, v.RemainderRem.Uint64())
			}
			if !v.RateK.IsNull() {
				want := v.RateK.Uint64()
				got := v.QuotientQ.Uint64()*v.SelectionRateBps.Uint64() +
					(v.RemainderRem.Uint64()*v.SelectionRateBps.Uint64())/denominator
				if got != want {
					t.Errorf("rate_k = %d, want %d", got, want)
				}
			}

			got, err := selectionv1.SelectedCount(candidateCount, selectionv1.CountLimits{
				SelectionRateBps:    v.SelectionRateBps.Uint64(),
				SlotMaxSelected:     v.SlotMaxWinners.Uint64(),
				ProtocolMaxSelected: v.ProtocolMaxWinnersPerDraw.Uint64(),
			})
			if err != nil {
				t.Fatalf("SelectedCount: %v", err)
			}
			if want := v.ExpectedK.Uint64(); got != want {
				t.Errorf("K = %d, want %d", got, want)
			}
			ledger.Record(packName, section, "")
		})
	}
}

func caseName(index int, candidateCount, rateBps uint64) string {
	return "case_" + itoa(index) + "_C" + utoa(candidateCount) + "_r" + utoa(rateBps)
}

func runEndToEndVectors(t *testing.T, cases consensusvectors.DrawEndToEnd, ledger *consensusvectors.CaseLedger) {
	const packName = consensusvectors.DrawPackFilename
	const section = "end_to_end"

	t.Run("success_with_target_slot_exclusion", func(t *testing.T) {
		v := cases.SuccessWithTargetSlotExclusion
		result := evaluateSuccessCase(t, v)

		if got, want := result.Outcome.String(), v.ExpectedOutcome; got != want {
			t.Errorf("outcome = %s, want %s", got, want)
		}
		if got, want := result.CandidateSetHash, mustHash(t, v.DrawCommitment.CandidateSetHashHex); got != want {
			t.Errorf("candidate set hash = %s, want %s", got, want)
		}
		assertEntriesEqual(t, result.IncludedEntries, v.ExpectedIncludedEntries)
		if got, want := result.Stats.UsableCount, v.ExpectedUsableBlockCount.Uint64(); got != want {
			t.Errorf("usable count = %d, want %d", got, want)
		}
		if got, want := result.Stats.DistinctExternalProposers, v.ExpectedDistinctExternalProposers.Uint64(); got != want {
			t.Errorf("distinct external proposers = %d, want %d", got, want)
		}
		if !result.BeaconHashDefined {
			t.Fatal("beacon hash undefined on the success path")
		}
		if got, want := result.BeaconHash, mustHash(t, v.ExpectedBeaconHashHex); got != want {
			t.Errorf("beacon hash = %s, want %s", got, want)
		}
		if got, want := result.SelectedCount, v.ExpectedK.Uint64(); got != want {
			t.Errorf("K = %d, want %d", got, want)
		}

		// The full ranking is asserted, not only the selected prefix: the ranking
		// is what the selection is a prefix of, so a comparator fault that leaves
		// the first entry intact would otherwise pass.
		if len(result.Ranking) != len(v.ExpectedRanking) {
			t.Fatalf("ranking = %d entries, want %d", len(result.Ranking), len(v.ExpectedRanking))
		}
		for i, want := range v.ExpectedRanking {
			if got := result.Ranking[i].DrawID; got != mustDrawID(t, want.DrawIDHex) {
				t.Errorf("ranking[%d] draw id = %s, want %s", i, got, want.DrawIDHex)
			}
			if got := result.Ranking[i].Ticket; got != mustHash(t, want.TicketHex) {
				t.Errorf("ranking[%d] ticket = %s, want %s", i, got, want.TicketHex)
			}
		}
		assertDrawIDsEqual(t, "selected", result.SelectedDrawIDs, v.ExpectedWinnerDrawIDsHex)

		// The stated commitment height and beacon geometry must also be admissible.
		if err := selectionv1.ValidateCommitmentHeight(
			v.DrawCommitment.CommittedHeight.Uint64(),
			v.EpochNMinus1StartHeight.Uint64(),
			result.BeaconStartHeight,
		); err != nil {
			t.Errorf("stated commitment height rejected: %v", err)
		}
		if err := selectionv1.ValidateBeaconWindowFits(
			result.BeaconEndHeight, v.EpochNStartHeight.Uint64(),
		); err != nil {
			t.Errorf("stated beacon geometry rejected: %v", err)
		}
		ledger.Record(packName, section, "success_with_target_slot_exclusion")
	})

	t.Run("commitment_height_invariance", func(t *testing.T) {
		v := cases.CommitmentHeightInvariance
		base := cases.SuccessWithTargetSlotExclusion

		if len(v.CommitmentHeights) < 2 {
			t.Fatalf("invariance vector carries %d commitment heights, want at least 2", len(v.CommitmentHeights))
		}

		// The invariance is structural: committed_height is not a parameter of
		// DeriveBeaconWindow, ComputeBeaconHash, or any other V1 byte function, so
		// there is no input to vary. What is checked here is the pair of claims
		// that gives that structure its meaning — that every stated height in the
		// pre-beacon window really is admissible, and that the outcome derived
		// alongside each is the one the pack states.
		var first selectionv1.EvaluationResult
		for i, height := range v.CommitmentHeights {
			if err := selectionv1.ValidateCommitmentHeight(
				height.Uint64(), v.EpochNMinus1StartHeight.Uint64(), v.BeaconStartHeight.Uint64(),
			); err != nil {
				t.Fatalf("commitment height %d rejected: %v", height.Uint64(), err)
			}
			result := evaluateSuccessCase(t, base)
			if i == 0 {
				first = result
				continue
			}
			if result.BeaconHash != first.BeaconHash {
				t.Errorf("beacon hash changed with commitment height %d", height.Uint64())
			}
			assertDrawIDsEqual(t, "selected", result.SelectedDrawIDs, v.ExpectedSameWinnerDrawIDsHex)
		}
		if got, want := first.BeaconHash, mustHash(t, v.ExpectedSameBeaconHashHex); got != want {
			t.Errorf("beacon hash = %s, want %s", got, want)
		}
		assertDrawIDsEqual(t, "selected", first.SelectedDrawIDs, v.ExpectedSameWinnerDrawIDsHex)
		ledger.Record(packName, section, "commitment_height_invariance")
	})

	for _, tc := range []struct {
		name  string
		value consensusvectors.SmallCandidateCase
	}{
		{"no_candidates", cases.NoCandidates},
		{"single_candidate", cases.SingleCandidate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := tc.value
			if v.BeaconRequired.Bool() {
				t.Fatalf("%s vector expects a beacon; the short-circuit paths require none", tc.name)
			}
			result, err := selectionv1.Evaluate(selectionv1.EvaluationInput{
				Context:          contextOf(v.ChainID, v.SlotID, v.TargetEpoch),
				CandidateDrawIDs: mustDrawIDs(t, v.CandidateListDrawIDsHex),
			})
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if got, want := result.Outcome.String(), v.ExpectedOutcome; got != want {
				t.Errorf("outcome = %s, want %s", got, want)
			}
			if got, want := result.CandidateSetHash, mustHash(t, v.ExpectedCandidateSetHashHex); got != want {
				t.Errorf("candidate set hash = %s, want %s", got, want)
			}
			if got, want := result.SelectedCount, v.ExpectedK.Uint64(); got != want {
				t.Errorf("K = %d, want %d", got, want)
			}
			// No beacon is required, so none may be claimed.
			if result.BeaconHashDefined {
				t.Error("beacon hash reported as defined on a path that requires no beacon")
			}
			assertDrawIDsEqual(t, "selected", result.SelectedDrawIDs, v.ExpectedWinnerDrawIDsHex)
			ledger.Record(packName, section, tc.name)
		})
	}

	for _, tc := range []struct {
		name  string
		value consensusvectors.InvalidBeaconCase
	}{
		{"no_valid_beacon_insufficient_usable_blocks", cases.NoValidBeaconInsufficientUsable},
		{"no_valid_beacon_insufficient_distinct_proposers", cases.NoValidBeaconInsufficientDistinct},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := tc.value
			observed := observedWindow(t, v.ObservedBeaconWindow)
			if len(observed) == 0 {
				t.Fatal("invalid-beacon vector carries no observed window")
			}

			// The vector states the window contents and the offset but not the
			// epoch anchor; the anchor is recovered mechanically from the two,
			// which is arithmetic on stated data rather than a protocol choice.
			offset := v.DrawParams.BeaconStartOffsetBlocks.Uint64()
			if observed[0].Height < offset {
				t.Fatalf("window starts at %d, below the offset %d", observed[0].Height, offset)
			}
			anchor := observed[0].Height - offset

			// Two or more candidates are needed to reach the beacon path at all;
			// the protocol short-circuits zero and one candidate before any beacon
			// is required. Which candidates they are cannot affect what this case
			// asserts — the usable count, the distinct-proposer count and the
			// resulting outcome are functions of the window alone. The count limits
			// below are likewise inert, because a failed beacon selects nobody.
			candidates := mustDrawIDs(t, []string{
				"0000000000000000000000000000000000000000000000000000000000000001",
				"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
			})
			result, err := selectionv1.Evaluate(selectionv1.EvaluationInput{
				Context:                 contextOf(v.ChainID, v.SlotID, v.TargetEpoch),
				CandidateDrawIDs:        candidates,
				EpochNMinus1StartHeight: anchor,
				BeaconStartOffsetBlocks: v.DrawParams.BeaconStartOffsetBlocks.Uint64(),
				BeaconWindowBlocks:      v.DrawParams.BeaconWindowBlocks.Uint64(),
				ObservedWindow:          observed,
				Thresholds: selectionv1.BeaconThresholds{
					MinExternalBeaconBlocks:      v.DrawParams.MinExternalBeaconBlocks.Uint64(),
					MinDistinctExternalProposers: v.DrawParams.MinDistinctExternalProposers.Uint64(),
				},
				Limits: selectionv1.CountLimits{
					SelectionRateBps:    2_500,
					SlotMaxSelected:     100,
					ProtocolMaxSelected: 1_000,
				},
			})
			if err != nil {
				t.Fatalf("Evaluate: %v", err)
			}
			if got, want := result.Outcome.String(), v.ExpectedOutcome; got != want {
				t.Errorf("outcome = %s, want %s", got, want)
			}
			if got, want := result.Stats.UsableCount, v.ExpectedUsableBlockCount.Uint64(); got != want {
				t.Errorf("usable count = %d, want %d", got, want)
			}
			if got, want := result.Stats.DistinctExternalProposers, v.ExpectedDistinctExternalProposers.Uint64(); got != want {
				t.Errorf("distinct external proposers = %d, want %d", got, want)
			}
			// BeaconHashV1 is undefined for an invalid beacon; claiming one would
			// assert randomness the protocol says does not exist here.
			if result.BeaconHashDefined != v.BeaconHashDefined.Bool() {
				t.Errorf("beacon hash defined = %v, want %v", result.BeaconHashDefined, v.BeaconHashDefined.Bool())
			}
			if len(result.SelectedDrawIDs) != 0 {
				t.Errorf("selected %d participants, want 0 under NO_VALID_BEACON", len(result.SelectedDrawIDs))
			}
			ledger.Record(packName, section, tc.name)
		})
	}
}

func evaluateSuccessCase(t *testing.T, v consensusvectors.SuccessCase) selectionv1.EvaluationResult {
	t.Helper()
	result, err := selectionv1.Evaluate(selectionv1.EvaluationInput{
		Context:                 contextOf(v.ChainID, v.SlotID, v.TargetEpoch),
		CandidateDrawIDs:        mustDrawIDs(t, v.CandidateListDrawIDsHex),
		EpochNMinus1StartHeight: v.EpochNMinus1StartHeight.Uint64(),
		BeaconStartOffsetBlocks: v.DrawParams.BeaconStartOffsetBlocks.Uint64(),
		BeaconWindowBlocks:      v.DrawParams.BeaconWindowBlocks.Uint64(),
		ObservedWindow:          observedWindow(t, v.ObservedBeaconWindow),
		Thresholds: selectionv1.BeaconThresholds{
			MinExternalBeaconBlocks:      v.DrawParams.MinExternalBeaconBlocks.Uint64(),
			MinDistinctExternalProposers: v.DrawParams.MinDistinctExternalProposers.Uint64(),
		},
		Limits: selectionv1.CountLimits{
			SelectionRateBps:    v.DrawCommitment.SelectionRateBps.Uint64(),
			SlotMaxSelected:     v.DrawCommitment.SlotMaxWinners.Uint64(),
			ProtocolMaxSelected: v.DrawParams.ProtocolMaxWinnersPerDraw.Uint64(),
		},
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	return result
}

func runTimingVectors(t *testing.T, vectors []consensusvectors.TimingVector, ledger *consensusvectors.CaseLedger) {
	const packName = consensusvectors.DrawPackFilename
	const section = "timing_vectors"

	for _, v := range vectors {
		t.Run(v.Name, func(t *testing.T) {
			switch v.Name {
			case "valid_commit_first_block", "valid_commit_last_pre_beacon_block", "commit_at_beacon_start_rejected":
				start, _, err := selectionv1.DeriveBeaconWindow(
					v.EpochNMinus1StartHeight.Uint64(),
					v.BeaconStartOffsetBlocks.Uint64(),
					v.BeaconWindowBlocks.Uint64(),
				)
				if err != nil {
					t.Fatalf("DeriveBeaconWindow: %v", err)
				}
				err = selectionv1.ValidateCommitmentHeight(
					v.CommittedHeight.Uint64(), v.EpochNMinus1StartHeight.Uint64(), start,
				)
				switch v.Expected {
				case "ACCEPT":
					if err != nil {
						t.Fatalf("commitment height %d rejected: %v", v.CommittedHeight.Uint64(), err)
					}
				case "REJECT_COMMITMENT_WINDOW":
					if !errors.Is(err, selectionv1.ErrCommitmentWindow) {
						t.Fatalf("err = %v, want ErrCommitmentWindow", err)
					}
				default:
					t.Fatalf("unhandled expectation %q", v.Expected)
				}

			case "beacon_fit_rejection":
				start, end, err := selectionv1.DeriveBeaconWindow(
					v.EpochNMinus1StartHeight.Uint64(),
					v.BeaconStartOffsetBlocks.Uint64(),
					v.BeaconWindowBlocks.Uint64(),
				)
				if err != nil {
					t.Fatalf("DeriveBeaconWindow: %v", err)
				}
				// The derived geometry itself is pinned, so a rejection cannot pass
				// for the wrong reason.
				if want := v.DerivedBeaconStartHeight.Uint64(); start != want {
					t.Errorf("derived beacon start = %d, want %d", start, want)
				}
				if want := v.DerivedBeaconEndHeight.Uint64(); end != want {
					t.Errorf("derived beacon end = %d, want %d", end, want)
				}
				if got, want := v.EpochNStartHeight.Uint64()-2, v.LatestPermittedBeaconEndHeight.Uint64(); got != want {
					t.Errorf("latest permitted beacon end = %d, want %d", got, want)
				}
				if err := selectionv1.ValidateBeaconWindowFits(end, v.EpochNStartHeight.Uint64()); !errors.Is(
					err, selectionv1.ErrBeaconWindowDoesNotFit,
				) {
					t.Fatalf("err = %v, want ErrBeaconWindowDoesNotFit", err)
				}

			// These two vectors state only a target-epoch start and a published
			// height, so they can exercise only the upper bound of r6 §47. The
			// remaining arguments are chosen so that no other bound can decide the
			// outcome, rather than invented as missing vector data: an epoch N-1
			// anchor of 0 is satisfied by every height, and a candidate count of 0
			// carries no beacon-relative bound, so the upper bound alone determines
			// the result. The complete rule is covered by focused unit tests.
			case "late_result_rejection":
				err := selectionv1.ValidateResultPublicationHeight(
					v.PublishedHeight.Uint64(), 0, v.TargetEpochStartHeight.Uint64(), 0, 0,
				)
				if !errors.Is(err, selectionv1.ErrLateResult) {
					t.Fatalf("err = %v, want ErrLateResult", err)
				}

			case "valid_result_last_block":
				if err := selectionv1.ValidateResultPublicationHeight(
					v.PublishedHeight.Uint64(), 0, v.TargetEpochStartHeight.Uint64(), 0, 0,
				); err != nil {
					t.Fatalf("published height %d rejected: %v", v.PublishedHeight.Uint64(), err)
				}

			default:
				t.Fatalf("timing vector %q has no mapped rule; a new case needs an explicit binding", v.Name)
			}
			ledger.Record(packName, section, v.Name)
		})
	}
}

func runNegativeVectors(t *testing.T, vectors []consensusvectors.DrawNegativeVector, ledger *consensusvectors.CaseLedger) {
	const packName = consensusvectors.DrawPackFilename
	const section = "negative_vectors"

	// The canonical Selection the rejection cases are stated against.
	sc := selectionv1.SelectionContext{ChainID: "twilight-1", SlotID: 7, TargetEpoch: 1042}
	canonical := mustDrawIDs(t, []string{
		"0000000000000000000000000000000000000000000000000000000000000001",
		"4779843297ece3a95590eee123f80a846cc2cecbfcda1fd88c7d2b3cc2ce91e3",
		"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
	})

	for _, v := range vectors {
		t.Run(v.Name, func(t *testing.T) {
			switch v.Name {
			case "candidate_list_not_strictly_sorted":
				list := mustDrawIDs(t, v.CandidateListDrawIDsHex)
				if err := selectionv1.ValidateCanonicalDrawIDs(list); !errors.Is(
					err, selectionv1.ErrCandidateListNotCanonical,
				) {
					t.Fatalf("err = %v, want ErrCandidateListNotCanonical", err)
				}
				// The same list must also be refused a candidate-set commitment,
				// so a non-canonical ordering cannot be hashed into one.
				if _, err := selectionv1.ComputeCandidateSetHash(sc, list); !errors.Is(
					err, selectionv1.ErrCandidateListNotCanonical,
				) {
					t.Fatalf("ComputeCandidateSetHash err = %v, want ErrCandidateListNotCanonical", err)
				}
				// Positive control: the same IDs in canonical order are accepted.
				if err := selectionv1.ValidateCanonicalDrawIDs(canonical); err != nil {
					t.Fatalf("canonical list rejected: %v", err)
				}

			case "candidate_set_hash_mismatch":
				expected := mustHash(t, v.ExpectedCandidateSetHashHex)
				published := mustHash(t, v.PublishedCandidateSetHash)
				if err := selectionv1.VerifyCandidateSetHash(sc, published, canonical); !errors.Is(
					err, selectionv1.ErrCandidateSetHashMismatch,
				) {
					t.Fatalf("err = %v, want ErrCandidateSetHashMismatch", err)
				}
				if err := selectionv1.VerifyCandidateSetHash(sc, expected, canonical); err != nil {
					t.Fatalf("expected hash rejected: %v", err)
				}

			case "candidate_count_mismatch":
				list := canonical[:v.CandidateListLength.Uint64()]
				if err := selectionv1.ValidateCandidateCount(
					v.DrawCommitmentCandidateCnt.Uint64(), list,
				); !errors.Is(err, selectionv1.ErrCandidateCountMismatch) {
					t.Fatalf("err = %v, want ErrCandidateCountMismatch", err)
				}
				if err := selectionv1.ValidateCandidateCount(
					v.CandidateListCandidateCount.Uint64(), list,
				); err != nil {
					t.Fatalf("matching count rejected: %v", err)
				}

			case "wrong_published_winner":
				expected := mustDrawIDs(t, v.ExpectedWinnerDrawIDsHex)
				published := mustDrawIDs(t, v.PublishedWinnerDrawIDsHex)
				if err := selectionv1.VerifySelectedDrawIDs(expected, published); !errors.Is(
					err, selectionv1.ErrSelectedSetMismatch,
				) {
					t.Fatalf("err = %v, want ErrSelectedSetMismatch", err)
				}
				if err := selectionv1.VerifySelectedDrawIDs(expected, expected); err != nil {
					t.Fatalf("identical lists rejected: %v", err)
				}

			case "wrong_beacon_hash":
				expected := mustHash(t, v.ExpectedBeaconHashHex)
				published := mustHash(t, v.PublishedBeaconHashHex)
				if err := selectionv1.VerifyBeaconHash(expected, published); !errors.Is(
					err, selectionv1.ErrBeaconHashMismatch,
				) {
					t.Fatalf("err = %v, want ErrBeaconHashMismatch", err)
				}
				if err := selectionv1.VerifyBeaconHash(expected, expected); err != nil {
					t.Fatalf("identical hashes rejected: %v", err)
				}

			case "duplicate_winner":
				ids := mustDrawIDs(t, v.WinnerDrawIDsHex)
				if err := selectionv1.ValidateSelectedList(v.WinnerCount.Uint64(), ids); !errors.Is(
					err, selectionv1.ErrDuplicateSelectedID,
				) {
					t.Fatalf("err = %v, want ErrDuplicateSelectedID", err)
				}

			case "non_32_byte_winner":
				if len(v.WinnerDrawIDsHex) != 1 {
					t.Fatalf("vector carries %d ids, want 1", len(v.WinnerDrawIDsHex))
				}
				// Rejection happens at the decode boundary: an ID of the wrong
				// length is never materialized, so it can never be hashed.
				_, err := selectionv1.DrawIDFromHex(v.WinnerDrawIDsHex[0])
				if !errors.Is(err, selectionv1.ErrInvalidLength) {
					t.Fatalf("err = %v, want ErrInvalidLength", err)
				}
				if _, err := selectionv1.DrawIDFromBytes(make([]byte, 31)); !errors.Is(
					err, selectionv1.ErrInvalidLength,
				) {
					t.Fatalf("31-byte err = %v, want ErrInvalidLength", err)
				}

			case "draw_result_key_mismatch":
				commitment := selectionv1.SelectionContext{
					ChainID:     sc.ChainID,
					SlotID:      v.CommitmentSlotID.Uint64(),
					TargetEpoch: v.CommitmentTargetEpoch.Uint64(),
				}
				result := selectionv1.SelectionContext{
					ChainID:     sc.ChainID,
					SlotID:      v.ResultSlotID.Uint64(),
					TargetEpoch: v.ResultTargetEpoch.Uint64(),
				}
				if err := selectionv1.ValidateSelectionKey(commitment, result); !errors.Is(
					err, selectionv1.ErrSelectionKeyMismatch,
				) {
					t.Fatalf("err = %v, want ErrSelectionKeyMismatch", err)
				}
				if err := selectionv1.ValidateSelectionKey(commitment, commitment); err != nil {
					t.Fatalf("identical keys rejected: %v", err)
				}

			case "winner_count_list_length_mismatch":
				ids := mustDrawIDs(t, v.WinnerDrawIDsHex)
				if err := selectionv1.ValidateSelectedList(v.WinnerCount.Uint64(), ids); !errors.Is(
					err, selectionv1.ErrSelectedCountMismatch,
				) {
					t.Fatalf("err = %v, want ErrSelectedCountMismatch", err)
				}
				if err := selectionv1.ValidateSelectedList(uint64(len(ids)), ids); err != nil {
					t.Fatalf("matching count rejected: %v", err)
				}

			default:
				t.Fatalf("negative vector %q has no mapped rejection; a new case needs an explicit binding", v.Name)
			}

			if v.ExpectedError == "" {
				t.Error("vector declares no expected error code")
			}
			ledger.Record(packName, section, v.Name)
		})
	}
}

func runComparatorVectors(t *testing.T, vectors []consensusvectors.ComparatorVector, ledger *consensusvectors.CaseLedger) {
	const packName = consensusvectors.DrawPackFilename
	const section = "comparator_vectors"

	for _, v := range vectors {
		t.Run(v.Name, func(t *testing.T) {
			candidates := make([]selectionv1.RankedCandidate, 0, len(v.Candidates))
			for _, c := range v.Candidates {
				candidates = append(candidates, selectionv1.RankedCandidate{
					DrawID: mustDrawID(t, c.DrawIDHex),
					Ticket: mustHash(t, c.TicketHex),
				})
			}
			// The vector is synthetic: identical tickets force the draw-ID
			// tie-break to decide the order.
			synthetic := mustHash(t, v.SyntheticTicketHex)
			for i, c := range candidates {
				if c.Ticket != synthetic {
					t.Fatalf("candidate %d ticket is not the synthetic tie value", i)
				}
			}

			ranked := selectionv1.RankCandidates(candidates)
			got := make([]selectionv1.DrawID, 0, len(ranked))
			for _, c := range ranked {
				got = append(got, c.DrawID)
			}
			assertDrawIDsEqual(t, "ranked order", got, v.ExpectedOrderDrawIDHex)

			// Ranking must not reorder the caller's slice underneath it.
			for i, c := range v.Candidates {
				if candidates[i].DrawID != mustDrawID(t, c.DrawIDHex) {
					t.Fatalf("RankCandidates mutated its input at index %d", i)
				}
			}

			// The comparator must be antisymmetric, or two implementations could
			// order a tie differently.
			if a, b := ranked[0], ranked[1]; selectionv1.CompareCandidates(a, b) >= 0 ||
				selectionv1.CompareCandidates(b, a) <= 0 ||
				selectionv1.CompareCandidates(a, a) != 0 {
				t.Error("comparator is not a strict antisymmetric order on the tie case")
			}
			ledger.Record(packName, section, v.Name)
		})
	}
}

func runEmptySetCrossCheck(t *testing.T, check consensusvectors.EmptySetCrossCheck, ledger *consensusvectors.CaseLedger) {
	const packName = consensusvectors.DrawPackFilename

	hashA, err := selectionv1.ComputeCandidateSetHash(
		contextOf(check.InputA.ChainID, check.InputA.SlotID, check.InputA.TargetEpoch),
		mustDrawIDs(t, check.InputA.DrawIDsHex),
	)
	if err != nil {
		t.Fatalf("hash A: %v", err)
	}
	hashB, err := selectionv1.ComputeCandidateSetHash(
		contextOf(check.InputB.ChainID, check.InputB.SlotID, check.InputB.TargetEpoch),
		mustDrawIDs(t, check.InputB.DrawIDsHex),
	)
	if err != nil {
		t.Fatalf("hash B: %v", err)
	}

	if want := mustHash(t, check.ExpectedHashAHex); hashA != want {
		t.Errorf("hash A = %s, want %s", hashA, want)
	}
	if want := mustHash(t, check.ExpectedHashBHex); hashB != want {
		t.Errorf("hash B = %s, want %s", hashB, want)
	}
	if equal := hashA == hashB; equal != check.ExpectedEqual.Bool() {
		t.Errorf("hashes equal = %v, want %v", equal, check.ExpectedEqual.Bool())
	}
	ledger.Record(packName, "empty_set_cross_check", "empty_set_cross_check")
}

// ---------------------------------------------------------------------------
// SelectedDrawIDsHashV1 pack (r1)
// ---------------------------------------------------------------------------

func TestSelectedDrawIDsPackConformance(t *testing.T) {
	pack, err := consensusvectors.LoadSelectedDrawIDsPack()
	if err != nil {
		t.Fatalf("load pack: %v", err)
	}
	ledger := &consensusvectors.CaseLedger{}
	const packName = consensusvectors.SelectedDrawIDsPackFilename

	// The pack declares the domain tag it was generated against. Cross-checking it
	// against the library constant catches a domain-separation drift directly,
	// rather than leaving it to show up as an unexplained digest mismatch.
	if got, want := pack.Domain, selectionv1.DomainSelectedDrawIDs; got != want {
		t.Fatalf("pack domain = %q, library constant = %q", got, want)
	}

	byName := make(map[string]consensusvectors.SelectedDrawIDsVector, len(pack.Vectors))
	for _, v := range pack.Vectors {
		byName[v.Name] = v
	}

	t.Run("vectors", func(t *testing.T) {
		for _, v := range pack.Vectors {
			t.Run(v.Name, func(t *testing.T) {
				got, err := selectionv1.ComputeSelectedDrawIDsHash(
					contextOf(v.ChainID, v.SlotID, v.TargetEpoch),
					mustDrawIDs(t, v.SelectedDrawIDs),
				)
				if err != nil {
					t.Fatalf("ComputeSelectedDrawIDsHash: %v", err)
				}
				if want := mustHash(t, v.ExpectedHash); got != want {
					t.Errorf("hash = %s, want %s", got, want)
				}
				ledger.Record(packName, "vectors", v.Name)
			})
		}
	})

	// Each negative requirement is asserted as the specific property the pack
	// states, not as a bare inequality.
	t.Run("negative_requirements", func(t *testing.T) {
		t.Run("reordering selected ids changes the hash", func(t *testing.T) {
			ordered, ok := byName["ordered-three"]
			if !ok {
				t.Fatal("vector ordered-three is missing")
			}
			reordered, ok := byName["reordered-three"]
			if !ok {
				t.Fatal("vector reordered-three is missing")
			}
			// Same multiset of IDs, different order: the pack states BOTH digests,
			// so the assertion is that each order yields its own stated digest and
			// that the two differ — not merely that something changed.
			if !sameMultiset(ordered.SelectedDrawIDs, reordered.SelectedDrawIDs) {
				t.Fatal("the two vectors do not carry the same ids in different orders")
			}
			orderedHash := hashOf(t, ordered)
			reorderedHash := hashOf(t, reordered)
			if orderedHash != mustHash(t, ordered.ExpectedHash) {
				t.Error("ordered-three does not match its stated digest")
			}
			if reorderedHash != mustHash(t, reordered.ExpectedHash) {
				t.Error("reordered-three does not match its stated digest")
			}
			if orderedHash == reorderedHash {
				t.Error("reordering did not change the digest; order is not committed")
			}
			ledger.Record(packName, "negative_requirements", "reordering")
		})

		t.Run("count is derived from exact list length", func(t *testing.T) {
			// The count is a U64BE field of the preimage and is derived from the
			// list, never supplied by a caller — the function takes no count
			// argument. Its effect is demonstrated by three stated lengths that
			// all yield distinct digests under one identical context.
			lengths := []string{"empty", "single", "ordered-three"}
			seen := make(map[selectionv1.Hash]string, len(lengths))
			for _, name := range lengths {
				v, ok := byName[name]
				if !ok {
					t.Fatalf("vector %s is missing", name)
				}
				h := hashOf(t, v)
				if previous, clash := seen[h]; clash {
					t.Fatalf("vectors %s and %s share a digest despite different lengths", previous, name)
				}
				seen[h] = name
			}
			// Truncating a list changes the digest, because n changes with it.
			three := byName["ordered-three"]
			truncated, err := selectionv1.ComputeSelectedDrawIDsHash(
				contextOf(three.ChainID, three.SlotID, three.TargetEpoch),
				mustDrawIDs(t, three.SelectedDrawIDs[:2]),
			)
			if err != nil {
				t.Fatalf("ComputeSelectedDrawIDsHash: %v", err)
			}
			if truncated == hashOf(t, three) {
				t.Error("truncating the list did not change the digest")
			}
			ledger.Record(packName, "negative_requirements", "count-from-length")
		})

		t.Run("every selected id is exactly 32 raw bytes", func(t *testing.T) {
			for _, size := range []int{0, 31, 33, 64} {
				if _, err := selectionv1.DrawIDFromBytes(make([]byte, size)); !errors.Is(
					err, selectionv1.ErrInvalidLength,
				) {
					t.Errorf("%d-byte draw id: err = %v, want ErrInvalidLength", size, err)
				}
			}
			if _, err := selectionv1.DrawIDFromBytes(make([]byte, 32)); err != nil {
				t.Errorf("32-byte draw id rejected: %v", err)
			}
			ledger.Record(packName, "negative_requirements", "id-length")
		})
	})

	t.Run("coverage_ledger", func(t *testing.T) {
		if err := ledger.ValidateNoDeferredExecuted(); err != nil {
			t.Fatal(err)
		}
		if got, want := ledger.Count(packName, "vectors"), consensusvectors.ExpectedSelectedDrawIDsVectors; got != want {
			t.Errorf("executed %d vectors, want %d", got, want)
		}
		if got, want := ledger.Count(packName, "negative_requirements"), consensusvectors.ExpectedSelectedNegativeReqs; got != want {
			t.Errorf("executed %d negative requirements, want %d", got, want)
		}
	})
}

func hashOf(t *testing.T, v consensusvectors.SelectedDrawIDsVector) selectionv1.Hash {
	t.Helper()
	h, err := selectionv1.ComputeSelectedDrawIDsHash(
		contextOf(v.ChainID, v.SlotID, v.TargetEpoch),
		mustDrawIDs(t, v.SelectedDrawIDs),
	)
	if err != nil {
		t.Fatalf("ComputeSelectedDrawIDsHash: %v", err)
	}
	return h
}

func sameMultiset(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[string]int, len(a))
	for _, v := range a {
		counts[v]++
	}
	for _, v := range b {
		counts[v]--
		if counts[v] < 0 {
			return false
		}
	}
	return true
}

// itoa and utoa keep subtest names allocation-simple without pulling fmt into
// the hot assertion paths.
func itoa(v int) string { return utoa(uint64(v)) }

func utoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
