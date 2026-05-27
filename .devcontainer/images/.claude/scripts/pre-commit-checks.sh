#!/bin/bash
# ============================================================================
# pre-commit-checks.sh - Auto-detect project languages and run checks
# Issue #141: Multi-language pre-commit validation
# ============================================================================
# Detects all languages used in a project by scanning dependency files,
# then runs appropriate lint/build/test checks for each detected language.
# ============================================================================

set -euo pipefail

# Source shared utilities (load_local_override, has_makefile_target helpers, …)
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=common.sh
[ -f "$SCRIPT_DIR/common.sh" ] && . "$SCRIPT_DIR/common.sh"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

# ============================================================================
# Language Detection
# ============================================================================

# Initialise empty so ${#DETECTED_LANGUAGES[@]} / ${!DETECTED_LANGUAGES[@]} are
# bound under `set -u` even when detect_languages finds no markers (#363).
#
# WHY two statements instead of `declare -A X=()`: kcov v43 instruments the
# script with DEBUG traps that briefly desynchronise the variable's
# associative-flag state from its value-initialisation when both happen on
# the same statement. Symptom in CI: the test passes under raw bats but
# fails under `Collect coverage` because `${#DETECTED_LANGUAGES[@]}` trips
# set -u as if the array were unbound. Splitting the declaration from the
# assignment makes each step a separate trap point and kcov sees a fully
# bound array before the empty-init runs.
declare -A DETECTED_LANGUAGES
DETECTED_LANGUAGES=()

detect_languages() {
    local workspace="${1:-/workspace}"

    # Go
    if [[ -f "$workspace/go.mod" ]]; then
        DETECTED_LANGUAGES["go"]=1
    fi

    # Rust
    if [[ -f "$workspace/Cargo.toml" ]]; then
        DETECTED_LANGUAGES["rust"]=1
    fi

    # Node.js/TypeScript
    if [[ -f "$workspace/package.json" ]]; then
        DETECTED_LANGUAGES["nodejs"]=1
    fi

    # Python
    if [[ -f "$workspace/pyproject.toml" ]] || [[ -f "$workspace/requirements.txt" ]] || [[ -f "$workspace/setup.py" ]]; then
        DETECTED_LANGUAGES["python"]=1
    fi

    # Ruby
    if [[ -f "$workspace/Gemfile" ]]; then
        DETECTED_LANGUAGES["ruby"]=1
    fi

    # Java (Maven)
    if [[ -f "$workspace/pom.xml" ]]; then
        DETECTED_LANGUAGES["java-maven"]=1
    fi

    # Java/Kotlin (Gradle)
    if [[ -f "$workspace/build.gradle" ]] || [[ -f "$workspace/build.gradle.kts" ]]; then
        DETECTED_LANGUAGES["java-gradle"]=1
    fi

    # Elixir
    if [[ -f "$workspace/mix.exs" ]]; then
        DETECTED_LANGUAGES["elixir"]=1
    fi

    # PHP
    if [[ -f "$workspace/composer.json" ]]; then
        DETECTED_LANGUAGES["php"]=1
    fi

    # Dart/Flutter
    if [[ -f "$workspace/pubspec.yaml" ]]; then
        DETECTED_LANGUAGES["dart"]=1
    fi

    # Scala
    if [[ -f "$workspace/build.sbt" ]]; then
        DETECTED_LANGUAGES["scala"]=1
    fi

    # C/C++ (CMake)
    if [[ -f "$workspace/CMakeLists.txt" ]]; then
        DETECTED_LANGUAGES["cpp-cmake"]=1
    fi

    # C/C++ (Meson)
    if [[ -f "$workspace/meson.build" ]]; then
        DETECTED_LANGUAGES["cpp-meson"]=1
    fi

    # C# (.NET)
    if compgen -G "$workspace/*.csproj" > /dev/null 2>&1 || compgen -G "$workspace/*.sln" > /dev/null 2>&1; then
        DETECTED_LANGUAGES["csharp"]=1
    fi

    # Swift
    if [[ -f "$workspace/Package.swift" ]]; then
        DETECTED_LANGUAGES["swift"]=1
    fi

    # R
    if [[ -f "$workspace/DESCRIPTION" ]] && grep -q "Package:" "$workspace/DESCRIPTION" 2>/dev/null; then
        DETECTED_LANGUAGES["r"]=1
    fi

    # Perl
    if [[ -f "$workspace/cpanfile" ]] || [[ -f "$workspace/Makefile.PL" ]] || [[ -f "$workspace/dist.ini" ]]; then
        DETECTED_LANGUAGES["perl"]=1
    fi

    # Lua
    if compgen -G "$workspace/*.rockspec" > /dev/null 2>&1 || [[ -f "$workspace/.luacheckrc" ]]; then
        DETECTED_LANGUAGES["lua"]=1
    fi

    # Fortran
    if [[ -f "$workspace/fpm.toml" ]]; then
        DETECTED_LANGUAGES["fortran"]=1
    fi

    # Ada
    if [[ -f "$workspace/alire.toml" ]] || compgen -G "$workspace/*.gpr" > /dev/null 2>&1; then
        DETECTED_LANGUAGES["ada"]=1
    fi

    # COBOL
    if compgen -G "$workspace/*.cob" > /dev/null 2>&1 || compgen -G "$workspace/*.cbl" > /dev/null 2>&1; then
        DETECTED_LANGUAGES["cobol"]=1
    fi

    # Pascal
    if compgen -G "$workspace/*.lpi" > /dev/null 2>&1 || compgen -G "$workspace/*.dproj" > /dev/null 2>&1 || compgen -G "$workspace/*.lpr" > /dev/null 2>&1; then
        DETECTED_LANGUAGES["pascal"]=1
    fi

    # VB.NET
    if compgen -G "$workspace/*.vbproj" > /dev/null 2>&1; then
        DETECTED_LANGUAGES["vbnet"]=1
    fi
}

