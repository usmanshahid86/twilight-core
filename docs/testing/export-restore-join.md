# Export, restore and node-join — what recovery actually works today

Audience: whoever has to rebuild a node, or relaunch a network, without warning.

This is a characterization of observed behaviour, not a design document. It records what
`twilightd export` produces, what happens when you try to start a chain from that document,
and what a node with no data needs in order to join a running network. Reproduce it with:

```bash
make localnet-export-restore-drill
```

**Short answer**

| path | status |
|---|---|
| export a live chain | **works** |
| start a chain from that export | **refused**, deliberately and with a clear message |
| join a running network from empty state | **works**, and is the supported recovery path |

If you are recovering a node right now: **resync it.** That is the path the chain supports.

---

## 1. Export

`twilightd export --home <node-home>` succeeds against a chain that has run for several epochs
and settled real value.

**The node must be stopped.** The command opens the application database directly, and a
running node holds the lock. On a multi-validator network you can stop one member and leave
the rest producing — with four validators, three is still above the two-thirds quorum.

The document is genesis-shaped: `app_state`, `chain_id`, `initial_height`, `genesis_time`,
`app_hash`, and `consensus` (which holds both `params` and `validators`). It can be dropped in
as a `config/genesis.json` without reshaping.

`initial_height` is the height the chain *would* resume at — one past the last committed
block. It is the only authoritative statement of what the export contains. Do not infer the
exported height from a status query taken before you stopped the node: the node can commit one
more block between being asked and exiting.

### What the document carries

Verified by projecting **both** the export and state captured at exactly the exported height
through one canonical form and requiring exact equality — every field below, not a sample:

- **CoreSlot** — every slot with its status and consensus power
- **finalized epochs** — with their emission records
- **entitlements** — `entitlement_amount`, `released_amount`, `payout_address`,
  `total_blocks_active`, reward-config version, lifecycle audit fields and created height, per
  slot per closed epoch
- **settlements** — slot, epoch, `finalized`, `finalized_height`, `finalization_reason`,
  `next_chunk_index`
- **outstanding entitlement liability**, **carry-forward remainder**
- **escrow** — as the rewards module account's balance in `app_state.bank`

Note the split, because it surprises people reading the document by hand: settlement
*workflow* state lives under `app_state.mining.settlements`, and the *money* — entitlement and
released amounts — lives under `app_state.rewards.slot_entitlements`.

### What the document does NOT carry

**Per-slot participation for the OPEN epoch is absent.**

A settled epoch keeps its attribution: each entitlement carries `total_blocks_active`, so you
can see exactly what every slot earned and why. The epoch in progress does not. The export
carries `open_reward_enabled_blocks` — the epoch's aggregate count — but nothing that says
which slot contributed which share of it.

That distinction matters. The aggregate alone cannot reconstruct an allocation: it tells you
how much the epoch is worth, not who earned it.

The drill answers this from the **artifact**, not by asking a restarted chain. A chain restarted
from the document begins accruing fresh counters immediately, so a non-zero reading there would
conceal exactly the loss being tested for. Recorded as
`open_participation_preservation: lost`.

This is tracked as **TW-011**. It is not fixed here, and #108 does not attempt to.

### `--for-zero-height` does nothing

The flag is accepted and has no effect. Exporting with and without it produces semantically
identical documents, and `initial_height` is the last block height plus one either way.
Heights are never rebased. The drill **asserts** this rather than recording whichever answer
appeared, so if the flag ever starts doing something this note fails with it.

This is an export-CLI characteristic, not a state-machine one. It is unrelated to fresh-genesis
height validation, which works (see below).

---

## 2. Starting a chain from the export — refused

The attempt is made in isolation: separate home directory, separate ports, and the original
network stopped first so two chains never share a chain-id and validator keys at once.

Every node refuses at `InitChain`, with:

```
panic: initialize coreslot genesis: active slot 1 must have activated and
activation-effective heights equal to the initial height 815: invalid core slot genesis
```

