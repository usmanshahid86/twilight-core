package keeper_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/types/bech32"
	"google.golang.org/grpc/codes"

	"github.com/twilight-project/twilight-core/internal/economicaddress"
	"github.com/twilight-project/twilight-core/x/mining/keeper"
	"github.com/twilight-project/twilight-core/x/mining/types"
)

// Whether an address may receive protocol value, asked rather than copied.
//
// The query holds no rule of its own: it asks the same injected validator
// settlement execution enforces, so a consumer gets the answer the release
// boundary will give rather than a second opinion that can drift from it. Two
// distinctions run through every test here. An inadmissible address is a
// SUCCESSFUL answer about the address, and only a defective request or a rule that
// cannot be applied is an RPC error — because a consumer must never turn "the
// chain could not tell me" into "this participant is excluded".

const (
	reasonNone          = types.EconomicAddressRejectionReason_ECONOMIC_ADDRESS_REJECTION_REASON_NONE
	reasonEmpty         = types.EconomicAddressRejectionReason_ECONOMIC_ADDRESS_REJECTION_REASON_EMPTY
	reasonInvalid       = types.EconomicAddressRejectionReason_ECONOMIC_ADDRESS_REJECTION_REASON_INVALID
	reasonModuleAccount = types.EconomicAddressRejectionReason_ECONOMIC_ADDRESS_REJECTION_REASON_MODULE_ACCOUNT
	reasonBankBlocked   = types.EconomicAddressRejectionReason_ECONOMIC_ADDRESS_REJECTION_REASON_BANK_BLOCKED
	reasonUnspecified   = types.EconomicAddressRejectionReason_ECONOMIC_ADDRESS_REJECTION_REASON_UNSPECIFIED
)

// economicAddressCase is one address and the single verdict both boundaries must
// reach for it.
//
// sentinel is the validator error the execution boundary surfaces for the same
// address. Carrying it in the table is what lets the cross-boundary test prove the
// two agree on the CAUSE and not merely on the yes/no, which a coincidence of two
// different rules could produce.
type economicAddressCase struct {
	name       string
	address    string
	admissible bool
	reason     types.EconomicAddressRejectionReason
	sentinel   error

	// executionRefusal overrides the text the execution boundary is expected to
	// report, for the one address it refuses at an earlier stage than the rule.
	// Empty means the sentinel's own text, which is the ordinary case.
	executionRefusal string
}

func economicAddressCases(t *testing.T) []economicAddressCase {
	t.Helper()

	// A well-formed address on a chain that is not this one. Built rather than
	// written down, so the case keeps testing a foreign prefix even if this
	// chain's own prefix is ever reconfigured.
	const foreignPrefix = "notthischain"
	require.NotEqual(t, foreignPrefix, sdk.GetConfig().GetBech32AccountAddrPrefix(),
		"the foreign-prefix fixture must not name this chain")
	raw := make([]byte, 20)
	raw[0] = 0x41
	foreign, err := bech32.ConvertAndEncode(foreignPrefix, raw)
	require.NoError(t, err)

	return []economicAddressCase{
		{
			name:       "a user address",
			address:    account(participantA),
			admissible: true,
			reason:     reasonNone,
		},
		{
			// The only case the two boundaries refuse at different stages. A payout
			// line naming no recipient is a malformed LINE, so stateless message
			// validation refuses it before the canonical rule is ever consulted.
			// The verdicts still agree — and the message check is not a second
			// admissibility rule, it is the structural precondition that gives the
			// rule something to judge.
			name:             "empty",
			address:          "",
			reason:           reasonEmpty,
			sentinel:         economicaddress.ErrEmptyAddress,
			executionRefusal: "names no recipient",
		},
		{
			name:     "not bech32 at all",
			address:  "not-an-address",
			reason:   reasonInvalid,
			sentinel: economicaddress.ErrInvalidAddress,
		},
		{
			name:     "a foreign prefix",
			address:  foreign,
			reason:   reasonInvalid,
			sentinel: economicaddress.ErrInvalidAddress,
		},
		{
			name:     "truncated bytes",
			address:  "cosmos1qqqqqq",
			reason:   reasonInvalid,
			sentinel: economicaddress.ErrInvalidAddress,
		},
		{
			// A well-formed encoding no key controls. Value sent there is destroyed
			// as surely as if it were burned, which is why it is inadmissible
			// despite parsing.
			name:     "all zero",
			address:  account(0x00),
			reason:   reasonInvalid,
			sentinel: economicaddress.ErrInvalidAddress,
		},
		{
			name:     "a module account",
			address:  moduleAccountAddress(),
			reason:   reasonModuleAccount,
			sentinel: economicaddress.ErrModuleAccount,
		},
		{
			name:     "bank blocked",
			address:  blockedAddress(),
			reason:   reasonBankBlocked,
			sentinel: economicaddress.ErrBlockedAddress,
		},
	}
}

