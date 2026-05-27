#!/usr/bin/env sh
#
# install.sh — fetch the mcp-3gpp binary for this platform into ~/.local/bin.
# Pure retrieval engine: no Python, no Ollama, no daemon. After installing, run
# `mcp-3gpp bootstrap` to provision the indexed DB (+ --semantic for models).
#
#   curl -fsSL https://raw.githubusercontent.com/kodflow/3gpp-mcp/main/scripts/install.sh | sh
#
# Unix only (linux/darwin). Windows: download mcp-3gpp_windows_amd64.zip from the
# release and run mcp-3gpp.exe (bootstrap is cross-platform pure-Go).
# Env overrides: REPO (owner/repo), VERSION (tag, default latest), BINDIR.
set -eu

REPO="${REPO:-kodflow/3gpp-mcp}"
VERSION="${VERSION:-latest}"
BINDIR="${BINDIR:-$HOME/.local/bin}"

os="$(uname -s)"
arch="$(uname -m)"
# GoReleaser native matrix ships linux + darwin on amd64 + arm64 (tar.gz).
case "$os" in
  Linux)  goos="linux";  sums="checksums-linux.txt" ;;
  Darwin) goos="darwin"; sums="checksums-darwin.txt" ;;
  *) echo "unsupported OS: $os (use the windows zip or build from source)" >&2; exit 1 ;;
esac
case "$arch" in
  x86_64|amd64)  goarch="amd64" ;;
  aarch64|arm64) goarch="arm64" ;;
  *) echo "unsupported arch: $arch" >&2; exit 1 ;;
esac
target="${goos}_${goarch}"
archive="mcp-3gpp_${target}.tar.gz"

if [ "$VERSION" = "latest" ]; then
  base="https://github.com/$REPO/releases/latest/download"
else
  base="https://github.com/$REPO/releases/download/$VERSION"
fi

echo "→ mcp-3gpp ($target) from $base/$archive"
mkdir -p "$BINDIR"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
curl -fSL --retry 3 -A "mcp-3gpp-install" "$base/$archive" -o "$tmp/$archive"

# Verify against the published GoReleaser checksums file (line: "<sha256>  <archive>").
curl -fSL --retry 3 -A "mcp-3gpp-install" "$base/$sums" -o "$tmp/sums.txt"
expected="$(awk -v a="$archive" '$2==a {print $1}' "$tmp/sums.txt")"
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$tmp/$archive" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
  actual="$(shasum -a 256 "$tmp/$archive" | awk '{print $1}')"
else
  echo "no sha256 tool (sha256sum/shasum) found — cannot verify integrity" >&2
  exit 1
fi
if [ -z "$expected" ] || [ "$expected" != "$actual" ]; then
  echo "checksum mismatch for $archive: expected=$expected actual=$actual" >&2
  exit 1
fi

tar -xzf "$tmp/$archive" -C "$tmp"
install -m 0755 "$tmp/mcp-3gpp" "$BINDIR/mcp-3gpp"

echo "✓ installed $BINDIR/mcp-3gpp (sha256 verified)"
case ":$PATH:" in
  *":$BINDIR:"*) : ;;
  *) echo "  note: add $BINDIR to your PATH" ;;
esac
echo "  next: mcp-3gpp bootstrap   (pulls the indexed DB; add --semantic for models)"
