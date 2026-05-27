#!/bin/bash
# Run tests for modified files
# Usage: test.sh <file_path>
#
# Strategy:
#   1. If Makefile exists with test target → make test FILE=<path>
#   2. Otherwise → direct test runner (jest, pytest, go test, cargo test)
#
# Makefile convention:
#   make test              # Run all tests
#   make test FILE=path    # Run tests for specific file

set +e  # Fail-open: hooks should never block unexpectedly

# Read file_path from stdin JSON (preferred) or fallback to argument
INPUT="$(cat 2>/dev/null || true)"
FILE=""
if [ -n "$INPUT" ] && command -v jq &>/dev/null; then
    FILE=$(printf '%s' "$INPUT" | jq -r '.tool_input.file_path // ""' 2>/dev/null || true)
fi
FILE="${FILE:-${1:-}}"

if [ -z "$FILE" ] || [ ! -f "$FILE" ]; then
    exit 0
fi

EXT="${FILE##*.}"
BASENAME=$(basename "$FILE")
DIR=$(dirname "$FILE")

# Pre-flight: skip files that never contain tests
case "$BASENAME" in
    *.md|*.json|*.yaml|*.yml|*.toml|*.lock|*.env|*.sh|*.css|*.scss|*.html|Dockerfile*|Makefile|*.gitignore)
        exit 0
        ;;
esac

# Source shared utilities
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=common.sh
[ -f "$SCRIPT_DIR/common.sh" ] && . "$SCRIPT_DIR/common.sh"

PROJECT_ROOT=$(find_project_root "$DIR" "$DIR")

# === Makefile-first approach ===
if has_makefile_target "test" "$PROJECT_ROOT"; then
    run_makefile_target "test" "$FILE" "$PROJECT_ROOT"
    exit 0
fi

# Override seam: source ~/.claude/scripts/test.local.sh if present.
# Loaded after the Makefile fast-path (which already exited) so consumer
# overrides can pre-empt the per-extension case dispatch — e.g. handle
# Bazel-purist projects without a Makefile, or short-circuit with `exit 0`.
load_local_override "${BASH_SOURCE[0]}"

# === Fallback: Direct test runners ===

# Check if this is a test file
IS_TEST=0
case "$BASENAME" in
    *.test.*|*.spec.*|*_test.*|test_*|*Test.java|*Tests.java|*Test.scala|*Spec.scala|*Test.cpp|*Test.cc|*Test.cs|*Tests.cs|*Test.kt|*Tests.swift|*_test.c|*.t|*_spec.lua|*_test.lua|*_test.f90|*_test.adb|*_test.pas|*Test.vb|test_*.m)
        IS_TEST=1
        ;;
esac