func validateAddress(
	t *testing.T, q types.QueryServer, ctx sdk.Context, address string,
) *types.QueryValidateEconomicAddressResponse {
	t.Helper()
	res, err := q.ValidateEconomicAddress(ctx, &types.QueryValidateEconomicAddressRequest{Address: address})
	require.NoError(t, err, "an inadmissible address is an answer, not a failure")
	return res
}

// TestEveryEnumeratedVerdictIsAnAnswerRatherThanAnError walks the whole rule.
//
// Each row is a deterministic answer carrying one response shape, so a consumer
// branches on rejection_reason and never on a transport code. The admissible row
// is the only one that emits a canonical address.
func TestEveryEnumeratedVerdictIsAnAnswerRatherThanAnError(t *testing.T) {
	q, _, ctx, _ := queryFixture(t)

	for _, testCase := range economicAddressCases(t) {
		t.Run(testCase.name, func(t *testing.T) {
			res := validateAddress(t, q, ctx, testCase.address)
			require.Equal(t, testCase.admissible, res.Admissible)
			require.Equal(t, testCase.reason, res.RejectionReason)
		})
	}
}

// TestCanonicalAddressIsEmptyOnEveryRejection covers the value a rejected answer
// must not carry.
//
// A module account and a blocked address both parse perfectly well, so the
// handler holds a canonical form it could return for them. Returning it would hand
// downstream code a value it could mistake for acceptance — the failure would not
// be a wrong verdict but a right verdict nobody read, and it would land money
// somewhere the rule had just refused.
func TestCanonicalAddressIsEmptyOnEveryRejection(t *testing.T) {
	q, _, ctx, _ := queryFixture(t)

	for _, testCase := range economicAddressCases(t) {
		t.Run(testCase.name, func(t *testing.T) {
			res := validateAddress(t, q, ctx, testCase.address)
			if testCase.admissible {
				require.NotEmpty(t, res.CanonicalAddress,
					"an admissible answer carries the parsed address so a caller need not parse again")
				require.Equal(t, testCase.address, res.CanonicalAddress)
				return
			}
			require.Emptyf(t, res.CanonicalAddress,
				"%s is inadmissible and must expose no canonical-looking value", testCase.name)
		})
	}
}

// TestAnEmptyAddressIsADomainRejectionAndNotAMalformedRequest freezes the one
// distinction an implementer is most likely to collapse.
//
// Both look like "nothing was supplied", and they are not the same event. A nil
// envelope is a broken caller; an empty address is a question with a canonical
// answer. Collapsing the second into InvalidArgument would remove the only case a
// consumer can reach on all three surfaces without constructing a malformed
// request, and would make an excluded participant indistinguishable from a
// transport fault.
func TestAnEmptyAddressIsADomainRejectionAndNotAMalformedRequest(t *testing.T) {
	q, _, ctx, _ := queryFixture(t)

	res := validateAddress(t, q, ctx, "")
	require.False(t, res.Admissible)
	require.Equal(t, reasonEmpty, res.RejectionReason)
	require.Empty(t, res.CanonicalAddress)

	_, err := q.ValidateEconomicAddress(ctx, nil)
	require.Equal(t, codes.InvalidArgument, grpcCode(t, err),
		"only the envelope itself leaves the deterministic response shape")
}

