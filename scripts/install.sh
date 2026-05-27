#!/usr/bin/env sh
#
# install.sh — fetch the mcp-3gpp binary for this platform into ~/.local/bin.
# Pure retrieval engine: no Python, no Ollama, no daemon. After installing, run
# `mcp-3gpp bootstrap` to provision the indexed DB (+ --semantic for models).
#
#   curl -fsSL https://raw.githubusercontent.com/<OWNER>/<REPO>/main/scripts/install.sh | sh
#
# Env overrides: REPO (owner/repo), VERSION (tag, default latest), BINDIR.
set -eu

REPO="${REPO:-<OWNER>/<REPO>}"
VERSION="${VERSION:-latest}"
BINDIR="${BINDIR:-$HOME/.local/bin}"

os="$(uname -s)"
arch="$(uname -m)"
case "$os-$arch" in
  Linux-x86_64)  target="linux_amd64" ;;
  Linux-aarch64) target="linux_arm64" ;;
  Darwin-arm64)  target="darwin_arm64" ;;
  Darwin-x86_64) target="darwin_amd64" ;;
  *) echo "unsupported platform: $os-$arch" >&2; exit 1 ;;
esac

if [ "$VERSION" = "latest" ]; then
  url="https://github.com/$REPO/releases/latest/download/mcp-3gpp_${target}.tar.zst"
else
  url="https://github.com/$REPO/releases/download/$VERSION/mcp-3gpp_${target}.tar.zst"
fi

echo "→ mcp-3gpp ($target) from $url"
mkdir -p "$BINDIR"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
curl -fSL --retry 3 -A "mcp-3gpp-install" "$url" -o "$tmp/pkg.tar.zst"
# zstd may be provided by tar (--zstd) or the standalone tool.
if tar --help 2>&1 | grep -q -- --zstd; then
  tar --zstd -xf "$tmp/pkg.tar.zst" -C "$tmp"
else
  zstd -d "$tmp/pkg.tar.zst" -o "$tmp/pkg.tar" && tar -xf "$tmp/pkg.tar" -C "$tmp"
fi
install -m 0755 "$tmp/mcp-3gpp" "$BINDIR/mcp-3gpp"

echo "✓ installed $BINDIR/mcp-3gpp"
case ":$PATH:" in
  *":$BINDIR:"*) : ;;
  *) echo "  note: add $BINDIR to your PATH" ;;
esac
echo "  next: mcp-3gpp bootstrap --db-url <release-db-url> [--semantic]"
