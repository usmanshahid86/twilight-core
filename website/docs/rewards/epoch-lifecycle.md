---
title: Epoch Lifecycle
---

# Epoch Lifecycle

An epoch is a window of `epoch_length_blocks` blocks. Active blocks accumulate
during the window; at its end the epoch finalizes.

## Boundary

The configured end height of the open epoch is:

```text
current_epoch_end_height = current_epoch_start_height + epoch_length_blocks − 1
```

This uses the **current epoch snapshot's** length, captured when the epoch opened
— not the latest params. So a queued `epoch_length_blocks` change does not move
the current epoch's end; it applies to the next epoch.

Query it:

```bash
twilightd rewards-query epoch-info --node <rpc>
# state.current_epoch, state.current_epoch_start_height, current_epoch_end_height
```

## Finalization steps

At EndBlock, when the height reaches the boundary **and** settlement is enabled,
the module finalizes the epoch atomically (in a cache context, so any fault rolls
back entirely):

1. Compute the clipped epoch emission ([economics](economics.mdx)). If emissions
   are paused, emission is zero and cumulative emitted does not advance.
2. Assert `cumulative_emitted + emission ≤ max_supply` **before** minting.
3. Mint the (positive) emission as `utwlt` into the `rewards` account.
4. Build the pool: `emission + carry_in + fees − treasury` (fees 0, treasury 0 by
   default).
5. Read the epoch's active-block rows and allocate uniformly by active blocks.
6. Write the immutable epoch aggregate and the separate per-slot claim records.
7. Set `carry_forward_remainder = carryOut`; update `cumulative_emitted`.
8. Delete the closed epoch's active-block rows.
9. Advance: `current_epoch += 1`, `current_epoch_start_height = end + 1`.
10. Activate pending params (if any) and build the next epoch config snapshot.

## Pause interactions

| Paused flag | Effect on the epoch |
|---|---|
| `emissions_enabled = false` | finalize still runs; mints zero; cumulative emitted unchanged; carry preserved |
| `epoch_settlement_enabled = false` | EndBlock does not finalize; active-block counting continues; finalizes once when re-enabled past the boundary |
| `claims_enabled = false` | finalize unaffected; claim transactions rejected |

## Pending params activation

A params update queued via [`update-params`](transactions.md#update-params) sits
in `PendingParams` until the next finalization, where it is activated and the next
epoch config snapshot is built from it. The immediate pause flags are preserved
across activation (they are emergency-controlled, not part of the queued economic
update).

## Edge cases

- **Empty active set:** emission is still minted and cumulative advances (when
  emissions enabled); no claim records are created; the full pool carries forward.
- **Cap reached / subsidy floored to zero:** finalize with zero mint and existing
  carry.
- **Mid-epoch halving:** the subsidy changes at the exact cumulative threshold.
