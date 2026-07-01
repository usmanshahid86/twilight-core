---
title: Node Operator Guide
---

# Node Operator Guide

Practical steps to build, initialize, and run a `twilightd` node. For multi-node
localnets see [Localnet](../chain/localnet.md).

## Build

```bash
make build           # ./build/twilightd
```

## Initialize

```bash
twilightd init <moniker> --chain-id <chain-id>
```

The generated genesis includes the rewards default genesis (rewards is registered
in the CLI basic manager). Inspect with:

```bash
jq '.app_state.rewards' ~/.twilightd/config/genesis.json
jq '.app_state.coreslot' ~/.twilightd/config/genesis.json
```

## Start

```bash
twilightd start
```

For a four-node localnet, use the scripts:

```bash
scripts/localnet/init.sh
scripts/localnet/start.sh
scripts/localnet/stop.sh
```

## Check status and agreement

```bash
curl -s http://<rpc-host>:26657/status | jq '.result.sync_info.latest_block_height'
# Cross-node hash agreement (localnet):
scripts/localnet/agree.sh
```

`agree.sh` confirms all nodes agree on **app hash**, **validators hash**, and
**next-validators hash** at a common height. App-hash divergence is a state fork —
treat as critical (see [Incident Response](incident-response.md)).

## Logs

Localnet logs are written under the network home (e.g.
`/tmp/twilight-rewards-localnet/logs`). Watch for repeated EndBlock errors — the
rewards module is fail-closed and will halt the block on a finalization fault (see
[Security & Failure Modes](../rewards/security-and-failure-modes.md)).

## Common failures

| Symptom | Check |
|---|---|
| Node won't start | genesis validity (`twilightd validate-genesis` if available); port conflicts |
| Halts at a height with EndBlock error | rewards finalization fault — inspect logs; this is fail-closed by design |
| App-hash divergence vs peers | **critical** — stop and investigate a possible fork |
| Empty validator set at InitChain | genesis must include at least one active CoreSlot |
