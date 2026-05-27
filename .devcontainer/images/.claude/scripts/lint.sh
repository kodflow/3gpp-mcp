#!/bin/bash
# Lint files based on extension
# Usage: lint.sh <file_path>
#
# Strategy:
#   1. If Makefile exists with lint target → make lint FILE=<path>
#   2. Otherwise → direct linter (eslint, ruff, golangci-lint, etc.)
#
# Note: This focuses on CODE QUALITY (style, errors, best practices).
# typecheck.sh handles strict type checking.

set +e  # Fail-open: hooks should never block unexpectedly

FILE="${1:-}"
if [ -z "$FILE" ] || [ ! -f "$FILE" ]; then
    exit 0
fi

EXT="${FILE##*.}"
DIR=$(dirname "$FILE")
BASENAME=$(basename "$FILE")

# Source shared utilities
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=common.sh
[ -f "$SCRIPT_DIR/common.sh" ] && . "$SCRIPT_DIR/common.sh"

# Hook profile gate: lint is "standard" level (skipped in minimal mode)
check_hook_profile "standard" || exit 0

PROJECT_ROOT=$(find_project_root "$DIR" "$DIR")

# === Makefile-first approach ===
if has_makefile_target "lint" "$PROJECT_ROOT"; then
    run_makefile_target "lint" "$FILE" "$PROJECT_ROOT"
    exit 0
fi

# === Fallback: Direct linters ===

