# Project Templates (AI Tools & CI Configuration)

## Phase 4.6: Qodo Merge (PR-Agent) Configuration (AI Tools 2/3)

**Generate `.pr_agent.toml` if missing, personalized from project context.**
**Official docs:** https://qodo-merge-docs.qodo.ai/usage-guide/configuration_options/
**Canonical defaults:** https://github.com/qodo-ai/pr-agent/blob/main/pr_agent/settings/configuration.toml

```yaml
qodo_merge_config:
  trigger: "ALWAYS (after CodeRabbit config)"
  docs: "https://qodo-merge-docs.qodo.ai/usage-guide/configuration_options/"
  canonical_defaults: "https://github.com/qodo-ai/pr-agent/blob/main/pr_agent/settings/configuration.toml"

  1_check_exists:
    action: "Glob('/workspace/.pr_agent.toml')"
    if_exists:
      status: "SKIP"
      output: "Phase 4.6 skipped output"
    if_missing:
      status: "GENERATE"
      steps: [2_detect_stack, 3_build_reviewer_instructions, 4_build_suggestion_instructions, 5_generate_file, 6_validate]

  2_detect_stack:
    action: "Map languages to review conventions for extra_instructions"
    mapping:
      "Go":      "enforce Go error handling (no bare returns), unused vars, panic prevention in production paths"
      "Rust":    "enforce ownership safety, flag unsafe blocks, check panic paths and unwrap/expect"
      "Python":  "enforce type hints, exception handling, no bare except"
      "Node/TS": "enforce strict TypeScript, async/await error handling, no floating promises"
      "Java":    "enforce null checks, resource management (try-with-resources), exception handling"
      "C#":      "enforce nullable reference types, async/await patterns, IDisposable"
      "Shell":   "enforce strict mode (set -euo pipefail), quoting, shellcheck compliance"
      "Docker":  "enforce Dockerfile best practices, non-root user, multi-stage builds, minimal images"
      "Ruby":    "enforce frozen string literals, exception handling, RuboCop compliance"
      "PHP":     "enforce strict types, null safety, PSR compliance"

  3_build_reviewer_instructions:
    action: "Combine base P0/P1/P2 triage + stack-specific rules"
    base: |
      Staff-level reviewer. Diff-first, evidence-driven.
      Triage: P0 (blocker), P1 (major), P2 (minor).
      Cap at 10 findings. If P0 exists, hide P2 entirely.
      Each finding: What/Where + Why + Fix.
    stack_specific: "Merged from step 2 per detected language"

  4_build_suggestion_instructions:
    action: "Adapt code suggestion rules to detected stack"
    base: |
      P0 blockers only. Minimal diffs. No refactors.
      Must compile and preserve existing behavior.
      Keep changes localized to smallest surface area.

  5_generate_file:
    action: "Write /workspace/.pr_agent.toml"
    sections:
      - "[pr_reviewer]": "enable_review_labels_security, enable_review_labels_effort, require_security_review, require_tests_review, extra_instructions"
      - "[pr_code_suggestions]": "num_code_suggestions=6, extra_instructions"
      - "[pr_description]": "enable_semantic_files_types, collapsible_file_list=adaptive, generate_ai_title=false"
      - "[pr_questions]": "enable_help_text=true"
      - "[rag_arguments]": "NOTE: RAG requires Enterprise tier (commented out by default)"
      - "[pr_compliance]": "enable_codebase_duplication, enable_global_pr_compliance, enable_generic_custom_compliance_checklist"
      - "[github_action_config]": "auto_review, auto_describe, auto_improve"
      - "[config]": "output_relevant_configurations=false"

  6_validate:
    action: |
      python3 -c "
      import tomllib, pathlib
      cfg = tomllib.loads(pathlib.Path('/workspace/.pr_agent.toml').read_text())
      sections = list(cfg.keys())
      print(f'valid ({len(sections)} sections: {", ".join(sections)})')
      "
    reference: "Cross-check keys against canonical: https://github.com/qodo-ai/pr-agent/blob/main/pr_agent/settings/configuration.toml"
    on_failure: "Fix TOML syntax and retry"
```

**Output Phase 4.6 (generated):**

```text
═══════════════════════════════════════════════════════════════
  Qodo Merge (PR-Agent) Configuration
═══════════════════════════════════════════════════════════════

  Status: GENERATED (new file)

  Detected Stack:
    ├─ Go       → error handling, panic prevention
    ├─ Shell    → strict mode, shellcheck
    └─ Docker   → hadolint compliance

  Sections:
    ├─ [pr_reviewer] (P0/P1/P2 triage + stack-specific extra_instructions)
    ├─ [pr_code_suggestions] (6 suggestions, P0 blockers only)
    ├─ [pr_description] (semantic files, adaptive collapse)
    ├─ [pr_compliance] (duplication + global compliance)
    └─ [github_action_config] (auto review/describe/improve)

  Validation: valid (TOML syntax)
  Reference: https://github.com/qodo-ai/pr-agent/blob/main/pr_agent/settings/configuration.toml

═══════════════════════════════════════════════════════════════
```

