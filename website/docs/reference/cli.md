---
title: CLI Reference
---

# CLI Reference

`twilightd` follows this repository's **top-level** command convention for module
CLIs (not the `query <module>` / `tx <module>` grouping):

| Command group | Purpose |
|---|---|
| `twilightd rewards-query <cmd>` | Read-only rewards queries → [Rewards Query API](rewards-query-api.md) |
| `twilightd rewards <cmd>` | Rewards transactions → [Rewards Tx API](rewards-tx-api.md) |
| `twilightd coreslot-query <cmd>` | Read-only CoreSlot queries |
| `twilightd coreslot <cmd>` | CoreSlot lifecycle transactions |
| `twilightd init`, `start`, `export`, `keys`, … | Standard Cosmos node/CLI commands |

Raw `--help` output for every rewards command is captured under
`website/generated/cli/` as audit artifacts and is the source for the reference
tables. Regenerate from a built binary with the per-command `--help`.

## Common query flags

| Flag | Meaning |
|---|---|
| `--node <rpc>` | Target node RPC (e.g. `tcp://127.0.0.1:26657`) |
| `--output json` | JSON output (recommended for scripts) |
| `--height <h>` | Query at a historical height (if retained) |

## Common transaction flags

| Flag | Meaning |
|---|---|
| `--from <key/addr>` | Signing account |
| `--chain-id <id>` | Network chain id |
| `--node <rpc>` | Broadcast target |
| `--gas` | Gas limit (a number, or `auto`) |
| `--fees <amount>` | Fees (the chain operates fee-free in localnet: `--fees 0utwlt`) |
| `--broadcast-mode` | Broadcast mode (`sync` or `async`) |
| `-y, --yes` | Skip confirmation |

## No REST gateway

A REST / gRPC-gateway surface is **not** wired in the current version (consistent
with CoreSlot). Use the gRPC/CLI queries above.
