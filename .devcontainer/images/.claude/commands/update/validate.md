# Post-Update Validation

## Phase 6.0: Hook Synchronization

**Goal:** Synchronize hooks from `~/.claude/settings.json` with the template.

**Problem solved:** Users with an older `settings.json` may have
references to obsolete scripts (bash-validate.sh, phase-validate.sh, etc.)
because `postStart.sh` only copies `settings.json` if it does not exist.

```yaml
hook_sync_workflow:
  1_backup:
    action: "Backup user settings.json"
    command: "cp ~/.claude/settings.json ~/.claude/settings.json.backup"

  2_merge_hooks:
    action: "Replace the hooks section with the template"
    strategy: "REPLACE (not merge) - the template is the source of truth"
    tool: jq
    preserves:
      - permissions
      - model
      - env
      - statusLine
      - disabledMcpjsonServers

  3_restore_on_failure:
    action: "Restore backup if merge fails"
```

**Implementation:**

```bash
sync_user_hooks() {
    local user_settings="$HOME/.claude/settings.json"
    local template_settings=".devcontainer/images/.claude/settings.json"

    if [ ! -f "$user_settings" ]; then
        echo "  ⚠ No user settings.json, skipping hook sync"
        return 0
    fi

    if [ ! -f "$template_settings" ]; then
        echo "  ✗ Template settings.json not found"
        return 1
    fi

    echo "  Synchronizing user hooks with template..."

    # Backup
    cp "$user_settings" "${user_settings}.backup"

    # Replace hooks section only (preserve all other settings)
    if jq --slurpfile tpl "$template_settings" '.hooks = $tpl[0].hooks' \
       "$user_settings" > "${user_settings}.tmp"; then

        # Validate JSON
        if jq empty "${user_settings}.tmp" 2>/dev/null; then
            mv "${user_settings}.tmp" "$user_settings"
            rm -f "${user_settings}.backup"
            echo "  ✓ User hooks synchronized with template"
            return 0
        else
            mv "${user_settings}.backup" "$user_settings"
            rm -f "${user_settings}.tmp"
            echo "  ✗ Hook merge produced invalid JSON, restored backup"
            return 1
        fi
    else
        mv "${user_settings}.backup" "$user_settings"
        echo "  ✗ Hook merge failed, restored backup"
        return 1
    fi
}
```

---

## Phase 7.0: Script Validation

**Goal:** Validate that all scripts referenced in hooks exist.

```yaml
validate_workflow:
  1_extract:
    action: "Extract all script paths from hooks"
    tool: jq
    pattern: ".hooks | .. | .command? // empty"

  2_verify:
    action: "Verify that each script exists"
    for_each: script_path
    check: "[ -f $script_path ]"

  3_report:
    on_missing: "List missing scripts with fix suggestion"
    on_success: "All scripts validated"
```

**Implementation:**

```bash
validate_hook_scripts() {
    local settings_file="$HOME/.claude/settings.json"
    local scripts_dir="$HOME/.claude/scripts"
    local missing_count=0

    if [ ! -f "$settings_file" ]; then
        echo "  ⚠ No settings.json to validate"
        return 0
    fi

    # Extract all script paths from hooks
    local scripts
    scripts=$(jq -r '.hooks | .. | .command? // empty' "$settings_file" 2>/dev/null \
        | grep -oE '/home/vscode/.claude/scripts/[^ "]+' \
        | sed 's/ .*//' \
        | sort -u)

    if [ -z "$scripts" ]; then
        echo "  ⚠ No hook scripts found in settings.json"
        return 0
    fi

    echo "  Validating hook scripts..."

    # Use while read for zsh compatibility (for x in $VAR breaks in zsh)
    echo "$scripts" | while IFS= read -r script_path; do
        [ -z "$script_path" ] && continue
        local script_name=$(basename "$script_path")

        if [ -f "$script_path" ]; then
            echo "    ✓ $script_name"
        else
            echo "    ✗ $script_name (MISSING)"
            missing_count=$((missing_count + 1))
        fi
    done

    if [ $missing_count -gt 0 ]; then
        echo ""
        echo "  ⚠ $missing_count missing script(s) detected!"
        echo "  → Run: /update --component hooks"
        return 1
    fi

    echo "  ✓ All hook scripts validated"
    return 0
}
```

