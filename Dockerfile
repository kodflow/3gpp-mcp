# syntax=docker/dockerfile:1
# =============================================================================
# 3gpp-mcp production image — TWO TARGETS (pass --target light|full):
#
#   light : binary + the lexical DB.zst baked from image-data/ (BM25 offline);
#           the DB is decompressed into place on first start by the entrypoint.
#   full  : binary + ONNX Runtime (arch-native, sha256-pinned) + the ~14 GB
#           fused corpus INHERITED from the data image 3gpp-data via
#           `FROM ${DATA_IMAGE}`. Inheritance (NOT COPY) is what makes full's
#           manifest REFERENCE 3gpp-data's data layer by digest, so a code-only
#           rebuild re-creates only the small top layers and pushes/pulls shrink
#           from ~15 GB to ~150 MB (plan split-data-image).
#
# WHY FROM and not COPY: `COPY --from` (even `--link`) re-tars the files into a
# NEW content-addressed layer — it never shares the source image's blob (the
# gate caught this: per-arch 14 GB layers with fresh digests). Only `FROM`
# inheritance lists the base's layers verbatim in the child manifest. The data
# image is therefore arch-specific and multi-arch (one data blob per platform);
# buildx picks the matching arch for each native build leg.
#
# DATA_IMAGE is resolved BY DIGEST by corpus-image.yml (crane digest on
# 3gpp-data:latest) and stamped into io.kodflow.3gpp.data.digest; the workflow
# gate then asserts each pushed platform manifest references that arch's exact
# 3gpp-data data blob.
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
# When BUILD_TAGS carries embed_ffi, build the rust/embed-core cdylib FIRST — it is
# the cgo link target for serve's query-embed. ort is `load-dynamic` (no bundled
# onnxruntime): the cdylib dlopens the image's libonnxruntime.so at run time
# (ORT_DYLIB_PATH, set in the runtime stage), SHARING it with the Go reranker so the
# process never loads two onnxruntimes. Verified live on Kaggle (FFI-CHECK OK).
RUN --mount=type=cache,target=/root/.cargo/registry \
    --mount=type=cache,target=/root/.cargo/git \
    mkdir -p /out/lib; \
    if echo " ${BUILD_TAGS} " | grep -q embed_ffi; then \
      curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y --default-toolchain stable --profile minimal; \
      "$HOME/.cargo/bin/cargo" build --release --features ort \
        --manifest-path rust/embed-core/Cargo.toml; \
      cp rust/embed-core/target/release/libembed_core.so /out/lib/libembed_core.so; \
    fi
# DuckDB links statically via duckdb-go-bindings (no runtime .so). GOTOOLCHAIN=local
# pins the go.mod toolchain. -trimpath + -s -w keep the binary lean.
RUN --mount=type=cache,target=/root/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 GOTOOLCHAIN=local \
    go build -tags "${BUILD_TAGS}" -trimpath \
      -ldflags="-s -w -X main.Version=${VERSION}" \
      -o /out/mcp-3gpp ./cmd/server

# ---- base runtime (LIGHT) ----------------------------------------------------
# glibc base (NOT scratch/alpine): DuckDB's static lib needs libstdc++/libgomp.
# DIGEST-PINNED: an unpinned tag can swap the base under our feet and churn the
# small layers' stability; bump deliberately. This is ALSO the base of the
# 3gpp-data image (Dockerfile.data), so full — which inherits 3gpp-data — shares
# the identical debian base layer.
FROM debian:bookworm-slim@sha256:96e378d7e6531ac9a15ad505478fcc2e69f371b10f5cdf87857c4b8188404716 AS base
# zstd: light decompresses its baked DB.zst on first start; wget: HEALTHCHECK +
# the full target's pinned ORT fetch. apt lists live in the cache mounts.
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt,sharing=locked \
    apt-get update -qq && \
    apt-get install -y --no-install-recommends \
      libstdc++6 libgomp1 ca-certificates wget zstd

ARG VARIANT=light
LABEL org.opencontainers.image.variant="${VARIANT}"

# Non-root. uid/gid 10001 matches the ownership baked into the 3gpp-data layer.
RUN groupadd -g 10001 mcp && \
    useradd -u 10001 -g mcp -d /home/mcp -m -s /usr/sbin/nologin mcp && \
    install -d -o mcp -g mcp /data /data/mcp-3gpp

