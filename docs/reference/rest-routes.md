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
| `CoreSlotByConsensusAddress` | `/twilight/coreslot/v1/consensus/{consensus_address}` | `consensus_address` (path, **hex**) | `QueryCoreSlotResponse` | `curl $REST/twilight/coreslot/v1/consensus/<HEXADDR>` | 200; 404 if none |
| `PendingKeyRotations` | `/twilight/coreslot/v1/pending-key-rotations` | — | `QueryPendingKeyRotationsResponse` | `curl $REST/twilight/coreslot/v1/pending-key-rotations` | 200 |
| `LastAppliedValidators` | `/twilight/coreslot/v1/last-applied-validators` | — | `QueryLastAppliedValidatorsResponse` | `curl $REST/twilight/coreslot/v1/last-applied-validators` | 200 |
| `ReservedConsensusAddress` | `/twilight/coreslot/v1/reserved-consensus-address/{consensus_address}` | `consensus_address` (path) | `QueryReservedConsensusAddressResponse` | `curl $REST/twilight/coreslot/v1/reserved-consensus-address/<addr>` | 200; 404 if none |
| `RewardWeight` | `/twilight/coreslot/v1/slots/{slot_id}/reward-weight` | `slot_id` (path, uint64) | `QueryRewardWeightResponse` | `curl $REST/twilight/coreslot/v1/slots/1/reward-weight` | 200 |

## x/mining — `twilight.mining.v1.Query`

| gRPC method | REST path | Request params | Response type | Example curl | Expected |
|---|---|---|---|---|---|
| `Settlement` | `/twilight/mining/v1/settlements/{slot_id}/{epoch}` | `slot_id`, `epoch` (path, uint64) | `QuerySettlementResponse` | `curl $REST/twilight/mining/v1/settlements/1/1` | 200; 404 if no settlement |
| `OpenSettlements` | `/twilight/mining/v1/slots/{slot_id}/open-settlements` | `slot_id` (path, uint64); `pagination.*` (query) | `QueryOpenSettlementsResponse` | `curl $REST/twilight/mining/v1/slots/1/open-settlements` | 200 |
| `SettlementClock` | `/twilight/mining/v1/settlement-clock` | — | `QuerySettlementClockResponse` | `curl $REST/twilight/mining/v1/settlement-clock` | 200 |
| `DistributionModeVersion` | `/twilight/mining/v1/distribution-mode-versions/{version}` | `version` (path, uint64) | `QueryDistributionModeVersionResponse` | `curl $REST/twilight/mining/v1/distribution-mode-versions/1` | 200; 404 if no such version |
| `DistributionModeVersions` | `/twilight/mining/v1/distribution-mode-versions` | `pagination.*` (query) | `QueryDistributionModeVersionsResponse` | `curl $REST/twilight/mining/v1/distribution-mode-versions` | 200 |
| `SelectionParamsVersion` | `/twilight/mining/v1/selection-params-versions/{version}` | `version` (path, uint64) | `QuerySelectionParamsVersionResponse` | `curl $REST/twilight/mining/v1/selection-params-versions/1` | 200; 404 if no such version |
| `SelectionParamsVersions` | `/twilight/mining/v1/selection-params-versions` | `pagination.*` (query) | `QuerySelectionParamsVersionsResponse` | `curl $REST/twilight/mining/v1/selection-params-versions` | 200 |
| `SettlementParamsVersion` | `/twilight/mining/v1/settlement-params-versions/{version}` | `version` (path, uint64) | `QuerySettlementParamsVersionResponse` | `curl $REST/twilight/mining/v1/settlement-params-versions/1` | 200; 404 if no such version |
| `SettlementParamsVersions` | `/twilight/mining/v1/settlement-params-versions` | `pagination.*` (query) | `QuerySettlementParamsVersionsResponse` | `curl $REST/twilight/mining/v1/settlement-params-versions` | 200 |
| `TargetEpochInterpretation` | `/twilight/mining/v1/target-epochs/{target_epoch}` | `target_epoch` (path, uint64) | `QueryTargetEpochInterpretationResponse` | `curl $REST/twilight/mining/v1/target-epochs/4` | 200; 400 for `0` |
| `ValidateEconomicAddress` | `/twilight/mining/v1/economic-address` | `address` (**query**, string) | `QueryValidateEconomicAddressResponse` | `curl "$REST/twilight/mining/v1/economic-address?address=twilight1..."` | 200 (including for a rejected address) |

### Consumer read contract (x/mining)

These two exist so a consumer reads the chain's own interpretation instead of
reimplementing a consensus rule. Every copy of such a rule outside the chain can drift
from what settlement actually does, without anything failing loudly.

- **`TargetEpochInterpretation`** answers which boundary binds a target epoch, which
  distribution-mode row governs that boundary, and whether the target is canonically a
  Selection target. `binding_epoch` is **diagnostic**: it reports where the answer came
  from so an operator can audit it, and a consumer that recomputes anything from it has
  reintroduced the copy this route removes. `selection_applicable` is a statement about
  canonical **binding**, not about runtime readiness — it does not assert that a Selection
  producer is enabled in the deployment. Target `0` is a malformed request (`400`); a
  damaged or missing distribution-mode history is `500`, never `404`, because every
  initialized chain has that history and "no mode configured" is not a thing a consumer
  may conclude.

