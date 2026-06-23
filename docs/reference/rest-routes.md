# Twilight Custom-Module REST Routes

REST gRPC-gateway surface for the custom modules, served on the API server (default
`:1317`) alongside gRPC (`:9090`). These wrap the same protobuf `Query` services that
gRPC exposes; gRPC remains canonical, REST is the browser/wallet/operator wrapper.

- Enable with `app.toml` `[api] enable = true` (and `[grpc] enable = true`, default).
- All routes are **read-only** GETs. No write/admin routes exist (tx signing/broadcast
  stays the normal Cosmos tx flow).
- Status legend: `200` success · `400` invalid/missing required param · `404` valid
  request, no such record · `501` would mean the route is **not** wired (must never
  happen for the routes below).

Base URL in examples: `REST=http://localhost:1317`.

## x/rewards — `twilight.rewards.v1.Query`

| gRPC method | REST path | Request params | Response type | Example curl | Expected |
|---|---|---|---|---|---|
| `Params` | `/twilight/rewards/v1/params` | — | `QueryParamsResponse` | `curl $REST/twilight/rewards/v1/params` | 200 |
| `EpochInfo` | `/twilight/rewards/v1/epoch-info` | — | `QueryEpochInfoResponse` | `curl $REST/twilight/rewards/v1/epoch-info` | 200 |
| `NextHalving` | `/twilight/rewards/v1/next-halving` | — | `QueryNextHalvingResponse` | `curl $REST/twilight/rewards/v1/next-halving` | 200 |
| `EpochReward` | `/twilight/rewards/v1/epochs/{epoch_number}` | `epoch_number` (path, uint64) | `QueryEpochRewardResponse` | `curl $REST/twilight/rewards/v1/epochs/5` | 200; 404 if epoch not finalized |
| `SlotRewards` | `/twilight/rewards/v1/slots/{slot_id}/rewards` | `slot_id` (path, uint64); `pagination.*` (query) | `QuerySlotRewardsResponse` | `curl $REST/twilight/rewards/v1/slots/1/rewards` | 200 |
| `ClaimableRewards` | `/twilight/rewards/v1/slots/{slot_id}/claimable` | `slot_id` (path); `start_epoch`,`end_epoch` (query, **required**, uint64) | `QueryClaimableRewardsResponse` | `curl "$REST/twilight/rewards/v1/slots/1/claimable?start_epoch=1&end_epoch=10"` | 200; 400 if range missing/invalid |
| `CumulativeEmitted` | `/twilight/rewards/v1/cumulative-emitted` | — | `QueryCumulativeEmittedResponse` | `curl $REST/twilight/rewards/v1/cumulative-emitted` | 200 |
| `SupplySchedule` | `/twilight/rewards/v1/supply-schedule` | — | `QuerySupplyScheduleResponse` | `curl $REST/twilight/rewards/v1/supply-schedule` | 200 |
| `CurrentEpochActiveBlocks` | `/twilight/rewards/v1/current-epoch/active-blocks` | `pagination.*` (query) | `QueryCurrentEpochActiveBlocksResponse` | `curl $REST/twilight/rewards/v1/current-epoch/active-blocks` | 200 |
| `ModuleBalances` | `/twilight/rewards/v1/module-balances` | — | `QueryModuleBalancesResponse` | `curl $REST/twilight/rewards/v1/module-balances` | 200 |

## x/coreslot — `twilight.coreslot.v1.Query`

| gRPC method | REST path | Request params | Response type | Example curl | Expected |
|---|---|---|---|---|---|
| `Params` | `/twilight/coreslot/v1/params` | — | `QueryParamsResponse` | `curl $REST/twilight/coreslot/v1/params` | 200 |
| `CoreSlot` | `/twilight/coreslot/v1/slots/{slot_id}` | `slot_id` (path, uint64) | `QueryCoreSlotResponse` | `curl $REST/twilight/coreslot/v1/slots/1` | 200; 400 if non-numeric |
| `CoreSlots` | `/twilight/coreslot/v1/slots` | `status` (query enum); `pagination.*` (query) | `QueryCoreSlotsResponse` | `curl $REST/twilight/coreslot/v1/slots` | 200 |
| `ActiveCoreSlots` | `/twilight/coreslot/v1/active-slots` | — | `QueryCoreSlotsResponse` | `curl $REST/twilight/coreslot/v1/active-slots` | 200 |
| `CoreSlotByOperator` | `/twilight/coreslot/v1/operators/{operator_address}` | `operator_address` (path, bech32) | `QueryCoreSlotResponse` | `curl $REST/twilight/coreslot/v1/operators/twilight1...` | 200; 404 if none |
| `CoreSlotByConsensusAddress` | `/twilight/coreslot/v1/consensus/{consensus_address}` | `consensus_address` (path) | `QueryCoreSlotResponse` | `curl $REST/twilight/coreslot/v1/consensus/<addr>` | 200; 404 if none |
| `PendingKeyRotations` | `/twilight/coreslot/v1/pending-key-rotations` | — | `QueryPendingKeyRotationsResponse` | `curl $REST/twilight/coreslot/v1/pending-key-rotations` | 200 |
| `LastAppliedValidators` | `/twilight/coreslot/v1/last-applied-validators` | — | `QueryLastAppliedValidatorsResponse` | `curl $REST/twilight/coreslot/v1/last-applied-validators` | 200 |
| `ReservedConsensusAddress` | `/twilight/coreslot/v1/reserved-consensus-address/{consensus_address}` | `consensus_address` (path) | `QueryReservedConsensusAddressResponse` | `curl $REST/twilight/coreslot/v1/reserved-consensus-address/<addr>` | 200; 404 if none |
| `RewardWeight` | `/twilight/coreslot/v1/slots/{slot_id}/reward-weight` | `slot_id` (path, uint64) | `QueryRewardWeightResponse` | `curl $REST/twilight/coreslot/v1/slots/1/reward-weight` | 200 |

### Notes
- **`ActiveCoreSlots` uses `/active-slots`, not `/slots/active`.** A `/slots/active`
  path collides with `/slots/{slot_id}` and is parsed as `slot_id="active"` → HTTP 400.
  The path was changed when REST was wired (API-0/1/2); the gRPC method name is
  unchanged. No prior REST consumer existed (REST was never served before this).
- `PendingKeyRotations`, `LastAppliedValidators`, `ReservedConsensusAddress`,
  `RewardWeight` had no `google.api.http` annotation before this work and were
  gRPC-only; they are now REST-exposed.
- Standard cosmos modules Twilight does **not** run (staking/gov/mint/distribution)
  return `501` by design — that is expected, not a regression.

Smoke check: `./scripts/smoke-api-surface.sh` (honors `BASE_REST`, `BASE_GRPC`).
