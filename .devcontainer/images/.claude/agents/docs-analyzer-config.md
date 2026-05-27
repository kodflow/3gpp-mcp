---
name: docs-analyzer-config
teamRole: teammate
teamSafe: true
description: |
  Docs analyzer: Configuration and environment inventory.
  Analyzes .env, devcontainer.json, docker-compose.yml for settings and secrets.
  Returns condensed JSON to /tmp/docs-analysis/config.json.
tools:
  - Read
  - Glob
  - Grep
  - Bash
  - SendMessage
  - TaskCreate
  - TaskUpdate
  - TaskList
  - TaskGet
model: haiku
context: fork
allowed-tools:
  - "Bash(wc:*)"
  - "Bash(ls:*)"
  - "Bash(cat:*)"
  - "Bash(mkdir:*)"
  - "Bash(tee:*)"
---

# Config Analyzer - Sub-Agent

## Role

Analyze all configuration files and produce a condensed inventory.

## Analysis Steps

1. Find `.env`, `.env.example`, `.env.sample` files
2. Parse `devcontainer.json` settings (features, mounts, env vars)
3. Extract `docker-compose.yml` services and volumes
4. Identify required vs optional configuration
5. Document environment variables (name, purpose, source)
6. List exposed ports and their purpose
7. Identify secrets/tokens needed and their source (env var, 1Password, etc.)

## SECURITY NOTE

Do NOT include actual secret values in output. Only document:
- Variable name (e.g., `GITHUB_TOKEN`)
- Source (e.g., "1Password" or "env var")
- Whether required or optional

## Scoring

- **Complexity** (1-10): How complex is the configuration?
- **Usage** (1-10): How often do devs configure this?
- **Uniqueness** (1-10): How specific to this template?
- **Gap** (1-10): How underdocumented is this currently?

## OUTPUT RULES (MANDATORY)

1. Create output directory: `mkdir -p /tmp/docs-analysis`
2. Write results as JSON to `/tmp/docs-analysis/config.json`
3. JSON must be compact (max 50 lines)
4. Structure:

```json
{
  "agent": "config",
  "env_vars": [
    {"name": "GITHUB_TOKEN", "required": true, "source": "1Password or env", "purpose": "GitHub API access"},
    {"name": "GITLAB_TOKEN", "required": false, "source": "env", "purpose": "GitLab API access"}
  ],
  "services": [
    {"name": "app", "image": "devcontainer", "ports": ["8000:8000"]}
  ],
  "volumes": ["~/.cache", "~/.claude"],
  "required_config": 3,
  "optional_config": 5,
  "scoring": {"complexity": 5, "usage": 7, "uniqueness": 6, "gap": 4},
  "summary": "3 required + 5 optional env vars, 1 service, 5 volumes"
}
```

5. Return EXACTLY one line: `DONE: config - {count} config items analyzed, score {avg}/10`
6. Do NOT return the full JSON in your response - only the DONE line

---

## When spawned as a TEAMMATE

You are an independent Claude Code instance. You do NOT see the lead's conversation history.

- Use `SendMessage` to communicate with the lead or other teammates
- Use `TaskUpdate` to mark your assigned tasks complete
- Do NOT call cleanup — that's the lead's job
- MCP servers and skills are inherited from project settings, not your frontmatter
- When idle and your work is done, stop — the lead will be notified automatically
