package economicaddress_test

import (
	"strings"
	"testing"

	"cosmossdk.io/core/address"
	addresscodec "github.com/cosmos/cosmos-sdk/codec/address"
	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/stretchr/testify/require"

	"github.com/twilight-project/twilight-core/internal/economicaddress"
)

const (
	prefix     = "twilight"
	moduleName = "test-module"
	otherName  = "test-other-module"
)

func codec() address.Codec {
	return addresscodec.NewBech32Codec(prefix)
}

// encode renders raw bytes in this test's account prefix, independent of the
// global SDK configuration.
func encode(t *testing.T, raw []byte) string {
	t.Helper()
	encoded, err := codec().BytesToString(raw)
	require.NoError(t, err)
	return encoded
}

func moduleAddress(t *testing.T, name string) string {
	t.Helper()
	return encode(t, authtypes.NewModuleAddress(name))
}

func account(t *testing.T, marker byte) string {
	t.Helper()
	raw := make([]byte, 20)
	raw[0] = marker
	raw[19] = marker
	return encode(t, raw)
}

func newValidator(t *testing.T, blocked map[string]bool) economicaddress.Validator {
	t.Helper()
	validator, err := economicaddress.New(codec(), []string{moduleName, otherName}, blocked)
	require.NoError(t, err)
	return validator
}

func TestValidateAcceptsOrdinaryAccount(t *testing.T) {
	validator := newValidator(t, nil)
	address := account(t, 7)

	parsed, err := validator.Validate(address)
	require.NoError(t, err)

	// The returned address must be the one supplied, so a caller can transfer to
	// it without parsing a second time.
	require.Equal(t, address, encode(t, parsed))
}

func TestValidateRejectsMalformedAndEmpty(t *testing.T) {
	validator := newValidator(t, nil)

	require.ErrorIs(t, errOf(validator.Validate("")), economicaddress.ErrEmptyAddress)
	require.ErrorIs(t, errOf(validator.Validate("not-an-address")), economicaddress.ErrInvalidAddress)
	require.ErrorIs(t, errOf(validator.Validate(prefix+"1notbech32")), economicaddress.ErrInvalidAddress)

	// Another chain's prefix decodes as bech32 but is not an address here.
	foreign, err := addresscodec.NewBech32Codec("cosmos").BytesToString(make([]byte, 20))
	require.NoError(t, err)
	require.ErrorIs(t, errOf(validator.Validate(foreign)), economicaddress.ErrInvalidAddress)
}

// TestModuleAccountRejectedWithoutBankBlocking is the first half of the
// independence requirement: module exclusion must not be a side effect of the
// bank set happening to contain module accounts.
func TestModuleAccountRejectedWithoutBankBlocking(t *testing.T) {
	validator := newValidator(t, nil) // bank blocks nothing at all

	for _, name := range []string{moduleName, otherName} {
		err := errOf(validator.Validate(moduleAddress(t, name)))
		require.ErrorIsf(t, err, economicaddress.ErrModuleAccount, "module account %q", name)
	}
}

// TestBlockedAccountRejectedWithoutBeingModule is the second half: a perfectly
// ordinary account that bank prohibits must be refused on that ground alone.
func TestBlockedAccountRejectedWithoutBeingModule(t *testing.T) {
	blockedAddress := account(t, 42)
	validator := newValidator(t, map[string]bool{blockedAddress: true})

	err := errOf(validator.Validate(blockedAddress))
	require.ErrorIs(t, err, economicaddress.ErrBlockedAddress)
	require.NotErrorIs(t, err, economicaddress.ErrModuleAccount)

	// A neighboring ordinary account is unaffected.
	_, err = validator.Validate(account(t, 43))
	require.NoError(t, err)
}

