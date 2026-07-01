---
title: Repo Map
---

# Repo Map

Top-level layout of `twilight-core`.

| Path | Contents |
|---|---|
| `app/` | App wiring (`app.go`), module/account config (`config.go`), encoding, params (`params/`). |
| `x/coreslot/` | CoreSlot PoA module (validator authority, slots, reward weights). |
| `x/rewards/` | Rewards module (emission, epochs, allocation, claims, params, invariants, events, query/CLI). |
| `cmd/twilightd/` | The `twilightd` binary and root command (`cmd/twilightd/cmd/root.go`). |
| `scripts/localnet/` | Localnet `init`/`start`/`agree`/`stop` + `smoke.sh` + `rewards-smoke.sh` + soak/drill scripts. |
| `proto/` | Protobuf definitions (`twilight.coreslot.v1`, `twilight.rewards.v1`). |
| `docs/` | Architecture, operator, security, and testing markdown. |
| `website/` | This Docusaurus documentation site (isolated; does not affect Go builds). |
| `Makefile` | `build`, `test`, `proto`, `localnet-smoke`, `localnet-rewards-smoke`, drills. |

## `x/rewards/` internals

| Path | Contents |
|---|---|
| `keeper/emission.go` | Pure halving/subsidy/emission math. |
| `keeper/beginblock.go` / `epoch.go` | Active-block crediting; epoch-boundary helpers. |
| `keeper/endblock.go` / `finalize.go` | EndBlock gate; atomic finalization. |
| `keeper/distribution.go` | Active-block participation allocation. |
| `keeper/claims.go` | Claim execution (grouped payout, atomic). |
| `keeper/msg_server.go` | Msg handlers (claim, update-params, pause/resume). |
| `keeper/query_server.go` | Read-only query server. |
| `keeper/invariants.go` | The five invariants. |
| `keeper/coreslot_reader.go` | The read-only CoreSlot snapshot adapter. |
| `types/` | Proto-generated types, `defaults.go`, `validation.go`, `events.go`, `keys.go`. |
| `client/cli/` | `query.go`, `tx.go` (+ tests). |
| `module.go` | App-module wrapper (modern `appmodule` lifecycle). |

See [Module Map](module-map.md) for the lifecycle/dependency view.
