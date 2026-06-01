SHELL    := /bin/bash
BIN      := mcp-3gpp
PKG      := github.com/kodflow/3gpp-mcp
GO       := go
GOFLAGS  := -trimpath
LDFLAGS  := -s -w -X main.Version=$$(git describe --tags --always --dirty 2>/dev/null || echo dev)
BUILD_DIR := bin

ORT_LIB  ?= $(CURDIR)/data/models/onnxruntime/lib/libonnxruntime.so

.PHONY: all build build-onnx ingest ingest-onnx ingest-openapi ingest-catalog fetch-apis serve test embed-smoke poc bench benchgo demo audit model lint fmt vet tidy clean install help

all: build ## Build the binary

build: ## Build static binary into bin/
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 $(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(BIN) ./cmd/server

build-onnx: ## Build with the real BGE-M3 semantic backend (run `make model` first)
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 $(GO) build $(GOFLAGS) -tags onnx -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(BIN) ./cmd/server

ingest: ## Build and run one-shot ingestion CLI
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 $(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(BIN)-ingest ./cmd/ingest
	./$(BUILD_DIR)/$(BIN)-ingest $(ARGS)

ingest-onnx: ## Build + ingest with real BGE-M3 vectors (run `make model` first)
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 $(GO) build $(GOFLAGS) -tags onnx -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(BIN)-ingest ./cmd/ingest
	ONNXRUNTIME_SHARED_LIBRARY_PATH=$(ORT_LIB) ./$(BUILD_DIR)/$(BIN)-ingest $(ARGS)

fetch-apis: ## Fetch the 5GC OpenAPI YAML corpus from 3GPP Forge (ARGS=releases)
	./scripts/fetch-5g-apis.sh $(ARGS)

ingest-openapi: ## Build + load the 5GC OpenAPI corpus into the DuckDB (additive)
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 $(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(BIN)-ingest-openapi ./cmd/ingest-openapi
	./$(BUILD_DIR)/$(BIN)-ingest-openapi $(ARGS)

ingest-catalog: ## Build + overlay authoritative DynaReport metadata (additive)
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=1 $(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o $(BUILD_DIR)/$(BIN)-ingest-catalog ./cmd/ingest-catalog
	./$(BUILD_DIR)/$(BIN)-ingest-catalog $(ARGS)

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

help: ## List targets
	@awk 'BEGIN{FS=":.*##"; printf "\nTargets:\n"} /^[a-zA-Z_-]+:.*##/ {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)
