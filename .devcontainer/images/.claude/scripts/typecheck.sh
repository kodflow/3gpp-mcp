#!/bin/bash
# Type check files based on extension
# Usage: typecheck.sh <file_path>
#
# Strategy:
#   1. If Makefile exists with typecheck target → make typecheck FILE=<path>
#   2. Otherwise → direct type checker (tsc, mypy, cargo check, etc.)
#
# Note: This focuses on TYPE SAFETY, not general linting.
# lint.sh handles general code quality issues.

set +e  # Fail-open: hooks should never block unexpectedly

FILE="${1:-}"
if [ -z "$FILE" ] || [ ! -f "$FILE" ]; then
    exit 0
fi

EXT="${FILE##*.}"
DIR=$(dirname "$FILE")

# Source shared utilities
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=common.sh
[ -f "$SCRIPT_DIR/common.sh" ] && . "$SCRIPT_DIR/common.sh"

PROJECT_ROOT=$(find_project_root "$DIR" "$DIR")

# === Makefile-first approach ===
if has_makefile_target "typecheck" "$PROJECT_ROOT"; then
    run_makefile_target "typecheck" "$FILE" "$PROJECT_ROOT"
    exit 0
fi

# === Fallback: Direct type checkers ===

case "$EXT" in
    # TypeScript - tsc for type checking (not covered by eslint)
    ts|tsx)
        if command -v tsc &>/dev/null; then
            (cd "$PROJECT_ROOT" && tsc --noEmit 2>/dev/null) || true
        elif command -v npx &>/dev/null && [ -f "$PROJECT_ROOT/package.json" ]; then
            (cd "$PROJECT_ROOT" && npx tsc --noEmit 2>/dev/null) || true
        fi
        ;;

    # Python - mypy for strict type checking (stricter than ruff)
    py)
        if command -v mypy &>/dev/null; then
            mypy --strict "$FILE" 2>/dev/null || true
        elif command -v pyright &>/dev/null; then
            pyright "$FILE" 2>/dev/null || true
        fi
        ;;

    # Go - go vet for type/correctness issues
    go)
        if command -v go &>/dev/null; then
            go vet "$FILE" 2>/dev/null || true
        fi
        ;;

    # Rust - cargo check (fast type checking without full build)
    rs)
        [[ -f "$HOME/.cache/cargo/env" ]] && source "$HOME/.cache/cargo/env"
        if command -v cargo &>/dev/null && [ -f "$PROJECT_ROOT/Cargo.toml" ]; then
            (cd "$PROJECT_ROOT" && cargo check 2>/dev/null) || true
        fi
        ;;

    # Java - compile check
    java)
        if command -v javac &>/dev/null; then
            javac -Xlint:all "$FILE" -d /tmp 2>/dev/null || true
        fi
        ;;

    # PHP - phpstan max level (stricter than general lint)
    php)
        if command -v phpstan &>/dev/null; then
            phpstan analyse -l max "$FILE" 2>/dev/null || true
        elif command -v psalm &>/dev/null; then
            psalm "$FILE" 2>/dev/null || true
        fi
        ;;

    # Ruby - steep or sorbet for type checking
    rb)
        if command -v steep &>/dev/null && [ -f "$PROJECT_ROOT/Steepfile" ]; then
            (cd "$PROJECT_ROOT" && steep check 2>/dev/null) || true
        elif command -v srb &>/dev/null; then
            srb tc "$FILE" 2>/dev/null || true
        fi
        ;;

    # Scala - scalac type check
    scala)
        if command -v scalac &>/dev/null; then
            scalac -Werror "$FILE" -d /tmp 2>/dev/null || true
        fi
        ;;

    # Elixir - dialyzer for type analysis
    ex|exs)
        if command -v mix &>/dev/null && [ -f "$PROJECT_ROOT/mix.exs" ]; then
            (cd "$PROJECT_ROOT" && mix dialyzer 2>/dev/null) || true
        fi
        ;;

    # Dart - dart analyze with strict mode
    dart)
        if command -v dart &>/dev/null; then
            dart analyze --fatal-infos "$FILE" 2>/dev/null || true
        fi
        ;;

    # C++ - clang type/syntax checking
    cpp|cc|cxx|hpp)
        if command -v clang++ &>/dev/null; then
            clang++ -std=c++23 -fsyntax-only -Werror "$FILE" 2>/dev/null || true
        elif command -v g++ &>/dev/null; then
            g++ -std=c++23 -fsyntax-only -Werror "$FILE" 2>/dev/null || true
        fi
        ;;

    # C - clang type/syntax checking
    c|h)
        if command -v clang &>/dev/null; then
            clang -std=c23 -fsyntax-only -Werror "$FILE" 2>/dev/null || true
        elif command -v gcc &>/dev/null; then
            gcc -std=c23 -fsyntax-only -Werror "$FILE" 2>/dev/null || true
        fi
        ;;

    # C# - dotnet build (type checking)
    cs)
        if command -v dotnet &>/dev/null; then
            dotnet build --no-restore 2>/dev/null || true
        fi
        ;;

    # Kotlin - kotlinc progressive check
    kt|kts)
        if command -v kotlinc &>/dev/null; then
            kotlinc -progressive -Werror "$FILE" -d /tmp 2>/dev/null || true
        fi
        ;;

    # Swift - swiftc typecheck
    swift)
        if command -v swiftc &>/dev/null; then
            swiftc -typecheck "$FILE" 2>/dev/null || true
        fi
        ;;

    # Perl - syntax check
    pl|pm)
        if command -v perl &>/dev/null; then
            perl -c "$FILE" 2>/dev/null || true
        fi
        ;;

    # Fortran - syntax check
    f|f90|f95|f03|f08)
        if command -v gfortran &>/dev/null; then
            gfortran -fsyntax-only "$FILE" 2>/dev/null || true
        fi
        ;;

    # COBOL - syntax check
    cob|cbl)
        if command -v cobc &>/dev/null; then
            cobc -fsyntax-only "$FILE" 2>/dev/null || true
        fi
        ;;

    # Pascal - syntax + type check
    pas|dpr|pp)
        if command -v fpc &>/dev/null; then
            fpc -Sew "$FILE" -o/dev/null 2>/dev/null || true
        fi
        ;;

    # Visual Basic .NET - dotnet build
    vb)
        if command -v dotnet &>/dev/null; then
            dotnet build --no-restore 2>/dev/null || true
        fi
        ;;

    # Ada - gnat compile check
    adb|ads)
        if command -v gcc &>/dev/null; then
            gcc -c -gnatc "$FILE" 2>/dev/null || true
        fi
        ;;
esac

exit 0
