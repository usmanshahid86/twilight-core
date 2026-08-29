package app

import (
	"context"
	"fmt"

	sdkmath "cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authkeeper "github.com/cosmos/cosmos-sdk/x/auth/keeper"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	"github.com/twilight-project/twilight-core/app/params"
	rewardstypes "github.com/twilight-project/twilight-core/x/rewards/types"
)

// MinimumAccountFunding is the smallest amount of the base denom that may bring
// a NEW account into existence in a single transfer.
//
// # What this is for
//
// A fresh recipient gets a permanent BaseAccount of roughly 100-130 bytes, and
// that record is NOT reclaimable: setBalance deletes a zero balance, but the
// account survives. With free transactions and max_gas at -1, bytes bind rather
// than gas, and the measured rate is about 340,000 accounts in one 21 MB block —
// roughly 40 MB of permanent IAVL growth that degrades state-sync, pruning and
// export for the life of the chain. That is TW-006.
//
// # What this bounds, precisely
//
// This is a TRANSACTION-LOCAL SHAPE RESTRICTION. It makes fresh-account fan-out
// WITHIN ONE TRANSACTION consume proportional available balance: N new
// recipients in one MsgMultiSend require N times this amount, and no part of it
// can be reused between the outputs of a single transaction.
//
// It does NOT make cumulative permanent account growth proportional to holdings,
// and must never be described that way. The amount is TRANSFERRED, not consumed
// or locked, so the same capital opens an unbounded number of accounts when
// walked across transactions:
//
//	A0 --M--> A1 --M--> A2 --M--> ...
//
// Each hop drains the previous account to zero, leaves its BaseAccount behind
// forever, and carries the identical M onward. Total supply is unchanged. This
// is demonstrated rather than assumed — see the recycling characterization in
// the tests, which walks M through 25 hops and counts 26 surviving accounts.
//
// The residual is accepted deliberately. Bounding permanent growth by
// irrecoverably spent resources requires a cost that is consumed, locked, or
// gated, which is a different mechanism at a different layer:
//
//   - TW-004, finite block gas — a per-block resource ceiling.
//   - TW-005, admission and fairness — an admission/fairness ceiling. It yields
//     an ECONOMIC cost only if the eventual fee or access design actually
//     provides one; a per-sender rate or fairness rule on its own does not.
//   - TW-006, this rule — the transaction-local shape restriction above.
//
// So #147 stays open until TW-004 and TW-005 are resolved, and this rule closes
// only the MINIMUM FIRST-FUNDING half of TW-006. The hard MsgMultiSend output
// cap is the other half and lands separately, so its value can be justified on
// its own terms.
//
// # Why it is the settlement floor
//
// It returns HardMinSettlementPayoutAmount rather than restating its value,
// because the two answer the same question and must not drift apart.
//
// x/mining validates the configured min_recipient_payout_amount at or above that
// floor and re-applies it in requirePayoutFloor at execution, so deriving from
// it gives
//
//	MinimumAccountFunding == hard floor <= every amount settlement will pay
//
// for every configuration the chain accepts: a settlement can never pay an
// amount too small to open the account it is paying into. The tests pin that
// identity as an EQUALITY. A one-sided bound would still hold if this floor were
// quietly halved, and no other test could see that weakening, because every
// other test derives its amounts from this function.
//
// The comparison below is therefore >= and not >: a payout at exactly the floor
// is one the chain has already committed to making.
//
// # Why it is compiled in rather than a genesis parameter
//
// There is no natural Params home: bank's own params cannot take a new field
// without forking the module, and x/coreslot and x/rewards do not own a bank
// send rule — putting it in either would give bank a keeper edge this
// application deliberately avoids, the same reason the economic-address rule is
// injected as a plain value.
//
// Changing it costs a coordinated upgrade, which is now a proven path. That is
// deliberate: an authority-updatable spam floor is a lever a compromised
// authority could set to zero, silently removing the protection.
//
// Returned by value, following HardMinSettlementPayoutAmount, so no caller can
// retain a reference to a shared monetary amount.
func MinimumAccountFunding() sdkmath.Int {
	return params.HardMinSettlementPayoutAmount()
}

