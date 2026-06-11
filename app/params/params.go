// Package params holds the canonical chain-identity and native-token constants.
//
// It is intentionally dependency-neutral: it imports NOTHING from this repo, so
// the app, the CLI, and future economic modules (x/emissions, x/rewards) can all
// consume it without import cycles. Denom identity is CHAIN identity — it is NOT
// owned by x/coreslot (the validator-ownership module).
package params

const (
	// NativeBaseDenom is the canonical, permanent, machine-level native asset
	// denom. It is the ONLY denom used in stateful accounting: bank balances,
	// total supply, genesis balances, module-account balances, fee/gas
	// accounting, future emissions reward pools, epoch/operator accounting, CLI
	// tx amounts, state export/import, params, tests, and fixtures.
	NativeBaseDenom = "utwlt"

	// The following are DISPLAY METADATA ONLY. They must never be used for
	// protocol accounting.
	NativeDisplayDenom = "twlt"     // bank denom-unit display name (lowercase)
	NativeSymbol       = "TWLT"     // ticker / symbol
	NativeName         = "Twilight" // human-readable token name
)

// NativeExponent is the display exponent: 1 TWLT (display) = 10^6 utwlt (base).
const NativeExponent = 6