COPY --from=builder /out/mcp-3gpp /usr/local/bin/mcp-3gpp
# The embed-core cdylib (present only when BUILD_TAGS carries embed_ffi; an empty
# dir otherwise). load-dynamic → it links no onnxruntime, so the binary loads it at
# startup without ORT; onnxruntime is dlopened lazily on the first query-embed via
# ORT_DYLIB_PATH (set where ORT is present — full/fulltop). ldconfig registers it.
COPY --from=builder /out/lib/ /usr/local/lib/
RUN ldconfig
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

# Bake the DuckDB loadable extensions (fts, vss) into the image NOW, while the
# build has network: serve INSTALLs them at startup otherwise, and a no-egress
# NetworkPolicy / read-only rootfs silently degrades BM25→LIKE and HNSW→exact-
# scan (observed in prod as fts:false/hnsw:false). The binary's own DuckDB does
# the install so the artefacts match the statically linked version exactly.
RUN HOME=/home/mcp mcp-3gpp prefetch-extensions && chown -R mcp:mcp /home/mcp/.duckdb

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

# ---- full: inherits the data layer FROM 3gpp-data, adds runtime + ORT --------
# `FROM ${DATA_IMAGE}` = the 3gpp-data base (debian + the ~14 GB data layer,
# uid 10001). Everything below is small top layers: the apt runtime libs, the
# mcp user, the binary, the entrypoint, and the arch-native ONNX Runtime. A
# code-only change rebuilds ONLY these; the inherited data layer is referenced
# by digest and never re-pushed/re-pulled.
#
# The apt + user + binary + entrypoint block is intentionally kept in lockstep
# with the `base` stage above (full cannot share `base` since its FROM differs).
FROM ${DATA_IMAGE} AS full
ARG TARGETARCH
ARG ORT_VERSION=1.26.0
ARG ORT_SHA256_AMD64
ARG ORT_SHA256_ARM64
ARG VARIANT=full
# Provenance of the inherited data layer — corpus-image.yml passes the SAME values
# it read from the 3gpp-data image labels (io.kodflow.3gpp.data.created / .source.corpus).
# Baked into ENV (below) so `serve` can surface them on /dashboard.json: the operator
# can then tell, by curl alone, WHICH data layer this image carries — the missing
# signal behind the stale-data-layer incident ([[project_served_stale_data_layer]]).
ARG DATA_CREATED=unknown
ARG SOURCE_CORPUS=unknown
LABEL org.opencontainers.image.variant="${VARIANT}"

# Runtime libs (the data base is debian-slim — same pin as `base`, so this apt
# layer is identical to light's and dedupes on the registry).
RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt,sharing=locked \
    apt-get update -qq && \
    apt-get install -y --no-install-recommends \
      libstdc++6 libgomp1 ca-certificates wget zstd

# Non-root mcp user (uid 10001 = the data layer's baked ownership). /data/mcp-3gpp
# already exists from the data layer; install -d is a no-op confirmation on it.
RUN groupadd -g 10001 mcp && \
    useradd -u 10001 -g mcp -d /home/mcp -m -s /usr/sbin/nologin mcp && \
    install -d -o mcp -g mcp /data

COPY --from=builder /out/mcp-3gpp /usr/local/bin/mcp-3gpp
# The embed-core cdylib (present only when BUILD_TAGS carries embed_ffi; an empty
# dir otherwise). load-dynamic → it links no onnxruntime, so the binary loads it at
# startup without ORT; onnxruntime is dlopened lazily on the first query-embed via
# ORT_DYLIB_PATH (set where ORT is present — full/fulltop). ldconfig registers it.
COPY --from=builder /out/lib/ /usr/local/lib/
RUN ldconfig
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

# Same extension bake as the `base` stage (full cannot share it: different FROM).
RUN HOME=/home/mcp mcp-3gpp prefetch-extensions && chown -R mcp:mcp /home/mcp/.duckdb

