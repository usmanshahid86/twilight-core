---
title: Localnet Drills
---

# Localnet Drills

:::note Current status
This page reflects the **Phase 10 validated** implementation. The zero-premine,
multi-epoch soak, and authority drill targets below are **Phase 11 pending** and
do not exist yet.
:::

## Available now

| Target | Proves |
|---|---|
| `make localnet-smoke` | four-node startup + app/validators/next-validators hash agreement (default profile; does **not** close a rewards epoch) |
| `make localnet-rewards-smoke` | four-node rewards finalization, exact minting/distribution, a real claim, and cross-node app-hash agreement before/after finalization and after claim |

See [Localnet](../chain/localnet.md) for what each proves and the funded-fixture
caveat.

## Pending (Phase 11)

:::warning Phase 11 pending
These targets are planned but **not implemented**:

- `make localnet-rewards-zero-premine-smoke` — finalization/claim determinism on a
  true **zero-premine** genesis (`cumulative_emitted = 0`, no funded accounts);
  asserts supply rises only from emission.
- `make localnet-rewards-soak` — a short-epoch network run across **multiple**
  epoch boundaries; asserts carry-forward chaining, pending-param activation, and
  sustained multi-node agreement.
- `make localnet-rewards-authority-smoke` — exercises `update-params` / `pause` /
  `resume` through real network transactions.
:::

## Notes for adding a drill

- Reuse the existing parameterized harness (`scripts/localnet/rewards-smoke.sh`
  honors `REWARDS_EPOCH_LENGTH` and `TWILIGHT_LOCALNET_HOME`) rather than adding
  parallel infrastructure.
- A zero-premine drill must **not** fund accounts in genesis; the chain is
  fee-free, so a zero-balance signer can still submit a claim with `--fees 0utwlt`.
- Always assert app-hash agreement **after** the state transition under test, not
  just before.
