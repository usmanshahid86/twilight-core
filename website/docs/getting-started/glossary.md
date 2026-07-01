---
title: Glossary
---

# Glossary

| Term | Meaning |
|---|---|
| **CoreSlot** | A validator/operator slot in `x/coreslot`; the unit of PoA validator authority. Holds operator address, payout address, consensus key, reward weight, and status. |
| **Active slot** | A slot with status `SLOT_STATUS_ACTIVE` — part of the validator set and eligible to earn rewards active-block credit. |
| **Operator address** | The account that operates a slot. |
| **Payout address** | The account that receives a slot's rewards. Snapshotted into each claim record at finalization. |
| **Reward weight** | Operator reward-weight metadata snapshotted for forward compatibility. It is separate from consensus power and is not used for v1 reward allocation. |
| **Consensus power** | A slot's CometBFT voting power; drives validator updates only; never used for reward accounting. |
| **Epoch** | A fixed window of `epoch_length_blocks` blocks over which active blocks accumulate and at whose end rewards finalize. |
| **Active block** | A per-`(epoch, slot)` counter incremented each block a slot is active; the basis for active-block participation allocation. |
| **Finalization** | The EndBlock step that mints the epoch emission, allocates the pool, and writes the epoch aggregate + claim records. |
| **Epoch emission** | The `utwlt` minted for an epoch: the per-block subsidy summed over the epoch, clipped by the supply cap. |
| **Subsidy** | The per-block reward before distribution: `initial_block_subsidy / 2^tier`. |
| **Halving tier** | The number of supply thresholds crossed; each tier halves the subsidy. |
| **Carry-forward** | The unallocated remainder of a pool, carried into the next epoch. |
| **Claim record** | A per-`(slot, epoch)` row recording the owed reward and snapshotted payout; consumed by a claim. |
| **Claim** | A transaction that transfers a finalized reward from the rewards module account to the snapshotted payout. |
| **Authority** | The CoreSlot account that admits slots and queues rewards params updates. |
| **Emergency authority** | The CoreSlot account that can immediately pause/resume rewards flags. |
| **Module account** | `rewards` (Minter; holds minted/unclaimed rewards) and `rewards_fee_pool` (dormant, no permissions). |
| **`utwlt`** | The native base denom — the only accounting denom. |
| **`twlt` / `TWLT`** | Display denom / symbol — metadata only. |
| **Treasury** | An optional external address receiving a configured share of emission; default share is zero. |
| **Fail-closed** | The posture where a rewards lifecycle error halts the block rather than committing partial state. |