# ============================================================================
# Check Functions per Language
# ============================================================================

run_check() {
    local name="$1"
    local cmd="$2"

    echo -e "${CYAN}[CHECK]${NC} $name..."

    if eval "$cmd" > /dev/null 2>&1; then
        echo -e "${GREEN}[PASS]${NC} $name"
        return 0
    else
        echo -e "${RED}[FAIL]${NC} $name"
        return 1
    fi
}

run_check_verbose() {
    local name="$1"
    local cmd="$2"

    echo -e "${CYAN}[CHECK]${NC} $name..."

    if eval "$cmd"; then
        echo -e "${GREEN}[PASS]${NC} $name"
        return 0
    else
        echo -e "${RED}[FAIL]${NC} $name"
        return 1
    fi
}

# Check if Makefile target exists
has_make_target() {
    local target="$1"
    [[ -f "Makefile" ]] && grep -q "^${target}:" Makefile
}

# ============================================================================
# Language-Specific Checks
# ============================================================================

check_go() {
    echo ""
    echo -e "${CYAN}--- Go Checks ---${NC}"
    local failed=0

    # Prefer Makefile targets if available
    if has_make_target "lint"; then
        run_check_verbose "Go lint (make)" "make lint" || failed=1
    elif command -v golangci-lint &> /dev/null; then
        local cfg=""
        for f in .golangci.yml .golangci.yaml .golangci.toml; do
            [[ -f "$f" ]] && { cfg="$f"; break; }
        done
        if [[ -n "$cfg" ]]; then
            run_check_verbose "Go lint (golangci-lint)" "golangci-lint run --config '$cfg' ./..." || failed=1
        else
            echo -e "${YELLOW}[SKIP]${NC} Go lint (no .golangci.{yml,yaml,toml} in project)"
        fi
    else
        echo -e "${YELLOW}[SKIP]${NC} Go lint (golangci-lint not found)"
    fi

    if has_make_target "build"; then
        run_check_verbose "Go build (make)" "make build" || failed=1
    elif has_bazel_workspace . && bz_build_cmd="$(bazel_bin)"; then
        run_check_verbose "Go build (bazel)" "$bz_build_cmd build //..." || failed=1
    else
        run_check_verbose "Go build" "go build ./..." || failed=1
    fi

    if has_make_target "test"; then
        run_check_verbose "Go tests (make)" "make test" || failed=1
    elif has_bazel_workspace . && bz_test_cmd="$(bazel_bin)"; then
        run_check_verbose "Go tests (bazel)" "$bz_test_cmd test --test_output=errors //..." || failed=1
    else
        run_check_verbose "Go tests (with race detection)" "go test -race ./..." || failed=1
    fi

    return $failed
}

