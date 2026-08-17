package types

import (
	"fmt"

	sdk "github.com/cosmos/cosmos-sdk/types"

	appparams "github.com/twilight-project/twilight-core/app/params"
)

// Stateless admission for the settlement transactions.
//
// # What belongs here and what does not
//
// Only what can be decided from the message bytes alone: identifiers are present,
// the payout list is within its ABSOLUTE bound, and every line is
// well-formed. Everything that needs chain state — whether the settlement exists,
// who may sign for it, which chunk is next, what the target-bound parameters
// permit — is decided in the handler, against a cache, and is not repeated here.
//
// The split is not stylistic. In recent SDK versions stateless validation is not
// guaranteed to run on every execution path, so a check that lives ONLY here is a
// check that may not run. Nothing consensus-critical may depend on this file
// alone; the handler re-derives everything it needs. What this buys is a cheap
// rejection of obvious garbage before a handler opens a cache and reads state.
//
// Note which bound is applied and which is not. The hard ceiling on recipients per
// chunk is a protocol constant and can be checked without state; the CONFIGURED
// ceiling is target-bound and cannot, so it is a handler check. Applying only the
// weaker one here is deliberate — a stateless check that guessed at the configured
// value would be wrong for every target whose parameters differ from the guess.

var _ sdk.Msg = &MsgSubmitSettlementChunk{}

// Validate performs the stateless part of chunk admission.
func (m MsgSubmitSettlementChunk) Validate() error {
	if m.SlotId == 0 {
		return ErrInvalidState.Wrap("slot identifiers start at 1")
	}
	if m.Epoch == 0 {
		return ErrInvalidState.Wrap("epoch numbers start at 1")
	}
	if len(m.Payouts) == 0 {
		return ErrInvalidState.Wrapf(
			"a settlement chunk for slot %d in epoch %d names no recipients; "+
				"a chunk that pays nobody is not a valid chunk",
			m.SlotId, m.Epoch)
	}
	// Compared as an int against a uint64 bound: the length is bounded by the tx
	// size long before it could approach the conversion edge, and comparing in the
	// wider type avoids a narrowing that a future larger bound would silently break.
	if uint64(len(m.Payouts)) > appparams.HardMaxRecipientsPerChunk {
		return ErrInvalidState.Wrapf(
			"a settlement chunk names %d recipients, above the immutable maximum of %d",
			len(m.Payouts), appparams.HardMaxRecipientsPerChunk)
	}
	for i, payout := range m.Payouts {
		// A nil element cannot arrive from the wire, but it can arrive from a Go
		// caller that built the message directly. Refusing it here means no later
		// stage has to defend against a payout line that is not a payout line.
		if payout == nil {
			return ErrInvalidState.Wrapf("chunk payout line %d is empty", i)
		}
		if payout.Recipient == "" {
			return ErrInvalidAddress.Wrapf("chunk payout line %d names no recipient", i)
		}
		// The amount's VALUE is checked against the floors in the handler, where the
		// target-bound minimum is known. What is checked here is only that the field
		// is a canonical base-10 integer at all, which needs no state.
		if _, err := ParseCanonicalAmount("chunk payout line amount", payout.Amount); err != nil {
			return err
		}
	}
	return nil
}

// ChunkPayoutLabel names one payout line in an error message.
//
// One helper so a rejected line reads the same wherever it was refused — the
// stateless pass and the handler both reject amounts, and an operator correlating
// a failure should not have to reconcile two phrasings of the same position.
func ChunkPayoutLabel(slotID, epoch uint64, index int) string {
	return fmt.Sprintf("chunk payout line %d for slot %d in epoch %d", index, slotID, epoch)
}
