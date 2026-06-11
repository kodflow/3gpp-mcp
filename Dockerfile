# syntax=docker/dockerfile:1
# =============================================================================
# 3gpp-mcp production image — TWO TARGETS (pass --target light|full):
#
#   light : binary + the lexical DB.zst baked from image-data/ (BM25 offline);
#           the DB is decompressed into place on first start by the entrypoint.
#   full  : binary + ONNX Runtime (arch-native, sha256-pinned) + the ~22 GB
#           fused corpus INHERITED from the pure-data image 3gpp-data via the
#           `corpus` stage + COPY --link — a code-only rebuild re-creates only
#           the small top layers; the data blob is reused by digest, so pushes
#           and pulls shrink from ~15 GB to ~150 MB (plan split-data-image).
#
# DATA_IMAGE is resolved BY DIGEST by corpus-image.yml (crane digest on
# 3gpp-data:latest) and stamped into the io.kodflow.3gpp.data.digest label;
# the workflow gate then asserts the pushed manifests reference the EXACT
# 3gpp-data blob (COPY --link is an optimisation, never a trusted guarantee).
#
# Default CMD is `serve` on stdio (the Claude-Code contract). Set
# MCP_TRANSPORT=http (+ MCP_PORT) for the HTTP/landing mode.
# =============================================================================

ARG DATA_IMAGE=scratch

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

# ---- corpus (full only): the pure-data base, resolved by digest --------------
# Copy-only stage — never executed, so the (arch-neutral) data image serves
# both platforms. With the scratch default (light builds) the stage is simply
# never reached; a full build without DATA_IMAGE fails loud on the COPY below.
FROM ${DATA_IMAGE} AS corpus

# ---- base runtime -------------------------------------------------------------
# glibc base (NOT scratch/alpine): DuckDB's static lib + libstdc++/libgomp.
# DIGEST-PINNED (split-data-image review): an unpinned tag can swap the base
# under our feet and churn the small layers' stability; bump deliberately.
FROM debian:bookworm-slim@sha256:96e378d7e6531ac9a15ad505478fcc2e69f371b10f5cdf87857c4b8188404716 AS base
# zstd: light decompresses its baked DB.zst on first start; wget: HEALTHCHECK +
# the full target's pinned ORT fetch. apt lists live in the cache mounts, not
# in the layer.
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt,sharing=locked \
    apt-get update -qq && \
    apt-get install -y --no-install-recommends \
      libstdc++6 libgomp1 ca-certificates wget zstd

# VARIANT is informational (light|full); contents come from the build target.
ARG VARIANT=light
LABEL org.opencontainers.image.variant="${VARIANT}"

# Non-root. uid/gid 10001 matches the ownership baked into the 3gpp-data layer
# (a runtime chown would rewrite ~22 GB).
RUN groupadd -g 10001 mcp && \
    useradd -u 10001 -g mcp -d /home/mcp -m -s /usr/sbin/nologin mcp && \
    install -d -o mcp -g mcp /data /data/mcp-3gpp

COPY --from=builder /out/mcp-3gpp /usr/local/bin/mcp-3gpp
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

ENV MCP3GPP_CACHE=/data/mcp-3gpp \
    MCP_TRANSPORT=stdio \
    MCP_PORT=8765
USER mcp:mcp
WORKDIR /home/mcp
# NO VOLUME declaration — deliberately (issue #124/#125): serve reads the baked
# data IN PLACE, read-only; a VOLUME would make Docker copy it into every
# named volume. Mount one explicitly only if you want a writable cache (light).

# Soft healthcheck: only meaningful in http mode; in stdio mode it is a no-op pass.
HEALTHCHECK --interval=30s --timeout=5s --start-period=120s --retries=3 \
  CMD sh -c '[ "$MCP_TRANSPORT" != "http" ] || wget -q --spider "http://127.0.0.1:${MCP_PORT}/healthz"' || exit 1

ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["serve"]

# ---- light: lexical DB.zst baked from the build context ----------------------
FROM base AS light
COPY --chown=mcp:mcp image-data/ /data/mcp-3gpp/

# ---- full: arch-native ORT (sha256-pinned) + the inherited data layer --------
FROM base AS full
ARG TARGETARCH
ARG ORT_VERSION=1.26.0
ARG ORT_SHA256_AMD64
ARG ORT_SHA256_ARM64
USER root
# ORT is the ONLY arch-specific piece, fetched from the Microsoft GitHub
# release (never rate-limited us) and verified against the same sha256 pins as
# scripts/fetch-model.sh — corpus-image.yml extracts and injects them so there
# is no second copy of the pin. Layout matches bootstrap.ORTLibPath:
# <cache>/models/onnxruntime/lib/libonnxruntime.so
RUN set -eu; \
    case "${TARGETARCH}" in \
      amd64) pkg="onnxruntime-linux-x64-${ORT_VERSION}";     sha="${ORT_SHA256_AMD64}" ;; \
      arm64) pkg="onnxruntime-linux-aarch64-${ORT_VERSION}"; sha="${ORT_SHA256_ARM64}" ;; \
      *) echo "unsupported TARGETARCH ${TARGETARCH}" >&2; exit 1 ;; \
    esac; \
    test -n "${sha}"; \
    wget -q -O /tmp/ort.tgz "https://github.com/microsoft/onnxruntime/releases/download/v${ORT_VERSION}/${pkg}.tgz"; \
    echo "${sha}  /tmp/ort.tgz" | sha256sum -c -; \
    install -d -o mcp -g mcp /data/mcp-3gpp/models /data/mcp-3gpp/models/onnxruntime; \
    tar -C /data/mcp-3gpp/models/onnxruntime --strip-components=1 -xzf /tmp/ort.tgz; \
    chown -R mcp:mcp /data/mcp-3gpp/models/onnxruntime; \
    rm /tmp/ort.tgz
USER mcp:mcp
# The ~22 GB corpus layer, inherited from 3gpp-data (ownership already baked
# there). --link = BuildKit reuse/rebase optimisation; the byte-identity of the
# blob is NOT assumed — the workflow gate asserts the pushed mcp manifests
# reference the EXACT 3gpp-data blob and fails loud otherwise.
COPY --link --from=corpus /data/mcp-3gpp /data/mcp-3gpp
