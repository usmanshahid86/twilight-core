# Twilight Core

Twilight Core is a greenfield Cosmos SDK and CometBFT Proof-of-Authority chain.
Validator admission and validator updates are owned exclusively by
`x/coreslot`. Standard staking, distribution, and slashing modules are omitted.

```bash
make build
make test
make localnet-smoke
```

The native base denomination is `utwlt` (the only denom used for accounting);
the display denomination is `twlt` (symbol `TWLT`, name Twilight) with six
decimal places. The development genesis creates `1,000,000,000,000utwlt` and
assigns it to the local authority account.

Twilight now includes a validated `x/rewards` module that mints scheduled
`utwlt` block rewards through epoch finalization. Rewards are allocated to
eligible CoreSlot operators and claimed through the rewards module. See the
documentation site for details.

## Documentation

The documentation site lives under [`website/`](website/) (Docusaurus):

```bash
cd website && npm install && npm run build   # or: npm run start
```

See also [docs/architecture/coreslot-poa.md](docs/architecture/coreslot-poa.md)
and [docs/operators/core-slot-operator-guide.md](docs/operators/core-slot-operator-guide.md).