# INHERITANCE GUARD: fail the build if the data layer inherited FROM 3gpp-data does
# NOT meet the data-completeness contract. The DEFAULT is the historical FTS+HNSW
# subset so a local/standalone build is unaffected; CI passes the FULL contract via
# --build-arg DATA_CONTRACT_FLAGS="$(scripts/data-contract.sh)" (adds dense-embed
# convergence + sparse + … with the right --embed-floor). This is the structural fix
# for the stale/half-baked-data-layer incident: an incomplete layer can no longer
# ship silently — `docker build` of `full` errors out here instead of producing a
# server that degrades to LIKE full-scan / exact-scan (or silently lacks sparse) in
# production. (light builds its own lexical DB and never reaches this stage.)
ARG DATA_CONTRACT_FLAGS="--require-fts --require-hnsw"
RUN HOME=/home/mcp mcp-3gpp check-data --db /data/mcp-3gpp/3gpp.duckdb ${DATA_CONTRACT_FLAGS}

# ORT is the ONLY arch-specific piece, fetched from the Microsoft GitHub release
# (never rate-limited us) and verified against the same sha256 pins as
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

ENV MCP3GPP_CACHE=/data/mcp-3gpp \
    MCP_TRANSPORT=stdio \
    MCP_PORT=8765 \
    MCP3GPP_DATA_CREATED=${DATA_CREATED} \
    MCP3GPP_SOURCE_CORPUS=${SOURCE_CORPUS} \
    ORT_DYLIB_PATH=/data/mcp-3gpp/models/onnxruntime/lib/libonnxruntime.so
USER mcp:mcp
WORKDIR /home/mcp

HEALTHCHECK --interval=30s --timeout=5s --start-period=120s --retries=3 \
  CMD sh -c '[ "$MCP_TRANSPORT" != "http" ] || wget -q --spider "http://127.0.0.1:${MCP_PORT}/healthz"' || exit 1

ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["serve"]

# ---- fulltop: the SMALL top layers of the full image, on the SAME debian base
# as 3gpp-data, WITHOUT the ~22 GB data layer. CI builds THIS (cheap, ~200 MB, no
# ENOSPC) then `crane rebase`s it onto 3gpp-data@digest — the data layer is then
# referenced by digest and NEVER materialised in buildkit (the ENOSPC that killed
# `FROM ${DATA_IMAGE}` full builds on stock runners). It is `full` minus the
# FROM-data inheritance and minus the in-image check-data guard: the data-
# completeness contract is enforced at the data bake (cmd/validate) and asserted by
# corpus-image via the io.kodflow.3gpp.data.contract label on 3gpp-data — no need to
# re-read the 22 GB DB at image-build time. base already carries the apt runtime,
# the mcp user, the (onnx, when BUILD_TAGS=onnx) binary, the entrypoint, the
# prefetched extensions, ENV/USER/HEALTHCHECK; fulltop adds only the arch-native ORT
# + provenance ENV. crane rebase keeps THIS image's config (entrypoint/env/user).
FROM base AS fulltop
ARG TARGETARCH
ARG ORT_VERSION=1.26.0
ARG ORT_SHA256_AMD64
ARG ORT_SHA256_ARM64
ARG DATA_CREATED=unknown
ARG SOURCE_CORPUS=unknown
USER root
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
ENV MCP3GPP_DATA_CREATED=${DATA_CREATED} \
    MCP3GPP_SOURCE_CORPUS=${SOURCE_CORPUS} \
    ORT_DYLIB_PATH=/data/mcp-3gpp/models/onnxruntime/lib/libonnxruntime.so
USER mcp:mcp

# ---- smoketest: NOT A PUBLISHED IMAGE ----------------------------------------
# This was the `light` variant. It is no longer published: it was lighter only by
# the model and the ORT stack, carried the full data layer including vectors its
# own binary could not use, and answered lexically while looking like the product.
#
# The stage survives because CI needs SOMETHING it can build without a DATA_IMAGE:
# ci.yml's image-smoke compiles the server and boots it, and pointing that at the
# real target would make every PR pull an 11 GB data layer. Nothing pushes it.
#
# LAST stage on purpose: a bare `docker build .` defaults to the final stage, and
# this is the only target that builds without a DATA_IMAGE — so a careless build
# gets the harmless one rather than a broken `fulltop` wearing the product's tag.
FROM base AS smoketest
COPY --chown=mcp:mcp image-data/ /data/mcp-3gpp/
