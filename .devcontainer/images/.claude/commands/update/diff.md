# Template Comparison & Download

## ZSH Compatibility (CRITICAL)

**The default shell is `zsh` (set via `chsh -s /bin/zsh` in Dockerfile).**
Claude Code's Bash tool executes commands using `$SHELL` (zsh), not bash.

**RULE: All inline scripts MUST be zsh-compatible.**

| Pattern | Status | Reason |
|---------|--------|--------|
| `for x in $VAR` | **BROKEN in zsh** | zsh does not split variables on IFS |
| `while IFS= read -r x; do` | **WORKS everywhere** | Portable bash/zsh |
| `for x in literal1 literal2` | **WORKS everywhere** | No variable expansion |

**Always use `while read` for iterating over command output:**

```bash
# CORRECT (works in both bash and zsh):
curl ... | jq ... | while IFS= read -r item; do
    [ -z "$item" ] && continue
    echo "$item"
done

# INCORRECT (breaks in zsh - variable not split):
ITEMS=$(curl ... | jq ...)
for item in $ITEMS; do
    echo "$item"
done
```

**For the reference script:** Write to a temp file and execute with `bash` explicitly:
```bash
# Write script to temp file, then run with bash
cat > /tmp/update-script.sh << 'SCRIPT'
#!/bin/bash
# ... script content ...
SCRIPT
bash /tmp/update-script.sh && rm -f /tmp/update-script.sh
```

---

## Configuration

```yaml
# DevContainer template (always - 1 API call)
REPO: "kodflow/devcontainer-template"
BRANCH: "main"
TARBALL_URL: "https://api.github.com/repos/${REPO}/tarball/${BRANCH}"

# Infrastructure template (auto-detected - 1 API call)
INFRA_REPO: "kodflow/infrastructure-template"
INFRA_BRANCH: "main"
INFRA_TARBALL_URL: "https://api.github.com/repos/${INFRA_REPO}/tarball/${INFRA_BRANCH}"
```

---

## Phase 2.0: Peek (Version Check)

```yaml
peek_workflow:
  1_connectivity:
    action: "Verify GitHub connectivity"
    tool: WebFetch
    url: "https://api.github.com/repos/kodflow/devcontainer-template/commits/main"

  2_local_version:
    action: "Read local version"
    tool: Read
    file: ".devcontainer/.template-version"
```

**Output Phase 2.0:**

```
═══════════════════════════════════════════════
  /update - Peek Analysis
═══════════════════════════════════════════════

  Connectivity   : ✓ GitHub API accessible
  Local version  : abc1234 (2024-01-15)
  Remote version : def5678 (2024-01-20)

  Status: UPDATE AVAILABLE

═══════════════════════════════════════════════
```

---

## Phase 3.0: Download (Git Tarball - Single API Call)

**CRITICAL RULE: Download the full tarball in 1 API call.**

One `curl` per source instead of N individual per-file calls.
The tarball is extracted into a temp directory, then files are
copied to their destinations.

```yaml
download_workflow:
  strategy: "GIT-TARBALL (1 API call per source)"

  devcontainer_tarball:
    url: "https://api.github.com/repos/kodflow/devcontainer-template/tarball/main"
    method: "curl -sL -o /tmp/devcontainer-template.tar.gz"
    extract: "tar xzf into /tmp/devcontainer-template/"
    note: "GitHub returns tarball with prefix dir (owner-repo-sha/)"

  infrastructure_tarball:
    condition: "PROFILE == infrastructure"
    url: "https://api.github.com/repos/kodflow/infrastructure-template/tarball/main"
    method: "curl -sL -o /tmp/infrastructure-template.tar.gz"
    extract: "tar xzf into /tmp/infrastructure-template/"

  protected_paths:
    description: "NEVER overwritten - product-specific files"
    paths:
      - "inventory/"
      - "terragrunt.hcl"
      - ".env*"
      - "CLAUDE.md"
      - "AGENTS.md"
      - "README.md"
      - "Makefile"
      - "docs/"
```

**Implementation:**

```bash
# Download and extract a GitHub tarball (1 API call)
# Returns the extracted directory path via EXTRACT_DIR variable
download_tarball() {
    local tarball_url="$1"
    local label="$2"
    local tmp_dir=$(mktemp -d)
    local tmp_tar="${tmp_dir}/template.tar.gz"

    echo "  Downloading $label tarball..."
    local http_code
    http_code=$(curl -sL -w "%{http_code}" -o "$tmp_tar" "$tarball_url")

    if [ "$http_code" != "200" ]; then
        echo "  ✗ $label tarball download failed (HTTP $http_code)"
        rm -rf "$tmp_dir"
        return 1
    fi

    if [ ! -s "$tmp_tar" ]; then
        echo "  ✗ $label tarball is empty"
        rm -rf "$tmp_dir"
        return 1
    fi

    # Extract with --strip-components=1 (GitHub tarballs have owner-repo-sha/ prefix)
    if ! tar xzf "$tmp_tar" --strip-components=1 -C "$tmp_dir"; then
        echo "  ✗ $label extraction failed"
        rm -rf "$tmp_dir"
        return 1
    fi
    rm -f "$tmp_tar"

    EXTRACT_DIR="$tmp_dir"

    echo "  ✓ $label tarball downloaded and extracted"
    return 0
}
```