// protocolPayoutModules names the module accounts whose transfers are exempt
// from the first-funding minimum.
//
// It is an explicit list rather than "every module account" on purpose. A
// blanket rule would silently exempt any module account added later, including
// one whose payout path took caller-chosen recipients and amounts with no floor
// of its own — reopening the vector this file exists to narrow, with nothing
// changing here to notice it.
//
// Narrowness cuts the other way too, and dangerously: a module that gains a
// payout path and is NOT listed here does not fail at review, it fails in
// production as a halted block (see newAccountFundingRestriction). That is why
// the list is paired with internal/payoutledger, which derives the real set of
// sending modules from source and fails the tests when it disagrees. The
// decision stays deliberate; only the detection is automated.
var protocolPayoutModules = []string{rewardstypes.ModuleName}

// protocolPayoutAddresses resolves that list to the addresses the restriction
// compares against, once, at wiring time.
func protocolPayoutAddresses() map[string]struct{} {
	addresses := make(map[string]struct{}, len(protocolPayoutModules))
	for _, module := range protocolPayoutModules {
		addresses[authtypes.NewModuleAddress(module).String()] = struct{}{}
	}
	return addresses
}

// newAccountFundingRestriction refuses to create an account funded below
// MinimumAccountFunding in a single transfer.
//
// Two exemptions, ordered so the cheapest check runs first.
//
// EXISTING RECIPIENTS are untouched at any amount. Ordinary trade between
// accounts that already exist is not gated, and this also covers every module
// account, since those are materialized on demand before any transfer reaches
// this point.
//
// PROTOCOL PAYOUTS — transfers originating from a listed module account — are
// exempt, and this exemption prevents a CONSENSUS HALT rather than an
// inconvenience. x/rewards PayTreasury is called from finalizeEpoch, which
// EndBlock runs in a cache context and whose error propagates out of EndBlock;
// under this repository's fail-closed rule that halts the block. A treasury
// share below this floor paid to a not-yet-funded treasury address is an
// ordinary configuration, so without the exemption such a chain would halt at
// epoch finalization. payEntitlementRemainder is the same shape: it pays
// whatever remains on an entitlement, possibly a single base unit, to the
// slot's payout address.
//
// Exempting them costs nothing here, because neither is an unbounded
// account-creation primitive:
//
//   - The settlement fan-out is the path with caller-chosen recipients, and
//     x/mining floors every recipient at max(configured, hard floor) in
//     requirePayoutFloor before the money moves.
//   - Remainder release and treasury payment each pay one canonical, configured
//     destination, bounded by the CoreSlot and reward configuration rather than
//     by how many addresses a signer can enumerate.
//
// What stays gated is user-submitted MsgSend and MsgMultiSend.
//
// The sender is consulted only once a transfer is otherwise about to be
// refused, so the common path costs the single recipient lookup and no more.
//
// The rule reads the BASE denom specifically rather than the total of the coin
// set. utwlt is the only accounting denomination on this chain, so a set
// carrying anything else contributes nothing to the cost of the account it
// would create.
func newAccountFundingRestriction(
	ak authkeeper.AccountKeeper, protocolPayouts map[string]struct{},
) banktypes.SendRestrictionFn {
	return func(ctx context.Context, fromAddr, toAddr sdk.AccAddress, amt sdk.Coins) (sdk.AccAddress, error) {
		if ak.HasAccount(ctx, toAddr) {
			return toAddr, nil
		}
		minimum := MinimumAccountFunding()
		funding := amt.AmountOf(BaseDenom)
		if funding.GTE(minimum) {
			return toAddr, nil
		}
		if _, isProtocolPayout := protocolPayouts[fromAddr.String()]; isProtocolPayout {
			return toAddr, nil
		}
		return nil, fmt.Errorf(
			"creating account %s requires at least %s%s in a single transfer, got %s%s: "+
				"a new account is permanent state that cannot be reclaimed",
			toAddr.String(), minimum.String(), BaseDenom, funding.String(), BaseDenom)
	}
}
