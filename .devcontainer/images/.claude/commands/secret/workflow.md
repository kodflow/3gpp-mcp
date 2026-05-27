# Cross-Skill Integration

## From /init

```yaml
init_integration:
  phase: "Phase 3 (Parallelize)"
  agent: "vault-checker"
  check:
    - "op CLI available"
    - "OP_SERVICE_ACCOUNT_TOKEN set"
    - "Vault accessible"
    - "Number of secrets for the current project"
  report_section: "1Password Secrets"
```

---

## From /git (pre-commit)

```yaml
git_integration:
  phase: "Phase 3 (Parallelize)"
  agent: "secret-scan"
  check:
    - "Scan git diff --cached for secret patterns"
    - "Patterns: ghp_, glpat-, sk-, pk_, postgres://, mysql://, mongodb://"
    - "If found: WARN (do not block)"
    - "Suggest: /secret --push <key>=<detected_value>"
  behavior: "WARNING only, does NOT block the commit"
```

---

## From /do

```yaml
do_integration:
  phase: "Phase 0 (before Questions)"
  check:
    - "If the task mentions: secret, token, credential, password, API key"
    - "List available secrets for the project"
    - "Suggest using them or creating new ones"
  behavior: "Informational, helps unblock"
```

---

## From /infra

```yaml
infra_integration:
  phase: "Before --plan and --apply"
  check:
    - "List project secrets with TF_VAR_ prefix"
    - "Check if Terraform variables reference secrets"
    - "Suggest retrieving from 1Password"
  cross_path: "Allow --path kodflow/shared-infra for shared secrets"
```

---

## Guardrails (ABSOLUTE)

| Action | Status | Reason |
|--------|--------|--------|
| Reveal a secret without explicit --get | **FORBIDDEN** | Security |
| Write a secret to logs | **FORBIDDEN** | Security |
| Push without confirmation if item exists | **FORBIDDEN** | Prevent overwrite |
| Access a different path without --path | **FORBIDDEN** | Strict scope |
| Operate without OP_SERVICE_ACCOUNT_TOKEN | **FORBIDDEN** | Auth required |
| Delete a secret (no --delete) | **FORBIDDEN** | Use 1Password UI |
| Skip Phase 1 (Peek) | **FORBIDDEN** | Connection verification |
