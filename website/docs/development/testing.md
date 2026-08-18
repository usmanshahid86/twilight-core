---
title: Testing
---

# Testing

What to run, and which layer covers which risk. The CoreSlot test plan
is in the repository at `docs/testing/coreslot-test-plan.md`.

## Commands

```bash
go test ./x/rewards/keeper -count=1     # economics
go test ./x/rewards/types -count=1      # params validation, genesis schema
go test ./x/rewards/... -count=1        # incl. client/cli construction tests
go test ./app -count=1                  # runtime wiring, export/import, fail-closed
go test ./app -run Simulation -count=1  # randomized state-machine sims
go test ./... -count=1                  # everything
go vet ./...
go build ./cmd/twilightd

make localnet-smoke                      # node startup + agreement (no epoch close)
make localnet-rewards-epoch-smoke        # multi-node epoch finalization + entitlements
```

## Which test covers which risk

| Layer | Risk covered |
|---|---|
| `x/rewards/keeper` | emission math, active-block accounting, atomic finalization, active-block participation allocation, entitlement release, params, pause/resume, invariants |
| `x/rewards/types` | params validation, genesis round-trip |
| `x/rewards/client/cli` | CLI request/message construction (incl. pagination) |
| `app` | app/runtime wiring, `InitChain`+`FinalizeBlock` dispatch, export/import, fail-closed lifecycle |
| `make localnet-rewards-epoch-smoke` | **multi-node** finalization determinism + cross-node app-hash agreement |
| randomized state-machine simulations | fixed-seed CoreSlot lifecycle and rewards accounting invariant coverage across long random operation sequences |

## Key app-level tests

| Test | Covers |
|---|---|
| `TestRewardsRuntimeDispatchFinalizeBlock` | the runtime actually dispatches rewards BeginBlock/EndBlock; exact supply delta |
| `TestRewardsInitChainGenesisAccounts` | genesis creates module accounts with correct permissions |
| `TestRewardsAuthorityMsgRoutedThroughApp` | Msg service reachable; authority/emergency read through wired CoreSlot |
| `TestRewardsEpochFinalizeSuspendAndRelease` | finalize → suspend → release against the real bank |
| `TestDefinitivePOC1SettlementEndToEnd` | a full 360-block epoch through settlement and finalization, with exact economics |
| `TestRewardsPopulatedAppExportImportAndContinue` | full app export/import round-trip |
| `TestRewardsRuntimeFinalizeBlockFailClosed` | a lifecycle fault halts the block, no partial commit |

## Determinism expectations

Rewards state transitions are integer-only: no wall-clock time, randomness,
environment variables, or CometBFT-local config; finalization and release iterate
sorted collections. Cross-node app-hash agreement after finalize is the
multi-node evidence. See
[Status & Validation](../chain/status-and-validation.md).
