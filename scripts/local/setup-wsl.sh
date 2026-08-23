#!/usr/bin/env bash
#
# setup-wsl.sh — installe TOUTE la toolchain d'indexation dans une Ubuntu WSL2.
#
# A lancer DEPUIS Ubuntu (pas depuis Windows) :
#     bash scripts/local/setup-wsl.sh
#
# Pourquoi WSL2 et pas Windows natif : internal/bootstrap/models.go ne publie
# ONNX Runtime que pour linux/amd64, linux/arm64 et darwin/arm64 -- il n'existe
# AUCUN asset ONNX Windows dans ce projet. Ajoute a cela flock, soffice
# --headless, df -BG et un Makefile : le chemin semantique sous Windows natif est
# structurellement impossible. WSL2 donne en plus le passthrough CUDA (libcuda.so
# vient du pilote Windows, rien a installer cote noyau).
#
# Idempotent : relancable sans risque, chaque etape se saute si deja faite.
#
set -uo pipefail

GO_VERSION="${GO_VERSION:-1.26.3}"        # impose par go.mod
CUDA_SERIES="${CUDA_SERIES:-12-6}"        # pilote 553.35 -> CUDA 12.4+, 12-6 OK
SKIP_CUDA="${SKIP_CUDA:-0}"
SKIP_MODEL="${SKIP_MODEL:-0}"

c_g=$'\033[32m'; c_y=$'\033[33m'; c_r=$'\033[31m'; c_b=$'\033[34m'; c_0=$'\033[0m'
[ -t 2 ] || { c_g=""; c_y=""; c_r=""; c_b=""; c_0=""; }
log()  { printf '%s==>%s %s\n' "$c_b" "$c_0" "$*" >&2; }
ok()   { printf '%s  ok%s %s\n' "$c_g" "$c_0" "$*" >&2; }
warn() { printf '%s  !!%s %s\n' "$c_y" "$c_0" "$*" >&2; }
die()  { printf '%s  XX%s %s\n' "$c_r" "$c_0" "$*" >&2; exit 1; }

[ -r /proc/version ] && grep -qiE 'microsoft|wsl' /proc/version \
  || warn "ne semble pas tourner sous WSL -- on continue quand meme"
[ "$(id -u)" -eq 0 ] && die "ne lance PAS ce script en root : rustup et go s'installent dans \$HOME"
command -v sudo >/dev/null || die "sudo est requis"

# ------------------------------------------------------------------ 1. apt
log "paquets systeme"
export DEBIAN_FRONTEND=noninteractive
sudo apt-get update -qq
# build-essential + cmake : le crate duckdb est compile "bundled" (pas de libduckdb
#   systeme), il lui faut un compilateur C++ et cmake.
# libreoffice-writer : LE convertisseur .doc/.docx -> HTML. ~55 % du corpus 3GPP
#   est en .doc binaire legacy ; c'est le seul outil qui garde la structure
#   (headings = clauses, tables). Version -nogui si dispo, sinon la normale.
# poppler-utils : pdftotext -layout, chemin ETSI uniquement, JAMAIS d'OCR.
# util-linux : flock, dont corpus.sh se sert pour ne pas se marcher dessus.
sudo apt-get install -y -qq --no-install-recommends \
  build-essential cmake pkg-config ca-certificates curl wget git unzip zip \
  zstd jq xz-utils util-linux bc \
  poppler-utils fonts-liberation \
  libreoffice-writer libreoffice-core \
  python3-minimal \
  || die "apt-get install a echoue"
ok "paquets systeme installes"

command -v soffice >/dev/null && ok "soffice $(soffice --version 2>/dev/null | head -1)" \
  || warn "soffice introuvable dans le PATH"

# ------------------------------------------------------------------- 2. Go
if command -v go >/dev/null && go version | grep -q "go$GO_VERSION"; then
  ok "go $GO_VERSION deja present"
else
  log "Go $GO_VERSION (go.mod l'exige)"
  tarball="go${GO_VERSION}.linux-amd64.tar.gz"
  curl -fSL --retry 3 -o "/tmp/$tarball" "https://go.dev/dl/$tarball" \
    || die "telechargement de Go impossible (version $GO_VERSION inexistante ?)"
  sudo rm -rf /usr/local/go
  sudo tar -C /usr/local -xzf "/tmp/$tarball"
  rm -f "/tmp/$tarball"
  grep -q '/usr/local/go/bin' "$HOME/.bashrc" 2>/dev/null \
    || echo 'export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin' >> "$HOME/.bashrc"
  export PATH="$PATH:/usr/local/go/bin:$HOME/go/bin"
  ok "$(go version)"
fi

