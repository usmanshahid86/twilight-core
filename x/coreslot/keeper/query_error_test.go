package keeper

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"cosmossdk.io/collections"

	"github.com/twilight-project/twilight-core/x/coreslot/types"
)

// The two mappers, tested directly.
//
// One of their arms cannot be reached through the query surface at all: mandatory
// state — params, the ACTIVE index, the rotation queue — is written by InitGenesis
// and maintained by consensus, so on any healthy chain it is present and no
// request can make it absent. That arm is precisely the one whose classification
// matters most, because it only ever runs when the chain is damaged, and a wrong
// answer there tells an operator "nothing configured" during an incident.
//
// Asserting it here is what makes it falsifiable. A black-box test cannot.

func codeOf(t *testing.T, err error) codes.Code {
	t.Helper()
	require.Error(t, err)
	status, ok := grpcstatus.FromError(err)
	require.Truef(t, ok, "error carries no gRPC status: %v", err)
	return status.Code()
}

// TestLookupErrorSeparatesAbsenceFromUnreadableState covers the arm a caller
// meets in normal use.
func TestLookupErrorSeparatesAbsenceFromUnreadableState(t *testing.T) {
	require.NoError(t, lookupError("slot 1", nil))

	// Absence, however it is spelled by the layer below.
	require.Equal(t, codes.NotFound, codeOf(t, lookupError("slot 9", collections.ErrNotFound)))
	require.Equal(t, codes.NotFound, codeOf(t, lookupError("slot 9", types.ErrSlotNotFound.Wrap("9"))))
	require.Equal(t, codes.NotFound,
		codeOf(t, lookupError("policy", types.ErrSelectionPolicyNotFound.Wrap("9"))))

	// Stored bytes that will not decode. The key IS there, so calling this absence
	// would tell the outside world a slot does not exist while the database
	// holding it is broken.
	require.Equal(t, codes.Internal,
		codeOf(t, lookupError("slot 9", fmt.Errorf("proto: cannot parse CoreSlot"))))
}

// TestMandatoryStateErrorNeverReportsAbsence is the arm the query surface cannot
// reach, and the reason this file exists.
//
// Absence of state every initialized chain has is corruption, not an answer. A
// consumer told NotFound would reasonably read it as "not configured yet" and
// carry on — the worst available response to a chain that cannot describe itself.
func TestMandatoryStateErrorNeverReportsAbsence(t *testing.T) {
	require.NoError(t, mandatoryStateError("params", nil))

	for name, err := range map[string]error{
		"absent":        collections.ErrNotFound,
		"undecodable":   fmt.Errorf("proto: cannot parse Params"),
		"storage fault": fmt.Errorf("iavl: version does not exist"),
	} {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, codes.Internal, codeOf(t, mandatoryStateError("params", err)),
				"mandatory state that cannot be read is a damaged chain, never absence")
		})
	}
}

// TestTransportErrorsKeepTheirOwnCode stops a client disconnect being reported as
// chain corruption.
//
// A canceled or timed-out query says nothing about this module's state, and
// flattening it into Internal would put a fault on the chain that belongs to the
// connection.
func TestTransportErrorsKeepTheirOwnCode(t *testing.T) {
	for _, mapper := range []func(string, error) error{lookupError, mandatoryStateError} {
		// Identity, not errors.Is. Wrapping with %w would keep the chain intact
		// while quietly attaching an Internal code, so a chain-only assertion
		// passes against exactly the bug this guards.
		require.Equal(t, context.Canceled, mapper("params", context.Canceled))
		require.Equal(t, context.DeadlineExceeded, mapper("params", context.DeadlineExceeded))

		// An error already classified by another mapper keeps that classification
		// rather than being re-wrapped into a second, contradictory one.
		already := grpcStatusError{code: codes.NotFound, err: types.ErrSlotNotFound.Wrap("7")}
		require.Equal(t, codes.NotFound, codeOf(t, mapper("slot 7", already)))
	}
}

// TestMappedErrorsStayInspectableInProcess protects the property the wrapper was
// designed for: a gRPC code for the wire, an intact error chain for callers here.
func TestMappedErrorsStayInspectableInProcess(t *testing.T) {
	mapped := lookupError("slot 9", types.ErrSlotNotFound.Wrap("9"))
	require.Equal(t, codes.NotFound, codeOf(t, mapped))
	require.True(t, errors.Is(mapped, types.ErrSlotNotFound),
		"an in-process caller must still be able to branch on the sentinel")
}
