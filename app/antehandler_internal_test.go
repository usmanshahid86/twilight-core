package app

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Installing this check as the ONLY ante handler would leave transactions
// unsigned-verified, sequence-unchecked and gas-unmetered. An absent chain is a
// build misconfiguration, so it must stop the node rather than be tolerated —
// the failure mode of tolerating it is silent and total.
func TestWrappingAnAbsentAnteChainPanics(t *testing.T) {
	require.PanicsWithValue(t,
		"no ante handler to wrap: the transaction chain is absent, and installing "+
			"the bank output cap alone would disable signature verification",
		func() { newBankOutputCap(nil) },
		"an absent ante chain must fail closed at startup")
}