// TestAllZeroAddressRejected covers §25's non-zero requirement. The all-zero
// address of otherwise normal length is a well-formed encoding that no key
// controls, so value sent there is destroyed.
func TestAllZeroAddressRejected(t *testing.T) {
	validator := newValidator(t, nil)
	zero := encode(t, make([]byte, 20))

	// Constructed through the codec, not as a literal string.
	err := errOf(validator.Validate(zero))
	require.ErrorIs(t, err, economicaddress.ErrInvalidAddress)
	require.Contains(t, err.Error(), "all zero")

	// The uppercase bech32 spelling of the same value is the same address.
	require.ErrorIs(t, errOf(validator.Validate(strings.ToUpper(zero))), economicaddress.ErrInvalidAddress)

	// The optional form must refuse it too — present but null is not absent.
	require.ErrorIs(t, errOf(validator.ValidateOptional(zero)), economicaddress.ErrInvalidAddress)

	// An ordinary non-zero address is unaffected, including one whose leading
	// bytes are zero.
	_, err = validator.Validate(account(t, 3))
	require.NoError(t, err)
	trailing := make([]byte, 20)
	trailing[19] = 1
	_, err = validator.Validate(encode(t, trailing))
	require.NoError(t, err, "only an entirely zero address is refused")
}

// TestAllZeroIsAnEconomicRuleOnly pins the scope of the non-zero requirement:
// it belongs to value destinations, not to the account-syntax primitive that
// control identities use.
func TestAllZeroIsAnEconomicRuleOnly(t *testing.T) {
	validator := newValidator(t, nil)
	zero := encode(t, make([]byte, 20))

	parsed, err := validator.ParseAccountAddress(zero)
	require.NoError(t, err, "the zero address is syntactically a valid account address")
	require.Equal(t, zero, encode(t, parsed))
}

// TestParseAccountAddressAppliesNoEconomicExclusion is the primitive behind the
// operator/payout split: it answers only "is this an address on this chain".
func TestParseAccountAddressAppliesNoEconomicExclusion(t *testing.T) {
	blockedAddress := account(t, 61)
	validator := newValidator(t, map[string]bool{blockedAddress: true})

	// Neither a module account nor a bank-blocked destination is excluded here.
	for _, address := range []string{blockedAddress, moduleAddress(t, moduleName)} {
		parsed, err := validator.ParseAccountAddress(address)
		require.NoErrorf(t, err, "address %s must parse as an identity", address)
		require.Equal(t, address, encode(t, parsed))

		// The same address is refused as a value destination.
		require.Errorf(t, errOf(validator.Validate(address)), "address %s must fail as a destination", address)
	}

	// It still rejects what is not an address at all.
	require.ErrorIs(t, errOf(validator.ParseAccountAddress("")), economicaddress.ErrEmptyAddress)
	require.ErrorIs(t, errOf(validator.ParseAccountAddress("not-an-address")), economicaddress.ErrInvalidAddress)

	var zero economicaddress.Validator
	require.ErrorIs(t, errOf(zero.ParseAccountAddress(account(t, 1))), economicaddress.ErrUnconfigured)
}

// TestBlockedMapFollowsItsBooleanValue covers the bank contract exactly. The
// blocked set is boolean-VALUED: a key mapped to false is explicitly permitted,
// and treating every key as prohibited would refuse destinations bank allows.
func TestBlockedMapFollowsItsBooleanValue(t *testing.T) {
	permitted := account(t, 71)
	prohibited := account(t, 72)

	t.Run("false-valued entry is not blocked", func(t *testing.T) {
		validator := newValidator(t, map[string]bool{permitted: false})
		_, err := validator.Validate(permitted)
		require.NoError(t, err, "an entry mapped to false is not a prohibited destination")
	})

	t.Run("true-valued entry is blocked", func(t *testing.T) {
		validator := newValidator(t, map[string]bool{prohibited: true})
		require.ErrorIs(t, errOf(validator.Validate(prohibited)), economicaddress.ErrBlockedAddress)
	})

	t.Run("both in one map, each follows its own boolean", func(t *testing.T) {
		validator := newValidator(t, map[string]bool{permitted: false, prohibited: true})

		_, err := validator.Validate(permitted)
		require.NoError(t, err)
		require.ErrorIs(t, errOf(validator.Validate(prohibited)), economicaddress.ErrBlockedAddress)
	})
}

