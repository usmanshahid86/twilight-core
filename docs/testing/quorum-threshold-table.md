# Quorum threshold table

Measured by `scripts/localnet/quorum-threshold.sh` (`make localnet-quorum-table`).
Regenerate rather than edit: every number here comes from a real chain that was
started, degraded a node at a time, and observed.

CometBFT commits on **more than 2/3** of total voting power. This chain makes that
arithmetic clean: `SlotVotingPower` is a single chain parameter and genesis validates
that every ACTIVE slot's `ConsensusPower` equals it, so there is no weighted quorum
and tolerance depends on the count alone.

| validators (n) | quorum needs | tolerates offline |
|---|---|---|
|  1    |  1              |  0          |
|  2    |  2              |  0          |
|  3    |  3              |  0          |
|  4    |  3              |  1          |
|  5    |  4              |  1          |

## What the table says

- **3 to 4 is the first step that buys any fault tolerance.** A three-validator set
  survives nothing; losing one halts it exactly as losing one of two does.
- **4 to 5 buys none.** It adds a spare, not resilience — both tolerate one loss.
  The next gain is at seven.
- **An offline validator is not removed from the set.** This chain has no auto-jail,
  so a stopped node keeps its voting power and still counts against quorum. Leaving
  and being switched off are opposite for quorum while looking identical from outside.

## Reading it for a deployment

A two-validator network — the current devnet shape — tolerates nothing: either node
stopping halts the chain until it returns. That is correct behaviour rather than a
fault, and it is worth planning for as a normal event.
