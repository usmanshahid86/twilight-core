---
title: Install
---

# Install

## Requirements

- **Go** — the toolchain version pinned in `go.mod`.
- **make**, **git**.
- For localnets: **jq** and **curl** (used by the localnet scripts).

## Build the node

```bash
git clone https://github.com/twilight-project/nyks-core
cd nyks-core
make build          # builds ./build/twilightd
```

Or directly:

```bash
go build -o build/twilightd ./cmd/twilightd
```

Verify:

```bash
./build/twilightd --help
./build/twilightd rewards-query --help
./build/twilightd rewards --help
```

## Run the tests

```bash
make test           # or: go test ./... -count=1
go vet ./...
```

See [Testing](../development/testing.md) for which test layer proves which risk,
and [Localnet](../chain/localnet.md) to run a multi-node network.

:::note
The documentation site (this site) is built separately under `website/` with
Docusaurus and **does not** affect the Go build or release binaries.
:::
