package app

import (
	"context"
	"fmt"

	sdkmath "cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authkeeper "github.com/cosmos/cosmos-sdk/x/auth/keeper"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"

	"github.com/twilight-project/twilight-core/app/params"
)

// MinimumAccountFunding is the smallest amount of the base denom that may bring a
// NEW account into existence.
//
// # What it is for
//
// A fresh recipient gets a permanent BaseAccount of roughly 100-130 bytes, and
// that record is NOT reclaimable: setBalance deletes a zero balance, but the
// account survives. With free transactions there is nothing to stop those being
// created in bulk — the measured rate is about 340,000 accounts in one 21 MB
// block, roughly 40 MB of permanent IAVL growth that degrades state-sync,
// pruning and export for the life of the chain. Gas does not bound it while
// max_gas is -1; bytes bind. That is TW-006.
//
// # Why it is the settlement floor and not a number of its own
//
// It returns HardMinSettlementPayoutAmount rather than restating its value,
// because the two answer the same question and must not drift apart.
//
// The safety argument depends on that identity. x/mining's configured
// min_recipient_payout_amount is validated to be at or above the hard floor, and
// requirePayoutFloor re-applies the hard floor at execution, so deriving from it
// gives
//
//	MinimumAccountFunding == hard floor <= every amount settlement will pay
//
// for every configuration the chain will accept. A settlement therefore can
// never pay an amount too small to open the account it is paying into — the
// chain cannot owe money it is unable to deliver. Restating 10_000 here would
// leave that invariant to be maintained by hand in two places, and a settlement
// halt is how the mistake would surface.
//
// Settlement is not the only path that pays an account, and the others are NOT
// floored: remainder release pays whatever is left on an entitlement. Those are
// covered by exempting protocol payouts outright, described below, rather than
// by this number.
//
// It also means the comparison below is >= rather than >: a payout at exactly
// the floor is one the chain has already committed to making.
//
// A larger, independent number was considered and rejected. The threshold bounds
// permanent account growth relative to token holdings — roughly holdings divided
// by this value — so a bigger number bounds it more tightly. But it is also an
// ONBOARDING FLOOR: to receive a first payment at all, someone must be sent at
// least this much. At 1 twlt the floor would be about ten thousand times more
// restrictive, relative to supply, than Polkadot's existential deposit, and
// would stop anyone holding less than 1 twlt from bringing a new person onto the
// chain. A tighter state bound is not worth that, and it would break the
// identity above.
//
// # What it does NOT do
//
// It does not close TW-006 on its own, and must not be described as doing so.
// Transactions are free and tokens are obtainable, so an attacker converts their
// balance into accounts at this rate rather than at no cost at all: the damage
// becomes proportional to holdings instead of unbounded. Bounding the ATTACKER
// needs a fee floor or access control, which is the rest of #147.
//
// It also does not restrict ordinary transfers. Sends between accounts that
// already exist are untouched at any amount; only the creation of a new account
// is gated.
//
// # Why it is compiled in rather than a genesis parameter
//
// There is no natural Params home: bank's own params cannot take a new field
// without forking the module, and x/coreslot and x/rewards do not own a bank
// send rule — putting it in either would give bank a keeper edge this
// application deliberately avoids, the same reason the economic-address rule is
// injected as a plain value.
//
// Changing it costs a coordinated upgrade, which is now a proven path rather
// than a theoretical one. That is deliberate: an authority-updatable spam floor
// is a lever a compromised authority could set to zero, silently removing the
// protection, and #130 exists precisely to constrain authority reach. Promoting
// this to a parameter later is an ordinary state-machine change; removing an
// authority lever after chains depend on it is not.
//
// Returned by value, following HardMinSettlementPayoutAmount, so no caller can
// retain a reference to a shared monetary amount.
func MinimumAccountFunding() sdkmath.Int {
	return params.HardMinSettlementPayoutAmount()
}

// newAccountFundingRestriction refuses to create an account funded below
// MinimumAccountFunding.
//
// Two exemptions, in the order the checks are cheapest:
//
// EXISTING RECIPIENTS are untouched at any amount. Ordinary trade between
// accounts that already exist is not gated, and this also covers every module
// account, since those are materialized on demand before any transfer reaches
// this point.
//
// PROTOCOL PAYOUTS — sends originating from a module account — are exempt, and
// this exemption is load-bearing rather than a convenience. x/rewards
// payEntitlementRemainder pays whatever remains on an entitlement, which may be
// a single base unit, to the slot's payout address; gating that would make the
// chain unable to discharge a debt it has already recorded, and the refusal
// would surface as a failed release rather than as anything pointing here.
//
// It costs nothing in spam terms, because neither module payout path is an
// unbounded account-creation primitive:
//
//   - The settlement fan-out is the one with caller-chosen recipients, and
//     x/mining floors every recipient at max(configured, hard floor) in
//     requirePayoutFloor before the money moves, so it is already bounded by the
//     same number this rule enforces.
//   - Remainder release pays exactly one recipient per (slot, epoch) — the
//     slot's own payout address — so it is bounded by the authority-admitted
//     CoreSlot set rather than by how many addresses a signer can enumerate.
//
// What remains unbounded without this rule is user-submitted MsgSend and
// MsgMultiSend, which is precisely what stays gated.
//
// The sender is consulted only once a send is otherwise about to be refused, so
// the common path costs the single recipient lookup and no more.
//
// The rule reads the BASE denom specifically rather than the total of the coin
// set. utwlt is the only accounting denomination on this chain, so a set
// carrying anything else contributes nothing to the cost of the account it would
// create.
func newAccountFundingRestriction(ak authkeeper.AccountKeeper) banktypes.SendRestrictionFn {
	return func(ctx context.Context, fromAddr, toAddr sdk.AccAddress, amt sdk.Coins) (sdk.AccAddress, error) {
		if ak.HasAccount(ctx, toAddr) {
			return toAddr, nil
		}
		minimum := MinimumAccountFunding()
		funding := amt.AmountOf(BaseDenom)
		if funding.GTE(minimum) {
			return toAddr, nil
		}
		if _, isProtocolPayout := ak.GetAccount(ctx, fromAddr).(sdk.ModuleAccountI); isProtocolPayout {
			return toAddr, nil
		}
		return nil, fmt.Errorf(
			"creating account %s requires at least %s%s, got %s%s: a new account is "+
				"permanent state that cannot be reclaimed",
			toAddr.String(), minimum.String(), BaseDenom, funding.String(), BaseDenom)
	}
}
