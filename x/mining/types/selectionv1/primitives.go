package selectionv1

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

// The five frozen V1 hash primitives.
//
// Each function assembles its preimage with straight-line appends in exactly the
// order the specification lists, so the body can be read against the spec text
// without decoding an abstraction. SHA-256 and HMAC-SHA-256 are the only
// cryptographic functions V1 uses; both produce exactly 32 raw bytes.

// ComputeDrawID implements DrawIDV1 (r6 §38):
//
//	HMAC_SHA256(
//	    key = participation_secret,
//	    msg = DOMAIN_DRAW_ID || ChainIDEncoding(chain_id)
//	       || U64BE(slot_id) || U64BE(target_epoch)
//	)
//
// The participation secret is participant-private and never reaches consensus.
// The function lives here so an Authorization Server, a client and a public
// verifier all derive the same draw ID from the same secret.
func ComputeDrawID(participationSecret [HashSize]byte, sc SelectionContext) (DrawID, error) {
	msg := make([]byte, 0, len(DomainDrawID)+2+len(sc.ChainID)+8+8)
	msg = append(msg, DomainDrawID...)
	msg, err := sc.appendTo(msg)
	if err != nil {
		return DrawID{}, err
	}

	mac := hmac.New(sha256.New, participationSecret[:])
	// hash.Hash.Write is documented never to return an error.
	mac.Write(msg)

	var out DrawID
	copy(out[:], mac.Sum(nil))
	return out, nil
}

// ComputeCandidateSetHash implements CandidateSetHashV1 (r6 §39):
//
//	SHA256(
//	    DOMAIN_CANDIDATE_SET || ChainIDEncoding(chain_id)
//	 || U64BE(slot_id) || U64BE(target_epoch) || U64BE(n)
//	 || draw_id[0] || ... || draw_id[n-1]
//	)
//
// The specification defines the input as "the strictly increasing canonical
// sequence" of draw IDs, so canonicality is enforced here rather than assumed:
// a candidate-set commitment computed over a non-canonical list would commit to
// an ordering the protocol does not admit. n = 0 is defined and yields a
// deterministic nonzero digest.
func ComputeCandidateSetHash(sc SelectionContext, drawIDs []DrawID) (Hash, error) {
	if err := ValidateCanonicalDrawIDs(drawIDs); err != nil {
		return Hash{}, err
	}

	buf := make([]byte, 0, len(DomainCandidateSet)+2+len(sc.ChainID)+8+8+8+HashSize*len(drawIDs))
	buf = append(buf, DomainCandidateSet...)
	buf, err := sc.appendTo(buf)
	if err != nil {
		return Hash{}, err
	}
	buf = binary.BigEndian.AppendUint64(buf, uint64(len(drawIDs)))
	for _, id := range drawIDs {
		buf = append(buf, id[:]...)
	}

	return sha256.Sum256(buf), nil
}

// ComputeBeaconHash implements BeaconHashV1 (r6 §41):
//
//	SHA256(
//	    DOMAIN_BEACON || ChainIDEncoding(chain_id)
//	 || U64BE(slot_id) || U64BE(target_epoch)
//	 || candidate_set_hash
//	 || U64BE(beacon_start_height) || U64BE(beacon_end_height) || U64BE(m)
//	 || BeaconEntryV1[0] || ... || BeaconEntryV1[m-1]
//	)
//
// where BeaconEntryV1 = U64BE(height) || U64BE(proposer_slot_id) || block_hash
// (r6 §40) and entries appear in strictly increasing height order.
//
// SelectionCommitment.committed_height is deliberately absent from the preimage:
// commitment timing within the permitted pre-beacon window must not change the
// randomness. The proposer Slot ID is deliberately present, so historical
// consensus-key attribution is committed into the beacon itself.
func ComputeBeaconHash(
	sc SelectionContext,
	candidateSetHash Hash,
	beaconStartHeight, beaconEndHeight uint64,
	entries []BeaconEntry,
) (Hash, error) {
	if err := validateEntryOrder(entries); err != nil {
		return Hash{}, err
	}

	buf := make([]byte, 0,
		len(DomainBeacon)+2+len(sc.ChainID)+8+8+HashSize+8+8+8+(8+8+HashSize)*len(entries))
	buf = append(buf, DomainBeacon...)
	buf, err := sc.appendTo(buf)
	if err != nil {
		return Hash{}, err
	}
	buf = append(buf, candidateSetHash[:]...)
	buf = binary.BigEndian.AppendUint64(buf, beaconStartHeight)
	buf = binary.BigEndian.AppendUint64(buf, beaconEndHeight)
	buf = binary.BigEndian.AppendUint64(buf, uint64(len(entries)))
	for _, e := range entries {
		buf = binary.BigEndian.AppendUint64(buf, e.Height)
		buf = binary.BigEndian.AppendUint64(buf, e.ProposerSlotID)
		buf = append(buf, e.BlockHash[:]...)
	}

	return sha256.Sum256(buf), nil
}

