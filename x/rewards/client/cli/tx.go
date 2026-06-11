package cli

import (
	"fmt"
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

func pauseCmd() *cobra.Command {
	cmd := txCmd("pause", cobra.NoArgs, func(cmd *cobra.Command, _ []string) error {
		from, err := signer(cmd)
		if err != nil {
			return err
		}
		emissions, _ := cmd.Flags().GetBool("emissions")
		settlement, _ := cmd.Flags().GetBool("settlement")
		claims, _ := cmd.Flags().GetBool("claims")
		msg, err := buildPauseMsg(from, emissions, settlement, claims)
		if err != nil {
			return err
		}
		return broadcast(cmd, msg)
	})
	cmd.Flags().Bool("emissions", false, "pause emissions")
	cmd.Flags().Bool("settlement", false, "pause epoch settlement")
	cmd.Flags().Bool("claims", false, "pause claims")
	return cmd
}

func resumeCmd() *cobra.Command {
	cmd := txCmd("resume", cobra.NoArgs, func(cmd *cobra.Command, _ []string) error {
		from, err := signer(cmd)
		if err != nil {
			return err
		}
		emissions, _ := cmd.Flags().GetBool("emissions")
		settlement, _ := cmd.Flags().GetBool("settlement")
		claims, _ := cmd.Flags().GetBool("claims")
		msg, err := buildResumeMsg(from, emissions, settlement, claims)
		if err != nil {
			return err
		}
		return broadcast(cmd, msg)
	})
	cmd.Flags().Bool("emissions", false, "resume emissions")
	cmd.Flags().Bool("settlement", false, "resume epoch settlement")
	cmd.Flags().Bool("claims", false, "resume claims")
	return cmd
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

func buildPauseMsg(from string, emissions, settlement, claims bool) (*types.MsgPauseRewards, error) {
	if !emissions && !settlement && !claims {
		return nil, fmt.Errorf("at least one of --emissions, --settlement, --claims is required")
	}
	return &types.MsgPauseRewards{
		EmergencyAuthority:   from,
		PauseEmissions:       emissions,
		PauseEpochSettlement: settlement,
		PauseClaims:          claims,
	}, nil
}

func buildResumeMsg(from string, emissions, settlement, claims bool) (*types.MsgResumeRewards, error) {
	if !emissions && !settlement && !claims {
		return nil, fmt.Errorf("at least one of --emissions, --settlement, --claims is required")
	}
	return &types.MsgResumeRewards{
		EmergencyAuthority:    from,
		ResumeEmissions:       emissions,
		ResumeEpochSettlement: settlement,
		ResumeClaims:          claims,
	}, nil
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
