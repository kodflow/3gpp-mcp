# Git Identity Validation (Phase 1.0)

**Verify and configure git identity BEFORE any action.**

This phase is MANDATORY for `--commit`. Skip only with `--skip-identity` flag.

---

## Identity Workflow

```yaml
identity_validation:
  env_file: "/workspace/.env"

  1_check_env:
    action: "Check if .env exists and contains GIT_USER/GIT_EMAIL"
    tool: Read("/workspace/.env")
    fallback: "File not found → create"

  2_extract_or_ask:
    rule: |
      IF .env exists AND contains GIT_USER AND GIT_EMAIL:
        user = extract(GIT_USER)
        email = extract(GIT_EMAIL)
      ELSE:
        → AskUserQuestion (see below)
        → Create/Update .env

  3_verify_git_config:
    action: "Compare with current git config"
    commands:
      - "git config user.name"
      - "git config user.email"
    decision:
      if_match: "→ Continue to Phase 1"
      if_mismatch: "→ Fix git config"

  4_fix_if_needed:
    action: "Apply the correct configuration"
    commands:
      - "git config user.name '{user}'"
      - "git config user.email '{email}'"

  5_check_gpg:
    action: "Check if GPG signing is configured"
    commands:
      - "git config --get commit.gpgsign"
      - "git config --get user.signingkey"

  6_configure_gpg_if_missing:
    condition: "commit.gpgsign != true OR user.signingkey is empty"
    action: "List GPG keys and prompt for selection if needed"
    workflow:
      1_list_keys: "gpg --list-secret-keys --keyid-format LONG"
      2_find_matching:
        rule: "Find key matching GIT_EMAIL"
        action: "grep -B1 '{email}' in gpg output"
      3_if_no_match_but_keys_exist:
        tool: AskUserQuestion
        questions:
          - question: "Which GPG key to use for signing commits?"
            header: "GPG Key"
            options: "<dynamically generated from gpg output>"
      4_configure:
        commands:
          - "git config --global user.signingkey {selected_key}"
          - "git config --global commit.gpgsign true"
          - "git config --global tag.forceSignAnnotated true"
```

---

## Prompt if .env Missing or Incomplete

```yaml
ask_identity:
  tool: AskUserQuestion
  questions:
    - question: "What name to use for git commits?"
      header: "Git User"
      options:
        - label: "{detected_user}"
          description: "Detected from global git config"
        - label: "{github_user}"
          description: "Detected from GitHub/GitLab"
      # User can also enter "Other" with custom value

    - question: "What email address to use for commits?"
      header: "Git Email"
      options:
        - label: "{detected_email}"
          description: "Detected from global git config"
        - label: "{noreply_email}"
          description: "GitHub/GitLab noreply email"
```

---

## Generated .env Format

```bash
# Git identity for commits (managed by /git)
GIT_USER="John Doe"
GIT_EMAIL="john.doe@example.com"
```

---

## Output: Identity & GPG Validated

```text
═══════════════════════════════════════════════════════════════
  /git --commit - Git Identity & GPG Validation
═══════════════════════════════════════════════════════════════

  .env check:
    ├─ File: /workspace/.env
    ├─ GIT_USER: "John Doe" ✓
    └─ GIT_EMAIL: "john.doe@example.com" ✓

  Git config:
    ├─ user.name: "John Doe" ✓ (match)
    └─ user.email: "john.doe@example.com" ✓ (match)

  GPG config:
    ├─ commit.gpgsign: true ✓
    └─ user.signingkey: ABCD1234EF567890 ✓

  Status: ✓ Identity & GPG validated, proceeding to Phase 1

═══════════════════════════════════════════════════════════════
```

## Output: Correction Needed

```text
═══════════════════════════════════════════════════════════════
  /git --commit - Git Identity Validation
═══════════════════════════════════════════════════════════════

  .env check:
    ├─ File: /workspace/.env
    ├─ GIT_USER: "John Doe" ✓
    └─ GIT_EMAIL: "john.doe@example.com" ✓

  Git config:
    ├─ user.name: "johndoe" ✗ (mismatch)
    └─ user.email: "old@email.com" ✗ (mismatch)

  Action: Correcting git config...
    ├─ git config user.name "John Doe"
    └─ git config user.email "john.doe@example.com"

  Status: ✓ Identity corrected, proceeding to Phase 1

═══════════════════════════════════════════════════════════════
```

## Output: .env Missing

```text
═══════════════════════════════════════════════════════════════
  /git --commit - Git Identity Validation
═══════════════════════════════════════════════════════════════

  .env check:
    └─ File: NOT FOUND → Creating...

  User input required...
    ├─ Git User: "John Doe" (entered)
    └─ Git Email: "john.doe@example.com" (entered)

  Actions:
    ├─ Created /workspace/.env with GIT_USER, GIT_EMAIL
    ├─ git config user.name "John Doe"
    └─ git config user.email "john.doe@example.com"

  Status: ✓ Identity configured, proceeding to Phase 1

═══════════════════════════════════════════════════════════════
```
