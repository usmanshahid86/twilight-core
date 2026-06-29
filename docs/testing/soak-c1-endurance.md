# Soak C1 Endurance Report

- **Status:** PASS
- **Run completed:** 2026-06-25
- **Harness:** `scripts/localnet/rewards-soak.sh`

## 1. Purpose

Exercise the rewards epoch/finalization, claim, and accounting paths under a long, continuous,
unattended run, to confirm determinism, liveness, and exact accounting hold over a large number
of epochs rather than only in short tests.

## 2. Environment summary

A single always-on cloud virtual machine hosting a four-node localnet. The run used a
deliberately short epoch (`EPOCH_LENGTH=4` blocks) to stress state growth and accounting many
times faster than a production epoch cadence, and ran with `PREMINE=off` (zero-premine) and the
chaos, pause-cycle, and parameter-drill options enabled. No private infrastructure details are
included here.

## 3. Duration and scope

A single continuous chain ran unattended, including a **48-hour continuous clean window**. The
canonical end-of-run export (nodes stopped, state exported) covers **10,401 finalized epochs**
and **41,604 claim records** (= 10,401 epochs × 4 slots), with **zero assertion failures** and
**zero halts**.

| Metric | Value |
|---|---|
| Verdict | PASS |
| Premine | off (zero-premine) |
| Validators | 4 |
| Epoch length | 4 blocks (stress cadence) |
| Finalized epochs | 10,401 |
| Claim records | 41,604 (exact: 10,401 × 4) |
| Carry-forward remainder | 0 utwlt |
| Cumulative emitted | 17,315,168,760 utwlt |
| Total supply | 17,315,168,760 utwlt (= cumulative + premine 0) |
| Assertion failures / halts | 0 / 0 |

## 4. Scenarios covered

- Continuous epoch finalization and minting over thousands of epochs.
- Periodic claims (single- and multi-epoch).
- **Emergency pause / resume:** a paused claim was rejected at `DeliverTx`, and resume restored
  claiming.
- **Deferred parameter activation:** a queued parameter update activated at the next epoch
  boundary.
- **Crash recovery:** a node was killed mid-run, restarted, caught up, and re-agreed on the app
  hash.

## 5. Invariants monitored

Continuously, each a hard failure on mismatch (all held):

- `total supply == cumulative emitted + premine` (zero-premine integrity — every `utwlt` in
  existence was emitted by the rewards module).
- `claim records == finalized epochs × active slots`.
- `claimed + unclaimed + carry == cumulative emitted` (carry-safe accounting identity).
- Four-node app-hash agreement maintained throughout, with zero divergence.

## 6. Result

PASS. Determinism (no app-hash divergence), liveness (no halt), and exact carry-safe accounting
with zero-premine integrity held across the entire run, including the 48-hour continuous window,
with the pause/resume, deferred-parameter, and crash-recovery scenarios all behaving as
expected. State growth followed the expected per-epoch pattern; at a production epoch length,
the same per-epoch state additions occur less frequently per unit wall-clock.

## 7. Limitations

At the run's parameters, several economic branches were structurally unreachable, so this run
does **not** exercise them (each is covered by unit tests, not by this endurance run):

| Path | Why it did not fire in this run |
|---|---|
| Halving / subsidy decay | Cumulative emission stayed far below the first supply threshold; subsidy was constant |
| Carry-forward remainder | The pool divided evenly every epoch, so carry stayed exactly 0 |
| Non-uniform allocation | All slots were active for every block of every epoch, so shares were equal |
| Treasury split | Treasury share was 0, so no treasury payment occurred |
| Active-set churn during accounting | The active set was fixed throughout the run |
| Claim-range cap | Claims used small ranges; the cap was not approached |
| Cross-host networking / partitions | Single host; cross-host p2p endurance is a separate run |

Intentionally inert in v1 (by design, not gaps): the configured reward weight has no payout
effect — allocation is purely by active-block participation — and fees are disabled. v1 supports
exactly uniform active-block allocation, carry-forward remainder, and supply-threshold halving.

**Recommended follow-up:** parameter-forced drills that make halving, non-zero carry,
non-uniform participation, treasury, active-set churn, and the claim-range cap actually fire and
re-assert the accounting identity across those boundaries; and a cross-host multi-VM endurance
run for network-partition and peer-loss coverage.

## 8. Artifact index

The following run artifacts are retained internally (not published): the harness final report,
the full run log, and the canonical exported application state used for the end-of-run figures
above. All figures in this report are sourced from the canonical export.
