SHELL    := /bin/bash

BIN      := mcp-3gpp
PKG      := github.com/kodflow/3gpp-mcp
GO       := go
GOFLAGS  := -trimpath
LDFLAGS  := -s -w -X main.Version=$$(git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD_DIR := bin

ORT_LIB  ?= $(CURDIR)/data/models/onnxruntime/lib/libonnxruntime.so

# ---------------------------------------------------------------------------
# The orchestrator, and the two rules that keep it honest. Defined ONCE, here,
# and reused by mk/local.mk — two copies of this drifted apart before, and the
# copy that lost was the one people actually typed.
#
# GOAL_ENV: every goal target sources the toolchain prelude. It is not optional
# and it is not the caller's job to remember. Without it `go` is not on PATH, the
# `toolchain` step fails its validation, and because that step is upstream of
# everything the plan turns into "26 steps, 15 heavy" — a full re-download of
# 20 163 spec versions, offered as if it were a rebuild. `make build` must not
# have a mode where forgetting one `source` costs a day.
#
# GOAL: the .exe suffix is load-bearing on Windows. The old recipes guarded with
# `test -x .local/bin/goal`, which matched a FOUR-DAY-OLD extensionless leftover
# and skipped the build, so `make build` ran an orchestrator that predated the
# fixes it was supposed to apply.
GOAL_ENV := . scripts/local/toolchain-env.sh
GOAL     := .local/bin/goal$(if $(filter Windows_NT,$(OS)),.exe,)

.PHONY: all build build-bin goal-bin plan steps status publish prove build-onnx build-ffi ingest ingest-onnx ingest-openapi ingest-catalog fetch-apis serve test embed-smoke poc bench benchgo demo audit model lint fmt vet tidy clean install help convert-smoke

all: build ## Build EVERYTHING (the corpus pipeline)

# ============================================================================
#  build / publish — the two entry points. Everything below is a detail of one.
# ============================================================================
#
# `goal` IS the pipeline, and it already does the two things that make a rebuild
# survivable: every step fingerprints its inputs, its implementation files and its
# declared environment, so a re-run does only what actually changed; and the heavy
# steps are resumable, because the embed and sparse ledgers are append-only and
# keyed by chunk_id. A killed run continues where it stopped. That is what makes
# `make build` safe to just re-run.
#
# WHAT "ONLY WHAT CHANGED" MEANS, PRECISELY — because the honest version of this
# paragraph used to be a warning that `make build` would re-download 20 163 spec
# versions on a machine that already held them.
#
#   - A step replays when ITS OWN determinants move: the sources it names in Impl,
#     the data it declares as Inputs, its configuration, its declared Version.
#   - A step replays when a DATA dependency actually produced something different.
#     "Different" is measured on what the dependency emitted, not on the fact that
#     it ran: a step that declined, or that rewrote its output identically, leaves
#     everything behind it alone.
#   - A step does NOT replay because a BINARY it launches was relinked. Build steps
#     are tools, not provenance. Editing an orchestration file rebuilds `goal` and
#     stops there; it does not re-download the corpus and it does not re-run the
#     GPU.
#
# `make plan` prints the decision and the reason for every step, and changes
# nothing — so you find out what a run intends before it starts, not after.

# goal-bin ALWAYS rebuilds. It is seconds, and the alternative is the failure
# this repository hit five times with five different binaries: a corrected source
# on disk, a stale executable next to it, and a run that reports success while
# doing the old thing. A conditional guard here is how the orchestrator ITSELF
# joined that list.
goal-bin: ## (re)build the pipeline orchestrator — always, never conditionally
	@set -e; $(GOAL_ENV); mkdir -p .local/bin; \
	  go build $${GOTAGS:+-tags $$GOTAGS} -o "$(GOAL)" ./cmd/goal

build: goal-bin ## Build EVERYTHING: binaries, corpus, vectors, sparse, compaction, index, validation
	@$(GOAL_ENV); "$(GOAL)" run $(ARGS)

plan: goal-bin ## What `make build` would do, and why — without doing it
	@$(GOAL_ENV); "$(GOAL)" plan $(ARGS)

steps: plan ## Every pipeline step, in DAG order, with what it would do and why

# ONE RULE, EVERY STEP — including the ones added after this line was written.
#
#   make build/fetch     make build/embed     make build/sparse
#   make build/ingest    make build/index     make build/compact
#
# `make steps` prints the authoritative list; there is no second copy of it here
# to drift out of date.
#
# DATA dependencies are not run: `make build/embed` uses the recorded state of the
# corpus rather than rebuilding it, which is the point of asking for one step.
# TOOL dependencies ARE run, and that is not a contradiction — it is what makes
# the step do what your working tree says. Fixing sparse_embed.rs and then running
# `make build/sparse` used to launch yesterday's binary and report success; the
# build steps are seconds and skip when nothing changed, so they are simply always
# brought up to date first.
#
# Two names people reach for that do not exist, because the pipeline's real seams
# are elsewhere:
#   `convert` — `fetch` downloads AND converts to HTML (LibreOffice) in one step;
#               `ingest` is what parses that HTML into shards.
#   `download` — that is `fetch` too.
build/%: goal-bin ## Run ONE pipeline step: make build/fetch, build/ingest, build/embed, build/sparse, build/index … (`make steps` lists them)
	@$(GOAL_ENV); "$(GOAL)" run -only $* $(ARGS)

status: goal-bin ## Per-step state of the last run
	@$(GOAL_ENV); "$(GOAL)" status

# PUBLISH IS LOCAL. The image used to be baked by two workflows that moved ~14 GB
# per run; they are gone. `make image` cross-compiles the Linux artefacts here
# (zig — no Docker, no WSL) and composes the OCI image with crane.
publish: image ## Build the image from the corpus `make build` produced and push it to GHCR

image: ## Build the full image locally and push it to GHCR (:latest)
	@test -s data/3gpp.duckdb || { echo "no corpus at data/3gpp.duckdb — run: make build"; exit 1; }
	./scripts/local/build-image.sh

image-local: ## Same, assembled into .local/image/image.tar without pushing
	./scripts/local/build-image.sh --no-push

image-toolchain: ## Fetch the Linux cross-toolchain the image build needs (zig + Debian libstdc++)
	./scripts/local/fetch-linux-toolchain.sh

prove: ## Drive server-full over real JSON-RPC and assert every retrieval arm is live on BOTH halves
	./scripts/local/prove-serving.sh

build-bin: ## Build the server binary alone into bin/ (no corpus)
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 $(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(BIN) ./cmd/server

build-onnx: ## Build with the real BGE-M3 semantic backend (run `make model` first)
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 $(GO) build $(GOFLAGS) -tags onnx,embed_ffi -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(BIN) ./cmd/server

build-ffi: ## Build serve on the Rust embed-core cdylib (FFI query-embed; Phase 11 target). Add `--features ort` to the cargo line for the real BGE-M3 backend.
	@mkdir -p $(BUILD_DIR)
	cargo build --release --manifest-path rust/embed-core/Cargo.toml
	CGO_ENABLED=1 $(GO) build $(GOFLAGS) -tags embed_ffi -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(BIN) ./cmd/server

ingest: ## Build and run the Rust ingest (parse3gpp + store-rs)
	cargo build --release --manifest-path rust/ingest/Cargo.toml --bin ingest
	./rust/target/release/ingest $(ARGS)

fetch-apis: ## Fetch the 5GC OpenAPI YAML corpus from 3GPP Forge (ARGS=releases)
	./scripts/fetch-5g-apis.sh $(ARGS)

ingest-openapi: ## Build + load the 5GC OpenAPI corpus into the DuckDB (additive; Rust)
	cargo build --release --manifest-path rust/ingest/Cargo.toml --bin ingest-openapi
	./rust/target/release/ingest-openapi --src data/sources/5g-apis --db data/3gpp.duckdb

ingest-catalog: ## Build + overlay authoritative DynaReport metadata (additive; Rust)
	cargo build --release --manifest-path rust/ingest/Cargo.toml --bin ingest-catalog
	./rust/target/release/ingest-catalog $(ARGS)

serve: build-bin ## Start MCP server on stdio
	./$(BUILD_DIR)/$(BIN) serve

test: ## Run unit tests with race detector
	$(GO) test -race -count=1 ./...

embed-smoke: ## Prove the embed pipeline works locally (no Kaggle/GPU; uses BGE-M3 on CPU if present)
	./scripts/embed-local-smoke.sh

poc: ## Run the Lawful-Interception POC end-to-end test (verbose)
	$(GO) test ./tests/e2e -run LIEvents -v

audit: ## Cross-check the external LI catalogue vs the indexed normative text
	$(GO) run ./cmd/li-audit

demo: build-bin ## Drive the MCP server over stdio and show every tool's output
	./scripts/demo.sh

model: ## Bootstrap the real ONNX BGE-M3 semantic backend (~2.3GB, optional)
	./scripts/fetch-model.sh

bench: ## Run the retrieval eval harness (axis #7) — ARGS for -db/-set; EMBEDDER/RERANKER env
	$(GO) run ./cmd/bench $(ARGS)

benchgo: ## Run Go micro-benchmarks
	$(GO) test -bench=. -benchmem ./...

lint: ## Run golangci-lint
	golangci-lint run ./...

fmt: ## Format Go sources
	$(GO) fmt ./...
	goimports -w .

vet: ## Run go vet
	$(GO) vet ./...

tidy: ## Tidy module dependencies
	$(GO) mod tidy

clean: ## Remove build artefacts
	rm -rf $(BUILD_DIR)

install: build-bin ## Install binary to $$GOBIN (or $$GOPATH/bin)
	install -m 0755 $(BUILD_DIR)/$(BIN) $${GOBIN:-$$(go env GOPATH)/bin}/$(BIN)

convert-smoke: ## Prove the convert fallback chain recovers the hardest specs (needs LibreOffice + pandoc/antiword/catdoc)
	./scripts/convert-smoke.sh



help: ## List targets
	@awk 'BEGIN{FS=":.*##"; printf "\nTargets:\n"} /^[a-zA-Z_\/%-]+:.*##/ {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

inspect-layers: ## Per-platform layers of the published :latest + the 3gpp-data blob (dedupe eyeball; needs crane + GHCR login)
	@DATA_PM=$$(crane manifest ghcr.io/kodflow/3gpp-data:latest | jq -r '.manifests[0].digest // empty'); \
	if [ -n "$$DATA_PM" ]; then DATA_LAYER=$$(crane manifest ghcr.io/kodflow/3gpp-data@$$DATA_PM | jq -r '.layers[-1].digest'); \
	else DATA_LAYER=$$(crane manifest ghcr.io/kodflow/3gpp-data:latest | jq -r '.layers[-1].digest'); fi; \
	echo "3gpp-data blob: $$DATA_LAYER"; \
	for d in $$(crane manifest ghcr.io/kodflow/3gpp-mcp:latest | jq -r '.manifests[] | select(.platform.os=="linux") | .digest'); do \
	  echo "== 3gpp-mcp@$$d =="; \
	  crane manifest ghcr.io/kodflow/3gpp-mcp@$$d | jq -r --arg l "$$DATA_LAYER" '.layers[] | (if .digest == $$l then "DATA→ " else "      " end) + (.size|tostring) + "  " + .digest'; \
	done

# ---------------------------------------------------------------------------
# Indexation locale (GPU du poste) — remplace Kaggle + les 5 workflows corpus.
# Inclus EN FIN de fichier pour que `all` reste la cible par defaut.
-include mk/local.mk
