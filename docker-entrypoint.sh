#!/bin/sh
# Entrypoint for the 3gpp-mcp image. Default (CMD=serve) runs the MCP server on
# stdio — the Claude-Code contract for `docker run -i --rm ... serve`. Set
# MCP_TRANSPORT=http (optionally MCP_PORT) to expose Streamable HTTP + the landing
# page instead. bootstrap/version/anything-else pass straight through to the binary.
#
# Image variants (built by corpus-image.yml):
#   :light          — binary only; the corpus DB is bootstrapped from the network
#                     on first run (lexical unless an onnx build + model are present).
#   :full / :latest — onnx binary + the corpus DB + vector sub-bases + BGE-M3 model
#                     BAKED IN as .zst (to keep the image small). This entrypoint
#                     decompresses them into the /data volume on first start, then
#                     serves OFFLINE (semantic, no network). Mount a NAMED volume
#                     (-v 3gpp-mcp-data:/data) so the one-time decompression is NOT
#                     repaid on every `--rm` run.
#
# stdio hygiene: in stdio mode stdout MUST carry only the JSON-RPC framing; the
# binary logs to stderr and this wrapper writes ONLY to stderr.
set -e

# decompress_baked expands any baked *.zst artifact under the cache into its raw
# sibling (idempotent: skips when the target already exists, so a persistent /data
# volume pays this once). Keeps the IMAGE small while serve reads raw files.
decompress_baked() {
  cache="${MCP3GPP_CACHE:-/data/mcp-3gpp}"
  [ -d "$cache" ] || return 0
  command -v zstd >/dev/null 2>&1 || return 0
  find "$cache" -type f -name '*.zst' 2>/dev/null | while IFS= read -r z; do
    out="${z%.zst}"
    [ -f "$out" ] && continue
    echo "[entrypoint] decompressing $(basename "$z") → $(basename "$out")…" >&2
    # --long=31 accepts any compression window; fall back to the default frame.
    zstd -d -q --long=31 "$z" -o "$out" 2>/dev/null \
      || zstd -d -q "$z" -o "$out" 2>/dev/null \
      || echo "[entrypoint] WARN: failed to decompress $z" >&2
  done
}

# baked_flags emits the serve flags that make a FULL (baked) image serve offline:
# never refresh from the network, and attach the baked vector sub-bases if present.
baked_flags() {
  cache="${MCP3GPP_CACHE:-/data/mcp-3gpp}"
  flags=""
  [ -f "$cache/3gpp.duckdb" ] && flags="-no-update"
  [ -f "$cache/vec/vec-manifest.json" ] && flags="$flags -vec-manifest $cache/vec/vec-manifest.json"
  printf '%s' "$flags"
}

case "${1:-serve}" in
  serve)
    shift 2>/dev/null || true
    decompress_baked
    # shellcheck disable=SC2046 # intentional word-split: baked_flags emits 0..N flags
    set -- $(baked_flags) "$@"
    if [ "${MCP_TRANSPORT:-stdio}" = "http" ]; then
      exec mcp-3gpp serve --http "0.0.0.0:${MCP_PORT:-8765}" "$@"
    fi
    exec mcp-3gpp serve "$@"
    ;;
  bootstrap|version|-v|--version)
    exec mcp-3gpp "$@"
    ;;
  *)
    # Allow `docker run ... mcp-3gpp <anything>` and raw shells for debugging.
    exec "$@"
    ;;
esac
