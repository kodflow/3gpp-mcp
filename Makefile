SHELL    := /bin/bash

BIN      := mcp-3gpp
PKG      := github.com/kodflow/3gpp-mcp
GO       := go
GOFLAGS  := -trimpath
LDFLAGS  := -s -w -X main.Version=$$(git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD_DIR := bin

ORT_LIB  ?= $(CURDIR)/data/models/onnxruntime/lib/libonnxruntime.so

.PHONY: all build build-bin plan steps status publish prove build-onnx build-ffi ingest ingest-onnx ingest-openapi ingest-catalog fetch-apis serve test embed-smoke poc bench benchgo demo audit model lint fmt vet tidy clean install help convert-smoke

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
# ONE WARNING WORTH THE LINE. `goal run` with no -only will re-run `seed`, which
# cascades discover/fetch/ingest/merge and re-downloads 20 163 spec versions from
# 3gpp.org. That is correct from a clean checkout and expensive on a machine that
# already holds the corpus. `make plan` prints what it intends to do, and why, so
# you find that out before it starts rather than after.

build: ## Build EVERYTHING: binaries, corpus, vectors, sparse, compaction, index, validation
	@test -x .local/bin/goal || $(GO) build -tags "$${GOTAGS:-duckdb_use_lib}" -o .local/bin/goal ./cmd/goal
	.local/bin/goal run

plan: ## What `make build` would do, and why — without doing it
	@test -x .local/bin/goal || $(GO) build -tags "$${GOTAGS:-duckdb_use_lib}" -o .local/bin/goal ./cmd/goal
	.local/bin/goal plan

steps: plan ## Every pipeline step, in DAG order, with what it would do and why

# ONE RULE, EVERY STEP — including the ones added after this line was written.
#
#   make build/fetch     make build/embed     make build/sparse
#   make build/ingest    make build/index     make build/compact
#
# `make steps` prints the authoritative list; there is no second copy of it here
# to drift out of date. Dependencies are NOT run: `-only` executes exactly the
# step you name, using the recorded state of everything it depends on — which is
# the point of asking for one step.
#
# Two names people reach for that do not exist, because the pipeline's real seams
# are elsewhere:
#   `convert` — `fetch` downloads AND converts to HTML (LibreOffice) in one step;
#               `ingest` is what parses that HTML into shards.
#   `download` — that is `fetch` too.
build/%: ## Run ONE pipeline step: make build/fetch, build/ingest, build/embed, build/sparse, build/index … (`make steps` lists them)
	@test -x .local/bin/goal || $(GO) build -tags "$${GOTAGS:-duckdb_use_lib}" -o .local/bin/goal ./cmd/goal
	.local/bin/goal run -only $*

status: ## Per-step state of the last run
	@test -x .local/bin/goal || $(GO) build -tags "$${GOTAGS:-duckdb_use_lib}" -o .local/bin/goal ./cmd/goal
	.local/bin/goal status

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
