# Review Process

Twilight Core is consensus- and value-critical, so every change must be reviewed before it
lands. This document describes how — both the automated gates and the multi-model
adversarial review used heavily during early development and, going forward, alongside
maintainer review.

## Process history (full transparency)

The chain — including the consensus (`x/coreslot`) and rewards (`x/rewards`) modules — was
initially developed **solo, in-house**, with the multi-model AI review described below run
continuously during development. That early review was **not recorded per-change** (some
work was merged locally without a PR).

**From the adoption of this document onward, all changes are reviewed on the record**
(PRs + CI + the checklist below). The already-built modules are additionally covered
retroactively by:

- a **recorded baseline review** of `x/coreslot` and `x/rewards`
  ([docs/reviews/2026-06-29-baseline-coreslot-rewards.md](docs/reviews/2026-06-29-baseline-coreslot-rewards.md)),
  with supporting evidence indexed in
  [docs/testing/validation-summary.md](docs/testing/validation-summary.md), and
- the going-forward automated gates, simulations, and chaos drills, which run against the
  current code regardless of how it was originally merged.

We deliberately do **not** fabricate back-dated issues/PRs or rewrite history. For code,
the assurance that matters is the *current* state plus the recorded review and tests
attached to it.

## Automated gates (CI — required to merge)

Every PR must pass [`.github/workflows/ci.yml`](.github/workflows/ci.yml):

- **build & test** — `go build ./...`, `go test ./...`
- **golangci-lint** — static analysis (`.golangci.yml`)
- **gofmt & tidy** — formatting and a clean `go mod tidy`
- **govulncheck** — dependency vulnerability scan; **blocking** (a newly reachable
  advisory fails CI; advisories in modules the code does not call do not)

Consensus/economic changes should also be exercised by the relevant **drills**
(`make drills`) and, as they land, the **module simulations**.

## Multi-model adversarial review

Changes go through a layered review designed to decorrelate blind spots as far as
practical with current tooling:

1. **Broad correctness pass** — a general LLM review of the change for correctness,
   clarity, and obvious defects.
2. **Adversarial/hostile pass** — a reviewer prompted to actively hunt for bugs, edge
   cases, and unsafe assumptions, treating the change as guilty until proven correct.
3. **Self-review pass** — an automated self-review before the change is opened, catching
   regressions and style/contract violations.
4. **PR review** — an automated reviewer on the pull request as the final gate.

This is a strong **defect-removal** layer, but it does **not** replace maintainer
responsibility, deterministic tests, simulations, operational drills, or independent expert
review. Emergent, system-level properties — consensus safety under adversarial validators,
economic-incentive attacks, and cross-module invariants — are validated separately by
simulations and chaos drills, and, before mainnet, by an **independent expert review and
security audit** (see [`SECURITY.md`](SECURITY.md)).

## Human review

While the project is small, maintainer approval is required for consensus-critical changes.
Consensus-critical areas include:

- `x/coreslot` validator-set and lifecycle logic;
- `x/rewards` finalization, emission, and economic accounting;
- `app/` wiring;
- upgrade handlers;
- genesis import/export behavior;
- any code path that can affect deterministic state transitions or `ValidatorUpdate`s.

As the contributor base grows, branch protection will require at least one independent
approving review for these paths.

## Reviewer checklist

See the [pull request template](.github/PULL_REQUEST_TEMPLATE.md) for the per-change
checklist (tests, determinism, fail-closed posture, validator-update provenance, security
considerations, migration/upgrade impact, and docs).
