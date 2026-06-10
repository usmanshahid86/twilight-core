# Core-Slot Test Plan

Current automated coverage validates params and invalid empty genesis,
deterministic genesis validator updates, lifecycle inactivation, active key
rotation, duplicate consensus-key rejection, and structural staking omission.

Validation commands:

```bash
make proto
make fmt
go test ./...
make build
make localnet-smoke
```

The localnet smoke test creates four independent node and validator keys,
creates four active core slots, derives and validates the matching CometBFT
genesis validators, starts all four nodes, and waits for block production.

Remaining v2 test work includes adversarial batch rollback injection,
transaction-driven lifecycle E2E, restart during active rotation, export/import
app-hash comparison, and validator-hash/next-validator-hash assertions.
