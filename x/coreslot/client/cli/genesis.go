package cli

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/client/flags"

	"github.com/twilight-project/twilight-core/app/params"
	"github.com/twilight-project/twilight-core/internal/checked"
	"github.com/twilight-project/twilight-core/x/coreslot/keeper"
	"github.com/twilight-project/twilight-core/x/coreslot/types"
)

// effectiveInitialHeight reads the chain's first block height from the genesis
// document and applies the SDK's convention for it.
//
// The document is the authority. These commands author slot rows that must be
// normalized against the height the chain will actually start at, so they read
// that height rather than declaring one — a command that wrote its own would be a
// second authority, and would silently corrupt a genesis whose initial height is
// not 1.
//
// The convention matches baseapp's: an absent or zero initial_height means the
// chain starts at height 1, and any other value is used exactly. A negative or
// unrepresentable height is refused rather than normalized, because a nonsensical
// document must not quietly define consensus state.
//
// CometBFT writes the field as a JSON string; some tooling writes a bare number.
// Both are accepted, and neither is rewritten.
func effectiveInitialHeight(doc map[string]json.RawMessage) (int64, error) {
	raw, present := doc["initial_height"]
	if !present {
		return types.EffectiveInitialHeight(0)
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return 0, fmt.Errorf("genesis initial_height is empty")
	}

	text := string(raw)
	if raw[0] == '"' {
		if err := json.Unmarshal(raw, &text); err != nil {
			return 0, fmt.Errorf("genesis initial_height %s is not a valid height: %w", string(raw), err)
		}
	}
	height, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("genesis initial_height %s is not a valid height: %w", string(raw), err)
	}
	effective, err := types.EffectiveInitialHeight(height)
	if err != nil {
		return 0, fmt.Errorf("genesis initial_height: %w", err)
	}
	return effective, nil
}

func GetGenesisCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "coreslot-genesis", Short: "Manage core slots in genesis"}
	cmd.AddCommand(addGenesisSlotCmd(), setAuthoritiesCmd(), validateGenesisCmd())
	return cmd
}

func setAuthoritiesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "set-authorities [authority] [emergency-authority]", Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx := client.GetClientContextFromCmd(cmd)
			path := filepath.Join(clientCtx.HomeDir, "config", "genesis.json")
			doc, state, err := loadGenesis(path)
			if err != nil {
				return err
			}
			var genesis types.GenesisState
			if err := clientCtx.Codec.UnmarshalJSON(state[types.ModuleName], &genesis); err != nil {
				return err
			}
			genesis.Params.Authority, genesis.Params.EmergencyAuthority = args[0], args[1]
			state[types.ModuleName] = clientCtx.Codec.MustMarshalJSON(&genesis)
			doc["app_state"], err = json.Marshal(state)
			if err != nil {
				return err
			}
			// initial_height is deliberately untouched: changing the authorities says
			// nothing about when the chain starts.
			return saveGenesis(path, doc)
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

func addGenesisSlotCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:  "add [operator] [payout] [settlement] [consensus-pubkey-base64] [moniker]",
		Args: cobra.ExactArgs(5),
		RunE: func(cmd *cobra.Command, args []string) error {
			clientCtx := client.GetClientContextFromCmd(cmd)
			path := filepath.Join(clientCtx.HomeDir, "config", "genesis.json")
			doc, state, err := loadGenesis(path)
			if err != nil {
				return err
			}
			var genesis types.GenesisState
			if raw, ok := state[types.ModuleName]; ok {
				if err := clientCtx.Codec.UnmarshalJSON(raw, &genesis); err != nil {
					return err
				}
			} else {
				return fmt.Errorf("coreslot genesis missing")
			}
			pk, err := pubKeyAny(args[3])
			if err != nil {
				return err
			}
			// The document owns the chain's first block height; this command reads it
			// and never rewrites it.
			initialHeight, err := effectiveInitialHeight(doc)
			if err != nil {
				return err
			}
			id := genesis.NextSlotId
			if id == 0 {
				id = 1
			}
			// Checked, and computed BEFORE any of the structures that will be saved
			// are touched: at the top of the range an unchecked increment would wrap
			// to zero and hand the next slot an identifier already in use. Failing
			// here leaves the file on disk untouched.
			nextID, err := checked.AddUint64(id, 1)
			if err != nil {
				return fmt.Errorf("slot id %d leaves no room for the next slot id: %w", id, err)
			}
			power, _ := cmd.Flags().GetInt64("power")
			rateBps, _ := cmd.Flags().GetUint64("selection-rate-bps")
			maxSelected, _ := cmd.Flags().GetUint64("max-selected-participants")
			// This tool writes a fresh V2 genesis, whose only admissible statuses are
			// PENDING and ACTIVE, and it always writes an ACTIVE slot. The §80
			// normalization for a genesis ACTIVE slot is the first activation
			// generation, effective from the initial height. Fresh genesis is the
			// explicit exception to the runtime H+1 rule, so both heights are that
			// same initial height.
			slot := &types.CoreSlot{
				SlotId: id, OperatorAddress: args[0], PayoutAddress: args[1], SettlementAddress: args[2],
				ConsensusPubkey: pk, Status: types.SlotStatus_SLOT_STATUS_ACTIVE, ConsensusPower: power,
				RewardWeight: types.DefaultRewardWeight, Metadata: &types.OperatorMetadata{Moniker: args[4]},
				ActivationSequence: 1, ActivatedHeight: initialHeight, ActivationEffectiveHeight: initialHeight,
				CurrentSelectionPolicyVersion: 1, LastSelectionPolicyUpdateHeight: 0,
			}
			genesis.Slots = append(genesis.Slots, slot)
			genesis.SelectionPolicies = append(genesis.SelectionPolicies, &types.SelectionPolicyVersion{
				SlotId: id, PolicyVersion: 1, SelectionRateBps: rateBps, MaxSelectedParticipants: maxSelected,
				ValidFromHeight: initialHeight, ValidUntilHeightExclusive: 0,
			})
			genesis.RewardWeights = append(genesis.RewardWeights, &types.OperatorRewardWeight{SlotId: id, BaseWeight: types.DefaultRewardWeight, UptimeWeight: types.DefaultRewardWeight, PerformanceWeight: types.DefaultRewardWeight, FinalWeight: types.DefaultRewardWeight})
			genesis.NextSlotId = nextID
			state[types.ModuleName] = clientCtx.Codec.MustMarshalJSON(&genesis)
			ensureBankMetadata(state)
			appState, err := json.Marshal(state)
			if err != nil {
				return err
			}
			doc["app_state"] = appState
			// initial_height is read above, not written: the document decides when the
			// chain starts, and adding a slot is not that decision.
			// Decode what is already there before appending to it. Discarding this
			// error silently drops every validator added by an earlier `add`: a
			// malformed value leaves the slice nil, the append writes a
			// single-entry array, and saveGenesis persists a genesis missing the
			// operators it was built from. A genesis is assembled once, by hand,
			// incrementally — exactly the shape of workflow where quiet data loss
			// survives to launch.
			//
			// An ABSENT key stays tolerated: the first add legitimately finds no
			// validators array, and only a value that is present and undecodable
			// is refused.
			var validators []genesisValidator
			if raw := doc["validators"]; len(raw) > 0 && string(raw) != "null" {
				if err := json.Unmarshal(raw, &validators); err != nil {
					return fmt.Errorf("genesis validators are present but unreadable, "+
						"so adding a slot would discard them: %w", err)
				}
			}
			// args[3] is the consensus pubkey and args[4] the moniker: the settlement
			// address was inserted at args[2], which shifted both.
			validators = append(validators, genesisValidator{PubKey: genesisPubKey{Type: "tendermint/PubKeyEd25519", Value: args[3]}, Power: strconv.FormatInt(power, 10), Name: args[4]})
			doc["validators"], err = json.Marshal(validators)
			if err != nil {
				return err
			}
			return saveGenesis(path, doc)
		},
	}
	cmd.Flags().Int64("power", types.DefaultSlotVotingPower, "consensus voting power")
	// Per-slot Selection policy. These are operator configuration, not protocol
	// constants: the values below are convenience defaults for local networks and
	// carry no protocol standing. Only the local §27 rule constrains them here —
	// a positive rate at most 5000 bps, and a positive participant maximum.
	cmd.Flags().Uint64("selection-rate-bps", 2_500, "initial selection rate in basis points")
	cmd.Flags().Uint64("max-selected-participants", 10, "initial per-slot maximum selected participants")
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

func validateGenesisCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "validate", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			clientCtx := client.GetClientContextFromCmd(cmd)
			path := filepath.Join(clientCtx.HomeDir, "config", "genesis.json")
			doc, state, err := loadGenesis(path)
			if err != nil {
				return err
			}
			var genesis types.GenesisState
			if err := clientCtx.Codec.UnmarshalJSON(state[types.ModuleName], &genesis); err != nil {
				return err
			}
			if err := genesis.Validate(); err != nil {
				return err
			}
			initialHeight, err := effectiveInitialHeight(doc)
			if err != nil {
				return err
			}
			if err := types.ValidateFreshGenesisInitialHeight(&genesis, initialHeight); err != nil {
				return err
			}
			var validators []genesisValidator
			if err := json.Unmarshal(doc["validators"], &validators); err != nil {
				return err
			}
			expected := map[string]string{}
			for _, slot := range genesis.Slots {
				if slot.Status != types.SlotStatus_SLOT_STATUS_ACTIVE {
					continue
				}
				pk, err := keeper.DecodePubKey(slot.ConsensusPubkey)
				if err != nil {
					return err
				}
				expected[base64.StdEncoding.EncodeToString(pk.Bytes())] = strconv.FormatInt(slot.ConsensusPower, 10)
			}
			if len(expected) != len(validators) {
				return fmt.Errorf("CometBFT validator count %d does not match active core slots %d", len(validators), len(expected))
			}
			for _, validator := range validators {
				if expected[validator.PubKey.Value] != validator.Power {
					return fmt.Errorf("CometBFT validator %s does not match coreslot genesis", validator.Name)
				}
			}
			// Reading the height is part of validating it; writing it back is not.
			// This command reports whether the file is valid and must not be capable
			// of changing the answer by editing the file it just judged.
			_, err = fmt.Fprintln(cmd.OutOrStdout(), "coreslot genesis valid; active slots:", strconv.Itoa(len(genesis.Slots)))
			return err
		},
	}
	flags.AddQueryFlagsToCmd(cmd)
	return cmd
}

type genesisPubKey struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type genesisValidator struct {
	PubKey genesisPubKey `json:"pub_key"`
	Power  string        `json:"power"`
	Name   string        `json:"name"`
}

func ensureBankMetadata(state map[string]json.RawMessage) {
	var bank map[string]interface{}
	if err := json.Unmarshal(state["bank"], &bank); err != nil {
		return
	}
	bank["denom_metadata"] = []map[string]interface{}{{
		"description": "Native " + params.NativeName + " token",
		"denom_units": []map[string]interface{}{
			{"denom": params.NativeBaseDenom, "exponent": 0},
			{"denom": params.NativeDisplayDenom, "exponent": params.NativeExponent},
		},
		"base":    params.NativeBaseDenom,
		"display": params.NativeDisplayDenom,
		"name":    params.NativeName,
		"symbol":  params.NativeSymbol,
	}}
	bz, err := json.Marshal(bank)
	if err == nil {
		state["bank"] = bz
	}
}

func loadGenesis(path string) (map[string]json.RawMessage, map[string]json.RawMessage, error) {
	bz, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(bz, &doc); err != nil {
		return nil, nil, err
	}
	var state map[string]json.RawMessage
	if err := json.Unmarshal(doc["app_state"], &state); err != nil {
		return nil, nil, err
	}
	return doc, state, nil
}

func saveGenesis(path string, doc map[string]json.RawMessage) error {
	bz, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, bz, 0o600)
}
