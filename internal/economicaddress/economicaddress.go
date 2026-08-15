// Package economicaddress holds the one canonical rule deciding whether an
// address may receive or hold protocol value.
//
// An economic address is an operator, payout, treasury, entitlement or
// settlement-recipient address — anywhere the protocol directs value. It is NOT
// a control-plane identity: a governance authority, an emergency authority or a
// transaction signer is an authorization, and several of those are deliberately
// module accounts. Applying this rule to them would break the chain's own
// control plane, so callers must classify a field before validating it.
//
// # Why one rule, and why here
//
// Chain architecture V2.2 §25 requires a single canonical rule shared by
// registration, updates, payout snapshot creation, settlement execution, genesis
// validation and continuation import, with no implementation-local alternative
// denylist whose contents may differ. A rule copied into two modules is two
// rules the moment one is edited.
//
// The package is deliberately dependency-neutral. It knows nothing of x/coreslot
// or x/rewards, and nothing of the app: it is handed its authorities at
// construction. That is what lets both modules consume it without importing the
// app package and without either module gaining a keeper edge to the other. The
// authorities themselves — the module-account set and the bank blocked set — are
// app knowledge, and are derived there once.
//
// # Failure posture
//
// The zero value is unconfigured and rejects every address. There is no
// permissive fallback: a validator that silently allowed everything when a caller
// forgot to wire it would be worse than none, because the tests would still pass.
package economicaddress

import (
	"errors"
	"fmt"

	"cosmossdk.io/core/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
)

// Sentinel errors. Each names a distinct reason an address is inadmissible so a
// caller — or a test — can branch on the cause rather than on error text.
// Compare with errors.Is.
var (
	// ErrUnconfigured reports use of a validator that was never constructed. It
	// exists so the zero value fails loudly instead of admitting everything.
	ErrUnconfigured = errors.New("economicaddress: validator is unconfigured")

	// ErrEmptyAddress reports an address that is absent where one is required.
	ErrEmptyAddress = errors.New("economicaddress: address is empty")

	// ErrInvalidAddress reports a value that is not a valid account address for
	// this chain, including one carrying another chain's prefix.
	ErrInvalidAddress = errors.New("economicaddress: not a valid account address for this chain")

	// ErrModuleAccount reports a module account. Module accounts hold protocol
	// balances under module control; directing an operator payout at one would
	// send value somewhere no user can spend it from.
	ErrModuleAccount = errors.New("economicaddress: address is a module account")

	// ErrBlockedAddress reports a destination the bank module prohibits from
	// receiving funds.
	ErrBlockedAddress = errors.New("economicaddress: address is blocked from receiving funds")
)

// Validator decides whether an address may hold protocol value.
//
// It is immutable once constructed and safe for concurrent use. Values are
// copied out of the caller's inputs at construction, so a later mutation of the
// caller's map cannot change what consensus admits.
type Validator struct {
	// codec is the chain's own account address codec. Holding it explicitly is
	// what keeps this package independent of the global SDK bech32 configuration
	// and of whatever order that configuration happens to be installed in.
	codec address.Codec

	// Both sets are keyed by RAW ADDRESS BYTES held in a string, not by the
	// bech32 text. Two spellings of one address must not be able to slip past a
	// text comparison, and the bank module keys its own set by a string produced
	// from global configuration this package deliberately does not read.
	moduleAccounts map[string]struct{}
	blocked        map[string]struct{}
}

// New builds a validator from the app's authoritative inputs.
//
// moduleAccountNames are the module-account names declared by the application's
// auth configuration; their addresses are derived deterministically from the
// names, so the validator needs no initialized account state to know them.
// blocked is the bank module's own blocked-destination set, keyed as bank keys
// it. Both are copied.
//
// It returns an error rather than a permissive validator when an input is
// unusable, because every failure here would otherwise become a silently weaker
// admission rule.
func New(codec address.Codec, moduleAccountNames []string, blocked map[string]bool) (Validator, error) {
	if codec == nil {
		return Validator{}, fmt.Errorf("economicaddress: account address codec is required")
	}
	// An application with no module accounts cannot be this chain, and an empty
	// set would silently disable half the rule.
	if len(moduleAccountNames) == 0 {
		return Validator{}, fmt.Errorf("economicaddress: at least one module account name is required")
	}

	moduleAccounts := make(map[string]struct{}, len(moduleAccountNames))
	for _, name := range moduleAccountNames {
		if name == "" {
			return Validator{}, fmt.Errorf("economicaddress: module account name must not be empty")
		}
		moduleAccounts[string(authtypes.NewModuleAddress(name))] = struct{}{}
	}

	blockedSet := make(map[string]struct{}, len(blocked))
	for encoded := range blocked {
		raw, err := codec.StringToBytes(encoded)
		if err != nil {
			// The bank set is authoritative; if this chain's own codec cannot read
			// an entry, the two disagree about what an address is and enforcement
			// would be partial.
			return Validator{}, fmt.Errorf("economicaddress: blocked address %q is unreadable: %w", encoded, err)
		}
		blockedSet[string(raw)] = struct{}{}
	}

	return Validator{codec: codec, moduleAccounts: moduleAccounts, blocked: blockedSet}, nil
}

// IsConfigured reports whether the validator was built by New.
func (v Validator) IsConfigured() bool { return v.codec != nil }

// Validate reports whether address may hold protocol value, returning the parsed
// address so a caller needing bytes for a transfer does not parse a second time.
//
// A second independent parse is not merely wasteful: it is where the two copies
// of a rule start to differ, and where an ignored error turns a rejected address
// into a zero-value one.
func (v Validator) Validate(address string) (sdk.AccAddress, error) {
	if !v.IsConfigured() {
		return nil, ErrUnconfigured
	}
	if address == "" {
		return nil, ErrEmptyAddress
	}

	raw, err := v.codec.StringToBytes(address)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrInvalidAddress, address, err)
	}
	if len(raw) == 0 {
		return nil, ErrEmptyAddress
	}

	// Module accounts are checked before the bank set so an address that is both
	// reports the same reason every time. A module account must be refused even
	// when bank does not block it — the two conditions are independent, and
	// neither implies the other.
	if _, isModule := v.moduleAccounts[string(raw)]; isModule {
		return nil, fmt.Errorf("%w: %s", ErrModuleAccount, address)
	}
	if _, isBlocked := v.blocked[string(raw)]; isBlocked {
		return nil, fmt.Errorf("%w: %s", ErrBlockedAddress, address)
	}

	return sdk.AccAddress(raw), nil
}

// ValidateOptional applies the rule only when an address is present. It exists
// for fields the protocol makes conditional — a treasury address is required
// when a treasury share is positive and legitimately absent when it is zero —
// so a caller expresses that condition once rather than reimplementing the
// empty check at each site.
func (v Validator) ValidateOptional(address string) (sdk.AccAddress, error) {
	if address == "" {
		if !v.IsConfigured() {
			return nil, ErrUnconfigured
		}
		return nil, nil
	}
	return v.Validate(address)
}
