#!/usr/bin/env bash
#
# toolchain-bootstrap.sh — install the whole build toolchain into .local/toolchain,
# portable and WITHOUT elevation.
#
#   bash scripts/local/toolchain-bootstrap.sh          # everything
#   bash scripts/local/toolchain-bootstrap.sh go rust  # only these components
#
# Everything lands under .local/toolchain (gitignored). Nothing is written to the
# user profile, nothing needs administrator rights, nothing is registered with the
# system. Re-running is safe: each component is skipped when already present.
#
# WHY THIS SCRIPT EXISTS
#
# The pipeline is only reproducible if the ENVIRONMENT is. Every version below was
# chosen for a reason that is recorded next to it, and several are not the obvious
# choice — a fresh machine that installs "the latest of everything" will hit the
# same three walls this project already hit:
#
#   1. mingw must be UCRT, not msvcrt (heap corruption at runtime otherwise);
#   2. the DuckDB library version must match the bindings HEADERS, not a comment;
#   3. ONNX Runtime must be loaded dynamically, and its CUDA provider needs the
#      CUDA + cuDNN redistributables next to it.
#
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TC="$ROOT/.local/toolchain"
mkdir -p "$TC"

# ---- versions, each with the reason it is pinned -----------------------------

# Imposed by go.mod. Not "the latest Go".
GO_VERSION="${GO_VERSION:-1.26.3}"

# winlibs mingw-w64 built against UCRT.
#
# THIS IS NOT INTERCHANGEABLE WITH w64devkit. w64devkit links msvcrt.dll, the
# legacy CRT; duckdb.dll (like every modern Windows DLL) links UCRT. A binary that
# mixes them has TWO heaps: DuckDB allocates with ucrtbase, cgo frees with msvcrt,
# and the process dies with 0xC0000374 STATUS_HEAP_CORRUPTION on the first query.
# The build is perfectly green — the crash is at runtime, and looks like a DuckDB
# bug until you read the import table.
UCRT_TAG="${UCRT_TAG:-16.2.0posix-14.0.0-ucrt-r1}"
UCRT_ZIP="${UCRT_ZIP:-winlibs-x86_64-posix-seh-gcc-16.2.0-mingw-w64ucrt-14.0.0-r1.zip}"

# DuckDB shared library.
#
# MUST match the HEADERS shipped by the duckdb-go-bindings ROOT module pinned in
# go.mod (currently v0.10503.0 -> DuckDB 1.5.3), NOT the version named in
# rust/store/Cargo.toml and NOT the version claimed in comments. Supplying 1.4.3
# against 1.5.3 headers fails at link time with a fistful of undefined symbols
# (duckdb_valid_utf8_check, duckdb_register_log_storage, …).
# scripts/check-duckdb-pin.sh is the guard; keep them in step.
DUCKDB_VERSION="${DUCKDB_VERSION:-1.5.3}"

# ONNX Runtime, GPU build.
#
# 1.20.1 rather than the newest: the corpus kernels documented that 1.26.0 HANGS
# on the first session.run with ort 2.0.0-rc.9. Loaded dynamically via
# ORT_DYLIB_PATH — rust/embedder and rust/embed-core both use `load-dynamic`, so
# one runtime serves the process instead of two bundled copies.
ORT_VERSION="${ORT_VERSION:-1.20.1}"

# CUDA runtime + cuDNN, user-space only.
#
# ONNX Runtime 1.20's CUDA provider needs CUDA 12 and cuDNN 9. We take the public
# NVIDIA redistributable archives and keep ONLY the DLLs: no driver, no toolkit,
# no installer, no elevation. Note the version skew that bites here — the PyPI
# nvidia-cudnn-cu12 wheel for Windows is still 8.9, which ORT 1.20 rejects.
CUDA_REDIST="${CUDA_REDIST:-12.6.3}"
CUDNN_ZIP="${CUDNN_ZIP:-cudnn-windows-x86_64-9.9.0.52_cuda12-archive.zip}"

# LibreOffice: the ONLY tool that reads the ~55% of the 3GPP corpus still shipped
# as legacy binary .doc while preserving structure (headings = clauses, tables).
LO_VERSION="${LO_VERSION:-25.8.7}"

c_g=$'\033[32m'; c_y=$'\033[33m'; c_b=$'\033[34m'; c_0=$'\033[0m'
[ -t 2 ] || { c_g=""; c_y=""; c_b=""; c_0=""; }
log()  { printf '%s==>%s %s\n' "$c_b" "$c_0" "$*" >&2; }
ok()   { printf '%s  ok%s %s\n' "$c_g" "$c_0" "$*" >&2; }
warn() { printf '%s  !!%s %s\n' "$c_y" "$c_0" "$*" >&2; }

want() { # want <component> — was it requested?
  [ $# -eq 0 ] && return 0
  case " ${COMPONENTS:-} " in *" $1 "*) return 0;; *) return 1;; esac
}
COMPONENTS="${*:-go mingw rust duckdb ort cuda libreoffice}"
log "components: $COMPONENTS"

