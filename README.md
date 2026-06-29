# Twilight Core

[![CI](https://github.com/twilight-project/twilight-core/actions/workflows/ci.yml/badge.svg)](https://github.com/twilight-project/twilight-core/actions/workflows/ci.yml)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go)](go.mod)

Twilight Core is a greenfield Cosmos SDK and CometBFT Proof-of-Authority chain. The node
binary is `twilightd`.

Validator admission and validator-set updates are owned exclusively by `x/coreslot`: the
chain uses CoreSlot operators rather than a public staking validator set. The standard
staking, distribution, governance, mint, and slashing modules are **intentionally omitted**
(not wired into the app), so none of those flows are exposed to users. The `auth`, `bank`,
and `consensus` modules are present, so standard account and token operations (e.g.
`twilightd tx bank send`) work as usual.

The native base denomination is `utwlt` — the canonical accounting denomination. The
display denomination is `twlt` (symbol `TWLT`, name Twilight) with six decimal places
(1 `twlt` = 10^6 `utwlt`).

## Architecture

**CoreSlot Proof-of-Authority (`x/coreslot`).** The validator set is formed from a capped
set of authority-governed *CoreSlots* (the active-set size is bounded by configurable
min/max parameters). Operators are admitted to a slot and move through a lifecycle
(`register → activate → inactivate / suspend → reactivate → remove`, plus consensus-key
rotation). The module's EndBlocker translates active slots into CometBFT validator-set
updates — and it is the **only** source of `ValidatorUpdate`s in the app. There is no
delegation, staking, slashing, or unbonding.

**Rewards & emission (`x/rewards`).** Block subsidy is emitted on an epoch schedule: at
epoch finalization, `utwlt` is minted into the rewards module account, tracked against a
maximum supply with **supply-threshold halving** (not a fixed block-height schedule). Each
epoch's emission is allocated to eligible active operators by active-block participation and is
claimable per `(slot, epoch)` until claimed. An emergency authority can pause emission, epoch settlement,
or claims independently. The chain's **default genesis has no premine**. (The standard
Cosmos `distribution` module is omitted; reward distribution is handled entirely by
Twilight's custom `x/rewards` module.)

**Determinism.** Epoch finalization runs in a cache context and is written only on full
success — on any unexpected condition it fails closed (errors without committing partial
state). See [REVIEW.md](REVIEW.md) for the determinism rules contributors follow.

## Modules

| Module | Responsibility | CLI |
|---|---|---|
| [`x/coreslot`](x/coreslot) | Validator admission, slot lifecycle, consensus-key rotation, payout address, reward weight, and validator-set updates | `twilightd coreslot …`, `twilightd coreslot-query …` |
| [`x/rewards`](x/rewards) | Epoch emission, supply-threshold halving, per-operator reward allocation and claims, emergency pause | `twilightd rewards …`, `twilightd rewards-query …` |

Standard `tx`/`query` subcommands for the wired Cosmos modules (`bank`, `auth`, `consensus`)
are generated via AutoCLI — e.g. `twilightd tx bank send`, `twilightd query bank balances`.

## Repository layout

```
app/              application wiring (depinject runtime app, module registration, params)
cmd/twilightd/    node binary entrypoint
x/coreslot/       CoreSlot PoA module (validator admission + set)
x/rewards/        rewards / emission module
proto/twilight/   protobuf definitions (coreslot, rewards)
scripts/localnet/ localnet bring-up, smoke tests, and chaos drills
docs/             architecture, operator guides, proto descriptors, references
website/          Docusaurus documentation site
tools/            auxiliary tooling (e.g. the read-only dashboard)
tests/            integration tests
devnet/           devnet genesis and configuration
```

## Prerequisites

- **Go 1.25.x** and **make** — to build and test the chain (see `go.mod`).
- **protoc** — only if you regenerate protobuf (`make proto`).
- **Node.js + npm** — only to build the documentation site under `website/`.

## Build, test, and run

```bash
make build            # go build ./cmd/twilightd
make test             # go test ./...
make localnet-smoke   # spin up a 4-node localnet, run a sanity pass, tear down
```

Bring up a local network manually, or run the rewards/chaos suites:

```bash
make localnet-init             # initialise a local multi-node genesis
./scripts/localnet/start.sh    # start the nodes  (./scripts/localnet/stop.sh to stop)

make localnet-rewards-smoke    # rewards finalize + claim
make drills                    # lifecycle + restart-rotation + quorum drills
```

## Network interfaces

A running node exposes the standard Cosmos / CometBFT interfaces:

| Interface | Default port | Use |
|---|---|---|
| CometBFT RPC | 26657 | blocks, `/block_results`, `/tx`, `/validators`, consensus/node info |
| gRPC | 9090 | typed query/tx services (coreslot, rewards, and the enabled Cosmos modules) |
| REST (gRPC-gateway) | 1317 | JSON wrapper over the gRPC query services |

When `api.swagger` is enabled, merged OpenAPI docs are served at `/swagger/` on the REST
port.

## Status

Twilight Core is pre-1.0 and under active development. The current codebase includes:

- `x/coreslot` — validator admission, slot lifecycle management, and validator-set updates;
- `x/rewards` — scheduled `utwlt` emission through epoch finalization, with per-operator
  reward accounting and claims;
- localnet smoke checks and operational drills exercising consensus-critical behavior.

The localnet/dev setup (`scripts/localnet/init.sh`) funds the local authority and
emergency-authority accounts with `1,000,000,000,000utwlt` each, for development and
localnet use only (the chain's default genesis has no premine).

## Documentation

The documentation site lives under [`website/`](website/) (Docusaurus):

```bash
cd website && npm install
npm run build                 # production build (use `npm run start` for local dev)
```

Key docs:

- [docs/architecture/overview.md](docs/architecture/overview.md) — architecture overview (read-first)
- [docs/architecture/adr/](docs/architecture/adr/README.md) — Architecture Decision Records
- [docs/architecture/coreslot-poa.md](docs/architecture/coreslot-poa.md) — CoreSlot module design
- [docs/operators/core-slot-operator-guide.md](docs/operators/core-slot-operator-guide.md) — operator guide
- [CONTRIBUTING.md](CONTRIBUTING.md), [REVIEW.md](REVIEW.md), [SECURITY.md](SECURITY.md)

## Contributing

Contributions are welcome. For non-trivial changes, open an issue first to discuss the
approach. Consensus-critical areas (`x/coreslot`, `x/rewards` economics, `app/` wiring, and
genesis handling) get extra review — see [CONTRIBUTING.md](CONTRIBUTING.md) and
[REVIEW.md](REVIEW.md).

## Security

**Do not open public issues for security vulnerabilities.** If an issue may affect funds,
validator keys, consensus safety, chain availability, private data, validator admission, or
token accounting, report it privately — see [SECURITY.md](SECURITY.md).

## License

Twilight Core is licensed under the [Apache-2.0](LICENSE) License.
