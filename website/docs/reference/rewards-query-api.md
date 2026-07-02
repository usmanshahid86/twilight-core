---
title: Rewards Query API
---

# Rewards Query API

The **authoritative, always-current reference** is auto-generated from the chain's
protobuf definitions — this page is a map, not a hand-maintained schema:

- **Swagger / OpenAPI UI** — served by any node's REST endpoint at `/swagger/`
  (the REST / gRPC-gateway for `twilight.rewards.v1.Query`).
- **Proto** — `proto/twilight/rewards/v1/query.proto`.
- **CLI** — `twilightd rewards-query --help`.

For **narrative usage** (what to call and why), see [Queries](../rewards/queries.md).

## Endpoints

`twilightd rewards-query` — all read-only:

| Command | REST path | Returns |
|---|---|---|
| `params` | `/twilight/rewards/v1/params` | current parameters |
| `epoch-info` | `/twilight/rewards/v1/epoch-info` | open-epoch state |
| `epoch-reward [epoch]` | `/twilight/rewards/v1/epochs/{epoch_number}` | finalized epoch aggregate (`NotFound` if not finalized) |
| `slot-rewards [slot-id]` | `/twilight/rewards/v1/slots/{slot_id}/rewards` | claim records — **paginated** |
| `claimable [slot-id] [start] [end]` | `/twilight/rewards/v1/slots/{slot_id}/claimable` | unclaimed positive rewards in range |
| `cumulative-emitted` | `/twilight/rewards/v1/cumulative-emitted` | cumulative emitted + max supply |
| `next-halving` / `supply-schedule` | `/twilight/rewards/v1/{next-halving,supply-schedule}` | halving / subsidy view |
| `current-active-blocks` | `/twilight/rewards/v1/current-epoch/active-blocks` | open-epoch active-block counters — **paginated** |
| `module-balances` | `/twilight/rewards/v1/module-balances` | module balances (`utwlt`) |

Full request/response field types are in the Swagger UI / proto. Paginated
endpoints take the standard `--limit` / `--offset` / `--page` / `--page-key` /
`--count-total` / `--reverse` flags; use a response's `next_key` as the next
`--page-key`.

:::note
`reward_weight` on a claim record is the CoreSlot snapshot and is **metadata-only —
no payout effect in v1**; the payout (`amount`) is by active-block participation.
See [Rewards Economics](../rewards/economics.mdx).
:::