fetch() { # fetch <url> <dest>
  [ -s "$2" ] && return 0
  curl -fSL --retry 3 --retry-delay 2 -C - -o "$2.part" "$1" || return 1
  mv "$2.part" "$2"
}

# ------------------------------------------------------------------------- Go
if want go; then
  if [ -x "$TC/go/bin/go.exe" ] || [ -x "$TC/go/bin/go" ]; then
    ok "Go already present"
  else
    log "Go $GO_VERSION"
    case "$(uname -s)" in
      MINGW*|MSYS*|CYGWIN*) pkg="go${GO_VERSION}.windows-amd64.zip" ;;
      Darwin)               pkg="go${GO_VERSION}.darwin-arm64.tar.gz" ;;
      *)                    pkg="go${GO_VERSION}.linux-amd64.tar.gz" ;;
    esac
    fetch "https://go.dev/dl/$pkg" "$TC/$pkg" || { warn "Go download failed"; }
    case "$pkg" in
      *.zip) ( cd "$TC" && unzip -oq "$pkg" ) ;;
      *)     ( cd "$TC" && tar xzf "$pkg" ) ;;
    esac
    rm -f "$TC/$pkg"
    ok "Go installed"
  fi
fi

# ---------------------------------------------------------------------- mingw
if want mingw && [ "$(uname -s)" != "Linux" ]; then
  if [ -x "$TC/ucrt64/bin/gcc.exe" ]; then
    ok "mingw-UCRT already present"
  else
    log "mingw-w64 UCRT (winlibs $UCRT_TAG)"
    fetch "https://github.com/brechtsanders/winlibs_mingw/releases/download/$UCRT_TAG/$UCRT_ZIP" "$TC/ucrt.zip" \
      && ( cd "$TC" && unzip -oq ucrt.zip && rm -f ucrt.zip && [ -d mingw64 ] && mv mingw64 ucrt64 ) \
      && ok "mingw-UCRT installed" || warn "mingw-UCRT install failed"
  fi
fi

# ----------------------------------------------------------------------- Rust
if want rust; then
  # Exported for BOTH branches: the "already present" path must still be able to
  # add a component that a previous minimal install left out.
  export RUSTUP_HOME="$TC/rustup" CARGO_HOME="$TC/cargo"
  if [ -x "$TC/cargo/bin/cargo.exe" ] || [ -x "$TC/cargo/bin/cargo" ]; then
    ok "Rust already present"
  else
    log "Rust (portable: RUSTUP_HOME/CARGO_HOME stay inside .local)"
    case "$(uname -s)" in
      MINGW*|MSYS*|CYGWIN*)
        fetch "https://static.rust-lang.org/rustup/dist/x86_64-pc-windows-gnu/rustup-init.exe" "$TC/rustup-init.exe" \
          && "$TC/rustup-init.exe" -y --no-modify-path --default-toolchain stable \
               --default-host x86_64-pc-windows-gnu --profile minimal >/dev/null \
          && ok "Rust installed (GNU host, to share the mingw-UCRT C++ runtime)" \
          || warn "rustup failed"
        ;;
      *)
        curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs \
          | sh -s -- -y --no-modify-path --default-toolchain stable --profile minimal >/dev/null \
          && ok "Rust installed" || warn "rustup failed"
        ;;
    esac
  fi

  # `--profile minimal` omits rustfmt, so `cargo fmt --check` — which CI runs and
  # which blocks the merge — could not be reproduced here at all: the first time
  # anyone found out was a red PR. A formatter the CI enforces has to exist on
  # the machine that writes the code.
  if [ -x "$TC/cargo/bin/cargo.exe" ] || [ -x "$TC/cargo/bin/cargo" ]; then
    if PATH="$TC/cargo/bin:$PATH" cargo fmt --version >/dev/null 2>&1; then
      ok "rustfmt already present"
    else
      log "rustfmt (component; the minimal profile leaves it out)"
      PATH="$TC/cargo/bin:$PATH" rustup component add rustfmt >/dev/null 2>&1 \
        && ok "rustfmt installed" || warn "rustup component add rustfmt failed"
    fi
  fi
fi

# --------------------------------------------------------------------- DuckDB
if want duckdb && [ "$(uname -s)" != "Linux" ]; then
  if [ -f "$TC/duckdb/duckdb.dll" ]; then
    ok "libduckdb already present"
  else
    log "libduckdb $DUCKDB_VERSION (must match the bindings headers — see above)"
    mkdir -p "$TC/duckdb"
    fetch "https://github.com/duckdb/duckdb/releases/download/v$DUCKDB_VERSION/libduckdb-windows-amd64.zip" "$TC/duckdb/lib.zip" \
      && ( cd "$TC/duckdb" && unzip -oq lib.zip && rm -f lib.zip ) \
      && ok "libduckdb installed" || warn "libduckdb download failed"
    # mingw links a DLL through an import library; generate one from the exports.
    if [ -f "$TC/duckdb/duckdb.dll" ] && [ ! -f "$TC/duckdb/libduckdb.dll.a" ]; then
      PATH="$TC/ucrt64/bin:$PATH" bash -c "cd '$TC/duckdb' && gendef duckdb.dll >/dev/null 2>&1 && dlltool -d duckdb.def -l libduckdb.dll.a -D duckdb.dll" \
        && ok "import library generated" || warn "gendef/dlltool unavailable"
    fi
  fi
