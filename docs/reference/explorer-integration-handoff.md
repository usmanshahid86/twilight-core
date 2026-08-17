# Twilight Chain → Explorer/Indexer Integration Handoff (ground truth)

Purpose: a **fact-checked** snapshot of the actual Twilight chain implementation, so an
explorer/indexer design can be validated against reality (not memory or projection).
Every claim here was verified against the code in `twilight-core` (Cosmos SDK v0.53.7 /
CometBFT). Use it as the checklist a reviewer holds the explorer design up against.

> The single biggest source of explorer bugs on this chain: **it is CoreSlot-PoA +
> rewards only — there is NO staking/gov/mint/distribution.** Anything in the explorer
> that assumes a standard Cosmos validator/staking/gov model is wrong here.

---

## 1. Chain identity & scope

- SDK **v0.53.7**, CometBFT. App is a depinject `runtime.App`.
- Native denom: **`utwlt`** (base, the only denom in stateful accounting); display `twlt`;
  symbol `TWLT`. Bech32 prefixes: accounts `twilight…`, consensus `twilightvalcons…`.
- Registered modules: **auth, bank, consensus, tx, x/coreslot, x/rewards** (+ genutil/params via runtime).
- **NOT present:** staking, gov, mint, distribution, slashing, IBC, group, feegrant,
  authz. Their REST/gRPC routes return **501 Not Implemented** — this is by design, not an
  outage. An explorer must not treat 501 on those as an error or a chain it can't index.
- Devnet chain-id: `twilight-devnet-1`. Localnet: `twilight-localnet-1`.

## 2. Validators come from x/coreslot, NOT staking

This is the most important architectural fact for an indexer.

- Validator set is managed by **x/coreslot** (Proof-of-Authority "slots"), applied via the
  module's EndBlocker emitting CometBFT validator updates. There are **no** `cosmos/staking`
  validators, delegations, unbonding, or `cosmos.staking` events.
- To list/track validators: use CoreSlot queries (below) + CometBFT `/validators`
  (`:26657`). Do **not** query `cosmos/staking/v1beta1/validators` (501).
- Slot lifecycle states: `PENDING → ACTIVE → INACTIVE/SUSPENDED → REMOVED` (+ key rotation).
- **PoA consensus math:** N validators each have voting power 1; CometBFT needs >2/3 to
  commit. So a 2-validator chain is **2-of-2** (either down ⇒ halt). The explorer's
  uptime/health view should reflect this, not staking-style fault tolerance.
- **Consensus addresses in CoreSlot queries are HEX-encoded, not bech32 `valcons`.** The
  keeper rejects bech32 with `encoding/hex: invalid byte`. A real hex value comes from
  CometBFT `/validators` (`validators[].address`). The bech32 `valcons` from
  `cosmos/base/tendermint/.../validatorsets` will NOT work against CoreSlot routes.

## 3. API surfaces & ports

| Surface | Port | Use |
|---|---|---|
| CometBFT RPC | 26657 | blocks, `/block_results`, `/tx`, `/validators`, consensus/node info — generic ingestion |
| gRPC | 9090 | canonical typed query API (rewards + coreslot + enabled cosmos modules) |
| REST (gRPC-gateway) | 1317 | JSON wrapper over the same query services; custom routes wired (see §5) |
| Swagger UI | 1317 `/swagger/` | merged OpenAPI for the enabled surface |

**Live devnet (box 2):** `http://16.192.99.123:{1317,26657}`, gRPC `:9090`. Swagger at
`http://16.192.99.123:1317/swagger/`. REST custom routes return **200** (gateway shipped).
NOTE: older devnet snapshots returned 501 on custom routes — that was the pre-gateway
binary; the current binary serves them.

## 4. Decoding transactions (the explorer's raw-tx fallback)

Type URLs are correct and registered as `sdk.Msg`:
- coreslot (9): `/twilight.coreslot.v1.{MsgRegisterCoreSlot, MsgActivateCoreSlot,
  MsgInactivateCoreSlot, MsgSuspendCoreSlot, MsgRemoveCoreSlot, MsgRotateConsensusKey,
  MsgUpdatePayoutAddress, MsgUpdateOperatorMetadata, MsgUpdateParams}`
- rewards (4): `/twilight.rewards.v1.{MsgClaimRewards, MsgUpdateRewardsParams,
  MsgPauseRewards, MsgResumeRewards}`

