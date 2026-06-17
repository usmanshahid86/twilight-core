---
title: Events
---

# Events

The rewards module emits typed string events (no typed-proto events). These are
the integration surface for indexers and monitoring. No event or state stores a
transaction hash.

## Event types

| Event | Emitted when | Key attributes |
|---|---|---|
| `epoch_finalized` | an epoch finalizes | `epoch`, `start_height`, `end_height`, `minted_emission`, `cumulative_emitted`, `reward_pool`, `allocated`, `carry_out`, `eligible_slots`, `distribution_method` |
| `reward_claimed` | a claim succeeds | `signer`, `slot_id`, `start_epoch`, `end_epoch`, `amount`, `payout_count` |
| `params_update_queued` | an authority params update is queued | `authority` |
| `params_activated` | queued params activate at a boundary | — |
| `rewards_paused` | emergency pause | `authority` |
| `rewards_resumed` | emergency resume | `authority` |
| `treasury_paid` | a nonzero treasury transfer occurs at finalization | `payout_address`, `amount` |

(Attribute names are defined in `x/rewards/types/events.go`.)

## Notes for indexers

- All amounts are `utwlt` integer strings.
- `reward_claimed` reports a `payout_count` (the number of distinct payout
  addresses paid in a multi-epoch claim), not a single payout — across a range,
  rewards are grouped per snapshotted payout address.
- `epoch_finalized` is the authoritative signal that a new epoch aggregate and
  its claim records exist; follow with
  [`epoch-reward <epoch>`](queries.md) and
  [`slot-rewards <slot>`](queries.md) to read the detail.
- CoreSlot separately emits `coreslot_validator_update_emitted` for validator-set
  changes (see [Consensus & CoreSlot](../chain/consensus-and-coreslot.md)); that
  is a CoreSlot event, not a rewards event.
