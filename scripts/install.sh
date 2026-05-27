#!/usr/bin/env sh
#
# install.sh — fetch the mcp-3gpp binary for this platform into ~/.local/bin.
# Pure retrieval engine: no Python, no Ollama, no daemon. After installing, run
# `mcp-3gpp bootstrap` to provision the indexed DB (+ --semantic for models).
#
#   curl -fsSL https://raw.githubusercontent.com/kodflow/3gpp-mcp/main/scripts/install.sh | sh
#
# Env overrides: REPO (owner/repo), VERSION (tag, default latest), BINDIR.
set -eu

REPO="${REPO:-kodflow/3gpp-mcp}"
VERSION="${VERSION:-latest}"
BINDIR="${BINDIR:-$HOME/.local/bin}"

os="$(uname -s)"
arch="$(uname -m)"
# V1 ships exactly what release.yml builds: linux/amd64 + darwin/arm64.
case "$os-$arch" in
  Linux-x86_64) target="linux_amd64" ;;
  Darwin-arm64) target="darwin_arm64" ;;
  *)
    echo "unsupported platform: $os-$arch" >&2
    echo "V1 ships linux/amd64 and darwin/arm64 only — build from source for others." >&2
    exit 1
    ;;
esac

base="https://github.com/$REPO/releases"
if [ "$VERSION" = "latest" ]; then
  url="$base/latest/download/mcp-3gpp_${target}.tar.zst"
else
  url="$base/download/$VERSION/mcp-3gpp_${target}.tar.zst"
fi

echo "→ mcp-3gpp ($target) from $url"
mkdir -p "$BINDIR"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
curl -fSL --retry 3 -A "mcp-3gpp-install" "$url" -o "$tmp/pkg.tar.zst"

# Verify the published SHA-256 before extracting (reject tampered artifacts).
curl -fSL --retry 3 -A "mcp-3gpp-install" "$url.sha256" -o "$tmp/pkg.sha256"
expected="$(awk '{print $1}' "$tmp/pkg.sha256")"
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$tmp/pkg.tar.zst" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
  actual="$(shasum -a 256 "$tmp/pkg.tar.zst" | awk '{print $1}')"
else
  echo "no sha256 tool (sha256sum/shasum) found — cannot verify integrity" >&2
  exit 1
fi
if [ -z "$expected" ] || [ "$expected" != "$actual" ]; then
  echo "checksum mismatch: expected=$expected actual=$actual" >&2
  exit 1
fi

# zstd may be provided by tar (--zstd) or the standalone tool.
if tar --help 2>&1 | grep -q -- --zstd; then
  tar --zstd -xf "$tmp/pkg.tar.zst" -C "$tmp"
else
  zstd -d "$tmp/pkg.tar.zst" -o "$tmp/pkg.tar" && tar -xf "$tmp/pkg.tar" -C "$tmp"
fi
install -m 0755 "$tmp/mcp-3gpp" "$BINDIR/mcp-3gpp"

echo "✓ installed $BINDIR/mcp-3gpp (sha256 verified)"
case ":$PATH:" in
  *":$BINDIR:"*) : ;;
  *) echo "  note: add $BINDIR to your PATH" ;;
esac
echo "  next: mcp-3gpp bootstrap --db-url <release-db-url> [--semantic]"