// ComputeTicket implements TicketV1 (r6 §42):
//
//	SHA256(
//	    DOMAIN_TICKET || ChainIDEncoding(chain_id)
//	 || U64BE(slot_id) || U64BE(target_epoch)
//	 || candidate_set_hash || beacon_hash || draw_id
//	)
//
// The ticket is a deterministic random-looking score, not a credential. It is
// unknowable from the committed candidate data until the beacon exists, and
// computable by anyone from public data once it does.
func ComputeTicket(sc SelectionContext, candidateSetHash, beaconHash Hash, drawID DrawID) (Hash, error) {
	buf := make([]byte, 0, len(DomainTicket)+2+len(sc.ChainID)+8+8+HashSize*3)
	buf = append(buf, DomainTicket...)
	buf, err := sc.appendTo(buf)
	if err != nil {
		return Hash{}, err
	}
	buf = append(buf, candidateSetHash[:]...)
	buf = append(buf, beaconHash[:]...)
	buf = append(buf, drawID[:]...)

	return sha256.Sum256(buf), nil
}

// ComputeSelectedDrawIDsHash implements SelectedDrawIDsHashV1 (r6 §37.1):
//
//	SHA256(
//	    DOMAIN_SELECTED_DRAW_IDS || ChainIDEncoding(chain_id)
//	 || U64BE(slot_id) || U64BE(target_epoch) || U64BE(n)
//	 || selected[0] || ... || selected[n-1]
//	)
//
// Unlike CandidateSetHashV1 this function does NOT require a sorted input. The
// order is significant and is exactly the order carried by
// MsgPublishSelectionResult, which is ranking order rather than draw-ID order.
// Reordering the same IDs therefore yields a different digest, and that is the
// intended behavior: the digest commits to the ranking, not merely to the set.
// n = 0 is defined.
func ComputeSelectedDrawIDsHash(sc SelectionContext, selected []DrawID) (Hash, error) {
	buf := make([]byte, 0, len(DomainSelectedDrawIDs)+2+len(sc.ChainID)+8+8+8+HashSize*len(selected))
	buf = append(buf, DomainSelectedDrawIDs...)
	buf, err := sc.appendTo(buf)
	if err != nil {
		return Hash{}, err
	}
	buf = binary.BigEndian.AppendUint64(buf, uint64(len(selected)))
	for _, id := range selected {
		buf = append(buf, id[:]...)
	}

	return sha256.Sum256(buf), nil
}

// ValidateCanonicalDrawIDs reports whether a candidate list is in the single
// canonical form: strictly increasing raw-byte lexicographic order. Strictness
// rejects duplicates as well as misordering, which is what makes the candidate
// count an honest cardinality. The empty list is canonical.
func ValidateCanonicalDrawIDs(drawIDs []DrawID) error {
	for i := 1; i < len(drawIDs); i++ {
		if bytes.Compare(drawIDs[i-1][:], drawIDs[i][:]) >= 0 {
			return fmt.Errorf(
				"%w: entry %d (%s) does not precede entry %d (%s)",
				ErrCandidateListNotCanonical, i-1, drawIDs[i-1], i, drawIDs[i],
			)
		}
	}
	return nil
}
