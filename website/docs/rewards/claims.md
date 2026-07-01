---
title: Claims
---

# Claims

When an epoch finalizes, the module writes one **claim record** per eligible slot.
Each record snapshots the slot's operator and **payout address**, the active
blocks, the reward weight (metadata-only — no payout effect in v1), and the reward
amount. The amount is derived from active blocks, not the reward weight. Records
start unclaimed and are held indefinitely until claimed.

## The claim model

- **Anyone can trigger a claim** — the signer need not be the operator or payout.
- Funds always go to the record's **snapshotted payout address**, never to the
  signer (unless the signer happens to be that payout address).
- A claim is a transfer of already-minted balance from the rewards module account;
  it **mints nothing** and **does not change total supply**.
- A successful claim marks the record `claimed = true` with the claim height.
- A **double claim fails** — a claimed record cannot be claimed again.
- The finalized epoch aggregate is immutable; claims mutate only the separate
  claim records.

:::warning The signer is not the payout selector
The payout address is fixed at finalization (snapshotted). Submitting a claim
from a different account does **not** redirect funds — it only pays the recorded
payout. A later change to a slot's payout address does not affect historical
claim records.
:::

## Claim transaction

```bash
twilightd rewards claim [slot-id] [start-epoch] [end-epoch] \
  --from <signer> --chain-id <chain-id> --node <rpc> --yes
```

- `start-epoch == end-epoch` claims a single epoch; a range claims multiple
  epochs at once (bounded by `max_claim_epochs_per_tx`).
- Across a range, rewards are grouped and sent per snapshotted payout address.

Example (claim slot 1, epoch 1 only):

```bash
twilightd rewards claim 1 1 1 --from operator1 \
  --chain-id twilight-rewards-localnet-1 --node tcp://127.0.0.1:26657 \
  --gas 600000 --fees 0utwlt --yes
```

If the signer differs from the payout — e.g. `operator1` claims slot 1 whose
payout is `operator0` — the funds go to `operator0` (`1,040,475utwlt`) and
`operator1` receives nothing; the rewards module balance drops from `4,161,900` to
`3,121,425utwlt`, total supply is unchanged, and a second claim of the same record
fails.

## Failure cases

| Condition | Result |
|---|---|
| Claims disabled (paused) | rejected |
| Epoch not finalized | rejected |
| Claim record missing | rejected |
| Amount not positive | rejected |
| Already claimed (double claim) | rejected |
| Range exceeds `max_claim_epochs_per_tx` | rejected |
| Rewards module balance insufficient | rejected |

## Checking claimable rewards

Before claiming, query what is owed:

```bash
twilightd rewards-query slot-rewards <slot-id> --limit 10 --node <rpc>
twilightd rewards-query claimable <slot-id> <start-epoch> <end-epoch> --node <rpc>
```

See [Queries](queries.md) for output fields.
