package app

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"
	sdkerrors "github.com/cosmos/cosmos-sdk/types/errors"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
)

// MaxBankOutputsPerTx is the largest number of bank recipient outputs one
// transaction may carry, counted across every bank message in it.
//
// # What is counted
//
// One per MsgSend, len(Outputs) per MsgMultiSend, nothing for anything else.
// The unit is the OUTPUT OPERATION, not the distinct recipient: two outputs to
// the same address are two writes and count twice.
//
// # What this bounds
//
// The recipient fan-out a SINGLE TRANSACTION can require of consensus. One
// signature currently buys an unbounded number of balance writes: a MsgMultiSend
// output costs 67 bytes on the wire, so a 1 MiB message carries about 15,650 of
// them and a 21 MiB block about 328,000, driven by roughly twenty signature
// verifications. This restores proportionality between what a transaction costs
// to authenticate and what it costs to execute.
//
// It does NOT bound per-block work, cumulative account growth, or an attacker's
// economic cost, and must not be described as doing any of those. An attacker
// splits across transactions; bounding the block is TW-004's finite max_gas, and
// bounding admission is TW-005. This closes neither, and #147 stays open.
//
// # Why the count spans the whole transaction
//
// The audit named unbounded MsgMultiSend specifically. Implementation review
// found the same fan-out reachable without it: a transaction may carry many
// messages, so
//
//	MsgSend A -> fresh1
//	MsgSend A -> fresh2
//	... MsgSend A -> fresh100
//
// produces a hundred recipient writes under ONE authentication envelope. Capping
// MsgMultiSend alone would have left that untouched while claiming a
// transaction-wide bound. The rule is therefore over the real security unit —
// bank outputs per transaction — and a user cannot escape it by splitting
// fan-out across bank messages, or between message types, inside one
// transaction.
//
// # Why 32
//
// An unprivileged bank transaction may not demand more recipient-output fan-out
// than the immutable maximum permitted in one AUTHORIZED SETTLEMENT CHUNK.
//
// The claim is deliberately about a chunk, not about a privileged transaction in
// general: HardMaxChunksPerSettlement allows a settlement to span four chunks,
// so the protocol's broader settlement fan-out reaches 128. Nothing here proves
// 32 is the maximum a privileged transaction may cause, and that stronger
// statement is not made. HardMaxChunksPerSettlement is deliberately absent from
// this calculation.
//
// 32 is an independently ratified bound on bank transactions.
// HardMaxRecipientsPerChunk is EVIDENCE that fan-out of this magnitude is
// already accepted by the chain — not the source of this value. In #159,
// deriving MinimumAccountFunding from HardMinSettlementPayoutAmount was
// load-bearing for correctness: a higher anti-spam floor would have left the
// chain unable to pay a debt it had already recorded. No such invariant exists
// here, so binding the two would let a settlement-capacity decision silently
// widen the spam surface.
//
// The tests keep a one-directional relation, cap <= HardMaxRecipientsPerChunk.
// Its meaning is narrow: it prevents the unprivileged bank bound from becoming
// looser than the current per-chunk authorized bound. It does NOT make the two
// parameters semantically identical, and the settlement bound stays free to rise
// without dragging this one with it.
//
// Splitting is free while transactions carry no fee, so a larger batch costs
// transaction count rather than exclusion. Raising this is an ordinary
// state-machine change at an upgrade boundary.
//
// # Scope
//
// The second of the two controls the audit specifies for TW-006, the first being
// the funding minimum in app/sendrestriction.go. Neither closes #147.
const MaxBankOutputsPerTx = 32

// bankOutputsIn counts the recipient outputs a transaction's TOP-LEVEL messages
// would produce.
//
// Top-level is sufficient for this application, and that is a checked property
// rather than an assumption: no wired module executes nested messages. The
// module list is auth, bank, upgrade, consensus, tx, coreslot, rewards and
// mining — authz, group and gov are among the modules this chain deliberately
// omits, and no custom message carries an executable nested message.
//
// If a module that can execute nested bank messages is ever wired in, this
// count silently stops being complete. That is a reason to revisit this rule
// deliberately at that time, not a reason to add speculative recursive
// inspection now.
func bankOutputsIn(tx sdk.Tx) int {
	outputs := 0
	for _, msg := range tx.GetMsgs() {
		switch bankMsg := msg.(type) {
		case *banktypes.MsgSend:
			outputs++
		case *banktypes.MsgMultiSend:
			outputs += len(bankMsg.Outputs)
		}
	}
	return outputs
}

// newBankOutputCap prepends the bank-output cap to an existing ante handler.
//
// # Why an ante decorator, given this repository has no ValidateBasic layer
//
// #126 records the deliberate position that validation lives next to the state
// it guards rather than in an automatic layer. That governs the modules this
// chain owns. It cannot govern bank's messages: bank is not ours, so there is no
// handler to put the check beside, and a bank SendRestriction is invoked once
// per output and never learns how many a transaction carries. The ante is the
// seam for messages we do not own.
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
// validator first paying for signature verification, which is the behavior worth
// having under flood. The check is a bounded stateless scan over tx.GetMsgs()
// and reads no state.
func newBankOutputCap(next sdk.AnteHandler) sdk.AnteHandler {
	// Fail closed. A nil chain here means the tx module did not install one, and
	// continuing would leave this check as the ONLY ante handler — silently
	// disabling signature verification, sequence checks and gas setup. That is a
	// build misconfiguration, so it must stop the node rather than be tolerated.
	if next == nil {
		panic("no ante handler to wrap: the transaction chain is absent, and installing " +
			"the bank output cap alone would disable signature verification")
	}
	return func(ctx sdk.Context, tx sdk.Tx, simulate bool) (sdk.Context, error) {
		if outputs := bankOutputsIn(tx); outputs > MaxBankOutputsPerTx {
			return ctx, sdkerrors.ErrInvalidRequest.Wrap(fmt.Sprintf(
				"transaction carries %d bank recipient outputs, above the maximum of %d; "+
					"a single transaction may not demand more recipient fan-out than the "+
					"maximum permitted in one authorized settlement chunk",
				outputs, MaxBankOutputsPerTx))
		}
		return next(ctx, tx, simulate)
	}
}
