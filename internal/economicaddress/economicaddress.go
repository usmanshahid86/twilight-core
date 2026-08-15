// Package economicaddress holds the one canonical rule deciding whether an
// address may receive or hold protocol value.
//
// An economic address is a payout, settlement, treasury or settlement-recipient
// address — anywhere the protocol DIRECTS VALUE. It is not merely any address
// the protocol stores.
//
// # Destinations versus identities
//
// Chain architecture V2.2 §25 scopes the economic rule to value destinations:
// payout_address, settlement_address, treasury destinations when enabled, and
// settlement recipients. Several other fields are addresses without being
// destinations, and applying the economic rule to them is a bug in the other
// direction:
//
//   - A governance authority and an emergency authority are module accounts by
//     design. Refusing them would leave the chain unable to govern itself.
//   - A transaction signer submits a claim; the value goes to each record's
//     stored payout address regardless of who submitted it.
//   - An operator address is required by §18 to be VALID, and is persisted
//     alongside a payout address, but the protocol never sends to it. Refusing a
//     bank-blocked operator would deny an operator the protocol permits.
//
// The package therefore offers two levels, and callers must classify a field
// before choosing one. ParseAccountAddress answers "is this an address on this
// chain"; Validate answers "may this address receive protocol value".
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
	"sort"

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

	// ErrInvalidAddress reports a value that cannot serve as an economic address:
	// one that is not a valid account address for this chain, including one
	// carrying another chain's prefix, and one whose bytes are entirely zero.
	// §25 requires an economic address to be both non-empty and non-zero, and the
	// all-zero address is a well-formed encoding that nobody controls.
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

	// The bank set is boolean-VALUED, not merely keyed: an entry mapped to false
	// is a key that is explicitly NOT prohibited. Treating every key as blocked
	// would refuse destinations bank permits, so only true-valued entries are
	// taken. False-valued entries are not decoded at all — they are not part of
	// the prohibited set, so a malformed one is not this rule's business.
	prohibited := make([]string, 0, len(blocked))
	for encoded, isBlocked := range blocked {
		if isBlocked {
			prohibited = append(prohibited, encoded)
		}
	}
	// Sorted before decoding so that which malformed entry is reported does not
	// depend on Go's map iteration order. Construction failure must be the same
	// failure every run.
	sort.Strings(prohibited)

	blockedSet := make(map[string]struct{}, len(prohibited))
	for _, encoded := range prohibited {
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

// ParseAccountAddress decodes an address using the chain's account codec and
// nothing more. It answers "is this an address on this chain", which is what
// §18 asks of an operator address and what any control identity needs.
//
// It deliberately performs NO economic checks: no module-account exclusion, no
// bank-blocked exclusion, no non-zero requirement. Those belong to value
// destinations, and imposing them here would refuse identities the protocol
// permits. There is still exactly one economic rule — this is only the
// account-syntax primitive it is built on.
func (v Validator) ParseAccountAddress(address string) (sdk.AccAddress, error) {
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
	return sdk.AccAddress(raw), nil
}

// Validate reports whether address may RECEIVE PROTOCOL VALUE, returning the
// parsed address so a caller needing bytes for a transfer does not parse a
// second time.
//
// A second independent parse is not merely wasteful: it is where the two copies
// of a rule start to differ, and where an ignored error turns a rejected address
// into a zero-value one.
//
// The rule is account syntax, plus §25's three additional requirements: the
// address must be non-zero, must not be a module account, and must not be
// bank-blocked. Use ParseAccountAddress instead for an identity that is stored
// but never sent to.
func (v Validator) Validate(address string) (sdk.AccAddress, error) {
	raw, err := v.ParseAccountAddress(address)
	if err != nil {
		return nil, err
	}

	// §25 requires an economic address to be non-empty AND non-zero. The all-zero
	// address of otherwise normal length is a well-formed encoding that no key
	// controls, so value sent there is destroyed as surely as if it were burned.
	if isZero(raw) {
		return nil, fmt.Errorf("%w: %s: address is all zero", ErrInvalidAddress, address)
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

	return raw, nil
}

// isZero reports whether every byte of a decoded address is zero.
func isZero(raw []byte) bool {
	for _, b := range raw {
		if b != 0 {
			return false
		}
	}
	return true
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