case "$EXT" in
    # JavaScript/TypeScript - eslint
    js|jsx|ts|tsx|mjs|cjs)
        if command -v eslint &>/dev/null; then
            eslint --fix "$FILE" 2>/dev/null || true
        elif command -v npx &>/dev/null && [ -f "$PROJECT_ROOT/package.json" ]; then
            (cd "$PROJECT_ROOT" && npx eslint --fix "$FILE" 2>/dev/null) || true
        fi
        ;;

    # Python - ruff is faster and comprehensive
    py)
        if command -v ruff &>/dev/null; then
            ruff check --fix "$FILE" 2>/dev/null || true
        elif command -v pylint &>/dev/null; then
            pylint --errors-only "$FILE" 2>/dev/null || true
        fi
        ;;

    # Go - golangci-lint is comprehensive (skip silently when consumer has no config)
    go)
        if command -v golangci-lint &>/dev/null; then
            cfg=$(find_golangci_config "$PROJECT_ROOT") || cfg=""
            if [ -n "$cfg" ]; then
                golangci-lint run --config "$cfg" --fix "$FILE" 2>/dev/null || true
            fi
        fi
        ;;

    # Rust - clippy is the standard linter
    rs)
        [[ -f "$HOME/.cache/cargo/env" ]] && source "$HOME/.cache/cargo/env"
        if command -v cargo &>/dev/null && [ -f "$PROJECT_ROOT/Cargo.toml" ]; then
            (cd "$PROJECT_ROOT" && cargo clippy --fix --allow-dirty --allow-staged -- -D warnings 2>/dev/null) || true
        fi
        ;;

    # Shell - shellcheck
    sh|bash)
        if command -v shellcheck &>/dev/null; then
            shellcheck "$FILE" 2>/dev/null || true
        fi
        ;;

    # Dockerfile - hadolint
    Dockerfile*)
        if command -v hadolint &>/dev/null; then
            hadolint "$FILE" 2>/dev/null || true
        fi
        ;;

    # YAML - yamllint
    yml|yaml)
        if command -v yamllint &>/dev/null; then
            yamllint -d relaxed "$FILE" 2>/dev/null || true
        fi
        # Ansible-specific lint for playbooks
        if [[ "$FILE" == *"playbook"* ]] || [[ "$FILE" == *"ansible"* ]]; then
            if command -v ansible-lint &>/dev/null; then
                ansible-lint "$FILE" 2>/dev/null || true
            fi
        fi
        ;;

    # Terraform - tflint
    tf|tfvars)
        if command -v tflint &>/dev/null; then
            tflint "$FILE" 2>/dev/null || true
        fi
        ;;

    # C/C++ - clang-tidy
    c|cpp|cc|cxx|h|hpp)
        if command -v clang-tidy &>/dev/null; then
            clang-tidy "$FILE" --fix 2>/dev/null || true
        elif command -v cppcheck &>/dev/null; then
            cppcheck "$FILE" 2>/dev/null || true
        fi
        ;;

    # Java - checkstyle
    java)
        if command -v checkstyle &>/dev/null; then
            checkstyle "$FILE" 2>/dev/null || true
        fi
        ;;

    # Ruby - rubocop
    rb)
        if command -v rubocop &>/dev/null; then
            rubocop -a "$FILE" 2>/dev/null || true
        fi
        ;;

    # PHP - phpstan or syntax check
    php)
        if command -v phpstan &>/dev/null; then
            phpstan analyse "$FILE" 2>/dev/null || true
        elif command -v php &>/dev/null; then
            php -l "$FILE" 2>/dev/null || true
        fi
        ;;

    # Kotlin - ktlint
    kt|kts)
        if command -v ktlint &>/dev/null; then
            ktlint "$FILE" 2>/dev/null || true
        fi
        ;;

    # Swift - swiftlint
    swift)
        if command -v swiftlint &>/dev/null; then
            swiftlint lint --path "$FILE" 2>/dev/null || true
        fi
        ;;

    # Lua - luacheck
    lua)
        if command -v luacheck &>/dev/null; then
            luacheck "$FILE" 2>/dev/null || true
        fi
        ;;

    # SQL - sqlfluff
    sql)
        if command -v sqlfluff &>/dev/null; then
            sqlfluff lint "$FILE" 2>/dev/null || true
        fi
        ;;

    # Markdown - markdownlint
    md)
        if command -v markdownlint &>/dev/null; then
            markdownlint "$FILE" 2>/dev/null || true
        fi
        ;;

    # JSON - jsonlint
    json)
        if command -v jsonlint &>/dev/null; then
            jsonlint -q "$FILE" 2>/dev/null || true
        fi
        ;;

    # HTML - htmlhint
    html|htm)
        if command -v htmlhint &>/dev/null; then
            htmlhint "$FILE" 2>/dev/null || true
        fi
        ;;

    # CSS/SCSS - stylelint
    css|scss|less)
        if command -v stylelint &>/dev/null; then
            stylelint --fix "$FILE" 2>/dev/null || true
        fi
        ;;

    # Elixir - credo
    ex|exs)
        if command -v mix &>/dev/null && [ -f "$PROJECT_ROOT/mix.exs" ]; then
            (cd "$PROJECT_ROOT" && mix credo "$FILE" 2>/dev/null) || true
        fi
        ;;

    # Dart - dart analyze
    dart)
        if command -v dart &>/dev/null; then
            dart analyze "$FILE" 2>/dev/null || true
        fi
        ;;

    # TOML - taplo lint
    toml)
        if command -v taplo &>/dev/null; then
            taplo lint "$FILE" 2>/dev/null || true
        fi
        ;;

    # Protobuf - buf lint
    proto)
        if command -v buf &>/dev/null; then
            buf lint "$FILE" 2>/dev/null || true
        fi
        ;;

    # Scala - scalafix or scalac -Xlint
    scala)
        if command -v scalafix &>/dev/null; then
            scalafix "$FILE" 2>/dev/null || true
        elif command -v scalac &>/dev/null; then
            scalac -Xlint "$FILE" 2>/dev/null || true
        fi
        ;;

    # C# - dotnet build with warnings as errors
    cs)
        if command -v dotnet &>/dev/null; then
            dotnet build /warnaserror 2>/dev/null || true
        fi
        ;;

    # R - lintr
    r|R)
        if command -v Rscript &>/dev/null; then
            Rscript -e "lintr::lint(commandArgs(TRUE)[1])" "$FILE" 2>/dev/null || true
        fi
        ;;

    # Fortran - gfortran warnings
    f|f90|f95|f03|f08)
        if command -v gfortran &>/dev/null; then
            gfortran -Wall -Wextra -fsyntax-only "$FILE" 2>/dev/null || true
        fi
        ;;

    # COBOL - cobc warnings
    cob|cbl)
        if command -v cobc &>/dev/null; then
            cobc -Wall -fsyntax-only "$FILE" 2>/dev/null || true
        fi
        ;;

    # Pascal - fpc syntax check
    pas|dpr|pp)
        if command -v fpc &>/dev/null; then
            fpc -Se "$FILE" 2>/dev/null || true
        fi
        ;;

    # Visual Basic .NET - dotnet build with warnings
    vb)
        if command -v dotnet &>/dev/null; then
            dotnet build /warnaserror 2>/dev/null || true
        fi
        ;;

    # Ada - gnat check
    adb|ads)
        if command -v gcc &>/dev/null; then
            gcc -c -gnatwa "$FILE" 2>/dev/null || true
        fi
        ;;

    # Perl - perlcritic or syntax check
    pl|pm)
        if command -v perlcritic &>/dev/null; then
            perlcritic "$FILE" 2>/dev/null || true
        elif command -v perl &>/dev/null; then
            perl -cw "$FILE" 2>/dev/null || true
        fi
        ;;
esac

exit 0
