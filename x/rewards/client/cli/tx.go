package cli

import (
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	clienttx "github.com/cosmos/cosmos-sdk/client/tx"
	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/x/rewards/types"
)

// GetTxCmd returns the rewards transaction command tree. It wraps only the
// existing Msgs; it adds no new messages and infers no authority.
func GetTxCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "rewards", Short: "Rewards module transactions", DisableFlagParsing: true, SuggestionsMinimumDistance: 2}
	cmd.AddCommand(updateParamsCmd(), pauseCmd(), resumeCmd(), claimCmd())
	return cmd
}

func broadcast(cmd *cobra.Command, msg sdk.Msg) error {
	ctx, err := client.GetClientTxContext(cmd)
	if err != nil {
		return err
	}
	return clienttx.GenerateOrBroadcastTxCLI(ctx, cmd.Flags(), msg)
}

func signer(cmd *cobra.Command) (string, error) {
	ctx, err := client.GetClientTxContext(cmd)
	if err != nil {
		return "", err
	}
	return ctx.GetFromAddress().String(), nil
}

func txCmd(use string, args cobra.PositionalArgs, run func(*cobra.Command, []string) error) *cobra.Command {
	cmd := &cobra.Command{Use: use, Args: args, RunE: run}
	flags.AddTxFlagsToCmd(cmd)
	return cmd
}

// updateParamsCmd queues a full Params update from a JSON file. The keeper rejects
// changes to the immutable native denom / max supply; the CLI simply forwards the
// signed Params and does not infer authority.
func updateParamsCmd() *cobra.Command {
	return txCmd("update-params [params-json-file]", cobra.ExactArgs(1), func(cmd *cobra.Command, args []string) error {
		ctx, err := client.GetClientTxContext(cmd)
		if err != nil {
			return err
		}
		msg, err := buildUpdateParamsMsg(ctx.Codec, ctx.GetFromAddress().String(), args[0])
		if err != nil {
			return err
		}
		return clienttx.GenerateOrBroadcastTxCLI(ctx, cmd.Flags(), msg)
	})
}

// pauseCmd schedules a GLOBAL rewards pause for the next block.
//
// The per-area flags are gone with the switches they drove: there is one pause
// state, and offering --emissions/--settlement/--claims would advertise a partial
// pause the protocol no longer has.
func pauseCmd() *cobra.Command {
	return txCmd("pause", cobra.NoArgs, func(cmd *cobra.Command, _ []string) error {
		from, err := signer(cmd)
		if err != nil {
			return err
		}
		msg, err := buildPauseMsg(from)
		if err != nil {
			return err
		}
		return broadcast(cmd, msg)
	})
}

// resumeCmd schedules a global rewards resume for the next block.
func resumeCmd() *cobra.Command {
	return txCmd("resume", cobra.NoArgs, func(cmd *cobra.Command, _ []string) error {
		from, err := signer(cmd)
		if err != nil {
			return err
		}
		msg, err := buildResumeMsg(from)
		if err != nil {
			return err
		}
		return broadcast(cmd, msg)
	})
}

// claimCmd triggers a claim for a slot over an inclusive epoch range. Anyone may
// sign; funds always go to each row's snapshotted payout address. The CLI exposes
// no payout or amount override.
func claimCmd() *cobra.Command {
	return txCmd("claim [slot-id] [start-epoch] [end-epoch]", cobra.ExactArgs(3), func(cmd *cobra.Command, args []string) error {
		from, err := signer(cmd)
		if err != nil {
			return err
		}
		msg, err := buildClaimMsg(from, args[0], args[1], args[2])
		if err != nil {
			return err
		}
		return broadcast(cmd, msg)
	})
}

// --- Pure message builders (testable without a client context/network) ---

func buildUpdateParamsMsg(cdc codec.Codec, from, jsonPath string) (*types.MsgUpdateRewardsParams, error) {
	raw, err := os.ReadFile(jsonPath)
	if err != nil {
		return nil, err
	}
	var params types.Params
	if err := cdc.UnmarshalJSON(raw, &params); err != nil {
		return nil, err
	}
	return &types.MsgUpdateRewardsParams{Authority: from, Params: &params}, nil
}

// buildPauseMsg builds a global pause. The deprecated per-area selectors are
// left unset: they carry no meaning and setting them would imply partial pause
// semantics the handler ignores.
func buildPauseMsg(from string) (*types.MsgPauseRewards, error) {
	return &types.MsgPauseRewards{EmergencyAuthority: from}, nil
}

func buildResumeMsg(from string) (*types.MsgResumeRewards, error) {
	return &types.MsgResumeRewards{EmergencyAuthority: from}, nil
}
func buildClaimMsg(from, slotArg, startArg, endArg string) (*types.MsgClaimRewards, error) {
	slot, err := strconv.ParseUint(slotArg, 10, 64)
	if err != nil {
		return nil, err
	}
	start, err := strconv.ParseUint(startArg, 10, 64)
	if err != nil {
		return nil, err
	}
	end, err := strconv.ParseUint(endArg, 10, 64)
	if err != nil {
		return nil, err
	}
	return &types.MsgClaimRewards{Signer: from, SlotId: slot, StartEpoch: start, EndEpoch: end}, nil
}
