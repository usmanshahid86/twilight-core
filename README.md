# Nyks Core

Nyks Core is a greenfield Cosmos SDK and CometBFT Proof-of-Authority chain.
Validator admission and validator updates are owned exclusively by
`x/coreslot`. Standard staking, distribution, and slashing modules are omitted.

```bash
make build
make test
make localnet-smoke
```

The native base denomination is `unyks`; the display denomination is `NYKS`
with six decimal places. The development genesis creates
`1,000,000,000,000unyks` and assigns it to the local authority account. There
is no inflation in v1.

See [docs/architecture/coreslot-poa.md](docs/architecture/coreslot-poa.md) and
[docs/operators/core-slot-operator-guide.md](docs/operators/core-slot-operator-guide.md).