---

## Guardrails (ABSOLUTE)

| Action | Status | Reason |
|--------|--------|--------|
| Per-file curl instead of tarball | **FORBIDDEN** | Use git tarball (1 API call) |
| Add CLI flags for profile | **FORBIDDEN** | Auto-detect only, no flags |
| Overwrite protected paths | **FORBIDDEN** | inventory/, terragrunt.hcl, .env*, CLAUDE.md, etc. |
| Write without validation | **FORBIDDEN** | Corruption risk |
| Non-official source | **FORBIDDEN** | Security |
| Hook sync without backup | **FORBIDDEN** | Always backup first |
| Delete user settings | **FORBIDDEN** | Only merge hooks |
| Skip script validation | **FORBIDDEN** | Error detection MANDATORY |
| Skip profile detection | **FORBIDDEN** | Must auto-detect before sync |
| `for x in $VAR` pattern | **FORBIDDEN** | Breaks in zsh ($SHELL=zsh) |
| Inline execution without bash | **FORBIDDEN** | Always `bash /tmp/script.sh` |

---

## Affected files

**Updated by /update (devcontainer - always):**
```
.devcontainer/
├── docker-compose.yml            # Update devcontainer service
├── hooks/
│   ├── lifecycle/*.sh            # Delegation stubs
│   └── shared/utils.sh          # Shared utilities (host)
├── images/
│   ├── .p10k.zsh
│   ├── hooks/                    # Image-embedded hooks (real logic)
│   │   ├── shared/utils.sh
│   │   └── lifecycle/*.sh
│   └── .claude/
│       ├── agents/*.md
│       ├── commands/*.md
│       ├── scripts/*.sh
│       └── settings.json
└── .template-version
```

**Updated by /update (infrastructure - if profile detected):**
```
modules/                          # Terraform modules
stacks/                           # Terragrunt stacks
ansible/                          # Roles and playbooks
packer/                           # Machine images
ci/                               # CI/CD pipelines
tests/                            # Terratest + Molecule
.infra-template-version           # Infrastructure version
```

**Protected paths (NEVER overwritten if they exist):**
```
inventory/                        # Ansible inventory (project-specific)
terragrunt.hcl                    # Root terragrunt config
.env*                             # Environment files
CLAUDE.md                         # Project documentation
AGENTS.md                         # Agent configuration
README.md                         # Project readme
Makefile                          # Build configuration
docs/                             # Documentation
```

**Also updated (container only):**
```
.devcontainer/
├── devcontainer.json      # Feature refs (GHCR)
├── Dockerfile             # Image FROM reference
└── images/
    ├── mcp.json.tpl       # MCP server template
    └── mcp-fragments/     # MCP server fragments
```

---

## Complete script (reference)

**IMPORTANT: This script uses `#!/bin/bash`. Always write to a temp file and execute with `bash`:**
```bash
cat > /tmp/update-devcontainer.sh << 'SCRIPT'
# ... (script below) ...
SCRIPT
bash /tmp/update-devcontainer.sh && rm -f /tmp/update-devcontainer.sh
```

