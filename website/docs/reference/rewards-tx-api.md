---
title: Rewards Tx API
---

# Rewards Tx API

The **authoritative, always-current reference** is auto-generated from the chain's
protobuf definitions — this page is a map, not a hand-maintained schema:

- **Swagger / OpenAPI UI** — a node's REST endpoint at `/swagger/`.
- **Proto** — `proto/twilight/rewards/v1/tx.proto` (`twilight.rewards.v1`).
- **CLI** — `twilightd rewards --help`.

For **narrative usage** (flags and examples), see [Transactions](../rewards/transactions.md).

## Messages

| CLI | Message | Authority | Effect |
|---|---|---|---|
| `rewards update-params <file>` | `MsgUpdateRewardsParams` | CoreSlot authority | queues `PendingParams`; activates at the next epoch boundary |
| `rewards pause` | `MsgPauseRewards` | emergency authority | immediately pauses the requested flags (`--emissions` / `--settlement` / `--claims`) |
| `rewards resume` | `MsgResumeRewards` | emergency authority | resumes the requested flags |
| `rewards claim <slot> <start> <end>` | `MsgClaimRewards` | any signer | pays each record's snapshotted payout address; mints nothing |

Full field/type schemas are in the Swagger UI / proto.

## Key semantics

- **`claim` pays the snapshotted payout address** (grouped by payout across the
  range); the signer receives nothing unless it is itself a payout, and total
  supply is unchanged.
- **Immutable fields** (`native_denom`, `max_supply`) and unsupported v1 features
  are rejected by `update-params`.
- The CLI never infers or hardcodes authority — the message server enforces it.
  `claim` is rejected if claims are paused, an epoch is not finalized, a record is
  missing or already claimed, the amount is non-positive, the range exceeds
  `max_claim_epochs_per_tx`, or the module balance is insufficient.
