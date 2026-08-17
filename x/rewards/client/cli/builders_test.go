package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/cosmos/cosmos-sdk/client/flags"
	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"

	"github.com/twilight-project/twilight-core/x/rewards/types"
)

func paginatedFlags(t *testing.T, set map[string]string) *cobra.Command {
	t.Helper()
	c := &cobra.Command{}
	flags.AddPaginationFlagsToCmd(c, "test")
	for k, v := range set {
		require.NoError(t, c.Flags().Set(k, v))
	}
	return c
}

func TestBuildSlotRewardsRequest(t *testing.T) {
	c := paginatedFlags(t, map[string]string{"limit": "5"})
	req, err := buildSlotRewardsRequest([]string{"3"}, c.Flags())
	require.NoError(t, err)
	require.Equal(t, uint64(3), req.SlotId)
	require.NotNil(t, req.Pagination)
	require.Equal(t, uint64(5), req.Pagination.Limit)

	_, err = buildSlotRewardsRequest([]string{"not-a-number"}, c.Flags())
	require.Error(t, err)
}

func TestBuildCurrentActiveBlocksRequest(t *testing.T) {
	c := paginatedFlags(t, map[string]string{"limit": "4", "offset": "2"})
	req, err := buildCurrentActiveBlocksRequest(c.Flags())
	require.NoError(t, err)
	require.NotNil(t, req.Pagination)
	require.Equal(t, uint64(4), req.Pagination.Limit)
	require.Equal(t, uint64(2), req.Pagination.Offset)
}

func TestBuildEpochRewardRequest(t *testing.T) {
	req, err := buildEpochRewardRequest([]string{"7"})
	require.NoError(t, err)
	require.Equal(t, uint64(7), req.EpochNumber)
	_, err = buildEpochRewardRequest([]string{"x"})
	require.Error(t, err)
}

func TestBuildClaimableRequest(t *testing.T) {
	req, err := buildClaimableRequest([]string{"3", "1", "5"})
	require.NoError(t, err)
	require.Equal(t, uint64(3), req.SlotId)
	require.Equal(t, uint64(1), req.StartEpoch)
	require.Equal(t, uint64(5), req.EndEpoch)
	_, err = buildClaimableRequest([]string{"3", "bad", "5"})
	require.Error(t, err)
}

// TestBuildPauseAndResumeMsgAreGlobal pins that the CLI builds a global pause.
//
// The deprecated per-area selectors must stay unset: leaving them false is what
// keeps the message from implying a partial pause the handler would ignore.
func TestBuildPauseAndResumeMsgAreGlobal(t *testing.T) {
	pause, err := buildPauseMsg("auth")
	require.NoError(t, err)
	require.Equal(t, "auth", pause.EmergencyAuthority)
	require.False(t, pause.PauseEmissions)
	require.False(t, pause.PauseEpochSettlement)
	require.False(t, pause.PauseClaims)

	resume, err := buildResumeMsg("auth")
	require.NoError(t, err)
	require.Equal(t, "auth", resume.EmergencyAuthority)
	require.False(t, resume.ResumeEmissions)
	require.False(t, resume.ResumeEpochSettlement)
	require.False(t, resume.ResumeClaims)
}

func TestBuildEpochBoundariesRequest(t *testing.T) {
	req, err := buildEpochBoundariesRequest([]string{"7"})
	require.NoError(t, err)
	require.Equal(t, uint64(7), req.EpochNumber)

	_, err = buildEpochBoundariesRequest([]string{"bad"})
	require.Error(t, err)
}
func TestBuildClaimMsg(t *testing.T) {
	msg, err := buildClaimMsg("signer-addr", "3", "1", "5")
	require.NoError(t, err)
	require.Equal(t, "signer-addr", msg.Signer)
	require.Equal(t, uint64(3), msg.SlotId)
	require.Equal(t, uint64(1), msg.StartEpoch)
	require.Equal(t, uint64(5), msg.EndEpoch)

	_, err = buildClaimMsg("signer-addr", "x", "1", "5")
	require.Error(t, err)
}

func TestBuildUpdateParamsMsg(t *testing.T) {
	registry := codectypes.NewInterfaceRegistry()
	types.RegisterInterfaces(registry)
	cdc := codec.NewProtoCodec(registry)

	// Valid params JSON.
	params := types.DefaultParams()
	good := filepath.Join(t.TempDir(), "params.json")
	require.NoError(t, os.WriteFile(good, cdc.MustMarshalJSON(&params), 0o600))
	msg, err := buildUpdateParamsMsg(cdc, "authority-addr", good)
	require.NoError(t, err)
	require.Equal(t, "authority-addr", msg.Authority)
	require.Equal(t, params.MaxSupply, msg.Params.MaxSupply)
	require.Equal(t, params.NativeDenom, msg.Params.NativeDenom)

	// Malformed JSON.
	bad := filepath.Join(t.TempDir(), "bad.json")
	require.NoError(t, os.WriteFile(bad, []byte("{ not valid json"), 0o600))
	_, err = buildUpdateParamsMsg(cdc, "authority-addr", bad)
	require.Error(t, err)

	// Missing file.
	_, err = buildUpdateParamsMsg(cdc, "authority-addr", filepath.Join(t.TempDir(), "missing.json"))
	require.Error(t, err)
}