```bash
#!/bin/bash
# /update implementation - Git Tarball + Profile-Aware Sync
# Downloads full tarballs (1 API call per source) instead of per-file curl.
# Auto-detects infrastructure profile (modules/, stacks/, ansible/).
# NOTE: Must be executed with bash (not zsh) due to word splitting in for loops.

set -uo pipefail
set +H 2>/dev/null || true  # Disable bash history expansion

# Configuration
DEVCONTAINER_REPO="kodflow/devcontainer-template"
DEVCONTAINER_BRANCH="main"
INFRA_REPO="kodflow/infrastructure-template"
INFRA_BRANCH="main"
INFRA_COMPONENTS="modules stacks ansible packer ci tests"
PROTECTED_PATHS="inventory/ terragrunt.hcl .env CLAUDE.md AGENTS.md README.md Makefile docs/"

# ═══ Phase 1.0: Environment Detection ═══
detect_context() {
    if [ -f /.dockerenv ]; then
        CONTEXT="container"
        UPDATE_TARGET="/workspace/.devcontainer/images/.claude"
        echo "  Environment: Container"
    else
        CONTEXT="host"
        UPDATE_TARGET="$HOME/.claude"
        echo "  Environment: Host machine"
    fi
    [ -n "${DEVCONTAINER:-}" ] && echo "  (DevContainer env var detected)"
    echo "  Target: $UPDATE_TARGET"
}

# ═══ Phase 1.5: Profile Detection ═══
detect_profile() {
    PROFILE="devcontainer"
    INFRA_DIRS_FOUND=""
    for dir in modules stacks ansible; do
        if [ -d "$dir/" ]; then
            INFRA_DIRS_FOUND="${INFRA_DIRS_FOUND} $dir"
            PROFILE="infrastructure"
        fi
    done
    echo "  Profile: $PROFILE"
    if [ "$PROFILE" = "infrastructure" ]; then
        echo "  Infrastructure dirs:$INFRA_DIRS_FOUND"
        echo "  Sources: devcontainer-template + infrastructure-template"
    else
        echo "  Source: devcontainer-template only"
    fi
}

# ═══ Download & Extract Tarball (1 API call) ═══
download_tarball() {
    local tarball_url="$1"
    local label="$2"
    local tmp_dir=$(mktemp -d)
    local tmp_tar="${tmp_dir}/template.tar.gz"

    echo "  Downloading $label tarball..."
    local http_code
    http_code=$(curl -sL -w "%{http_code}" -o "$tmp_tar" "$tarball_url")

    if [ "$http_code" != "200" ]; then
        echo "  ✗ $label download failed (HTTP $http_code)"
        rm -rf "$tmp_dir"
        return 1
    fi

    if [ ! -s "$tmp_tar" ]; then
        echo "  ✗ $label tarball is empty"
        rm -rf "$tmp_dir"
        return 1
    fi

    if ! tar xzf "$tmp_tar" --strip-components=1 -C "$tmp_dir"; then
        echo "  ✗ $label extraction failed"
        rm -rf "$tmp_dir"
        return 1
    fi
    rm -f "$tmp_tar"

    EXTRACT_DIR="$tmp_dir"

    echo "  ✓ $label downloaded and extracted"
    return 0
}

# ═══ Protected path check (for infra sync) ═══
is_protected() {
    local file_path="$1"
    for protected in $PROTECTED_PATHS; do
        case "$file_path" in
            "$protected"*) return 0 ;;
            */"$protected") return 0 ;;
        esac
        local bn
        bn=$(basename "$file_path")
        if [ "$bn" = "$protected" ]; then
            return 0
        fi
    done
    return 1
}

# ═══ Safe glob copy (avoids set -e failures on empty globs) ═══
safe_glob_copy() {
    local pattern="$1" dest="$2" make_exec="${3:-}"
    local dir=$(dirname "$pattern")
    local glob=$(basename "$pattern")
    while IFS= read -r -d '' f; do
        cp -f "$f" "$dest/"
        [ "$make_exec" = "+x" ] && chmod +x "$dest/$(basename "$f")"
    done < <(find "$dir" -maxdepth 1 -name "$glob" -type f -print0 2>/dev/null)
    return 0
}

# ═══ Apply devcontainer components from tarball ═══
apply_devcontainer_tarball() {
    local src="$DEVCONTAINER_EXTRACT_DIR"

    # Scripts (hooks)
    if [ -d "$src/.devcontainer/images/.claude/scripts" ]; then
        mkdir -p "$UPDATE_TARGET/scripts"
        safe_glob_copy "$src/.devcontainer/images/.claude/scripts/*.sh" "$UPDATE_TARGET/scripts" "+x"
        echo "  ✓ hooks"
    fi

    # Commands (top-level)
    if [ -d "$src/.devcontainer/images/.claude/commands" ]; then
        mkdir -p "$UPDATE_TARGET/commands"
        safe_glob_copy "$src/.devcontainer/images/.claude/commands/*.md" "$UPDATE_TARGET/commands"
        echo "  ✓ commands"
    fi

    # Command sub-modules (subdirectories)
    if [ -d "$src/.devcontainer/images/.claude/commands" ]; then
        while IFS= read -r -d '' subdir; do
            local rel="${subdir#$src/.devcontainer/images/.claude/}"
            mkdir -p "$UPDATE_TARGET/$rel"
            safe_glob_copy "$subdir/*.md" "$UPDATE_TARGET/$rel"
        done < <(find "$src/.devcontainer/images/.claude/commands" -mindepth 1 -type d -print0 2>/dev/null)
        echo "  ✓ command sub-modules"
    fi

    # Agents
    if [ -d "$src/.devcontainer/images/.claude/agents" ]; then
        mkdir -p "$UPDATE_TARGET/agents"
        safe_glob_copy "$src/.devcontainer/images/.claude/agents/*.md" "$UPDATE_TARGET/agents"
        echo "  ✓ agents"
    fi

    # Lifecycle stubs (container only)
    if [ "$CONTEXT" = "container" ] && [ -d "$src/.devcontainer/hooks/lifecycle" ]; then
        mkdir -p ".devcontainer/hooks/lifecycle"
        safe_glob_copy "$src/.devcontainer/hooks/lifecycle/*.sh" ".devcontainer/hooks/lifecycle" "+x"
        echo "  ✓ lifecycle"
    fi

    # Image-embedded hooks (container only)
    if [ "$CONTEXT" = "container" ] && [ -d "$src/.devcontainer/images/hooks" ]; then
        mkdir -p ".devcontainer/images/hooks/shared" ".devcontainer/images/hooks/lifecycle"
        [ -f "$src/.devcontainer/images/hooks/shared/utils.sh" ] && \
            cp -f "$src/.devcontainer/images/hooks/shared/utils.sh" ".devcontainer/images/hooks/shared/utils.sh" && \
            chmod +x ".devcontainer/images/hooks/shared/utils.sh"
        safe_glob_copy "$src/.devcontainer/images/hooks/lifecycle/*.sh" ".devcontainer/images/hooks/lifecycle" "+x"
        echo "  ✓ image-hooks"
    fi

    # Shared utils (container only)
    if [ "$CONTEXT" = "container" ] && [ -f "$src/.devcontainer/hooks/shared/utils.sh" ]; then
        cp -f "$src/.devcontainer/hooks/shared/utils.sh" ".devcontainer/hooks/shared/utils.sh"
        echo "  ✓ shared-utils"
    fi

    # p10k (container only)
    if [ "$CONTEXT" = "container" ] && [ -f "$src/.devcontainer/images/.p10k.zsh" ]; then
        cp -f "$src/.devcontainer/images/.p10k.zsh" ".devcontainer/images/.p10k.zsh"
        echo "  ✓ p10k"
    fi

    # settings.json
    if [ -f "$src/.devcontainer/images/.claude/settings.json" ]; then
        cp -f "$src/.devcontainer/images/.claude/settings.json" "$UPDATE_TARGET/settings.json"
        echo "  ✓ settings"
    fi

    # MCP template (container only)
    if [ "$CONTEXT" = "container" ] && [ -f "$src/.devcontainer/images/mcp.json.tpl" ]; then
        mkdir -p ".devcontainer/images"
        cp -f "$src/.devcontainer/images/mcp.json.tpl" ".devcontainer/images/mcp.json.tpl"
        echo "  ✓ mcp-template"
    fi

    # MCP fragments (container only)
    if [ "$CONTEXT" = "container" ] && [ -d "$src/.devcontainer/images/mcp-fragments" ]; then
        mkdir -p ".devcontainer/images/mcp-fragments"
        safe_glob_copy "$src/.devcontainer/images/mcp-fragments/*.json" ".devcontainer/images/mcp-fragments"
        echo "  ✓ mcp-fragments"
    fi

    # Design patterns docs
    if [ -d "$src/.devcontainer/images/.claude/docs" ]; then
        mkdir -p "$UPDATE_TARGET/docs"
        cp -rf "$src/.devcontainer/images/.claude/docs/"* "$UPDATE_TARGET/docs/"
        echo "  ✓ docs"
    fi

    # Templates
    if [ -d "$src/.devcontainer/images/.claude/templates" ]; then
        mkdir -p "$UPDATE_TARGET/templates"
        cp -rf "$src/.devcontainer/images/.claude/templates/"* "$UPDATE_TARGET/templates/"
        echo "  ✓ templates"
    fi

    # devcontainer.json (container only)
    if [ "$CONTEXT" = "container" ] && [ -f "$src/.devcontainer/devcontainer.json" ]; then
        cp -f "$src/.devcontainer/devcontainer.json" ".devcontainer/devcontainer.json"
        echo "  ✓ devcontainer.json"
    fi

    # Dockerfile (container only)
    if [ "$CONTEXT" = "container" ] && [ -f "$src/.devcontainer/Dockerfile" ]; then
        cp -f "$src/.devcontainer/Dockerfile" ".devcontainer/Dockerfile"
        echo "  ✓ Dockerfile"
    fi

    # docker-compose.yml (container only)
    if [ "$CONTEXT" = "container" ]; then
        update_compose_from_tarball "$src"
    fi
}

# ═══ Compose merge from tarball ═══
update_compose_from_tarball() {
    local src="$1"
    local compose_file=".devcontainer/docker-compose.yml"
    local template_compose="$src/.devcontainer/docker-compose.yml"

    if [ ! -f "$template_compose" ]; then
        return 0
    fi

    if [ ! -f "$compose_file" ]; then
        cp "$template_compose" "$compose_file"
        echo "  ✓ compose (created from template)"
        return 0
    fi

    local temp_services=$(mktemp --suffix=.yaml)
    local temp_volumes=$(mktemp --suffix=.yaml)
    local temp_networks=$(mktemp --suffix=.yaml)
    local backup_file="${compose_file}.backup"
    cp "$compose_file" "$backup_file"

    # Extract custom services, volumes, and networks
    yq '.services | to_entries | map(select(.key != "devcontainer")) | from_entries' \
        "$compose_file" > "$temp_services"
    yq '.volumes // {}' "$compose_file" > "$temp_volumes"
    yq '.networks // {}' "$compose_file" > "$temp_networks"

    cp "$template_compose" "$compose_file"

    # Merge back custom services
    if [ -s "$temp_services" ] && [ "$(yq '. | length' "$temp_services")" != "0" ]; then
        yq -i ".services *= load(\"$temp_services\")" "$compose_file"
    fi

    # Merge back custom volumes
    if [ -s "$temp_volumes" ] && [ "$(yq '. | length' "$temp_volumes")" != "0" ]; then
        yq -i ".volumes *= load(\"$temp_volumes\")" "$compose_file"
    fi

    # Merge back custom networks
    if [ -s "$temp_networks" ] && [ "$(yq '. | length' "$temp_networks")" != "0" ]; then
        yq -i ".networks *= load(\"$temp_networks\")" "$compose_file"
    fi

    rm -f "$temp_services" "$temp_volumes" "$temp_networks"

    if [ -s "$compose_file" ] && yq '.services.devcontainer' "$compose_file" > /dev/null 2>&1; then
        rm -f "$backup_file"
        echo "  ✓ compose (devcontainer replaced, custom preserved)"
    else
        mv "$backup_file" "$compose_file"
        echo "  ✗ compose validation failed, restored backup"
        return 1
    fi
}

# ═══ Apply infrastructure components with protected paths ═══
apply_infra_tarball() {
    if [ "$PROFILE" != "infrastructure" ]; then
        return 0
    fi

    local src="$INFRA_EXTRACT_DIR"
    local synced=0
    local skipped=0

    echo "  Infrastructure components:"
    for component in $INFRA_COMPONENTS; do
        [ ! -d "$src/$component" ] && continue

        local comp_count=0
        while IFS= read -r -d '' src_file; do
            local rel_path="${src_file#$src/}"

            # Always skip protected paths (prevents restoring deleted files)
            if is_protected "$rel_path"; then
                skipped=$((skipped + 1))
                continue
            fi

            mkdir -p "$(dirname "$rel_path")"
            cp -f "$src_file" "$rel_path"
            synced=$((synced + 1))
            comp_count=$((comp_count + 1))

            case "$rel_path" in
                *.sh) chmod +x "$rel_path" ;;
            esac
        done < <(find "$src/$component" -type f -print0 2>/dev/null)

        echo "    ✓ $component/ ($comp_count files)"
    done

    echo "  Synced: $synced files, Protected: $skipped skipped"
}

# ═══ Hook synchronization (Phase 6.0) ═══
sync_user_hooks() {
    local user_settings="$HOME/.claude/settings.json"
    local template_settings=".devcontainer/images/.claude/settings.json"

    if [ ! -f "$user_settings" ]; then
        echo "  ⚠ No user settings.json, skipping hook sync"
        return 0
    fi

    if [ ! -f "$template_settings" ]; then
        echo "  ✗ Template settings.json not found"
        return 1
    fi

    echo "  Synchronizing user hooks with template..."
    cp "$user_settings" "${user_settings}.backup"

    if jq --slurpfile tpl "$template_settings" '.hooks = $tpl[0].hooks' \
       "$user_settings" > "${user_settings}.tmp"; then
        if jq empty "${user_settings}.tmp" 2>/dev/null; then
            mv "${user_settings}.tmp" "$user_settings"
            rm -f "${user_settings}.backup"
            echo "  ✓ User hooks synchronized"
            return 0
        else
            mv "${user_settings}.backup" "$user_settings"
            rm -f "${user_settings}.tmp"
            echo "  ✗ Invalid JSON, restored backup"
            return 1
        fi
    else
        mv "${user_settings}.backup" "$user_settings"
        echo "  ✗ Hook merge failed, restored backup"
        return 1
    fi
}

# ═══ Script validation (Phase 7.0) ═══
validate_hook_scripts() {
    local settings_file="$HOME/.claude/settings.json"
    local missing_count=0

    if [ ! -f "$settings_file" ]; then
        echo "  ⚠ No settings.json to validate"
        return 0
    fi

    local scripts
    scripts=$(jq -r '.hooks | .. | .command? // empty' "$settings_file" 2>/dev/null \
        | grep -oE '/home/vscode/.claude/scripts/[^ "]+' \
        | sed 's/ .*//' | sort -u)

    if [ -z "$scripts" ]; then
        echo "  ⚠ No hook scripts found"
        return 0
    fi

    echo "  Validating hook scripts..."
    while IFS= read -r script_path; do
        [ -z "$script_path" ] && continue
        local script_name=$(basename "$script_path")
        if [ -f "$script_path" ]; then
            echo "    ✓ $script_name"
        else
            echo "    ✗ $script_name (MISSING)"
            missing_count=$((missing_count + 1))
        fi
    done <<< "$scripts"

    if [ $missing_count -gt 0 ]; then
        echo "  ⚠ $missing_count missing script(s)!"
        return 1
    fi

    echo "  ✓ All scripts validated"
    return 0
}

# ═══════════════════════════════════════════════
#   MAIN EXECUTION
# ═══════════════════════════════════════════════

# Cleanup temp directories on exit (normal or error)
CLEANUP_DIRS=""
cleanup() {
    for d in $CLEANUP_DIRS; do
        rm -rf "$d" 2>/dev/null
    done
}
trap cleanup EXIT

echo "═══════════════════════════════════════════════"
echo "  /update - DevContainer Environment Update"
echo "═══════════════════════════════════════════════"
echo ""

# Phase 1.0: Environment Detection
echo "Phase 1.0: Environment Detection"
detect_context
echo ""

# Phase 1.5: Profile Detection
echo "Phase 1.5: Profile Detection"
detect_profile
echo ""

# Phase 3.0: Download tarballs
echo "Phase 3.0: Download (git tarball)"
DEVCONTAINER_TARBALL="https://api.github.com/repos/$DEVCONTAINER_REPO/tarball/$DEVCONTAINER_BRANCH"
download_tarball "$DEVCONTAINER_TARBALL" "devcontainer-template"
DEVCONTAINER_EXTRACT_DIR="$EXTRACT_DIR"
CLEANUP_DIRS="$DEVCONTAINER_EXTRACT_DIR"

if [ "$PROFILE" = "infrastructure" ]; then
    INFRA_TARBALL="https://api.github.com/repos/$INFRA_REPO/tarball/$INFRA_BRANCH"
    download_tarball "$INFRA_TARBALL" "infrastructure-template"
    INFRA_EXTRACT_DIR="$EXTRACT_DIR"
    CLEANUP_DIRS="$CLEANUP_DIRS $INFRA_EXTRACT_DIR"
fi
echo ""

# Phase 4.0: Extract & Apply
echo "Phase 4.0: Apply devcontainer components"
apply_devcontainer_tarball

# Migration: old full hooks to delegation stubs
if [ "$CONTEXT" = "container" ]; then
    for hook in onCreate postCreate postStart postAttach updateContent; do
        hook_file=".devcontainer/hooks/lifecycle/${hook}.sh"
        if [ -f "$hook_file" ] && ! grep -q "Delegation stub" "$hook_file"; then
            src_stub="$DEVCONTAINER_EXTRACT_DIR/.devcontainer/hooks/lifecycle/${hook}.sh"
            if [ -f "$src_stub" ]; then
                cp -f "$src_stub" "$hook_file"
                chmod +x "$hook_file"
                echo "  Migrated ${hook}.sh to delegation stub"
            fi
        fi
    done
fi

# Infrastructure components
if [ "$PROFILE" = "infrastructure" ]; then
    echo ""
    echo "Phase 4.1: Apply infrastructure components"
    apply_infra_tarball
fi
echo ""

# Phase 6.0: Synchronize user hooks
echo "Phase 6.0: Synchronizing user hooks..."
sync_user_hooks
echo ""

# Phase 7.0: Validate hook scripts
echo "Phase 7.0: Validating hook scripts..."
validate_hook_scripts
echo ""

# Version tracking (use git ls-remote for commit SHA)
echo "Updating version files..."
DC_COMMIT=$(git ls-remote "https://github.com/$DEVCONTAINER_REPO.git" "$DEVCONTAINER_BRANCH" | cut -c1-7)
DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)

if [ "$CONTEXT" = "container" ]; then
    echo "{\"commit\": \"$DC_COMMIT\", \"updated\": \"$DATE\"}" > .devcontainer/.template-version
else
    echo "{\"commit\": \"$DC_COMMIT\", \"updated\": \"$DATE\"}" > "$UPDATE_TARGET/.template-version"
fi
echo "  ✓ .template-version ($DC_COMMIT)"

if [ "$PROFILE" = "infrastructure" ] && [ -n "${INFRA_EXTRACT_DIR:-}" ]; then
    INFRA_COMMIT=$(git ls-remote "https://github.com/$INFRA_REPO.git" "$INFRA_BRANCH" | cut -c1-7)
    echo "{\"commit\": \"$INFRA_COMMIT\", \"updated\": \"$DATE\"}" > .infra-template-version
    echo "  ✓ .infra-template-version ($INFRA_COMMIT)"
fi

# Cleanup deprecated files
[ -f ".coderabbit.yaml" ] && rm -f ".coderabbit.yaml" && echo "  Removed deprecated .coderabbit.yaml"

# Migration: remove deprecated MCP servers from runtime mcp.json
if [ -f "$HOME/.claude/mcp.json" ] && command -v jq &>/dev/null; then
    for server in codacy taskmaster; do
        if jq -e ".mcpServers.$server" "$HOME/.claude/mcp.json" &>/dev/null; then
            jq "del(.mcpServers.$server)" "$HOME/.claude/mcp.json" > "$HOME/.claude/mcp.json.tmp" && \
                mv "$HOME/.claude/mcp.json.tmp" "$HOME/.claude/mcp.json"
            echo "  Removed deprecated $server MCP server"
        fi
    done
fi

# Migration: remove .taskmaster/ directory
[ -d ".taskmaster" ] && rm -rf ".taskmaster" && echo "  Removed deprecated .taskmaster/"

# Temp directories cleaned up automatically by trap EXIT

echo ""
echo "═══════════════════════════════════════════════"
echo "  ✓ Update complete"
echo "  Profile: $PROFILE"
echo "  Method: git tarball"
if [ "$PROFILE" = "infrastructure" ]; then
    echo "  Sources: $DEVCONTAINER_REPO + $INFRA_REPO"
else
    echo "  Source: $DEVCONTAINER_REPO"
fi
echo "  Version: $DC_COMMIT"
echo "═══════════════════════════════════════════════"
```

