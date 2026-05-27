# Environment & Profile Detection

## Phase 1.0: Environment Detection (MANDATORY)

**MANDATORY: Detect execution context before any operation.**

```yaml
environment_detection:
  1_container_check:
    action: "Detect if running inside container"
    method: "[ -f /.dockerenv ]"
    output: "IS_CONTAINER (true|false)"

  2_devcontainer_check:
    action: "Check DEVCONTAINER env var"
    method: "[ -n \"${DEVCONTAINER:-}\" ]"
    note: "Set by VS Code when attached to devcontainer"

  3_determine_target:
    container_mode:
      target: "/workspace/.devcontainer/images/.claude"
      behavior: "Update template source (requires rebuild)"
      propagation: "Changes applied at next container start"

    host_mode:
      target: "$HOME/.claude"
      behavior: "Update user Claude configuration"
      propagation: "Immediate (no rebuild needed)"

  4_display_context:
    output: |
      Environment: {CONTAINER|HOST}
      Update target: {path}
      Mode: {template|user}
```

**Implementation:**

```bash
# Detect environment context
detect_context() {
    # Check if running inside container
    if [ -f /.dockerenv ]; then
        CONTEXT="container"
        UPDATE_TARGET="/workspace/.devcontainer/images/.claude"
        echo "Detected: Container environment"
    else
        CONTEXT="host"
        UPDATE_TARGET="$HOME/.claude"
        echo "Detected: Host machine"
    fi

    # Additional checks
    if [ -n "${DEVCONTAINER:-}" ]; then
        echo "  (DevContainer detected via DEVCONTAINER env var)"
    fi

    echo "Update target: $UPDATE_TARGET"
    echo "Mode: $CONTEXT"
}

# Call at start of update
detect_context
```

**Output Phase 1.0:**

```
═══════════════════════════════════════════════
  /update - Environment Detection
═══════════════════════════════════════════════

  Environment: HOST MACHINE
  Update target: /home/user/.claude
  Mode: user configuration

  Changes will be:
    - Applied immediately
    - No container rebuild needed
    - Synced to container via postStart.sh

═══════════════════════════════════════════════
```

Or in container:

```
═══════════════════════════════════════════════
  /update - Environment Detection
═══════════════════════════════════════════════

  Environment: DEVCONTAINER
  Update target: /workspace/.devcontainer/images/.claude
  Mode: template source

  Changes will be:
    - Applied to template files
    - Require container rebuild to propagate
    - Or wait for next postStart.sh sync

═══════════════════════════════════════════════
```

---

## Phase 1.5: Profile Detection

**MANDATORY: Auto-detect project profile to determine sync sources. No flags.**

```yaml
profile_detection:
  1_check_directories:
    action: "Check for infrastructure markers"
    checks:
      - "[ -d modules/ ]"
      - "[ -d stacks/ ]"
      - "[ -d ansible/ ]"
    result: "Any directory exists → INFRASTRUCTURE profile"

  2_determine_profile:
    infrastructure:
      condition: "modules/ OR stacks/ OR ansible/ exists"
      sources:
        - "kodflow/devcontainer-template (always)"
        - "kodflow/infrastructure-template (additional)"
      version_files:
        - ".devcontainer/.template-version"
        - ".infra-template-version"

    devcontainer:
      condition: "No infrastructure markers found"
      sources:
        - "kodflow/devcontainer-template (only)"
      version_files:
        - ".devcontainer/.template-version"
```

**Implementation:**

```bash
detect_profile() {
    PROFILE="devcontainer"
    INFRA_DIRS_FOUND=""

    for dir in modules stacks ansible; do
        if [ -d "$dir/" ]; then
            INFRA_DIRS_FOUND="${INFRA_DIRS_FOUND} $dir"
            PROFILE="infrastructure"
        fi
    done

    echo "Profile: $PROFILE"
    if [ "$PROFILE" = "infrastructure" ]; then
        echo "  Infrastructure dirs found:$INFRA_DIRS_FOUND"
        echo "  Sources: devcontainer-template + infrastructure-template"
    else
        echo "  Source: devcontainer-template only"
    fi
}
```

**Output Phase 1.5 (infrastructure detected):**

```
═══════════════════════════════════════════════
  /update - Profile Detection
═══════════════════════════════════════════════

  Profile: infrastructure
  Detected dirs: modules stacks ansible

  Sources:
    - kodflow/devcontainer-template (always)
    - kodflow/infrastructure-template

═══════════════════════════════════════════════
```

## Phase 2.5: Feature Staleness Scan

**Detect OCI feature drift so Phase 4.5 can auto-fix without prompting.**

The scan runs after the template tarball has been downloaded (Phase 3 /diff.md).
It compares each `ghcr.io/kodflow/devcontainer-features/*` ref pinned in
`devcontainer.json` / `devcontainer.local.json` against:

1. the upstream `devcontainer-feature.json` version shipped in the tarball;
2. the GHCR manifest digest currently served by the referenced tag.

When the upstream version is strictly greater than the pinned version, the
feature is marked **stale**. When the feature exists upstream but isn't
referenced downstream (or vice-versa), it is flagged for the apply phase to
surface in the final report.

```yaml
feature_staleness_scan:
  trigger: "Always — runs silently; emits no output when everything is fresh"

  1_repo_mode:
    action: "Detect whether we are in the template repo or a downstream consumer"
    script: "$HOME/.claude/scripts/update-repo-mode.sh"
    exports: "REPO_MODE ∈ {template, downstream}"

  2_scan:
    action: "Enumerate and classify referenced features"
    script: "$HOME/.claude/scripts/update-feature-scan.sh --template-root \"$TEMPLATE_ROOT\""
    output: |
      One line per feature:
        <ref>|<pinned_version>|<ghcr_digest>|<upstream_install_sha>|<state>
      state ∈ {fresh, stale, missing, unknown}
    exports: "STALE_FEATURES (newline-separated list of refs with state == stale)"

  3_read_mcp_skip_log:
    action: "Ingest /workspace/.claude/logs/mcp-skipped.json (written by postStart.sh) if present"
    note: |
      Drops recorded by postStart when a feature's trigger binary isn't on
      PATH — strong signal that either the feature's install.sh silently
      failed or the feature isn't bumped.
    exports: "MCP_DROPS (JSON array, possibly empty)"
```

**Implementation:**

```bash
detect_feature_staleness() {
    export REPO_MODE=$("$HOME/.claude/scripts/update-repo-mode.sh")
    STALE_FEATURES=""
    if [ -n "${TEMPLATE_ROOT:-}" ]; then
        while IFS='|' read -r ref _ver _digest _sha state; do
            [ "$state" = "stale" ] && STALE_FEATURES+="$ref"$'\n'
        done < <(UPDATE_TEMPLATE_ROOT="$TEMPLATE_ROOT" \
                 "$HOME/.claude/scripts/update-feature-scan.sh" 2>/dev/null || true)
        export STALE_FEATURES
    fi
    local mcp_log="${WORKSPACE_FOLDER:-/workspace}/.claude/logs/mcp-skipped.json"
    if [ -f "$mcp_log" ]; then
        export MCP_DROPS=$(cat "$mcp_log")
    else
        export MCP_DROPS="[]"
    fi
}
```

No user-facing output at this phase — results feed Phase 4.5 (apply.md).
