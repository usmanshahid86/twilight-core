# Twilight Swagger / OpenAPI

Twilight serves a single merged Swagger/OpenAPI document for its **enabled** REST
surface — the Cosmos SDK modules this app actually registers **plus** the custom
`x/rewards` and `x/coreslot` modules. It is served by the node's API server (REST,
default `:1317`) and rendered with an embedded Swagger UI.

## What it documents

Full enabled REST surface (61 paths), merged into one spec:

- **Twilight custom modules:** `twilight.rewards.v1` (10 routes), `twilight.coreslot.v1`
  (10 routes). See `docs/reference/rest-routes.md` for the route inventory.
- **Enabled Cosmos SDK modules:** `auth`, `bank`, `tx`, `consensus`,
  `base/tendermint` (node/block/validatorset), `base/node`.

Standard modules Twilight does **not** run (`staking`, `gov`, `mint`, `distribution`)
are intentionally **excluded** — they are not registered, so they are not documented
as supported APIs.

## How to enable

`app.toml`:

```toml
[api]
enable = true     # API/REST server (required for REST and Swagger)
swagger = true    # serve Swagger UI + spec

[grpc]
enable = true     # canonical gRPC (default)
```

Config behavior:

| `api.enable` | `api.swagger` | REST | Swagger |
|---|---|---|---|
| false | — | not served | not served |
| true | false | served | not served |
| true | true | served | served |

## URLs

- Swagger UI: `http://<host>:1317/swagger/`
- OpenAPI spec (JSON): `http://<host>:1317/swagger/twilight.swagger.json`

Devnet example: `http://16.192.99.123:1317/swagger/` (after that node runs a binary
built with API-3; redeploy required).

## Generated file & embedding

- Generated spec: **`app/openapi/twilight.swagger.json`** (this is the canonical path;
  the openapiv2 plugin's `merge_file_name=twilight` yields `twilight.swagger.json`).
- Embedded into the binary via `app/openapi/openapi.go` (`//go:embed`), alongside a
  minimal `index.html` UI shell. The heavy Swagger UI JS/CSS are reused from the Cosmos
  SDK's embedded `client/docs/swagger-ui` bundle (not vendored again here).
- Registration: `app.(*App).RegisterAPIRoutes` calls through to `runtime.App` for all
  gateway routes, then mounts Swagger at `/swagger/` when `api.swagger=true`.

## Regenerating the spec

```bash
./scripts/protocgen.sh
```

Requires `protoc` and `protoc-gen-openapiv2` on `PATH`. The script's OpenAPI pass runs
`protoc-gen-openapiv2` with `allow_merge=true,merge_file_name=twilight` over the
enabled-module query protos (the twilight `query.proto` files plus the enabled Cosmos
SDK `query.proto`/`service.proto` files) and writes `app/openapi/twilight.swagger.json`.
This emits **JSON only** (no Go runtime code), so the grpc-gateway **v1** runtime used
by the REST gateway is unaffected.

To add a newly enabled module to the docs, add its `query.proto` to the OpenAPI `protoc`
invocation in `scripts/protocgen.sh` and regenerate. Do **not** add protos for modules
the app does not register.

## Smoke check

```bash
BASE_REST=http://localhost:1317 ./scripts/smoke-swagger-api.sh
```
