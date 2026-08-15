// Package selectionv1 implements the frozen byte and arithmetic contracts of
// Participant Selection Protocol V1.
//
// Everything here is pure. The package holds no state, reads no store, performs
// no I/O, consults no clock or randomness, starts no goroutine, and never
// iterates a Go map to produce an ordered result. Every exported function is a
// deterministic function of its arguments alone, which is what allows an
// independent verifier and a consensus node to agree byte for byte.
//
// # Scope
//
// The package covers the five frozen hash primitives, beacon-window derivation
// and filtering, the deterministic selected-participant count K, ticket ranking,
// the height rules governing commitment and result publication, and the
// structural validation of a published result against its commitment.
//
// It deliberately does NOT resolve historical block proposers. Attribution of a
// block proposer to a Core Slot at a historical height requires consensus-key
// history, which is chain state. Callers supply already-resolved proposer Slot
// IDs; a height whose attribution could not be resolved is simply omitted from
// the observed window, which makes it unusable exactly as the protocol requires.
//
// # Byte discipline
//
// Hash preimages are built from exact bytes. JSON, protobuf, hexadecimal text
// and native integer layouts are never hashed. Hexadecimal appears only at the
// transport boundary, in the FromHex constructors, and never inside a preimage.
// Each preimage is assembled by straight-line appends in the order the
// specification lists them, so the code can be read against the spec line by
// line; that transparency is worth more here than concision.
//
// # Naming
//
// The specification says "selected participant" where earlier drafts and the
// golden-vector packs say "winner". The two mean the same thing. This package
// follows the specification.
package selectionv1
