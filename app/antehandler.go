package app

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
)

// MaxMultiSendOutputsPerTx is the largest number of MsgMultiSend outputs one
// transaction may carry, counted across every MsgMultiSend it contains.
//
// # What this bounds
//
// The work a SINGLE TRANSACTION can require of consensus. One signature
// currently buys an unbounded number of balance writes: a MsgMultiSend output
// costs 67 bytes on the wire, so a 1 MiB message carries about 15,650 of them
// and a 21 MiB block about 328,000 — driven by roughly twenty signature
// verifications. This restores proportionality between what a transaction costs
// to authenticate and what it costs to execute.
//
// It does NOT bound per-block work, and must not be described as doing so. An
// attacker splits across transactions; bounding the block is TW-004's finite
// max_gas, and the two compose, because more transactions means more gas.
//
// # Why 32
//
// One transaction may cause no more fan-out than the chain's most privileged
// transaction already may. A slot operator holding settlement credentials is
// limited to HardMaxRecipientsPerChunk recipients in one MsgSettlementChunk;
// before this rule, an anonymous sender could demand hundreds of times more from
// the same validators. That inversion — the permissionless path bounded more
// loosely than the authorized one — is what this closes.
//
// The settlement bound is cited as evidence that 32 is a fan-out this chain
// already considers reasonable. It is deliberately NOT derived from it. In #159
// deriving MinimumAccountFunding from HardMinSettlementPayoutAmount was
// load-bearing for correctness: a higher anti-spam floor would have left the
// chain unable to pay a debt it had already recorded. No such invariant exists
// here — nothing breaks if the two differ — so binding them would couple two
// decisions with different rationales, and raising the settlement bound for
// settlement reasons would silently widen the spam surface. The tests encode the
// principle one-directionally instead.
//
// Splitting is free while transactions carry no fee, so a larger batch costs
// transaction count rather than exclusion. Raising this is an ordinary
// state-machine change at an upgrade boundary.
//
// # Scope
//
// This is the second of the two controls the audit specifies for TW-006, the
// first being the funding minimum in app/sendrestriction.go. Neither closes
// #147, which stays open until TW-004 and TW-005 are resolved.
const MaxMultiSendOutputsPerTx = 32

// newMultiSendOutputCap prepends the output cap to an existing ante handler.
//
// # Why an ante decorator, given this repository has no ValidateBasic layer
//
// #126 records the deliberate position that validation lives next to the state
// it guards rather than in an automatic layer. That governs the modules this
// chain owns. It cannot govern MsgMultiSend: bank is not ours, so there is no
// handler to put the check beside, and a bank SendRestriction is invoked once
// per output and never learns how many there are. The ante is the seam for
// messages we do not own, which is the only place the audit asks for one.
//
// # Why wrap rather than rebuild
//
// The chain the tx module installs through depinject is left byte-for-byte
// intact and this check is prepended to it. Reconstructing that chain by hand
// would risk dropping or reordering a decorator — a change to signature
// verification or sequence handling that would pass most tests and split
// consensus.
//
// Running BEFORE the chain means an oversized transaction is refused without the
// validator first paying for signature verification, which is the behavior
// worth having under flood. The check is a bounded stateless scan over
// tx.GetMsgs() and reads no state.
//
// # Counted per transaction, not per message
//
// MsgMultiSend permits exactly one input, so only outputs matter. Nothing in the
// standard ante chain limits how many messages a transaction may carry, so a
// per-message cap would be defeated by sending many MsgMultiSend messages in one
// transaction. Outputs are therefore summed across all of them.
func newMultiSendOutputCap(next sdk.AnteHandler) sdk.AnteHandler {
	// Fail closed. A nil chain here means the tx module did not install one, and
	// continuing would leave this check as the ONLY ante handler — silently
	// disabling signature verification, sequence checks and gas setup. That is a
	// build misconfiguration, so it must stop the node rather than be tolerated.
	if next == nil {
		panic("no ante handler to wrap: the transaction chain is absent, and installing " +
			"the MsgMultiSend output cap alone would disable signature verification")
	}
	return func(ctx sdk.Context, tx sdk.Tx, simulate bool) (sdk.Context, error) {
		outputs := 0
		for _, msg := range tx.GetMsgs() {
			multiSend, ok := msg.(*banktypes.MsgMultiSend)
			if !ok {
				continue
			}
			outputs += len(multiSend.Outputs)
		}
		if outputs > MaxMultiSendOutputsPerTx {
			return ctx, sdkerrors.ErrInvalidRequest.Wrap(fmt.Sprintf(
				"transaction carries %d MsgMultiSend outputs, above the maximum of %d; "+
					"a single transaction may not require more fan-out of consensus than "+
					"the chain's most privileged transaction may",
				outputs, MaxMultiSendOutputsPerTx))
		}
		return next(ctx, tx, simulate)
	}
}
