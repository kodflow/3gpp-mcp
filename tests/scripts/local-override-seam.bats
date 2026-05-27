#!/usr/bin/env bats
# Tests for the *.local.sh override seam (issue #352)
# - load_local_override no-op when missing
# - load_local_override sources when present
# - last-definition-wins: .local.sh overrides upstream functions
# - safe_glob_copy skips *.local.sh during /update copy

setup() {
    load '../helpers/setup'
    common_setup

    SCRIPTS_DIR="${BATS_TEST_DIRNAME}/../../.devcontainer/images/.claude/scripts"
    QUALITY_SCRIPT="$SCRIPTS_DIR/pre-commit-quality.sh"
    CHECKS_SCRIPT="$SCRIPTS_DIR/pre-commit-checks.sh"
    TEST_SCRIPT="$SCRIPTS_DIR/test.sh"
}

teardown() {
    common_teardown
}

# ---------- core helper semantics ----------

@test "load_local_override is a silent no-op when no companion file exists" {
    # shellcheck source=/dev/null
    source "$SCRIPTS_DIR/common.sh"

    local fake="$TEST_TMPDIR/fake_script.sh"
    : > "$fake"

    run load_local_override "$fake"
    [ "$status" -eq 0 ]
    [ -z "$output" ]
}

@test "load_local_override sources the companion file when present" {
    # shellcheck source=/dev/null
    source "$SCRIPTS_DIR/common.sh"

    local fake="$TEST_TMPDIR/fake_script.sh"
    : > "$fake"
    cat > "$TEST_TMPDIR/fake_script.local.sh" <<'EOF'
SEAM_LOADED=1
EOF

    SEAM_LOADED=0
    load_local_override "$fake"
    [ "$SEAM_LOADED" -eq 1 ]
}

@test "load_local_override is a no-op when caller passes empty string" {
    # shellcheck source=/dev/null
    source "$SCRIPTS_DIR/common.sh"

    run load_local_override ""
    [ "$status" -eq 0 ]
}

# ---------- override semantics: last-definition-wins ----------

@test ".local.sh can redefine an upstream function (last-definition-wins)" {
    # shellcheck source=/dev/null
    source "$SCRIPTS_DIR/common.sh"

    local fake="$TEST_TMPDIR/fake_script.sh"
    : > "$fake"

    upstream_fn() { echo "upstream"; }

    cat > "$TEST_TMPDIR/fake_script.local.sh" <<'EOF'
upstream_fn() { echo "override"; }
EOF

    load_local_override "$fake"
    run upstream_fn
    [ "$output" = "override" ]
}

# ---------- placement contract: real scripts call seam after upstream defs ----------

@test "pre-commit-quality.sh: .local.sh override of run_test is invoked" {
    # Replicate ~/.claude/scripts/ layout in TEST_TMPDIR so the seam resolves
    # to a writable companion without touching the real script tree.
    local stage="$TEST_TMPDIR/scripts"
    mkdir -p "$stage"
    cp "$SCRIPTS_DIR/common.sh" "$stage/"
    cp "$QUALITY_SCRIPT" "$stage/"

    cat > "$stage/pre-commit-quality.local.sh" <<EOF
run_test() {
    local out="\$1"
    echo "OVERRIDE_RAN" > "$TEST_TMPDIR/sentinel"
    return 0
}
EOF

    local repo="$TEST_TMPDIR/repo"
    mkdir -p "$repo"
    git -C "$repo" init -q -b main
    git -C "$repo" config user.email "t@t.com"
    git -C "$repo" config user.name "T"
    cat > "$repo/main.go" <<'EOF'
package main
func main() {}
EOF
    git -C "$repo" add . && git -C "$repo" commit -q -m init
    git -C "$repo" checkout -q -b feature
    echo "// edit" >> "$repo/main.go"
    git -C "$repo" add main.go

    run bash -c "cd '$repo' && CLAUDE_PROJECT_DIR='$repo' bash '$stage/pre-commit-quality.sh' main 2>&1"
    [ -f "$TEST_TMPDIR/sentinel" ]
    [ "$(cat "$TEST_TMPDIR/sentinel")" = "OVERRIDE_RAN" ]
}

