.PHONY: build build-release check-release-stamping test fmt lint vet vuln tidy consensus-vectors proto proto-descriptor localnet-init localnet-smoke localnet-rewards-smoke localnet-rewards-epoch-smoke localnet-settlement-smoke localnet-quorum-table localnet-validator-growth localnet-validator-departures validator-set-study localnet-join-and-settle localnet-settlement-matrix localnet-upgrade-drill localnet-export-restore-drill localnet-export-restore-faults localnet-rewards-soak localnet-agree \
	api-smoke drill-lifecycle drill-restart-rotation drill-quorum drills

# Version and commit are stamped at link time; the chain and binary names are
# compiled in (see cmd/twilightd/main.go) so even an unstamped build identifies
# itself.
#
# Dirtiness is derived separately from VERSION and is NOT overridable. Carrying
# it inside VERSION meant an explicit `VERSION=v0.1.0` replaced the whole
# `git describe --dirty` expression and silently dropped the marker: the binary
# then claimed to be exactly COMMIT while the source differed from it, and the
# checksum hashed that artifact faithfully without being able to disclose the
# mismatch. So `--dirty` comes off git describe, and the marker is appended to
# whatever VERSION says — default and explicit builds behave identically.
#
# `override` because a GNU Make command-line assignment beats any assignment in
# the makefile: `make build DIRTY=` blanked the marker and stamped a modified
# tree as clean. Provenance must not be something the caller can switch off.
#
# Dirty means tracked modifications, or ANY untracked file outside the allowlist.
#
# Enumerating build-relevant extensions does not close: the Go toolchain also
# consumes .s, .c, .h and .syso, and //go:embed can pull in a file of any
# extension at all. An untracked .s under cmd/twilightd changed the binary while
# a .go-only check reported clean. So the rule is default-deny, and the allowlist
# names what is known to be safe rather than guessing what is dangerous.
#
# docs/specs/ is the one entry: it is user-owned material this project keeps
# untracked by convention and the compiler cannot reach it. Everything gitignored
# — build/ included — is already excluded by --exclude-standard.
override ALLOWED_UNTRACKED := ':(exclude)docs/specs'
#
# go.work/go.work.sum are checked BY NAME because .gitignore lists them, and
# --exclude-standard skips ignored files — so the default-deny rule above is
# structurally blind to exactly the file that can redirect the whole module.
override DIRTY := $(shell \
  { git diff-index --quiet HEAD -- 2>/dev/null \
    && test -z "$$(git ls-files --others --exclude-standard -- . $(ALLOWED_UNTRACKED) 2>/dev/null)" \
    && test ! -e go.work && test ! -e go.work.sum; } \
  || echo -dirty)
VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo unknown)
COMMIT  ?= $(shell git rev-parse HEAD 2>/dev/null || echo unknown)
BUILD_TAGS ?=
# The effective stamp. Always carries the dirty marker when the tree is dirty.
STAMP = $(VERSION)$(DIRTY)
LDFLAGS = -X github.com/cosmos/cosmos-sdk/version.Version=$(STAMP) \
          -X github.com/cosmos/cosmos-sdk/version.Commit=$(COMMIT) \
          -X github.com/cosmos/cosmos-sdk/version.BuildTags=$(BUILD_TAGS)

build:
	go build -ldflags '$(LDFLAGS)' -o build/twilightd ./cmd/twilightd

# Release artifacts for the platforms validators run, plus one developer target.
# CGO_ENABLED=0 keeps them static on the default goleveldb backend; RocksDB is an
# indirect dependency and is not compiled in without its build tag.
#
# The checksum file is the artifact operators actually verify: cosmovisor runs
# with DAEMON_ALLOW_DOWNLOAD_BINARIES=false, so a pre-staged binary is trusted
# because its hash matches, not because of where it came from.
RELEASE_DIR ?= build/release
RELEASE_TARGETS = linux/amd64 linux/arm64 darwin/arm64

# Delegated to a script so the guards run in shell rather than as Make variables,
# which a command-line assignment can override. The script also builds from
# `git archive HEAD` rather than the worktree, so an artifact is the commit it
# claims by construction and untracked files cannot reach the compiler.
build-release:
	@RELEASE_DIR=$(RELEASE_DIR) RELEASE_TARGETS="$(RELEASE_TARGETS)" \
	  VERSION=$(VERSION) BUILD_TAGS=$(BUILD_TAGS) ./scripts/build-release.sh

# Provenance checks for the stamping and release targets above. Fast, and needs
# a clean tree because it deliberately dirties a tracked file and restores it.
check-release-stamping:
	./scripts/check-release-stamping.sh

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

# Three Slots over three epochs. Membership moves in both directions with
# settlements outstanding behind it, every settlement bound is pushed, and both
# finalization arms are reached — including the deadline, which no other run gets
# to. Long by nature: three epoch boundaries plus a 720-block window.
localnet-settlement-matrix:
	./scripts/localnet/settlement-lifecycle-matrix.sh

# #108: characterizes export, restore and fresh-node join. The export is taken
# deliberately mid-epoch, where per-slot participation for the open epoch is
# non-zero, because a boundary export has nothing in progress to lose.
localnet-export-restore-drill:
	./scripts/localnet/export-restore-drill.sh

# Fast, chain-free negative tests for the outcome classifiers the drill uses.
localnet-export-restore-faults:
	./scripts/localnet/lib/drill-assert-selftest.sh
	./scripts/localnet/export-restore-drill-faults.sh

# The operational half of the x/upgrade proof: four validators, two binaries, a
# real coordinated halt and a partial rollout. Deliberately NOT part of `drills`
# — it is minutes long by nature (a protocol epoch must close so a settlement
# spans the boundary) and would make the routine drill set too slow to run often.
localnet-upgrade-drill:
	./scripts/localnet/upgrade-drill.sh

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