**Output Phase 4.6 (skipped):**

```text
═══════════════════════════════════════════════════════════════
  Qodo Merge (PR-Agent) Configuration
═══════════════════════════════════════════════════════════════

  Status: SKIPPED (file already exists)

═══════════════════════════════════════════════════════════════
```

---

## Phase 4.7: GitHub Branch Protection (CI Gates)

**Configure branch protection ruleset and tighten CI gates for merge quality.**
**API docs:** https://docs.github.com/en/rest/repos/rules

```yaml
branch_protection_config:
  trigger: "ALWAYS (after Qodo Merge config)"
  api: "https://docs.github.com/en/rest/repos/rules"

  1_check_exists:
    action: |
      GITHUB_TOKEN=$(jq -r '.mcpServers.github.env.GITHUB_PERSONAL_ACCESS_TOKEN // empty' /workspace/mcp.json 2>/dev/null)
      [ -z "$GITHUB_TOKEN" ] && { echo "No GITHUB_TOKEN — skipping"; exit 1; }
      REMOTE=$(git remote get-url origin 2>/dev/null)
      [ -z "$REMOTE" ] && { echo "No git remote — skipping"; exit 1; }
      OWNER=$(echo "$REMOTE" | sed 's|.*github.com[:/]\([^/]*\)/.*|\1|')
      REPO=$(echo "$REMOTE" | sed 's|.*/\([^.]*\)\.git$|\1|; s|.*/\([^/]*\)$|\1|')
      if [ -z "$OWNER" ] || [ -z "$REPO" ]; then echo "Cannot parse owner/repo from $REMOTE"; exit 1; fi
      TMPFILE=$(mktemp)
      trap 'rm -f "$TMPFILE"' EXIT
      HTTP_CODE=$(curl -sS -w "%{http_code}" -o "$TMPFILE" \
        -H "Authorization: Bearer $GITHUB_TOKEN" \
        -H "Accept: application/vnd.github+json" \
        -H "X-GitHub-Api-Version: 2022-11-28" \
        "https://api.github.com/repos/$OWNER/$REPO/rulesets")
      if [ "$HTTP_CODE" = "200" ]; then
        jq -e '.[] | select(.name == "main-protection")' "$TMPFILE" > /dev/null 2>&1
      elif [ "$HTTP_CODE" = "404" ]; then
        false  # Not found → trigger CONFIGURE
      else
        echo "GitHub API error (HTTP $HTTP_CODE)"; cat "$TMPFILE"; exit 2
      fi
    exit_codes:
      0: "Ruleset found (jq matched)"
      1: "No token, no remote, bad owner/repo, or ruleset not found (404/jq miss)"
      2: "HTTP/auth error (non-200, non-404)"
    if_exists:
      status: "SKIP"
      message: "Ruleset main-protection already exists."
    if_missing:
      status: "CONFIGURE"
      steps: [2_extract_tokens, 3_detect_owner_repo, 4_update_coderabbit, 5_create_ruleset, 6_validate]
    if_api_error:
      status: "SKIP"
      message: "GitHub API error — cannot verify rulesets. Check token permissions."
    if_no_token:
      status: "SKIP"
      message: "No GITHUB_TOKEN in mcp.json — cannot configure branch protection."

  2_extract_tokens:
    action: "Extract tokens from /workspace/mcp.json using jq"
    github: "jq -r '.mcpServers.github.env.GITHUB_PERSONAL_ACCESS_TOKEN // empty' /workspace/mcp.json"
    notes:
      - "GITHUB_TOKEN must be non-empty — abort phase if empty"

  3_detect_owner_repo:
    action: "Parse owner/repo from git remote origin"
    command: |
      REMOTE=$(git remote get-url origin 2>/dev/null)
      [ -z "$REMOTE" ] && { echo "No git remote — skipping"; exit 1; }
      OWNER=$(echo "$REMOTE" | sed 's|.*github.com[:/]\([^/]*\)/.*|\1|')
      REPO=$(echo "$REMOTE" | sed 's|.*/\([^.]*\)\.git$|\1|; s|.*/\([^/]*\)$|\1|')
      if [ -z "$OWNER" ] || [ -z "$REPO" ]; then echo "Cannot parse owner/repo from $REMOTE"; exit 1; fi
    handles: "SSH (git@github.com:owner/repo.git) and HTTPS (https://github.com/owner/repo)"
    on_failure: "Log warning, skip phase"

  4_update_coderabbit:
    action: "Edit .coderabbit.yaml — harden pre_merge_checks from warning to error"
    condition: "Glob('/workspace/.coderabbit.yaml') returns a match"
    edit:
      target_keys:
        - "reviews.pre_merge_checks.title.mode"
        - "reviews.pre_merge_checks.description.mode"
      from: "warning"
      to: "error"
    preserve:
      - "reviews.request_changes_workflow: true (must remain true)"
      - "All other keys unchanged"
    if_file_missing: "SKIP — log: .coderabbit.yaml not found"

  4b_validate_coderabbit:
    action: "Re-validate .coderabbit.yaml after edit (same logic as Phase 4.5 step 7)"
    condition: "Glob('/workspace/.coderabbit.yaml') returns a match"
    command: |
      python3 - <<'PY'
      import json, pathlib, urllib.request, yaml
      from jsonschema import validate

      cfg_path = pathlib.Path("/workspace/.coderabbit.yaml")
      cfg = yaml.safe_load(cfg_path.read_text())
      schema = json.load(urllib.request.urlopen("https://www.coderabbit.ai/integrations/schema.v2.json"))
      validate(instance=cfg, schema=schema)
      print("valid")
      PY
    on_success: "YAML valid after pre_merge_checks edit"
    if_file_missing: "SKIP — log: .coderabbit.yaml not found"
    on_failure: "Revert edit (restore warning mode), log error, continue"

  5_create_ruleset:
    action: "POST to GitHub Rulesets API to create main-protection"
    command: |
      RULES='[{"type":"pull_request","parameters":{"required_approving_review_count":1,"dismiss_stale_reviews_on_push":true,"require_last_push_approval":false,"required_review_thread_resolution":true}}]'
      curl -fsSL -X POST \
        -H "Authorization: Bearer $GITHUB_TOKEN" \
        -H "Accept: application/vnd.github+json" \
        -H "X-GitHub-Api-Version: 2022-11-28" \
        -H "Content-Type: application/json" \
        -d "{
          \"name\": \"main-protection\",
          \"target\": \"branch\",
          \"enforcement\": \"active\",
          \"conditions\": {
            \"ref_name\": {
              \"include\": [\"refs/heads/main\"],
              \"exclude\": []
            }
          },
          \"rules\": $RULES
        }" \
        "https://api.github.com/repos/$OWNER/$REPO/rulesets"
    on_failure: "Display HTTP error — may require GitHub Pro/Team plan for rulesets"

  6_validate:
    action: "Confirm ruleset is active"
    command: |
      curl -fsSL \
        -H "Authorization: Bearer $GITHUB_TOKEN" \
        -H "Accept: application/vnd.github+json" \
        -H "X-GitHub-Api-Version: 2022-11-28" \
        "https://api.github.com/repos/$OWNER/$REPO/rulesets" \
        | jq -e '.[] | select(.name == "main-protection" and .enforcement == "active")'
    on_success: "Ruleset confirmed active"
    on_failure: "Log warning: could not verify ruleset"
```

