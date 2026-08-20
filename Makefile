.PHONY: build test fmt lint vet vuln tidy consensus-vectors proto proto-descriptor localnet-init localnet-smoke localnet-rewards-smoke localnet-rewards-epoch-smoke localnet-settlement-smoke localnet-quorum-table localnet-validator-growth localnet-validator-departures validator-set-study localnet-join-and-settle localnet-rewards-soak localnet-agree \
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

# Protocol-vector conformance. Runs the tracked normative vector packs
# (internal/consensusvectors/testdata) against the production and
# production-intended functions that implement them. Runs the same script as CI,
# so the two cannot drift. Nothing is downloaded at run time; the gate executes
# the repository's own committed bytes.
consensus-vectors:
	./scripts/consensus-vectors.sh

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

localnet-rewards-epoch-smoke:
	./scripts/localnet/rewards-smoke.sh

# The definitive POC1 money-movement proof: real signed settlement transactions on
# a four-node localnet, across more than one epoch. This is the run that shows the
# chain paying participants, and it replaces the retired claim smoke in that role.
localnet-settlement-smoke:
	./scripts/localnet/settlement-smoke.sh

# Measures how many validators the chain needs and how many it can lose, by
# building real sets of each size and degrading them. Writes the table to
# docs/testing/quorum-threshold-table.md. No epochs: validator-set mechanics only.
localnet-quorum-table:
	./scripts/localnet/quorum-threshold.sh

# A chain that starts at one validator and grows to five, each node syncing
# before it is admitted to the set. The untested half of node-join.
localnet-validator-growth:
	./scripts/localnet/validator-growth.sh

# The four ways a validator leaves — offline, inactivated, suspended, removed —
# plus the guards on the way down and a key rotation with no quorum margin.
localnet-validator-departures:
	./scripts/localnet/validator-departures.sh

# The operational playbook: one Slot, a second joins, both earn, and each
# operator settles its own entitlement alone from its own node.
localnet-join-and-settle:
	./scripts/localnet/join-and-settle.sh

# The whole validator-set behaviour study.
validator-set-study: localnet-quorum-table localnet-validator-growth localnet-validator-departures

# Retained under its historical name so existing invocations keep working. It is
# NOT a money-movement gate: V2 release is keeper-only until Settlement, so no
# public payout exists for a localnet to submit.
localnet-rewards-smoke: localnet-rewards-epoch-smoke

# Soak harness: runs the four-node rewards localnet until SOAK_EPOCHS epochs have
# closed (default 3) with continuous determinism/accounting assertions + periodic
# pause/param/restart drills. Sized in EPOCHS, not seconds: the ratified 360-block
# minimum epoch takes minutes on a localnet, and a seconds-based budget could finish
# without closing one — proving nothing while reporting success.
# Env-tunable (SOAK_EPOCHS, SOAK_DURATION as a safety cap, EPOCH_LENGTH, PREMINE, CHAOS, ...). See
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
