#!/usr/bin/env bash
#
# toolchain-env.sh — resout la toolchain de build et l'exporte. SOURCEABLE.
#
#   source scripts/local/toolchain-env.sh
#
# Deux environnements sont supportes, detectes automatiquement :
#
#   LINUX / WSL2  chemin cible du projet. Go + rustup systeme, gcc, LibreOffice,
#                 ONNX Runtime .so, CUDA par passthrough. C'est la cible de
#                 reference : elle correspond a ce que la CI et les images
#                 construisent.
#
#   WINDOWS       chemin de secours, entierement PORTABLE et sans elevation :
#                 Go et w64devkit (gcc/g++/make) sont extraits sous
#                 .local/toolchain/. Suffisant pour compiler et tester tout le
#                 read-side Go, y compris DuckDB via CGO — le module
#                 duckdb-go-bindings publie bien un artefact windows-amd64.
#                 Ne couvre PAS LibreOffice ni l'embed GPU.
#
# Idempotent : ne reinstalle rien, ne fait qu'exporter ce qui existe.
# shellcheck shell=bash

have() { command -v "$1" >/dev/null 2>&1; }

_TE_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
_TE_LOCAL="$_TE_ROOT/.local/toolchain"

case "$(uname -s 2>/dev/null)" in
  MINGW*|MSYS*|CYGWIN*) TOOLCHAIN_PLATFORM="windows" ;;
  *)                    TOOLCHAIN_PLATFORM="unix" ;;
esac
export TOOLCHAIN_PLATFORM

if [ "$TOOLCHAIN_PLATFORM" = "windows" ]; then
  # Go portable
  if [ -x "$_TE_LOCAL/go/bin/go.exe" ]; then
    export GOROOT="$_TE_LOCAL/go"
    case ":$PATH:" in *":$_TE_LOCAL/go/bin:"*) ;; *) PATH="$_TE_LOCAL/go/bin:$PATH";; esac
  fi
  # w64devkit apporte make + les utilitaires POSIX (busybox). On le met d'abord
  # pour que ucrt64 puisse le SHADOWER sur gcc juste apres.
  if [ -x "$_TE_LOCAL/w64devkit/bin/gcc.exe" ]; then
    case ":$PATH:" in *":$_TE_LOCAL/w64devkit/bin:"*) ;; *) PATH="$_TE_LOCAL/w64devkit/bin:$PATH";; esac
  fi
  # ucrt64 (winlibs) : LE compilateur a utiliser. Doit passer AVANT w64devkit,
  # inconditionnellement — un simple "deja dans le PATH ?" ne suffit pas, il faut
  # garantir la PRIORITE, pas la presence.
  #
  # w64devkit est bati sur msvcrt.dll, l'ancien CRT. duckdb.dll (comme toute DLL
  # Windows moderne) est batie sur UCRT. Un binaire qui melange les deux a DEUX
  # tas: DuckDB alloue avec ucrtbase, cgo libere avec msvcrt, et le process meurt
  # en 0xC0000374 STATUS_HEAP_CORRUPTION des la premiere requete — build vert,
  # crash a l'execution. Un mingw UCRT met les deux cotes sur le meme tas.
  if [ -x "$_TE_LOCAL/ucrt64/bin/gcc.exe" ]; then
    PATH="$_TE_LOCAL/ucrt64/bin:$(printf '%s' "$PATH" | tr ':' '\n' | grep -vxF "$_TE_LOCAL/ucrt64/bin" | paste -sd: -)"
  fi
  command -v gcc >/dev/null 2>&1 && export CC="${CC:-gcc}" CXX="${CXX:-g++}"
  # Rust portable : rustup-init a ete lance avec RUSTUP_HOME/CARGO_HOME dans
  # .local/toolchain pour ne rien ecrire dans le profil utilisateur. L'hote est
  # x86_64-pc-windows-gnu (et non -msvc) pour partager le mingw UCRT ci-dessus :
  # le crate duckdb compile DuckDB depuis les sources et a besoin du meme C++
  # runtime que le reste du binaire.
  if [ -x "$_TE_LOCAL/cargo/bin/cargo.exe" ]; then
    export RUSTUP_HOME="$_TE_LOCAL/rustup" CARGO_HOME="$_TE_LOCAL/cargo"
    case ":$PATH:" in *":$_TE_LOCAL/cargo/bin:"*) ;; *) PATH="$_TE_LOCAL/cargo/bin:$PATH";; esac
  elif [ -x "$HOME/.cargo/bin/cargo.exe" ]; then
    case ":$PATH:" in *":$HOME/.cargo/bin:"*) ;; *) PATH="$HOME/.cargo/bin:$PATH";; esac
  fi
else
  [ -d /usr/local/go/bin ] && case ":$PATH:" in *":/usr/local/go/bin:"*) ;; *) PATH="/usr/local/go/bin:$PATH";; esac
  [ -d "$HOME/.cargo/bin" ] && case ":$PATH:" in *":$HOME/.cargo/bin:"*) ;; *) PATH="$HOME/.cargo/bin:$PATH";; esac
  [ -d "$HOME/go/bin" ] && case ":$PATH:" in *":$HOME/go/bin:"*) ;; *) PATH="$HOME/go/bin:$PATH";; esac
fi
export PATH

# CGO est OBLIGATOIRE : DuckDB (go-duckdb) et ONNX Runtime passent par lui.
# Un build CGO_ENABLED=0 echoue au link de duckdb-go-bindings — mieux vaut
# l'imposer ici que laisser un "undefined: bindings.Type" incomprehensible.
export CGO_ENABLED="${CGO_ENABLED:-1}"

