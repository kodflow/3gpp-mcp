# syntax=docker/dockerfile:1
# =============================================================================
# 3gpp-mcp production image — the MCP server, optionally with the merged corpus
# DB baked in. Built by .github/workflows/corpus-image.yml after it pulls every
# per-release GitHub Release DB and re-merges them (cmd/merge rebuilds FTS+HNSW).
#
#   docker build -t 3gpp-mcp .                       # lexical, DB bootstrapped at runtime
#   docker build --build-arg BUILD_TAGS=onnx ...     # semantic (needs ORT; model at runtime)
#   # the CI puts the reconstituted DB in image-data/ before build to BAKE it in.
#
# Default CMD is `serve` on stdio (the Claude-Code contract). Set MCP_TRANSPORT=http
# (+ MCP_PORT) for the HTTP/landing mode. The DB is NEVER fetched at build time
# unless image-data/ carries it; otherwise serve bootstraps it from the rolling
# binary-only `latest` channel on first run into the /data volume.
# =============================================================================

# ---- builder ----------------------------------------------------------------
FROM golang:1.26-bookworm AS builder
ARG BUILD_TAGS=""
ARG VERSION=dev
WORKDIR /src

# Module cache layer (re-used across source-only changes).
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/root/go/pkg/mod go mod download

COPY . .
# DuckDB links statically via duckdb-go-bindings (no runtime .so). GOTOOLCHAIN=local
# pins the go.mod toolchain. -trimpath + -s -w keep the binary lean.
RUN --mount=type=cache,target=/root/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 GOTOOLCHAIN=local \
    go build -tags "${BUILD_TAGS}" -trimpath \
      -ldflags="-s -w -X main.Version=${VERSION}" \
      -o /out/mcp-3gpp ./cmd/server

# ---- runtime ----------------------------------------------------------------
# glibc base (NOT scratch/alpine): DuckDB's static lib + libstdc++/libgomp.
FROM debian:bookworm-slim AS runtime
# zstd: the FULL image bakes the corpus DB + vector sub-bases as .zst (small image);
# the entrypoint decompresses them into /data on first start.
RUN apt-get update -qq && \
    apt-get install -y --no-install-recommends \
      libstdc++6 libgomp1 ca-certificates wget zstd && \
    rm -rf /var/lib/apt/lists/*

# VARIANT is informational (light|full); the actual contents come from BUILD_TAGS
# (onnx for full) and whether image-data/ was populated by the CI before the build.
ARG VARIANT=light
LABEL org.opencontainers.image.variant="${VARIANT}"

# Non-root.
RUN groupadd -g 10001 mcp && \
    useradd -u 10001 -g mcp -d /home/mcp -m -s /usr/sbin/nologin mcp

COPY --from=builder /out/mcp-3gpp /usr/local/bin/mcp-3gpp
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

# image-data/ holds the baked merged DB when the CI built one (else just .gitkeep);
# it lands at the bootstrap cache path so `serve` finds it with no network on boot.
COPY --chown=mcp:mcp image-data/ /data/mcp-3gpp/

ENV MCP3GPP_CACHE=/data/mcp-3gpp \
    MCP_TRANSPORT=stdio \
    MCP_PORT=8765
RUN chown -R mcp:mcp /data
USER mcp:mcp
WORKDIR /home/mcp
VOLUME ["/data"]

# Soft healthcheck: only meaningful in http mode; in stdio mode it is a no-op pass.
HEALTHCHECK --interval=30s --timeout=5s --start-period=120s --retries=3 \
  CMD sh -c '[ "$MCP_TRANSPORT" != "http" ] || wget -q --spider "http://127.0.0.1:${MCP_PORT}/healthz"' || exit 1

ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["serve"]
