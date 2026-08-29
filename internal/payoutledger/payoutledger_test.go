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
