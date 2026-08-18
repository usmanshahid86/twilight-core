---
title: CoreSlot Operator Guide
---

# CoreSlot Operator Guide

CoreSlot is the PoA validator authority. This guide covers what an operator needs
to know about how their slot relates to rewards; the consensus model is in
[Consensus & CoreSlot](../chain/consensus-and-coreslot.md).

## A slot's reward-relevant fields

| Field | Used by | Notes |
|---|---|---|
| Operator address | rewards snapshot | Recorded into slot entitlements |
| **Payout address** | rewards payout | Where an entitlement's remainder is released; snapshotted at finalization |
| Reward weight | rewards metadata | Snapshotted for forward compatibility; separate from consensus power and not used for v1 reward allocation |
| Consensus power | consensus only | Validator voting power; never used for reward accounting |
| Status | both | Only `ACTIVE` slots vote and earn active-block credit |

## Active slots and rewards

Only `SLOT_STATUS_ACTIVE` slots earn active-block credit. Each block a slot is
active, the rewards module increments its `(epoch, slot)` counter (see
[Active-Block Accounting](../rewards/active-block-accounting.md)).

## Updating your payout address

Operators can update their own payout address. **Timing matters:** the payout
address is snapshotted into the entitlement **at finalization**. Changing it
affects future epochs' entitlements, not already-finalized ones. Funds for a
finalized epoch always go to the address recorded at that epoch's finalization.

## Suspend / remove implications for rewards

Suspending or removing a slot stops it from earning new active-block credit
(it is no longer in the active set). But:

- Credit already earned earlier in the open epoch is **still paid** at
  finalization.
- CoreSlot **retains** the slot row, operator/payout addresses, and reward-weight
  row on suspend and remove (it only changes status and zeroes consensus power),
  so rewards finalization can still snapshot a suspended/removed-but-credited slot.

This is why earned rewards survive suspend or remove: finalization can still read
the retained slot and reward metadata for any slot that already earned
active-block credit, and the entitlement it writes is payable afterwards.

## Reward snapshot dependencies (summary)

For a slot that earned credit, finalization needs, and CoreSlot retains:

1. the slot row (`GetSlot`);
2. valid operator and payout address fields on it;
3. the matching `OperatorRewardWeight` row (`GetRewardWeight`).

If any of these were ever deleted on suspend/remove, finalization would fail —
they are not, which is why a suspended/removed slot's earned reward is still
recorded as a payable entitlement.
