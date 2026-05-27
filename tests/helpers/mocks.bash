#!/usr/bin/env bash
# Mock functions for bats tests

# Generate hook JSON input for PreToolUse
# Usage: mock_hook_input "Bash" "git commit -m 'test'"
mock_hook_input() {
    local tool_name="${1:-Bash}"
    local command="${2:-}"
    jq -n \
        --arg tool "$tool_name" \
        --arg cmd "$command" \
        '{"tool_name":$tool,"tool_input":{"command":$cmd}}'
}

# Create a fake Makefile with specified targets
# Usage: mock_makefile "$dir" "build" "lint" "test"
mock_makefile() {
    local dir="$1"
    shift
    {
        for target in "$@"; do
            printf '%s:\n\t@echo "%s done"\n\n' "$target" "$target"
        done
    } > "$dir/Makefile"
}

# Create a Makefile that supports FILE= parameter
# Usage: mock_makefile_with_file "$dir" "fmt" "lint"
mock_makefile_with_file() {
    local dir="$1"
    shift
    {
        echo 'FILE ?='
        echo ''
        for target in "$@"; do
            printf '%s:\n\t@echo "%s $(FILE)"\n\n' "$target" "$target"
        done
    } > "$dir/Makefile"
}

# Generate fake secret patterns dynamically (avoids GitGuardian detection in source)
# Patterns must match git-guard.sh regexes but not appear as literals here
fake_github_pat() {
    # Builds: ghp_ + 36 alpha chars (matches ghp_[a-zA-Z0-9]{36})
    local p="gh"
    local s="p"
    printf '%s%s_%s' "$p" "$s" "aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789"
}

fake_aws_key() {
    # Builds: AKIA + 16 uppercase/digits (matches AKIA[0-9A-Z]{16})
    local a="AK"
    local b="IA"
    printf '%s%s%s' "$a" "$b" "IOSFODNN7EXAMPLE"
}
