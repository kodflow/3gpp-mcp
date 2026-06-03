#!/bin/sh
# Entrypoint for the 3gpp-mcp image. Default (CMD=serve) runs the MCP server on
# stdio — the Claude-Code contract for `docker run -i --rm ... serve`. Set
# MCP_TRANSPORT=http (optionally MCP_PORT) to expose Streamable HTTP + the landing
# page instead. bootstrap/version/anything-else pass straight through to the binary.
#
# stdio hygiene: in stdio mode stdout MUST carry only the JSON-RPC framing; the
# binary already logs to stderr, and this wrapper adds nothing to stdout.
set -e

case "${1:-serve}" in
  serve)
    shift 2>/dev/null || true
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