**Output Phase 4.7 (configured):**

```text
═══════════════════════════════════════════════════════════════
  GitHub Branch Protection (CI Gates)
═══════════════════════════════════════════════════════════════

  Status: CONFIGURED (ruleset created)

  Ruleset: main-protection
    ├─ Target  : refs/heads/main
    ├─ Enforce : active
    └─ Reviews : 1 required approver (dismiss stale on push)

  CodeRabbit:
    └─ pre_merge_checks: title + description → mode: error

  Qodo:
    └─ No gate required

═══════════════════════════════════════════════════════════════
```

**Output Phase 4.7 (skipped):**

```text
═══════════════════════════════════════════════════════════════
  GitHub Branch Protection (CI Gates)
═══════════════════════════════════════════════════════════════

  Status: SKIPPED (ruleset main-protection already exists)

═══════════════════════════════════════════════════════════════
```

---

## Phase 4.8: Feature Bootstrap (Conditional)

```yaml
phase_4.8_feature_bootstrap:
  condition: "/workspace/.claude/features.json absent"
  actions:
    1_create_db:
      action: |
        Ensure directory exists: mkdir -p /workspace/.claude
        Create /workspace/.claude/features.json with: { "version": 2, "features": [] }
    2_propose_features:
      action: |
        Based on the discovery conversation, propose /feature --add
        for each identified feature of the project.
        For each feature, ask user to specify:
          - level (0 = architectural, 1 = subsystem, 2+ = component)
          - workdirs (directories this feature owns)
          - audit_dirs (directories this feature audits, default = workdirs)
        Show inferred parent-child relationships after all features are added.
        Ask user to confirm each feature before adding.
```
