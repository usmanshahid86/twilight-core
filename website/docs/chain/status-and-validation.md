---
title: Status & Validation
---

# Status & Validation

## Status

Twilight Core is under active development. The current implementation provides the
**CoreSlot** Proof-of-Authority validator lifecycle and a bounded **Rewards**
emission and claims system denominated in `utwlt`. It is **not yet mainnet-ready**
and has **not undergone an external security audit**.

This page is a plain-language summary of what has been validated, and how — and,
just as importantly, what has not. It is engineering validation evidence, not an
audit or a mainnet-readiness certification.

## Validated behavior

Each behavior below is exercised by the evidence type named next to it.

| Behavior | How it is checked |
|---|---|
| CoreSlot is the single source of validator-set updates | deterministic keeper tests and randomized state-machine simulation |
| CoreSlot lifecycle transitions enforce authorization, active-set floor/cap rules, and validator metadata retention | deterministic keeper tests and randomized state-machine simulation |
| Bounded emission — supply-threshold halving, max-supply clipping, and terminal behavior | deterministic keeper tests with exact numeric vectors |
| Epoch finalization and active-block reward allocation (`amount = pool × blocks_active / Σ blocks_active`) | keeper tests and integration drills |
| Carry-forward remainder accounting | keeper tests and branch-coverage drills |
| Treasury split, when enabled | keeper tests and branch-coverage drills |
| Claim replay rejection, claim-range atomicity, and snapshotted payout-address payment | keeper tests and integration drills |
| Rewards accounting identity — treasury, claimed rewards, unclaimed rewards, and carry-forward reconcile to cumulative emitted supply | invariant tests checked against real module, treasury, and payout balances |
| Export / import determinism of full application state | application-level round-trip test |
| Local multi-node finalization and claim, with cross-node app-hash agreement | four-node localnet validation |
| Long-run determinism and exact accounting over many epochs on a zero-premine chain | endurance soak testing |
| Cross-host fault tolerance — peer loss, network partition, and quorum-loss safe-halt, each with recovery | fault-tolerance drills on a live multi-host network |
| Off-happy-path economic branches (halving crossing, non-zero carry, non-uniform participation, treasury, active-set churn, claim-range cap) | branch-coverage drills |
| Module invariants across long, random operation sequences | randomized state-machine simulations |

## Known limitations

- **Not externally audited.** No independent security review has been performed.
- **Not mainnet-ready.** Public devnet and mainnet operation still require
  deployment validation beyond what is listed above.
- **Multi-day cross-host endurance is not yet done.** Cross-host coverage to date
  is the fault-tolerance drills above plus single-host endurance soak; a sustained
  multi-day run across hosts is still pending.
- **The production on-chain upgrade procedure is not yet exercised** (upgrade
  handlers and store migrations).
- **Weighted rewards and fee-funded rewards are not active.** They are code-gated
  and rejected until implemented. The reward weight recorded on a claim record is
  metadata only and has no payout effect — payout is by
  [active-block participation](../rewards/economics.mdx).
- **Code-gated behavior is not user-facing functionality.** Anything not enabled
  in the current implementation is simply not available.

## Where the evidence lives

Detailed evidence is maintained in the repository's test suites and curated
validation reports. The main locations are `x/coreslot/`, `x/rewards/`, `app/`,
`scripts/localnet/`, and `docs/testing/validation-summary.md`.