check_rust() {
    echo ""
    echo -e "${CYAN}--- Rust Checks ---${NC}"
    local failed=0

    if has_make_target "lint"; then
        run_check_verbose "Rust lint (make)" "make lint" || failed=1
    else
        run_check_verbose "Rust lint (clippy)" "cargo clippy -- -D warnings" || failed=1
    fi

    if has_make_target "build"; then
        run_check_verbose "Rust build (make)" "make build" || failed=1
    else
        run_check_verbose "Rust build" "cargo build --release" || failed=1
    fi

    if has_make_target "test"; then
        run_check_verbose "Rust tests (make)" "make test" || failed=1
    else
        run_check_verbose "Rust tests" "cargo test" || failed=1
    fi

    return $failed
}

check_nodejs() {
    echo ""
    echo -e "${CYAN}--- Node.js Checks ---${NC}"
    local failed=0

    # Determine package manager
    local pm="npm"
    [[ -f "pnpm-lock.yaml" ]] && pm="pnpm"
    [[ -f "yarn.lock" ]] && pm="yarn"
    [[ -f "bun.lockb" ]] && pm="bun"

    # Check if script exists in package.json
    has_npm_script() {
        local script="$1"
        [[ -f "package.json" ]] && jq -e ".scripts.\"$script\"" package.json > /dev/null 2>&1
    }

    if has_npm_script "lint"; then
        run_check_verbose "Node.js lint" "$pm run lint" || failed=1
    elif command -v eslint &> /dev/null; then
        run_check_verbose "Node.js lint (eslint)" "eslint ." || failed=1
    else
        echo -e "${YELLOW}[SKIP]${NC} Node.js lint (no lint script)"
    fi

    if has_npm_script "build"; then
        run_check_verbose "Node.js build" "$pm run build" || failed=1
    elif has_npm_script "compile"; then
        run_check_verbose "Node.js compile" "$pm run compile" || failed=1
    else
        echo -e "${YELLOW}[SKIP]${NC} Node.js build (no build script)"
    fi

    if has_npm_script "test"; then
        run_check_verbose "Node.js tests" "$pm test" || failed=1
    else
        echo -e "${YELLOW}[SKIP]${NC} Node.js tests (no test script)"
    fi

    return $failed
}

check_python() {
    echo ""
    echo -e "${CYAN}--- Python Checks ---${NC}"
    local failed=0

    if has_make_target "lint"; then
        run_check_verbose "Python lint (make)" "make lint" || failed=1
    elif command -v ruff &> /dev/null; then
        run_check_verbose "Python lint (ruff)" "ruff check ." || failed=1
    elif command -v flake8 &> /dev/null; then
        run_check_verbose "Python lint (flake8)" "flake8 ." || failed=1
    else
        echo -e "${YELLOW}[SKIP]${NC} Python lint (no linter found)"
    fi

    # Type checking
    if command -v mypy &> /dev/null && [[ -f "pyproject.toml" ]]; then
        run_check_verbose "Python types (mypy)" "mypy ." || failed=1
    fi

    if has_make_target "test"; then
        run_check_verbose "Python tests (make)" "make test" || failed=1
    elif command -v pytest &> /dev/null; then
        run_check_verbose "Python tests (pytest)" "pytest" || failed=1
    else
        echo -e "${YELLOW}[SKIP]${NC} Python tests (pytest not found)"
    fi

    return $failed
}

