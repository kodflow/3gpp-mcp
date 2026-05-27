#!/usr/bin/env bats
# Tests for .githooks/commit-msg — git-native AI-attribution + workflow-leak guard.

setup() {
    load '../helpers/setup'
    common_setup
    HOOK="${BATS_TEST_DIRNAME}/../../.githooks/commit-msg"
    MSG="$TEST_TMPDIR/msg"
}

teardown() {
    common_teardown
}

@test "commit-msg: blocks Plan: footer (#364)" {
    cat > "$MSG" <<'EOF'
docs: update
Plan: .claude/plans/foo.md
EOF
    run "$HOOK" "$MSG"
    [ "$status" -eq 1 ]
}

@test "commit-msg: blocks inline .claude/ path reference (#364)" {
    echo "fix: see .claude/contexts/x" > "$MSG"
    run "$HOOK" "$MSG"
    [ "$status" -eq 1 ]
}

@test "commit-msg: blocks 'see .claude/...' shape (#364)" {
    echo "chore: see .claude/agents/foo for context" > "$MSG"
    run "$HOOK" "$MSG"
    [ "$status" -eq 1 ]
}

@test "commit-msg: still blocks AI co-authored-by (#358)" {
    cat > "$MSG" <<'EOF'
fix: thing

Co-Authored-By: Claude <noreply@anthropic.com>
EOF
    run "$HOOK" "$MSG"
    [ "$status" -eq 1 ]
}

@test "commit-msg: still blocks 🤖 emoji" {
    echo "fix: 🤖 done" > "$MSG"
    run "$HOOK" "$MSG"
    [ "$status" -eq 1 ]
}

@test "commit-msg: legitimate 'plan ahead' message passes" {
    echo "docs: plan ahead for v2 release" > "$MSG"
    run "$HOOK" "$MSG"
    [ "$status" -eq 0 ]
}

@test "commit-msg: clean message passes" {
    echo "fix(scope): describe the change" > "$MSG"
    run "$HOOK" "$MSG"
    [ "$status" -eq 0 ]
}
