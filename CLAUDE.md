# CLAUDE.md

The agent instructions for this repository live in **[AGENTS.md](AGENTS.md)** — read it
first. It covers what the chain is, the verify commands (`make build/test/lint/vet/vuln`,
`make localnet-smoke`, `make drills`), the hard consensus/economic invariants, the
"don't hand-edit generated code" rule, and the contribution workflow.

@AGENTS.md

## Claude Code notes

- Shared permission defaults are committed in `.claude/settings.json` (deny credential/
  token extraction; prompt before remote writes like `git push` / `gh`; allow the safe
  build/test/lint verify loop). Put personal overrides in `.claude/settings.local.json`
  (git-ignored), never in the shared file.
- Prefer the `make` targets above over ad-hoc commands so your checks match CI exactly.
