# Development Container
# Uses pre-built base image from GHCR (includes all tools + Claude Code)
# Languages are added via devcontainer features as needed

# hadolint ignore=DL3007
FROM ghcr.io/kodflow/devcontainer-template:latest

# =============================================================================
# 3GPP MCP build/run dependencies — persisted so a devcontainer REBUILD works
# =============================================================================
# The GHCR base image ships Claude Code + generic tooling but NOT: a Go
# toolchain, zstd, or the Lightning/Kaggle CLIs the embed pipeline needs. The
# devcontainer "go" feature has proven unreliable here, so we install these
# explicitly and pin them. This project does not run /update, so this file is
# authoritative and will not be overwritten.

USER root

# Go toolchain (CGO: DuckDB + ONNX Runtime). MUST match go.mod (go 1.26.3).
ARG GO_VERSION=1.26.3
# hadolint ignore=DL4006
RUN curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" -o /tmp/go.tgz && \
    rm -rf /usr/local/go && tar -C /usr/local -xzf /tmp/go.tgz && rm -f /tmp/go.tgz && \
    /usr/local/go/bin/go version

# zstd: decompress the published `latest` DuckDB snapshot (3gpp.duckdb.zst).
# python3-venv: isolated env for the GPU-embed launcher CLIs below.
# hadolint ignore=DL3008
RUN apt-get update && apt-get install -y --no-install-recommends zstd python3-venv && \
    rm -rf /var/lib/apt/lists/*

USER vscode

# Lightning AI + Kaggle CLIs/SDK for the GPU embed launchers, isolated in a venv
# so they never collide with the system Python. Versions pinned for reproducibility.
ARG LIGHTNING_SDK_VERSION=2026.05.21
ARG KAGGLE_VERSION=2.2.0
RUN python3 -m venv /home/vscode/.venvs/embed-tools && \
    /home/vscode/.venvs/embed-tools/bin/pip install --no-cache-dir --upgrade pip && \
    /home/vscode/.venvs/embed-tools/bin/pip install --no-cache-dir \
      "lightning-sdk==${LIGHTNING_SDK_VERSION}" "kaggle==${KAGGLE_VERSION}"

# Go + the embed-tools venv on PATH (image-level; docker-compose also pins these).
ENV PATH=/usr/local/go/bin:/home/vscode/.venvs/embed-tools/bin:/home/vscode/.local/bin:$PATH
