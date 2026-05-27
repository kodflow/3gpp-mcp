#!/usr/bin/env bats
# Tests for step_rtk_settings_migration in postStart.sh.
#
# Migrates stale ~/.claude/settings.json references to the legacy
# rtk-rewrite.sh hook (removed in #341/#349) by rewriting them in place to
# the new rtk-hook-claude.sh wrapper (#348). The migration is in-place,
# minimal, and idempotent.

setup() {
    load '../helpers/setup'
    common_setup

    SCRIPT="${BATS_TEST_DIRNAME}/../../.devcontainer/images/hooks/lifecycle/postStart.sh"

    # Sandbox HOME so writes don't touch the developer's real ~/.claude.
    export TEST_HOME="$TEST_TMPDIR/home"
    mkdir -p "$TEST_HOME/.claude/scripts"
    SETTINGS="$TEST_HOME/.claude/settings.json"
    WRAPPER="$TEST_HOME/.claude/scripts/rtk-hook-claude.sh"
}

teardown() {
    common_teardown
}

extract_step_fn() {
    awk '/^step_rtk_settings_migration\(\) \{/,/^\}/' "$SCRIPT"
}

# Run the extracted migration step against $TEST_HOME.
run_step() {
    bash -c "
        set -uo pipefail
        export HOME='$TEST_HOME'
        log_info()    { printf '[INFO] %s\n' \"\$*\"; }
        log_warning() { printf '[WARN] %s\n' \"\$*\"; }
        log_success() { printf '[OK] %s\n' \"\$*\"; }
        $(extract_step_fn)
        step_rtk_settings_migration
    "
}

stub_wrapper() {
    : > "$WRAPPER"
    chmod +x "$WRAPPER"
}

# === Edge cases (no-op paths) ===

@test "no settings.json → silent no-op, exit 0" {
    run run_step
    [ "$status" -eq 0 ]
    [ ! -f "$SETTINGS" ]
}

@test "settings.json without legacy reference → no-op, file untouched" {
    cat > "$SETTINGS" <<EOF
{
  "hooks": {
    "PreToolUse": [{
      "command": "$TEST_HOME/.claude/scripts/rtk-hook-claude.sh"
    }]
  }
}
EOF
    local before
    before=$(sha256sum "$SETTINGS" | awk '{print $1}')

    stub_wrapper
    run run_step
    [ "$status" -eq 0 ]

    local after
    after=$(sha256sum "$SETTINGS" | awk '{print $1}')
    [ "$before" = "$after" ]
}

# === Happy path ===

@test "legacy reference + wrapper present → rewritten in place" {
    cat > "$SETTINGS" <<EOF
{
  "hooks": {
    "PreToolUse": [{
      "command": "$TEST_HOME/.claude/scripts/rtk-rewrite.sh",
      "timeout": 5
    }]
  }
}
EOF
    stub_wrapper

    run run_step
    [ "$status" -eq 0 ]

    grep -q "rtk-hook-claude.sh" "$SETTINGS"
    ! grep -q "rtk-rewrite.sh" "$SETTINGS"
    [[ "$output" == *"rtk-rewrite.sh → rtk-hook-claude.sh"* ]]
}

@test "rewrite preserves surrounding JSON keys (timeout, async, other hooks)" {
    cat > "$SETTINGS" <<EOF
{
  "permissions": {
    "allow": ["Bash(rtk:*)"]
  },
  "hooks": {
    "PreToolUse": [
      {
        "command": "/home/vscode/.claude/scripts/git-guard.sh",
        "timeout": 15
      },
      {
        "command": "$TEST_HOME/.claude/scripts/rtk-rewrite.sh",
        "timeout": 5
      },
      {
        "command": "/home/vscode/.claude/scripts/log.sh",
        "timeout": 5,
        "async": true
      }
    ]
  }
}
EOF
    stub_wrapper

    run run_step
    [ "$status" -eq 0 ]

    # Sibling entries unchanged.
    grep -q "git-guard.sh" "$SETTINGS"
    grep -q "log.sh" "$SETTINGS"
    grep -q '"async": true' "$SETTINGS"
    grep -q '"timeout": 15' "$SETTINGS"
    # Only the rtk-rewrite line was touched; structure is otherwise byte-stable.
    grep -q "\"command\": \"$TEST_HOME/.claude/scripts/rtk-hook-claude.sh\"" "$SETTINGS"
}

# === Non-vscode user safety (CodeRabbit thread on PR #353) ===

@test "settings.json with foreign /home/vscode path is NOT mis-migrated when HOME differs" {
    # Simulate a consumer running as a different user whose settings.json
    # somehow contains the vscode-user path. Since we use $HOME-derived
    # fixed-string match, this should be a silent no-op.
    cat > "$SETTINGS" <<'EOF'
{ "command": "/home/vscode/.claude/scripts/rtk-rewrite.sh" }
EOF
    local before
    before=$(sha256sum "$SETTINGS" | awk '{print $1}')

    stub_wrapper
    run run_step
    [ "$status" -eq 0 ]

    local after
    after=$(sha256sum "$SETTINGS" | awk '{print $1}')
    [ "$before" = "$after" ]
    # No false-positive success log
    ! [[ "$output" == *"rtk settings migration:"*"→"* ]]
}

# === Idempotency ===

@test "second invocation is a no-op (file unchanged after first migration)" {
    cat > "$SETTINGS" <<EOF
{ "command": "$TEST_HOME/.claude/scripts/rtk-rewrite.sh" }
EOF
    stub_wrapper

    run run_step
    [ "$status" -eq 0 ]
    local after_first
    after_first=$(sha256sum "$SETTINGS" | awk '{print $1}')

    run run_step
    [ "$status" -eq 0 ]
    local after_second
    after_second=$(sha256sum "$SETTINGS" | awk '{print $1}')

    [ "$after_first" = "$after_second" ]
}

# === Failure mode: wrapper absent ===

@test "legacy reference + wrapper missing → warn, leave file untouched" {
    cat > "$SETTINGS" <<EOF
{ "command": "$TEST_HOME/.claude/scripts/rtk-rewrite.sh" }
EOF
    # NOTE: stub_wrapper NOT called — wrapper is missing.
    local before
    before=$(sha256sum "$SETTINGS" | awk '{print $1}')

    run run_step
    [ "$status" -eq 0 ]

    local after
    after=$(sha256sum "$SETTINGS" | awk '{print $1}')
    [ "$before" = "$after" ]
    [[ "$output" == *"missing"* ]]
}
