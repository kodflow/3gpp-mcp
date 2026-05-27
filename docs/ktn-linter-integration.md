# ktn-linter Integration Contract

## Overview

This document defines the integration between `devcontainer-template` and `ktn-linter` for Claude Code hooks and MCP server configuration.

**Architecture: The template provides hook wrapper scripts, ktn-linter provides the runtime logic.**

```
┌───────────────────────────────────────────────────────────────┐
│                    TEMPLATE (devcontainer)                      │
│  - ktn-linter calls embedded in existing hook scripts          │
│  - pre-validate.sh, on-stop.sh                                 │
│  - PostToolUse wired via project-level native HTTP hook        │
│  - Graceful degradation if ktn-linter not running              │
│  - MCP fragment system (requires_binary gate)                  │
└──────────────────────────┬────────────────────────────────────┘
                           │ HTTP calls (localhost:7717)
┌──────────────────────────▼────────────────────────────────────┐
│                    KTN-LINTER (runtime)                         │
│  - HTTP endpoints: /hooks/pre-tool-use, post-tool-use, stop    │
│  - ScanReport canonique + severity-first formatting            │
│  - Session tracking + package-scoped validation                │
└───────────────────────────────────────────────────────────────┘
```

## Responsibilities

### Template (devcontainer-template)

