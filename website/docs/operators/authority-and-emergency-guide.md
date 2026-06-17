---
title: Authority & Emergency Guide
---

# Authority & Emergency Guide

Rewards is governed by **two CoreSlot authorities**. Rewards stores no authority
of its own — both are read from CoreSlot params.

## Who can do what

| Action | Authority | Effect timing |
|---|---|---|
| Queue a rewards params update | CoreSlot **authority** | Activates at the **next epoch boundary** |
| Pause emissions / settlement / claims | CoreSlot **emergency authority** | **Immediate** |
| Resume emissions / settlement / claims | CoreSlot **emergency authority** | **Immediate** |
| Claim rewards | any signer | Immediate (pays snapshotted payout) |

Neither authority can change the immutable `native_denom` or `max_supply`, and
the normal authority cannot pause (that is emergency-only).

## Params update (normal authority)

```bash
twilightd rewards-query params --node <rpc> --output json > params.json
# edit mutable fields only
twilightd rewards update-params ./params.json --from <authority> \
  --chain-id <chain-id> --node <rpc> --yes
```

The update is queued; the current epoch settles under its existing snapshot and
the change applies to the next epoch. See [Parameters](../rewards/params.md).

## Pause / resume (emergency authority)

```bash
twilightd rewards pause  --emissions --settlement --claims --from <emergency-authority> ...
twilightd rewards resume --emissions --settlement --claims --from <emergency-authority> ...
```

| Flag | What pausing it does |
|---|---|
| `--emissions` | Finalization still runs but mints zero; cumulative emitted frozen; carry preserved |
| `--settlement` | EndBlock stops finalizing; active-block counting continues; finalizes once when re-enabled |
| `--claims` | Claim transactions rejected; finalization unaffected |

At least one flag is required. Pausing changes only the named flags; it does not
touch pending params or closed epochs.

## Recovery

- **Accidental pause:** `resume` the same flags. Settlement re-enabled past a
  boundary finalizes the open epoch once.
- **Bad queued params:** queue a corrected `update-params` before the next
  boundary (the latest queued params win).
- **Key compromise:** rotate the authority/emergency key via CoreSlot. A
  compromised emergency key can deny-of-service (pause) but cannot mint, redirect
  payouts, or change immutable fields; a compromised authority key can queue
  params at the next boundary but cannot change denom/cap or pause.

## What not to do

:::warning
- Do not attempt to change `native_denom` or `max_supply` via `update-params` —
  rejected by the keeper.
- Do not enable fees or weighted rewards in v1 — rejected by validation.
- Do not assume a pause takes effect at the next epoch — pause/resume are
  **immediate**, params updates are **queued**.
:::
