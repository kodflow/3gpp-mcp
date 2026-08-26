SHELL    := /bin/bash

BIN      := mcp-3gpp
PKG      := github.com/kodflow/3gpp-mcp
GO       := go
GOFLAGS  := -trimpath
LDFLAGS  := -s -w -X main.Version=$$(git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD_DIR := bin

ORT_LIB  ?= $(CURDIR)/data/models/onnxruntime/lib/libonnxruntime.so

.PHONY: all build build-onnx build-ffi ingest ingest-onnx ingest-openapi ingest-catalog fetch-apis serve test embed-smoke poc bench benchgo demo audit model lint fmt vet tidy clean install help light-artifacts image-light convert-smoke

all: build ## Build the binary

build: ## Build static binary into bin/
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

serve: build ## Start MCP server on stdio
	./$(BUILD_DIR)/$(BIN) serve

test: ## Run unit tests with race detector
	$(GO) test -race -count=1 ./...

embed-smoke: ## Prove the embed pipeline works locally (no Kaggle/GPU; uses BGE-M3 on CPU if present)
	./scripts/embed-local-smoke.sh

poc: ## Run the Lawful-Interception POC end-to-end test (verbose)
	$(GO) test ./tests/e2e -run LIEvents -v

audit: ## Cross-check the external LI catalogue vs the indexed normative text
	$(GO) run ./cmd/li-audit

demo: build ## Drive the MCP server over stdio and show every tool's output
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

install: build ## Install binary to $$GOBIN (or $$GOPATH/bin)
	install -m 0755 $(BUILD_DIR)/$(BIN) $${GOBIN:-$$(go env GOPATH)/bin}/$(BIN)

convert-smoke: ## Prove the convert fallback chain recovers the hardest specs (needs LibreOffice + pandoc/antiword/catdoc)
	./scripts/convert-smoke.sh

light-artifacts: ## Emit the 2 .zst (full lexical DB + embedding delta) from LEX=<lexical.duckdb>
	./scripts/light-artifacts.sh

image-light: ## Build the lexical (no-embed) runtime image 3gpp-mcp:light with LEX baked in
	@test -s "$${LEX:-data/3gpp.lexical.duckdb}" || { echo "set LEX to a lexical DB (merge --strip-embeddings)"; exit 1; }
	mkdir -p image-data && cp "$${LEX:-data/3gpp.lexical.duckdb}" image-data/3gpp.duckdb
	docker build --target light -t 3gpp-mcp:light .
	rm -f image-data/3gpp.duckdb
	@echo "built 3gpp-mcp:light (lexical DB baked) — run: docker run -i --rm 3gpp-mcp:light serve"

help: ## List targets
	@awk 'BEGIN{FS=":.*##"; printf "\nTargets:\n"} /^[a-zA-Z_-]+:.*##/ {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

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
