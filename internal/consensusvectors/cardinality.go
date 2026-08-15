package consensusvectors

// Expected section cardinalities of the tracked packs.
//
// These are part of the conformance contract, not a convenience. A harness that
// says it executes "every case in the section" proves nothing on its own: if a
// pack revision drops a section from nine cases to four, the loop still passes
// while covering less. Asserting the count turns silent shrinkage into a
// failure, and asserting it here — rather than inside one test file — lets every
// consumer of a pack check the same number.
//
// A count changes only when a pack revision changes, and a pack revision is
// itself asserted at load time, so the two checks fail together and point at the
// same cause.
const (
	// Draw pack (r2).
	ExpectedPrimitiveVectors        = 5
	ExpectedWinnerCountVectors      = 10
	ExpectedEndToEndCases           = 6
	ExpectedTimingVectors           = 6
	ExpectedDrawNegativeVectors     = 9
	ExpectedComparatorVectors       = 1
	ExpectedEmptySetCrossChecks     = 1
	ExpectedProposerResolutionCases = 1

	// SelectedDrawIDsHashV1 pack (r1).
	ExpectedSelectedDrawIDsVectors = 7
	ExpectedSelectedNegativeReqs   = 3

	// Reward pack (r1).
	ExpectedEmissionVectors        = 6
	ExpectedAllocationVectors      = 5
	ExpectedPoolVectors            = 2
	ExpectedRequiredAssertions     = 6
	ExpectedNegativeDiscriminators = 2
)
