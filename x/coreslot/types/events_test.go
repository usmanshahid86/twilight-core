// This file must spell the legacy "cancelled" event name in order to assert that
// the chain no longer emits it. A spell checker is exactly the wrong tool for a
// file whose subject is the two spellings.
//
//nolint:misspell // deliberate references to the pre-V2 event name.
package types_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/twilight-project/twilight-core/x/coreslot/types"
)

// The emitted event type strings, pinned as literals.
//
// Every other event test compares against the constants, which means a change to
// what a constant SPELLS passes them all: the test and the production code move
// together. These are the wire names indexers match on, so the literal is the
// contract and has to be asserted as a literal.
func TestEmittedEventTypesAreTheirCanonicalWireNames(t *testing.T) {
	for name, emitted := range map[string]string{
		"registered":             types.EventTypeRegistered,
		"activated":              types.EventTypeActivated,
		"inactivated":            types.EventTypeInactivated,
		"suspended":              types.EventTypeSuspended,
		"removed":                types.EventTypeRemoved,
		"key rotation requested": types.EventTypeKeyRotationRequested,
		"key rotated":            types.EventTypeKeyRotated,
		"payout updated":         types.EventTypePayoutUpdated,
		"metadata updated":       types.EventTypeMetadataUpdated,
		"settlement updated":     types.EventTypeSettlementUpdated,
		"selection policy":       types.EventTypeSelectionPolicyUpdated,
		"params updated":         types.EventTypeParamsUpdated,
		"validator update":       types.EventTypeValidatorUpdateEmitted,
		"rotation canceled":      types.EventTypeRotationCanceled,
	} {
		require.NotEmptyf(t, emitted, "the %s event has no type name", name)
	}

	expected := map[string]string{
		types.EventTypeRegistered:             "coreslot_registered",
		types.EventTypeActivated:              "coreslot_activated",
		types.EventTypeInactivated:            "coreslot_inactivated",
		types.EventTypeSuspended:              "coreslot_suspended",
		types.EventTypeRemoved:                "coreslot_removed",
		types.EventTypeKeyRotationRequested:   "coreslot_key_rotation_requested",
		types.EventTypeKeyRotated:             "coreslot_key_rotated",
		types.EventTypePayoutUpdated:          "coreslot_payout_updated",
		types.EventTypeMetadataUpdated:        "coreslot_metadata_updated",
		types.EventTypeSettlementUpdated:      "coreslot_settlement_updated",
		types.EventTypeSelectionPolicyUpdated: "coreslot_selection_policy_updated",
		types.EventTypeParamsUpdated:          "coreslot_params_updated",
		types.EventTypeValidatorUpdateEmitted: "coreslot_validator_update_emitted",
		types.EventTypeRotationCanceled:       "coreslot_rotation_canceled",
	}
	for actual, want := range expected {
		require.Equal(t, want, actual)
	}
	require.Len(t, expected, 14, "every event type must be pinned here")
}

// TestTheLegacyRotationCancellationSpellingIsGone is the V2 breaking change.
//
// The emitted type was renamed from coreslot_rotation_cancelled to
// coreslot_rotation_canceled. The chain emits the V2 name ONLY: there is no dual
// emission and no compatibility alias in the node, because two names for one
// occurrence would let an indexer double-count a cancellation. An indexer that
// must read pre-V2 history handles both spellings in its own decoding layer.
//
// Asserted as an inequality against the literal, so reintroducing the old spelling
// — as an alias, a second constant, or a revert — fails here.
func TestTheLegacyRotationCancellationSpellingIsGone(t *testing.T) {
	const legacy = "coreslot_rotation_cancelled"

	require.Equal(t, "coreslot_rotation_canceled", types.EventTypeRotationCanceled)
	require.NotEqual(t, legacy, types.EventTypeRotationCanceled)

	for _, emitted := range []string{
		types.EventTypeRegistered, types.EventTypeActivated, types.EventTypeInactivated,
		types.EventTypeSuspended, types.EventTypeRemoved, types.EventTypeKeyRotationRequested,
		types.EventTypeKeyRotated, types.EventTypePayoutUpdated, types.EventTypeMetadataUpdated,
		types.EventTypeSettlementUpdated, types.EventTypeSelectionPolicyUpdated,
		types.EventTypeParamsUpdated, types.EventTypeValidatorUpdateEmitted,
		types.EventTypeRotationCanceled,
	} {
		require.NotEqualf(t, legacy, emitted, "no event may carry the legacy spelling")
	}
}

// TestRotationCancelReasonsAreUnchangedByTheRename states what the V2 rename did
// NOT touch.
//
// Only the event type name changed. The reason values are part of the same
// integration surface, and an indexer updating its event-name match must not have
// to revisit these as well.
func TestRotationCancelReasonsAreUnchangedByTheRename(t *testing.T) {
	require.Equal(t, "lifecycle_change", types.RotationCancelReasonLifecycle)
	require.Equal(t, "stale_rotation", types.RotationCancelReasonStale)
}