fi

# --------------------------------------------------------------- ONNX Runtime
if want ort; then
  if compgen -G "$TC/ort/*/lib" >/dev/null 2>&1; then
    ok "ONNX Runtime already present"
  else
    log "ONNX Runtime $ORT_VERSION (GPU, loaded dynamically)"
    mkdir -p "$TC/ort"
    case "$(uname -s)" in
      MINGW*|MSYS*|CYGWIN*) pkg="onnxruntime-win-x64-gpu-$ORT_VERSION.zip" ;;
      *)                    pkg="onnxruntime-linux-x64-gpu-$ORT_VERSION.tgz" ;;
    esac
    fetch "https://github.com/microsoft/onnxruntime/releases/download/v$ORT_VERSION/$pkg" "$TC/ort/$pkg" \
      && ( cd "$TC/ort" && case "$pkg" in *.zip) unzip -oq "$pkg";; *) tar xzf "$pkg";; esac && rm -f "$pkg" ) \
      && ok "ONNX Runtime installed" || warn "ONNX Runtime download failed"
  fi
fi

# ------------------------------------------------------------- CUDA + cuDNN
if want cuda && [ "$(uname -s)" != "Linux" ]; then
  if [ -f "$TC/cuda/dll/cudnn64_9.dll" ]; then
    ok "CUDA runtime already present"
  else
    if ! command -v nvidia-smi >/dev/null 2>&1; then
      warn "no NVIDIA driver detected — skipping the CUDA runtime (embedding would run on CPU, ~100x slower)"
    else
      log "CUDA $CUDA_REDIST runtime + cuDNN (user-space DLLs only, ~2 GB)"
      mkdir -p "$TC/cuda/dll" "$TC/cuda/ext"
      B="https://developer.download.nvidia.com/compute/cuda/redist"
      CB="https://developer.download.nvidia.com/compute/cudnn/redist/cudnn/windows-x86_64"
      # Resolve the exact archive names from the official manifest rather than
      # hard-coding component versions that drift independently of the CUDA one.
      man="$TC/cuda/redistrib.json"
      fetch "$B/redistrib_$CUDA_REDIST.json" "$man" || warn "CUDA manifest unavailable"
      if [ -s "$man" ]; then
        for rel in $(grep -oE '"[a-z_]+/windows-x86_64/[^"]+\.zip"' "$man" | tr -d '"' \
                     | grep -E 'cudart|cublas|cufft|curand'); do
          fetch "$B/$rel" "$TC/cuda/$(basename "$rel")" || warn "failed: $rel"
        done
      fi
      fetch "$CB/$CUDNN_ZIP" "$TC/cuda/$CUDNN_ZIP" || warn "cuDNN download failed"
      for z in "$TC"/cuda/*.zip; do [ -f "$z" ] && unzip -oq "$z" -d "$TC/cuda/ext"; done
      find "$TC/cuda/ext" -name '*.dll' -exec cp -n {} "$TC/cuda/dll/" \; 2>/dev/null
      # The archives are large and only their DLLs matter; reclaim the rest.
      rm -rf "$TC/cuda/ext" "$TC"/cuda/*.zip
      n=$(find "$TC/cuda/dll" -name '*.dll' | wc -l)
      [ "$n" -gt 0 ] && ok "$n CUDA/cuDNN DLL(s) staged" || warn "no CUDA DLL extracted"
    fi
  fi
fi

# ---------------------------------------------------------------- LibreOffice
if want libreoffice && [ "$(uname -s)" != "Linux" ]; then
  if [ -x "$TC/libreoffice/program/soffice.exe" ]; then
    ok "LibreOffice already present"
  else
    log "LibreOffice $LO_VERSION (administrative install — extracts, does not install)"
    msi="$TC/libreoffice.msi"
    fetch "https://download.documentfoundation.org/libreoffice/stable/$LO_VERSION/win/x86_64/LibreOffice_${LO_VERSION}_Win_x86-64.msi" "$msi" \
      && msiexec.exe //a "$(cygpath -w "$msi")" //qn "TARGETDIR=$(cygpath -w "$TC/libreoffice")" \
      && rm -f "$msi" \
      && ok "LibreOffice extracted" || warn "LibreOffice install failed"
  fi
fi

log "done — source scripts/local/toolchain-env.sh, then run: make goal-plan"
