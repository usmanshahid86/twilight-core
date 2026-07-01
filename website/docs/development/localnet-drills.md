---
title: Localnet Drills
---

# Localnet Drills

:::note Target availability
The smoke and soak targets below are implemented. Zero-premine is already
exercised today via the soak's `PREMINE=off` mode (it strips genesis balances and
asserts supply rises only from emission); only the *dedicated* zero-premine smoke
target and the authority drill target listed under *Planned* do not exist yet.
:::

## Available now

| Target | Covers |
|---|---|
| `make localnet-smoke` | four-node startup + app/validators/next-validators hash agreement (default profile; does **not** close a rewards epoch) |
| `make localnet-rewards-smoke` | four-node rewards finalization, exact minting/allocation, a claim transaction, and cross-node app-hash agreement before/after finalization and after claim |
| `make localnet-rewards-soak` | a long, continuous short-epoch run across many epoch boundaries; asserts carry-forward chaining, pending-param activation, pause/resume, and sustained multi-node agreement (basis of the endurance soak) |

See [Localnet](../chain/localnet.md) for what each covers and the funded-fixture
caveat.

## Planned

:::warning Not yet implemented
These targets are planned but **not implemented**:

- `make localnet-rewards-zero-premine-smoke` — a *dedicated fast* smoke for
  finalization/claim determinism on a true **zero-premine** genesis
  (`cumulative_emitted = 0`, no funded accounts). Zero-premine is already covered
  today by the soak's `PREMINE=off` mode; this would be a quick standalone target.
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
