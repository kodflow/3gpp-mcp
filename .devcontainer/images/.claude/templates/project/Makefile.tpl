.PHONY: all build test lint fmt clean help

# {{PROJECT_NAME}} Makefile

{{#IF_GO}}
# Go targets
GO := go
GOFLAGS := -v

.PHONY: build
build:
	$(GO) build $(GOFLAGS) ./...

.PHONY: test
test:
	$(GO) test -race -cover ./...

.PHONY: lint
lint:
	golangci-lint run ./...

.PHONY: fmt
fmt:
	$(GO) fmt ./...
	goimports -w .

.PHONY: clean
clean:
	$(GO) clean
	rm -rf bin/
{{/IF_GO}}

{{#IF_RUST}}
# Rust targets
CARGO := cargo

.PHONY: build
build:
	$(CARGO) build

.PHONY: test
test:
	$(CARGO) test

.PHONY: lint
lint:
	$(CARGO) clippy -- -D warnings

.PHONY: fmt
fmt:
	$(CARGO) fmt

.PHONY: clean
clean:
	$(CARGO) clean
{{/IF_RUST}}

{{#IF_NODE}}
# Node.js targets
NPM := npm

.PHONY: build
build:
	$(NPM) run build

.PHONY: test
test:
	$(NPM) test

.PHONY: lint
lint:
	$(NPM) run lint

.PHONY: fmt
fmt:
	$(NPM) run format

.PHONY: clean
clean:
	rm -rf node_modules dist
{{/IF_NODE}}

{{#IF_PYTHON}}
# Python targets
PYTHON := python
PIP := pip

.PHONY: build
build:
	$(PIP) install -e .

.PHONY: test
test:
	pytest -v --cov

.PHONY: lint
lint:
	ruff check .
	mypy .

.PHONY: fmt
fmt:
	ruff format .

.PHONY: clean
clean:
	rm -rf __pycache__ .pytest_cache .mypy_cache dist *.egg-info
{{/IF_PYTHON}}

{{#IF_INFRA}}
# Infrastructure targets (Terraform / Terragrunt / Ansible)
TG := terragrunt

.PHONY: plan
plan:
	$(TG) run-all plan

.PHONY: apply
apply:
	$(TG) run-all apply

.PHONY: test
test:
	go test -v -timeout 30m ./tests/...
	molecule test

.PHONY: lint
lint:
	terraform fmt -check -recursive
	terraform validate
	tflint --recursive
	ansible-lint

.PHONY: fmt
fmt:
	terraform fmt -recursive

.PHONY: cost
cost:
	infracost breakdown --path .

.PHONY: drift
drift:
	$(TG) run-all plan -detailed-exitcode

.PHONY: configure
configure:
	ansible-playbook site.yml

.PHONY: bootstrap
bootstrap:
	ansible-playbook bootstrap.yml

.PHONY: clean
clean:
	find . -name ".terraform" -type d -exec rm -rf {} + 2>/dev/null || true
	rm -f tfplan *.tfstate.backup
{{/IF_INFRA}}

# Common targets
.PHONY: help
help:
	@echo "{{PROJECT_NAME}} - Available targets:"
	@echo "  make build   - Build the project"
	@echo "  make test    - Run tests"
	@echo "  make lint    - Run linters"
	@echo "  make fmt     - Format code"
	@echo "  make clean   - Clean build artifacts"
