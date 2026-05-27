# 1Password CLI Integration & Path Conventions

## Phase 1.0: Peek (MANDATORY)

**Verify prerequisites BEFORE any operation:**

```yaml
peek_workflow:
  1_check_op:
    action: "Verify that op CLI is available"
    command: "command -v op"
    on_failure: |
      ABORT with message:
      "op CLI not found. Install 1Password CLI or run inside DevContainer."

  2_check_token:
    action: "Verify OP_SERVICE_ACCOUNT_TOKEN"
    command: "test -n \"$OP_SERVICE_ACCOUNT_TOKEN\""
    on_failure: |
      ABORT with message:
      "OP_SERVICE_ACCOUNT_TOKEN not set. Configure in .devcontainer/.env"

  3_check_vault:
    action: "Verify vault access"
    command: "op vault list --format=json 2>/dev/null | jq -r '.[0].id'"
    store: "VAULT_ID"
    on_failure: |
      ABORT with message:
      "Cannot access 1Password vault. Check OP_SERVICE_ACCOUNT_TOKEN."

  4_resolve_path:
    action: "Resolve project path from git remote"
    command: |
      REMOTE_URL=$(git config --get remote.origin.url 2>/dev/null || echo "")
      # Remove .git suffix
      REMOTE_URL="${REMOTE_URL%.git}"
      # Extract org/repo (handles HTTPS, SSH, token-embedded)
      if [[ "$REMOTE_URL" =~ [:/]([^/]+)/([^/]+)$ ]]; then
        PROJECT_PATH="${BASH_REMATCH[1]}/${BASH_REMATCH[2]}"
      else
        ABORT "Cannot resolve project path from git remote: $REMOTE_URL"
      fi
    store: "PROJECT_PATH"
    override: "--path argument if provided"
```

**Phase 1 Output:**

```
═══════════════════════════════════════════════════════════════
  /secret - Connection Check
═══════════════════════════════════════════════════════════════

  1Password CLI : op v2.32.0
  Service Token : OP_SERVICE_ACCOUNT_TOKEN (set)
  Vault Access  : CI (${OP_VAULT_ID})
  Project Path  : kodflow/devcontainer-template

═══════════════════════════════════════════════════════════════
```

---

## Path Convention (Vault-like)

**Tree structure in 1Password:**

```
<vault>/                              # 1Password vault (default: CI)
├── kodflow/
│   ├── devcontainer-template/        # Current project
│   │   ├── DB_PASSWORD               # Item: kodflow/devcontainer-template/DB_PASSWORD
│   │   ├── API_KEY                   # Item: kodflow/devcontainer-template/API_KEY
│   │   └── JWT_SECRET                # Item: kodflow/devcontainer-template/JWT_SECRET
│   ├── shared-infra/                 # Shared secrets
│   │   ├── AWS_CREDENTIALS            # Item: kodflow/shared-infra/AWS_CREDENTIALS
│   │   └── TF_VAR_db_password       # Item: kodflow/shared-infra/TF_VAR_db_password
│   └── other-project/
│       └── STRIPE_KEY                # Item: kodflow/other-project/STRIPE_KEY
└── mcp-github                        # Existing items (legacy pattern)
```

**Path resolution:**

```bash
# Git remote → path
git remote get-url origin
  → https://github.com/kodflow/devcontainer-template.git
  → path: kodflow/devcontainer-template

# SSH format
  → git@github.com:kodflow/devcontainer-template.git
  → path: kodflow/devcontainer-template

# Token-embedded
  → https://ghp_xxx@github.com/kodflow/devcontainer-template.git
  → path: kodflow/devcontainer-template
```

**Strict rule:** Without `--path`, ALL operations are scoped to the current project path. It is impossible to access a different path without specifying it explicitly.

---

## 1Password Item Format

Each secret is stored as a 1Password item:

```yaml
item:
  title: "<org>/<repo>/<key>"           # Ex: kodflow/devcontainer-template/DB_PASSWORD
  category: "API_CREDENTIAL"            # Same category as mcp-github
  vault: "${OP_VAULT_ID}"               # Configured vault (default: CI)
  fields:
    - name: "credential"                # Main field (same pattern as MCP tokens)
      value: "<secret_value>"
    - name: "notesPlain"                # Optional metadata
      value: "Managed by /secret skill"
```

---

## Cross-Project Secret Sharing

**Use `--path` to share secrets between projects:**

```yaml
sharing_patterns:
  # Share a common infra secret
  push_shared:
    command: '/secret --push AWS_CREDENTIALS=xxx... --path kodflow/shared-infra'
    note: "Accessible by all kodflow projects"

  # Retrieve from another project
  get_cross_project:
    command: '/secret --get STRIPE_KEY --path kodflow/payment-service'
    note: "Unblock a situation by retrieving a secret from another project"

  # Unblock a situation
  unblock_workflow:
    1: '/secret --list --path /'
    2: 'Identify the needed secret and its path'
    3: '/secret --get <key> --path <org>/<repo>'
```
