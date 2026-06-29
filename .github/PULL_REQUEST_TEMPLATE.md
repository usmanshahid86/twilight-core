<!-- Thanks for contributing to Twilight Core. Keep PRs focused and explain the WHY. -->

## Summary

<!-- What does this change do and why? Link any related issue: Closes #NNN -->

## Type of change

- [ ] Bug fix
- [ ] Feature
- [ ] Refactor / internal
- [ ] Docs
- [ ] CI / tooling

## Affected area

- [ ] `x/coreslot` — consensus / validator set (**consensus-critical**)
- [ ] `x/rewards` — emission / epoch accounting / reward claims / economics (**consensus-critical**)
- [ ] `app/` wiring
- [ ] CLI / REST / gRPC surface
- [ ] proto / generated code
- [ ] genesis / config
- [ ] tooling / docs / scripts
- [ ] tests / CI

## Checklist

- [ ] `make build` passes locally
- [ ] `make test` passes locally
- [ ] `make lint` passes locally, if available
- [ ] Tests added/updated for the change
- [ ] If `proto/` changed: `make proto` re-run and generated code committed
- [ ] Conventional Commit title used, for example `feat(rewards): ...`
- [ ] Docs/ADRs updated if behavior, economics, operator flow, or design changed

## Determinism & safety

Required for consensus-critical changes:

- [ ] No `time.Now()`, `rand`, goroutines, map-order dependence, external I/O, or nondeterministic behavior in any consensus path
- [ ] State-transition changes are fail-closed: error safely, no partial commit, no silent skip
- [ ] No new source of `ValidatorUpdate`s outside `x/coreslot`
- [ ] BeginBlock / EndBlock / PreBlock / upgrade-handler behavior considered where applicable
- [ ] Genesis import/export behavior considered where applicable
- [ ] Exercised by a drill, simulation, invariant test, or multi-node smoke test where applicable

## Security considerations

- [ ] No secrets, private keys, mnemonics, RPC credentials, private IPs, or sensitive infrastructure details included
- [ ] This PR does not introduce a public security disclosure
- [ ] Security-sensitive behavior is clearly called out for reviewer attention

Notes:

<!-- Mention any trust-boundary, key-handling, consensus-safety, funds-safety, or validator-safety concerns. -->

## Review

See [REVIEW.md](../REVIEW.md).

- [ ] Multi-model adversarial review pass completed where applicable
- [ ] Maintainer review required for consensus-critical paths
- [ ] Risky files / assumptions called out below

## Reviewer notes

<!-- Highlight files, assumptions, edge cases, or areas that need careful review. -->