| Responsibility | Details |
|---------------|---------|
| **Binary** | Installs ktn-linter (Go feature) |
| **MCP registration** | Fragment at `/etc/mcp/features/go.mcp.json` with `requires_binary` gate |
| **Hook integration** | ktn-linter calls embedded in `pre-validate.sh`, `on-stop.sh` (PostToolUse moved to project-level native HTTP hook — see images/CLAUDE.md, issue #344) |
| **Graceful degradation** | Calls exit silently if ktn-linter is not running (curl fails → continue) |
| **Permissions** | `Bash(ktn-linter:*)` pre-authorized in settings.json |

### ktn-linter

| Responsibility | Details |
|---------------|---------|
| **HTTP server** | Listens on port 7717, exposes `/hooks/*` endpoints |
| **Lint logic** | ScanReport, severity ordering, phase grouping |
| **Response format** | Returns `hookSpecificOutput` JSON or plain text |
| **Session tracking** | Tracks edited packages via SessionID |
| **No settings.json writes** | Template handles all hook declarations |

### Consumer project

| Responsibility | Details |
|---------------|---------|
| **PostToolUse hook** | Wire the native HTTP hook in `.claude/settings.json` (matcher `Edit|Write|MultiEdit`, `type: "http"`, `url: http://localhost:7717/hooks/post-tool-use`, `timeout: 15`). See PostToolUse section below for the full snippet. |
| **PreToolUse / Stop** | Zero manual configuration — embedded in `pre-validate.sh` / `on-stop.sh`. |
| **Port override** | If you change the port: edit the literal URL in your `.claude/settings.json` AND set `KTN_LINTER_PORT` so the template scripts (`pre-validate.sh`, `on-stop.sh`) match. JSON settings do not perform shell expansion. |

## Hook Integration Points

ktn-linter calls are embedded directly in existing template hook scripts — no separate files.

### `pre-validate.sh` — PreToolUse (Write|Edit)

After protected file validation, calls `/hooks/pre-tool-use` to surface existing package issues.

- Skips non-code files (*.md, *.json, *.yaml, /tmp/*, .claude/*)
- Curl timeout: 4s (within 5s hook timeout)
- Phase scope: `structural,signatures` (override: `KTN_PRE_PHASES`)
- Fail-open: continues silently if ktn-linter unreachable

### PostToolUse (Write|Edit|MultiEdit) — project-level native HTTP hook

`post-edit.sh` deliberately does NOT call `/hooks/post-tool-use`. A script-level
curl would race with the project's native HTTP hook (Claude Code keeps only the
*last* JSON of a hook chain, silently dropping `decision: "block"` payloads —
issue #344). Consumers wire ktn-linter directly in their `.claude/settings.json`:

```json
{
  "hooks": {
    "PostToolUse": [
      {
        "matcher": "Edit|Write|MultiEdit",
        "hooks": [
          { "type": "http", "url": "http://localhost:7717/hooks/post-tool-use", "timeout": 15 }
        ]
      }
    ]
  }
}
```

The native hook speaks the Claude Code hook payload protocol directly (decision
+ hookSpecificOutput) — no re-parse, no re-wrap. Phase scope falls through to
ktn-linter's YAML config (`.ktn-linter.yaml`); per-request override via the
project-level body is consumer-managed.

### `on-stop.sh` — Stop (*)

Before session summary, calls `/hooks/stop` for session-level validation.

- Adds `CLAUDE_SESSION_ID` to request payload
- Curl timeout: 28s (within 30s hook timeout)
- Phase scope: `structural,signatures,logic,performance,modern,style,comment,tests` (override: `KTN_STOP_PHASES`)
- Outputs summary to stderr (visible to user)
- Never blocks session stop

### Phase scope (per-request override, ktn-linter ≥ #190)

Each script-backed hook (`pre-validate.sh`, `on-stop.sh`) injects an explicit `phases` field into the JSON request body, scoped to what the event-type actually needs to surface. The server's YAML config (`.ktn-linter.yaml`) is **not** consulted when `phases` is present — it acts as a per-request override. Empty/absent `phases` → YAML default (back-compat for servers pre-#190, which ignore the unknown field).

> **Note:** PostToolUse runs through a project-level native HTTP hook (no script wrapper) and therefore **does not** inject `phases` — it forwards the request body unchanged to ktn-linter, which falls back to its YAML config.

Override per-project via env vars (comma-separated, no spaces):

```bash
export KTN_PRE_PHASES=structural,signatures,modern
export KTN_STOP_PHASES=structural,signatures,logic,performance,modern,style,comment,tests,health
```

(`KTN_POST_PHASES` is no longer applicable — PostToolUse uses the project-level
native HTTP hook, which forwards the body unchanged to ktn-linter's YAML config.)

Defensive: if `jq` is missing, the scripts fall through to the raw `${INPUT:-{}}` body — server uses YAML default, no failure.

## Hook Flow

```
Agent wants to edit file.go
        │
        ▼
┌─ PreToolUse ──────────────────────────────┐
│  pre-validate.sh    → protect files       │
│  ktn-pre-tool-use.sh → package context    │  ← "3 existing warnings in this package"
└───────────────────────────────────────────┘
        │
        ▼
    Agent edits file.go
        │
        ▼
┌─ PostToolUse ─────────────────────────────┐
│  post-edit.sh        → format file        │
│  (project-level HTTP hook → lint scan)    │  ← "ERROR: unused variable line 42"
│  log.sh (async)      → action logging     │
└───────────────────────────────────────────┘
        │
        ▼ (if blocked, agent must fix before continuing)
        │
    ... more edits ...
        │
        ▼
┌─ Stop ────────────────────────────────────┐
│  ktn-stop.sh  → session validation (30s)  │  ← "2 packages scanned, 0 violations"
│  on-stop.sh   → terminal bell + summary   │
└───────────────────────────────────────────┘
```

## Timeout Justification

| Hook | Timeout | Curl | Rationale |
|------|---------|------|-----------|
| PreToolUse | 5s | 4s (`pre-validate.sh`) | Must be fast — quick HTTP call to cached package state |
| PostToolUse | 15s | N/A — project-level native HTTP hook | Single file scan with 148 rules, must complete before agent proceeds |
| Stop | 30s | 28s (`on-stop.sh`) | Full project scan of all modified packages, runs once at session end |

## Canonical Hooks Doctrine (Future)

ktn-linter hooks are evolving toward a canonical model based on `ScanReport`:

| Concept | Description |
|---------|-------------|
| **ScanReport** | Canonical data structure for all lint results (findings, severity, phase, location) |
| **HookSummary** | Text formatter that derives hook output from ScanReport |
| **Severity-first** | PostToolUse shows critical/error first, then warnings |
| **Phase ordering** | Results grouped by lint phase (syntax → semantics → style) |
| **SessionStore** | Tracks which packages were edited during the session |
| **Stop optimization** | Only scans packages touched during the session (via SessionID) |

### Forward compatibility

The template hook calls are **endpoint-agnostic** — they forward the full hook input JSON to ktn-linter and relay the response. When ktn-linter evolves its response format (ScanReport v2, new fields), the scripts don't need to change.

### Health phase: `claude-rtk-hook` (proposed upstream)

> **Status:** local spec, not yet wired upstream. Tracking issue:
> `<TBD: open issue at kodflow/ktn-linter>` — when opened, link here and add
> `claude-rtk-hook` to `KTN_STOP_PHASES` defaults in `on-stop.sh`. Until then,
> **no template-side change** — this section documents intent only.

A new `health` phase (8) sub-rule that fires once at session end via the
`/hooks/stop` endpoint. Reports the same RTK mode/reason that
`session-init.sh probe_rtk_mode` emits at session start, so `Stop` events
double-check the doctrine after a session of edits.

**Signal source.** The rule reads `~/.claude/logs/<branch>/rtk-mode.json`
(written by `session-init.sh` and `postStart.sh init_rtk`); if absent, it
re-runs the probe logic inline. The file schema is documented as a fixture
in `tests/scripts/rtk-config-toml.bats` (runtime artifact, never committed).

**Severity.** `info` — non-blocking, matches the project doctrine that
runtime degradation is visible but never blocking. Could be elevated to
`warning` upstream once the rule is stable; controllable per-project via
`KTN_*_SEVERITY` env (existing override pattern).

**Output (proposed).**

```text
[health][claude-rtk-hook] mode=enforcing version=0.38.0
[health][claude-rtk-hook] mode=advisory  reason=session-bypass
[health][claude-rtk-hook] mode=degraded  reason=no-binary
                          fix=`rtk init -g --auto-patch` (re-applies wiring)
[health][claude-rtk-hook] mode=degraded  reason=config-invalid
                          fix=check ~/.config/rtk/config.toml schema
[health][claude-rtk-hook] mode=degraded  reason=marker-missing
                          fix=`rtk init -g --auto-patch` (re-creates RTK.md/@import)
```

**Why it matters at session end.** A degraded mode mid-session means every
`rtk discover` entry from that session is a savings miss — visible in /audit
but easy to miss across many short sessions. The `Stop` summary surfaces it
when the operator is most likely to act.

## MCP Server Registration

```
Build time:
  .devcontainer/features/languages/go/install.sh
    → downloads ktn-linter binary
    → writes /etc/mcp/features/go.mcp.json

Runtime (every container start):
  postStart.sh → step_mcp_configuration()
    → checks: command -v ktn-linter (requires_binary gate)
    → if found: merges into /workspace/mcp.json
    → if not found: silently skips
```

**Port:** 7717 (configurable via `KTN_LINTER_PORT` env var in hook scripts).

## Idempotence Guarantees

| Scenario | Behavior |
|----------|----------|
| Container rebuild (×10) | Scripts restored from image defaults, ktn-linter calls always present |
| ktn-linter not installed | curl fails silently, rest of hook script runs normally |
| ktn-linter not running | curl connection refused, rest of hook script runs normally |
| ktn-linter running | Full lint integration active |
| User has custom hooks in settings.local.json | Template hooks preserved, local overrides apply |

## Health Check

```bash
# 1. Binary installed?
which ktn-linter && ktn-linter --version

# 2. MCP server registered?
jq '.mcpServers["ktn-linter"]' /workspace/mcp.json

# 3. Hook scripts have ktn-linter integration?
grep -l "ktn-linter" ~/.claude/scripts/{pre-validate,on-stop}.sh
# 3b. PostToolUse wired at project level? (guard on hook type and optional .url
# so a sibling type:"command" hook in the same array doesn't error the filter)
jq '.hooks.PostToolUse[]?.hooks[]?
    | select(.type == "http" and (.url? | test("ktn|7717")))' .claude/settings.json

# 4. Server responding?
curl -sf http://localhost:7717/health && echo "OK" || echo "Not running"
```

## Override & Disable

### Change port

Set `KTN_LINTER_PORT` environment variable (default: 7717). Read by the template scripts that still call ktn-linter directly (`pre-validate.sh`, `on-stop.sh`). PostToolUse runs through the project-level native HTTP hook, where the URL is hard-coded in `.claude/settings.json` — adjust the URL there if you change the port.

### Disable ktn-linter hooks only

Set the environment variable to a non-listening port:

```bash
export KTN_LINTER_PORT=0
```

Or edit the scripts to remove the ktn-linter sections (they will be restored on next container start).

### Disable all hooks for an event

Override in `settings.local.json`:
```json
{ "hooks": { "PostToolUse": [] } }
```

**Warning:** This disables ALL PostToolUse hooks, including formatting.

## Troubleshooting

| Symptom | Cause | Fix |
|---------|-------|-----|
| No lint feedback on edits | ktn-linter not running | Start with `ktn-linter serve` |
| `/lint` command fails | Binary not installed | Enable Go feature, rebuild container |
| MCP server missing from mcp.json | Binary not in PATH | Check `which ktn-linter` |
| Port conflict on 7717 | Another service using port | Set `KTN_LINTER_PORT=7718` |
| PostToolUse timeout | Large file or slow scan | Check ktn-linter logs, consider timeout increase |
| Hook scripts missing ktn-linter calls | Old version of scripts | Rebuild container or run `/update` |
