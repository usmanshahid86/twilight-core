// Package queryapi holds the pinned contract for the operator-facing query CLI:
// for every command on every query surface, which request it must build and which
// query RPC that request must reach.
//
// # This table is written by hand, and that is the entire point
//
// It is deliberately NOT generated from the cobra tree, from the dispatch
// switches, or from protobuf descriptors. It is a THIRD AUTHORITY, independent of
// the two things it holds against each other.
//
// Derive it from any of them and the protection collapses silently. A contract
// generated from the cobra tree agrees with the cobra tree by construction, so
// deleting a command deletes its contract entry and the test still passes — which
// is exactly the hole that let three rewards commands ship having never returned
// data (#136): they were registered, they built a request, and nothing compared
// that against a list of what was supposed to exist.
//
// So: adding a command means adding a row here, by hand. That is not friction to
// be optimized away. It is the check.
//
// # What the three columns catch
//
//	Command     vs the cobra tree      a command added or removed without intent
//	Request     vs the command's own builder   a command wired to the wrong request
//	RPC         vs recorded dispatch   a request that reaches the wrong query, or none
//
// Any one of them alone can be satisfied while the surface is broken. Together
// they cannot: deleting a registration AND its dispatch case still leaves this row
// pointing at something that no longer exists.
package queryapi

// Entry is one command on one query surface.
//
// Request and RPC are plain strings rather than typed references, so this file
// imports neither the CLI packages nor the generated types. That keeps it an
// independent statement of intent rather than a restatement of the code.
type Entry struct {
	// Module is the surface: "rewards", "mining" or "coreslot".
	Module string
	// Command is the cobra command name — the first word of its Use string.
	Command string
	// Request is the bare request type name, without the package qualifier:
	// "QueryParamsRequest", not "*types.QueryParamsRequest".
	Request string
	// RPC is the method the request must reach on the generated query client.
	RPC string
	// Args are sample positional arguments, when the command takes any. They only
	// need to satisfy the builder; their values are never sent anywhere.
	Args []string
}