# ----------------------------------------------------------------- 3. Rust
if command -v cargo >/dev/null || [ -x "$HOME/.cargo/bin/cargo" ]; then
  export PATH="$HOME/.cargo/bin:$PATH"
  ok "rust deja present : $(rustc --version 2>/dev/null)"
else
  log "rustup + toolchain stable"
  curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs \
    | sh -s -- -y --default-toolchain stable --profile minimal || die "rustup a echoue"
  export PATH="$HOME/.cargo/bin:$PATH"
  ok "$(rustc --version)"
fi

# ------------------------------------------------------------- 4. GPU / CUDA
if [ "$SKIP_CUDA" = "1" ]; then
  warn "SKIP_CUDA=1 -- etape CUDA sautee"
elif ! command -v nvidia-smi >/dev/null 2>&1; then
  warn "nvidia-smi absent dans WSL : le passthrough GPU ne marche pas."
  warn "verifie que /usr/lib/wsl/lib est dans le PATH et que le pilote Windows est a jour"
else
  ok "GPU vu depuis WSL : $(nvidia-smi --query-gpu=name,memory.total --format=csv,noheader | head -1)"
  # ONNX Runtime GPU 1.20+ a besoin de cuBLAS 12 et cuDNN 9. Sous WSL, libcuda.so
  # vient du pilote Windows (/usr/lib/wsl/lib) : on n'installe QUE les libs
  # utilisateur, surtout PAS un pilote noyau (cela casserait le passthrough).
  if ldconfig -p 2>/dev/null | grep -q libcudnn.so.9; then
    ok "cuDNN 9 deja present"
  else
    log "depot NVIDIA + runtime CUDA $CUDA_SERIES et cuDNN 9 (~2 Go)"
    kr=/usr/share/keyrings/cuda-archive-keyring.gpg
    if [ ! -f "$kr" ]; then
      curl -fsSL -o /tmp/cuda-keyring.deb \
        https://developer.download.nvidia.com/compute/cuda/repos/wsl-ubuntu/x86_64/cuda-keyring_1.1-1_all.deb \
        && sudo dpkg -i /tmp/cuda-keyring.deb && rm -f /tmp/cuda-keyring.deb
    fi
    sudo apt-get update -qq
    # Paquets utilisateur uniquement : cublas + cudnn. PAS de cuda-drivers.
    sudo apt-get install -y -qq --no-install-recommends \
      "libcublas-${CUDA_SERIES}" "libcufft-${CUDA_SERIES}" "libcurand-${CUDA_SERIES}" \
      libcudnn9-cuda-12 \
      || warn "installation CUDA partielle -- l'embed pourra retomber en CPU"
    sudo ldconfig
    ldconfig -p | grep -q libcudnn.so.9 && ok "cuDNN 9 installe" || warn "cuDNN 9 toujours absent"
  fi
fi

# --------------------------------------------------------------- 5. le modele
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
if [ "$SKIP_MODEL" = "1" ]; then
  warn "SKIP_MODEL=1 -- modele non telecharge"
elif [ -s "$ROOT/data/models/bge-m3/model.onnx" ]; then
  ok "BGE-M3 deja present dans data/models/bge-m3"
else
  log "BGE-M3 ONNX (~2,3 Go) via scripts/fetch-model.sh"
  bash "$ROOT/scripts/fetch-model.sh" || warn "fetch-model.sh a echoue -- 'make local-model' plus tard"
fi

# ------------------------------------------------------------------ 6. bilan
log "verification finale"
miss=0
for t in gcc g++ cmake curl git unzip zstd jq flock soffice pdftotext go cargo rustc; do
  if command -v "$t" >/dev/null 2>&1; then
    printf '  %s%-12s%s %s\n' "$c_g" "$t" "$c_0" "$(command -v "$t")" >&2
  else
    printf '  %s%-12s MANQUANT%s\n' "$c_r" "$t" "$c_0" >&2; miss=$((miss + 1))
  fi
done
ram=$(awk '/MemTotal/{printf "%.1f", $2/1024/1024}' /proc/meminfo)
printf '  RAM vue par WSL : %s Go\n' "$ram" >&2
if [ "$(printf '%.0f' "$ram")" -lt 24 ]; then
  warn "WSL voit moins de 24 Go : la construction de l'index HNSW sera SAUTEE."
  warn "Corrige cote Windows dans %USERPROFILE%\\.wslconfig :"
  warn "    [wsl2]"
  warn "    memory=24GB"
  warn "    swap=16GB"
  warn "puis 'wsl --shutdown' et relance."
fi
[ "$miss" -eq 0 ] && ok "toolchain complete -- 'make corpus' est pret" \
                  || die "$miss outil(s) manquant(s)"