Two supported decode paths:
1. **Server-side (easy):** REST `GET /cosmos/tx/v1beta1/txs/{hash}` — the chain decodes
   custom Msgs to JSON. Good for an MVP indexer; depends on the live node.
2. **Client-side (offline):** the repo exports a self-contained `FileDescriptorSet` at
   **`docs/proto/twilight-descriptors.pb`** (regenerate via `scripts/export-proto-descriptor.sh`).
   It bundles the **full Cosmos tx envelope** (`cosmos.tx.v1beta1.{TxRaw,Tx,TxBody,AuthInfo,
   SignerInfo,Fee}`, `cosmos.tx.signing.v1beta1.SignMode`), signer pubkey types
   (`secp256k1/ed25519/multisig`), bank/auth/coin, AND all 9 twilight protos.
   Decode flow: `TxRaw → TxBody(body_bytes) → messages[] (Any) →` resolve each `type_url`.
   `docs/proto/twilight-msg-type-urls.json` is the machine-readable manifest;
   `docs/proto/README.md` has a drop-in protobufjs `decodeRawTx`.
   - **There is no buf / Telescope / ts-proto in the chain repo** — only the descriptor set.
     If the explorer design assumes generated TS bindings exist upstream, that's wrong today.

## 5. REST / query surface (exact)

Full inventory: `docs/reference/rest-routes.md`. Highlights an indexer relies on:

**x/rewards** (`twilight.rewards.v1.Query`, base `/twilight/rewards/v1`): `params`,
`epoch-info`, `next-halving`, `epochs/{epoch_number}`, `slots/{slot_id}/rewards`,
`slots/{slot_id}/claimable`, `cumulative-emitted`, `supply-schedule`,
`current-epoch/active-blocks`, `module-balances`.

**x/coreslot** (`twilight.coreslot.v1.Query`, base `/twilight/coreslot/v1`): `params`,
`slots/{slot_id}`, `slots`, **`active-slots`**, `operators/{operator_address}`,
`consensus/{consensus_address}` (hex), `pending-key-rotations`, `last-applied-validators`,
`reserved-consensus-address/{consensus_address}` (hex), `slots/{slot_id}/reward-weight`.

Query footguns the design MUST account for:
- **`active-slots` not `slots/active`** — the latter collides with `slots/{slot_id}` and
  returns 400 (parsed as `slot_id="active"`).
- **`slot-rewards` / `SlotRewards` paginates ascending by epoch.** A fixed `--limit` page
  drops the most recent epochs once the chain exceeds one page — do NOT use it to find a
  recent epoch's claim status. (This exact bug produced false failures in the soak.) Use
  `ClaimableRewards` (targeted epoch range) or page to the key.
- **`ClaimableRewards` requires `start_epoch` & `end_epoch`** (query params) and returns
  **only UNCLAIMED** records (claimed ones are filtered out). Empty ⇒ claimed (or none).
- **`EpochReward` (`epochs/{n}`) returns 404** for an epoch that isn't finalized yet.
- Authoritative "is this slot-epoch claimed?" lives in **ClaimRecords** (via
  `slot-rewards`/`claimable`), NOT in the `EpochReward` snapshot's embedded rewards.
- Rewards **amounts are strings** (not `cosmos.base.v1beta1.Coin`); denom is `utwlt`.

## 6. Events (exact emitted strings — for event projections)

Verified from `x/{rewards,coreslot}/keeper/events.go` + the `types` constants. Event
**type** strings and their key **attributes**:

**x/rewards:**
- `epoch_finalized` — `epoch`, `start_height`, `end_height`, `minted_emission`,
  `cumulative_emitted`, `reward_pool`, `allocated`, `carry_out`, `eligible_slots`,
  `distribution_method`
- `reward_claimed` — `signer`, `slot_id`, `start_epoch`, `end_epoch`, `amount`, `payout_count`
- `treasury_paid` — `payout_address`, `amount`
- `params_update_queued`, `params_activated` — params governance (authority)
- `rewards_paused`, `rewards_resumed` — emergency pause state

**x/coreslot:** (all attributes use `slot_id`, `operator_address`, and status/consensus keys)
- `coreslot_registered`, `coreslot_activated`, `coreslot_inactivated`, `coreslot_suspended`,
  `coreslot_removed`, `coreslot_key_rotation_requested`, `coreslot_key_rotated`,
  `coreslot_rotation_canceled`, `coreslot_payout_updated`, `coreslot_metadata_updated`,
  `coreslot_params_updated`, `coreslot_validator_update_emitted`