// Contract is the pinned surface. All three modules live here from the start,
// including the two that are complete today: their completeness is the property
// being protected, and a contract covering only the broken surface would guard
// nothing going forward.
var Contract = []Entry{
	// ---- rewards ---------------------------------------------------------------
	//
	// The last three had no dispatch case and printed usage instead of data. They
	// are ordinary rows now; nothing distinguishes them, which is the point.
	{Module: "rewards", Command: "params", Request: "QueryParamsRequest", RPC: "Params"},
	{Module: "rewards", Command: "epoch-info", Request: "QueryEpochInfoRequest", RPC: "EpochInfo"},
	{Module: "rewards", Command: "next-halving", Request: "QueryNextHalvingRequest", RPC: "NextHalving"},
	{Module: "rewards", Command: "epoch-reward", Request: "QueryEpochRewardRequest", RPC: "EpochReward", Args: []string{"1"}},
	{Module: "rewards", Command: "cumulative-emitted", Request: "QueryCumulativeEmittedRequest", RPC: "CumulativeEmitted"},
	{Module: "rewards", Command: "supply-schedule", Request: "QuerySupplyScheduleRequest", RPC: "SupplySchedule"},
	{Module: "rewards", Command: "current-active-blocks", Request: "QueryCurrentEpochActiveBlocksRequest", RPC: "CurrentEpochActiveBlocks"},
	{Module: "rewards", Command: "module-balances", Request: "QueryModuleBalancesRequest", RPC: "ModuleBalances"},
	{Module: "rewards", Command: "entitlement", Request: "QuerySlotEntitlementRequest", RPC: "SlotEntitlement", Args: []string{"1", "1"}},
	{Module: "rewards", Command: "epoch-entitlements", Request: "QuerySlotEntitlementsByEpochRequest", RPC: "SlotEntitlementsByEpoch", Args: []string{"1"}},
	{Module: "rewards", Command: "reward-config-versions", Request: "QueryRewardConfigVersionsRequest", RPC: "RewardConfigVersions"},
	{Module: "rewards", Command: "reward-config-version", Request: "QueryRewardConfigVersionRequest", RPC: "RewardConfigVersion", Args: []string{"1"}},
	{Module: "rewards", Command: "epoch-config-versions", Request: "QueryEpochConfigVersionsRequest", RPC: "EpochConfigVersions"},
	{Module: "rewards", Command: "epoch-boundaries", Request: "QueryEpochBoundariesRequest", RPC: "EpochBoundaries", Args: []string{"1"}},
	{Module: "rewards", Command: "pause-state", Request: "QueryRewardsPauseStateRequest", RPC: "RewardsPauseState"},

	// ---- mining ----------------------------------------------------------------
	{Module: "mining", Command: "settlement", Request: "QuerySettlementRequest", RPC: "Settlement", Args: []string{"1", "1"}},
	{Module: "mining", Command: "open-settlements", Request: "QueryOpenSettlementsRequest", RPC: "OpenSettlements", Args: []string{"1"}},
	{Module: "mining", Command: "settlement-clock", Request: "QuerySettlementClockRequest", RPC: "SettlementClock"},
	{Module: "mining", Command: "distribution-mode-version", Request: "QueryDistributionModeVersionRequest", RPC: "DistributionModeVersion", Args: []string{"1"}},
	{Module: "mining", Command: "distribution-mode-versions", Request: "QueryDistributionModeVersionsRequest", RPC: "DistributionModeVersions"},
	{Module: "mining", Command: "selection-params-version", Request: "QuerySelectionParamsVersionRequest", RPC: "SelectionParamsVersion", Args: []string{"1"}},
	{Module: "mining", Command: "selection-params-versions", Request: "QuerySelectionParamsVersionsRequest", RPC: "SelectionParamsVersions"},
	{Module: "mining", Command: "settlement-params-version", Request: "QuerySettlementParamsVersionRequest", RPC: "SettlementParamsVersion", Args: []string{"1"}},
	{Module: "mining", Command: "settlement-params-versions", Request: "QuerySettlementParamsVersionsRequest", RPC: "SettlementParamsVersions"},
	{Module: "mining", Command: "target-epoch-interpretation", Request: "QueryTargetEpochInterpretationRequest", RPC: "TargetEpochInterpretation", Args: []string{"1"}},
	{Module: "mining", Command: "validate-economic-address", Request: "QueryValidateEconomicAddressRequest", RPC: "ValidateEconomicAddress", Args: []string{"twilight1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq"}},

	// ---- coreslot --------------------------------------------------------------
	{Module: "coreslot", Command: "params", Request: "QueryParamsRequest", RPC: "Params"},
	{Module: "coreslot", Command: "slot", Request: "QueryCoreSlotRequest", RPC: "CoreSlot", Args: []string{"1"}},
	{Module: "coreslot", Command: "slots", Request: "QueryCoreSlotsRequest", RPC: "CoreSlots"},
	{Module: "coreslot", Command: "active", Request: "QueryActiveCoreSlotsRequest", RPC: "ActiveCoreSlots"},
	{Module: "coreslot", Command: "by-operator", Request: "QueryCoreSlotByOperatorRequest", RPC: "CoreSlotByOperator", Args: []string{"twilight1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq"}},
	{Module: "coreslot", Command: "by-consensus", Request: "QueryCoreSlotByConsensusAddressRequest", RPC: "CoreSlotByConsensusAddress", Args: []string{"0011223344556677889900112233445566778899"}},
	{Module: "coreslot", Command: "pending-rotations", Request: "QueryPendingKeyRotationsRequest", RPC: "PendingKeyRotations"},
	{Module: "coreslot", Command: "last-applied", Request: "QueryLastAppliedValidatorsRequest", RPC: "LastAppliedValidators"},
	{Module: "coreslot", Command: "reserved", Request: "QueryReservedConsensusAddressRequest", RPC: "ReservedConsensusAddress", Args: []string{"0011223344556677889900112233445566778899"}},
	{Module: "coreslot", Command: "reward-weight", Request: "QueryRewardWeightRequest", RPC: "RewardWeight", Args: []string{"1"}},
	{Module: "coreslot", Command: "selection-policy", Request: "QuerySelectionPolicyRequest", RPC: "SelectionPolicy", Args: []string{"1"}},
	{Module: "coreslot", Command: "selection-policy-version", Request: "QuerySelectionPolicyVersionRequest", RPC: "SelectionPolicyVersion", Args: []string{"1", "1"}},
	{Module: "coreslot", Command: "selection-policy-at-height", Request: "QuerySelectionPolicyAtHeightRequest", RPC: "SelectionPolicyAtHeight", Args: []string{"1", "1"}},
}

// ForModule returns the pinned entries for one surface.
func ForModule(module string) []Entry {
	var out []Entry
	for _, e := range Contract {
		if e.Module == module {
			out = append(out, e)
		}
	}
	return out
}