// TestMalformedBlockedEntriesAreDeterministic covers the diagnostic. Which
// malformed entry gets reported must not depend on Go's map iteration order, or
// the same configuration would fail differently between runs.
func TestMalformedBlockedEntriesAreDeterministic(t *testing.T) {
	t.Run("two malformed true entries give a stable result", func(t *testing.T) {
		blocked := map[string]bool{"aaa-not-an-address": true, "zzz-not-an-address": true}

		_, first := economicaddress.New(codec(), []string{moduleName}, blocked)
		require.Error(t, first)
		for i := 0; i < 20; i++ {
			_, again := economicaddress.New(codec(), []string{moduleName}, blocked)
			require.Error(t, again)
			require.Equal(t, first.Error(), again.Error(),
				"construction must fail identically every run")
		}
		// Sorted order decides, so the lexicographically first entry is reported.
		require.Contains(t, first.Error(), "aaa-not-an-address")
	})

	t.Run("a malformed false entry does not fail construction", func(t *testing.T) {
		// A false-valued key is not part of the prohibited set, so it is never
		// decoded and cannot make the validator unbuildable.
		validator, err := economicaddress.New(
			codec(), []string{moduleName}, map[string]bool{"not-an-address": false},
		)
		require.NoError(t, err)

		_, err = validator.Validate(account(t, 73))
		require.NoError(t, err)
	})
}

// TestUnconfiguredValidatorFailsClosed is the property that makes a forgotten
// injection loud instead of silently permissive.
func TestUnconfiguredValidatorFailsClosed(t *testing.T) {
	var zero economicaddress.Validator
	require.False(t, zero.IsConfigured())

	require.ErrorIs(t, errOf(zero.Validate(account(t, 1))), economicaddress.ErrUnconfigured)
	require.ErrorIs(t, errOf(zero.Validate("")), economicaddress.ErrUnconfigured)

	// The optional form must fail closed too, including on the empty input it
	// would otherwise wave through.
	require.ErrorIs(t, errOf(zero.ValidateOptional("")), economicaddress.ErrUnconfigured)
	require.ErrorIs(t, errOf(zero.ValidateOptional(account(t, 1))), economicaddress.ErrUnconfigured)
}

// TestConstructorCopiesInputs proves the capability cannot be mutated after app
// construction. A validator retaining the caller's map would let a later write
// to that map change what consensus admits.
func TestConstructorCopiesInputs(t *testing.T) {
	blockedAddress := account(t, 9)
	laterAddress := account(t, 10)

	blocked := map[string]bool{blockedAddress: true}
	names := []string{moduleName, otherName}

	validator, err := economicaddress.New(codec(), names, blocked)
	require.NoError(t, err)

	// Mutate both inputs after construction.
	blocked[laterAddress] = true
	delete(blocked, blockedAddress)
	names[0] = "some-other-module"

	// The validator's answers are unchanged by any of it.
	require.ErrorIs(t, errOf(validator.Validate(blockedAddress)), economicaddress.ErrBlockedAddress)
	_, err = validator.Validate(laterAddress)
	require.NoError(t, err, "an address added to the caller's map after construction must not become blocked")
	require.ErrorIs(t, errOf(validator.Validate(moduleAddress(t, moduleName))), economicaddress.ErrModuleAccount)
}

func TestConstructorRejectsUnusableInput(t *testing.T) {
	_, err := economicaddress.New(nil, []string{moduleName}, nil)
	require.Error(t, err, "a nil codec must not produce a usable validator")

	_, err = economicaddress.New(codec(), nil, nil)
	require.Error(t, err, "an empty module-account set would silently disable half the rule")

	_, err = economicaddress.New(codec(), []string{""}, nil)
	require.Error(t, err, "an empty module account name is not a module account")

	// A blocked entry this chain's codec cannot read means the two disagree about
	// what an address is, and enforcement would be partial.
	_, err = economicaddress.New(codec(), []string{moduleName}, map[string]bool{"garbage": true})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unreadable")
}

