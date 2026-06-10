package cli

import (
	"encoding/json"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	clienttx "github.com/cosmos/cosmos-sdk/client/tx"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/nyks/nyks-core/x/coreslot/types"
)

func GetTxCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "coreslot", Short: "Core-slot lifecycle transactions", DisableFlagParsing: true, SuggestionsMinimumDistance: 2}
	cmd.AddCommand(
		registerCmd(), activateCmd(), inactivateCmd(), suspendCmd(), removeCmd(), rotateCmd(),
		updatePayoutCmd(), updateMetadataCmd(), updateParamsCmd(),
	)
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

func registerCmd() *cobra.Command {
	return txCmd("register [operator] [payout] [consensus-pubkey-base64] [moniker]", cobra.ExactArgs(4), func(cmd *cobra.Command, args []string) error {
		from, err := signer(cmd)
		if err != nil {
			return err
		}
		pk, err := pubKeyAny(args[2])
		if err != nil {
			return err
		}
		return broadcast(cmd, &types.MsgRegisterCoreSlot{Authority: from, OperatorAddress: args[0], PayoutAddress: args[1], ConsensusPubkey: pk, Metadata: &types.OperatorMetadata{Moniker: args[3]}})
	})
}

func activateCmd() *cobra.Command {
	return txCmd("activate [slot-id]", cobra.ExactArgs(1), func(cmd *cobra.Command, args []string) error {
		from, err := signer(cmd)
		if err != nil {
			return err
		}
		id, err := strconv.ParseUint(args[0], 10, 64)
		if err != nil {
			return err
		}
		return broadcast(cmd, &types.MsgActivateCoreSlot{Authority: from, SlotId: id})
	})
}

func inactivateCmd() *cobra.Command {
	return txCmd("inactivate [slot-id] [reason]", cobra.ExactArgs(2), func(cmd *cobra.Command, args []string) error {
		from, err := signer(cmd)
		if err != nil {
			return err
		}
		id, err := strconv.ParseUint(args[0], 10, 64)
		if err != nil {
			return err
		}
		return broadcast(cmd, &types.MsgInactivateCoreSlot{AuthorityOrOperator: from, SlotId: id, Reason: args[1]})
	})
}

func suspendCmd() *cobra.Command {
	return txCmd("suspend [slot-id] [reason] [evidence-reference]", cobra.ExactArgs(3), func(cmd *cobra.Command, args []string) error {
		from, err := signer(cmd)
		if err != nil {
			return err
		}
		id, err := strconv.ParseUint(args[0], 10, 64)
		if err != nil {
			return err
		}
		return broadcast(cmd, &types.MsgSuspendCoreSlot{Authority: from, SlotId: id, Reason: args[1], EvidenceReference: args[2]})
	})
}

func removeCmd() *cobra.Command {
	return txCmd("remove [slot-id] [reason]", cobra.ExactArgs(2), func(cmd *cobra.Command, args []string) error {
		from, err := signer(cmd)
		if err != nil {
			return err
		}
		id, err := strconv.ParseUint(args[0], 10, 64)
		if err != nil {
			return err
		}
		return broadcast(cmd, &types.MsgRemoveCoreSlot{Authority: from, SlotId: id, Reason: args[1]})
	})
}

func rotateCmd() *cobra.Command {
	return txCmd("rotate-key [slot-id] [new-consensus-pubkey-base64]", cobra.ExactArgs(2), func(cmd *cobra.Command, args []string) error {
		from, err := signer(cmd)
		if err != nil {
			return err
		}
		id, err := strconv.ParseUint(args[0], 10, 64)
		if err != nil {
			return err
		}
		pk, err := pubKeyAny(args[1])
		if err != nil {
			return err
		}
		return broadcast(cmd, &types.MsgRotateConsensusKey{Authority: from, SlotId: id, NewConsensusPubkey: pk})
	})
}

func updatePayoutCmd() *cobra.Command {
	return txCmd("update-payout [slot-id] [new-payout]", cobra.ExactArgs(2), func(cmd *cobra.Command, args []string) error {
		from, err := signer(cmd)
		if err != nil {
			return err
		}
		id, err := strconv.ParseUint(args[0], 10, 64)
		if err != nil {
			return err
		}
		return broadcast(cmd, &types.MsgUpdatePayoutAddress{Operator: from, SlotId: id, NewPayoutAddress: args[1]})
	})
}

func updateMetadataCmd() *cobra.Command {
	return txCmd("update-metadata [slot-id] [moniker]", cobra.ExactArgs(2), func(cmd *cobra.Command, args []string) error {
		from, err := signer(cmd)
		if err != nil {
			return err
		}
		id, err := strconv.ParseUint(args[0], 10, 64)
		if err != nil {
			return err
		}
		return broadcast(cmd, &types.MsgUpdateOperatorMetadata{Operator: from, SlotId: id, Metadata: &types.OperatorMetadata{Moniker: args[1]}})
	})
}

func updateParamsCmd() *cobra.Command {
	return txCmd("update-params [params-json-file]", cobra.ExactArgs(1), func(cmd *cobra.Command, args []string) error {
		from, err := signer(cmd)
		if err != nil {
			return err
		}
		bz, err := os.ReadFile(args[0])
		if err != nil {
			return err
		}
		var params types.Params
		if err := json.Unmarshal(bz, &params); err != nil {
			return err
		}
		return broadcast(cmd, &types.MsgUpdateParams{Authority: from, Params: &params})
	})
}
