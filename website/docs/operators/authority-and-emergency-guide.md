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
| Pause rewards | CoreSlot **emergency authority** | Next block (H+1) |
| Resume rewards | CoreSlot **emergency authority** | Next block (H+1) |

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
twilightd rewards pause  --from <emergency-authority> --chain-id <chain-id> --node <rpc> --yes
twilightd rewards resume --from <emergency-authority> --chain-id <chain-id> --node <rpc> --yes
```

There are no per-area selectors: one canonical pause state stops reward accrual
and release together. A transition accepted in block H takes effect at the
beginning of H+1, before any reward sampling.

Pausing does not stop epoch time. Epoch numbering advances and epochs still
finalize; a fully paused epoch counts zero reward-enabled blocks and emits
nothing. Pausing does not touch pending params or closed epochs.

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
