import type { SidebarsConfig } from "@docusaurus/plugin-content-docs";

// Full chain documentation IA (Phases A–C).
const sidebars: SidebarsConfig = {
  docs: [
    { type: "doc", id: "intro", label: "Documentation Home" },
    {
      type: "category",
      label: "Getting Started",
      collapsed: false,
      items: [
        { type: "doc", id: "getting-started/overview", label: "Overview" },
        { type: "doc", id: "getting-started/install", label: "Install" },
        { type: "doc", id: "getting-started/quickstart", label: "Quickstart" },
        { type: "doc", id: "getting-started/chain-concepts", label: "Chain Concepts" },
        { type: "doc", id: "getting-started/glossary", label: "Glossary" },
      ],
    },
    {
      type: "category",
      label: "Chain",
      collapsed: false,
      items: [
        { type: "doc", id: "chain/architecture", label: "Architecture" },
        { type: "doc", id: "chain/consensus-and-coreslot", label: "Consensus & CoreSlot" },
        { type: "doc", id: "chain/accounts-and-denoms", label: "Accounts & Denoms" },
        { type: "doc", id: "chain/genesis", label: "Genesis" },
        { type: "doc", id: "chain/lifecycle", label: "Block Lifecycle" },
        { type: "doc", id: "chain/localnet", label: "Localnet" },
        { type: "doc", id: "chain/status-and-validation", label: "Status & Validation" },
      ],
    },
    {
      type: "category",
      label: "Rewards",
      collapsed: false,
      items: [
        { type: "doc", id: "rewards/overview", label: "Overview" },
        { type: "doc", id: "rewards/economics", label: "Economics" },
        { type: "doc", id: "rewards/epoch-lifecycle", label: "Epoch Lifecycle" },
        { type: "doc", id: "rewards/active-block-accounting", label: "Active-Block Accounting" },
        { type: "doc", id: "rewards/claims", label: "Claims" },
        { type: "doc", id: "rewards/params", label: "Parameters" },
        { type: "doc", id: "rewards/invariants", label: "Invariants" },
        { type: "doc", id: "rewards/events", label: "Events" },
        { type: "doc", id: "rewards/queries", label: "Queries" },
        { type: "doc", id: "rewards/transactions", label: "Transactions" },
        { type: "doc", id: "rewards/operator-runbook", label: "Operator Runbook" },
        { type: "doc", id: "rewards/troubleshooting", label: "Troubleshooting" },
        { type: "doc", id: "rewards/security-and-failure-modes", label: "Security & Failure Modes" },
      ],
    },
    {
      type: "category",
      label: "Operators",
      collapsed: true,
      items: [
        { type: "doc", id: "operators/node-operator-guide", label: "Node Operator Guide" },
        { type: "doc", id: "operators/coreslot-operator-guide", label: "CoreSlot Operator Guide" },
        { type: "doc", id: "operators/rewards-operator-guide", label: "Rewards Operator Guide" },
        { type: "doc", id: "operators/authority-and-emergency-guide", label: "Authority & Emergency" },
        { type: "doc", id: "operators/monitoring", label: "Monitoring" },
        { type: "doc", id: "operators/incident-response", label: "Incident Response" },
        { type: "doc", id: "operators/upgrade-and-export-import", label: "Upgrade & Export/Import" },
      ],
    },
    {
      type: "category",
      label: "Reference",
      collapsed: true,
      items: [
        { type: "doc", id: "reference/cli", label: "CLI" },
        { type: "doc", id: "reference/rewards-query-api", label: "Rewards Query API" },
        { type: "doc", id: "reference/rewards-tx-api", label: "Rewards Tx API" },
        { type: "doc", id: "reference/genesis-reference", label: "Genesis Reference" },
        { type: "doc", id: "reference/params-reference", label: "Params Reference" },
        { type: "doc", id: "reference/module-accounts", label: "Module Accounts" },
      ],
    },
    {
      type: "category",
      label: "Development",
      collapsed: true,
      items: [
        { type: "doc", id: "development/repo-map", label: "Repo Map" },
        { type: "doc", id: "development/module-map", label: "Module Map" },
        { type: "doc", id: "development/testing", label: "Testing" },
        { type: "doc", id: "development/localnet-drills", label: "Localnet Drills" },
        { type: "doc", id: "development/contributing", label: "Contributing" },
      ],
    },
  ],
};

export default sidebars;