**This is correct behaviour, not a bug.** `x/coreslot` validates that a fresh genesis describes
a chain starting now: an ACTIVE slot must have been activated *at* the initial height. A
mid-life export describes slots activated hundreds of blocks earlier, so it is refused.

Worth recording precisely, because it was not what we predicted: **`x/coreslot` refuses first.**
The `x/rewards` epoch-anchor rule — which requires the anchor to be version 1 effective at
epoch 1 — would also refuse a mid-life document, but the chain never reaches it.

So there is **no continuation path today**. An exported document is a record of state, not a
restart medium. Building one is deliberate future work with its own consensus review, and
nothing in this drill implements it.

### What that means operationally

Because open-epoch participation is missing *and* the document is refused, the two findings do
not compound: you cannot silently relaunch from a lossy document, because you cannot relaunch
from it at all. The failure is loud and immediate.

Were continuation support ever added, TW-011 would have to be resolved first — otherwise the
first epoch after a relaunch would under-credit every operator that had participated in the
open epoch, silently.

---

## 3. Joining from empty state — the supported path

A node with no application or database state joins the running network and reaches the head.
In the drill: from empty to synced in **22 seconds** against a network at height 814, agreeing
with all four incumbents on `app_hash`, `validators_hash` and `next_validators_hash`.

Ordinary block sync. **No state-sync snapshot configuration is involved or required.**

What the operator actually has to supply:

- the running network's `genesis.json`
- `persistent_peers` naming at least one reachable member — peer to several; with `pex`
  disabled, a joiner that knows only one node depends on it to relay everything
- the `twilightd` binary
- RPC / P2P / gRPC ports that do not collide with anything else on the host
- an empty data directory

That is the whole list. The joining node is a non-validator — it holds no CoreSlot — which is
the ordinary case: an operator rebuilding a machine, or adding a read node.

---

## 4. Recovery paths, plainly

**Exists today**

- **Resync from peers.** A node that lost its disk rejoins from empty state and catches up.
  This is the recovery path.
- **Export for inspection.** You can capture a chain's state at a height and read it. Useful
  for audit, forensics and analysis.

**Does not exist today**

- **Relaunch from exported state.** Refused at `InitChain` by fresh-genesis validation.
- **Mid-life genesis import / continuation.** Not implemented; refusing it is the current
  designed behaviour.
- **State-sync snapshots.** Not configured. Not needed for join at localnet scale; a
  production-height chain may want them, which is separate work.

**Consequences worth planning around**

- A network with no continuation path can only be relaunched from a *fresh* genesis. Any
  running chain's history ends at that point.
- Recovery of a single node is well supported; recovery of a whole network from a snapshot is
  not.
- This is precisely why `x/upgrade` matters: a coordinated in-place upgrade is how a live
  chain changes without needing continuation. See [`upgrade-drill.md`](upgrade-drill.md).

---

## Evidence

Each run writes `build/localnet/evidence/<run-id>/export-restore/`:

| file | contents |
|---|---|
| `binaries.json` | source commit and binary SHA-256 |
| `export.json`, `export.sha256` | the exported document and its hash |
| `export-zero-height.json` | the same export with `--for-zero-height`, for comparison |
| `state-at-export-height.json` | live state pinned at exactly the exported height |
| `export-summary.json` | the canonical economic state compared, and the participation classification |
| `restore-attempt.json` | outcome, node count, blocks committed, restored-network agreement, refusal text |
| `restore-nodes.json`, `restore-node<N>.log` | per-node process state, refusal class and excerpt — the refusal is reconstructible from evidence alone |
| `join.json` | start/end heights, sync duration, operator inputs required |
| `assertions.jsonl`, `summary.csv`, `hashes.jsonl` | per-assertion, per-phase, per-height records |
| `verdict.txt` | `export=`, `restore=`, `join=`, `overall=` — separately |

The verdict deliberately exposes all three outcomes. A run where the restore is refused is a
**passing** run — the refusal is the finding — and collapsing that to a single word would let
"#108 passed" be misread as "continuation is supported".
