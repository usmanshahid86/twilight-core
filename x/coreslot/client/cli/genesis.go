package cli

import (
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
	"github.com/twilight-project/twilight-core/x/coreslot/keeper"
	"github.com/twilight-project/twilight-core/x/coreslot/types"
)

// The chain's first block height. These commands write it into the CometBFT
// genesis document, and the slot rows they build are normalized against the same
// value, so the two cannot drift apart. Both spellings exist because the document
// carries the height as a JSON string.
const (
	genesisInitialHeight     = int64(1)
	genesisInitialHeightJSON = `"1"`
)

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
			doc["initial_height"] = json.RawMessage(genesisInitialHeightJSON)
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
			id := genesis.NextSlotId
			if id == 0 {
				id = 1
			}
			power, _ := cmd.Flags().GetInt64("power")
			rateBps, _ := cmd.Flags().GetUint64("selection-rate-bps")
			maxSelected, _ := cmd.Flags().GetUint64("max-selected-participants")
			// This tool writes a fresh V2 genesis, whose only admissible statuses are
			// PENDING and ACTIVE, and it always writes an ACTIVE slot. The §80
			// normalization for a genesis ACTIVE slot is the first activation
			// generation effective from the initial height, which this command pins
			// to 1 below. Fresh genesis is the explicit exception to the runtime H+1
			// rule, so both heights are the initial height.
			slot := &types.CoreSlot{
				SlotId: id, OperatorAddress: args[0], PayoutAddress: args[1], SettlementAddress: args[2],
				ConsensusPubkey: pk, Status: types.SlotStatus_SLOT_STATUS_ACTIVE, ConsensusPower: power,
				RewardWeight: types.DefaultRewardWeight, Metadata: &types.OperatorMetadata{Moniker: args[4]},
				ActivationSequence: 1, ActivatedHeight: genesisInitialHeight, ActivationEffectiveHeight: genesisInitialHeight,
				CurrentSelectionPolicyVersion: 1, LastSelectionPolicyUpdateHeight: 0,
			}
			genesis.Slots = append(genesis.Slots, slot)
			genesis.SelectionPolicies = append(genesis.SelectionPolicies, &types.SelectionPolicyVersion{
				SlotId: id, PolicyVersion: 1, SelectionRateBps: rateBps, MaxSelectedParticipants: maxSelected,
				ValidFromHeight: genesisInitialHeight, ValidUntilHeightExclusive: 0,
			})
			genesis.RewardWeights = append(genesis.RewardWeights, &types.OperatorRewardWeight{SlotId: id, BaseWeight: types.DefaultRewardWeight, UptimeWeight: types.DefaultRewardWeight, PerformanceWeight: types.DefaultRewardWeight, FinalWeight: types.DefaultRewardWeight})
			genesis.NextSlotId = id + 1
			state[types.ModuleName] = clientCtx.Codec.MustMarshalJSON(&genesis)
			ensureBankMetadata(state)
			appState, err := json.Marshal(state)
			if err != nil {
				return err
			}
			doc["app_state"] = appState
			doc["initial_height"] = json.RawMessage(genesisInitialHeightJSON)
			var validators []genesisValidator
			_ = json.Unmarshal(doc["validators"], &validators)
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
			doc["initial_height"] = json.RawMessage(genesisInitialHeightJSON)
			if err := saveGenesis(path, doc); err != nil {
				return err
			}
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
