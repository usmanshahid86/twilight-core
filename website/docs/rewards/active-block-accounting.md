---
title: Active-Block Accounting
---

# Active-Block Accounting

Reward eligibility is based on **active blocks**, accumulated during BeginBlock —
not on a slot's status at finalization.

## How counters accrue

Each block, rewards `BeginBlock`:

1. reads the active CoreSlot set (`GetActiveSlots`, ascending slot id);
2. requires every returned slot to be active (fail-closed otherwise);
3. increments the `(current_epoch, slot_id)` counter by 1 for each.

The counters are stored keyed by `(epoch, slot_id)`, so they are scoped per epoch
and iterate deterministically by slot id.

## The block-N convention

Reward credit for block N uses the active set observed at **BeginBlock(N)**. A
CoreSlot status change inside block N (via a transaction processed that block)
affects credit starting at **block N+1**.

## Eligibility survives status changes

Because eligibility is the accumulated counter, a slot that was active earlier in
the epoch and earned credit is **still paid** at finalization even if it is
suspended or removed before the epoch closes. This is safe because CoreSlot
retains the slot row, operator/payout addresses, and reward-weight row on suspend
and remove (see [Consensus & CoreSlot](../chain/consensus-and-coreslot.md)).

Finalization reads each credited slot's snapshot (`GetSlot`, `GetRewardWeight`) —
collections that suspend/remove retain — never the operator/consensus indexes that
remove deletes.

## Querying the open epoch

```bash
twilightd rewards-query current-active-blocks --limit 10 --node <rpc>
# epoch_number, active_blocks[].slot_id, active_blocks[].blocks_active
```

This is paginated (ascending slot id). After finalization the closed epoch's rows
are deleted, so this reflects the currently-open epoch only.

## Cleanup

When an epoch finalizes, its active-block rows are deleted. The carry and the
entitlements persist; the counters do not. The per-slot count the epoch produced
is preserved on the entitlement.
