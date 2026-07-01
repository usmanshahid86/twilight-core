import type { SidebarsConfig } from "@docusaurus/plugin-content-docs";

const sidebar: SidebarsConfig = {
  apisidebar: [
    {
      type: "doc",
      id: "api/twilight-chain-api",
    },
    {
      type: "category",
      label: "Query",
      link: {
        type: "doc",
        id: "api/query",
      },
      items: [
        {
          type: "doc",
          id: "api/query-active-core-slots",
          label: "Query_ActiveCoreSlots",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api/query-core-slot-by-consensus-address",
          label: "Query_CoreSlotByConsensusAddress",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api/query-last-applied-validators",
          label: "Query_LastAppliedValidators",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api/query-core-slot-by-operator",
          label: "Query_CoreSlotByOperator",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api/query-params",
          label: "Query_Params",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api/query-pending-key-rotations",
          label: "Query_PendingKeyRotations",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api/query-reserved-consensus-address",
          label: "Query_ReservedConsensusAddress",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api/query-core-slots",
          label: "Query_CoreSlots",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api/query-core-slot",
          label: "Query_CoreSlot",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api/query-reward-weight",
          label: "Query_RewardWeight",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api/query-cumulative-emitted",
          label: "Query_CumulativeEmitted",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api/query-current-epoch-active-blocks",
          label: "Query_CurrentEpochActiveBlocks",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api/query-epoch-info",
          label: "Query_EpochInfo",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api/query-epoch-reward",
          label: "Query_EpochReward",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api/query-module-balances",
          label: "Query_ModuleBalances",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api/query-next-halving",
          label: "Query_NextHalving",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api/query-params",
          label: "Query_Params",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api/query-claimable-rewards",
          label: "Query_ClaimableRewards",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api/query-slot-rewards",
          label: "Query_SlotRewards",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api/query-supply-schedule",
          label: "Query_SupplySchedule",
          className: "api-method get",
        },
      ],
    },
  ],
};

export default sidebar.apisidebar;