check_ruby() {
    echo ""
    echo -e "${CYAN}--- Ruby Checks ---${NC}"
    local failed=0

    if has_make_target "lint"; then
        run_check_verbose "Ruby lint (make)" "make lint" || failed=1
    elif command -v rubocop &> /dev/null; then
        run_check_verbose "Ruby lint (rubocop)" "bundle exec rubocop" || failed=1
    else
        echo -e "${YELLOW}[SKIP]${NC} Ruby lint (rubocop not found)"
    fi

    if has_make_target "test"; then
        run_check_verbose "Ruby tests (make)" "make test" || failed=1
    elif [[ -f "spec/spec_helper.rb" ]]; then
        run_check_verbose "Ruby tests (rspec)" "bundle exec rspec" || failed=1
    elif command -v rake &> /dev/null; then
        run_check_verbose "Ruby tests (rake)" "bundle exec rake test" || failed=1
    else
        echo -e "${YELLOW}[SKIP]${NC} Ruby tests (no test runner found)"
    fi

    return $failed
}

check_java_maven() {
    echo ""
    echo -e "${CYAN}--- Java (Maven) Checks ---${NC}"
    local failed=0

    run_check_verbose "Java lint (checkstyle)" "mvn checkstyle:check" || failed=1
    run_check_verbose "Java build" "mvn compile -q" || failed=1
    run_check_verbose "Java tests" "mvn test -q" || failed=1

    return $failed
}

check_java_gradle() {
    echo ""
    echo -e "${CYAN}--- Java/Kotlin (Gradle) Checks ---${NC}"
    local failed=0

    local gradle="./gradlew"
    [[ ! -x "$gradle" ]] && gradle="gradle"

    run_check_verbose "Gradle check" "$gradle check" || failed=1
    run_check_verbose "Gradle build" "$gradle build -x test" || failed=1
    run_check_verbose "Gradle tests" "$gradle test" || failed=1

    return $failed
}

check_elixir() {
    echo ""
    echo -e "${CYAN}--- Elixir Checks ---${NC}"
    local failed=0

    if command -v mix &> /dev/null; then
        run_check_verbose "Elixir lint (credo)" "mix credo --strict" || failed=1
        run_check_verbose "Elixir compile" "mix compile --warnings-as-errors" || failed=1
        run_check_verbose "Elixir tests" "mix test" || failed=1
    else
        echo -e "${YELLOW}[SKIP]${NC} Elixir (mix not found)"
    fi

    return $failed
}

check_php() {
    echo ""
    echo -e "${CYAN}--- PHP Checks ---${NC}"
    local failed=0

    if [[ -f "vendor/bin/phpstan" ]]; then
        run_check_verbose "PHP lint (phpstan)" "vendor/bin/phpstan analyse" || failed=1
    elif command -v phpstan &> /dev/null; then
        run_check_verbose "PHP lint (phpstan)" "phpstan analyse" || failed=1
    else
        echo -e "${YELLOW}[SKIP]${NC} PHP lint (phpstan not found)"
    fi

    if [[ -f "vendor/bin/phpunit" ]]; then
        run_check_verbose "PHP tests (phpunit)" "vendor/bin/phpunit" || failed=1
    elif command -v phpunit &> /dev/null; then
        run_check_verbose "PHP tests (phpunit)" "phpunit" || failed=1
    else
        echo -e "${YELLOW}[SKIP]${NC} PHP tests (phpunit not found)"
    fi

    return $failed
}

check_dart() {
    echo ""
    echo -e "${CYAN}--- Dart/Flutter Checks ---${NC}"
    local failed=0

    if [[ -f "pubspec.yaml" ]] && grep -q "flutter:" pubspec.yaml; then
        # Flutter project
        run_check_verbose "Flutter analyze" "flutter analyze --fatal-infos" || failed=1
        run_check_verbose "Flutter tests" "flutter test" || failed=1
    else
        # Pure Dart project
        run_check_verbose "Dart analyze" "dart analyze --fatal-infos" || failed=1
        run_check_verbose "Dart tests" "dart test" || failed=1
    fi

    return $failed
}

check_scala() {
    echo ""
    echo -e "${CYAN}--- Scala Checks ---${NC}"
    local failed=0

    run_check_verbose "Scala compile" "sbt compile" || failed=1
    run_check_verbose "Scala tests" "sbt test" || failed=1

    return $failed
}