- **`ValidateEconomicAddress`** asks the same canonical rule settlement execution
  enforces. **The address is a query parameter, not a path segment**, because a gateway
  path segment must be non-empty to match its pattern and the empty address is a
  *successful* domain rejection rather than a malformed request. `?address=` and an
  omitted parameter both reach the handler.
  - An inadmissible address is a **`200`** carrying `admissible=false` and a
    `rejection_reason` (`EMPTY`, `INVALID`, `MODULE_ACCOUNT`, `BANK_BLOCKED`). Only a
    defective request envelope, or a node whose rule cannot be applied at all, is an
    error. A consumer MUST NOT convert "the chain could not answer" into participant
    exclusion.
  - `canonical_address` is populated **only** when `admissible=true`, and is empty on
    every rejection — including a module or blocked address, which parses cleanly and is
    still inadmissible. Do not use a normalized address from a rejected result.
  - It reports admissibility under the **serving node's currently configured** rule. Its
    module-account and bank-blocked sets are application configuration copied into the
    validator at process construction, not consensus state read through the query
    context, so supplying `x-cosmos-block-height` does **not** reconstruct the app
    configuration of a prior height.

### Absence, failure and pagination (x/mining)

- `Settlement` and `SlotEntitlement` absent → `404`. Existing but corrupt or unreadable
  state → `500`. Corruption is never reported as absence, and no response is completed
  with a synthesized default.
- A consumer MUST NOT treat an entitlement `404` as definitive zero participation until
  the target epoch has also been proven finalized **at the same pinned height**.
- **`OpenSettlements` discovery is complete only when `next_key` is empty.** An empty
  `settlements` list with a non-empty `next_key` is **not** proof that no open settlement
  remains: the server-side bound applies to canonical rows inspected, not to rows
  returned.
- The three version histories page the same way — follow `next_key` until it is empty for
  a complete traversal.
- `offset`, `count_total` and `reverse` are refused on these listings. The key cursor is
  the supported way to continue; the others would let one request cost work proportional
  to the whole collection.
- Settlement creation height is **not** a stored field. It is
  `EpochBoundaries(settlement.epoch).end_height`; `SlotEntitlement.created_height` matches
  it under canonical execution and may be cross-checked.

### Notes
- **`ActiveCoreSlots` uses `/active-slots`, not `/slots/active`.** A `/slots/active`
  path collides with `/slots/{slot_id}` and is parsed as `slot_id="active"` → HTTP 400.
  The path was changed when REST was wired (API-0/1/2); the gRPC method name is
  unchanged. No prior REST consumer existed (REST was never served before this).
- `PendingKeyRotations`, `LastAppliedValidators`, `ReservedConsensusAddress`,
  `RewardWeight` had no `google.api.http` annotation before this work and were
  gRPC-only; they are now REST-exposed.
- **`RewardWeight` is metadata-only in v1 — do not build payout logic on it.** The
  `OperatorRewardWeight` fields (`base_weight`, `uptime_weight`, `performance_weight`,
  `stake_weight`, `external_weight`, `final_weight`) are snapshotted but have **no payout
  effect**. Rewards are allocated solely by active-block participation
  (`amount = pool × blocks_active / Σ blocks_active`); weighted rewards are code-gated and
  inactive in v1. See [ADR-0002](../architecture/adr/0002-rewards-emission.md).
- Standard cosmos modules Twilight does **not** run (staking/gov/mint/distribution)
  return `501` by design — that is expected, not a regression.
- **`CoreSlotByConsensusAddress` / `ReservedConsensusAddress` take a hex-encoded
  consensus address** (the keeper rejects bech32 `valcons`). A real hex value is
  available from CometBFT `:26657/validators` (`validators[].address`).

Smoke check: `./scripts/smoke-api-surface.sh` (honors `BASE_REST`, `BASE_GRPC`,
`BASE_RPC`). It exercises `CoreSlotByOperator` (REST-sourced operator) and
`CoreSlotByConsensusAddress` (hex cons address via `BASE_RPC`) with real fixtures.

`ReservedConsensusAddress` 200 coverage:
- **Integration (deterministic):** `x/coreslot/keeper/query_server_test.go` seeds a
  reservation via genesis and via the realistic inactivate→remove lifecycle, then asserts
  the query returns it.
- **Smoke (opt-in):** a clean chain has no reservations, so the smoke check is gated on
  `RESERVED_CONS_HEX` (a known reserved lowercase-hex address) and skipped otherwise. To
  produce one on a localnet, run `scripts/seed-reservation.sh` after `init.sh` and before
  `start.sh`; it seeds a reservation into genesis and prints the hex to export:
  ```sh
  RESERVED_CONS_HEX="$(TWILIGHT_LOCALNET_HOME=/tmp/twilight-localnet ./scripts/seed-reservation.sh -q)" \
    ./scripts/smoke-api-surface.sh
  ```
