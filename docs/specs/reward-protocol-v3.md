# Twilight Reward Protocol — Normative Specification v3

> **⛔ ARCHIVED / PARKED (2026-08).** The team simplified the chain design. The RANDAO
> random-single-winner reward protocol described here is **superseded for now by the TokenDrop
> direction** — uniform per-block emission to every ACTIVE slot, with randomness moved off-chain to
> *client* selection. This v3 document (and its source `reward-redesign-decision-inventory.md`, the
> layered plan, and any RANDAO task graph) is **retained intact for possible future revival** and is
> **no longer the implementation authority**. Do not implement from it unless the team explicitly
> revives it. See the TokenDrop specs for the current direction.

**Status:** ARCHIVED (was NORMATIVE). Single authority for the *RANDAO* reward protocol, generated
from `docs/research/reward-redesign-decision-inventory.md`. Parked pending possible future revival.

**Chain:** Cosmos SDK v0.53.7 / CometBFT v0.38.21 / Go 1.25.x, feeless Proof-of-Authority.
**Denominations:** base `utwlt` (the only accounting denom); display `twlt` = 10^6 `utwlt`
(presentation only — never appears in on-chain amounts).
**Scope:** two custom modules change — `x/coreslot` (small deltas) and `x/rewards` (rewrite) — plus a
new `x/randomness` module and an off-consensus runtime worker.

> Requirement keywords: **MUST / MUST NOT / SHALL** = consensus-binding; **SHOULD** = strong default;
> **MAY** = optional. Every state transition is **fail-closed**: it runs in a cache context and commits
> only on full success; on any unexpected condition it returns an error (halting the block) rather than
> committing partial or silently-wrong state.

---

## 0. Conventions & determinism

### 0.1 Determinism rules (apply to all consensus code)
Consensus state transitions MUST NOT read wall-clock time, randomness, environment variables, or
node-local config, MUST NOT iterate Go maps in range order, MUST NOT use floating point, and MUST NOT
contain unbounded loops. All collection iteration MUST be over a deterministic key order. All
serialization for hashing MUST use the canonical encodings in §0.2.

### 0.2 Encoding primitives (used by all hashing)
- **Hash:** `SHA256` (32-byte output) everywhere.
- **Integers:** unsigned, fixed-width, **big-endian**. `uint32_be(x)` = 4 bytes; `uint64_be(x)` = 8 bytes.
- **`secret`:** exactly 32 bytes.
- **Addresses:** the **raw account address bytes** (e.g. the 20-byte account id), never the bech32
  string — the HRP is presentation. Encoded length-prefixed.
- **Length prefix:** `LP(x) = uint32_be(len(x)) || x` for any byte string `x` (addresses, chain_id, …).
- **Domain separation:** every hash preimage begins with a distinct ASCII domain string (§4.7).
- **Ordering:** any hashed set of per-slot entries is sorted by ascending `slot_id` before encoding.

### 0.3 Glossary (canonical names)
`cumulative_emitted`, `reserved_total`, `settlement_clock`, `last_processed_epoch` (rewards state);
`premine_total` (genesis); `rewards_vault` (= the rewards module account, the only Minter);
`rewards_unallocated` (keyless module account, the LOCKED reserve); `SlotPeriodReward`,
`PayoutPeriodBoundary`, `ExpiryQueue` (rewards records); `Round`, `Selection`, `SelectionExclusion`
(randomness records); `randao_signer_address` (per-Slot signing authority).

---

## 1. Module topology & wiring

### 1.1 Modules
`auth, bank, consensus, x/coreslot, x/randomness (new), x/rewards`. Staking, distribution, gov, mint,
and slashing remain **intentionally omitted** and MUST NOT be wired in. `x/coreslot` is the **only**
source of CometBFT `ValidatorUpdate`s.

### 1.2 Keeper dependency direction (no cycles)
`x/randomness → x/coreslot`; `x/rewards → x/randomness`; `x/rewards → x/coreslot`. `x/randomness` MUST
NOT depend on `x/rewards`. `x/randomness` owns the canonical mining-epoch clock (§3); `x/rewards`
consumes it.

### 1.3 Ordering
- **InitGenesis:** `auth → bank → consensus → coreslot → randomness → rewards`.
- **BeginBlock:** `randomness → rewards`.
- **EndBlock:** `coreslot → rewards`. This ordering is consensus-binding: `x/coreslot`'s validator-set
  and active-status effects MUST be visible to the rewards entitlement-eligibility check in the same
  block. A wiring test MUST assert it.

---

## 2. x/coreslot deltas

`x/coreslot` keeps its existing lifecycle, authority model, key rotation, and validator-update logic.
This spec adds/changes only the following.

### 2.1 Active-slot index (TW-008)
Add `ActiveSlots` as a `KeySet[uint64]` (slot_id). Maintain it transactionally on
activate/inactivate/suspend/remove. Reimplement `GetActiveSlots()` to walk this index in O(active),
preserving its external contract: **ACTIVE only, ascending `slot_id`, byte-identical output** to today.
Rebuild the index in InitGenesis. Invariant: `ActiveSlots == { s.slot_id | s.status == ACTIVE }`.
Classification: **DERIVED-REBUILT** (§8).

### 2.2 `randao_signer_address`
Add `randao_signer_address` to `CoreSlot`. At registration (`MsgRegisterCoreSlot`), if omitted it
defaults to `operator_address`. It MUST be validated as a real account address (reject zero, malformed,
and blocked/module addresses). It is the address authorized to sign RANDAO commit/reveal for the Slot.

**Genesis:** on **FRESH** genesis, a CoreSlot entry that omits `randao_signer_address` has it defaulted
to that Slot's `operator_address`; on **CONTINUATION** import it is always present in the export. In
**both** modes, InitGenesis MUST store every Slot with an explicit, validated, **non-empty**
`randao_signer_address`, and MUST fail closed on an empty/invalid one. Export always emits an explicit
signer for every Slot.

Rotation: `MsgUpdateRandaoSigner{operator_address, slot_id, new_randao_signer_address}` signed by the
Slot **operator**. It takes effect for **future round snapshots only**; any already-open round keeps the
signer frozen in its snapshot (§4.3). New value validated as above.

### 2.3 Genesis authority sanity (TW-003, minimal)
InitGenesis MUST reject a coreslot authority configuration that is empty, malformed, or self-locking
(an unusable/locked-out `validator_authority` or `emergency_authority`). This is the early guardrail;
full authority hardening is §7.

### 2.4 Remaining query pagination (TW-013 remainder)
`CoreSlots` is already paginated. `ActiveCoreSlots`, `PendingKeyRotations`, and `LastAppliedValidators`
still lack a `PageRequest` field and MUST gain one (additive proto change) with honored pagination.
(Tracked as task T0.6; do not reopen the merged CoreSlots fix.)

---

## 3. Canonical epoch clock (x/randomness)

