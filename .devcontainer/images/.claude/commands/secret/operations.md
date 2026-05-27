# Secret Operations (Push/Get/List)

## Action: --push

**Write a secret to 1Password:**

```yaml
push_workflow:
  1_parse_args:
    action: "Parse key=value"
    validation:
      - "key contains no special characters (a-zA-Z0-9_)"
      - "value is not empty"
      - "exact format: KEY=VALUE (single =)"

  2_build_title:
    action: "Build the item title"
    format: "<PROJECT_PATH>/<key>"
    example: "kodflow/devcontainer-template/DB_PASSWORD"

  3_check_exists:
    action: "Check if the item already exists"
    command: "op item get '<title>' --vault '$VAULT_ID' 2>/dev/null"
    decision:
      exists: "Update (op item edit)"
      not_exists: "Create (op item create)"

  4a_create:
    condition: "Item does not exist"
    command: |
      op item create \
        --category=API_CREDENTIAL \
        --title='<org>/<repo>/<key>' \
        --vault='$VAULT_ID' \
        'credential=<value>'
    note: "The 'credential' field matches the existing MCP token pattern"

  4b_update:
    condition: "Item already exists"
    command: |
      op item edit '<org>/<repo>/<key>' \
        --vault='$VAULT_ID' \
        'credential=<value>'

  5_confirm:
    action: "Verify that the item is properly stored"
    command: "op item get '<title>' --vault '$VAULT_ID' --format=json | jq -r '.title'"
```

**Output --push (new):**

```
═══════════════════════════════════════════════════════════════
  /secret --push
═══════════════════════════════════════════════════════════════

  Path   : kodflow/devcontainer-template
  Key    : DB_PASSWORD
  Action : Created

  Item: kodflow/devcontainer-template/DB_PASSWORD
  Vault: CI
  Field: credential
  Status: Stored successfully

═══════════════════════════════════════════════════════════════
```

**Output --push (update):**

```
═══════════════════════════════════════════════════════════════
  /secret --push
═══════════════════════════════════════════════════════════════

  Path   : kodflow/devcontainer-template
  Key    : DB_PASSWORD
  Action : Updated (existing item)

  Item: kodflow/devcontainer-template/DB_PASSWORD
  Vault: CI
  Field: credential
  Status: Updated successfully

═══════════════════════════════════════════════════════════════
```

---

## Action: --get

**Read a secret from 1Password:**

```yaml
get_workflow:
  1_build_title:
    action: "Build the title"
    format: "<PROJECT_PATH>/<key>"

  2_retrieve:
    action: "Retrieve the value"
    command: |
      op item get '<org>/<repo>/<key>' \
        --vault='$VAULT_ID' \
        --fields='credential' \
        --reveal
    fallback_fields: ["credential", "password", "identifiant", "mot de passe"]
    note: "Same fallback logic as get_1password_field in postStart.sh"

  3_display:
    action: "Display the result"
    security: "The value is revealed ONLY ONCE in the output"
```

**Output --get (success):**

```
═══════════════════════════════════════════════════════════════
  /secret --get
═══════════════════════════════════════════════════════════════

  Path  : kodflow/devcontainer-template
  Key   : DB_PASSWORD
  Value : s3cr3t_p4ssw0rd

═══════════════════════════════════════════════════════════════
```

**Output --get (not found):**

```
═══════════════════════════════════════════════════════════════
  /secret --get
═══════════════════════════════════════════════════════════════

  Path  : kodflow/devcontainer-template
  Key   : DB_PASSWORD
  Status: Not found

  Hint: Use /secret --list to see available secrets
        Use /secret --push DB_PASSWORD=<value> to create it

═══════════════════════════════════════════════════════════════
```

---

## Action: --list

**List secrets for a path:**

```yaml
list_workflow:
  1_list_items:
    action: "List all items in the vault"
    command: |
      op item list \
        --vault='$VAULT_ID' \
        --format=json
    filter: "Filter by PROJECT_PATH/ prefix"

  2_display:
    action: "Display the filtered list"
    format: "Table with title, category, modification date"
    extract_key: "Remove the path/ prefix to display only the key"
```

**Output --list (with secrets):**

```
═══════════════════════════════════════════════════════════════
  /secret --list
═══════════════════════════════════════════════════════════════

  Path: kodflow/devcontainer-template

  | Key             | Category       | Updated            |
  |-----------------|----------------|--------------------|
  | DB_PASSWORD     | API_CREDENTIAL | 2026-02-09 10:30   |
  | API_KEY         | API_CREDENTIAL | 2026-02-08 14:22   |
  | JWT_SECRET      | API_CREDENTIAL | 2026-02-07 09:15   |

  Total: 3 secrets

═══════════════════════════════════════════════════════════════
```

**Output --list (empty):**

```
═══════════════════════════════════════════════════════════════
  /secret --list
═══════════════════════════════════════════════════════════════

  Path: kodflow/devcontainer-template

  No secrets found for this project.

  Hint: Use /secret --push KEY=VALUE to store a secret
        Use /secret --list --path / to see all paths

═══════════════════════════════════════════════════════════════
```

**Output --list --path / (all paths):**

```
═══════════════════════════════════════════════════════════════
  /secret --list --path /
═══════════════════════════════════════════════════════════════

  All secrets (grouped by path):

  kodflow/devcontainer-template/ (3 secrets)
    ├─ DB_PASSWORD
    ├─ API_KEY
    └─ JWT_SECRET

  kodflow/shared-infra/ (2 secrets)
    ├─ AWS_CREDENTIALS
    └─ TF_VAR_db_password

  (legacy items without path)
    ├─ mcp-github
    └─ mcp-coderabbit

  Total: 7 items (5 with paths, 2 legacy)

═══════════════════════════════════════════════════════════════
```
