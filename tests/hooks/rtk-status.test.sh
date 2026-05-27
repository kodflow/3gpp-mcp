#!/bin/bash
# ============================================================================
# rtk-status.test.sh - Tests for probe_rtk_mode in session-init.sh
# Layer 1 of plan 2026-05-06-rtk-mandatory-install-and-claude-memory.
#
# Covers each row of the canonical modes table:
#   enforcing  binary + RTK.md + @RTK.md + hook + no bypass + config valid
#   advisory   reason=session-bypass    (RTK_BYPASS=1)
#   advisory   reason=hook-missing      (settings.json absent or no entry)
#   degraded   reason=no-binary         (rtk binary missing)
#   degraded   reason=config-invalid    (TOML parse error)
#   degraded   reason=marker-missing    (RTK.md missing or @RTK.md import absent)
#
# Plus: bootstrap (CLAUDE_HOOKS_BOOTSTRAP=1) suppresses the line entirely.
# ============================================================================

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "$SCRIPT_DIR/test-utils.sh"

# Use the in-tree source script (not the deployed copy) so this validates
# the change before image rebuild + container restart, mirroring the pattern
# used by tests/hooks/lint.test.sh §"golangci-lint config gate (issue #342)".
SCRIPT="${BATS_TEST_DIRNAME:-$SCRIPT_DIR}/../../.devcontainer/images/.claude/scripts/session-init.sh"
[ -f "$SCRIPT" ] || SCRIPT=/workspace/.devcontainer/images/.claude/scripts/session-init.sh

echo "Testing: session-init.sh probe_rtk_mode"
echo "───────────────────────────────────────────────"

# Sandbox HOME so probe_rtk_mode reads our fixtures, not the real ~/.claude.
SANDBOX=$(mktemp -d)
trap 'rm -rf "$SANDBOX"' EXIT

mkdir -p "$SANDBOX/.claude" "$SANDBOX/.config/rtk"

# Helper: run probe with given env vars (KEY=VAL form), echo just the [rtk] line(s).
# Uses `env -i` with an explicit minimal PATH so PATH-based no-binary tests work
# without breaking grep/head/sed lookups.
run_probe() {
    local home="$1"
    shift
    env -i \
        HOME="$home" \
        CLAUDE_PROJECT_DIR="$home" \
        CLAUDE_ENV_FILE=/tmp/test-env-rtk-$$ \
        PATH="/usr/local/bin:/usr/bin:/bin" \
        "$@" \
        bash "$SCRIPT" </dev/null 2>&1 \
        | grep -E '^\[rtk\]' | head -1
}

# Helper: same as run_probe but with rtk explicitly NOT on PATH.
run_probe_no_rtk() {
    local home="$1"
    shift
    # /usr/local/bin (where rtk lives) is intentionally absent here.
    env -i \
        HOME="$home" \
        CLAUDE_PROJECT_DIR="$home" \
        CLAUDE_ENV_FILE=/tmp/test-env-rtk-$$ \
        PATH="/usr/bin:/bin" \
        "$@" \
        bash "$SCRIPT" </dev/null 2>&1 \
        | grep -E '^\[rtk\]' | head -1
}

