#!/usr/bin/env bash
#
# fetch-linux-toolchain.sh — put the Linux cross-compilation toolchain in
# .local/toolchain, so this Windows machine can build the container image's
# artefacts with no Docker, no WSL and no administrator rights.
#
# Two pieces, and the second one is the non-obvious half:
#
#   zig            a self-contained clang + lld that targets linux-gnu. ~80 MB,
#                  unpacked in place, nothing installed system-wide.
#   Debian's
#   libstdc++ +    NOT a convenience. DuckDB's prebuilt linux-amd64 archive (the
#   libgomp        one duckdb-go-bindings ships, which the Linux build links
#                  because it does not use duckdb_use_lib) is compiled against GNU
#                  libstdc++, and zig ships LLVM's libc++. Linking against zig's
#                  C++ runtime fails with hundreds of undefined std::__cxx11
#                  symbols — the two libraries do not share an ABI. So the link
#                  uses the same libstdc++.so.6 / libgomp.so.1 the runtime image
#                  carries, taken from bookworm's own packages.
#
# The .so files are also SHIPPED in the image layer, which is why the version is
# pinned to the runtime base (debian:bookworm-slim, gcc-12): linking against a
# newer libstdc++ than the image has is the classic way to get a binary that
# builds here and dies there.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TC="$ROOT/.local/toolchain"
ZIG_VERSION="${ZIG_VERSION:-0.13.0}"
GCC_DEB_VERSION="${GCC_DEB_VERSION:-12.2.0-14+deb12u1}"
mkdir -p "$TC"

if [ ! -x "$TC/zig-windows-x86_64-$ZIG_VERSION/zig.exe" ]; then
  echo "[toolchain] zig $ZIG_VERSION"
  curl -fsSL -o "$TC/zig.zip" "https://ziglang.org/download/$ZIG_VERSION/zig-windows-x86_64-$ZIG_VERSION.zip"
  (cd "$TC" && unzip -q -o zig.zip && rm -f zig.zip)
fi
"$TC/zig-windows-x86_64-$ZIG_VERSION/zig.exe" version

LIB="$TC/sysroot-linux/lib"
if [ ! -s "$LIB/libstdc++.so.6" ] || [ ! -s "$LIB/libgomp.so.1" ]; then
  echo "[toolchain] Debian bookworm libstdc++6 + libgomp1 ($GCC_DEB_VERSION)"
  mkdir -p "$LIB" "$TC/sysroot-linux/dl"
  ZIG="$TC/zig-windows-x86_64-$ZIG_VERSION/zig.exe"
  for pkg in libstdc++6 libgomp1; do
    deb="${pkg}_${GCC_DEB_VERSION}_amd64.deb"
    work="$TC/sysroot-linux/dl/$pkg"
    rm -rf "$work"; mkdir -p "$work"
    curl -fsSL -o "$work/$deb" "https://deb.debian.org/debian/pool/main/g/gcc-12/$deb"
    # `ar` is not on this machine's PATH; zig ships llvm-ar, which reads a .deb
    # (it is an ar archive) perfectly well.
    ( cd "$work" && "$ZIG" ar x "$deb" && tar -xJf data.tar.xz ./usr/lib/x86_64-linux-gnu >/dev/null 2>&1 || true )
    cp -a "$work"/usr/lib/x86_64-linux-gnu/*.so* "$LIB/" 2>/dev/null || true
  done
  rm -rf "$TC/sysroot-linux/dl"
fi
ls -la "$LIB"

echo "[toolchain] ready — scripts/local/build-image.sh can now cross-build"