case "$EXT" in
    # JavaScript/TypeScript
    js|jsx|ts|tsx)
        if [ $IS_TEST -eq 1 ]; then
            if [ -f "$PROJECT_ROOT/package.json" ]; then
                cd "$PROJECT_ROOT"
                # Check for test script in package.json
                if grep -q '"test"' package.json 2>/dev/null; then
                    npm test -- "$FILE" 2>/dev/null || \
                    pnpm test "$FILE" 2>/dev/null || \
                    yarn test "$FILE" 2>/dev/null || true
                elif command -v vitest &>/dev/null; then
                    vitest run "$FILE" 2>/dev/null || true
                elif command -v jest &>/dev/null; then
                    jest "$FILE" --passWithNoTests 2>/dev/null || true
                fi
            fi
        fi
        ;;

    # Python
    py)
        if [ $IS_TEST -eq 1 ]; then
            cd "$PROJECT_ROOT"
            if command -v pytest &>/dev/null; then
                pytest "$FILE" -v 2>/dev/null || true
            elif command -v python &>/dev/null; then
                python -m pytest "$FILE" -v 2>/dev/null || true
            fi
        fi
        ;;

    # Go - tests alongside source files. Bazel-aware (issue #351):
    # Bazel-driven projects without a Makefile shadow `go test` with their
    # action cache; falling through to raw `go test` on those projects
    # produces minutes-long false negatives. Both branches keep `|| true`
    # so a PostToolUse failure never blocks the agent.
    go)
        if [[ "$BASENAME" == *"_test.go" ]]; then
            if has_bazel_workspace "$PROJECT_ROOT"; then
                BAZEL_CMD=""
                if BAZEL_CMD="$(bazel_bin)"; then
                    BAZEL_LABEL="$(bazel_label_for_dir "$DIR" "$PROJECT_ROOT")"
                    (cd "$PROJECT_ROOT" && "$BAZEL_CMD" test --test_output=errors "$BAZEL_LABEL" 2>/dev/null) || true
                fi
            elif command -v go &>/dev/null; then
                (cd "$DIR" && go test -v -run . 2>/dev/null) || true
            fi
        fi
        ;;

    # Rust
    rs)
        [[ -f "$HOME/.cache/cargo/env" ]] && source "$HOME/.cache/cargo/env"
        if command -v cargo &>/dev/null; then
            if [[ "$FILE" == *"tests"* ]] || grep -q "#\[test\]" "$FILE" 2>/dev/null; then
                (cd "$PROJECT_ROOT" && cargo test 2>/dev/null) || true
            fi
        fi
        ;;

    # Elixir
    ex|exs)
        if [[ "$BASENAME" == *"_test.exs" ]]; then
            if command -v mix &>/dev/null && [ -f "$PROJECT_ROOT/mix.exs" ]; then
                (cd "$PROJECT_ROOT" && mix test "$FILE" 2>/dev/null) || true
            fi
        fi
        ;;

    # Ruby
    rb)
        if [[ "$BASENAME" == *"_spec.rb" ]] || [[ "$BASENAME" == *"_test.rb" ]]; then
            cd "$PROJECT_ROOT"
            if command -v rspec &>/dev/null && [[ "$BASENAME" == *"_spec.rb" ]]; then
                rspec "$FILE" 2>/dev/null || true
            elif command -v ruby &>/dev/null; then
                ruby -Itest "$FILE" 2>/dev/null || true
            fi
        fi
        ;;

    # PHP
    php)
        if [[ "$BASENAME" == *"Test.php" ]]; then
            if command -v phpunit &>/dev/null; then
                phpunit "$FILE" 2>/dev/null || true
            elif [ -f "$PROJECT_ROOT/vendor/bin/phpunit" ]; then
                "$PROJECT_ROOT/vendor/bin/phpunit" "$FILE" 2>/dev/null || true
            fi
        fi
        ;;

    # Java - Maven or Gradle
    java)
        if [[ "$BASENAME" == *"Test.java" ]] || [[ "$BASENAME" == *"Tests.java" ]]; then
            cd "$PROJECT_ROOT"
            CLASS_NAME="${BASENAME%.java}"
            if [ -f "$PROJECT_ROOT/pom.xml" ]; then
                mvn test -Dtest="$CLASS_NAME" -q 2>/dev/null || true
            elif [ -f "$PROJECT_ROOT/build.gradle" ] || [ -f "$PROJECT_ROOT/build.gradle.kts" ]; then
                gradle test --tests "*$CLASS_NAME" 2>/dev/null || \
                ./gradlew test --tests "*$CLASS_NAME" 2>/dev/null || true
            fi
        fi
        ;;

    # Scala - sbt
    scala)
        if [[ "$BASENAME" == *"Test.scala" ]] || [[ "$BASENAME" == *"Spec.scala" ]]; then
            if command -v sbt &>/dev/null && [ -f "$PROJECT_ROOT/build.sbt" ]; then
                CLASS_NAME="${BASENAME%.scala}"
                (cd "$PROJECT_ROOT" && sbt "testOnly *$CLASS_NAME" 2>/dev/null) || true
            fi
        fi
        ;;

    # Dart - dart test or flutter test
    dart)
        if [[ "$BASENAME" == *"_test.dart" ]]; then
            cd "$PROJECT_ROOT"
            if [ -f "$PROJECT_ROOT/pubspec.yaml" ]; then
                if command -v flutter &>/dev/null && grep -q "flutter:" "$PROJECT_ROOT/pubspec.yaml" 2>/dev/null; then
                    flutter test "$FILE" 2>/dev/null || true
                elif command -v dart &>/dev/null; then
                    dart test "$FILE" 2>/dev/null || true
                fi
            fi
        fi
        ;;

    # C++ - ctest from build dir
    cpp|cc|cxx)
        if [[ "$BASENAME" == *"_test.cpp" ]] || [[ "$BASENAME" == *"Test.cpp" ]] || \
           [[ "$BASENAME" == *"_test.cc" ]] || [[ "$BASENAME" == *"Test.cc" ]]; then
            for build_dir in "$PROJECT_ROOT/build" "$PROJECT_ROOT/cmake-build-debug" "$PROJECT_ROOT/out/build"; do
                if [ -d "$build_dir" ] && [ -f "$build_dir/CMakeCache.txt" ]; then
                    if command -v ctest &>/dev/null; then
                        (cd "$build_dir" && ctest --output-on-failure 2>/dev/null) || true
                    fi
                    break
                fi
            done
        fi
        ;;

    # C# - dotnet test
    cs)
        if [[ "$BASENAME" == *"Test.cs" ]] || [[ "$BASENAME" == *"Tests.cs" ]]; then
            if command -v dotnet &>/dev/null; then
                CLASS_NAME="${BASENAME%.cs}"
                (cd "$PROJECT_ROOT" && dotnet test --filter "FullyQualifiedName~$CLASS_NAME" 2>/dev/null) || true
            fi
        fi
        ;;

    # Kotlin - gradle test
    kt|kts)
        if [[ "$BASENAME" == *"Test.kt" ]] || [[ "$BASENAME" == *"Test.kts" ]]; then
            cd "$PROJECT_ROOT"
            CLASS_NAME="${BASENAME%.kt}"
            CLASS_NAME="${CLASS_NAME%.kts}"
            if [ -f "$PROJECT_ROOT/build.gradle" ] || [ -f "$PROJECT_ROOT/build.gradle.kts" ]; then
                gradle test --tests "*$CLASS_NAME" 2>/dev/null || \
                ./gradlew test --tests "*$CLASS_NAME" 2>/dev/null || true
            fi
        fi
        ;;

    # Swift - swift test
    swift)
        if [[ "$BASENAME" == *"Tests.swift" ]] || [[ "$BASENAME" == *"Test.swift" ]]; then
            if command -v swift &>/dev/null && [ -f "$PROJECT_ROOT/Package.swift" ]; then
                SWIFT_CLASS="${BASENAME%.swift}"
                (cd "$PROJECT_ROOT" && swift test --filter "$SWIFT_CLASS" 2>/dev/null) || true
            fi
        fi
        ;;

    # R - testthat
    r|R)
        if [[ "$BASENAME" == test_* ]] || [[ "$FILE" == *"/tests/"* ]]; then
            if command -v Rscript &>/dev/null; then
                Rscript -e "testthat::test_file(commandArgs(TRUE)[1])" "$FILE" 2>/dev/null || true
            fi
        fi
        ;;

    # Perl - prove
    t)
        if command -v prove &>/dev/null; then
            prove "$FILE" 2>/dev/null || true
        fi
        ;;

    # Lua - busted
    lua)
        if [[ "$BASENAME" == *"_spec.lua" ]] || [[ "$BASENAME" == *"_test.lua" ]]; then
            if command -v busted &>/dev/null; then
                busted "$FILE" 2>/dev/null || true
            fi
        fi
        ;;

    # Fortran - fpm test
    f|f90|f95|f03|f08)
        if [[ "$BASENAME" == *"_test."* ]]; then
            if command -v fpm &>/dev/null && [ -f "$PROJECT_ROOT/fpm.toml" ]; then
                (cd "$PROJECT_ROOT" && fpm test 2>/dev/null) || true
            fi
        fi
        ;;

    # Ada - compile and run test
    adb)
        if [[ "$BASENAME" == *"_test.adb" ]]; then
            if command -v gnatmake &>/dev/null; then
                gnatmake "$FILE" -o /tmp/ada_test 2>/dev/null && /tmp/ada_test 2>/dev/null || true
            fi
        fi
        ;;

    # Visual Basic .NET - dotnet test
    vb)
        if [[ "$BASENAME" == *"Test.vb" ]] || [[ "$BASENAME" == *"Tests.vb" ]]; then
            if command -v dotnet &>/dev/null; then
                CLASS_NAME="${BASENAME%.vb}"
                (cd "$PROJECT_ROOT" && dotnet test --filter "FullyQualifiedName~$CLASS_NAME" 2>/dev/null) || true
            fi
        fi
        ;;

    # MATLAB/Octave - runtests (skip if Objective-C project detected)
    m)
        if [[ "$BASENAME" == test_* ]]; then
            # Heuristic: if Xcode project files exist, this is likely Objective-C, not MATLAB
            if [ -f "$PROJECT_ROOT/Package.swift" ] || \
               compgen -G "$PROJECT_ROOT/*.xcodeproj" > /dev/null 2>&1 || \
               compgen -G "$PROJECT_ROOT/*.xcworkspace" > /dev/null 2>&1; then
                : # Skip - likely Objective-C project
            elif command -v octave &>/dev/null; then
                octave --eval "runtests(argv(){1})" -- "$FILE" 2>/dev/null || true
            fi
        fi
        ;;
esac

exit 0
