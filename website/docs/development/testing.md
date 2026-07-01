---
title: Testing
---

# Testing

What to run, and which layer proves which risk. The pre-rewards CoreSlot test plan
is in the repository at `docs/testing/coreslot-test-plan.md`.

## Commands

```bash
go test ./x/rewards/keeper -count=1     # economics
go test ./x/rewards/types -count=1      # params validation, genesis schema
go test ./x/rewards/... -count=1        # incl. client/cli construction tests
go test ./app -count=1                  # runtime wiring, export/import, fail-closed
go test ./... -count=1                  # everything
go vet ./...
go build ./cmd/twilightd

make localnet-smoke                      # node startup + agreement (no epoch close)
make localnet-rewards-smoke              # multi-node finalization + claim
```

## Which test proves which risk

| Layer | Risk covered |
|---|---|
| `x/rewards/keeper` | emission math, active-block accounting, atomic finalization, distribution, claims, params, pause/resume, invariants |
| `x/rewards/types` | params validation, genesis round-trip |
| `x/rewards/client/cli` | CLI request/message construction (incl. pagination) |
| `app` | app/runtime wiring, `InitChain`+`FinalizeBlock` dispatch, export/import, fail-closed lifecycle |
| `make localnet-rewards-smoke` | **multi-node** finalization/claim determinism + cross-node app-hash agreement |

## Key app-level tests

| Test | Proves |
|---|---|
| `TestRewardsRuntimeDispatchFinalizeBlock` | the runtime actually dispatches rewards BeginBlock/EndBlock; exact supply delta |
| `TestRewardsInitChainGenesisAccounts` | genesis creates module accounts with correct permissions |
| `TestRewardsAuthorityMsgRoutedThroughApp` | Msg service reachable; authority/emergency read through wired CoreSlot |
| `TestRewardsShortEpochFinalizeSuspendClaim` | finalize → suspend → claim against the real bank |
| `TestRewardsPopulatedAppExportImportAndContinue` | full app export/import round-trip |
| `TestRewardsRuntimeFinalizeBlockFailClosed` | a lifecycle fault halts the block, no partial commit |

## Determinism expectations

Rewards state transitions are integer-only: no wall-clock time, randomness,
environment variables, or CometBFT-local config; finalization/claims iterate
sorted collections. Cross-node app-hash agreement after finalize and after claim
is the multi-node evidence. See
[Status & Validation](../chain/status-and-validation.md).