# ---------------------------------------------------------------- DuckDB
#
# GOTAGS porte les build tags a passer a go build/test. Sous Windows on impose
# `duckdb_use_lib` (lien DYNAMIQUE sur une libduckdb fournie) au lieu du chemin
# par defaut (lien STATIQUE sur les libs prebuild). Deux raisons cumulees :
#
#  1. Les libs statiques windows-amd64 sont compilees avec MSVC : elles exportent
#     des symboles de la STL MSVC (`std::fpos<_Mbstatet>`, `_Ios_Openmode`) et de
#     l'UCRT (`__stdio_common_vsnprintf_s`). Aucun mingw ne peut les lier — il
#     faudrait MSVC Build Tools (~7 Go, elevation). L'API C de DuckDB, elle, est
#     du C pur : une DLL MSVC se lie parfaitement depuis mingw.
#
#  2. Le go.mod du projet requiert DEUX familles de bindings incompatibles :
#     `duckdb-go-bindings/lib/<plat> v0.10503.0` (DuckDB 1.5.3, ce qu'importe
#     reellement prebuilt_<plat>.go) ET `duckdb-go-bindings/<plat> v0.1.24`
#     (DuckDB 1.4.3, tire par go-duckdb/mapping v0.0.21). Le lien statique melange
#     donc les headers 1.5.3 et les objets 1.4.3. `duckdb_use_lib` neutralise le
#     probleme : aucun module plateforme n'est utilise, une seule libduckdb decide.
#     Voir docs/audit-resolution.md (F26).
#
# La version de la DLL doit correspondre aux HEADERS du module d'API
# (duckdb-go-bindings/include/duckdb.h), pas au pin historique de
# contracts/identity.toml.
DUCKDB_LIB_DIR="${DUCKDB_LIB_DIR:-$_TE_LOCAL/duckdb}"
export DUCKDB_LIB_DIR
if [ "$TOOLCHAIN_PLATFORM" = "windows" ] && [ -f "$DUCKDB_LIB_DIR/duckdb.dll" ]; then
  export GOTAGS="${GOTAGS:-duckdb_use_lib}"
  export CGO_LDFLAGS="${CGO_LDFLAGS:--L$DUCKDB_LIB_DIR}"
  # duckdb.dll doit etre trouvable a l'EXECUTION, pas seulement au lien.
  case ":$PATH:" in *":$DUCKDB_LIB_DIR:"*) ;; *) PATH="$DUCKDB_LIB_DIR:$PATH";; esac
  export PATH
else
  export GOTAGS="${GOTAGS:-}"
fi

# LibreOffice — le convertisseur .doc/.docx -> HTML de l'etape `fetch`.
#
# toolchain-bootstrap.sh l'extrait sous .local/toolchain/libreoffice (msiexec /a,
# sans elevation) mais rien ne l'exportait : `soffice` restait introuvable et
# `fetch` echouait au premier appel, apres avoir deja telecharge. Le binaire
# etait la depuis le debut ; seul le PATH manquait.
if [ -d "$_TE_LOCAL/libreoffice/program" ]; then
  case ":$PATH:" in
    *":$_TE_LOCAL/libreoffice/program:"*) ;;
    *) PATH="$PATH:$_TE_LOCAL/libreoffice/program";;
  esac
  export PATH
fi

# pandoc — l'echelon 3 de la cascade de secours de lib/convert.sh.
#
# Sans lui la cascade n'a qu'UN echelon utile sur quatre pour un .docx : l'export
# direct crashe sur les figures EMF/WMF, le strip EMF est le seul recours, et
# l'echelon 4 (antiword/catdoc) ne lit que le .doc binaire. C'est ce qui a fait
# perdre TS 33.501 sur Rel-17/18/19/20, TS 28.552 et TS 26.253 — des specs
# centrales, pas des cas limites.
if [ -x "$_TE_LOCAL/pandoc/pandoc.exe" ] || [ -x "$_TE_LOCAL/pandoc/pandoc" ]; then
  case ":$PATH:" in
    *":$_TE_LOCAL/pandoc:"*) ;;
    *) PATH="$PATH:$_TE_LOCAL/pandoc";;
  esac
  export PATH
fi

# gobuild / gotest — wrappers qui appliquent GOTAGS sans le dupliquer partout.
gobuild() { if [ -n "$GOTAGS" ]; then go build -tags "$GOTAGS" "$@"; else go build "$@"; fi; }
gotest()  { if [ -n "$GOTAGS" ]; then go test  -tags "$GOTAGS" "$@"; else go test  "$@"; fi; }

# toolchain_identity — empreinte des outils qui peuvent changer un resultat de
# build. Entre dans le fingerprint des etapes de compilation, PAS dans celui des
# etapes de donnees (un bump de Go ne doit pas invalider le corpus telecharge).
toolchain_identity() {
  {
    go version 2>/dev/null || echo "go absent"
    "${CC:-cc}" --version 2>/dev/null | head -1 || echo "cc absent"
    (cargo --version 2>/dev/null) || echo "cargo absent"
    (rustc --version 2>/dev/null) || echo "rustc absent"
    printf 'cgo=%s platform=%s\n' "$CGO_ENABLED" "$TOOLCHAIN_PLATFORM"
  } | sha256sum | cut -c1-16
}

