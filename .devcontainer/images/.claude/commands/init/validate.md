# Setup Validation

## Phase 5.0: Environment Validation

**Verify the environment (parallel via Task agents).**

```yaml
parallel_checks:
  agents:
    - name: "tools-checker"
      checks: [git, node, go, terraform, docker, rtk]
      output: "{tool, required, installed, status}"

    - name: "deps-checker"
      checks: [npm ci, go mod, terraform init]
      output: "{manager, status, issues}"

    - name: "config-checker"
      checks: [.env, CLAUDE.md, mcp.json]
      output: "{file, status, issue}"

    - name: "rtk-checker"
      checks: [rtk binary, hook installed, rtk gain]
      output: "{component, status, details}"

    - name: "secret-checker"
      checks: [op CLI, OP_SERVICE_ACCOUNT_TOKEN, vault access, project secrets]
      output: "{op_installed, token_set, vault_name, project_path, secrets_count, status}"
```

---

## Phase 6.0: Report

```
═══════════════════════════════════════════════════════════════
  /init - Complete
═══════════════════════════════════════════════════════════════

  Project: {name}
  Purpose: {purpose summary}

  Generated:
    ✓ docs/vision.md
    ✓ CLAUDE.md
    ✓ AGENTS.md
    ✓ docs/architecture.md
    ✓ docs/workflows.md
    ✓ README.md (updated)
    ✓ .coderabbit.yaml (generated if missing)
    ✓ .pr_agent.toml (generated if missing)
    ✓ .codacy.yaml (generated if missing)
    {{#if phase4_8_configured}}✓ Branch protection: main-protection ruleset (CI gates){{/if}}
    {conditional files}

  Environment:
    ✓ Tools installed ({tool list})
    ✓ Dependencies ready
    ✓ RTK rewriter active (hook installed)

  1Password:
    ✓ op CLI installed
    ✓ Vault connected ({N} project secrets)

  Ready to develop!
    → /feature "description" to start

═══════════════════════════════════════════════════════════════
```

---

## Auto-fix (automatic)

When a problem is detected, auto-fix if possible:

| Problem | Auto Action |
|---------|-------------|
| `.env` missing | `cp .env.example .env` |
| deps not installed | `npm ci` / `go mod download` |
| rtk binary missing | Re-run image postStart `init_rtk` step |
| rtk hook missing | Re-run image `postStart` `init_rtk` step (the rtk binary owns the hook — `rtk init -g` to reinstall) |

---

## Guardrails

| Action | Status |
|--------|--------|
| Skip detection | FORBIDDEN |
| Closed questions / AskUserQuestion | FORBIDDEN |
| Placeholders in generated files | FORBIDDEN |
| Skip vision synthesis review | FORBIDDEN |
| Destructive fix without asking | FORBIDDEN |