---

## Phase 7.5: Feature Staleness Re-verification

**Confirms Phase 4.5 actually closed the gap. Silent on success; loud on
persistent drift so a forgotten PR or broken push surfaces immediately.**

```yaml
staleness_reverification:
  trigger: "Always when STALE_FEATURES was non-empty at Phase 2.5"

  template_mode_rules:
    - "Expect the bump branch to exist locally and all bumps to be committed"
    - "Exit 1 if the staleness set did NOT shrink to zero (bump script crashed)"

  downstream_mode_rules:
    - "Re-run the GHCR digest check — if the local pin uses a floating tag (:1),
      the digest should now match the just-pulled one. Mismatch → warn but do
      not fail (BuildKit prune is best-effort)."

  implementation:
    - "Re-source detect_feature_staleness from detect.md Phase 2.5"
    - "Compare before/after set sizes"
    - "Append one line to the consolidated report"
```

**Implementation:**

```bash
reverify_feature_staleness() {
    [ -z "${STALE_FEATURES_BEFORE:-${STALE_FEATURES:-}}" ] && return 0
    local before
    before=$(echo "${STALE_FEATURES_BEFORE:-${STALE_FEATURES}}" | grep -c . || true)
    detect_feature_staleness
    local after
    after=$(echo "${STALE_FEATURES:-}" | grep -c . || true)

    if [ "$after" -eq 0 ]; then
        echo "  Staleness re-check: PASS (${before} → 0)"
    else
        case "${REPO_MODE:-downstream}" in
          template)
            echo "  Staleness re-check: FAIL (${before} → ${after}) — bump or push likely broken"
            return 1
            ;;
          *)
            echo "  Staleness re-check: WARN (${before} → ${after}) — rebuild may still be needed"
            ;;
        esac
    fi
}
```
