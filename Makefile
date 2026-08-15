.PHONY: build test fmt lint vet vuln tidy proto proto-descriptor localnet-init localnet-smoke localnet-rewards-smoke localnet-rewards-soak localnet-agree \
	api-smoke drill-lifecycle drill-restart-rotation drill-quorum drills

build:
	go build ./cmd/twilightd

test:
	go test ./...

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*' -not -path './website/*')

# Static analysis — matches the CI golangci-lint job (config in .golangci.yml).
# Requires golangci-lint: https://golangci-lint.run/usage/install/
lint:
	golangci-lint run

vet:
	go vet ./...

# Dependency vulnerability scan. Runs the same script as CI, so the two cannot drift:
# the govulncheck version is pinned inside the script, and reachable advisories fail
# unless explicitly accepted in .govulncheck-allow.json. Requires jq.
vuln:
	./scripts/vulncheck.sh ./...

tidy:
	go mod tidy

proto:
	PATH="$${PATH}:$$(go env GOPATH)/bin" ./scripts/protocgen.sh

# Regenerate the offline tx-decode descriptor set for downstream indexers/explorers
# (docs/proto/twilight-descriptors.pb + manifest). CI re-runs this and fails if the
# committed output is stale (.github/workflows/ci.yml: proto-descriptor). Use the
# protoc version pinned in scripts/export-proto-descriptor.sh to avoid byte drift.
proto-descriptor:
	./scripts/export-proto-descriptor.sh

localnet-init:
	./scripts/localnet/init.sh

localnet-smoke:
	./scripts/localnet/smoke.sh

localnet-rewards-smoke:
	./scripts/localnet/rewards-smoke.sh

# Soak harness: runs the four-node rewards localnet for SOAK_DURATION seconds with
# continuous determinism/accounting assertions + periodic claim/pause/param/restart
# drills. Env-tunable (SOAK_DURATION, EPOCH_LENGTH, PREMINE, CHAOS, ...). See
# docs/research/x-rewards-soak-harness-design.md.
localnet-rewards-soak:
	./scripts/localnet/rewards-soak.sh

# Cross-node app/validators/next-validators hash agreement against an already
# running localnet (use after `make localnet-init` + scripts/localnet/start.sh).
localnet-agree:
	./scripts/localnet/agree.sh

# Self-contained API-surface smoke: spins up a throwaway localnet with REST + Swagger
# enabled and a seeded reservation, runs both smoke scripts, and tears it down.
api-smoke:
	./scripts/smoke-local.sh

# Operational drills (each spins up its own four-node localnet and tears it down).
drill-lifecycle:
	./scripts/localnet/lifecycle-e2e.sh

drill-restart-rotation:
	./scripts/localnet/restart-rotation.sh

drill-quorum:
	./scripts/localnet/quorum-drill.sh

drills:
	@RUN_ID="$${RUN_ID:-$$(date -u +%Y%m%d-%H%M%S)-$$$$}"; \
	export RUN_ID; \
	./scripts/localnet/lifecycle-e2e.sh; \
	./scripts/localnet/restart-rotation.sh; \
	./scripts/localnet/quorum-drill.sh
