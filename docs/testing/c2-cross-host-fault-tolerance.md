# Cross-Host Fault-Tolerance Report (C2)

- **Status:** PASS
- **Run completed:** 2026-07-01
- **Scope:** cross-host validator fault-tolerance and recovery — peer loss, network partition, and quorum-loss halt

## 1. Purpose

Validate that the Proof-of-Authority validator set tolerates real cross-host node and network
failures — and recovers cleanly — on a live, multi-host network. This complements the single-host
C1 endurance run, which explicitly deferred cross-host networking and partition coverage. The run
confirms: liveness while a supermajority is present, safety when it is lost (the chain halts rather
than forks), no automatic jailing or slashing of an unavailable validator (PoA), and deterministic
catch-up on recovery.

## 2. Environment summary

A live network of four validators running on separate cloud hosts across more than one geographic
region. One validator was admitted to the set (via the CoreSlot authority) specifically for this
exercise; the others were already running. One validator is operated by a separate party. Block
cadence is ~5 seconds, zero-premine rewards active. The network was observed throughout from an
independent node's public RPC endpoint (last committed height and per-block commit signatures) and
from each operated node's local status. No private infrastructure details (addresses, hostnames,
accounts, regions, or tooling) are included here.

Each drill changed only node **availability or connectivity** — never chain parameters or state —
and operated only on operator-controlled nodes. The full four-validator set was healthy and signing
at the start of the run (confirmed across finalized commits), so each drill began from a clean 4/4
baseline.

## 3. Drills and results

### Drill A — peer loss and recovery (PASS)

**Method:** one validator's node was stopped, left down for ~1 minute, then restarted.

**Observed:** the network kept producing blocks (height advanced ~8 blocks) with the stopped
validator absent from commit signatures; its CoreSlot stayed **ACTIVE** — no automatic jailing,
because PoA admission is authority-controlled with no slashing. After restart it caught up and
re-appeared in commit signatures within ~40 seconds.

### Drill B — network partition and recovery (PASS)

**Method:** one validator was network-isolated at the host firewall (its peer-to-peer port dropped
in both directions) while its process kept running; isolation was held ~50 seconds, then removed. A
time-bounded auto-restore safeguard was armed on the host in case the manual restore were
interrupted (it was not needed).

**Observed:** the isolated validator's height **froze** — the process stayed up but received no
block data — while the rest of the network continued and it dropped from commit signatures. After
connectivity was restored it reconnected, caught up, and resumed signing. No firewall residue
remained afterward.

> The peer count reported by the isolated node lagged during the partition (stale connection
> entries are not pruned immediately); the **frozen height** is the reliable isolation signal.

### Drill C — quorum-loss safe halt and recovery (PASS)

**Method:** two validators were stopped simultaneously, dropping the live set to two of four (at or
below two-thirds), then both were restarted after ~75 seconds.

**Observed:** block production **stopped at the last committed height and stayed there** for the
entire window — a single frozen height, with no fork and no divergent chain. Without a supermajority
the network halted rather than progressing. On restart, the set returned to full strength and the
chain **resumed from exactly that height**, with all available validators signing again.

| Drill | Failure injected | Liveness | Safety | Recovery |
|---|---|---|---|---|
| A — peer loss | one validator down | maintained (supermajority intact) | — | caught up + re-signed (~40s) |
| B — partition | one validator isolated (process up) | maintained | no fork | reconnected + caught up |
| C — quorum loss | two validators down (≤ ⅔) | halted (by design) | halt, not fork (single height) | resumed from same height |

## 4. Properties confirmed

- **Liveness above threshold:** with a supermajority present, the chain keeps producing through node
  loss and network partition.
- **Safety at/below threshold:** without a supermajority, the chain halts at the last committed
  height — no fork, no divergent state.
- **No automatic jailing or slashing:** a validator that is offline or partitioned keeps its ACTIVE
  CoreSlot. Validator-set membership is authority-controlled, not stake-slashed.
- **Deterministic recovery:** every recovered node caught up via block-sync (which verifies the
  application hash at each height) and resumed signing, with no manual state intervention.

## 5. Limitations

- **Not an endurance run.** These are short, targeted fault-injection drills. Multi-day cross-host
  endurance with continuous app-hash agreement over time is a separate exercise.
- **Not an independent audit.**
- **Sampled, not exhaustive.** Representative failure modes were exercised (single node loss,
  single-node partition, two-node quorum loss), not a systematic fault-coverage sweep.

## 6. Recommended follow-up

- Multi-day cross-host endurance with continuous application-hash agreement monitoring.
- Halving determinism across hosts via a separate parameter-forced throwaway genesis (the default
  first halving is far in the future, so it does not occur within a short run).

## 7. Artifact index

Per-drill observation logs (height and commit-signature timelines for each phase of each drill) are
retained internally and not published. All statements above are sourced from those observations
against an independent node's RPC.