check_cpp_cmake() {
    echo ""
    echo -e "${CYAN}--- C++ (CMake) Checks ---${NC}"
    local failed=0

    if [[ ! -d "build" ]]; then
        mkdir -p build
        run_check_verbose "CMake configure" "cmake -B build -S ." || failed=1
    fi

    run_check_verbose "C++ build" "cmake --build build" || failed=1

    if [[ -f "build/CTestTestfile.cmake" ]]; then
        run_check_verbose "C++ tests (ctest)" "ctest --test-dir build" || failed=1
    fi

    return $failed
}

check_cpp_meson() {
    echo ""
    echo -e "${CYAN}--- C++ (Meson) Checks ---${NC}"
    local failed=0

    if [[ ! -d "builddir" ]]; then
        run_check_verbose "Meson setup" "meson setup builddir" || failed=1
    fi

    run_check_verbose "Meson compile" "meson compile -C builddir" || failed=1
    run_check_verbose "Meson tests" "meson test -C builddir" || failed=1

    return $failed
}

check_csharp() {
    echo ""
    echo -e "${CYAN}--- C# (.NET) Checks ---${NC}"
    local failed=0

    if has_make_target "lint"; then
        run_check_verbose "C# lint (make)" "make lint" || failed=1
    elif command -v dotnet &> /dev/null; then
        run_check_verbose "C# build (warnings as errors)" "dotnet build /warnaserror" || failed=1
    else
        echo -e "${YELLOW}[SKIP]${NC} C# lint (dotnet not found)"
    fi

    if has_make_target "test"; then
        run_check_verbose "C# tests (make)" "make test" || failed=1
    elif command -v dotnet &> /dev/null; then
        run_check_verbose "C# tests" "dotnet test" || failed=1
    fi

    return $failed
}

check_swift() {
    echo ""
    echo -e "${CYAN}--- Swift Checks ---${NC}"
    local failed=0

    if has_make_target "lint"; then
        run_check_verbose "Swift lint (make)" "make lint" || failed=1
    elif command -v swiftlint &> /dev/null; then
        run_check_verbose "Swift lint (swiftlint)" "swiftlint lint" || failed=1
    else
        echo -e "${YELLOW}[SKIP]${NC} Swift lint (swiftlint not found)"
    fi

    if command -v swift &> /dev/null && [[ -f "Package.swift" ]]; then
        run_check_verbose "Swift build" "swift build" || failed=1
        run_check_verbose "Swift tests" "swift test" || failed=1
    fi

    return $failed
}

check_r() {
    echo ""
    echo -e "${CYAN}--- R Checks ---${NC}"
    local failed=0

    if command -v Rscript &> /dev/null; then
        if [[ -d "R" ]]; then
            run_check_verbose "R lint (lintr)" "Rscript -e 'lintr::lint_dir(\"R\")'" || failed=1
        fi
        if [[ -d "tests" ]]; then
            run_check_verbose "R tests (testthat)" "Rscript -e 'testthat::test_dir(\"tests\")'" || failed=1
        fi
    else
        echo -e "${YELLOW}[SKIP]${NC} R (Rscript not found)"
    fi

    return $failed
}

check_perl() {
    echo ""
    echo -e "${CYAN}--- Perl Checks ---${NC}"
    local failed=0

    if command -v perlcritic &> /dev/null; then
        run_check_verbose "Perl lint (perlcritic)" "perlcritic --severity 4 lib/" || failed=1
    elif command -v perl &> /dev/null; then
        if compgen -G "lib/*.pl" > /dev/null 2>&1; then
            run_check_verbose "Perl syntax" "perl -cw lib/*.pl 2>&1" || failed=1
        fi
    else
        echo -e "${YELLOW}[SKIP]${NC} Perl lint (perlcritic not found)"
    fi

    if command -v prove &> /dev/null && [[ -d "t" ]]; then
        run_check_verbose "Perl tests (prove)" "prove -l t/" || failed=1
    fi

    return $failed
}

