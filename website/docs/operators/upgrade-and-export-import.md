---
title: Upgrade & Export/Import
---

# Upgrade & Export/Import

## Exporting state

```bash
twilightd export --home <node-home> --output-document state.json
```

This exports the full app state (including `rewards` and `coreslot`) via the
module manager's export path. The exported `app_state.rewards` contains:

- `params`, `state` (incl. `cumulative_emitted`, `carry_forward_remainder`),
  `current_epoch_config`,
- `finalized_epochs[]` and `pending_params` if present.

## What must round-trip

A re-import (`InitChain` from exported state) must preserve, exactly:

- rewards params and state (cumulative emitted, carry, current epoch);
- the current epoch config;
- every finalized epoch aggregate, every outstanding slot entitlement, and the
  outstanding entitlement liability;
- the `rewards` module account balance and total `utwlt` supply.

## What export/import covers

A full app-level export/import test finalizes an epoch, exports via
`App.ExportAppStateAndValidators`, `InitChain`s a **fresh app** from the exported
state, **continues a block**, and asserts the rewards params, state, cumulative
emitted, finalized epoch, slot entitlement, module balance, native supply, and
continued active-block accounting are all preserved — with no panic.

This exercises the full app / module-manager export/import path, not a keeper-only
genesis round trip.

## Verify after import

```bash
twilightd rewards-query cumulative-emitted --node <rpc>   # matches pre-export
twilightd rewards-query module-balances --node <rpc>      # matches pre-export
twilightd rewards-query epoch-reward <finalized-epoch> --node <rpc>
```

Then advance one block and confirm the chain continues to finalize epochs
coherently.

:::warning On-chain upgrades not yet supported
A coordinated **on-chain upgrade** procedure (handler / store migration) is not
part of the current implementation. The rewards store key and proto are stable;
any future upgrade must preserve the immutable `native_denom` / `max_supply` and
the finalized epoch and entitlement history. This tooling is not available in the current implementation.
:::
