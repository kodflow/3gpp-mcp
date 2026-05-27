#!/bin/bash
# =============================================================================
# LibreOffice headless — DevContainer Feature
# =============================================================================
# 3GPP corpus ingestion (scripts/corpus.sh) converts every spec DOC/DOCX to
# HTML with `soffice`. Without it, ingestion silently produces zero HTML, so we
# fail loud here rather than ship a container that looks fine until the first
# corpus run. Installed at build time -> survives container rebuilds (the
# previous runtime `apt-get` install was lost on every rebuild).
# =============================================================================
set -Eeuo pipefail

on_error() {
    local code=$1 line=$2 cmd=$3
    echo -e "\033[0;31m✗ install.sh (libreoffice feature) FAILED at line ${line} (exit=${code}): ${cmd}\033[0m" >&2
    exit "$code"
}
trap 'on_error $? $LINENO "$BASH_COMMAND"' ERR

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
ok()   { echo -e "${GREEN}✓${NC} $*"; }
info() { echo -e "${YELLOW}→${NC} $*"; }

if command -v soffice >/dev/null 2>&1; then
    ok "LibreOffice already present: $(soffice --version 2>/dev/null | head -1)"
    exit 0
fi

info "Installing libreoffice-writer (headless DOC/DOCX -> HTML for 3GPP ingest) ..."
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
# --no-install-recommends keeps the image lean: no Java, no GUI stack. The
# Writer + core packages are enough for headless --convert-to html.
apt-get install -y --no-install-recommends libreoffice-writer libreoffice-core
apt-get clean
rm -rf /var/lib/apt/lists/*

command -v soffice >/dev/null 2>&1 || { echo -e "${RED}✗ soffice still missing after install${NC}" >&2; exit 1; }
ok "LibreOffice installed: $(soffice --version 2>/dev/null | head -1)"

# Register the 3gpp-mcp server as an MCP fragment so postStart.sh merges it into
# /workspace/mcp.json on every restart — durable across rebuilds (this feature
# re-runs each build). `requires_binary` gates it: the server only appears once
# `make build` has produced /workspace/bin/mcp-3gpp.
info "Installing 3gpp MCP fragment ..."
mkdir -p /etc/mcp/features
cat > /etc/mcp/features/3gpp.mcp.json <<'MCPFRAG'
{
  "servers": {
    "3gpp": {
      "command": "/workspace/bin/mcp-3gpp",
      "args": ["serve", "--db", "/workspace/data/3gpp.duckdb"],
      "requires_binary": "/workspace/bin/mcp-3gpp"
    }
  }
}
MCPFRAG
ok "3gpp MCP fragment installed (merged on restart once the binary is built)"