check_lua() {
    echo ""
    echo -e "${CYAN}--- Lua Checks ---${NC}"
    local failed=0

    if command -v luacheck &> /dev/null; then
        run_check_verbose "Lua lint (luacheck)" "luacheck ." || failed=1
    else
        echo -e "${YELLOW}[SKIP]${NC} Lua lint (luacheck not found)"
    fi

    if command -v busted &> /dev/null; then
        run_check_verbose "Lua tests (busted)" "busted" || failed=1
    fi

    return $failed
}

check_fortran() {
    echo ""
    echo -e "${CYAN}--- Fortran Checks ---${NC}"
    local failed=0

    if command -v gfortran &> /dev/null; then
        if compgen -G "src/*.f90" > /dev/null 2>&1 || compgen -G "src/*.f95" > /dev/null 2>&1 || compgen -G "src/*.f03" > /dev/null 2>&1; then
            run_check_verbose "Fortran syntax" "find src/ -name '*.f90' -o -name '*.f95' -o -name '*.f03' -o -name '*.f08' | xargs gfortran -Wall -Wextra -fsyntax-only 2>&1" || failed=1
        fi
    else
        echo -e "${YELLOW}[SKIP]${NC} Fortran (gfortran not found)"
    fi

    if command -v fpm &> /dev/null && [[ -f "fpm.toml" ]]; then
        run_check_verbose "Fortran build (fpm)" "fpm build" || failed=1
        run_check_verbose "Fortran tests (fpm)" "fpm test" || failed=1
    fi

    return $failed
}

check_ada() {
    echo ""
    echo -e "${CYAN}--- Ada Checks ---${NC}"
    local failed=0

    if command -v alr &> /dev/null && [[ -f "alire.toml" ]]; then
        run_check_verbose "Ada build (alire)" "alr build" || failed=1
    elif command -v gprbuild &> /dev/null && compgen -G "*.gpr" > /dev/null 2>&1; then
        run_check_verbose "Ada build (gprbuild)" "gprbuild -P $(compgen -G '*.gpr' | head -n 1)" || failed=1
    elif command -v gnatmake &> /dev/null; then
        echo -e "${YELLOW}[SKIP]${NC} Ada build (no project file found)"
    else
        echo -e "${YELLOW}[SKIP]${NC} Ada (gnat not found)"
    fi

    return $failed
}

check_cobol() {
    echo ""
    echo -e "${CYAN}--- COBOL Checks ---${NC}"
    local failed=0

    if command -v cobc &> /dev/null; then
        local cobol_files=""
        compgen -G "*.cob" > /dev/null 2>&1 && cobol_files+="*.cob "
        compgen -G "*.cbl" > /dev/null 2>&1 && cobol_files+="*.cbl "
        if [[ -n "$cobol_files" ]]; then
            run_check_verbose "COBOL syntax" "cobc -fsyntax-only $cobol_files 2>/dev/null" || failed=1
        fi
    else
        echo -e "${YELLOW}[SKIP]${NC} COBOL (cobc not found)"
    fi

    return $failed
}

check_pascal() {
    echo ""
    echo -e "${CYAN}--- Pascal Checks ---${NC}"
    local failed=0

    if command -v lazbuild &> /dev/null && compgen -G "*.lpi" > /dev/null 2>&1; then
        run_check_verbose "Pascal build (lazbuild)" "lazbuild $(compgen -G '*.lpi' | head -n 1)" || failed=1
    elif command -v fpc &> /dev/null && compgen -G "*.pas" > /dev/null 2>&1; then
        run_check_verbose "Pascal syntax" "find . -maxdepth 2 -name '*.pas' | xargs -I{} fpc -Se {} 2>&1" || failed=1
    else
        echo -e "${YELLOW}[SKIP]${NC} Pascal (fpc not found)"
    fi

    return $failed
}

check_vbnet() {
    echo ""
    echo -e "${CYAN}--- VB.NET Checks ---${NC}"
    local failed=0

    if command -v dotnet &> /dev/null; then
        run_check_verbose "VB.NET build" "dotnet build /warnaserror" || failed=1
        run_check_verbose "VB.NET tests" "dotnet test" || failed=1
    else
        echo -e "${YELLOW}[SKIP]${NC} VB.NET (dotnet not found)"
    fi

    return $failed
}