@test "pre-commit-checks.sh: .local.sh override of check_go is invoked" {
    local stage="$TEST_TMPDIR/scripts"
    mkdir -p "$stage"
    cp "$SCRIPTS_DIR/common.sh" "$stage/"
    cp "$CHECKS_SCRIPT" "$stage/"

    cat > "$stage/pre-commit-checks.local.sh" <<EOF
check_go() {
    echo "CHECK_GO_OVERRIDE" > "$TEST_TMPDIR/sentinel"
    return 0
}
EOF

    local repo="$TEST_TMPDIR/repo"
    mkdir -p "$repo"
    cat > "$repo/go.mod" <<EOF
module example.com/test
go 1.21
EOF

    run bash -c "bash '$stage/pre-commit-checks.sh' '$repo' 2>&1"
    [ -f "$TEST_TMPDIR/sentinel" ]
    [ "$(cat "$TEST_TMPDIR/sentinel")" = "CHECK_GO_OVERRIDE" ]
}

@test "test.sh: .local.sh can short-circuit before per-extension dispatch" {
    local stage="$TEST_TMPDIR/scripts"
    mkdir -p "$stage"
    cp "$SCRIPTS_DIR/common.sh" "$stage/"
    cp "$TEST_SCRIPT" "$stage/"

    # Override seam writes a sentinel and exits before the per-ext case.
    cat > "$stage/test.local.sh" <<EOF
echo "LOCAL_RAN" > "$TEST_TMPDIR/sentinel"
exit 0
EOF

    local repo="$TEST_TMPDIR/repo"
    mkdir -p "$repo"
    cat > "$repo/go.mod" <<EOF
module example.com/test
go 1.21
EOF
    cat > "$repo/foo_test.go" <<'EOF'
package foo
EOF

    run bash -c "bash '$stage/test.sh' '$repo/foo_test.go' 2>&1"
    [ -f "$TEST_TMPDIR/sentinel" ]
    [ "$(cat "$TEST_TMPDIR/sentinel")" = "LOCAL_RAN" ]
}

# ---------- /update copy preserves *.local.sh ----------

@test "safe_glob_copy skips *.local.sh during component sync" {
    # Inline copy of safe_glob_copy from update/apply.md — bats can't source markdown.
    safe_glob_copy() {
        local pattern="$1" dest="$2" make_exec="${3:-}"
        local dir
        dir=$(dirname "$pattern")
        local glob
        glob=$(basename "$pattern")
        while IFS= read -r -d '' f; do
            case "$(basename "$f")" in
                *.local.sh) continue ;;
            esac
            cp -f "$f" "$dest/"
            [ "$make_exec" = "+x" ] && chmod +x "$dest/$(basename "$f")"
        done < <(find "$dir" -maxdepth 1 -name "$glob" -type f -print0 2>/dev/null)
        return 0
    }

    local src="$TEST_TMPDIR/src"
    local dst="$TEST_TMPDIR/dst"
    mkdir -p "$src" "$dst"
    : > "$src/regular.sh"
    : > "$src/another.sh"
    echo "# consumer override" > "$src/regular.local.sh"

    safe_glob_copy "$src/*.sh" "$dst" "+x"

    [ -f "$dst/regular.sh" ]
    [ -f "$dst/another.sh" ]
    [ ! -f "$dst/regular.local.sh" ]
}

@test "safe_glob_copy is a no-op when no files match (empty source dir)" {
    safe_glob_copy() {
        local pattern="$1" dest="$2" make_exec="${3:-}"
        local dir
        dir=$(dirname "$pattern")
        local glob
        glob=$(basename "$pattern")
        while IFS= read -r -d '' f; do
            case "$(basename "$f")" in
                *.local.sh) continue ;;
            esac
            cp -f "$f" "$dest/"
            [ "$make_exec" = "+x" ] && chmod +x "$dest/$(basename "$f")"
        done < <(find "$dir" -maxdepth 1 -name "$glob" -type f -print0 2>/dev/null)
        return 0
    }

    local src="$TEST_TMPDIR/empty_src"
    local dst="$TEST_TMPDIR/empty_dst"
    mkdir -p "$src" "$dst"

    run safe_glob_copy "$src/*.sh" "$dst"
    [ "$status" -eq 0 ]
    [ -z "$(ls -A "$dst")" ]
}