Let `H0 = initial_height` (the chain's first block height) and `L = epoch_length_blocks = 360`.

```
epoch(h) = 1 + floor((h − H0) / L)
start(E) = H0 + (E − 1) * L
end(E)   = start(E) + L − 1
```

**`H0` contract:** `H0` is the `initial_height` supplied for chain initialization and persisted by
`x/randomness` genesis (`State.current_epoch = 1`, `State.current_epoch_start_height = H0`). The **first
`x/randomness` BeginBlock height MUST equal `H0`**, or initialization fails closed. `State` advances
`current_epoch`/`current_epoch_start_height` at each exact `start(E)` boundary. Implementations MUST
validate against integer overflow and MUST use no wall-clock input.

API exposed to `x/rewards`:
```
GetEpochForHeight(h) -> (epoch, startHeight, endHeight, error)
IsEpochEnd(h)        -> (bool, error)     // h == end(epoch(h))
GetCurrentEpoch()    -> (epoch, error)
GetSelection(target) -> (Selection, error)
```
`x/randomness` owns **no** reward policy: `first_reward_epoch` lives in `x/rewards` (§5). The clock is
**never** frozen by `randomness_enabled` (§4.9).

---

## 4. x/randomness

Produces exactly one random winner Slot per mining epoch (or NO_WINNER), via transaction-based RANDAO
commit/reveal by the frozen ACTIVE Slot set, with a winner announced two epochs ahead.

### 4.1 Params (genesis-configurable, runtime-immutable except the emergency flag)
```
epoch_length_blocks        = 360
selection_lookahead_epochs = 2
commit_window_epochs       = 1
reveal_window_epochs       = 1
quorum_numerator           = 2
quorum_denominator         = 3
non_reveal_penalty_epochs  = 4
missed_commit_penalty_epochs = 1
randomness_enabled         = true      // emergency-toggleable (§4.9)
algorithm_version          = 1         // descriptive; the binding version lives in domain strings (§4.7)
```
**Algorithm v1 fixes the pipeline shape:** `selection_lookahead_epochs`, `commit_window_epochs`, and
`reveal_window_epochs` MUST equal `2`, `1`, `1` respectively — Params validation MUST reject any other
values (the `T−4 / T−3 / T−2` pipeline and the one-commit-one-reveal worker assume exactly this). They
remain proto fields so a future protocol version can evolve them. `epoch_length_blocks` (within
`0 < L ≤ MAX_SAFE_EPOCH_BLOCKS`) and the quorum fraction remain **genesis-configurable** — all epoch and
quorum arithmetic is generic in `L` and `numerator/denominator`. All timing params are **runtime-immutable**.
`first_selectable_epoch = 1 + selection_lookahead_epochs + commit_window_epochs + reveal_window_epochs`
(= 5 with defaults) is a derived, exposed randomness-timing fact.

**Genesis param validation (MUST reject otherwise):** `algorithm_version == 1`;
`selection_lookahead_epochs == 2`, `commit_window_epochs == 1`, `reveal_window_epochs == 1`;
`0 < epoch_length_blocks ≤ MAX_SAFE_EPOCH_BLOCKS`; `quorum_denominator > 0` and
`0 < quorum_numerator ≤ quorum_denominator`; `non_reveal_penalty_epochs ≥ 1`,
`missed_commit_penalty_epochs ≥ 1`. Consensus arithmetic MUST be overflow-safe: `quorum_numerator * N`
(N = participant count) and `participant_count`/`valid_reveal_count` MUST fit `uint32` (a snapshot larger
than `uint32` max is a genesis/lint error).

### 4.2 Pipeline
For mining target `T`: commit window at epoch `T−4`, reveal window at `T−3`, finalize at the first
block of `T−2` (announced ~2 epochs ahead), mined at `T`. During current epoch `E` the worker targets
`commit=E+4, reveal=E+3`. Bootstrap: the first mineable target is `first_selectable_epoch`.

### 4.3 Participant snapshot
At the first block of commit epoch `E` (= target `E+4`), snapshot the ACTIVE Slot set via
`GetActiveSlots()`. Each entry:
```
RandaoParticipant { slot_id, operator_address, randao_signer_address }
```
frozen for the round's lifetime. Authorization for commit/reveal uses the **snapshotted**
`randao_signer_address`, not live CoreSlot state. `participant_count = |snapshot|`.
While `randomness_enabled == false`, no new snapshot is taken (§4.9).

### 4.4 Commit — `MsgCommitRandomness{signer, target_epoch, commitment}`
Signer MUST equal the snapshot's `randao_signer_address` for the Slot. **Validation order (deterministic):**
(1) the round exists **and `Round[target].status == OPEN`** — a `CANCELLED_BY_PAUSE` or `FINALIZED` round
deterministically rejects the message; (2) `current == target−4`; (3) the Slot is in the snapshot;
(4) signer matches the snapshotted `randao_signer_address`; (5) `len(commitment) == 32`; (6) duplicate
check: if a commitment is already recorded for `(target, slot)`, a **different** value is an equivocation
error (rejected; the first stands) and an **identical** value is idempotent success. Store
`Commitments[(target, slot_id)] = commitment`.

Commitment construction (Algorithm v1):
```
commitment = SHA256("twilight-randao-commit-v1" || LP(chain_id)
                    || uint64_be(target_epoch) || uint64_be(slot_id) || secret[32])
```

### 4.5 Reveal — `MsgRevealRandomness{signer, target_epoch, secret}`
**Validation order (deterministic):** (1) the round exists **and `Round[target].status == OPEN`**
(reject `CANCELLED_BY_PAUSE`/`FINALIZED`); (2) `current == target−3`; (3) a commitment exists for
`(target, slot)`; (4) signer matches the snapshotted `randao_signer_address`; (5) `len(secret) == 32`;
(6) recomputed commitment (§4.4) equals the stored commitment; (7) duplicate check: no reveal recorded —
divergent second reveal rejected, identical re-submission idempotent. An invalid reveal (commitment
mismatch) is not counted (treated as non-reveal); the commitment remains. Store
`Reveals[(target, slot_id)] = secret`.

### 4.6 Finalization (BeginBlock at `start(T−2)`)
`Selection[T]` MUST NOT already exist; finalization writes it **exactly once** (a pre-existing
`Selection[T]` is a consensus error). Every finalized round records a `selection_outcome`; §4.10 gives
the exact **field-presence matrix** per outcome. Order for target `T`:
1. If `Round[T].status == CANCELLED_BY_PAUSE` (§4.9): write `Selection[T]` with
   `outcome = NO_WINNER_RANDOMNESS_PAUSE` (minimal fields per §4.10), no seed, no penalties; `Round[T]`
   keeps its terminal `CANCELLED_BY_PAUSE` status. Go to pruning (§4.11).
2. `Round[T].status` MUST be `OPEN`. Compute `participant_set_hash` and `participant_count`/
   `commit_count`/`reveal_count` from the snapshot (§4.7). Collect valid reveals `R` (snapshot member
   with a stored reveal matching its commitment).
3. **Reveal quorum:** require `|R| ≥ floor(quorum_numerator * N / quorum_denominator) + 1` where
   `N = participant_count` (the **full** frozen ACTIVE snapshot, including reward-excluded Slots).
   There is no separate commit quorum (commit count is monitoring only). If quorum fails →
   `outcome = NO_WINNER_NO_QUORUM` (no `reveal_set_hash`, no `seed`); apply penalties (§4.8), write
   `Selection[T]`, set `Round[T].status = FINALIZED`, prune.
4. Compute `reveal_set_hash` and `seed` (§4.7).
5. **Candidates** = snapshot members that produced a valid reveal AND are not under an active selection
   exclusion (§4.8, inclusive predicate), sorted ascending `slot_id`. If empty →
   `outcome = NO_WINNER_NO_CANDIDATES` (seed recorded). Otherwise sample (§4.7): a winner →
   `outcome = WINNER`; rehash exhaustion → `outcome = NO_WINNER_REHASH_EXHAUSTED`. Consecutive wins allowed.
6. Apply penalties (§4.8). Write `Selection[T]` with its `outcome` and fields per §4.10; **set
   `Round[T].status = FINALIZED` after writing the Selection**. Prune (§4.11).

There is no fallback seed, no block-hash fallback, no authority-selected winner, and no reroll.

### 4.7 Randomness Algorithm v1 — byte contract (consensus-critical)
Domains (ASCII, exact): `twilight-randao-commit-v1`, `twilight-randao-participants-v1`,
`twilight-randao-reveals-v1`, `twilight-randao-seed-v1`, `twilight-randao-sample-v1`. The active
algorithm version is fixed by these domain strings; `algorithm_version` param is descriptive metadata
and MUST agree with the deployed domain suffix.

```
participant_entry = uint64_be(slot_id) || LP(operator_raw) || LP(randao_signer_raw)
participant_set_hash = SHA256(
    "twilight-randao-participants-v1" || LP(chain_id) || uint64_be(target_epoch)
    || uint32_be(participant_count) || concat(participant_entry sorted by slot_id) )

reveal_entry = uint64_be(slot_id) || secret[32]
reveal_set_hash = SHA256(
    "twilight-randao-reveals-v1" || LP(chain_id) || uint64_be(target_epoch)
    || uint32_be(valid_reveal_count) || concat(reveal_entry sorted by slot_id) )

seed = SHA256(
    "twilight-randao-seed-v1" || LP(chain_id) || uint64_be(target_epoch)
    || participant_set_hash[32] || reveal_set_hash[32] )
```
`reveal_set_hash` is stored in the finalized `Selection` (it has precise consensus meaning as the
canonical reveal digest).

**Winner selection (unbiased rejection sampling)** over `n = |candidates|`:
```
for counter in 0 .. 127 (inclusive):
    sample_hash = SHA256("twilight-randao-sample-v1" || seed[32] || uint32_be(counter))
    sample      = uint256_be(sample_hash)            // 32 bytes, big-endian
    limit       = floor(2^256 / n) * n
    if sample < limit:
        winner_index = sample mod n                  // index into candidates sorted ascending slot_id
        return candidates[winner_index]
return NO_WINNER                                       // all 128 counters rejected (astronomically rare)
```

### 4.8 Penalties & exclusions
`SelectionExclusion{slot_id, excluded_until_epoch, reason, source_epoch}`. At finalization of `T`
(when not cancelled by pause):
- Snapshot member with **no commit** for `T` → exclusion penalty `missed_commit_penalty_epochs` (=1).
- Snapshot member that committed but produced **no valid reveal** → penalty `non_reveal_penalty_epochs`
  (=4).
- Stacking: `excluded_until_epoch = max(existing_excluded_until, T) + penalty_epochs`; on stacking,
  the record's `reason` and `source_epoch` are updated to the **latest** violation (`source_epoch = T`).

**Active-exclusion predicate (inclusive):** a Slot is excluded for target `T'` **iff
`T' ≤ excluded_until_epoch`**. (E.g. a single missed commit at `T` sets `excluded_until = T+1`, i.e.
excluded through `T+1` inclusive = one future epoch; commit-without-reveal sets `T+4` = four.) An
excluded Slot still contributes to the seed and counts in the quorum denominator; it merely cannot be a
*candidate* (win) while excluded. **Last-revealer grinding is an accepted testnet weakness** — the
deterrents are the stacking penalty and PoA suspension by `validator_authority`; the mainnet path is
threshold BLS (out of scope here). **Selection eligibility** (snapshot member + valid reveal + not
excluded) and **entitlement eligibility** (§5.6) are distinct and MUST NOT be collapsed.

### 4.9 Emergency pause — `randomness_enabled` and `CANCELLED_BY_PAUSE`
`MsgSetRandomnessEnabled{emergency_authority, enabled}` toggles the flag. The pause **never** freezes
the epoch clock (§3) or the monetary clock (§5). A round is `CANCELLED_BY_PAUSE` iff its commit **or**
reveal window intersects any block during which `randomness_enabled == false`. Two mechanisms, both
required:

1. **In the pause tx handler** (on `true → false`): immediately mark every round currently within its
   commit or reveal window as `CANCELLED_BY_PAUSE`. (This is essential: a pause+resume within the same
   block — e.g. during a reveal window's final block — would be invisible to any later BeginBlock that
   sees the flag already `true`.)
2. **Each randomness BeginBlock while `randomness_enabled == false`:** ensure the current commit-target
   and reveal-target rounds are represented as `CANCELLED_BY_PAUSE` where their window intersects the
   pause, and **do not snapshot participants for a new commit round**.

A target whose commit window begins entirely during the pause gets a **placeholder**:
```
Round[T] { target_epoch: T, status: CANCELLED_BY_PAUSE, cancellation_reason: RANDOMNESS_PAUSE,
           no snapshot, no commitments, no reveals }
```
Absence is not sufficient consensus evidence (it cannot be distinguished from a state bug). On
`false → true`, cancellation is **never** undone, and later commit/reveal messages against a cancelled
round are rejected by the `status == OPEN` guard (§4.4/§4.5). At finalize,
`CANCELLED_BY_PAUSE ⇒ Selection[T].outcome = NO_WINNER_RANDOMNESS_PAUSE`, no seed, **no penalties**
(honest Slots are not punished for a pause). The set of live rounds is bounded by the pipeline (~2–4),
so all of this is bounded work — no global scan. Cancelled rounds (incl. placeholders) survive
export/import.

### 4.10 State / collections
```
Params
State { current_epoch, current_epoch_start_height }     // NO previous_seed / initial_randomness_seed
Round[target_epoch] { status: OPEN|CANCELLED_BY_PAUSE|FINALIZED, cancellation_reason?, snapshot_ref? }
RoundSnapshots[target_epoch]         // RandaoParticipant list (pruned after finalize)
Commitments[(target_epoch, slot_id)] // pruned after finalize
Reveals[(target_epoch, slot_id)]     // pruned after finalize
Selections[target_epoch]             // compact, retained
SelectionExclusions[slot_id]
```
```
Selection {
    target_epoch, snapshot_height, participant_set_hash, participant_count,
    commit_count, reveal_count, reveal_set_hash, seed,
    winner_slot_id, selection_outcome, finalized_height, algorithm_version
}
selection_outcome ∈ { WINNER, NO_WINNER_NO_QUORUM, NO_WINNER_NO_CANDIDATES,
                      NO_WINNER_REHASH_EXHAUSTED, NO_WINNER_RANDOMNESS_PAUSE }
```
**Field-presence matrix** (SET = populated; ∅ = zero/empty). All outcomes set `target_epoch`,
`selection_outcome`, `finalized_height`, `algorithm_version`. `counts` = `participant_count`,
`commit_count`, `reveal_count`:
```
outcome                      | snapshot_height,counts | participant_set_hash | reveal_set_hash | seed | winner_slot_id
WINNER                       | SET                    | SET                  | SET             | SET  | SET
NO_WINNER_NO_CANDIDATES      | SET                    | SET                  | SET             | SET  | ∅
NO_WINNER_REHASH_EXHAUSTED   | SET                    | SET                  | SET             | SET  | ∅
NO_WINNER_NO_QUORUM          | SET                    | SET                  | ∅               | ∅    | ∅
NO_WINNER_RANDOMNESS_PAUSE   | ∅                      | ∅                    | ∅               | ∅    | ∅
```
`has_winner ≡ (selection_outcome == WINNER)`. A `CANCELLED_BY_PAUSE` round's Selection is minimal
regardless of whether a snapshot had been taken (any snapshot data is pruned).

### 4.11 Pruning & retention
After finalization the module deterministically deletes `RoundSnapshots[T]`, `Commitments[T,*]`,
`Reveals[T,*]`, keeping the compact `Selection[T]` (and the `Round[T]` status). `Selections` are
retained indefinitely for this testnet (they are the explorer's history). Transaction history in blocks
allows full reconstruction. See the classification table (§8).

### 4.12 Events & queries
Events: `randao_round_opened`, `randao_round_cancelled_by_pause`, `randao_commit_recorded`,
`randao_reveal_recorded`, `randao_selection_finalized`, `randao_no_quorum`,
`randao_non_reveal_violation`, `randao_missed_commit_violation`, `randao_selection_exclusion_applied`.
Queries: `Params`, `CurrentEpoch`, `Round(target)`, `Selection(target)`, `UpcomingSelections`,
`SelectionExclusion(slot_id)`. CLI: `twilightd tx randomness commit|reveal …`; `twilightd query
randomness params|current-epoch|round|selection|upcoming-selections|exclusion …`. No raw user activity
enters this module.

### 4.13 Golden vectors
Seed and winner computation carry fixed golden vectors that are consensus regression tests. Vectors MUST
be produced by an **independent oracle** (a standalone script or hand-computed), never generated from the
implementation under test, and MUST expose every intermediate byte string and digest: participant
entries, `participant_set_hash`, each commitment, reveal entries, `reveal_set_hash`, `seed`, each
`sample_hash`/`sample`, and the final winner.

---

## 5. x/rewards

Single monetary counter, eager epoch minting to the vault, three-way RESERVED/AVAILABLE/LOCKED
accounting, transfer-only payouts, per-`(slot,period)` entitlements with a settlement-clock deadline.

### 5.1 Params
```
native_denom              = "utwlt"          // immutable
max_supply                = 21,000,000 TWLT in utwlt  // immutable
initial_block_subsidy
halving_mode
first_reward_epoch                          // >= first_selectable_epoch (validated); default 5
payout_period_epochs      = 48
settlement_window_epochs  = 48
max_payouts_per_chunk                        // interim non-production until T6.3 load test
max_chunks_per_batch                         // interim non-production until T6.3 load test
gas_per_reward_recipient                     // interim non-production until T6.3 load test
emissions_enabled                            // pause flag
entitlements_enabled                         // pause flag
payouts_enabled                              // pause flag
```
`MAX_SAFE_EPOCH_BLOCKS = 17280` bounds `epoch_length_blocks` (TW-007). **Structural economic params are
runtime-immutable in v3** — `native_denom`, `max_supply`, `initial_block_subsidy`, `halving_mode`,
`first_reward_epoch`, `payout_period_epochs`, `settlement_window_epochs`, `max_payouts_per_chunk`,
`max_chunks_per_batch`, and `gas_per_reward_recipient` MUST NOT change after genesis (no `UpdateParams`
path may reach them). Only the three emergency flags are mutable, via their dedicated messages (§5.12).
`economic_authority` owns Treasury spending (§5.11) and is the designated authority for any *future*
economic-update mechanism, but **v3 defines no live mutation of structural economic parameters** — this
prevents changing settlement windows or payout caps underneath existing records; a later protocol
version MAY add effective-epoch parameter updates. `premine_total` is **not** a param (§5.3). Removed
legacy params: `distribution_method`, `remainder_policy`, `weighted_rewards_enabled`,
`max_claim_epochs_per_tx`, and all carry-forward settings.

**Genesis param validation (MUST reject otherwise):** `native_denom == "utwlt"`; `max_supply` = the
fixed 21M-TWLT base-unit value; `initial_block_subsidy > 0`; `halving_mode == SUPPLY_THRESHOLD` (only v3
mode); `first_reward_epoch ≥ first_selectable_epoch`; `0 < payout_period_epochs`,
`0 < settlement_window_epochs`; `0 < max_payouts_per_chunk`, `0 < max_chunks_per_batch`,
`0 < gas_per_reward_recipient`; the product `max_payouts_per_chunk × gas_per_reward_recipient` MUST be
overflow-safe and `≤ per_tx_gas_cap ≤ block.max_gas`; deadline arithmetic
(`settlement_start_clock + settlement_window_epochs`) MUST be overflow-safe.

### 5.2 State
```
RewardsState {
    cumulative_emitted     // drives halving; = premine_total at fresh genesis
    reserved_total         // O(1) live-entitlement liability counter
    settlement_clock       // uint64; +1 per mining-epoch close while payouts_enabled; frozen while paused
    last_processed_epoch   // double-mint guard
}
```
Records: `SlotPeriodReward[(slot_id, payout_period)]`, `PayoutPeriodBoundary[payout_period]`,
`EpochEmission[epoch]` (immutable history), `ExpiryQueue` (DERIVED, §5.9), and pending
`eligible_at_start` selection-consumption state (§5.6).

```
EpochEmission {                              // written once per epoch E >= first_reward_epoch; immutable
    epoch, start_height, end_height,
    emission_amount,
    selection_outcome,                       // mirrors Selection.selection_outcome (§4.10)
    winner_slot_id, winner_operator_address,  // valid only when selection_outcome == WINNER
    eligible_at_start, eligible_at_end, entitlement_created,
    disposition,                             // NONE | RESERVED | LOCKED
    emissions_enabled_at_finalize,           // bool; disambiguates the NONE cause
    payout_period,
    cumulative_emitted_after
}
// disposition: RESERVED = minted to a winner's entitlement; LOCKED = minted to rewards_unallocated;
//              NONE = no mint (emission_amount == 0), cause via emissions_enabled_at_finalize:
//                     false = emissions paused; true = natural zero emission (halving curve exhausted).
// AVAILABLE is not an emission-time disposition (forfeiture/expiry reclassification lives on the
// SlotPeriodReward). EpochEmission is written ONCE per epoch E >= first_reward_epoch, INCLUDING NONE epochs.
```

### 5.3 Genesis: `premine_total`, FRESH vs CONTINUATION
`GenesisState` carries an explicit **mode discriminator** `FRESH | CONTINUATION` (not inferred) and a
genesis-only, runtime-**immutable** field `premine_total` = **all** TWLT existing in bank state at fresh
genesis (team + investor allocations + any explicit initial AVAILABLE-treasury allocation into the
vault). No runtime path may mutate `premine_total` (it joins `native_denom`, `max_supply`).

```
ALWAYS (both modes):
    bank_supply == cumulative_emitted
    cumulative_emitted <= max_supply
    reserved_total == Σ(entitlement − total_paid) over live (ACCRUING|OPEN) records   // recomputed & verified

FRESH only:
    bank_supply == premine_total == cumulative_emitted
    reserved_total == 0
    settlement_clock == 0

CONTINUATION:
    (does NOT require cumulative_emitted == premine_total — cumulative grows past premine)
```
Premine counts inside `max_supply` and advances the halving curve
(`cumulative_emitted(genesis) = premine_total`, remaining issuance `= max_supply − premine_total`).

### 5.4 Vaults & three-way accounting
- **`rewards_vault`** = the rewards module account; the **only** account with `Minter`. Its balance is
  partitioned logically: `RESERVED = reserved_total`; `AVAILABLE = rewards_vault_balance − reserved_total`.
- **`rewards_unallocated`** = a second module account **registered via appconfig/module-account
  permissions with an explicitly empty permission list** (no `Minter`/`Burner`/`Staking`), and **no
  spend message** (the LOCKED reserve). No handler, EndBlocker, or upgrade handler may move funds out of
  it during this testnet; a test MUST assert this.

### 5.5 Eager minting & emission routing (rewards EndBlock at `end(E)`, `E ≥ first_reward_epoch`)
```
if emissions_enabled == false:
    E_emit = 0                                            // paused: no mint/send/accrual this epoch
else:
    E_emit = ComputeEpochEmission(cumulative_emitted, epoch_length_blocks, halving_mode,
                                  max_supply, initial_block_subsidy)   // canonical configured L (§3/§4.1)
if E_emit > 0:
    assert cumulative_emitted + E_emit <= max_supply      // fail-closed, before mint
    MintCoins(rewards_vault, E_emit)
    cumulative_emitted += E_emit
    if winner is entitlement-eligible (§5.6):
        entitlement[(slot, period)] += E_emit ; reserved_total += E_emit          // RESERVED
    else:
        SendCoinsFromModuleToModule(rewards_vault → rewards_unallocated, E_emit)  // LOCKED
// E_emit == 0 (paused OR halving curve exhausted): NO mint, NO send, NO accrual; entitlement_created =
//   false; EpochEmission.disposition = NONE, cause disambiguated by emissions_enabled_at_finalize (§5.2).
```
`ComputeEpochEmission`/`HalvingTier`/`NextBlockSubsidy` are reused, fed the single counter, the
**canonical configured `epoch_length_blocks`** (the same `L` the epoch clock uses, §3/§4.1 — **never a
hard-coded 360**), and the immutable `halving_mode`. **The only `halving_mode` accepted in v3 is
`SUPPLY_THRESHOLD`** (Bitcoin-like supply-threshold halving); genesis MUST reject any other value.
`emissions_enabled == false` freezes the mint (⇒ zero entitlement automatically; no catch-up).

**Routing table** — RESERVED for a valid winner; AVAILABLE only via operator-caused forfeiture/expiry;
LOCKED for every authority-influenceable non-payout:

| Event | Destination |
|---|---|
| Valid winner (ACTIVE@start & ACTIVE@end, `entitlements_enabled`) | RESERVED |
| Operator voluntarily leaves remainder / lets batch expire | AVAILABLE Treasury |
| NO_WINNER (no quorum / zero candidates / rehash exhaustion) | LOCKED |
| Randomness paused/failed | LOCKED |
| `entitlements_enabled == false` | LOCKED |
| Winner ineligible at accrual (`ACTIVE@start` or `ACTIVE@end` false) | LOCKED |

### 5.6 Entitlement eligibility & accrual
At `x/rewards` BeginBlock of `start(E)`, for the finalized winner of `E`, write a **persisted**
`PendingEligibility` record (keyed by mining epoch `E`; at most one):
```
PendingEligibility[epoch] { epoch, winner_slot_id, winner_operator_address, eligible_at_start }
```
It MUST be exported (it cannot be reconstructed after a continuation import). At `end(E)`, entitlement
eligibility = `eligible_at_start && ACTIVE@end && entitlements_enabled`; the record is **consumed and
deleted** at `end(E)` (step 2 of §5.7). If the finalized `Selection` for `E` has a winner but
`PendingEligibility[E]` is missing at `end(E)`, that is a consensus error (fail closed). On the winner
path, accrue into the merged `SlotPeriodReward` record and snapshot the operator identity at accrual
(immutable across the period).

```
SlotPeriodReward {
    slot_id, payout_period, operator_address,    // operator snapshotted at accrual
    entitlement, total_paid, next_chunk_index, distribution_hash,
    status: ACCRUING | OPEN | FINALIZED | EXPIRED,
    opened_height, finalized_height
}
```
On first accrual the record is created `ACCRUING` and immediately enrolled in the `ExpiryQueue` (§5.9).
Only one winner per epoch → O(1) update.

### 5.7 Payout-period mapping, settlement clock & the close-block order
For `E ≥ first_reward_epoch`:
```
payout_period(E) = 1 + floor((E − first_reward_epoch) / payout_period_epochs)
period_start(P)  = first_reward_epoch + (P − 1) * payout_period_epochs
period_end(P)    = period_start(P) + payout_period_epochs − 1
```
Epochs before `first_reward_epoch` have no payout period and mint zero.
`deadline_clock(P) = PayoutPeriodBoundary[P].settlement_start_clock + settlement_window_epochs`.

Exact **close-block order** at rewards EndBlock for `end(E)`:
1. `x/coreslot` EndBlock has already run (§1.3).
2. Determine `ACTIVE@end` / entitlement eligibility (§5.6).
3. Finalize monetary emission for `E`.
4. Mint `E_emit` and route to RESERVED or LOCKED (§5.5).
5. If `E` is the last epoch of its period `P`, close `P`.
6. If `payouts_enabled`, increment `settlement_clock` by 1.
7. If `P` closed, capture `PayoutPeriodBoundary[P].settlement_start_clock` = the **POST-increment**
   `settlement_clock` value from step 6.
8. Process bounded due expiry (§5.9) using the resulting clock.
9. Update `last_processed_epoch = E`.

**POST-increment (step 7) is required** so a new settlement period gets a full `settlement_window_epochs`
of enabled epoch-closes. *Worked example (window 48):* let the counter be `C` before `end(E_P)`; step 6
→ `C+1`; capture `start=C+1`; `deadline=C+49`; payment allowed while `settlement_clock < C+49`, i.e. for
clock values `C+1..C+48` = 48 enabled epoch-closes. PRE-increment would yield 47 (one short).

**Same-block ordering:** all transactions in a block (payout chunks, finalize, treasury spend) execute
during transaction processing, which **precedes** rewards EndBlock; a chunk paid in block `b` is
therefore reflected in `total_paid`/`reserved_total` before the EndBlock expiry step (step 8) of the
same block runs.

### 5.8 SlotPeriodReward state machine
```
ACCRUING --(MsgOpenRewardBatch, period closed, window open)--> OPEN
OPEN     --(MsgFinalizeRewardBatch)-------------------------> FINALIZED   (release remainder → AVAILABLE)
ACCRUING|OPEN --(expiry processor at deadline)-------------> EXPIRED     (release remainder → AVAILABLE)
```
`FINALIZED` and `EXPIRED` are **both terminal** and both mean the remaining reserve has already been
released to AVAILABLE. There is **no separate `RELEASED` state**. Release-exactly-once is guaranteed by
guarding the reserve decrement on the terminal transition. **Past-deadline rejection uses the derived
predicate `settlement_clock ≥ deadline_clock(P)`, NOT the stored status** (the bounded processor sets
`EXPIRED` lazily, so a past-deadline record may still be stored `ACCRUING|OPEN` briefly). Queries MAY
display `EXPIRED` for such a record.

### 5.9 Expiry — derived `ExpiryQueue`, no cursor
`ExpiryQueue = KeySet[(payout_period, slot_id)]`, classification **DERIVED** (not exported). Enroll
`(P, slot)` at first accrual (so never-opened `ACCRUING` records are covered). At the expiry step (§5.7
step 8; `MAX_EXPIRY_RELEASES_PER_BLOCK = 1`, a locked policy constant):
```
key = smallest ExpiryQueue key
if none: stop
P = key.payout_period
if PayoutPeriodBoundary[P] absent: stop                  // period not yet closed
if settlement_clock < deadline_clock(P): stop            // not yet due
rec = SlotPeriodReward[key]
require rec.status in {ACCRUING, OPEN}                    // else stale-index error (fail-closed)
remaining = rec.entitlement - rec.total_paid
reserved_total -= remaining
rec.status = EXPIRED
delete ExpiryQueue[key]
```
Period-order equals deadline-order because `deadline_clock(P)` is monotonic non-decreasing in `P`
(`P1<P2 ⇒ settlement_start_clock(P1) ≤ settlement_start_clock(P2)`; ties are possible during a long
pause and remain deterministic). On explicit finalize, delete the queue entry too. On import, **rebuild**
the queue from every `ACCRUING|OPEN` record and verify a one-to-one correspondence. There is **no expiry
cursor**; deletion makes the smallest remaining key the resume position.

### 5.10 Payout (transfer-only)
Each `SlotPeriodReward` is keyed `(slot_id, payout_period)`; **an operator alone does not identify the
record** — after Slot removal + re-registration an operator can hold records for multiple `slot_id`s in
the same period. All three messages therefore carry `slot_id`, and the handler MUST load
`SlotPeriodReward[(slot_id, payout_period)]` and require its snapshotted `operator_address == signer`.
- **`MsgOpenRewardBatch{operator, slot_id, payout_period, distribution_hash}`** — require the record
  exists, `rec.operator_address == operator`, period closed, settlement window open (derived),
  `distribution_hash` empty or 32 bytes; `ACCRUING→OPEN`.
- **`MsgSubmitRewardChunk{operator, slot_id, payout_period, chunk_index, payouts[]}`** — cache context:
  ```
  rec = SlotPeriodReward[(slot_id, payout_period)]            // must exist
  require rec.operator_address == operator
  require rec.status == OPEN and NOT past_deadline (derived: settlement_clock >= deadline_clock(P))
  require chunk_index == rec.next_chunk_index and rec.next_chunk_index < max_chunks_per_batch
  validate payouts: 0 < count <= max_payouts_per_chunk;
                    recipient order STRICTLY INCREASING over raw account-address bytes (unique on that
                      representation) — NEVER over bech32 text;
                    reject blocked/module recipients (incl. rewards_vault, rewards_unallocated);
                    every amount > 0 in utwlt
  chunk_total = Σ amounts ; require rec.total_paid + chunk_total <= rec.entitlement
  ConsumeGas(count * gas_per_reward_recipient)
  require rewards_vault_balance >= chunk_total                  // holds by construction
  SendCoinsFromModuleToAccount(rewards_vault → each recipient)  // NO MintCoins on this path
  rec.total_paid += chunk_total ; reserved_total -= chunk_total ; rec.next_chunk_index += 1
  ```
  Any transfer failure rolls back the entire chunk (no partial settlement). Minting is off the fan-out
  path entirely (it happened once at §5.5).
- **`MsgFinalizeRewardBatch{operator, slot_id, payout_period}`** — require record + `operator` match +
  `status == OPEN` + NOT past-deadline; `OPEN→FINALIZED`; release `remaining = entitlement − total_paid`
  to AVAILABLE (`reserved_total −= remaining`); release-once.

Caps (`max_payouts_per_chunk`, `max_chunks_per_batch`, `per_tx_gas_cap`, `block.max_gas`) satisfy
`max_payouts_per_chunk × gas_per_reward_recipient ≤ per_tx_gas_cap ≤ block.max_gas`; final values come
from load testing (T6.3) and replace the interim non-production values; no `min_reward_payout_amount`
unless load testing proves the fan-out cap insufficient.

### 5.11 Treasury spend & authority roles
Three named roles: `validator_authority` (coreslot lifecycle), `economic_authority` (`MsgSpendTreasury`;
designated for any *future* economic-update mechanism — no live structural mutation in v3, §5.1),
`emergency_authority` (pause/resume). **Authoritative ownership by module:** `x/coreslot` owns
`validator_authority` **and** `emergency_authority` (their state and their `PendingAuthorityTransfer`);
`x/rewards` owns `economic_authority` (and its pending transfer). `x/randomness` and `x/rewards` both
**read `emergency_authority` through `CoreSlotKeeper`** — coreslot is not turned into a global economic
registry. All three pending transfers (validator + emergency in coreslot; economic in rewards) are
exported/imported. Roles MAY resolve to one controllable address at genesis but are separate protocol
interfaces (TW-014 STRUCTURALLY PREPARED, not closed — the production linter enforces separation/multisig
for hardened deployment). Authority addresses MUST be real controllable addresses; module-derived
(keyless) addresses are prohibited.

`MsgSpendTreasury{economic_authority, recipient, amount, purpose}` — `amount` is a **single positive
`utwlt` coin** with `0 < amount ≤ available_treasury` (O(1): `rewards_vault_balance − reserved_total`);
recipient validated as in §5.10 (cannot reach `rewards_unallocated`); it is a transfer (no supply change).

**Two-step authority rotation** (one-step is prohibited — it reintroduces TW-003 lockout):
```
MsgProposeAuthorityTransfer{role, new_authority}  // current holder only; creates OR replaces the
                                                  // pending proposal (event: old_pending, new_proposed)
MsgAcceptAuthorityTransfer{role}                  // proposed authority only; commits the transfer
MsgCancelAuthorityTransfer{role}                  // current holder only; clears the pending proposal
```
**Acceptance MUST atomically verify** (else reject, fail-closed): a `PendingAuthorityTransfer` for
`role` exists; the signer equals `pending.proposed`; and **`pending.current == the live authority`** — a
stale proposal whose recorded `current` no longer matches the live holder is rejected (important for
continuation-import validation). On success the live authority becomes `pending.proposed` and the
pending record is deleted in the same transition. Validation before proposal: `new_authority` valid,
non-empty, not a module account, not equal to the current holder. `PendingAuthorityTransfer{role,
current, proposed}` is exported/imported.

### 5.12 Emergency flags (distinct semantics)
```
emissions_enabled  = false → mint freezes; cumulative_emitted frozen; no catch-up ⇒ no entitlement (auto)
entitlements_enabled = false → E minted, routed to LOCKED; winner gets no reservation
payouts_enabled    = false → block MsgOpenRewardBatch, MsgSubmitRewardChunk, MsgFinalizeRewardBatch;
                             settlement_clock frozen; expiry deferred (§5.9 stops on frozen clock)
randomness_enabled = false → §4.9 (never freezes the epoch/monetary clock)
```
Blocking `MsgFinalizeRewardBatch` while payouts are paused prevents irreversible voluntary forfeiture
during an emergency freeze; because the clock is frozen, no deadline is lost. **Resume-only recovery:**
`economic_authority` MAY transition `payouts_enabled: false → true` (it **cannot** pause), recovering a
stuck payouts pause without a fourth role. `emergency_authority` owns full pause/resume.

### 5.13 Invariants (authoritative — the bank module is authoritative for total supply)
```
bank_supply == cumulative_emitted
cumulative_emitted <= max_supply
reserved_total <= rewards_vault_balance
available_treasury = rewards_vault_balance - reserved_total
reserved_total == Σ(entitlement - total_paid) over live (ACCRUING|OPEN) records   // backstop; crisis/test only
rewards_unallocated balance is monotonically non-decreasing; no message spends it
```
There is **no** provenance-reconstruction invariant (`x/rewards` does not sum outside-holder balances).

### 5.14 Events & queries
Events: `reward_epoch_emitted`, `reward_entitlement_accrued`, `reward_locked_unallocated`,
`reward_batch_opened`, `reward_chunk_paid`, `reward_batch_finalized`, `reward_entitlement_expired`,
`treasury_spent`, `authority_transfer_proposed|accepted|cancelled`.
Queries: `EmissionState` (cumulative/reserved/available/unallocated), `EpochEmission(epoch)`,
`SlotPeriodReward(slot,period)` — the single record holding accrual, batch, and terminal state (with
derived `EXPIRED` display); `PendingAuthorityTransfer(role)`. CLI: `twilightd tx rewards
open-batch|submit-chunk|finalize-batch|spend-treasury|propose-authority-transfer|accept-authority-transfer|cancel-authority-transfer …`;
`twilightd query rewards emission-state|epoch-emission|slot-period-reward|pending-authority-transfer …`.
There is **no** separate `RewardBatch` query (entitlement and batch are one record). Consensus emits
only **raw** facts — the
`reward_chunk_paid` event carries `chunk_recipient_count` (per chunk; recipients within a chunk are
unique by validation) plus the recipient list, amounts, and operator identity. All cross-period
aggregation (unique recipients, self-pay share, top-k, Gini) is derived **off-chain** in the
explorer/indexer.

---

## 6. RANDAO worker (off-consensus runtime)

An in-process `RandaoWorker` (package `internal/randao/…`) automates commit/reveal. It MUST NOT be
started from a keeper, and its failure MUST NEVER halt consensus or block CometBFT startup — the only
consequence of worker failure is a missing randomness contribution.

- Generate a 32-byte `crypto/rand` secret and **persist it before broadcasting** the commitment
  (atomic write, 0600, fsync, atomic rename). Persisted secrets are validator **recovery material**
  once a commit confirms (cover in the recovery runbook).
- Submit **one combined tx per epoch**: `MsgCommitRandomness(target=E+4) + MsgRevealRandomness(target=E+3)`
  (one sequence bump / ~30 min); bootstrap epoch 1 is commit-only. Compose from **current chain state**;
  if the combined tx cannot succeed whole, **prioritize preserving the reveal**.
- Idempotent duplicate = success; divergent second commitment = equivocation error. **Do not submit
  while the node is catching up** (act only when synced).
- Sequence handling: worker-local mutex, fresh-sequence-before-sign, mismatch → rebuild → retry.
- **Signing key:** sign with the Slot's authorized `randao_signer` key (CORE-005). Default = operator
  key until a distinct signer is registered. Because payout and RANDAO traffic are concurrent by design,
  a dedicated signer removes shared-sequence contention. On the shared operator-signer path, the payout
  automation MUST observe the epoch's combined RANDAO tx confirmed before starting payout chunks (a
  worker mutex cannot serialize a separate payout process).
- Expose health/metrics so operators detect non-reveal risk before the deadline.

---

## 7. Security & feeless controls

- **TW-004 — finite `block.max_gas`** (placeholder early; final from load testing, T6.3).
- **TW-005 — feeless anti-spam is a deterministic mempool/proposal design, not a single AnteHandler.**
  Split: (a) transaction-local ante bounds (max gas wanted, message count, MultiSend outputs, message
  complexity, per-tx gas cap); (b) node-local mempool fairness that MUST NOT affect block validity;
  (c) deterministic proposal/block fairness via `PrepareProposal`/`ProcessProposal` — consensus-critical,
  with multi-proposer app-hash agreement tests.
- **TW-006 — account-growth controls** (bank `SendRestriction` + MultiSend cap). The first-funding
  restriction exemption applies to **all sends originating from `rewards_vault`** — both payout chunks
  **and** Treasury (`MsgSpendTreasury`) transfers — so the restriction needs no message-context
  sensitivity. Treasury sends remain single-recipient, authority-gated, gas-bounded, and
  module-address-validated; payouts rely on caps + gas charging + finite block gas + module-address
  validation + atomic cache context.
- **TW-007 — bounded epoch emission** (`MAX_SAFE_EPOCH_BLOCKS = 17280`).
- **TW-008 — active-slot index** (§2.1). **TW-010 — supply reconciliation** via premine (§5.3).
- **TW-012 — recipient/module-address validation** everywhere funds move (payouts, `MsgSpendTreasury`,
  guard against sends into `rewards_unallocated`).
- **TW-013 remainder** (§2.4). **TW-014** STRUCTURALLY PREPARED (§5.11). **TW-003** minimal genesis
  check (§2.3) + full hardening at launch.
- **Defensive code fixes (implementation prerequisites — task-graph T0.4 / T5.2, not new protocol
  rules):** TW-022 = bound `ConsensusKeyReuseLockout` in `Params.Validate` and fix the `int64(uint64)`
  cast so lockout is never silently disabled; TW-023 = `RegisterCoreSlot` MUST distinguish a `NextSlotID`
  store error from not-found (never coerce a store error to `id=1`); TW-024 = rewards `SetState` MUST
  check the `MaxSupply` parse `ok`/error (never proceed on a silent parse failure).
- **TW-020/021/025** dispositioned at launch lint. Superseded (no work): TW-001/011/017/019.
- **Genesis linter:** reject a genesis with `active_slot_count < coreslot.params.min_active_slots` (the
  exact consensus minimum to complete a first RANDAO round; **7–9 is an operational target, not the
  consensus minimum**); reconcile Min/Max vs active count (TW-021); reject known non-production test
  placeholders (e.g. placeholder `block.max_gas`, interim payout caps).

---

## 8. Export / import & index classification

Continuation export/import MUST reproduce identical future behavior; there is **no zero-height rebasing**
(absolute height preserved; zero-height export modes disabled/rejected/documented-unsupported).

**Rewards `GenesisState` (exhaustive):** mode (`FRESH|CONTINUATION`); `Params` (all fields incl. the
three pause flags); `premine_total`; `RewardsState` = `{cumulative_emitted, reserved_total,
settlement_clock, last_processed_epoch}`; all `SlotPeriodReward` (with status + `total_paid` +
`next_chunk_index` = partially-paid state); all `PayoutPeriodBoundary`; all `EpochEmission`; all
`PendingEligibility`; `economic_authority` + its `PendingAuthorityTransfer`. **Import validation:**
recompute `reserved_total` from live records and reject a mismatch; verify `cumulative_emitted ≤
max_supply` and `bank_supply == cumulative_emitted`; **verify every `PayoutPeriodBoundary` is
monotonic non-decreasing in `payout_period` and that `settlement_clock ≥` the latest closed period's
`settlement_start_clock`** (reject otherwise); rebuild the DERIVED `ExpiryQueue` from every
`ACCRUING|OPEN` record and verify a one-to-one correspondence. The `ExpiryQueue` is NOT exported.

**Randomness `GenesisState` (exhaustive):** `Params`; `State` = `{current_epoch,
current_epoch_start_height (= H0)}` (first BeginBlock height MUST equal `H0`, §3); **all `Round` records
with status** — `OPEN`, `CANCELLED_BY_PAUSE` (incl. placeholders), and retained `FINALIZED`; for `OPEN`
rounds their `RoundSnapshots`/`Commitments`/`Reveals`; all `Selections` (incl. finalized-but-not-yet-mined
future selections); all `SelectionExclusions`. `State` carries no seed. `validator_authority` +
`emergency_authority` + their `PendingAuthorityTransfer` are owned by `x/coreslot` and exported in the
**coreslot** `GenesisState` (§2); every `CoreSlot` carries an explicit, validated `randao_signer_address`
(§2.2).

**Classification table (exhaustive):**

| Collection | Class | Rebuild / retention / import validation |
|---|---|---|
| `CoreSlots` (incl. `randao_signer_address`) | AUTHORITATIVE | exported; every Slot has an explicit validated signer |
| `ActiveSlots` index | DERIVED-REBUILT | rebuilt from CoreSlot statuses; equality invariant-checked |
| pending key-rotation index | DERIVED-REBUILT | rebuilt from rotation records |
| coreslot authorities (`validator`, `emergency`) + their pending transfers | AUTHORITATIVE | exported (coreslot GenesisState); acceptance `current==live` check |
| `Randomness Params` | AUTHORITATIVE | exported; genesis validation (§4.1) |
| `Randomness State` (`current_epoch`, `H0`) | AUTHORITATIVE | exported; first-BeginBlock `== H0` |
| `Round` (all: OPEN / CANCELLED_BY_PAUSE / FINALIZED) | AUTHORITATIVE | exported; FINALIZED retained |
| `RoundSnapshots` / `Commitments` / `Reveals` | HISTORICAL / PRUNED | exported while round OPEN; pruned after finalization |
| `Selections` | AUTHORITATIVE / RETAINED | exported; retained indefinitely (testnet) |
| `SelectionExclusions` | AUTHORITATIVE | exported; active predicate `T' ≤ excluded_until_epoch` |
| `Rewards Params` (+ 3 pause flags) | AUTHORITATIVE | exported; structural immutability enforced |
| `premine_total` | AUTHORITATIVE | exported; FRESH-only `bank==premine==cumulative` |
| `RewardsState` (`cumulative`, `reserved_total`, `settlement_clock`, `last_processed_epoch`) | AUTHORITATIVE | exported; `reserved_total` recomputed & clock re-verified |
| `EpochEmission` | AUTHORITATIVE / RETAINED | exported; retained indefinitely; one per `E ≥ first_reward_epoch` |
| `SlotPeriodReward` (incl. `total_paid`/`next_chunk_index`) | AUTHORITATIVE | exported; key/status/amount invariants |
| `PayoutPeriodBoundary` | AUTHORITATIVE | exported; monotonic-non-decreasing check |
| `PendingEligibility` | AUTHORITATIVE | exported; consumed & deleted at `end(E)` |
| `rewards` authority (`economic`) + pending transfer | AUTHORITATIVE | exported (rewards GenesisState); `current==live` check |
| `ExpiryQueue` | DERIVED-REBUILT | NOT exported; rebuilt from ACCRUING/OPEN records; one-to-one verified |

Continuation tests MUST cover export→import→continue across: bootstrap/mid-epoch, commit/reveal/winner-prep
windows, `CANCELLED_BY_PAUSE` (incl. placeholder), NO_WINNER, future `Selection`, signer rotation,
`PendingEligibility` mid-mining, ACCRUING, OPEN, partially-paid, FINALIZED, payouts-paused,
expiry-backlog, and a pending authority transfer — each with **app-hash agreement**.

---

## 9. Genesis & launch requirements

- Fresh genesis MUST satisfy §5.3 (FRESH), contain `active_slot_count ≥ coreslot.params.min_active_slots`,
  and set `first_reward_epoch ≥ first_selectable_epoch`.
- Operationally target **7–9 active Slots** to keep no-winner frequency low.
- Recovery runbook MUST document RANDAO secrets as validator recovery material.
- The production genesis linter (§7) gates public launch.

---

## 10. Test & golden-vector requirements

These are **merge-gating** for the relevant PRs.

**Randomness (consensus):**
- Golden vectors from an **independent oracle** (§4.13), exposing every preimage/digest.
- Quorum table `N = 1..12` (exact `floor(2N/3)+1`); overflow-safe `numerator*N`; counts fit `uint32`.
- Excluded valid revealer still contributes entropy + quorum but cannot win; **inclusive** exclusion
  boundary (`T' ≤ excluded_until_epoch`); stacking updates `reason`/`source_epoch`.
- All five outcomes, each asserting the §4.10 **field-presence matrix**, incl. synthetic all-128-reject
  `REHASH_EXHAUSTED`; **finalize write-once** replay guard (a pre-existing `Selection[T]` is rejected).
- Pause at every window boundary; **same-block pause+resume**; post-cancellation commit/reveal rejected
  by the `status == OPEN` guard; cancelled placeholder for a fully-in-pause commit window.
- Signer default at genesis (FRESH omit → operator) and rotation after snapshot (old frozen signer stays
  authorized); insertion-order permutations over snapshots/commits/reveals/exclusions yield identical
  hashes; consecutive wins; ordering-independence.

**Rewards / monetary:**
- Premine 0% / 5% / 10%; optional initial in-vault AVAILABLE allocation; **non-360 valid
  `epoch_length_blocks`** (emission uses the configured `L`, never 360); halving-threshold crossing and
  max-supply clipping.
- Zero-emission (curve-exhausted, `emissions_enabled_at_finalize == true`) **vs** emissions-paused
  (`false`) history; all four LOCKED routing causes; RESERVED and AVAILABLE (forfeit/expiry) paths;
  `rewards_unallocated` no-spend assertion; §5.13 invariant suite; `bank == cumulative`.
- Entitlement: eligible / inactive-start / inactive-end / no-winner / two-wins / multi-slot;
  `PendingEligibility` written in BeginBlock, consumed at `end(E)`, missing-record fail-closed.
- Expiry: never-opened ACCRUING expires; `MAX_EXPIRY_RELEASES_PER_BLOCK` respected; derived past-deadline
  rejection; expiry-after-finalize and double-release prevented; backlog rebuild + smallest-key resume.
- Payout: `slot_id`-keyed record match after Slot re-registration; **strictly-increasing raw-byte
  recipient ordering** (uniqueness on raw bytes); partial-bank-failure rollback; post-finalize/
  past-deadline/replay rejection; max recipient/chunk/batch bounds and overflow-safe `cap × gas`.
- Authority: two-step rotation (propose/accept/cancel, **replacement** proposal, `current==live`
  acceptance check); resume-only recovery; Treasury positive-coin + `≤ available`; Treasury/payout
  adjacency (both use current `V−R`).
- Genesis: FRESH and CONTINUATION reconciliation (continuation with `cumulative > premine` MUST pass);
  `premine_total` immutability; ACTIVE below `min_active_slots` rejected.

**Cross-module / recovery / security:**
- Determinism (both modules): no wall-clock/map-order/float/unbounded loops; canonical encodings;
  fail-closed cache context.
- CoreSlot removal on the epoch-close block; finite `block.max_gas`; fresh-account flood; maximum payout
  fan-out; **multi-proposer app-hash agreement** (T6.1c and export/import).
- Continuation export→import→continue across every §8 state (esp. bootstrap, NO_WINNER, pending authority
  transfer, signer rotation, cancelled placeholder, future Selection, `PendingEligibility`, expiry backlog).
- Drills: RANDAO soak, forced-withhold, **grinding** (accepted weakness), pause-cancelled round,
  payout+RANDAO sequence-contention, restart, fresh-account payout flood.
- The test plan and Definition of Done MUST contain **zero** tests for abandoned behavior (`previous_seed`,
  scheduled/realized, `balance == 0`, mint-rollback-on-payout, no-penalty-missed-commit, epoch-derived
  expiry, split PeriodEntitlement+RewardBatch) and use only current-architecture terminology.

---

## Appendix A — message → role map (finalize proto in implementation)

| Message | Authorizing role |
|---|---|
| CoreSlot admission/suspension/lifecycle | `validator_authority` |
| `MsgUpdateRandaoSigner` | Slot **operator** (not an authority role) |
| `MsgSetRandomnessEnabled` | `emergency_authority` |
| rewards pause (emissions/entitlements/payouts) | `emergency_authority` |
| payouts resume-only recovery | `economic_authority` (resume `false→true` only) |
| `MsgSpendTreasury` (economic **param updates are future-only — none in v3**) | `economic_authority` |
| `MsgPropose/Accept/CancelAuthorityTransfer{role}` | current holder / proposed / current holder of `role` |

---

*End of Reward Protocol v3. This document is the normative source; the execution sequencing lives in
`docs/plans/reward-redesign-task-graph.md`, and historical rationale in `docs/research/`.*