# Helper: build a minimal "good" sandbox (binary present + RTK.md + @RTK.md + hook + valid config)
setup_good_sandbox() {
    local home="$1"
    rm -rf "$home/.claude" "$home/.config"
    mkdir -p "$home/.claude" "$home/.config/rtk"
    # RTK.md present
    echo "# RTK" > "$home/.claude/RTK.md"
    # @RTK.md import present
    echo "@RTK.md" > "$home/.claude/CLAUDE.md"
    # settings.json with the canonical hook entry
    cat > "$home/.claude/settings.json" <<'EOF'
{"hooks": {"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "rtk hook claude"}]}]}}
EOF
    # valid rtk config (mirrors the new template; required for rtk config exit 0)
    # Resolve from $SCRIPT_DIR rather than a hardcoded /workspace path so the
    # test runs the same way under bats/CI and outside a devcontainer.
    local repo_rtk_toml="$SCRIPT_DIR/../../.devcontainer/images/rtk.config.toml"
    cp "$repo_rtk_toml" "$home/.config/rtk/config.toml"
}

# === ENFORCING (happy path, legacy "rtk hook claude" hook entry) ===
setup_good_sandbox "$SANDBOX"
TESTS_RUN=$((TESTS_RUN + 1))
out=$(run_probe "$SANDBOX")
if [[ "$out" == "[rtk] mode=enforcing version="* ]]; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
    printf "${GREEN}  PASS${NC}: enforcing mode (binary+marker+hook+config+no bypass) — legacy hook form\n"
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    printf "${RED}  FAIL${NC}: enforcing mode → got: %s\n" "$out"
fi

# === ENFORCING (wrapper hook entry — issue #348) ===
# Same conditions as above but settings.json invokes the fail-open wrapper
# rather than `rtk hook claude` directly. The probe must accept BOTH forms.
setup_good_sandbox "$SANDBOX"
cat > "$SANDBOX/.claude/settings.json" <<'EOF'
{"hooks": {"PreToolUse": [{"matcher": "Bash", "hooks": [{"type": "command", "command": "/home/vscode/.claude/scripts/rtk-hook-claude.sh"}]}]}}
EOF
TESTS_RUN=$((TESTS_RUN + 1))
out=$(run_probe "$SANDBOX")
if [[ "$out" == "[rtk] mode=enforcing version="* ]]; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
    printf "${GREEN}  PASS${NC}: enforcing mode — wrapper hook form (issue #348)\n"
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    printf "${RED}  FAIL${NC}: enforcing mode (wrapper form) → got: %s\n" "$out"
fi

# === ADVISORY: session-bypass ===
setup_good_sandbox "$SANDBOX"
TESTS_RUN=$((TESTS_RUN + 1))
out=$(run_probe "$SANDBOX" RTK_BYPASS=1)
if [ "$out" = "[rtk] mode=advisory reason=session-bypass" ]; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
    printf "${GREEN}  PASS${NC}: advisory reason=session-bypass (RTK_BYPASS=1)\n"
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    printf "${RED}  FAIL${NC}: advisory session-bypass → got: %s\n" "$out"
fi

# === ADVISORY: hook-missing (settings.json absent) ===
setup_good_sandbox "$SANDBOX"
rm -f "$SANDBOX/.claude/settings.json"
TESTS_RUN=$((TESTS_RUN + 1))
out=$(run_probe "$SANDBOX")
if [ "$out" = "[rtk] mode=advisory reason=hook-missing" ]; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
    printf "${GREEN}  PASS${NC}: advisory reason=hook-missing (settings.json absent)\n"
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    printf "${RED}  FAIL${NC}: advisory hook-missing → got: %s\n" "$out"
fi

# === ADVISORY: hook-missing (settings.json present but no rtk entry) ===
setup_good_sandbox "$SANDBOX"
echo '{}' > "$SANDBOX/.claude/settings.json"
TESTS_RUN=$((TESTS_RUN + 1))
out=$(run_probe "$SANDBOX")
if [ "$out" = "[rtk] mode=advisory reason=hook-missing" ]; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
    printf "${GREEN}  PASS${NC}: advisory reason=hook-missing (no rtk entry in settings.json)\n"
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    printf "${RED}  FAIL${NC}: advisory hook-missing (no entry) → got: %s\n" "$out"
fi

# === DEGRADED: marker-missing (RTK.md absent) ===
setup_good_sandbox "$SANDBOX"
rm -f "$SANDBOX/.claude/RTK.md"
TESTS_RUN=$((TESTS_RUN + 1))
out=$(run_probe "$SANDBOX")
if [ "$out" = "[rtk] mode=degraded reason=marker-missing" ]; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
    printf "${GREEN}  PASS${NC}: degraded reason=marker-missing (no RTK.md)\n"
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    printf "${RED}  FAIL${NC}: degraded marker-missing (RTK.md) → got: %s\n" "$out"
fi

# === DEGRADED: marker-missing (@RTK.md import absent from CLAUDE.md) ===
setup_good_sandbox "$SANDBOX"
echo "# Just custom rules, no @RTK.md" > "$SANDBOX/.claude/CLAUDE.md"
TESTS_RUN=$((TESTS_RUN + 1))
out=$(run_probe "$SANDBOX")
if [ "$out" = "[rtk] mode=degraded reason=marker-missing" ]; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
    printf "${GREEN}  PASS${NC}: degraded reason=marker-missing (@RTK.md not imported)\n"
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    printf "${RED}  FAIL${NC}: degraded marker-missing (@import) → got: %s\n" "$out"
fi

# === DEGRADED: config-invalid (TOML parse error) ===
# We can't easily strip rtk from PATH inside the test, but we CAN install a
# malformed config. The probe runs `rtk config` which will exit non-zero.
setup_good_sandbox "$SANDBOX"
cat > "$SANDBOX/.config/rtk/config.toml" <<'EOF'
[hooks
exclude_commands = []
EOF
TESTS_RUN=$((TESTS_RUN + 1))
# rtk reads from $XDG_CONFIG_HOME/rtk/config.toml first, fallback to $HOME/.config/rtk/
out=$(XDG_CONFIG_HOME="$SANDBOX/.config" run_probe "$SANDBOX")
if [ "$out" = "[rtk] mode=degraded reason=config-invalid" ]; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
    printf "${GREEN}  PASS${NC}: degraded reason=config-invalid (malformed TOML)\n"
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    printf "${RED}  FAIL${NC}: degraded config-invalid → got: %s\n" "$out"
fi

# === DEGRADED: no-binary ===
# Strip rtk from PATH; probe must report no-binary.
setup_good_sandbox "$SANDBOX"
TESTS_RUN=$((TESTS_RUN + 1))
out=$(run_probe_no_rtk "$SANDBOX")
if [ "$out" = "[rtk] mode=degraded reason=no-binary" ]; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
    printf "${GREEN}  PASS${NC}: degraded reason=no-binary (rtk not on PATH)\n"
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    printf "${RED}  FAIL${NC}: degraded no-binary → got: %s\n" "$out"
fi

# === BOOTSTRAP suppression: CLAUDE_HOOKS_BOOTSTRAP=1 → no [rtk] line emitted ===
setup_good_sandbox "$SANDBOX"
TESTS_RUN=$((TESTS_RUN + 1))
out=$(run_probe "$SANDBOX" CLAUDE_HOOKS_BOOTSTRAP=1)
if [ -z "$out" ]; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
    printf "${GREEN}  PASS${NC}: bootstrap suppression (no line during postStart)\n"
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    printf "${RED}  FAIL${NC}: bootstrap not suppressed → got: %s\n" "$out"
fi

# === Bypass beats hook-missing (precedence sanity) ===
# RTK_BYPASS=1 must take priority over hook-missing — bypass and degradation
# are first-class signals and must not be conflated even when a degradation
# condition is present alongside.
setup_good_sandbox "$SANDBOX"
rm -f "$SANDBOX/.claude/settings.json"
TESTS_RUN=$((TESTS_RUN + 1))
out=$(run_probe "$SANDBOX" RTK_BYPASS=1)
# Per the canonical modes table: degraded reasons (marker-missing, no-binary,
# config-invalid) outrank bypass; advisory reasons (hook-missing) are below.
# So with hook-missing + RTK_BYPASS=1 → bypass wins (advisory session-bypass).
# But with marker-missing + RTK_BYPASS=1 → degraded wins (precedence by severity).
# This test pins the hook-missing × bypass case (advisory wins).
if [ "$out" = "[rtk] mode=advisory reason=session-bypass" ]; then
    TESTS_PASSED=$((TESTS_PASSED + 1))
    printf "${GREEN}  PASS${NC}: bypass × hook-missing → bypass wins (advisory session-bypass)\n"
else
    TESTS_FAILED=$((TESTS_FAILED + 1))
    printf "${RED}  FAIL${NC}: bypass precedence → got: %s\n" "$out"
fi

print_summary "session-init.sh probe_rtk_mode"
exit $?
