# Twilight Chain Docs — Style & Voice Guide

This governs everything under `website/docs/`. It exists because the docs are
**public, audience-first documentation** — not a mirror of the code or of the
project's internal development history. Follow it when writing or reviewing any
page.

## 1. Audience & purpose

These docs are for people who want to **understand, run, query, integrate with,
or build on** Twilight Chain. They have never seen the source and do not know the
project's internal history. Every page should answer some of:

- What is this (chain / module / concept)?
- What can it do (capabilities)?
- How do I use, query, or operate it (interfaces, examples)?
- What is stable, and what is not yet?

**The register test — apply it to every sentence:** *"Does a reader who has never
seen the code or the project's internal history need this, and is it phrased for
them?"* If not, cut or rewrite it.

## 2. Belongs / doesn't belong

| Belongs | Doesn't belong |
|---|---|
| What the chain/modules **do** (capabilities) | How the project was **built** (phases, sprints, sequence) |
| How to **run / query / operate** (interfaces, examples) | Internal validation-report narration ("the … smoke proved") |
| **Concepts** for understanding (why PoA; how epochs/claims work) | Per-page dev-status blocks ("this page reflects the … implementation") |
| Honest **maturity**, stated once (see §4) | Pointers to internal `docs/research/` material or "phase reports" |

## 3. Wording rules

**Avoid** (internal-process or overclaiming):

- `Phase 10`, `Phase 11`, any `Phase N` / `phase N`
- `validated through …`, `this page reflects the … implementation`, `pending Phase`
- `smoke proved`, `drill proved`
- `release candidate` — unless it is literally a tagged RC
- `audit` / `audited` — unless an external audit actually exists
- Overclaim words **used as broad assurance claims**: `proof`, `guarantee`,
  `complete coverage`, `exhaustive`, `mainnet-ready` (as a positive claim).
  Technical names for evidence artifacts are fine — e.g. "claim proof",
  "finalization proof", "localnet finalization + claim proof".

**Prefer** (reader-facing, honest):

- `current implementation`, `current validation evidence`, `tested behavior`
- `known limitations`, `not externally audited`, `not mainnet-ready`
- `local multi-node validation`, `cross-host fault-tolerance drills`,
  `endurance soak`, `randomized state-machine simulations`,
  `branch-coverage drills`

## 4. Maturity & validation live in exactly one place

All maturity/validation content belongs on **`chain/status-and-validation.md`**,
written **by capability and evidence type — never by development phase**. Every
other page:

- carries **no** status-note block and **no** "Phase N proved …" aside;
- may, at most, link to Status & Validation **at the end** of the page.

**Be specific and never inflate.** State exactly what was done: "cross-host
fault-tolerance drills" is not "cross-host endurance"; "local multi-node
validation" is not "network validated." If only the local case was run, say local
only.

Module pages **may** still describe **safety-relevant behavior** — e.g. "claims
are replay-protected" or "claims pay the snapshotted payout address" — because that
is *behavior*, not maturity. They just must not narrate *how* that behavior was
validated.

### Evidence wording (how to reference validation without phases)

When a page needs to say something is validated, describe it by **evidence type**,
not project history.

Prefer:

- "covered by deterministic keeper tests"
- "exercised by branch-coverage drills"
- "checked by randomized state-machine simulations"
- "observed in local multi-node validation"
- "covered by endurance soak testing"

Avoid:

- "Phase N proved…"
- "validated through Phase N…"
- "the smoke/drill proved…"

## 5. Page shape

Lead with capability/usage, not history. A typical page:

1. What it is / does (one or two sentences).
2. How to use, query, or operate it — with concrete examples.
3. Concepts needed to understand it.
4. (Optional) a single closing link to Status & Validation for maturity.

Match the tone of the repository's conceptual docs under `docs/` (architecture
overview, ADRs): explain for understanding, don't dump the code.

## 6. Sanitization

No real infrastructure (IPs other than `127.0.0.1` in local examples, hostnames,
cloud/account/region, internal node names), no tool residue (assistant/model
names), and no **machine-specific** local paths (e.g. `/Users/…`, `/home/<user>/…`,
`/mnt/…`, worktree paths). Public examples **may** use documented paths such as
`~/.twilightd` or `$HOME/.twilightd`. Public, curated content only.

## 7. Review gates — run before every docs PR

**Gate 1 — development-phase vocabulary (must be ZERO across the whole site):**

```bash
grep -RInE "[Pp]hase[- ]?[0-9]|validated through|reflects the .* implementation|pending [Pp]hase" website/docs website/src
```

**Gate 2 — overclaim / audit review (eyeball each hit):**

```bash
grep -RInE "audit|audited|proof|proved|guarantee|complete coverage|exhaustive|mainnet-ready|release candidate" website/docs website/src
```

Legitimate hits are allowed here — "**not** externally audited", "**not**
mainnet-ready", "audit artifacts", and evidence-artifact names like "claim
**proof**". Eyeball `proof`/`proved` (fine in artifact names, wrong as a broad
assurance claim or in "the drill **proved**…"). `guarantee`, `exhaustive`,
`complete coverage`, and stray `release candidate` should trend to zero.

**Gate 3 — build (strict):**

```bash
cd website && npm run build   # onBrokenLinks / onBrokenAnchors: "throw"
```

A page is done when it passes the register test, all three gates, and reads
capability/usage-first to someone who has never seen the code.