- common attribute keys: `slot_id`, `operator_address`, `consensus_address`,
  `old_status`, `new_status`, `power`, `reason`, `old_consensus_address`,
  `new_consensus_address`, `effective_height`, `authority`, `height`

If the indexer's event projections reference any event name NOT in these two lists (e.g.
`reward_distributed`, `validator_jailed`, staking/gov events), that's a design error —
those events do not exist.

### V2 breaking change — rotation-cancellation event renamed

The rotation-cancellation event was renamed in V2 to correct a misspelling in the
emitted type:

| | event type |
|---|---|
| legacy / V1 | `coreslot_rotation_cancelled` |
| **V2** | `coreslot_rotation_canceled` |

This is an intentional breaking integration-surface cleanup. The chain emits the V2
name **only** — there is no dual emission and no compatibility alias in the node, so
an indexer matching the legacy string will silently stop seeing cancellations rather
than fail loudly. Match on the V2 name.

Nothing else about the event changed: the attribute set (`slot_id`,
`operator_address`, `old_consensus_address`, `new_consensus_address`, `reason`,
`height`) and the `reason` values (`lifecycle_change`, `stale_rotation`) are
unchanged.

An indexer that must ingest history from a pre-V2 chain is the one case that needs
both spellings, and that belongs in the indexer's own decoding layer rather than in
the node.

## 7. Rewards economics (so rewards pages match the chain)

- Epoch-based emission: each epoch finalizes and mints `minted_emission` into the rewards
  module account; tracked as `cumulative_emitted` (vs `max_supply`).
- **Halving is by supply threshold**, not block height (see `supply-schedule` /
  `next-halving`). Don't model it as a fixed block-height halving.
- Per-slot distribution: each epoch's emission is split across eligible active slots by
  reward weight; claimable per (slot, epoch) until claimed. Claims can span an epoch range.
- Module accounting is queryable via `module-balances` (rewards + fee-pool balances).
- Premine is configurable (devnet may have funded accounts; the soak ran zero-premine).

## 8. What the explorer can rely on as data sources

- **Blocks/txs:** CometBFT RPC `/block`, `/block_results` (per-tx code/gas/events),
  `/tx?hash=`. Or REST `cosmos/tx/v1beta1` (decodes Msgs).
- **Generic state:** `cosmos/bank` (supply, balances), `cosmos/auth` (accounts),
  `cosmos/base/tendermint` (blocks, validatorsets), `cosmos/base/node`.
- **Custom state:** the rewards + coreslot queries in §5 (REST or gRPC).
- A reference Go reader exists in the chain repo: `tools/dashboard` reuses the generated
  query clients + `TxConfig`/codec over RPC and decodes all custom modules — a working
  example of correct decoding (note: it had to call `rewardstypes.RegisterInterfaces` +
  `coreslottypes.RegisterInterfaces` on the codec; the equivalent for a TS client is
  loading `twilight-descriptors.pb`).

## 9. Review checklist (hold the explorer design against these)

1. Does it avoid assuming staking/gov/mint/distribution exist? (validators via CoreSlot)
2. Does it decode custom Msgs via the descriptor set or the REST tx service (not hand-rolled
   or assuming upstream TS bindings)?
3. Does it use hex (not bech32 valcons) for CoreSlot consensus-address lookups?
4. Does it avoid the `slot-rewards` fixed-`limit` pagination trap for recent-epoch state?
5. Do its event projections match exactly the event names in §6 (no invented events)?
6. Does it model halving by supply threshold, amounts as strings/`utwlt`, claims as epoch
   ranges, and "claimed" from ClaimRecords?
7. Does it treat 501 on absent modules as expected, and `active-slots` (not `slots/active`)?
8. Does it track validator-set changes via CoreSlot events + CometBFT validators, and
   reflect the N-of-N PoA liveness model?

## 10. Source-of-truth files in the chain repo (for the agent to cite)

- `docs/reference/rest-routes.md` — full REST route table
- `docs/reference/swagger.md` + `app/openapi/twilight.swagger.json` — OpenAPI of the surface
- `docs/proto/{twilight-descriptors.pb, twilight-msg-type-urls.json, README.md}` — tx decode
- `proto/twilight/{coreslot,rewards}/v1/*.proto` — schema
- `x/{coreslot,rewards}/types/codec.go` — registered Msg type URLs
- `x/{coreslot,rewards}/keeper/events.go` + `types` consts — emitted events
- `x/{coreslot,rewards}/keeper/query_server.go` — query semantics (pagination, not-found, hex keys)
