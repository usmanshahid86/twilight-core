package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"
	clienttx "github.com/cosmos/cosmos-sdk/client/tx"
	sdk "github.com/cosmos/cosmos-sdk/types"

	"github.com/twilight-project/twilight-core/x/coreslot/types"
)

func GetTxCmd() *cobra.Command {
	// RunE is client.ValidateCmd for the same reason the `tx` and `query` parents
	// use it: cobra only reports an unknown subcommand for the ROOT command. A
	// non-root parent that carries subcommands but is not itself runnable falls
	// through to printing help and EXITING ZERO.
	//
	// That matters here because this tree replaced an AutoCLI-generated one whose
	// names it does not share. Without this, `tx coreslot register-core-slot …` —
	// the name an existing script would carry — would print help, exit 0, and
	// register nobody. A script guarded by `&& echo ok` would report success. The
	// generated names used to fail loudly; they must keep failing loudly.
	cmd := &cobra.Command{
		Use:                        "coreslot",
		Short:                      "Core-slot lifecycle transactions",
		DisableFlagParsing:         true,
		SuggestionsMinimumDistance: 2,
		RunE:                       client.ValidateCmd,
	}
	cmd.AddCommand(
		registerCmd(), activateCmd(), inactivateCmd(), suspendCmd(), removeCmd(), rotateCmd(),
		updatePayoutCmd(), updateMetadataCmd(), updateSettlementCmd(), updateSelectionPolicyCmd(), updateParamsCmd(),
		scheduleUpgradeCmd(), cancelUpgradeCmd(),
		nominateAuthorityCmd(), acceptAuthorityCmd(), cancelAuthorityNominationCmd(),
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
	cmd := txCmd("register [operator] [payout] [settlement] [consensus-pubkey-base64] [moniker]", cobra.ExactArgs(5), func(cmd *cobra.Command, args []string) error {
		from, err := signer(cmd)
		if err != nil {
			return err
		}
		pk, err := txPubKeyAny(args[3])
		if err != nil {
			return err
		}
		rateBps, err := cmd.Flags().GetUint64("selection-rate-bps")
		if err != nil {
			return err
		}
		maxSelected, err := cmd.Flags().GetUint64("max-selected-participants")
		if err != nil {
			return err
		}
		return broadcast(cmd, &types.MsgRegisterCoreSlot{
			Authority: from, OperatorAddress: args[0], PayoutAddress: args[1], SettlementAddress: args[2],
			ConsensusPubkey: pk, Metadata: &types.OperatorMetadata{Moniker: args[4]},
			InitialSelectionPolicy: &types.InitialSelectionPolicy{
				SelectionRateBps: rateBps, MaxSelectedParticipants: maxSelected,
			},
		})
	})
	// Operator configuration, not protocol constants: convenience defaults with
	// no protocol standing, constrained only by the local §27 rule.
	cmd.Flags().Uint64("selection-rate-bps", 2_500, "initial selection rate in basis points")
	cmd.Flags().Uint64("max-selected-participants", 10, "initial per-slot maximum selected participants")
	return cmd
}

func updateSelectionPolicyCmd() *cobra.Command {
	return txCmd("update-selection-policy [slot-id] [selection-rate-bps] [max-selected-participants]", cobra.ExactArgs(3), func(cmd *cobra.Command, args []string) error {
		from, err := signer(cmd)
		if err != nil {
			return err
		}
		id, err := strconv.ParseUint(args[0], 10, 64)
		if err != nil {
			return err
		}
		rateBps, err := strconv.ParseUint(args[1], 10, 64)
		if err != nil {
			return err
		}
		maxSelected, err := strconv.ParseUint(args[2], 10, 64)
		if err != nil {
			return err
		}
		return broadcast(cmd, &types.MsgUpdateSelectionPolicy{
			Operator: from, SlotId: id, SelectionRateBps: rateBps, MaxSelectedParticipants: maxSelected,
		})
	})
}

func updateSettlementCmd() *cobra.Command {
	return txCmd("update-settlement [slot-id] [settlement-address]", cobra.ExactArgs(2), func(cmd *cobra.Command, args []string) error {
		from, err := signer(cmd)
		if err != nil {
			return err
		}
		id, err := strconv.ParseUint(args[0], 10, 64)
		if err != nil {
			return err
		}
		return broadcast(cmd, &types.MsgUpdateSettlementAddress{Operator: from, SlotId: id, SettlementAddress: args[1]})
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
		pk, err := txPubKeyAny(args[1])
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

// scheduleUpgradeCmd schedules a coordinated halt.
//
// It lives under `coreslot` rather than a top-level `upgrade` command because that
// is where the authority is. The x/upgrade module's own messages are unreachable
// on this chain by design — its authority is a module address with no private key —
// so this is the only route to a plan. See ADR-0003.
func scheduleUpgradeCmd() *cobra.Command {
	return txCmd("schedule-upgrade [name] [height] [info]", cobra.RangeArgs(2, 3), func(cmd *cobra.Command, args []string) error {
		from, err := signer(cmd)
		if err != nil {
			return err
		}
		height, err := strconv.ParseInt(args[1], 10, 64)
		if err != nil {
			return fmt.Errorf("height must be an integer: %w", err)
		}
		// The chain refuses a non-future height on its own authority; this refuses
		// an obviously impossible one so a typo costs no round trip.
		if height <= 0 {
			return fmt.Errorf("height must be positive")
		}
		info := ""
		if len(args) == 3 {
			info = args[2]
		}
		return broadcast(cmd, &types.MsgScheduleUpgrade{
			Authority: from, Name: args[0], Height: height, Info: info,
		})
	})
}

func cancelUpgradeCmd() *cobra.Command {
	return txCmd("cancel-upgrade", cobra.NoArgs, func(cmd *cobra.Command, _ []string) error {
		from, err := signer(cmd)
		if err != nil {
			return err
		}
		return broadcast(cmd, &types.MsgCancelUpgrade{Authority: from})
	})
}

// parseAuthorityRole accepts the two operational roles by short name.
//
// The unspecified zero value is unreachable from the CLI by construction: an
// unrecognized word is an error rather than a default, so a mistyped role cannot
// silently become a nomination for the primary authority — the more
// consequential of the two.
func parseAuthorityRole(value string) (types.AuthorityRole, error) {
	switch value {
	case "primary":
		return types.AuthorityRole_AUTHORITY_ROLE_PRIMARY, nil
	case "emergency":
		return types.AuthorityRole_AUTHORITY_ROLE_EMERGENCY, nil
	default:
		return types.AuthorityRole_AUTHORITY_ROLE_UNSPECIFIED,
			fmt.Errorf("role must be %q or %q, got %q", "primary", "emergency", value)
	}
}

func nominateAuthorityCmd() *cobra.Command {
	return txCmd("nominate-authority [primary|emergency] [nominee]", cobra.ExactArgs(2),
		func(cmd *cobra.Command, args []string) error {
			from, err := signer(cmd)
			if err != nil {
				return err
			}
			role, err := parseAuthorityRole(args[0])
			if err != nil {
				return err
			}
			return broadcast(cmd, &types.MsgNominateAuthority{Authority: from, Role: role, Nominee: args[1]})
		})
}

func acceptAuthorityCmd() *cobra.Command {
	return txCmd("accept-authority [primary|emergency]", cobra.ExactArgs(1),
		func(cmd *cobra.Command, args []string) error {
			// Signed by the nominee, not the incumbent — this signature IS the proof
			// that the destination key is controlled.
			from, err := signer(cmd)
			if err != nil {
				return err
			}
			role, err := parseAuthorityRole(args[0])
			if err != nil {
				return err
			}
			return broadcast(cmd, &types.MsgAcceptAuthority{Nominee: from, Role: role})
		})
}

func cancelAuthorityNominationCmd() *cobra.Command {
	return txCmd("cancel-authority-nomination [primary|emergency]", cobra.ExactArgs(1),
		func(cmd *cobra.Command, args []string) error {
			from, err := signer(cmd)
			if err != nil {
				return err
			}
			role, err := parseAuthorityRole(args[0])
			if err != nil {
				return err
			}
			return broadcast(cmd, &types.MsgCancelAuthorityNomination{Authority: from, Role: role})
		})
}