// TestAValidatorThatCannotAnswerFailsClosed keeps a node fault out of the domain.
//
// The zero-value validator is unconfigured and refuses every address. That is a
// misconfigured node, not an inadmissible participant, and reporting it as a
// rejection would let a deployment fault silently exclude everyone. The empty
// address is included deliberately: it has a canonical rejection of its own, so it
// is exactly where an unconfigured rule could hide behind a plausible answer.
func TestAValidatorThatCannotAnswerFailsClosed(t *testing.T) {
	k, ctx, _ := setupKeeperWithValidator(
		t, &coreSlotKeeperMock{}, newRewardsMock(), economicaddress.Validator{})
	q := keeper.NewQueryServer(k)

	for _, address := range []string{account(participantA), "", moduleAccountAddress()} {
		_, err := q.ValidateEconomicAddress(ctx, &types.QueryValidateEconomicAddressRequest{Address: address})
		require.Equal(t, codes.Internal, grpcCode(t, err),
			"a rule that cannot be applied has no admissibility answer to give")
	}
}

// TestAnUnrecognizedFailureIsNeverAGuessedReason pins the classifier's default.
//
// The enum names reasons an address is INADMISSIBLE. "The rule could not be
// applied" is not one of them, so an unrecognized error must leave the map
// unclassified rather than land in whichever arm happened to be nearest. That
// default is what makes a sentinel added upstream later surface as an explicit
// failure instead of quietly becoming a rejection reason it does not mean.
func TestAnUnrecognizedFailureIsNeverAGuessedReason(t *testing.T) {
	t.Run("each sentinel maps, including when wrapped", func(t *testing.T) {
		for _, testCase := range economicAddressCases(t) {
			if testCase.sentinel == nil {
				continue
			}
			// The validator wraps its sentinels with the offending address, so the
			// classifier has to match through the wrap rather than by identity.
			wrapped := errors.Join(testCase.sentinel, errors.New("context the validator adds"))
			reason, classified := keeper.ClassifyEconomicAddressRejection(wrapped)
			require.Truef(t, classified, "%s must be recognized", testCase.name)
			require.Equal(t, testCase.reason, reason)
		}
	})

	t.Run("an unconfigured rule is not a rejection reason", func(t *testing.T) {
		reason, classified := keeper.ClassifyEconomicAddressRejection(economicaddress.ErrUnconfigured)
		require.False(t, classified)
		require.Equal(t, reasonUnspecified, reason)
	})

	t.Run("an unknown error is not a rejection reason", func(t *testing.T) {
		reason, classified := keeper.ClassifyEconomicAddressRejection(errors.New("a sentinel from a later change"))
		require.False(t, classified)
		require.Equal(t, reasonUnspecified, reason)
	})
}

// TestAddressAdmissibilityAgreesWithSettlementRecipientAdmission is the named
// acceptance criterion, and the reason the query is worth having at all.
//
// A consumer reads this query to decide who it may pay. If the public
// interpretation and the execution boundary could disagree, an address the query
// blessed would be refused at the chunk — or, far worse, an address the query
// refused would be payable. Every fixture is therefore asserted twice: once
// through the query, once through real settlement recipient admission, under one
// app configuration and one validator.
//
// The cause is compared too. Two rules that happened to reject the same address
// for different reasons would satisfy a yes/no comparison while already having
// drifted.
func TestAddressAdmissibilityAgreesWithSettlementRecipientAdmission(t *testing.T) {
	for _, testCase := range economicAddressCases(t) {
		t.Run(testCase.name, func(t *testing.T) {
			// A fixture per case: an admitted chunk moves the cursor and releases
			// value, so cases sharing one settlement would stop being independent.
			k, ctx, rewards := settlementFixture(t)
			q := keeper.NewQueryServer(k)

			res := validateAddress(t, q, ctx, testCase.address)

			// One recipient, a well-formed amount, the next expected index: the
			// address is the only thing that can decide this chunk.
			_, err := k.SubmitSettlementChunk(ctx, chunk(0,
				&types.SettlementChunkPayout{Recipient: testCase.address, Amount: "50000"}))

			if testCase.admissible {
				require.NoError(t, err, "the query admitted this address; execution must pay it")
				require.True(t, res.Admissible)
				require.Equal(t, 1, rewards.payCalls)
				return
			}

			require.Error(t, err, "the query refused this address; execution must refuse it too")
			require.False(t, res.Admissible)
			refusal := testCase.sentinel.Error()
			if testCase.executionRefusal != "" {
				refusal = testCase.executionRefusal
			}
			require.ErrorContains(t, err, refusal,
				"both boundaries must refuse for the same reason, not merely refuse")
			require.Zero(t, rewards.payCalls, "a refused recipient releases nothing")
		})
	}
}