# ============================================================================
# Main Execution
# ============================================================================

main() {
    local workspace="${1:-/workspace}"
    local total_failed=0

    echo ""
    echo -e "${CYAN}═══════════════════════════════════════════════════════════════${NC}"
    echo -e "${CYAN}   Pre-commit Checks${NC}"
    echo -e "${CYAN}═══════════════════════════════════════════════════════════════${NC}"

    # Detect languages
    detect_languages "$workspace"

    # Bash 5.2 + `set -u` raise an unbound-variable error on `${#arr[@]}` for
    # an associative array that was declared but never assigned. The `[*]:-`
    # form is safe and equivalent for "is the array empty?" (issue #361 fix
    # spillover: the template repo itself has no root-level language marker).
    if [[ -z "${DETECTED_LANGUAGES[*]:-}" ]]; then
        echo ""
        echo -e "${YELLOW}No supported languages detected.${NC}"
        echo "Supported dependency files: go.mod, Cargo.toml, package.json,"
        echo "pyproject.toml, Gemfile, pom.xml, build.gradle, mix.exs,"
        echo "composer.json, pubspec.yaml, build.sbt, CMakeLists.txt, meson.build,"
        echo "*.csproj, Package.swift, DESCRIPTION (R), cpanfile, *.rockspec,"
        echo "fpm.toml, alire.toml, *.cob, *.lpi, *.vbproj"
        echo ""
        exit 0
    fi

    echo ""
    echo -e "  Languages detected: ${GREEN}${!DETECTED_LANGUAGES[*]}${NC}"

    # Run checks for each detected language
    for lang in "${!DETECTED_LANGUAGES[@]}"; do
        case "$lang" in
            go)           check_go || ((total_failed++)) ;;
            rust)         check_rust || ((total_failed++)) ;;
            nodejs)       check_nodejs || ((total_failed++)) ;;
            python)       check_python || ((total_failed++)) ;;
            ruby)         check_ruby || ((total_failed++)) ;;
            java-maven)   check_java_maven || ((total_failed++)) ;;
            java-gradle)  check_java_gradle || ((total_failed++)) ;;
            elixir)       check_elixir || ((total_failed++)) ;;
            php)          check_php || ((total_failed++)) ;;
            dart)         check_dart || ((total_failed++)) ;;
            scala)        check_scala || ((total_failed++)) ;;
            cpp-cmake)    check_cpp_cmake || ((total_failed++)) ;;
            cpp-meson)    check_cpp_meson || ((total_failed++)) ;;
            csharp)       check_csharp || ((total_failed++)) ;;
            swift)        check_swift || ((total_failed++)) ;;
            r)            check_r || ((total_failed++)) ;;
            perl)         check_perl || ((total_failed++)) ;;
            lua)          check_lua || ((total_failed++)) ;;
            fortran)      check_fortran || ((total_failed++)) ;;
            ada)          check_ada || ((total_failed++)) ;;
            cobol)        check_cobol || ((total_failed++)) ;;
            pascal)       check_pascal || ((total_failed++)) ;;
            vbnet)        check_vbnet || ((total_failed++)) ;;
        esac
    done

    echo ""
    echo -e "${CYAN}═══════════════════════════════════════════════════════════════${NC}"

    if [[ $total_failed -eq 0 ]]; then
        echo -e "${GREEN}   All pre-commit checks passed${NC}"
    else
        echo -e "${RED}   $total_failed language(s) failed checks${NC}"
    fi

    echo -e "${CYAN}═══════════════════════════════════════════════════════════════${NC}"
    echo ""

    return $total_failed
}

# Run if executed directly (not sourced)
if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    # Override seam: source ~/.claude/scripts/pre-commit-checks.local.sh if present.
    # Loaded after every check_* function so consumer overrides win.
    load_local_override "${BASH_SOURCE[0]}"
    main "$@"
fi
