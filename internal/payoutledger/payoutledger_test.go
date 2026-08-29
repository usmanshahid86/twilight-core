package payoutledger_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/twilight-project/twilight-core/internal/payoutledger"
)

// The inventory is only worth having if it refuses what it cannot follow. A
// parser that silently skipped an unreadable call site would report a SMALLER
// set of sending modules, which is precisely the answer that makes an
// out-of-date exemption list look correct.
//
// Each fixture is a shape a real edit could produce.
func TestUnresolvableCallSitesAreRefused(t *testing.T) {
	for _, tc := range []struct {
		dir     string
		because string
	}{
		{"nested_selector", "a sender reached through a nested selector cannot be attributed"},
		{"not_modulename", "a constant other than ModuleName must not be guessed at"},
		{"variable_sender", "a sender held in a variable cannot be followed statically"},
		{"dot_import", "a dot-import lets ModuleName appear under no qualifier"},
		{"unknown_alias", "a qualifier matching no import cannot be resolved"},
		{"external_package", "a module outside this repository cannot be attributed"},
		{"too_few_args", "a call without a sender argument cannot be read"},
		{"method_value", "a callee stored in a variable separates the call from its module"},
		{"method_value_as_arg", "a method value handed elsewhere cannot be followed"},
	} {
		t.Run(tc.dir, func(t *testing.T) {
			_, _, err := payoutledger.SendingModules("testdata/" + tc.dir)
			require.Error(t, err, "must fail closed: %s", tc.because)
		})
	}
}

// Go repeats the preceding expression list when a const spec omits its own, so
//
//	const (
//		sender = "coreslot"
//		ModuleName
//	)
//
// declares ModuleName as "coreslot" while its ValueSpec carries no Values entry.
// Skipping that identifier was a concrete false-PASS: a build variant declaring
// ModuleName implicitly went unseen, and another variant's explicit value was
// reported as the package's answer. A payout from that package was then
// attributed to the wrong module, and the exemption-set equality compared
// successfully against a set that did not describe the built binary.
//
// Exercised through a real SendCoinsFromModuleToAccount call site, so this
// covers the load-bearing inventory path rather than a standalone const parse.
func TestImplicitConstModuleNameIsRefused(t *testing.T) {
	modules, _, err := payoutledger.SendingModules("testdata/implicit_const")
	require.Error(t, err,
		"an implicitly declared ModuleName must be refused, not skipped")
	require.Contains(t, err.Error(), "implicit repeated const expression")
	require.Empty(t, modules,
		"the package must never resolve to the other variant's explicit value")
}

// A parenthesized callee is the same direct call and must RESOLVE, not be
// skipped. Matching only a bare SelectorExpr missed it silently, which was a
// concrete false-pass path for the exemption inventory.
func TestParenthesizedCalleesAreStillCalls(t *testing.T) {
	modules, sites, err := payoutledger.SendingModules("testdata/paren_callee")
	require.NoError(t, err, "(x.Method)(...) is a direct call and must resolve")
	require.Equal(t, []string{"rewards"}, modules)
	require.Len(t, sites, 1)
}

// The two shapes it does accept must actually resolve, or the inventory would
// fail closed on the real code and be switched off.
func TestAcceptedShapesResolve(t *testing.T) {
	modules, sites, err := payoutledger.SendingModules("testdata/string_literal")
	require.NoError(t, err)
	require.Equal(t, []string{"rewards"}, modules)
	require.Len(t, sites, 1)
	require.Contains(t, sites[0].Position, "x.go")
}

// And the real tree must resolve completely. If this fails, a payout path was
// added in a shape the parser cannot read — which is a reason to widen the
// parser deliberately, not to loosen it.
func TestTheRealTreeResolvesCompletely(t *testing.T) {
	modules, sites, err := payoutledger.SendingModulesInRepo("../..")
	require.NoError(t, err, "every module payout call site must be resolvable")
	require.NotEmpty(t, sites, "the parser found no payout call sites at all, which cannot be right")
	require.Equal(t, []string{"rewards"}, modules,
		"rewards is the only module that pays ordinary accounts; a new one here means "+
			"the protocol-payout exemption list needs a deliberate decision")
}
