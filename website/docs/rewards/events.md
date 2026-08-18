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
| `params_update_queued` | an authority params update is queued | `authority` |
| `params_activated` | queued params activate at a boundary | — |
| `rewards_paused` | emergency pause | `authority` |
| `rewards_resumed` | emergency resume | `authority` |
| `treasury_paid` | a nonzero treasury transfer occurs at finalization | `payout_address`, `amount` |

(Attribute names are defined in `x/rewards/types/events.go`.)

## Notes for indexers

- All amounts are `utwlt` integer strings.
- `epoch_finalized` is the authoritative signal that a new epoch aggregate and
  its slot entitlements exist; follow with
  [`epoch-reward <epoch>`](queries.md) to read the aggregate.
- There is no claim event. The claim path is retired, and `reward_claimed` is
  never emitted again; value now leaves escrow through settlement in `x/mining`.
- CoreSlot separately emits `coreslot_validator_update_emitted` for validator-set
  changes (see [Consensus & CoreSlot](../chain/consensus-and-coreslot.md)); that
  is a CoreSlot event, not a rewards event.