// TestValidateDoesNotNormalize pins that invalid input is refused rather than
// repaired into something valid.
func TestValidateDoesNotNormalize(t *testing.T) {
	validator := newValidator(t, nil)
	address := account(t, 5)

	for _, mutated := range []string{
		" " + address,
		address + " ",
		"\t" + address,
	} {
		_, err := validator.Validate(mutated)
		require.Errorf(t, err, "input %q must not be trimmed into validity", mutated)
	}
}

// TestAlternativeSpellingCannotBypassTheSets is why both sets are keyed by raw
// address bytes rather than by text.
//
// bech32 permits an all-uppercase encoding of the same value, and the bank
// module keys its blocked set by the lowercase rendering. A validator comparing
// strings would therefore admit the uppercase spelling of a blocked or
// module-account address — the same destination, spelled differently, waved
// through. Comparing decoded bytes closes that off, and the same spelling is
// still accepted for an address that is genuinely permitted.
func TestAlternativeSpellingCannotBypassTheSets(t *testing.T) {
	blockedAddress := account(t, 51)
	validator := newValidator(t, map[string]bool{blockedAddress: true})

	upperBlocked := strings.ToUpper(blockedAddress)
	require.NotEqual(t, blockedAddress, upperBlocked)
	require.ErrorIs(t, errOf(validator.Validate(upperBlocked)), economicaddress.ErrBlockedAddress)

	upperModule := strings.ToUpper(moduleAddress(t, moduleName))
	require.ErrorIs(t, errOf(validator.Validate(upperModule)), economicaddress.ErrModuleAccount)

	// An uppercase spelling of a permitted address remains permitted: this is a
	// legitimate encoding, not something to reject on sight.
	permitted := account(t, 52)
	parsed, err := validator.Validate(strings.ToUpper(permitted))
	require.NoError(t, err)
	require.Equal(t, permitted, encode(t, parsed))
}

func TestValidateOptional(t *testing.T) {
	blockedAddress := account(t, 21)
	validator := newValidator(t, map[string]bool{blockedAddress: true})

	// Absent is permitted; that is the whole point of the optional form.
	parsed, err := validator.ValidateOptional("")
	require.NoError(t, err)
	require.Nil(t, parsed)

	// Present is held to the full rule.
	_, err = validator.ValidateOptional(account(t, 22))
	require.NoError(t, err)
	require.ErrorIs(t, errOf(validator.ValidateOptional(blockedAddress)), economicaddress.ErrBlockedAddress)
	require.ErrorIs(t, errOf(validator.ValidateOptional(moduleAddress(t, moduleName))), economicaddress.ErrModuleAccount)
}

// TestErrorsDoNotLeakTheDenylist keeps rejection messages deterministic and
// bounded: naming the offending address is useful, reproducing the prohibited
// set is neither deterministic nor safe to log.
func TestErrorsDoNotLeakTheDenylist(t *testing.T) {
	first, second := account(t, 31), account(t, 32)
	validator := newValidator(t, map[string]bool{first: true, second: true})

	err := errOf(validator.Validate(first))
	require.Contains(t, err.Error(), first)
	require.NotContains(t, err.Error(), second)
	require.NotContains(t, err.Error(), moduleAddress(t, moduleName))
}

// TestModuleExclusionIndependentOfGlobalConfig pins that the validator answers
// from the codec it was handed rather than from global SDK state.
func TestModuleExclusionIndependentOfGlobalConfig(t *testing.T) {
	validator := newValidator(t, nil)

	// authtypes.NewModuleAddress yields raw bytes; the validator must recognize
	// them regardless of how the global configuration would render them.
	raw := authtypes.NewModuleAddress(moduleName)
	require.ErrorIs(t, errOf(validator.Validate(encode(t, raw))), economicaddress.ErrModuleAccount)
	if raw.String() == encode(t, raw) {
		t.Skip("global prefix matches the codec's; the distinction is not observable here")
	}
}

// errOf discards the parsed address so a rejection can be asserted inline.
// It takes exactly the validator's return pair, which is what lets a call be
// spread directly into it.
func errOf(_ sdk.AccAddress, err error) error { return err }
