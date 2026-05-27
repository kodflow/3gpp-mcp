#!/usr/bin/env bats
# Tests for the shipped rtk.config.toml template.
# Layer 1 of plan 2026-05-06-rtk-mandatory-install-and-claude-memory.

setup() {
    load '../helpers/setup'
    common_setup

    TEMPLATE="${BATS_TEST_DIRNAME}/../../.devcontainer/images/rtk.config.toml"

    # Isolated rtk config dir — tests must never touch ~/.config/rtk on a real host
    export XDG_CONFIG_HOME="$TEST_TMPDIR/xdg"
    mkdir -p "$XDG_CONFIG_HOME/rtk"
    cp "$TEMPLATE" "$XDG_CONFIG_HOME/rtk/config.toml"
}

teardown() {
    common_teardown
}

# === schema tests ===

@test "template parses cleanly under current rtk" {
    if ! command -v rtk >/dev/null 2>&1; then
        skip "rtk not installed in this environment"
    fi
    run rtk config
    [ "$status" -eq 0 ]
    [[ "$output" != *"TOML parse error"* ]]
    [[ "$output" != *"missing field"* ]]
}

@test "exclude_commands is exactly the safe-trivial builtins set" {
    # Pin EXACT equality against the canonical list: cd, pwd, set, export, echo.
    # Each is a shell builtin or trivial passthrough where rewrite is meaningless.
    # Adding more here weakens RTK's default coverage — per-project additions
    # belong in filters.toml, not this template.
    run grep -E '^exclude_commands' "$TEMPLATE"
    [ "$status" -eq 0 ]
    # Strip the leading "exclude_commands = " and trailing whitespace, normalize
    # the array to a canonical compact form, and assert exact equality.
    local actual
    actual=$(printf '%s' "$output" | sed -E 's/^exclude_commands[[:space:]]*=[[:space:]]*//; s/[[:space:]]+$//')
    local expected='["cd", "pwd", "set", "export", "echo"]'
    [ "$actual" = "$expected" ]
}

@test "max_file_size is present in [tee] (rtk >= 0.38 requirement)" {
    run grep -E '^max_file_size' "$TEMPLATE"
    [ "$status" -eq 0 ]
    # 1 MB sensible default for tee-on-failure
    [[ "$output" == *"1048576"* ]]
}

@test "[tee] section present and enabled" {
    run grep -E '^\[tee\]' "$TEMPLATE"
    [ "$status" -eq 0 ]
    run grep -E '^enabled = true' "$TEMPLATE"
    [ "$status" -eq 0 ]
}

@test "intentionally invalid TOML triggers degraded mode in the probe" {
    # Document the rtk-mode.json schema as a fixture — runtime artifact,
    # never committed in the repo.
    if ! command -v rtk >/dev/null 2>&1; then
        skip "rtk not installed in this environment"
    fi
    # Drop a malformed TOML and verify rtk config exits non-zero (the probe
    # in session-init.sh keys off this).
    cat > "$XDG_CONFIG_HOME/rtk/config.toml" <<'EOF'
[hooks
exclude_commands = []
EOF
    run rtk config
    [ "$status" -ne 0 ]
}

# === rtk-mode.json schema (runtime artifact, documented here as fixture) ===
#
# Path: ~/.claude/logs/<branch>/rtk-mode.json
# Written by: session-init.sh probe_rtk_mode (always) + postStart.sh init_rtk
#             (on degraded-mode boot)
# Read by:    audit.md (top-level "RTK mode:" line)
#
# Schema (jq-compatible):
#   {
#     "mode":      "enforcing" | "advisory" | "degraded",
#     "reason":    "" | "session-bypass" | "hook-missing"
#                  | "no-binary" | "config-invalid" | "marker-missing",
#     "version":   "X.Y.Z" | "",
#     "timestamp": ISO 8601 UTC, e.g. "2026-05-06T18:35:48Z"
#   }
#
# This file is a runtime artifact. NOT committed to the repo. The schema is
# documented here so /audit and tests can rely on a stable shape.

@test "rtk-mode.json schema fixture is well-formed (sanity)" {
    # Build the canonical fixture and verify our jq filter accepts it.
    if ! command -v jq >/dev/null 2>&1; then
        skip "jq not installed"
    fi
    fixture='{"mode":"enforcing","reason":"","version":"0.38.0","timestamp":"2026-05-06T00:00:00Z"}'
    run jq -e '.mode and (.reason != null) and (.version != null) and .timestamp' <<<"$fixture"
    [ "$status" -eq 0 ]
}
